package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

const imageOriginResolutionColumns = `mirror_image_repo, variant, origin_image_repo,
	via_hostname, resolved_at, last_error, last_error_at`

func scanImageOriginResolution(row pgx.Row) (api.ImageOriginResolution, error) {
	var r api.ImageOriginResolution
	if err := row.Scan(
		&r.MirrorImageRepo, &r.Variant, &r.OriginImageRepo,
		&r.ViaHostname, &r.ResolvedAt, &r.LastError, &r.LastErrorAt,
	); err != nil {
		return api.ImageOriginResolution{}, fmt.Errorf("scan image_origin_resolution: %w", err)
	}
	return r, nil
}

// UpsertImageOriginResolution inserts or replaces a resolution row keyed
// on (mirror_image_repo, variant). Success rows have origin_image_repo
// and via_hostname populated together; failure rows have last_error set
// and both nullable origin fields NULL (enforced by a CHECK constraint).
//
//nolint:gocritic // in matches the api.Store interface signature
func (p *PG) UpsertImageOriginResolution(ctx context.Context, in api.ImageOriginResolutionUpsert) (api.ImageOriginResolution, error) {
	const q = `
INSERT INTO image_origin_resolutions
  (mirror_image_repo, variant, origin_image_repo, via_hostname,
   resolved_at, last_error, last_error_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (mirror_image_repo, variant) DO UPDATE SET
  origin_image_repo = EXCLUDED.origin_image_repo,
  via_hostname      = EXCLUDED.via_hostname,
  resolved_at       = EXCLUDED.resolved_at,
  last_error        = EXCLUDED.last_error,
  last_error_at     = EXCLUDED.last_error_at
RETURNING ` + imageOriginResolutionColumns
	r, err := scanImageOriginResolution(p.pool.QueryRow(ctx, q,
		in.MirrorImageRepo, in.Variant, in.OriginImageRepo, in.ViaHostname,
		in.ResolvedAt, in.LastError, in.LastErrorAt,
	))
	if err != nil {
		return api.ImageOriginResolution{}, fmt.Errorf("upsert image_origin_resolution: %w", err)
	}
	return r, nil
}

// GetImageOriginResolution returns the row for the given
// (mirror_image_repo, variant). Returns api.ErrNotFound when missing.
func (p *PG) GetImageOriginResolution(ctx context.Context, mirrorImageRepo, variant string) (api.ImageOriginResolution, error) {
	const q = `SELECT ` + imageOriginResolutionColumns + `
FROM image_origin_resolutions
WHERE mirror_image_repo = $1 AND variant = $2`
	r, err := scanImageOriginResolution(p.pool.QueryRow(ctx, q, mirrorImageRepo, variant))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageOriginResolution{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageOriginResolution{}, fmt.Errorf("get image_origin_resolution: %w", err)
	}
	return r, nil
}

// DeleteImageOriginResolutionsNotIn removes rows whose
// (mirror_image_repo, variant) pair is not in keep. When keep is empty,
// all rows are deleted. Mirrors DeleteImageVersionsNotIn semantics.
func (p *PG) DeleteImageOriginResolutionsNotIn(ctx context.Context, keep [][2]string) (int64, error) {
	if len(keep) == 0 {
		tag, err := p.pool.Exec(ctx, `DELETE FROM image_origin_resolutions`)
		if err != nil {
			return 0, fmt.Errorf("delete all image_origin_resolutions: %w", err)
		}
		return tag.RowsAffected(), nil
	}
	repos := make([]string, len(keep))
	variants := make([]string, len(keep))
	for i, k := range keep {
		repos[i] = k[0]
		variants[i] = k[1]
	}
	const q = `
DELETE FROM image_origin_resolutions
WHERE (mirror_image_repo, variant) NOT IN (
    SELECT * FROM unnest($1::text[], $2::text[])
)`
	tag, err := p.pool.Exec(ctx, q, repos, variants)
	if err != nil {
		return 0, fmt.Errorf("reap image_origin_resolutions: %w", err)
	}
	return tag.RowsAffected(), nil
}
