package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

func scanImageVersion(row pgx.Row) (api.ImageVersionRow, error) {
	var iv api.ImageVersionRow
	var ann []byte
	if err := row.Scan(
		&iv.ImageRepo, &iv.Variant, &iv.Registry, &iv.LatestTag, &ann,
		&iv.Source, &iv.LastCheckedAt, &iv.LastError, &iv.LastErrorAt, &iv.CreatedAt,
	); err != nil {
		return api.ImageVersionRow{}, err
	}
	iv.Annotation = json.RawMessage(ann)
	return iv, nil
}

// UpsertImageVersion inserts or updates a row in image_versions keyed on
// (image_repo, variant). A nil or empty Annotation is normalized to "{}"
// so the NOT NULL constraint never fires from a forgotten initialization.
func (p *PG) UpsertImageVersion(ctx context.Context, in api.ImageVersionUpsert) (api.ImageVersionRow, error) {
	if len(in.Annotation) == 0 {
		in.Annotation = json.RawMessage("{}")
	}
	const q = `
INSERT INTO image_versions
  (image_repo, variant, registry, latest_tag, annotation, source,
   last_checked_at, last_error, last_error_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (image_repo, variant) DO UPDATE SET
  registry        = EXCLUDED.registry,
  latest_tag      = EXCLUDED.latest_tag,
  annotation      = EXCLUDED.annotation,
  source          = EXCLUDED.source,
  last_checked_at = EXCLUDED.last_checked_at,
  last_error      = EXCLUDED.last_error,
  last_error_at   = EXCLUDED.last_error_at
RETURNING image_repo, variant, registry, latest_tag, annotation, source,
          last_checked_at, last_error, last_error_at, created_at`
	iv, err := scanImageVersion(p.pool.QueryRow(ctx, q,
		in.ImageRepo, in.Variant, in.Registry, in.LatestTag, []byte(in.Annotation),
		in.Source, in.LastCheckedAt, in.LastError, in.LastErrorAt))
	if err != nil {
		return api.ImageVersionRow{}, fmt.Errorf("upsert image version: %w", err)
	}
	return iv, nil
}

// GetImageVersionsByRepo returns all variant rows for a given image_repo,
// ordered by variant.
func (p *PG) GetImageVersionsByRepo(ctx context.Context, imageRepo string) ([]api.ImageVersionRow, error) {
	const q = `
SELECT image_repo, variant, registry, latest_tag, annotation, source,
       last_checked_at, last_error, last_error_at, created_at
FROM image_versions WHERE image_repo = $1
ORDER BY variant`
	rows, err := p.pool.Query(ctx, q, imageRepo)
	if err != nil {
		return nil, fmt.Errorf("get image versions by repo: %w", err)
	}
	defer rows.Close()
	out := make([]api.ImageVersionRow, 0)
	for rows.Next() {
		iv, err := scanImageVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan image version: %w", err)
		}
		out = append(out, iv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image versions: %w", err)
	}
	return out, nil
}

