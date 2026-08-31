// Cluster CRUD + idempotent EnsureCluster + cascade soft-delete.
// Split out of pg.go; shared helpers (cursors, scanUUIDs, JSON
// marshalling) stay in pg.go.
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

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
	"github.com/sthalbert/longue-vue/internal/timetravel"
)

// EnsureCluster is idempotent on the cluster name. It has three branches:
//
//   - RESTORE — a terminated row already exists. Clear terminated_at,
//     write a `restore` history row, return the existing row. Mirrors
//     the auto-restore behaviour of UpsertNamespace/Node/Workload so a
//     collector that keeps pushing after a soft-delete resurrects the
//     cluster instead of being silently ignored (Fix 2 in the spec at
//     docs/superpowers/specs/2026-05-18-cluster-cascade-delete-design.md).
//   - NO-OP — a live row already exists. Return it unchanged. No history.
//   - CREATE — no row exists. INSERT and capture create history.
//
// The returned bool is true only on CREATE. Concurrent inserts of the
// same name are resolved by INSERT … ON CONFLICT (name) DO NOTHING:
// the loser falls through to a re-fetch (still false).
//
//nolint:gocyclo,gocritic // branch-heavy idempotent insert; hugeParam: Store interface
func (p *PG) EnsureCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, bool, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Cluster{}, false, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return api.Cluster{}, false, fmt.Errorf("marshal cluster annotations: %w", err)
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return api.Cluster{}, false, fmt.Errorf("begin ensure cluster: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	actor := timetravel.ActorFromContext(ctx)

	// Snapshot any existing row by name so we can pick a branch.
	var prevID *uuid.UUID
	var prevTerminatedAt *time.Time
	_ = tx.QueryRow(ctx,
		`SELECT id, terminated_at FROM clusters WHERE name=$1`,
		in.Name,
	).Scan(&prevID, &prevTerminatedAt)

	// Branch RESTORE: terminated row exists → clear terminated_at + history.
	if prevID != nil && prevTerminatedAt != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE clusters SET terminated_at = NULL, updated_at = $1, last_seen_at = $1 WHERE id = $2`,
			now, *prevID); err != nil {
			return api.Cluster{}, false, fmt.Errorf("restore cluster: %w", err)
		}
		if snap, err := clusterRowMapNoLock(ctx, tx, *prevID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindCluster, *prevID, nil, snap, changeTypeRestore, actor)
		}
		if err := tx.Commit(ctx); err != nil {
			return api.Cluster{}, false, fmt.Errorf("commit ensure cluster (restore): %w", err)
		}
		restored, getErr := p.GetClusterByName(ctx, in.Name)
		if getErr != nil {
			return api.Cluster{}, false, fmt.Errorf("ensure cluster %q: fetch restored: %w", in.Name, getErr)
		}
		return restored, false, nil
	}

	// Branch NO-OP: live row exists → refresh the collector heartbeat only.
	// last_seen_at is timetravel-excluded (watched.go), so this write never
	// creates a history row, and updated_at keeps meaning "data changed".
	if prevID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE clusters SET last_seen_at = $1 WHERE id = $2`,
			now, *prevID); err != nil {
			return api.Cluster{}, false, fmt.Errorf("touch cluster heartbeat: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return api.Cluster{}, false, fmt.Errorf("commit ensure cluster (existing): %w", err)
		}
		existing, getErr := p.GetClusterByName(ctx, in.Name)
		if getErr != nil {
			return api.Cluster{}, false, fmt.Errorf("ensure cluster %q: fetch existing: %w", in.Name, getErr)
		}
		return existing, false, nil
	}

	// Branch CREATE: insert. Use ON CONFLICT DO NOTHING to absorb a race
	// where two transactions reach this point with the same name.
	const q = `
		INSERT INTO clusters (
			id, name, display_name, environment, provider, region,
			kubernetes_version, api_endpoint, labels,
			owner, criticality, notes, runbook_url, annotations,
			created_at, updated_at, last_seen_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15, $15)
		ON CONFLICT (name) DO NOTHING
		RETURNING id
	`
	var insertedID uuid.UUID
	scanErr := tx.QueryRow(ctx, q,
		id, in.Name, in.DisplayName, in.Environment, in.Provider, in.Region,
		in.KubernetesVersion, in.ApiEndpoint, labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON,
		now,
	).Scan(&insertedID)
	switch {
	case scanErr == nil:
		if snap, err := clusterRowMapNoLock(ctx, tx, insertedID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindCluster, insertedID, nil, snap, changeTypeCreate, actor)
		}
		if err := tx.Commit(ctx); err != nil {
			return api.Cluster{}, false, fmt.Errorf("commit ensure cluster: %w", err)
		}
		return api.Cluster{
			Id:                &insertedID,
			Name:              in.Name,
			DisplayName:       in.DisplayName,
			Environment:       in.Environment,
			Provider:          in.Provider,
			Region:            in.Region,
			KubernetesVersion: in.KubernetesVersion,
			ApiEndpoint:       in.ApiEndpoint,
			Labels:            in.Labels,
			Owner:             in.Owner,
			Criticality:       in.Criticality,
			Notes:             in.Notes,
			RunbookUrl:        in.RunbookUrl,
			Annotations:       in.Annotations,
			CreatedAt:         &now,
			UpdatedAt:         &now,
			LastSeenAt:        &now,
		}, true, nil
	case errors.Is(scanErr, pgx.ErrNoRows):
		// Race: a concurrent tx inserted between our pre-snapshot and INSERT.
		// Commit (the tx has no effect) and re-fetch.
		if err := tx.Commit(ctx); err != nil {
			return api.Cluster{}, false, fmt.Errorf("commit ensure cluster (race): %w", err)
		}
		existing, getErr := p.GetClusterByName(ctx, in.Name)
		if getErr != nil {
			return api.Cluster{}, false, fmt.Errorf("ensure cluster %q: fetch existing after race: %w", in.Name, getErr)
		}
		return existing, false, nil
	default:
		return api.Cluster{}, false, fmt.Errorf("insert cluster: %w", scanErr)
	}
}

