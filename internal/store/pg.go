// Package store provides the PostgreSQL implementation of api.Store.
//
// This file holds the PG core (pool, migrations) plus helpers shared
// across entities (cursor codec, scan/marshal utilities, error
// classification). Entity CRUD lives in per-entity pg_*.go files
// (pg_clusters.go, pg_nodes.go, pg_namespaces.go, pg_pods.go,
// pg_workloads.go, pg_services.go, pg_ingresses.go,
// pg_persistent_volumes.go, pg_persistent_volume_claims.go,
// pg_settings.go) alongside the pre-existing domain files
// (pg_auth.go, pg_applications.go, pg_cloud_accounts.go, …).
package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/secrets"
	"github.com/sthalbert/longue-vue/migrations"
)

// change type constants for time-travel history capture.
const (
	changeTypeCreate     = "create"
	changeTypeUpdate     = "update"
	changeTypeRestore    = "restore"
	changeTypeSoftDelete = "soft_delete"
)

// PG is a PostgreSQL-backed implementation of api.Store.
type PG struct {
	pool          *pgxpool.Pool
	revokedTokens chan string
	encrypter     *secrets.Encrypter
}

// SetEncrypter wires the AES-GCM encrypter used by encrypted-secret tables
// (currently image_versions_registries auth tokens). Optional — methods
// that require it return an explicit error when it is unset.
func (p *PG) SetEncrypter(enc *secrets.Encrypter) {
	p.encrypter = enc
}

// Encrypter returns the wired encrypter, or nil. Test-only helper.
func (p *PG) Encrypter() *secrets.Encrypter { return p.encrypter }

// Pool returns the underlying pool. Test-only helper.
func (p *PG) Pool() *pgxpool.Pool { return p.pool }

// Open connects to PostgreSQL via the given DSN and verifies the connection.
func Open(ctx context.Context, dsn string) (*PG, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &PG{pool: pool, revokedTokens: make(chan string, 64)}, nil
}

// RevocationChan returns a channel that emits the prefix of each token that is
// revoked via RevokeAPIToken. The channel is buffered (cap 64); sends are
// non-blocking so a slow consumer never stalls token revocation.
func (p *PG) RevocationChan() <-chan string {
	return p.revokedTokens
}

// Close releases the connection pool.
func (p *PG) Close() {
	p.pool.Close()
}

// Ping checks the database is reachable.
func (p *PG) Ping(ctx context.Context) error {
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	return nil
}

// Migrate applies every pending migration embedded in the migrations package.
func (p *PG) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}

	db := stdlib.OpenDBFromPool(p.pool)
	defer func() { _ = db.Close() }()

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// withTx runs fn inside a transaction: rollback on error or panic,
// commit when fn returns nil. op names the operation in the begin/commit
// error wrapping (e.g. "update cluster").
func (p *PG) withTx(ctx context.Context, op string, fn func(tx pgx.Tx) error) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin %s: %w", op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s: %w", op, err)
	}
	return nil
}

// classifyOutcome maps the CTE's (inserted, business_changed) tuple to the
// corresponding api.UpsertOutcome — used by every Upsert* impl that computes
// audit-noop detection. See ADR-0024.
func classifyOutcome(inserted, businessChanged bool) api.UpsertOutcome {
	switch {
	case inserted:
		return api.OutcomeInserted
	case businessChanged:
		return api.OutcomeBusinessChanged
	default:
		return api.OutcomeNoChange
	}
}

// scanRowWith wraps a pgx.Row so a scan* helper can be reused while the
// caller threads extra destinations (e.g. the audit CTE's inserted /
// business_changed bool tail) onto the same row.Scan call.
type scanRowWith struct {
	row   pgx.Row
	extra []any
}

// Scan forwards to the wrapped row, appending the extra destinations after
// the caller's so a single Scan call can hydrate both the normal columns
// and the CTE outcome bools.
func (r scanRowWith) Scan(dest ...any) error {
	if err := r.row.Scan(append(dest, r.extra...)...); err != nil {
		return fmt.Errorf("scan row: %w", err)
	}
	return nil
}

func scanUUIDs(rows pgx.Rows) ([]uuid.UUID, error) {
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan uuid: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate uuids: %w", err)
	}
	return ids, nil
}

func marshalPorts(ports *[]map[string]interface{}) ([]byte, error) {
	if ports == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(*ports)
	if err != nil {
		return nil, fmt.Errorf("marshal ports: %w", err)
	}
	return b, nil
}

// unmarshalContainers decodes a JSONB array into the shared ContainerList
// type. Returns nil when the column is empty or contains an empty array.
//
//nolint:nilnil // nil, nil is the intentional "no data" signal for optional JSONB columns
func unmarshalContainers(b []byte) (*api.ContainerList, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var cs api.ContainerList
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("unmarshal containers: %w", err)
	}
	if len(cs) == 0 {
		return nil, nil
	}
	return &cs, nil
}

