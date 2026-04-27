package store

// PostgreSQL implementation of the cloud_accounts methods on api.Store
// (ADR-0015). The SK ciphertext lives in the same row as the AK
// plaintext; the AAD on AES-256-GCM is bound to the row id so a
// database backup-restore cannot move a SK between accounts.

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

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/secrets"
)

const cloudAccountColumns = `id, provider, name, region, status,
	access_key, last_seen_at, last_error, last_error_at,
	owner, criticality, notes, runbook_url, annotations,
	created_at, updated_at, disabled_at`

// UpsertCloudAccount idempotently registers a cloud account by
// (provider, name). New rows are created in pending_credentials.
func (p *PG) UpsertCloudAccount(ctx context.Context, in api.CloudAccountUpsert) (api.CloudAccount, error) {
	id := uuid.New()
	now := time.Now().UTC()
	const q = `
		INSERT INTO cloud_accounts (
			id, provider, name, region, status,
			annotations, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'pending_credentials',
			'{}'::jsonb, $5, $5)
		ON CONFLICT (provider, name) DO UPDATE
		SET region = EXCLUDED.region,
		    updated_at = EXCLUDED.updated_at
		RETURNING ` + cloudAccountColumns
	row := p.pool.QueryRow(ctx, q, id, in.Provider, in.Name, in.Region, now)
	acct, err := scanCloudAccount(row)
	if err != nil {
		return api.CloudAccount{}, fmt.Errorf("upsert cloud account: %w", err)
	}
	return acct, nil
}

