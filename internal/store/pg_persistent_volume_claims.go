// PersistentVolumeClaim CRUD + upsert + reconcile. Split out of pg.go.
package store

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

// classifyPVCFKError disambiguates 23503 foreign-key violations on the
// persistent_volume_claims table into namespace vs bound-volume misses.
func classifyPVCFKError(err error, namespaceID uuid.UUID, boundVolumeID *uuid.UUID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return nil
	}
	if strings.Contains(pgErr.ConstraintName, "bound_volume_id") {
		target := nilUUIDDisplay
		if boundVolumeID != nil {
			target = boundVolumeID.String()
		}
		return fmt.Errorf("persistent volume %s does not exist: %w", target, api.ErrNotFound)
	}
	return fmt.Errorf("namespace %s does not exist: %w", namespaceID, api.ErrNotFound)
}

// CreatePersistentVolumeClaim inserts a namespace-scoped PVC.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreatePersistentVolumeClaim(ctx context.Context, in api.PersistentVolumeClaimCreate) (api.PersistentVolumeClaim, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolumeClaim{}, err
	}

	const q = `
		INSERT INTO persistent_volume_claims (
			id, namespace_id, name, phase, storage_class_name,
			volume_name, bound_volume_id, access_modes, requested_storage,
			labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name,
		in.Phase, in.StorageClassName,
		in.VolumeName, in.BoundVolumeId,
		accessModesValue(in.AccessModes), in.RequestedStorage,
		labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "23505" {
				return api.PersistentVolumeClaim{}, fmt.Errorf("pvc %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
			}
			if pErr := classifyPVCFKError(err, in.NamespaceId, in.BoundVolumeId); pErr != nil {
				return api.PersistentVolumeClaim{}, pErr
			}
		}
		return api.PersistentVolumeClaim{}, fmt.Errorf("insert pvc: %w", err)
	}

	return api.PersistentVolumeClaim{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		Phase:            in.Phase,
		StorageClassName: in.StorageClassName,
		VolumeName:       in.VolumeName,
		BoundVolumeId:    in.BoundVolumeId,
		AccessModes:      in.AccessModes,
		RequestedStorage: in.RequestedStorage,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}, nil
}

// PVC read-side projection with parent-name JOINs (ADR-0027).
const pvcSelectColumns = `pvc.id, pvc.namespace_id, pvc.name, pvc.phase, pvc.storage_class_name,
	pvc.volume_name, pvc.bound_volume_id, pvc.access_modes, pvc.requested_storage,
	pvc.labels, pvc.created_at, pvc.updated_at,
	n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name`

const pvcFromJoined = `FROM persistent_volume_claims pvc
	LEFT JOIN namespaces n ON n.id = pvc.namespace_id
	LEFT JOIN clusters   c ON c.id = n.cluster_id`

// GetPersistentVolumeClaim fetches a PVC by id.
func (p *PG) GetPersistentVolumeClaim(ctx context.Context, id uuid.UUID) (api.PersistentVolumeClaim, error) {
	q := `SELECT ` + pvcSelectColumns + ` ` + pvcFromJoined + ` WHERE pvc.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	pvc, err := scanPersistentVolumeClaim(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PersistentVolumeClaim{}, api.ErrNotFound
		}
		return api.PersistentVolumeClaim{}, fmt.Errorf("select pvc: %w", err)
	}
	return pvc, nil
}

// pvcSortSpec is the sort=<key> allowlist for GET /v1/persistentvolumeclaims.
//
// capacity / requested_storage are TEXT ("10Gi") — sort is
// lexicographic on the stored value (native-column semantics, spec
// decision #3); numeric ordering would need a computed column.
var pvcSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:             {expr: "LOWER(pvc.name)", kind: sortText},
		sortKeyPhase:            {expr: "LOWER(pvc.phase)", kind: sortText, nullable: true},
		sortKeyStorageClassName: {expr: "LOWER(pvc.storage_class_name)", kind: sortText, nullable: true},
		sortKeyRequestedStorage: {expr: "LOWER(pvc.requested_storage)", kind: sortText, nullable: true},
		sortKeyCreatedAt:        {expr: "pvc.created_at", kind: sortTime},
		sortKeyUpdatedAt:        {expr: "pvc.updated_at", kind: sortTime},
	},
	defaultKey: sortKeyCreatedAt,
}