// GetCluster fetches a cluster by id.
func (p *PG) GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       created_at, updated_at, last_seen_at
		FROM clusters
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	c, err := scanCluster(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Cluster{}, api.ErrNotFound
		}
		return api.Cluster{}, fmt.Errorf("select cluster: %w", err)
	}
	return c, nil
}

// GetClusterByName fetches a cluster by its unique name column.
func (p *PG) GetClusterByName(ctx context.Context, name string) (api.Cluster, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       created_at, updated_at, last_seen_at
		FROM clusters
		WHERE name = $1
	`
	row := p.pool.QueryRow(ctx, q, name)
	c, err := scanCluster(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Cluster{}, api.ErrNotFound
		}
		return api.Cluster{}, fmt.Errorf("select cluster by name: %w", err)
	}
	return c, nil
}

// clusterSortSpec is the sort=<key> allowlist for GET /v1/clusters.
var clusterSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:              {expr: "LOWER(name)", kind: sortText},
		sortKeyEnvironment:       {expr: "LOWER(environment)", kind: sortText, nullable: true},
		sortKeyProvider:          {expr: "LOWER(provider)", kind: sortText, nullable: true},
		sortKeyRegion:            {expr: "LOWER(region)", kind: sortText, nullable: true},
		sortKeyKubernetesVersion: {expr: "LOWER(kubernetes_version)", kind: sortText, nullable: true},
		sortKeyCreatedAt:         {expr: "created_at", kind: sortTime},
		sortKeyUpdatedAt:         {expr: "updated_at", kind: sortTime},
		sortKeyLastSeenAt:        {expr: "last_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyCreatedAt,
}

func clusterSortVal(c *api.Cluster, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&c.Name)
	case sortKeyEnvironment:
		return sortValText(c.Environment)
	case sortKeyProvider:
		return sortValText(c.Provider)
	case sortKeyRegion:
		return sortValText(c.Region)
	case sortKeyKubernetesVersion:
		return sortValText(c.KubernetesVersion)
	case sortKeyUpdatedAt:
		return sortValTime(c.UpdatedAt)
	case sortKeyLastSeenAt:
		return sortValTime(c.LastSeenAt)
	default:
		return sortValTime(c.CreatedAt)
	}
}

// ListClusters returns up to page.Limit clusters. Default order is the
// historical (created_at DESC, id DESC); sort/order/name follow the
// uniform list contract (ADR-0042).
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListClusters(ctx context.Context, filter api.ClusterListFilter, page api.ListPage) ([]api.Cluster, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := clusterSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, name, display_name, environment, provider, region,
	       kubernetes_version, api_endpoint, labels,
	       owner, criticality, notes, runbook_url, annotations,
	       created_at, updated_at, last_seen_at
	FROM clusters`)
	args := make([]any, 0, 5)
	conds := make([]string, 0, 3)

	if !filter.IncludeTerminated {
		conds = append(conds, "terminated_at IS NULL")
	}
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, namePattern(*filter.Name))
		idx := len(args)
		conds = append(conds, fmt.Sprintf(
			"(LOWER(name) LIKE $%d ESCAPE '\\' OR LOWER(COALESCE(display_name,'')) LIKE $%d ESCAPE '\\')",
			idx, idx,
		))
	}
	if filter.Stale != nil {
		args = append(args, filter.StaleCutoff)
		idx := len(args)
		if *filter.Stale {
			conds = append(conds, fmt.Sprintf("last_seen_at < $%d", idx))
		} else {
			conds = append(conds, fmt.Sprintf("last_seen_at >= $%d", idx))
		}
	}
	if page.Cursor != "" {
		val, cid, err := decodeListCursor(page.Cursor, key, dir)
		if err != nil {
			return nil, "", err
		}
		if err := keysetCond(col, "id", dir, val, cid, &conds, &args); err != nil {
			return nil, "", err
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query clusters: %w", err)
	}
	defer rows.Close()

	items := make([]api.Cluster, 0, limit)
	for rows.Next() {
		c, err := scanCluster(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan cluster: %w", err)
		}
		items = append(items, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate clusters: %w", err)
	}

	var next string
	if len(items) > limit {
		last := &items[limit-1]
		if last.Id != nil {
			next = encodeListCursor(key, clusterSortVal(last, key), *last.Id, dir)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateCluster applies merge-patch semantics: only fields that are non-nil
// on ClusterUpdate are written. updated_at is always refreshed.
//
//nolint:gocyclo,gocritic // merge-patch nil checks are inherently repetitive; hugeParam: Store interface requires value param
func (p *PG) UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 10)
	idx := 1

	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Environment != nil {
		appendSet("environment", *in.Environment)
	}
	if in.Provider != nil {
		appendSet("provider", *in.Provider)
	}
	if in.Region != nil {
		appendSet("region", *in.Region)
	}
	if in.KubernetesVersion != nil {
		appendSet("kubernetes_version", *in.KubernetesVersion)
	}
	if in.ApiEndpoint != nil {
		appendSet("api_endpoint", *in.ApiEndpoint)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Cluster{}, err
		}
		appendSet("labels", b)
	}
	// Curated metadata — never written by the collector (it only ever
	// patches KubernetesVersion), so omission here is already safe.
	if in.Owner != nil {
		appendSet("owner", *in.Owner)
	}
	if in.Criticality != nil {
		appendSet("criticality", *in.Criticality)
	}
	if in.Notes != nil {
		appendSet("notes", *in.Notes)
	}
	if in.RunbookUrl != nil {
		appendSet("runbook_url", *in.RunbookUrl)
	}
	if in.Annotations != nil {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.Cluster{}, fmt.Errorf("marshal cluster annotations: %w", err)
		}
		appendSet("annotations", b)
	}

	appendSet("updated_at", time.Now().UTC())

	args = append(args, id)

	// Wrap in a transaction so we can read prev, run the UPDATE, read next,
	// and call Capture atomically.
	if err := p.withTx(ctx, "update cluster", func(tx pgx.Tx) error {
		prev, prevErr := clusterRowMap(ctx, tx, id) // FOR UPDATE lock
		if prevErr != nil {
			return api.ErrNotFound
		}

		q := fmt.Sprintf("UPDATE clusters SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
		tag, err := tx.Exec(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("update cluster: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return api.ErrNotFound
		}

		next, nextErr := clusterRowMapNoLock(ctx, tx, id)
		if nextErr == nil {
			actor := timetravel.ActorFromContext(ctx)
			_ = timetravel.Capture(ctx, tx, timetravel.KindCluster, id, prev, next, changeTypeUpdate, actor)
		}
		return nil
	}); err != nil {
		return api.Cluster{}, err
	}
	return p.GetCluster(ctx, id)
}

// DeleteCluster removes a cluster by id.
func (p *PG) DeleteCluster(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM clusters WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete cluster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// SoftDeleteCluster marks the cluster and all its live children
// (namespaces, nodes, workloads) as terminated in a single transaction.
// Mirrors ADR-0021 §IMP-007. Children that are already terminated are
// skipped via the AND terminated_at IS NULL guard.
//
// Pods, services, ingresses, PVCs (per namespace) and PVs (per cluster)
// are out-of-scope for time-travel history (ADR-0021 §IMP-007) and are
// HARD-DELETED here. Without this step they would remain in the DB
// attached to a terminated parent and still be queryable.
func (p *PG) SoftDeleteCluster(ctx context.Context, id uuid.UUID) error {
	return p.withTx(ctx, "soft-delete cluster", func(tx pgx.Tx) error {
		return softDeleteClusterInTx(ctx, tx, id)
	})
}

// softDeleteClusterInTx runs the cascade inside the caller's transaction:
// hard-delete the unhistoried children, soft-delete
// workloads/namespaces/nodes/cluster, then capture history rows.
//
//nolint:gocyclo // cascade + history capture per entity-kind add branches; acceptable here
func softDeleteClusterInTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	actor := timetravel.ActorFromContext(ctx)

	// Collect live workload IDs before soft-deleting them so we can write history.
	wlIDs, err := liveWorkloadIDsForCluster(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("list workloads for soft-delete cluster: %w", err)
	}
	nsIDs, err := liveNamespaceIDsForCluster(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("list namespaces for soft-delete cluster: %w", err)
	}
	nodeIDs, err := liveNodeIDsForCluster(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("list nodes for soft-delete cluster: %w", err)
	}

	// Hard-delete the unhistoried children first. These tables have ON
	// DELETE CASCADE from cluster/namespace, but FK CASCADE only fires
	// on hard-delete; since we soft-delete the parent it never fires.
	if _, err := tx.Exec(ctx,
		`DELETE FROM pods
		   WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)`, id); err != nil {
		return fmt.Errorf("cascade-delete pods: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM services
		   WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)`, id); err != nil {
		return fmt.Errorf("cascade-delete services: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM ingresses
		   WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)`, id); err != nil {
		return fmt.Errorf("cascade-delete ingresses: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM persistent_volume_claims
		   WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)`, id); err != nil {
		return fmt.Errorf("cascade-delete persistent_volume_claims: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM persistent_volumes WHERE cluster_id = $1`, id); err != nil {
		return fmt.Errorf("cascade-delete persistent_volumes: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE workloads SET terminated_at = NOW(), updated_at = NOW()
		   WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)
		     AND terminated_at IS NULL`, id); err != nil {
		return fmt.Errorf("soft-delete workloads: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE namespaces SET terminated_at = NOW(), updated_at = NOW()
		   WHERE cluster_id = $1 AND terminated_at IS NULL`, id); err != nil {
		return fmt.Errorf("soft-delete namespaces: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE nodes SET terminated_at = NOW(), updated_at = NOW()
		   WHERE cluster_id = $1 AND terminated_at IS NULL`, id); err != nil {
		return fmt.Errorf("soft-delete nodes: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`UPDATE clusters SET terminated_at = NOW(), updated_at = NOW()
		   WHERE id = $1 AND terminated_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("soft-delete cluster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM clusters WHERE id = $1)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("check cluster exists: %w", err)
		}
		if !exists {
			return api.ErrNotFound
		}
		// Already terminated → idempotent success.
	}

	// Capture history for cascade-affected entities (after UPDATEs so
	// the rows reflect terminated_at).
	for _, wlID := range wlIDs {
		if snap, err := workloadRowMapNoLock(ctx, tx, wlID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindWorkload, wlID, nil, snap, changeTypeSoftDelete, actor)
		}
	}
	for _, nsID := range nsIDs {
		if snap, err := namespaceRowMapNoLock(ctx, tx, nsID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindNamespace, nsID, nil, snap, changeTypeSoftDelete, actor)
		}
	}
	for _, nodeID := range nodeIDs {
		if snap, err := nodeRowMapNoLock(ctx, tx, nodeID); err == nil {
			_ = timetravel.Capture(ctx, tx, timetravel.KindNode, nodeID, nil, snap, changeTypeSoftDelete, actor)
		}
	}
	if snap, err := clusterRowMapNoLock(ctx, tx, id); err == nil {
		_ = timetravel.Capture(ctx, tx, timetravel.KindCluster, id, nil, snap, changeTypeSoftDelete, actor)
	}

	return nil
}

// liveWorkloadIDsForCluster returns ids of non-terminated workloads in namespaces of cluster.
func liveWorkloadIDsForCluster(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM workloads
		  WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)
		    AND terminated_at IS NULL`,
		clusterID)
	if err != nil {
		return nil, fmt.Errorf("query live workloads for cluster: %w", err)
	}
	return scanUUIDs(rows)
}

// liveNamespaceIDsForCluster returns ids of non-terminated namespaces of cluster.
func liveNamespaceIDsForCluster(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM namespaces WHERE cluster_id = $1 AND terminated_at IS NULL`,
		clusterID)
	if err != nil {
		return nil, fmt.Errorf("query live namespaces for cluster: %w", err)
	}
	return scanUUIDs(rows)
}

