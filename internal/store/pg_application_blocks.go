package store

// PostgreSQL implementation of the application_blocks methods on
// api.Store (ADR-0029 §1, §2.2). Operator-curated; collectors never
// write here. Replaces the Task 1.5 stubs in pg_applications.go.
//
// Follows the canonical pattern in pg_cloud_accounts.go: column constant +
// scan helper, sqlc-free direct pgx queries, pgx.ErrNoRows ⇒ ErrNotFound,
// 23505 unique-violation ⇒ ErrConflict, opaque (created_at, id) keyset
// cursor matching encodeCursor / decodeCursor (pg.go).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

// errApplicationBlockNameRequired is returned when CreateApplicationBlock
// is called with an empty or whitespace-only name (normalisation collapses
// to ""). Static sentinel so err113 doesn't trip on ad-hoc fmt.Errorf.
var errApplicationBlockNameRequired = errors.New("application_block: name required")

const applicationBlockColumns = `id, name, display_name, description,
	owner, notes, annotations, created_at, updated_at`

// CreateApplicationBlock inserts a new application_blocks row. The name
// is normalised (NormalizeApplicationName ⇒ kebab-case) before insert so
// the UNIQUE (name) index treats "Office Apps" and "office-apps" as the
// same key. Empty / whitespace-only names are rejected
// (errApplicationBlockNameRequired); the API layer surfaces 400 and the
// UNIQUE violation surfaces as ErrConflict.
func (p *PG) CreateApplicationBlock(ctx context.Context, in api.ApplicationBlockCreate) (api.ApplicationBlock, error) {
	name := api.NormalizeApplicationName(in.Name)
	if name == "" {
		return api.ApplicationBlock{}, errApplicationBlockNameRequired
	}

	annotations, err := marshalLabels(in.Annotations)
	if err != nil {
		return api.ApplicationBlock{}, fmt.Errorf("marshal application_block annotations: %w", err)
	}

	id := uuid.New()
	now := time.Now().UTC()

	q := `
		INSERT INTO application_blocks (
			id, name, display_name, description,
			owner, notes, annotations, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		RETURNING ` + applicationBlockColumns
	row := p.pool.QueryRow(ctx, q,
		id, name, in.DisplayName, in.Description,
		in.Owner, in.Notes, annotations, now,
	)
	block, err := scanApplicationBlock(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.ApplicationBlock{}, fmt.Errorf("application_block name %q already exists: %w", name, api.ErrConflict)
		}
		return api.ApplicationBlock{}, fmt.Errorf("insert application_block: %w", err)
	}
	return block, nil
}