// GetCloudAccount fetches by id.
func (p *PG) GetCloudAccount(ctx context.Context, id uuid.UUID) (api.CloudAccount, error) {
	q := `SELECT ` + cloudAccountColumns + ` FROM cloud_accounts WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	acct, err := scanCloudAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.CloudAccount{}, api.ErrNotFound
		}
		return api.CloudAccount{}, fmt.Errorf("select cloud account: %w", err)
	}
	return acct, nil
}

// GetCloudAccountByName fetches by (provider, name).
func (p *PG) GetCloudAccountByName(ctx context.Context, provider, name string) (api.CloudAccount, error) {
	q := `SELECT ` + cloudAccountColumns + ` FROM cloud_accounts WHERE provider = $1 AND name = $2`
	row := p.pool.QueryRow(ctx, q, provider, name)
	acct, err := scanCloudAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.CloudAccount{}, api.ErrNotFound
		}
		return api.CloudAccount{}, fmt.Errorf("select cloud account by name: %w", err)
	}
	return acct, nil
}

// GetCloudAccountByNameAny fetches by name across every provider in
// one query. Used by the credentials-fetch handler so a vm-collector
// PAT lookup doesn't fan out to one SQL round-trip per supported
// provider. (provider, name) is UNIQUE; if two providers happen to
// share the same name the row with the most recent created_at wins —
// the binding check downstream will reject if it's not the one the
// caller is bound to. Returns ErrNotFound when no row matches.
func (p *PG) GetCloudAccountByNameAny(ctx context.Context, name string) (api.CloudAccount, error) {
	q := `SELECT ` + cloudAccountColumns + ` FROM cloud_accounts WHERE name = $1 ORDER BY created_at DESC, id DESC LIMIT 1`
	row := p.pool.QueryRow(ctx, q, name)
	acct, err := scanCloudAccount(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.CloudAccount{}, api.ErrNotFound
		}
		return api.CloudAccount{}, fmt.Errorf("select cloud account by name (any provider): %w", err)
	}
	return acct, nil
}

// ListCloudAccounts returns paged accounts (created_at DESC, id DESC).
func (p *PG) ListCloudAccounts(ctx context.Context, limit int, cursor string) ([]api.CloudAccount, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(cloudAccountColumns)
	sb.WriteString(` FROM cloud_accounts`)
	args := make([]any, 0, 3)
	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		fmt.Fprintf(&sb, " WHERE (created_at, id) < ($%d, $%d)", tsIdx, idIdx)
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query cloud accounts: %w", err)
	}
	defer rows.Close()

	items := make([]api.CloudAccount, 0, limit)
	for rows.Next() {
		acct, err := scanCloudAccount(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan cloud account: %w", err)
		}
		items = append(items, acct)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate cloud accounts: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateCloudAccount applies merge-patch on curated metadata + name +
// region. Status transitions are constrained: rejects to/from `disabled`
// and `pending_credentials`.
//
//nolint:gocyclo,gocritic // merge-patch nil checks are repetitive; hugeParam: Store interface requires value param
func (p *PG) UpdateCloudAccount(ctx context.Context, id uuid.UUID, in api.CloudAccountPatch) (api.CloudAccount, error) {
	sets := make([]string, 0, 12)
	args := make([]any, 0, 14)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Name != nil {
		appendSet("name", *in.Name)
	}
	if in.Region != nil {
		appendSet("region", *in.Region)
	}
	if in.Owner != nil {
		appendSet("owner", *in.Owner)
	}
	if in.Criticality != nil {
		appendSet("criticality", *in.Criticality)
	}
	if in.Notes != nil {
		appendSet("notes", *in.Notes)
	}
	if in.RunbookURL != nil {
		appendSet("runbook_url", *in.RunbookURL)
	}
	if in.Annotations != nil {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.CloudAccount{}, fmt.Errorf("marshal cloud account annotations: %w", err)
		}
		appendSet("annotations", b)
	}
	if in.Status != nil {
		switch *in.Status {
		case api.CloudAccountStatusActive, api.CloudAccountStatusError:
			appendSet("status", *in.Status)
		default:
			return api.CloudAccount{}, fmt.Errorf("status %q not allowed via UpdateCloudAccount: %w", *in.Status, api.ErrConflict)
		}
	}
	if in.LastSeenAt != nil {
		appendSet("last_seen_at", *in.LastSeenAt)
	}
	if in.LastError != nil {
		appendSet("last_error", *in.LastError)
	}
	if in.LastErrorAt != nil {
		appendSet("last_error_at", *in.LastErrorAt)
	}

	if len(sets) == 0 {
		// nothing to do — just return current state.
		return p.GetCloudAccount(ctx, id)
	}

	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)
	q := fmt.Sprintf("UPDATE cloud_accounts SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)

	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.CloudAccount{}, fmt.Errorf("cloud account name already exists: %w", api.ErrConflict)
		}
		return api.CloudAccount{}, fmt.Errorf("update cloud account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.CloudAccount{}, api.ErrNotFound
	}
	return p.GetCloudAccount(ctx, id)
}

// SetCloudAccountCredentials writes AK plaintext and SK ciphertext+nonce+kid,
// transitions the row to status='active'.
func (p *PG) SetCloudAccountCredentials(ctx context.Context, id uuid.UUID, accessKey string, encSK secrets.Ciphertext) (api.CloudAccount, error) {
	now := time.Now().UTC()
	const q = `
		UPDATE cloud_accounts
		SET access_key = $1,
		    secret_key_encrypted = $2,
		    secret_key_nonce = $3,
		    secret_key_kid = $4,
		    status = 'active',
		    updated_at = $5
		WHERE id = $6
	`
	tag, err := p.pool.Exec(ctx, q, accessKey, encSK.Bytes, encSK.Nonce, encSK.KID, now, id)
	if err != nil {
		return api.CloudAccount{}, fmt.Errorf("set cloud account credentials: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.CloudAccount{}, api.ErrNotFound
	}
	return p.GetCloudAccount(ctx, id)
}

// GetCloudAccountCredentials returns AK + SK ciphertext+nonce+kid.
// 404-equivalent (ErrNotFound) when status='pending_credentials' or
// the row is absent. ErrConflict when status='disabled'.
func (p *PG) GetCloudAccountCredentials(ctx context.Context, id uuid.UUID) (string, secrets.Ciphertext, error) {
	const q = `SELECT status, access_key, secret_key_encrypted, secret_key_nonce, secret_key_kid
	           FROM cloud_accounts WHERE id = $1`
	var (
		status  string
		ak      sql.NullString
		ctBytes []byte
		nonce   []byte
		kid     sql.NullString
	)
	err := p.pool.QueryRow(ctx, q, id).Scan(&status, &ak, &ctBytes, &nonce, &kid)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", secrets.Ciphertext{}, api.ErrNotFound
		}
		return "", secrets.Ciphertext{}, fmt.Errorf("select cloud account credentials: %w", err)
	}
	switch status {
	case api.CloudAccountStatusDisabled:
		return "", secrets.Ciphertext{}, api.ErrConflict
	case api.CloudAccountStatusPendingCredentials:
		return "", secrets.Ciphertext{}, api.ErrNotFound
	}
	if !ak.Valid || ctBytes == nil || nonce == nil || !kid.Valid {
		// Defensive: status is supposed to be `pending_credentials`
		// in this case, but treat missing fields the same way.
		return "", secrets.Ciphertext{}, api.ErrNotFound
	}
	return ak.String, secrets.Ciphertext{Bytes: ctBytes, Nonce: nonce, KID: kid.String}, nil
}

// UpdateCloudAccountStatus is the collector heartbeat path. Only allows
// transitions between `active` and `error`. Rejects to/from `disabled`
// and `pending_credentials`.
func (p *PG) UpdateCloudAccountStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastError *string) error {
	switch status {
	case "":
		// no status change requested; just write heartbeat fields.
	case api.CloudAccountStatusActive, api.CloudAccountStatusError:
		// allowed.
	default:
		return fmt.Errorf("status %q not allowed via UpdateCloudAccountStatus: %w", status, api.ErrConflict)
	}

	now := time.Now().UTC()
	sets := []string{"updated_at = $1"}
	args := []any{now}
	idx := 2
	appendSet := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s = $%d", col, idx))
		args = append(args, val)
		idx++
	}
	if status != "" {
		appendSet("status", status)
	}
	if lastSeenAt != nil {
		appendSet("last_seen_at", *lastSeenAt)
	}
	if lastError != nil {
		appendSet("last_error", *lastError)
		appendSet("last_error_at", now)
	}

	args = append(args, id)
	// Exclude rows in the protected statuses to keep the heartbeat path
	// from accidentally rescuing or burying a disabled / pending row.
	q := fmt.Sprintf(
		"UPDATE cloud_accounts SET %s WHERE id = $%d AND status NOT IN ('disabled', 'pending_credentials')",
		strings.Join(sets, ", "), idx,
	)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("update cloud account status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the row is missing or it's in a protected status; check.
		var st sql.NullString
		if err := p.pool.QueryRow(ctx, "SELECT status FROM cloud_accounts WHERE id = $1", id).Scan(&st); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return api.ErrNotFound
			}
			return fmt.Errorf("recheck cloud account status: %w", err)
		}
		return api.ErrConflict
	}
	return nil
}

// DisableCloudAccount sets disabled_at and status='disabled'.
func (p *PG) DisableCloudAccount(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	tag, err := p.pool.Exec(ctx,
		`UPDATE cloud_accounts SET status = 'disabled', disabled_at = $1, updated_at = $1 WHERE id = $2`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("disable cloud account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// EnableCloudAccount clears disabled_at; resets status based on whether
// credentials are present.
func (p *PG) EnableCloudAccount(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	const q = `
		UPDATE cloud_accounts
		SET status = CASE WHEN access_key IS NULL THEN 'pending_credentials' ELSE 'active' END,
		    disabled_at = NULL,
		    updated_at = $1
		WHERE id = $2
	`
	tag, err := p.pool.Exec(ctx, q, now, id)
	if err != nil {
		return fmt.Errorf("enable cloud account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// DeleteCloudAccount removes the account; cascades to virtual_machines
// and to bound api_tokens via the FK ON DELETE CASCADE.
func (p *PG) DeleteCloudAccount(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM cloud_accounts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete cloud account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// CountCloudAccountsWithSecrets returns the count of accounts that
// have a stored encrypted SK. Used at startup to decide whether
// missing master-key configuration is fatal.
func (p *PG) CountCloudAccountsWithSecrets(ctx context.Context) (int, error) {
	var n int
	if err := p.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cloud_accounts WHERE secret_key_encrypted IS NOT NULL`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count cloud accounts with secrets: %w", err)
	}
	return n, nil
}