// liveNodeIDsForCluster returns ids of non-terminated nodes of cluster.
func liveNodeIDsForCluster(ctx context.Context, tx pgx.Tx, clusterID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx,
		`SELECT id FROM nodes WHERE cluster_id = $1 AND terminated_at IS NULL`,
		clusterID)
	if err != nil {
		return nil, fmt.Errorf("query live nodes for cluster: %w", err)
	}
	return scanUUIDs(rows)
}

// CountClusterChildren counts every child resource attached to the cluster
// at the time of the call. Used by the audit log to snapshot the pre-delete
// inventory before SoftDeleteCluster removes pods / services / ingresses /
// PVCs / PVs (Fix 1, see SoftDeleteCluster) and soft-deletes the cluster /
// namespaces / nodes / workloads. A single round-trip multi-CTE query keeps
// the cost bounded regardless of how many resource types exist (ADR-0010).
func (p *PG) CountClusterChildren(ctx context.Context, clusterID uuid.UUID) (api.CascadeCounts, error) {
	const q = `
		WITH ns_ids AS (
			SELECT id FROM namespaces WHERE cluster_id = $1
		),
		ns_count   AS (SELECT COUNT(*) AS c FROM ns_ids),
		node_count AS (SELECT COUNT(*) AS c FROM nodes WHERE cluster_id = $1),
		pv_count   AS (SELECT COUNT(*) AS c FROM persistent_volumes WHERE cluster_id = $1),
		pod_count  AS (SELECT COUNT(*) AS c FROM pods WHERE namespace_id IN (SELECT id FROM ns_ids)),
		wl_count   AS (SELECT COUNT(*) AS c FROM workloads WHERE namespace_id IN (SELECT id FROM ns_ids)),
		svc_count  AS (SELECT COUNT(*) AS c FROM services WHERE namespace_id IN (SELECT id FROM ns_ids)),
		ing_count  AS (SELECT COUNT(*) AS c FROM ingresses WHERE namespace_id IN (SELECT id FROM ns_ids)),
		pvc_count  AS (SELECT COUNT(*) AS c FROM persistent_volume_claims WHERE namespace_id IN (SELECT id FROM ns_ids))
		SELECT
			(SELECT c FROM ns_count),
			(SELECT c FROM node_count),
			(SELECT c FROM pod_count),
			(SELECT c FROM wl_count),
			(SELECT c FROM svc_count),
			(SELECT c FROM ing_count),
			(SELECT c FROM pv_count),
			(SELECT c FROM pvc_count)
	`
	// Verify the cluster exists before running counts; a non-existent
	// cluster would just return all zeroes, which is misleading.
	var exists bool
	if err := p.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM clusters WHERE id=$1)", clusterID).Scan(&exists); err != nil {
		return api.CascadeCounts{}, fmt.Errorf("count cluster children: existence check: %w", err)
	}
	if !exists {
		return api.CascadeCounts{}, api.ErrNotFound
	}

	var cc api.CascadeCounts
	err := p.pool.QueryRow(ctx, q, clusterID).Scan(
		&cc.Namespaces,
		&cc.Nodes,
		&cc.Pods,
		&cc.Workloads,
		&cc.Services,
		&cc.Ingresses,
		&cc.PersistentVolumes,
		&cc.PersistentVolumeClaims,
	)
	if err != nil {
		return api.CascadeCounts{}, fmt.Errorf("count cluster children: %w", err)
	}
	return cc, nil
}