// GetApplicationBlock fetches by id.
func (p *PG) GetApplicationBlock(ctx context.Context, id uuid.UUID) (api.ApplicationBlock, error) {
	q := `SELECT ` + applicationBlockColumns + ` FROM application_blocks WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	block, err := scanApplicationBlock(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.ApplicationBlock{}, api.ErrNotFound
		}
		return api.ApplicationBlock{}, fmt.Errorf("select application_block: %w", err)
	}
	return block, nil
}

// GetApplicationBlockByName fetches by name; the input is normalised so
// operator-typed "Office Apps" and the stored "office-apps" both match.
func (p *PG) GetApplicationBlockByName(ctx context.Context, name string) (api.ApplicationBlock, error) {
	norm := api.NormalizeApplicationName(name)
	if norm == "" {
		return api.ApplicationBlock{}, api.ErrNotFound
	}
	q := `SELECT ` + applicationBlockColumns + ` FROM application_blocks WHERE name = $1`
	row := p.pool.QueryRow(ctx, q, norm)
	block, err := scanApplicationBlock(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.ApplicationBlock{}, api.ErrNotFound
		}
		return api.ApplicationBlock{}, fmt.Errorf("select application_block by name: %w", err)
	}
	return block, nil
}

// ListApplicationBlocks returns paged rows ordered by (created_at DESC,
// id DESC). The opaque cursor matches the codebase encoding (base64 of
// "RFC3339Nano|uuid"). Filters: name → case-insensitive substring on
// LOWER(name) with LIKE-escaped pattern; owner → exact match.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListApplicationBlocks(
	ctx context.Context,
	filter api.ApplicationBlockListFilter,
	limit int,
	cursor string,
) ([]api.ApplicationBlock, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(applicationBlockColumns)
	sb.WriteString(` FROM application_blocks`)

	args := make([]any, 0, 4)
	conds := make([]string, 0, 3)

	if filter.Name != nil {
		args = append(args, escapeLike(strings.ToLower(*filter.Name)))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE '%%' || $%d || '%%' ESCAPE '\\'", len(args)))
	}
	if filter.Owner != nil {
		args = append(args, *filter.Owner)
		conds = append(conds, fmt.Sprintf("owner = $%d", len(args)))
	}
	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		conds = append(conds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query application_blocks: %w", err)
	}
	defer rows.Close()

	items := make([]api.ApplicationBlock, 0, limit)
	for rows.Next() {
		block, err := scanApplicationBlock(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan application_block: %w", err)
		}
		items = append(items, block)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate application_blocks: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateApplicationBlock applies merge-patch on the mutable fields.
// Name is intentionally NOT mutable via this path (ADR-0029 §2.2): a
// rename would orphan inbound application_block_id links from the human
// operator's POV. updated_at is always bumped when at least one field
// is being patched; an all-nil patch is a no-op that returns the
// current row.
func (p *PG) UpdateApplicationBlock(ctx context.Context, id uuid.UUID, in api.ApplicationBlockPatch) (api.ApplicationBlock, error) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Description != nil {
		appendSet("description", *in.Description)
	}
	if in.Owner != nil {
		appendSet("owner", *in.Owner)
	}
	if in.Notes != nil {
		appendSet("notes", *in.Notes)
	}
	if in.Annotations != nil {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.ApplicationBlock{}, fmt.Errorf("marshal application_block annotations: %w", err)
		}
		appendSet("annotations", b)
	}

	if len(sets) == 0 {
		// All-nil patch → no DB write, but still surface a 404 if the row
		// is missing (matches GetApplicationBlock).
		return p.GetApplicationBlock(ctx, id)
	}

	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)
	q := fmt.Sprintf("UPDATE application_blocks SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)

	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.ApplicationBlock{}, fmt.Errorf("update application_block: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ApplicationBlock{}, api.ErrNotFound
	}
	return p.GetApplicationBlock(ctx, id)
}

// DeleteApplicationBlock removes the row. Migration 00046 ON DELETE
// SET NULL on applications.application_block_id means dependent
// applications survive the delete (just become un-blocked).
func (p *PG) DeleteApplicationBlock(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM application_blocks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete application_block: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// scanApplicationBlock matches applicationBlockColumns. Annotations is
// always populated as a non-nil map (possibly empty) because the
// ApplicationBlock json contract has no `omitempty`.
func scanApplicationBlock(row pgx.Row) (api.ApplicationBlock, error) {
	var (
		out             api.ApplicationBlock
		displayName     sql.NullString
		description     sql.NullString
		owner           sql.NullString
		notes           sql.NullString
		annotationsJSON []byte
	)
	if err := row.Scan(
		&out.ID, &out.Name, &displayName, &description,
		&owner, &notes, &annotationsJSON,
		&out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return api.ApplicationBlock{}, fmt.Errorf("scan application_block: %w", err)
	}
	out.DisplayName = nullableString(displayName)
	out.Description = nullableString(description)
	out.Owner = nullableString(owner)
	out.Notes = nullableString(notes)

	anns := map[string]string{}
	if len(annotationsJSON) > 0 {
		if err := json.Unmarshal(annotationsJSON, &anns); err != nil {
			return api.ApplicationBlock{}, fmt.Errorf("unmarshal application_block annotations: %w", err)
		}
	}
	out.Annotations = anns
	return out, nil
}