func scanCloudAccount(row pgx.Row) (api.CloudAccount, error) {
	var (
		out             api.CloudAccount
		ak              sql.NullString
		lastSeenAt      *time.Time
		lastError       sql.NullString
		lastErrorAt     *time.Time
		owner           sql.NullString
		criticality     sql.NullString
		notes           sql.NullString
		runbookURL      sql.NullString
		annotationsJSON []byte
		disabledAt      *time.Time
	)
	if err := row.Scan(
		&out.ID, &out.Provider, &out.Name, &out.Region, &out.Status,
		&ak, &lastSeenAt, &lastError, &lastErrorAt,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON,
		&out.CreatedAt, &out.UpdatedAt, &disabledAt,
	); err != nil {
		return api.CloudAccount{}, fmt.Errorf("scan cloud account: %w", err)
	}
	if ak.Valid {
		s := ak.String
		out.AccessKey = &s
	}
	out.LastSeenAt = lastSeenAt
	if lastError.Valid {
		s := lastError.String
		out.LastError = &s
	}
	out.LastErrorAt = lastErrorAt
	out.Owner = nullableString(owner)
	out.Criticality = nullableString(criticality)
	out.Notes = nullableString(notes)
	out.RunbookURL = nullableString(runbookURL)
	if len(annotationsJSON) > 0 {
		var anns map[string]string
		if err := json.Unmarshal(annotationsJSON, &anns); err != nil {
			return api.CloudAccount{}, fmt.Errorf("unmarshal cloud account annotations: %w", err)
		}
		if len(anns) > 0 {
			out.Annotations = anns
		}
	}
	out.DisabledAt = disabledAt
	return out, nil
}