func scanCluster(row pgx.Row) (api.Cluster, error) {
	var (
		c                 api.Cluster
		id                uuid.UUID
		createdAt         time.Time
		updatedAt         time.Time
		lastSeenAt        time.Time
		displayName       sql.NullString
		environment       sql.NullString
		provider          sql.NullString
		region            sql.NullString
		kubernetesVersion sql.NullString
		apiEndpoint       sql.NullString
		labelsJSON        []byte
		owner             sql.NullString
		criticality       sql.NullString
		notes             sql.NullString
		runbookURL        sql.NullString
		annotationsJSON   []byte
	)
	if err := row.Scan(
		&id, &c.Name,
		&displayName, &environment, &provider, &region,
		&kubernetesVersion, &apiEndpoint,
		&labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON,
		&createdAt, &updatedAt, &lastSeenAt,
	); err != nil {
		return api.Cluster{}, fmt.Errorf("scan cluster: %w", err)
	}

	c.Id = &id
	c.CreatedAt = &createdAt
	c.UpdatedAt = &updatedAt
	c.LastSeenAt = &lastSeenAt
	c.DisplayName = nullableString(displayName)
	c.Environment = nullableString(environment)
	c.Provider = nullableString(provider)
	c.Region = nullableString(region)
	c.KubernetesVersion = nullableString(kubernetesVersion)
	c.ApiEndpoint = nullableString(apiEndpoint)
	c.Owner = nullableString(owner)
	c.Criticality = nullableString(criticality)
	c.Notes = nullableString(notes)
	c.RunbookUrl = nullableString(runbookURL)

	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Cluster{}, fmt.Errorf("unmarshal labels: %w", err)
		}
		if len(labels) > 0 {
			c.Labels = &labels
		}
	}
	if len(annotationsJSON) > 0 {
		var annotations map[string]string
		if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
			return api.Cluster{}, fmt.Errorf("unmarshal annotations: %w", err)
		}
		if len(annotations) > 0 {
			c.Annotations = &annotations
		}
	}
	return c, nil
}

// ClusterHeartbeats returns (name, last_seen_at) for every live cluster,
// feeding the metrics refresher. Terminated clusters are excluded — their
// silence is expected.
func (p *PG) ClusterHeartbeats(ctx context.Context) ([]metrics.ClusterHeartbeat, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT name, last_seen_at FROM clusters WHERE terminated_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("query cluster heartbeats: %w", err)
	}
	defer rows.Close()

	var out []metrics.ClusterHeartbeat
	for rows.Next() {
		var hb metrics.ClusterHeartbeat
		if err := rows.Scan(&hb.Name, &hb.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan cluster heartbeat: %w", err)
		}
		out = append(out, hb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cluster heartbeats: %w", err)
	}
	return out, nil
}
