package store

// Auth-substrate SQL per ADR-0007: users, user_identities, sessions,
// api_tokens. Kept in its own file so pg.go stays scannable.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/auth"
)

// --- users ---------------------------------------------------------------

const userColumns = `id, username, role, must_change_password,
	created_at, updated_at, last_login_at, disabled_at`

// CountActiveAdmins returns the number of non-disabled admin users.
func (p *PG) CountActiveAdmins(ctx context.Context) (int, error) {
	var n int
	if err := p.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return n, nil
}

// CreateUser inserts a new user and returns the stored representation.
func (p *PG) CreateUser(ctx context.Context, in api.UserInsert) (api.User, error) {
	id := uuid.New()
	now := time.Now().UTC()
	const q = `
		INSERT INTO users (id, username, password_hash, role, must_change_password, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
	`
	if _, err := p.pool.Exec(ctx, q,
		id, in.Username, in.PasswordHash, in.Role, in.MustChangePassword, now,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.User{}, fmt.Errorf("user %q already exists: %w", in.Username, api.ErrConflict)
		}
		return api.User{}, fmt.Errorf("insert user: %w", err)
	}
	return p.GetUser(ctx, id)
}

// GetUser fetches a user by id.
func (p *PG) GetUser(ctx context.Context, id uuid.UUID) (api.User, error) {
	q := `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	u, err := scanUser(p.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.User{}, api.ErrNotFound
		}
		return api.User{}, fmt.Errorf("select user: %w", err)
	}
	return u, nil
}

// GetUserByUsername fetches a non-disabled user by case-insensitive username, including the password hash.
func (p *PG) GetUserByUsername(ctx context.Context, username string) (api.UserWithSecret, error) {
	q := `SELECT ` + userColumns + `, password_hash
	      FROM users
	      WHERE LOWER(username) = LOWER($1) AND disabled_at IS NULL`
	row := p.pool.QueryRow(ctx, q, username)
	var (
		out         api.UserWithSecret
		id          uuid.UUID
		mustChange  bool
		createdAt   time.Time
		updatedAt   time.Time
		lastLoginAt *time.Time
		disabledAt  *time.Time
		role        string
	)
	if err := row.Scan(
		&id, &out.Username, &role, &mustChange,
		&createdAt, &updatedAt, &lastLoginAt, &disabledAt,
		&out.PasswordHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.UserWithSecret{}, api.ErrNotFound
		}
		return api.UserWithSecret{}, fmt.Errorf("select user by username: %w", err)
	}
	out.Id = &id
	out.Role = api.Role(role)
	out.MustChangePassword = &mustChange
	out.CreatedAt = &createdAt
	out.UpdatedAt = &updatedAt
	out.LastLoginAt = lastLoginAt
	out.DisabledAt = disabledAt
	return out, nil
}

// ListUsers returns a cursor-paginated list of users sorted by creation date descending.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListUsers(ctx context.Context, limit int, cursor string) ([]api.User, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(userColumns)
	sb.WriteString(` FROM users`)
	args := make([]any, 0, 3)
	conds := make([]string, 0, 1)
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
		return nil, "", fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	items := make([]api.User, 0, limit)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan user: %w", err)
		}
		items = append(items, u)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate users: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateUser applies merge-patch semantics on user fields (role, must-change, disabled).
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateUser(ctx context.Context, id uuid.UUID, in api.UserPatch) (api.User, error) {
	sets := []string{"updated_at = $1"}
	args := []any{time.Now().UTC()}
	idx := 2
	if in.Role != nil {
		if _, ok := auth.ValidRoles[*in.Role]; !ok {
			return api.User{}, fmt.Errorf("invalid role %q: %w", *in.Role, api.ErrNotFound)
		}
		sets = append(sets, fmt.Sprintf("role = $%d", idx))
		args = append(args, *in.Role)
		idx++
	}
	if in.MustChangePassword != nil {
		sets = append(sets, fmt.Sprintf("must_change_password = $%d", idx))
		args = append(args, *in.MustChangePassword)
		idx++
	}
	if in.Disabled != nil {
		if *in.Disabled {
			sets = append(sets, fmt.Sprintf("disabled_at = $%d", idx))
			args = append(args, time.Now().UTC())
			// No further placeholders after this branch, and the false
			// branch binds a literal NULL — no idx++ needed on either
			// path. (The final `$%d` below uses len(args).)
		} else {
			sets = append(sets, "disabled_at = NULL")
		}
	}
	args = append(args, id)

	q := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(sets, ", "), len(args))
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.User{}, fmt.Errorf("update user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.User{}, api.ErrNotFound
	}
	// Disabling a user immediately kills their active sessions.
	if in.Disabled != nil && *in.Disabled {
		if err := p.DeleteSessionsForUser(ctx, id); err != nil {
			return api.User{}, err
		}
	}
	return p.GetUser(ctx, id)
}

// SetUserPassword updates the password hash and must-change flag, then invalidates all sessions.
func (p *PG) SetUserPassword(ctx context.Context, id uuid.UUID, hash string, mustChange bool) error {
	now := time.Now().UTC()
	tag, err := p.pool.Exec(ctx,
		`UPDATE users SET password_hash = $1, must_change_password = $2, updated_at = $3 WHERE id = $4`,
		hash, mustChange, now, id,
	)
	if err != nil {
		return fmt.Errorf("set user password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	// Log out every session for this user so other tabs / devices stop
	// working the moment the password rotates.
	return p.DeleteSessionsForUser(ctx, id)
}

// TouchUserLogin records the last login timestamp.
func (p *PG) TouchUserLogin(ctx context.Context, id uuid.UUID, now time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET last_login_at = $1 WHERE id = $2`, now, id)
	if err != nil {
		return fmt.Errorf("touch user login: %w", err)
	}
	return nil
}

