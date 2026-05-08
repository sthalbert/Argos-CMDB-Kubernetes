package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

func scanImageRegistry(row pgx.Row) (api.ImageRegistry, error) {
	var r api.ImageRegistry
	if err := row.Scan(&r.Hostname, &r.RateLimitPerSec, &r.Enabled, &r.Notes,
		&r.CreatedAt, &r.UpdatedAt); err != nil {
		return api.ImageRegistry{}, err
	}
	return r, nil
}

// ListImageRegistries returns all rows from image_versions_registries ordered
// by hostname.
func (p *PG) ListImageRegistries(ctx context.Context) ([]api.ImageRegistry, error) {
	const q = `
SELECT hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at
FROM image_versions_registries
ORDER BY hostname`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list image registries: %w", err)
	}
	defer rows.Close()
	out := make([]api.ImageRegistry, 0)
	for rows.Next() {
		r, err := scanImageRegistry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan image registry: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image registries: %w", err)
	}
	return out, nil
}

// GetImageRegistry fetches a single registry by hostname.
// Returns api.ErrNotFound when the hostname does not exist.
func (p *PG) GetImageRegistry(ctx context.Context, hostname string) (api.ImageRegistry, error) {
	const q = `
SELECT hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at
FROM image_versions_registries WHERE hostname = $1`
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageRegistry{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageRegistry{}, fmt.Errorf("get image registry: %w", err)
	}
	return r, nil
}

// CreateImageRegistry inserts a new registry row.
// Returns api.ErrConflict when a row with the same hostname already exists.
// Enabled defaults to true when in.Enabled is nil.
func (p *PG) CreateImageRegistry(ctx context.Context, in api.ImageRegistryUpsert) (api.ImageRegistry, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	const q = `
INSERT INTO image_versions_registries (hostname, rate_limit_per_sec, enabled, notes)
VALUES ($1, $2, $3, $4)
RETURNING hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at`
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, in.Hostname, in.RateLimitPerSec, enabled, in.Notes))
	if err != nil {
		if isUniqueViolation(err) {
			return api.ImageRegistry{}, api.ErrConflict
		}
		return api.ImageRegistry{}, fmt.Errorf("create image registry: %w", err)
	}
	return r, nil
}

// UpdateImageRegistry applies a merge-patch to an existing registry row.
// Returns api.ErrNotFound when the hostname does not exist.
//
// Notes semantics: a nil pointer leaves the column unchanged; a non-nil
// pointer overwrites with the pointed-to string (which may be empty).
// JSON null and an absent field are indistinguishable through *string,
// so callers cannot use this API to clear notes back to NULL — pass
// pointer-to-empty-string if a "cleared" placeholder is acceptable, or
// extend the patch type to **string if true NULL-clearing is needed.
func (p *PG) UpdateImageRegistry(ctx context.Context, hostname string, patch api.ImageRegistryPatch) (api.ImageRegistry, error) {
	const q = `
UPDATE image_versions_registries SET
    rate_limit_per_sec = COALESCE($2, rate_limit_per_sec),
    enabled            = COALESCE($3, enabled),
    notes              = CASE WHEN $4::bool THEN $5 ELSE notes END,
    updated_at         = now()
WHERE hostname = $1
RETURNING hostname, rate_limit_per_sec, enabled, notes, created_at, updated_at`
	notesProvided := patch.Notes != nil
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname,
		patch.RateLimitPerSec, patch.Enabled, notesProvided, patch.Notes))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageRegistry{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageRegistry{}, fmt.Errorf("update image registry: %w", err)
	}
	return r, nil
}

// DeleteImageRegistry removes a registry by hostname.
// Returns api.ErrNotFound when no such row exists.
func (p *PG) DeleteImageRegistry(ctx context.Context, hostname string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM image_versions_registries WHERE hostname = $1`, hostname)
	if err != nil {
		return fmt.Errorf("delete image registry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}
