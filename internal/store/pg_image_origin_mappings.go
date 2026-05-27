package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// imageOriginMappingColumns lists the SELECT columns in canonical order.
const imageOriginMappingColumns = `image_name, public_registry, notes,
	created_at, updated_at, created_by, updated_by`

func scanImageOriginMapping(row pgx.Row) (api.ImageOriginMapping, error) {
	var m api.ImageOriginMapping
	var notes, createdBy, updatedBy *string
	if err := row.Scan(&m.ImageName, &m.PublicRegistry, &notes,
		&m.CreatedAt, &m.UpdatedAt, &createdBy, &updatedBy); err != nil {
		return api.ImageOriginMapping{}, fmt.Errorf("scan image origin mapping: %w", err)
	}
	m.Notes = notes
	m.CreatedBy = createdBy
	m.UpdatedBy = updatedBy
	return m, nil
}

// nilIfEmpty maps "" to a nil pointer for nullable column writes. Used so
// PATCH callers can clear a notes column by sending the empty string.
func nilIfEmpty(s *string) *string {
	if s == nil {
		return nil
	}
	if *s == "" {
		return nil
	}
	return s
}

// FindImageOrigin returns the public_registry for the given bare
// image_name, or api.ErrNotFound when no mapping exists. Hot path called
// from the mirror resolver — keeps to a single indexed lookup on the PK.
func (p *PG) FindImageOrigin(ctx context.Context, imageName string) (string, error) {
	const q = `SELECT public_registry FROM image_origin_mappings WHERE image_name = $1`
	var reg string
	err := p.pool.QueryRow(ctx, q, imageName).Scan(&reg)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", api.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find image origin: %w", err)
	}
	return reg, nil
}

// CreateImageOriginMapping inserts a new (image_name, public_registry)
// row. Returns api.ErrConflict when the image_name already exists.
func (p *PG) CreateImageOriginMapping(
	ctx context.Context, in api.ImageOriginMappingCreate, createdBy string,
) (api.ImageOriginMapping, error) {
	const q = `INSERT INTO image_origin_mappings
		(image_name, public_registry, notes, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4)
		RETURNING ` + imageOriginMappingColumns
	row := p.pool.QueryRow(ctx, q, in.ImageName, in.PublicRegistry,
		nilIfEmpty(in.Notes), createdBy)
	m, err := scanImageOriginMapping(row)
	if err != nil {
		if isUniqueViolation(err) {
			return api.ImageOriginMapping{}, api.ErrConflict
		}
		return api.ImageOriginMapping{}, fmt.Errorf("create image origin mapping: %w", err)
	}
	return m, nil
}

// GetImageOriginMapping returns the row by image_name, or api.ErrNotFound.
func (p *PG) GetImageOriginMapping(ctx context.Context, imageName string) (api.ImageOriginMapping, error) {
	const q = `SELECT ` + imageOriginMappingColumns + `
FROM image_origin_mappings WHERE image_name = $1`
	m, err := scanImageOriginMapping(p.pool.QueryRow(ctx, q, imageName))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageOriginMapping{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageOriginMapping{}, fmt.Errorf("get image origin mapping: %w", err)
	}
	return m, nil
}

// PatchImageOriginMapping applies a merge-patch. Only public_registry and
// notes are patchable. notes='' clears the column. Returns ErrNotFound
// when no row matches.
func (p *PG) PatchImageOriginMapping(
	ctx context.Context, imageName string,
	patch api.ImageOriginMappingPatch, updatedBy string,
) (api.ImageOriginMapping, error) {
	// COALESCE($n, column) preserves the existing value when the patch
	// field is nil. For notes we want '' to mean "clear", so we use a
	// sentinel boolean ($n_set) gating the COALESCE input.
	const q = `UPDATE image_origin_mappings SET
		public_registry = COALESCE($2, public_registry),
		notes = CASE WHEN $3::boolean THEN NULLIF($4, '') ELSE notes END,
		updated_at = now(),
		updated_by = $5
	WHERE image_name = $1
	RETURNING ` + imageOriginMappingColumns

	notesSet := patch.Notes != nil
	notesVal := ""
	if patch.Notes != nil {
		notesVal = *patch.Notes
	}
	row := p.pool.QueryRow(ctx, q, imageName,
		patch.PublicRegistry, notesSet, notesVal, updatedBy)
	m, err := scanImageOriginMapping(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageOriginMapping{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageOriginMapping{}, fmt.Errorf("patch image origin mapping: %w", err)
	}
	return m, nil
}

// DeleteImageOriginMapping removes the row by image_name. ErrNotFound
// when no row matched.
func (p *PG) DeleteImageOriginMapping(ctx context.Context, imageName string) error {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM image_origin_mappings WHERE image_name = $1`, imageName)
	if err != nil {
		return fmt.Errorf("delete image origin mapping: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// defaultListLimit and maxListLimit follow the conventions used by other
// list endpoints in this package.
const (
	defaultImageOriginListLimit = 50
	maxImageOriginListLimit     = 500
)

// ListImageOriginMappings returns a cursor-paginated slice. The cursor is
// the last row's image_name; pagination is keyset-style on the PK.
func (p *PG) ListImageOriginMappings(
	ctx context.Context, params api.ListImageOriginMappingsParams,
) ([]api.ImageOriginMapping, string, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = defaultImageOriginListLimit
	}
	if limit > maxImageOriginListLimit {
		limit = maxImageOriginListLimit
	}

	var (
		filters []string
		args    []any
	)
	argN := 1
	if params.Cursor != "" {
		filters = append(filters, fmt.Sprintf("image_name > $%d", argN))
		args = append(args, params.Cursor)
		argN++
	}
	if params.PublicRegistry != "" {
		filters = append(filters, fmt.Sprintf("public_registry = $%d", argN))
		args = append(args, params.PublicRegistry)
		argN++
	}
	if params.Q != "" {
		filters = append(filters, fmt.Sprintf("image_name ILIKE $%d", argN))
		args = append(args, "%"+params.Q+"%")
		argN++
	}
	where := ""
	if len(filters) > 0 {
		where = "WHERE " + strings.Join(filters, " AND ")
	}
	args = append(args, limit+1) // fetch one extra to detect "has next"

	q := fmt.Sprintf(`SELECT %s FROM image_origin_mappings %s
		ORDER BY image_name ASC LIMIT $%d`,
		imageOriginMappingColumns, where, argN)

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list image origin mappings: %w", err)
	}
	defer rows.Close()

	out := make([]api.ImageOriginMapping, 0, limit)
	for rows.Next() {
		m, err := scanImageOriginMapping(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate image origin mappings: %w", err)
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].ImageName
		out = out[:limit]
	}
	return out, next, nil
}