// DeleteUser removes a user by id, returning ErrConflict if the user owns active API tokens.
func (p *PG) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			// FK from api_tokens.created_by_user_id prevents the delete —
			// the caller needs to revoke the user's tokens first. Surface
			// as ErrConflict so the handler returns 409.
			return fmt.Errorf("user owns active api tokens — revoke them first: %w", api.ErrConflict)
		}
		return fmt.Errorf("delete user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// lockActiveAdminsForGuard acquires row-level FOR UPDATE locks on every
// currently-active admin row inside the open transaction. Two
// concurrent guarded operations both observing the same admin set will
// serialise on these locks: the second transaction blocks until the
// first commits, then re-evaluates the count against the post-commit
// snapshot — closing the TOCTOU window described in audit finding H1.
func lockActiveAdminsForGuard(ctx context.Context, tx pgx.Tx) error {
	// Discard the rows themselves; we only care about the side effect of
	// FOR UPDATE acquiring the locks.
	rows, err := tx.Query(ctx,
		`SELECT id FROM users
		 WHERE role = 'admin' AND disabled_at IS NULL
		 FOR UPDATE`,
	)
	if err != nil {
		return fmt.Errorf("lock active admins: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("scan locked admins: %w", err)
	}
	return nil
}

// UpdateUserGuarded mirrors UpdateUser but enforces the last-admin
// invariant atomically inside a single transaction (audit finding H1).
// Returns api.ErrLastAdmin when the patch would demote (role != admin)
// or disable an active admin and no other active admin would remain.
//
//nolint:gocyclo // merge-patch + invariant guard is inherently branchy
func (p *PG) UpdateUserGuarded(ctx context.Context, id uuid.UUID, in api.UserPatch) (api.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.User{}, fmt.Errorf("begin update guarded: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockActiveAdminsForGuard(ctx, tx); err != nil {
		return api.User{}, err
	}

	var (
		role       string
		disabledAt *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT role, disabled_at FROM users WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&role, &disabledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.User{}, api.ErrNotFound
		}
		return api.User{}, fmt.Errorf("lock target user: %w", err)
	}

	demoting := in.Role != nil && *in.Role != auth.RoleAdmin
	disabling := in.Disabled != nil && *in.Disabled
	if (demoting || disabling) && role == auth.RoleAdmin && disabledAt == nil {
		var others int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users
			 WHERE role = 'admin' AND disabled_at IS NULL AND id <> $1`,
			id,
		).Scan(&others); err != nil {
			return api.User{}, fmt.Errorf("count other admins: %w", err)
		}
		if others == 0 {
			return api.User{}, api.ErrLastAdmin
		}
	}

	if err := applyUserPatchTx(ctx, tx, id, in); err != nil {
		return api.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.User{}, fmt.Errorf("commit update guarded: %w", err)
	}
	// Disabling kills sessions; do it after commit so the kill is durable.
	if in.Disabled != nil && *in.Disabled {
		if err := p.DeleteSessionsForUser(ctx, id); err != nil {
			return api.User{}, err
		}
	}
	return p.GetUser(ctx, id)
}

// applyUserPatchTx writes the merge-patch inside an already-open
// transaction. Mirrors UpdateUser's body but returns rather than
// committing, and skips the post-commit session sweep (the caller
// performs it after Commit).
func applyUserPatchTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, in api.UserPatch) error {
	sets := []string{"updated_at = $1"}
	args := []any{time.Now().UTC()}
	idx := 2
	if in.Role != nil {
		if _, ok := auth.ValidRoles[*in.Role]; !ok {
			return fmt.Errorf("invalid role %q: %w", *in.Role, api.ErrNotFound)
		}
		sets = append(sets, fmt.Sprintf("role = $%d", idx))
		args = append(args, *in.Role)
		idx++
	}
	if in.MustChangePassword != nil {
		sets = append(sets, fmt.Sprintf("must_change_password = $%d", idx))
		args = append(args, *in.MustChangePassword)
		idx++
	}
	if in.Disabled != nil {
		if *in.Disabled {
			sets = append(sets, fmt.Sprintf("disabled_at = $%d", idx))
			args = append(args, time.Now().UTC())
		} else {
			sets = append(sets, "disabled_at = NULL")
		}
	}
	args = append(args, id)
	q := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d", strings.Join(sets, ", "), len(args))
	tag, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("update user tx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// DeleteUserGuarded is the transactional counterpart to DeleteUser
// (audit finding H1). Returns api.ErrLastAdmin when the target is the
// only currently active admin.
//
//nolint:gocyclo // guarded count + write under one lock is inherently branchy
func (p *PG) DeleteUserGuarded(ctx context.Context, id uuid.UUID) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete guarded: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := lockActiveAdminsForGuard(ctx, tx); err != nil {
		return err
	}

	var (
		role       string
		disabledAt *time.Time
	)
	err = tx.QueryRow(ctx,
		`SELECT role, disabled_at FROM users WHERE id = $1 FOR UPDATE`,
		id,
	).Scan(&role, &disabledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.ErrNotFound
		}
		return fmt.Errorf("lock target user: %w", err)
	}

	if role == auth.RoleAdmin && disabledAt == nil {
		var others int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users
			 WHERE role = 'admin' AND disabled_at IS NULL AND id <> $1`,
			id,
		).Scan(&others); err != nil {
			return fmt.Errorf("count other admins: %w", err)
		}
		if others == 0 {
			return api.ErrLastAdmin
		}
	}

	tag, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return fmt.Errorf("user owns active api tokens — revoke them first: %w", api.ErrConflict)
		}
		return fmt.Errorf("delete user tx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete guarded: %w", err)
	}
	return nil
}