// pvcSortVal extracts the serialized sort value for cursor minting.
func pvcSortVal(pvc *api.PersistentVolumeClaim, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&pvc.Name)
	case sortKeyPhase:
		return sortValText(pvc.Phase)
	case sortKeyStorageClassName:
		return sortValText(pvc.StorageClassName)
	case sortKeyRequestedStorage:
		return sortValText(pvc.RequestedStorage)
	case sortKeyUpdatedAt:
		return sortValTime(pvc.UpdatedAt)
	default: // created_at
		return sortValTime(pvc.CreatedAt)
	}
}

// ListPersistentVolumeClaims returns a cursor-paginated page of PVCs, optionally
// filtered by namespace id and/or name.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPersistentVolumeClaims(
	ctx context.Context, filter api.PersistentVolumeClaimListFilter, page api.ListPage,
) ([]api.PersistentVolumeClaim, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := pvcSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(pvcSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(pvcFromJoined)
	args := make([]any, 0, 6)
	conds := make([]string, 0, 4)

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("pvc.namespace_id = $%d", len(args)))
	}
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(pvc.name) LIKE $%d ESCAPE '\\'", len(args)))
	}
	if page.Cursor != "" {
		val, cid, err := decodeListCursor(page.Cursor, key, dir)
		if err != nil {
			return nil, "", err
		}
		if err := keysetCond(col, "pvc.id", dir, val, cid, &conds, &args); err != nil {
			return nil, "", err
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "pvc.id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query pvcs: %w", err)
	}
	defer rows.Close()

	items := make([]api.PersistentVolumeClaim, 0, limit)
	for rows.Next() {
		pvc, err := scanPersistentVolumeClaim(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan pvc: %w", err)
		}
		items = append(items, pvc)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate pvcs: %w", err)
	}

	var next string
	if len(items) > limit {
		last := &items[limit-1]
		if last.Id != nil {
			next = encodeListCursor(key, pvcSortVal(last, key), *last.Id, dir)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdatePersistentVolumeClaim applies merge-patch on mutable PVC fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdatePersistentVolumeClaim(ctx context.Context, id uuid.UUID, in api.PersistentVolumeClaimUpdate) (api.PersistentVolumeClaim, error) {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 10)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.StorageClassName != nil {
		appendSet("storage_class_name", *in.StorageClassName)
	}
	if in.VolumeName != nil {
		appendSet("volume_name", *in.VolumeName)
	}
	if in.BoundVolumeId != nil {
		appendSet("bound_volume_id", *in.BoundVolumeId)
	}
	if in.AccessModes != nil {
		appendSet("access_modes", accessModesValue(in.AccessModes))
	}
	if in.RequestedStorage != nil {
		appendSet("requested_storage", *in.RequestedStorage)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.PersistentVolumeClaim{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE persistent_volume_claims SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		if pErr := classifyPVCFKError(err, uuid.Nil, in.BoundVolumeId); pErr != nil {
			return api.PersistentVolumeClaim{}, pErr
		}
		return api.PersistentVolumeClaim{}, fmt.Errorf("update pvc: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.PersistentVolumeClaim{}, api.ErrNotFound
	}
	return p.GetPersistentVolumeClaim(ctx, id)
}

// DeletePersistentVolumeClaim removes a PVC by id.
func (p *PG) DeletePersistentVolumeClaim(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM persistent_volume_claims WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete pvc: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertPersistentVolumeClaim inserts-or-updates a PVC keyed by (namespace_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertPersistentVolumeClaim(
	ctx context.Context,
	in api.PersistentVolumeClaimCreate,
) (api.PersistentVolumeClaim, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolumeClaim{}, api.OutcomeNoChange, err
	}

	// AUDIT_BUSINESS_FIELDS: phase, storage_class_name, volume_name,
	// bound_volume_id, access_modes, requested_storage, labels.
	// updated_at is a clock field — excluded.
	const q = `
		WITH old AS (
		  SELECT phase, storage_class_name, volume_name, bound_volume_id,
		         access_modes, requested_storage, labels
		    FROM persistent_volume_claims WHERE namespace_id=$2 AND name=$3
		),
		upserted AS (
		  INSERT INTO persistent_volume_claims (
		    id, namespace_id, name, phase, storage_class_name,
		    volume_name, bound_volume_id, access_modes, requested_storage,
		    labels, created_at, updated_at
		  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
		  ON CONFLICT (namespace_id, name) DO UPDATE SET
		      phase              = EXCLUDED.phase,
		      storage_class_name = EXCLUDED.storage_class_name,
		      volume_name        = EXCLUDED.volume_name,
		      bound_volume_id    = EXCLUDED.bound_volume_id,
		      access_modes       = EXCLUDED.access_modes,
		      requested_storage  = EXCLUDED.requested_storage,
		      labels             = EXCLUDED.labels,
		      updated_at         = EXCLUDED.updated_at
		  RETURNING id, namespace_id, name, phase, storage_class_name,
		            volume_name, bound_volume_id, access_modes, requested_storage,
		            labels, created_at, updated_at, xmax
		)
		SELECT u.id, u.namespace_id, u.name, u.phase, u.storage_class_name,
		       u.volume_name, u.bound_volume_id, u.access_modes, u.requested_storage,
		       u.labels, u.created_at, u.updated_at,
		       n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.phase              IS DISTINCT FROM u.phase              OR
		           o.storage_class_name IS DISTINCT FROM u.storage_class_name OR
		           o.volume_name        IS DISTINCT FROM u.volume_name        OR
		           o.bound_volume_id    IS DISTINCT FROM u.bound_volume_id    OR
		           o.access_modes       IS DISTINCT FROM u.access_modes       OR
		           o.requested_storage  IS DISTINCT FROM u.requested_storage  OR
		           o.labels             IS DISTINCT FROM u.labels
		       )) AS business_changed
		  FROM upserted u
		  LEFT JOIN old o        ON true
		  LEFT JOIN namespaces n ON n.id = u.namespace_id
		  LEFT JOIN clusters   c ON c.id = n.cluster_id
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name,
		in.Phase, in.StorageClassName,
		in.VolumeName, in.BoundVolumeId,
		accessModesValue(in.AccessModes), in.RequestedStorage,
		labelsJSON, now,
	)
	var inserted, businessChanged bool
	pvc, err := scanPersistentVolumeClaim(scanRowWith{row: row, extra: []any{&inserted, &businessChanged}})
	if err != nil {
		if pErr := classifyPVCFKError(err, in.NamespaceId, in.BoundVolumeId); pErr != nil {
			return api.PersistentVolumeClaim{}, api.OutcomeNoChange, pErr
		}
		return api.PersistentVolumeClaim{}, api.OutcomeNoChange, fmt.Errorf("upsert pvc: %w", err)
	}
	return pvc, classifyOutcome(inserted, businessChanged), nil
}

// DeletePersistentVolumeClaimsNotIn removes namespace-scoped PVCs whose name
// is not in keepNames.
func (p *PG) DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM persistent_volume_claims
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete pvcs not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanPersistentVolumeClaim(row pgx.Row) (api.PersistentVolumeClaim, error) {
	var (
		pvc                api.PersistentVolumeClaim
		id                 uuid.UUID
		namespaceID        uuid.UUID
		createdAt          time.Time
		updatedAt          time.Time
		phase              sql.NullString
		storageClassName   sql.NullString
		volumeName         sql.NullString
		boundVolumeID      *uuid.UUID
		accessModes        []string
		requestedStorage   sql.NullString
		labelsJSON         []byte
		namespaceName      sql.NullString
		namespaceClusterID uuid.NullUUID
		clusterName        sql.NullString
	)
	if err := row.Scan(
		&id, &namespaceID, &pvc.Name,
		&phase, &storageClassName,
		&volumeName, &boundVolumeID,
		&accessModes, &requestedStorage,
		&labelsJSON,
		&createdAt, &updatedAt,
		&namespaceName, &namespaceClusterID, &clusterName,
	); err != nil {
		return api.PersistentVolumeClaim{}, fmt.Errorf("scan pvc: %w", err)
	}
	pvc.Id = &id
	pvc.NamespaceId = namespaceID
	pvc.CreatedAt = &createdAt
	pvc.UpdatedAt = &updatedAt
	pvc.Phase = nullableString(phase)
	pvc.StorageClassName = nullableString(storageClassName)
	pvc.VolumeName = nullableString(volumeName)
	pvc.BoundVolumeId = boundVolumeID
	pvc.AccessModes = accessModesPointer(accessModes)
	pvc.RequestedStorage = nullableString(requestedStorage)
	pvc.NamespaceName = nullableString(namespaceName)
	if namespaceClusterID.Valid {
		v := namespaceClusterID.UUID
		pvc.ClusterId = &v
	}
	pvc.ClusterName = nullableString(clusterName)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.PersistentVolumeClaim{}, fmt.Errorf("unmarshal pvc labels: %w", err)
		}
		if len(labels) > 0 {
			pvc.Labels = &labels
		}
	}
	return pvc, nil
}

// --- Settings ------------------------------------------------------------