// ListImageVersionsByRepo pages over distinct image_repos and returns each
// with its full set of variant rows nested under it. lp.Limit controls the
// number of distinct repos per page; lp.Cursor is an opaque base64 token
// from the previous call.
func (p *PG) ListImageVersionsByRepo(ctx context.Context, lp api.ImageVersionListParams) ([]api.ImageVersionRepoView, string, error) {
	if lp.Limit <= 0 || lp.Limit > 200 {
		lp.Limit = 50
	}

	var conds []string
	var args []any
	if lp.Registry != "" {
		args = append(args, lp.Registry)
		conds = append(conds, fmt.Sprintf("registry = $%d", len(args)))
	}
	if lp.ImageRepoLike != "" {
		args = append(args, "%"+escapeLike(lp.ImageRepoLike)+"%")
		conds = append(conds, fmt.Sprintf(`image_repo ILIKE $%d ESCAPE '\'`, len(args)))
	}
	if lp.Variant != "" {
		args = append(args, lp.Variant)
		conds = append(conds, fmt.Sprintf("variant = $%d", len(args)))
	}
	if lp.HasError != nil {
		if *lp.HasError {
			conds = append(conds, "last_error IS NOT NULL")
		} else {
			conds = append(conds, "last_error IS NULL")
		}
	}
	if lp.LastCheckedBefore != nil {
		args = append(args, *lp.LastCheckedBefore)
		conds = append(conds, fmt.Sprintf("last_checked_at < $%d", len(args)))
	}
	if lp.Cursor != "" {
		decoded, err := decodeImageRepoCursor(lp.Cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, decoded)
		conds = append(conds, fmt.Sprintf("image_repo > $%d", len(args)))
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	// Phase 1: page over distinct image_repos (limit+1 to detect next page).
	repoQ := fmt.Sprintf(`
SELECT DISTINCT image_repo FROM image_versions
%s
ORDER BY image_repo
LIMIT $%d`, where, len(args)+1)
	repoArgs := append(append([]any(nil), args...), lp.Limit+1)
	rows, err := p.pool.Query(ctx, repoQ, repoArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("list image_repos: %w", err)
	}
	var repos []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("scan image_repo: %w", err)
		}
		repos = append(repos, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate image_repos: %w", err)
	}

	next := ""
	if len(repos) > lp.Limit {
		repos = repos[:lp.Limit]
		next = encodeImageRepoCursor(repos[len(repos)-1])
	}
	if len(repos) == 0 {
		return []api.ImageVersionRepoView{}, "", nil
	}

	// Phase 2: fetch every variant row for the selected repos.
	detailQ := `
SELECT image_repo, variant, registry, latest_tag, annotation, source,
       last_checked_at, last_error, last_error_at, created_at
FROM image_versions
WHERE image_repo = ANY($1)
ORDER BY image_repo, variant`
	drows, err := p.pool.Query(ctx, detailQ, repos)
	if err != nil {
		return nil, "", fmt.Errorf("list image_versions detail: %w", err)
	}
	defer drows.Close()

	grouped := map[string]*api.ImageVersionRepoView{}
	var order []string
	for drows.Next() {
		iv, err := scanImageVersion(drows)
		if err != nil {
			return nil, "", fmt.Errorf("scan image_version detail: %w", err)
		}
		v, ok := grouped[iv.ImageRepo]
		if !ok {
			v = &api.ImageVersionRepoView{
				ImageRepo: iv.ImageRepo,
				Registry:  iv.Registry,
				Variants:  make([]api.ImageVersionRow, 0, 4),
			}
			grouped[iv.ImageRepo] = v
			order = append(order, iv.ImageRepo)
		}
		v.Variants = append(v.Variants, iv)
	}
	if err := drows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate image_versions detail: %w", err)
	}

	items := make([]api.ImageVersionRepoView, 0, len(order))
	for _, k := range order {
		items = append(items, *grouped[k])
	}
	return items, next, nil
}

// DeleteImageVersionsNotIn deletes all rows whose (image_repo, variant) pair
// is not in the keep slice. When keep is nil or empty, all rows are deleted.
func (p *PG) DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error) {
	if len(keep) == 0 {
		tag, err := p.pool.Exec(ctx, `DELETE FROM image_versions`)
		if err != nil {
			return 0, fmt.Errorf("delete all image versions: %w", err)
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
DELETE FROM image_versions
WHERE (image_repo, variant) NOT IN (
    SELECT * FROM unnest($1::text[], $2::text[])
)`
	tag, err := p.pool.Exec(ctx, q, repos, variants)
	if err != nil {
		return 0, fmt.Errorf("reap image versions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DistinctImageRefs returns the set of distinct image strings found in
// workloads.containers and pods.containers JSONB arrays. Only non-empty
// image values are returned.
func (p *PG) DistinctImageRefs(ctx context.Context) ([]string, error) {
	const q = `
SELECT DISTINCT image FROM (
    SELECT jsonb_array_elements(containers)->>'image' AS image FROM workloads
    WHERE containers IS NOT NULL AND jsonb_typeof(containers) = 'array'
    UNION
    SELECT jsonb_array_elements(containers)->>'image' AS image FROM pods
    WHERE containers IS NOT NULL AND jsonb_typeof(containers) = 'array'
) u
WHERE image IS NOT NULL AND image <> ''`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("distinct image refs: %w", err)
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan ref: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate refs: %w", err)
	}
	return out, nil
}

func encodeImageRepoCursor(repo string) string {
	return base64.URLEncoding.EncodeToString([]byte(repo))
}

func decodeImageRepoCursor(c string) (string, error) {
	b, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