// GetUserForAuth is the auth.Store lookup — lightweight view the
// middleware uses after a session resolves.
func (p *PG) GetUserForAuth(ctx context.Context, id uuid.UUID) (auth.User, error) {
	var (
		u        auth.User
		username string
		role     string
		mustChg  bool
		disabled *time.Time
	)
	err := p.pool.QueryRow(ctx,
		`SELECT id, username, role, must_change_password, disabled_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(&u.ID, &username, &role, &mustChg, &disabled)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.User{}, auth.ErrUnauthorized
		}
		return auth.User{}, fmt.Errorf("select user for auth: %w", err)
	}
	u.Username = username
	u.Role = role
	u.MustChangePassword = mustChg
	u.Disabled = disabled != nil
	return u, nil
}

func scanUser(row pgx.Row) (api.User, error) {
	var (
		out         api.User
		id          uuid.UUID
		role        string
		mustChange  bool
		createdAt   time.Time
		updatedAt   time.Time
		lastLoginAt *time.Time
		disabledAt  *time.Time
	)
	if err := row.Scan(
		&id, &out.Username, &role, &mustChange,
		&createdAt, &updatedAt, &lastLoginAt, &disabledAt,
	); err != nil {
		return api.User{}, fmt.Errorf("scan user: %w", err)
	}
	out.Id = &id
	out.Role = api.Role(role)
	out.MustChangePassword = &mustChange
	out.CreatedAt = &createdAt
	out.UpdatedAt = &updatedAt
	out.LastLoginAt = lastLoginAt
	out.DisabledAt = disabledAt
	return out, nil
}

// --- sessions ------------------------------------------------------------

// CreateSession persists a new session row.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateSession(ctx context.Context, in api.SessionInsert) error {
	var userAgent, sourceIP any = in.UserAgent, in.SourceIP
	if in.UserAgent == "" {
		userAgent = nil
	}
	if in.SourceIP == "" {
		sourceIP = nil
	}
	// public_id is populated by the DB (gen_random_uuid() default would
	// work too, but explicit here keeps the SQL self-contained).
	publicID := uuid.New()
	_, err := p.pool.Exec(ctx,
		`INSERT INTO sessions (id, public_id, user_id, created_at, last_used_at, expires_at, user_agent, source_ip)
		 VALUES ($1, $2, $3, $4, $4, $5, $6, $7)`,
		in.ID, publicID, in.UserID, in.CreatedAt, in.ExpiresAt, userAgent, sourceIP,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetActiveSession retrieves a non-expired session by cookie id.
func (p *PG) GetActiveSession(ctx context.Context, id string) (auth.Session, error) {
	var s auth.Session
	err := p.pool.QueryRow(ctx,
		`SELECT id, user_id, expires_at
		 FROM sessions
		 WHERE id = $1 AND expires_at > NOW()`,
		id,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Session{}, auth.ErrUnauthorized
		}
		return auth.Session{}, fmt.Errorf("select session: %w", err)
	}
	return s, nil
}

// TouchSession extends the session's last-used and expiry timestamps.
func (p *PG) TouchSession(ctx context.Context, id string, now, newExpiry time.Time) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE sessions SET last_used_at = $1, expires_at = $2 WHERE id = $3`,
		now, newExpiry, id,
	)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// DeleteSession revokes the session whose cookie value is `id`. Used by
// the logout handler, where the caller has the cookie from ctx.
func (p *PG) DeleteSession(ctx context.Context, id string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// DeleteSessionByPublicID revokes by public_id, the UUID handle surfaced
// to admins. Admins never see cookie values, so they use this path.
func (p *PG) DeleteSessionByPublicID(ctx context.Context, publicID uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE public_id = $1`, publicID)
	if err != nil {
		return fmt.Errorf("delete session by public_id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// DeleteSessionsForUser revokes every session for the given user.
func (p *PG) DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// ListSessions returns active sessions, surfacing each row's public_id
// (not the cookie value) as the API-facing `id`. Cookie values never
// leave the database.
func (p *PG) ListSessions(ctx context.Context, limit int, cursor string) ([]api.Session, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT s.public_id, s.user_id, u.username, s.created_at, s.last_used_at,
	                       s.expires_at, s.user_agent, s.source_ip
	                FROM sessions s
	                JOIN users u ON u.id = s.user_id
	                WHERE s.expires_at > NOW()`)
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
		fmt.Fprintf(&sb, " AND (s.created_at, s.public_id) < ($%d, $%d)", tsIdx, idIdx)
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY s.created_at DESC, s.public_id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()

	items := make([]api.Session, 0, limit)
	var lastPublicID uuid.UUID
	for rows.Next() {
		var (
			s         api.Session
			publicID  uuid.UUID
			userAgent *string
			sourceIP  *string
			username  string
		)
		if err := rows.Scan(
			&publicID, &s.UserId, &username,
			&s.CreatedAt, &s.LastUsedAt, &s.ExpiresAt,
			&userAgent, &sourceIP,
		); err != nil {
			return nil, "", fmt.Errorf("scan session: %w", err)
		}
		s.Id = publicID.String()
		s.Username = &username
		s.UserAgent = userAgent
		s.SourceIp = sourceIP
		items = append(items, s)
		lastPublicID = publicID
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate sessions: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, lastPublicID)
		items = items[:limit]
	}
	return items, next, nil
}