// unmarshalMapArray decodes a JSONB array of objects. Returns nil for
// empty arrays so the pointer semantics match what callers expect
// (nil = absent, &[...] = present).
//
//nolint:nilnil // nil, nil is the intentional "no data" signal for optional JSONB columns
func unmarshalMapArray(b []byte) (*[]map[string]interface{}, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("unmarshal map array: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &out, nil
}

// marshalLabels encodes the optional labels map as JSON, preserving NULL-vs-empty semantics.
func marshalLabels(labels *map[string]string) ([]byte, error) { //nolint:gocritic // ptrToRefParam: callers pass *map from generated API types
	if labels == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*labels) //nolint:errchkjson // map[string]string is unconditionally JSON-safe
	if err != nil {
		return nil, fmt.Errorf("marshal labels: %w", err)
	}
	return b, nil
}

func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// encodeCursor mints the legacy positional cursor ("<RFC3339Nano>|<uuid>")
// still used by pods, nodes, namespaces, services, PVs, auth, and audit
// sort tests as a "legacy cursor rejection" fixture. Production code uses
// encodeListCursor exclusively.
func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// listCursor is the tagged, versioned pagination cursor (ADR-0042).
// It replaces the positional "<RFC3339Nano>|<uuid>" format: it names
// the sort column and direction it was minted under, so a cursor
// replayed with different sort parameters is rejected instead of
// silently mis-paginating. Val is nil while paginating inside the
// NULLS LAST region of a nullable sort column.
type listCursor struct {
	V   int       `json:"v"`
	Col string    `json:"col"`
	Val *string   `json:"val"`
	ID  uuid.UUID `json:"id"`
	Dir string    `json:"dir"`
}

// encodeListCursor mints the opaque cursor for the row whose sort-column
// value is val (already serialized: lowercased text, or RFC3339Nano time).
func encodeListCursor(col string, val *string, id uuid.UUID, dir string) string {
	raw, err := json.Marshal(listCursor{V: 1, Col: col, Val: val, ID: id, Dir: dir})
	if err != nil {
		// Unreachable: every listCursor field marshals without error
		// (uuid.UUID's MarshalText never fails). An empty cursor merely
		// ends pagination early rather than corrupting a page.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeListCursor validates shape, version, and that the cursor was
// minted under the same resolved (col, dir) as the current request.
// All failures map to api.ErrInvalidCursor so handlers can return 400.
func decodeListCursor(cursor, wantCol, wantDir string) (*string, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("%w: %v", api.ErrInvalidCursor, err)
	}
	var c listCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, uuid.Nil, fmt.Errorf("%w: %v", api.ErrInvalidCursor, err)
	}
	if c.V != 1 || c.ID == uuid.Nil {
		return nil, uuid.Nil, api.ErrInvalidCursor
	}
	if c.Col != wantCol || c.Dir != wantDir {
		return nil, uuid.Nil, fmt.Errorf("%w: cursor sort mismatch", api.ErrInvalidCursor)
	}
	return c.Val, c.ID, nil
}

// sortKind tells the pagination helpers how to serialize/parse a sort
// column's cursor value.
type sortKind int

const (
	sortTime sortKind = iota
	sortText
	sortInt
)

// dirAsc / dirDesc are the two order= directions accepted by list
// endpoints and recorded in cursors.
const (
	dirAsc  = "asc"
	dirDesc = "desc"
)

// sortColumn describes one sortable column of a paginated list query.
// expr is a trusted SQL expression (a package constant — never derived
// from user input). nullable columns sort NULLS LAST and get a
// null-aware keyset predicate.
type sortColumn struct {
	expr     string
	kind     sortKind
	nullable bool
}

// sortSpec is a per-entity allowlist of sortable columns. defaultKey
// names the column used when the request carries no sort parameter,
// preserving the entity's historical implicit order. defaultDir sets
// that implicit order's direction; empty means "desc", which fits the
// timestamp defaultKeys most entities use — name-keyed entities set
// "asc" so an unsorted list reads A→Z.
type sortSpec struct {
	columns    map[string]sortColumn
	defaultKey string
	defaultDir string
}

// resolve validates page.Sort/page.Order against the allowlist.
// "" Sort → (defaultKey, defaultDir): unsorted requests keep the
// entity's implicit order. "" Order with an explicit Sort → "asc".
func (s sortSpec) resolve(page api.ListPage) (key string, col sortColumn, dir string, err error) {
	key = page.Sort
	dir = page.Order
	if key == "" {
		key = s.defaultKey
		// order= is documented as ignored when sort= is absent — the
		// entity's implicit order always applies. This also
		// deliberately skips order validation: ?order=garbage without
		// sort= is ignored, not a 400.
		dir = s.defaultDir
		if dir == "" {
			dir = dirDesc
		}
	} else if dir == "" {
		dir = dirAsc
	}
	if dir != dirAsc && dir != dirDesc {
		return "", sortColumn{}, "", fmt.Errorf("%w: order %q", api.ErrInvalidSort, page.Order)
	}
	col, ok := s.columns[key]
	if !ok {
		return "", sortColumn{}, "", fmt.Errorf("%w: sort key %q", api.ErrInvalidSort, page.Sort)
	}
	return key, col, dir, nil
}