// --- api tokens ----------------------------------------------------------

const apiTokenColumns = `id, name, prefix, scopes, created_by_user_id,
	created_at, last_used_at, expires_at, revoked_at`

// CreateAPIToken inserts a new bearer token and returns the stored representation.
// When in.BoundCloudAccountID is non-nil, the token is bound to that
// cloud account (vm-collector preset, ADR-0015).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateAPIToken(ctx context.Context, in api.APITokenInsert) (api.ApiToken, error) {
	now := time.Now().UTC()
	_, err := p.pool.Exec(ctx,
		`INSERT INTO api_tokens (id, name, prefix, hash, scopes, created_by_user_id, created_at, expires_at, bound_cloud_account_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		in.ID, in.Name, in.Prefix, in.Hash, in.Scopes, in.CreatedByUserID, now, in.ExpiresAt, in.BoundCloudAccountID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				// Prefix collision on 8 chars is astronomically unlikely,
				// but it's the only uniqueness path that can hit this.
				return api.ApiToken{}, fmt.Errorf("token prefix collision; retry: %w", api.ErrConflict)
			}
			if pgErr.Code == "23503" {
				// Either the creator user is missing or the bound
				// cloud_account_id does not exist.
				return api.ApiToken{}, fmt.Errorf("foreign key violation on token insert: %w", api.ErrNotFound)
			}
		}
		return api.ApiToken{}, fmt.Errorf("insert api token: %w", err)
	}
	return p.getAPIToken(ctx, in.ID)
}

func (p *PG) getAPIToken(ctx context.Context, id uuid.UUID) (api.ApiToken, error) {
	q := `SELECT ` + apiTokenColumns + ` FROM api_tokens WHERE id = $1`
	t, err := scanAPIToken(p.pool.QueryRow(ctx, q, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.ApiToken{}, api.ErrNotFound
		}
		return api.ApiToken{}, fmt.Errorf("select api token: %w", err)
	}
	return t, nil
}

// GetActiveTokenByPrefix looks up a non-revoked, non-expired token by its 8-char prefix.
func (p *PG) GetActiveTokenByPrefix(ctx context.Context, prefix string) (auth.APIToken, error) {
	var (
		t        auth.APIToken
		scopes   []string
		boundAcc *uuid.UUID
	)
	err := p.pool.QueryRow(ctx,
		`SELECT id, name, hash, scopes, created_by_user_id, bound_cloud_account_id
		 FROM api_tokens
		 WHERE prefix = $1
		   AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > NOW())`,
		prefix,
	).Scan(&t.ID, &t.Name, &t.Hash, &scopes, &t.CreatedByUserID, &boundAcc)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.APIToken{}, auth.ErrUnauthorized
		}
		return auth.APIToken{}, fmt.Errorf("select token by prefix: %w", err)
	}
	t.Scopes = scopes
	t.BoundCloudAccountID = boundAcc
	return t, nil
}

// TouchToken records the last-used timestamp for an API token.
func (p *PG) TouchToken(ctx context.Context, id uuid.UUID, now time.Time) error {
	_, err := p.pool.Exec(ctx, `UPDATE api_tokens SET last_used_at = $1 WHERE id = $2`, now, id)
	if err != nil {
		return fmt.Errorf("touch token: %w", err)
	}
	return nil
}

// ListAPITokens returns a cursor-paginated list of API tokens sorted by creation date descending.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListAPITokens(ctx context.Context, limit int, cursor string) ([]api.ApiToken, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(apiTokenColumns)
	sb.WriteString(` FROM api_tokens`)
	args := make([]any, 0, 3)
	conds := make([]string, 0, 1)
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
		return nil, "", fmt.Errorf("query tokens: %w", err)
	}
	defer rows.Close()

	items := make([]api.ApiToken, 0, limit)
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan token: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate tokens: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// RevokeAPIToken marks a token as revoked. Idempotent if already revoked; returns ErrNotFound if absent.
func (p *PG) RevokeAPIToken(ctx context.Context, id uuid.UUID, now time.Time) error {
	tag, err := p.pool.Exec(ctx,
		`UPDATE api_tokens SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the id doesn't exist or it's already revoked. The
		// latter is idempotent success; check which.
		var exists bool
		if err := p.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM api_tokens WHERE id = $1)`,
			id,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check token existence: %w", err)
		}
		if !exists {
			return api.ErrNotFound
		}
	}
	return nil
}

// --- OIDC identities + auth states --------------------------------------

// GetUserByIdentity looks up a non-disabled user by their OIDC (issuer, subject) pair.
func (p *PG) GetUserByIdentity(ctx context.Context, issuer, subject string) (api.User, error) {
	// user_identities also has an id / created_at column — qualify every
	// selected column with the users alias so the planner doesn't reject
	// the unqualified reference as ambiguous (SQLSTATE 42702).
	q := `SELECT u.id, u.username, u.role, u.must_change_password,
	             u.created_at, u.updated_at, u.last_login_at, u.disabled_at
	      FROM users u
	      JOIN user_identities ui ON ui.user_id = u.id
	      WHERE ui.issuer = $1 AND ui.subject = $2 AND u.disabled_at IS NULL`
	u, err := scanUser(p.pool.QueryRow(ctx, q, issuer, subject))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.User{}, api.ErrNotFound
		}
		return api.User{}, fmt.Errorf("select user by identity: %w", err)
	}
	return u, nil
}

// CreateUserWithIdentity wraps the user + user_identity INSERTs in a
// single transaction. Callers that hit ErrConflict (23505 on username)
// pick a different username and retry.
func (p *PG) CreateUserWithIdentity(ctx context.Context, in api.UserInsert, ident api.UserIdentityInsert) (api.User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.User{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id := uuid.New()
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, username, password_hash, role, must_change_password, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id, in.Username, in.PasswordHash, in.Role, in.MustChangePassword, now,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.User{}, fmt.Errorf("user %q already exists: %w", in.Username, api.ErrConflict)
		}
		return api.User{}, fmt.Errorf("insert user: %w", err)
	}

	var emailArg any = ident.Email
	if ident.Email == "" {
		emailArg = nil
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_identities (id, user_id, issuer, subject, email, created_at, last_seen_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		uuid.New(), id, ident.Issuer, ident.Subject, emailArg, now,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Race: another login registered this (issuer, subject) between
			// our GetUserByIdentity check and the insert. Surface as
			// ErrConflict so the handler can re-read and issue a session.
			return api.User{}, fmt.Errorf("identity already exists: %w", api.ErrConflict)
		}
		return api.User{}, fmt.Errorf("insert identity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return api.User{}, fmt.Errorf("commit tx: %w", err)
	}

	// Re-read through GetUser so the returned row is populated exactly
	// the same way GetUserByIdentity / scanUser do.
	return p.GetUser(ctx, id)
}

// TouchUserIdentity updates the last-seen timestamp for an OIDC identity row.
func (p *PG) TouchUserIdentity(ctx context.Context, userID uuid.UUID, issuer, subject string, now time.Time) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE user_identities SET last_seen_at = $1
		 WHERE user_id = $2 AND issuer = $3 AND subject = $4`,
		now, userID, issuer, subject,
	)
	if err != nil {
		return fmt.Errorf("touch identity: %w", err)
	}
	return nil
}