// orderBy renders the ORDER BY clause for a resolved sort. idExpr is
// the entity's id column (tiebreaker), e.g. "n.id".
func orderBy(col sortColumn, idExpr, dir string) string {
	d := strings.ToUpper(dir)
	if col.nullable {
		return fmt.Sprintf("ORDER BY %s %s NULLS LAST, %s %s", col.expr, d, idExpr, d)
	}
	return fmt.Sprintf("ORDER BY %s %s, %s %s", col.expr, d, idExpr, d)
}

// keysetCond appends the after-cursor predicate to conds/args. val is
// the cursor's serialized sort value (nil = the cursor row sat in the
// NULLS LAST region). Placeholders are numbered $len(args) after each
// append, matching the package-wide convention.
func keysetCond(col sortColumn, idExpr, dir string, val *string, id uuid.UUID, conds *[]string, args *[]any) error {
	op := ">"
	if dir == dirDesc {
		op = "<"
	}
	if val == nil {
		if !col.nullable {
			return fmt.Errorf("%w: null value for non-nullable sort column", api.ErrInvalidCursor)
		}
		*args = append(*args, id)
		*conds = append(*conds, fmt.Sprintf("(%s IS NULL AND %s %s $%d)", col.expr, idExpr, op, len(*args)))
		return nil
	}
	var arg any
	switch col.kind {
	case sortTime:
		ts, err := time.Parse(time.RFC3339Nano, *val)
		if err != nil {
			return fmt.Errorf("%w: cursor timestamp: %v", api.ErrInvalidCursor, err)
		}
		arg = ts
	case sortText:
		arg = *val
	case sortInt:
		n, err := strconv.Atoi(*val)
		if err != nil {
			return fmt.Errorf("%w: cursor int: %v", api.ErrInvalidCursor, err)
		}
		arg = n
	}
	*args = append(*args, arg)
	vIdx := len(*args)
	*args = append(*args, id)
	idIdx := len(*args)
	if col.nullable {
		// NULLS LAST: rows after the cursor are strictly beyond the
		// value, tied-on-value with a later id, or inside the NULL tail.
		*conds = append(*conds, fmt.Sprintf(
			"(%s %s $%d OR (%s = $%d AND %s %s $%d) OR %s IS NULL)",
			col.expr, op, vIdx, col.expr, vIdx, idExpr, op, idIdx, col.expr,
		))
		return nil
	}
	*conds = append(*conds, fmt.Sprintf("(%s, %s) %s ($%d, $%d)", col.expr, idExpr, op, vIdx, idIdx))
	return nil
}

// nilUUIDDisplay renders an absent optional UUID FK in the
// classify*FKError messages.
const nilUUIDDisplay = "<nil>"

// clampLimit applies the package-wide limit defaults (default 50,
// entity-specific hard cap).
func clampLimit(limit, maxLimit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

// sortValText / sortValTime serialize an item's sort-column field for
// cursor minting. Text values are lowercased to match the LOWER(...)
// sort expressions (Go and Postgres agree on lowercasing for UTF-8
// locales; a mismatch would only shift a page boundary, not corrupt
// results).
func sortValText(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.ToLower(*s)
	return &v
}

func sortValTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.UTC().Format(time.RFC3339Nano)
	return &v
}

func sortValInt(i *int) *string {
	if i == nil {
		return nil
	}
	v := strconv.Itoa(*i)
	return &v
}

func intPtr(v int) *int { return &v }

func boolToIntPtr(b *bool) *int {
	if b == nil {
		return nil
	}
	if *b {
		return intPtr(1)
	}
	return intPtr(0)
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505). Insert/update paths use it to map the error
// to api.ErrConflict with an entity-specific message.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// escapeLike escapes LIKE/ILIKE metacharacters in s (backslash, percent,
// underscore) so callers can safely build `%<user-input>%` patterns. The
// surrounding SQL must use `ESCAPE '\'` to honor the backslash escape.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// namePattern builds the LIKE pattern for the uniform `name=` filter
// (spec 2026-07-10). The term is lowercased and LIKE-escaped; each `*`
// then becomes a `%` wildcard. A term without `*` is wrapped in `%…%`
// (substring semantics); a term with `*` is used as an anchored glob
// (`du*` = starts-with, `*du` = ends-with). The surrounding SQL must
// compare against LOWER(col) and carry `ESCAPE '\'`.
func namePattern(term string) string {
	escaped := escapeLike(strings.ToLower(term))
	if !strings.Contains(term, "*") {
		return "%" + escaped + "%"
	}
	return strings.ReplaceAll(escaped, "*", "%")
}