// CreateOidcAuthState persists an in-flight OIDC state row (code_verifier + nonce).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateOidcAuthState(ctx context.Context, in api.OidcAuthStateInsert) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO oidc_auth_states (state, code_verifier, nonce, created_at, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		in.State, in.CodeVerifier, in.Nonce, in.CreatedAt, in.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert oidc state: %w", err)
	}
	return nil
}

// ConsumeOidcAuthState does SELECT ... FOR UPDATE + DELETE in one tx so
// the row is single-use even under concurrent callbacks carrying the
// same state (an attack scenario, not a normal case).
func (p *PG) ConsumeOidcAuthState(ctx context.Context, state string) (codeVerifier, nonce string, err error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	err = tx.QueryRow(ctx,
		`SELECT code_verifier, nonce FROM oidc_auth_states
		 WHERE state = $1 AND expires_at > NOW()
		 FOR UPDATE`,
		state,
	).Scan(&codeVerifier, &nonce)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", api.ErrNotFound
		}
		return "", "", fmt.Errorf("select oidc state: %w", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM oidc_auth_states WHERE state = $1`, state); err != nil {
		return "", "", fmt.Errorf("delete oidc state: %w", err)
	}
	// Opportunistic cleanup of anything else that's expired — keeps the
	// table tiny without a separate cron.
	if _, err := tx.Exec(ctx, `DELETE FROM oidc_auth_states WHERE expires_at <= NOW()`); err != nil {
		return "", "", fmt.Errorf("sweep expired oidc states: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", "", fmt.Errorf("commit tx: %w", err)
	}
	return codeVerifier, nonce, nil
}

func scanAPIToken(row pgx.Row) (api.ApiToken, error) {
	var (
		out        api.ApiToken
		id         uuid.UUID
		createdBy  uuid.UUID
		scopes     []string
		createdAt  time.Time
		lastUsedAt *time.Time
		expiresAt  *time.Time
		revokedAt  *time.Time
	)
	var prefix string
	if err := row.Scan(
		&id, &out.Name, &prefix, &scopes, &createdBy,
		&createdAt, &lastUsedAt, &expiresAt, &revokedAt,
	); err != nil {
		return api.ApiToken{}, fmt.Errorf("scan api token: %w", err)
	}
	out.Id = &id
	out.Prefix = &prefix
	out.Scopes = scopes
	out.CreatedByUserId = &createdBy
	out.CreatedAt = &createdAt
	out.LastUsedAt = lastUsedAt
	out.ExpiresAt = expiresAt
	out.RevokedAt = revokedAt
	return out, nil
}
