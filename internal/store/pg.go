// Package store provides the PostgreSQL implementation of api.Store.
package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/migrations"
)

// errCursorFormatInvalid is returned when a pagination cursor cannot be decoded.
var errCursorFormatInvalid = errors.New("cursor format invalid")

// PG is a PostgreSQL-backed implementation of api.Store.
type PG struct {
	pool *pgxpool.Pool
}

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
	return &PG{pool: pool}, nil
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

// CreateCluster inserts a new cluster and returns the stored representation.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Cluster{}, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		// marshalLabels' own message says "marshal labels"; rewrap so
		// the operator-facing error points at annotations instead.
		return api.Cluster{}, fmt.Errorf("marshal cluster annotations: %w", err)
	}

	const q = `
		INSERT INTO clusters (
			id, name, display_name, environment, provider, region,
			kubernetes_version, api_endpoint, labels,
			owner, criticality, notes, runbook_url, annotations,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $15)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.Name, in.DisplayName, in.Environment, in.Provider, in.Region,
		in.KubernetesVersion, in.ApiEndpoint, labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON,
		now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.Cluster{}, fmt.Errorf("cluster name %q already exists: %w", in.Name, api.ErrConflict)
		}
		return api.Cluster{}, fmt.Errorf("insert cluster: %w", err)
	}

	return api.Cluster{
		Id:                &id,
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
	}, nil
}

// GetCluster fetches a cluster by id.
func (p *PG) GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       created_at, updated_at
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
		       created_at, updated_at
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

// ListClusters returns up to limit clusters in (created_at DESC, id DESC) order,
// starting after the opaque cursor if non-empty.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListClusters(ctx context.Context, limit int, cursor string) ([]api.Cluster, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var (
		rows pgx.Rows
		err  error
	)
	if cursor == "" {
		const q = `
			SELECT id, name, display_name, environment, provider, region,
			       kubernetes_version, api_endpoint, labels,
			       owner, criticality, notes, runbook_url, annotations,
			       created_at, updated_at
			FROM clusters
			ORDER BY created_at DESC, id DESC
			LIMIT $1
		`
		rows, err = p.pool.Query(ctx, q, limit+1)
	} else {
		ts, id, cerr := decodeCursor(cursor)
		if cerr != nil {
			return nil, "", cerr
		}
		const q = `
			SELECT id, name, display_name, environment, provider, region,
			       kubernetes_version, api_endpoint, labels,
			       owner, criticality, notes, runbook_url, annotations,
			       created_at, updated_at
			FROM clusters
			WHERE (created_at, id) < ($1, $2)
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		`
		rows, err = p.pool.Query(ctx, q, ts, id, limit+1)
	}
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
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
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

	q := fmt.Sprintf("UPDATE clusters SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Cluster{}, fmt.Errorf("update cluster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Cluster{}, api.ErrNotFound
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

// CountClusterChildren counts every child resource that ON DELETE CASCADE
// will remove when the cluster is deleted. A single round-trip multi-CTE
// query keeps the cost bounded regardless of how many resource types
// exist (ADR-0010).
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

// CreateNode inserts a new node. Returns api.ErrNotFound when the parent
// cluster does not exist (FK violation), api.ErrConflict on duplicate
// (cluster_id, name).
// nodeColumns is the INSERT/SELECT column order used by every Node SQL
// path — CreateNode, UpsertNode, scanNode, ListNodes. Kept as a single
// const so adding a field is a three-line change (const + values + scan).
const nodeColumns = `id, cluster_id, name, display_name, role,
	kubelet_version, kube_proxy_version, container_runtime_version,
	os_image, operating_system, kernel_version, architecture,
	internal_ip, external_ip, pod_cidr,
	provider_id, instance_type, zone,
	capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
	allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
	conditions, taints, unschedulable, ready,
	labels,
	owner, criticality, notes, runbook_url, annotations, hardware_model,
	created_at, updated_at`

func nodeInsertValues(in *api.NodeCreate, id uuid.UUID, now time.Time) ([]any, error) {
	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return nil, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return nil, fmt.Errorf("marshal node annotations: %w", err)
	}
	conditionsJSON, err := marshalPorts(in.Conditions)
	if err != nil {
		return nil, err
	}
	taintsJSON, err := marshalPorts(in.Taints)
	if err != nil {
		return nil, err
	}
	return []any{
		id, in.ClusterId, in.Name, in.DisplayName, in.Role,
		in.KubeletVersion, in.KubeProxyVersion, in.ContainerRuntimeVersion,
		in.OsImage, in.OperatingSystem, in.KernelVersion, in.Architecture,
		in.InternalIp, in.ExternalIp, in.PodCidr,
		in.ProviderId, in.InstanceType, in.Zone,
		in.CapacityCpu, in.CapacityMemory, in.CapacityPods, in.CapacityEphemeralStorage,
		in.AllocatableCpu, in.AllocatableMemory, in.AllocatablePods, in.AllocatableEphemeralStorage,
		conditionsJSON, taintsJSON, boolOrFalse(in.Unschedulable), boolOrFalse(in.Ready),
		labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON, in.HardwareModel,
		now,
	}, nil
}

func boolOrFalse(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// CreateNode inserts a new node into the given cluster.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	id := uuid.New()
	now := time.Now().UTC()

	values, err := nodeInsertValues(&in, id, now)
	if err != nil {
		return api.Node{}, err
	}

	// 38 placeholders: 36 "value" slots + created_at + updated_at (both = $38).
	const q = `
		INSERT INTO nodes (` + nodeColumns + `)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$38)
	`
	if _, err := p.pool.Exec(ctx, q, values...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Node{}, fmt.Errorf("node %q in cluster %s already exists: %w", in.Name, in.ClusterId, api.ErrConflict)
			case "23503":
				return api.Node{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.Node{}, fmt.Errorf("insert node: %w", err)
	}

	return p.GetNode(ctx, id)
}

// GetNode fetches a node by id.
func (p *PG) GetNode(ctx context.Context, id uuid.UUID) (api.Node, error) {
	q := `SELECT ` + nodeColumns + ` FROM nodes WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	n, err := scanNode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Node{}, api.ErrNotFound
		}
		return api.Node{}, fmt.Errorf("select node: %w", err)
	}
	return n, nil
}

// ListNodes returns up to limit nodes sorted (created_at DESC, id DESC),
// optionally filtered by cluster id.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Node, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(nodeColumns)
	sb.WriteString(` FROM nodes`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	items := make([]api.Node, 0, limit)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan node: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate nodes: %w", err)
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

// UpdateNode applies merge-patch semantics on mutable fields only. Each
// non-nil pointer on NodeUpdate translates to a single column set; omitted
// fields keep their existing value.
//
//nolint:gocyclo,gocognit,gocritic // merge-patch nil checks are inherently repetitive; hugeParam: Store interface requires value param
func (p *PG) UpdateNode(ctx context.Context, id uuid.UUID, in api.NodeUpdate) (api.Node, error) {
	sets := make([]string, 0, 24)
	args := make([]any, 0, 26)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Role != nil {
		appendSet("role", *in.Role)
	}
	if in.KubeletVersion != nil {
		appendSet("kubelet_version", *in.KubeletVersion)
	}
	if in.KubeProxyVersion != nil {
		appendSet("kube_proxy_version", *in.KubeProxyVersion)
	}
	if in.ContainerRuntimeVersion != nil {
		appendSet("container_runtime_version", *in.ContainerRuntimeVersion)
	}
	if in.OsImage != nil {
		appendSet("os_image", *in.OsImage)
	}
	if in.OperatingSystem != nil {
		appendSet("operating_system", *in.OperatingSystem)
	}
	if in.KernelVersion != nil {
		appendSet("kernel_version", *in.KernelVersion)
	}
	if in.Architecture != nil {
		appendSet("architecture", *in.Architecture)
	}
	if in.InternalIp != nil {
		appendSet("internal_ip", *in.InternalIp)
	}
	if in.ExternalIp != nil {
		appendSet("external_ip", *in.ExternalIp)
	}
	if in.PodCidr != nil {
		appendSet("pod_cidr", *in.PodCidr)
	}
	if in.ProviderId != nil {
		appendSet("provider_id", *in.ProviderId)
	}
	if in.InstanceType != nil {
		appendSet("instance_type", *in.InstanceType)
	}
	if in.Zone != nil {
		appendSet("zone", *in.Zone)
	}
	if in.CapacityCpu != nil {
		appendSet("capacity_cpu", *in.CapacityCpu)
	}
	if in.CapacityMemory != nil {
		appendSet("capacity_memory", *in.CapacityMemory)
	}
	if in.CapacityPods != nil {
		appendSet("capacity_pods", *in.CapacityPods)
	}
	if in.CapacityEphemeralStorage != nil {
		appendSet("capacity_ephemeral_storage", *in.CapacityEphemeralStorage)
	}
	if in.AllocatableCpu != nil {
		appendSet("allocatable_cpu", *in.AllocatableCpu)
	}
	if in.AllocatableMemory != nil {
		appendSet("allocatable_memory", *in.AllocatableMemory)
	}
	if in.AllocatablePods != nil {
		appendSet("allocatable_pods", *in.AllocatablePods)
	}
	if in.AllocatableEphemeralStorage != nil {
		appendSet("allocatable_ephemeral_storage", *in.AllocatableEphemeralStorage)
	}
	if in.Conditions != nil {
		b, err := marshalPorts(in.Conditions)
		if err != nil {
			return api.Node{}, err
		}
		appendSet("conditions", b)
	}
	if in.Taints != nil {
		b, err := marshalPorts(in.Taints)
		if err != nil {
			return api.Node{}, err
		}
		appendSet("taints", b)
	}
	if in.Unschedulable != nil {
		appendSet("unschedulable", *in.Unschedulable)
	}
	if in.Ready != nil {
		appendSet("ready", *in.Ready)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Node{}, err
		}
		appendSet("labels", b)
	}
	// Curated metadata — collector never writes these, so merge-patch
	// omission is enough to keep operator edits safe across polls.
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
			return api.Node{}, fmt.Errorf("marshal node annotations: %w", err)
		}
		appendSet("annotations", b)
	}
	if in.HardwareModel != nil {
		appendSet("hardware_model", *in.HardwareModel)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE nodes SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Node{}, fmt.Errorf("update node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Node{}, api.ErrNotFound
	}
	return p.GetNode(ctx, id)
}

// DeleteNodesNotIn removes every node of the given cluster whose name is not
// in keepNames. Used by the collector to reconcile state after a polling
// cycle. keepNames is allowed to be nil or empty (deletes all for the cluster).
//
// COALESCE guards against pgx encoding a nil []string as SQL NULL: without
// it, 'name <> ALL(NULL)' evaluates to NULL and the DELETE matches nothing
// instead of clearing the cluster's nodes.
func (p *PG) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM nodes
		 WHERE cluster_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		clusterID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete nodes not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteNamespacesNotIn mirrors DeleteNodesNotIn for namespaces.
func (p *PG) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM namespaces
		 WHERE cluster_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		clusterID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete namespaces not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteNode removes a node by id.
func (p *PG) DeleteNode(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM nodes WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// CreateNamespace inserts a new namespace.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Namespace{}, err
	}
	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return api.Namespace{}, fmt.Errorf("marshal namespace annotations: %w", err)
	}

	const q = `
		INSERT INTO namespaces (
			id, cluster_id, name, display_name, phase, labels,
			owner, criticality, notes, runbook_url, annotations,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.Phase, labelsJSON,
		in.Owner, in.Criticality, in.Notes, in.RunbookUrl, annotationsJSON,
		now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Namespace{}, fmt.Errorf("namespace %q in cluster %s already exists: %w", in.Name, in.ClusterId, api.ErrConflict)
			case "23503":
				return api.Namespace{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.Namespace{}, fmt.Errorf("insert namespace: %w", err)
	}

	return api.Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		Owner:       in.Owner,
		Criticality: in.Criticality,
		Notes:       in.Notes,
		RunbookUrl:  in.RunbookUrl,
		Annotations: in.Annotations,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// GetNamespace fetches a namespace by id.
func (p *PG) GetNamespace(ctx context.Context, id uuid.UUID) (api.Namespace, error) {
	const q = `
		SELECT id, cluster_id, name, display_name, phase, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       created_at, updated_at
		FROM namespaces
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	n, err := scanNamespace(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Namespace{}, api.ErrNotFound
		}
		return api.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}
	return n, nil
}

// ListNamespaces returns up to limit namespaces sorted (created_at DESC, id DESC).
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Namespace, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, cluster_id, name, display_name, phase, labels,
	                       owner, criticality, notes, runbook_url, annotations,
	                       created_at, updated_at
	                FROM namespaces`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query namespaces: %w", err)
	}
	defer rows.Close()

	items := make([]api.Namespace, 0, limit)
	for rows.Next() {
		n, err := scanNamespace(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan namespace: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate namespaces: %w", err)
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

// UpdateNamespace applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateNamespace(ctx context.Context, id uuid.UUID, in api.NamespaceUpdate) (api.Namespace, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Namespace{}, err
		}
		appendSet("labels", b)
	}
	// Curated metadata — collector never writes these, so merge-patch
	// omission is enough to keep operator edits safe across polls.
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
			return api.Namespace{}, fmt.Errorf("marshal namespace annotations: %w", err)
		}
		appendSet("annotations", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE namespaces SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Namespace{}, fmt.Errorf("update namespace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Namespace{}, api.ErrNotFound
	}
	return p.GetNamespace(ctx, id)
}

// DeleteNamespace removes a namespace by id.
func (p *PG) DeleteNamespace(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM namespaces WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertNamespace inserts-or-updates a namespace keyed by (cluster_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Namespace{}, err
	}

	const q = `
		INSERT INTO namespaces (
			id, cluster_id, name, display_name, phase,
			labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (cluster_id, name) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			phase        = EXCLUDED.phase,
			labels       = EXCLUDED.labels,
			updated_at   = EXCLUDED.updated_at
		RETURNING id, cluster_id, name, display_name, phase, labels,
		          owner, criticality, notes, runbook_url, annotations,
		          created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.Phase,
		labelsJSON, now,
	)
	n, err := scanNamespace(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Namespace{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
		}
		return api.Namespace{}, fmt.Errorf("upsert namespace: %w", err)
	}
	return n, nil
}

// CreatePod inserts a new pod.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreatePod(ctx context.Context, in api.PodCreate) (api.Pod, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Pod{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Pod{}, err
	}

	const q = `
		INSERT INTO pods (
			id, namespace_id, name, phase, node_name, pod_ip,
			workload_id, containers, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, in.Phase, in.NodeName, in.PodIp,
		in.WorkloadId, containersJSON, labelsJSON, now,
	)
	if err != nil {
		if pErr := classifyPodFKError(err, in.NamespaceId, in.WorkloadId); pErr != nil {
			return api.Pod{}, pErr
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.Pod{}, fmt.Errorf("pod %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
		}
		return api.Pod{}, fmt.Errorf("insert pod: %w", err)
	}

	return api.Pod{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Phase:       in.Phase,
		NodeName:    in.NodeName,
		PodIp:       in.PodIp,
		WorkloadId:  in.WorkloadId,
		Containers:  in.Containers,
		Labels:      in.Labels,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// classifyPodFKError disambiguates 23503 foreign-key violations on the pods
// table into namespace vs workload misses, so the handler can return an
// accurate 404 message. PG auto-names FK constraints <table>_<column>_fkey;
// we match on the column name in pgErr.ConstraintName.
func classifyPodFKError(err error, namespaceID uuid.UUID, workloadID *uuid.UUID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return nil
	}
	if strings.Contains(pgErr.ConstraintName, "workload_id") {
		target := "<nil>"
		if workloadID != nil {
			target = workloadID.String()
		}
		return fmt.Errorf("workload %s does not exist: %w", target, api.ErrNotFound)
	}
	return fmt.Errorf("namespace %s does not exist: %w", namespaceID, api.ErrNotFound)
}

// GetPod fetches a pod by id.
func (p *PG) GetPod(ctx context.Context, id uuid.UUID) (api.Pod, error) {
	const q = `
		SELECT id, namespace_id, name, phase, node_name, pod_ip,
		       workload_id, containers, labels, created_at, updated_at
		FROM pods
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	pod, err := scanPod(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Pod{}, api.ErrNotFound
		}
		return api.Pod{}, fmt.Errorf("select pod: %w", err)
	}
	return pod, nil
}

// ListPods returns up to limit pods, optionally filtered by namespace.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPods(ctx context.Context, filter api.PodListFilter, limit int, cursor string) ([]api.Pod, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, namespace_id, name, phase, node_name, pod_ip,
	                       workload_id, containers, labels, created_at, updated_at
	                FROM pods`)
	args := make([]any, 0, 6)
	conds := make([]string, 0, 4)

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
	}
	if filter.NodeName != nil {
		args = append(args, *filter.NodeName)
		conds = append(conds, fmt.Sprintf("node_name = $%d", len(args)))
	}
	if filter.ImageSubstring != nil && *filter.ImageSubstring != "" {
		// Case-insensitive substring match against any container's `image`
		// string. jsonb_array_elements unpacks the array so we can compare
		// ->>'image' element-by-element; EXISTS short-circuits on first hit.
		// The ILIKE pattern is built with %…% wrapping.
		args = append(args, "%"+*filter.ImageSubstring+"%")
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM jsonb_array_elements(containers) elem WHERE elem->>'image' ILIKE $%d)",
			len(args),
		))
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
		return nil, "", fmt.Errorf("query pods: %w", err)
	}
	defer rows.Close()

	items := make([]api.Pod, 0, limit)
	for rows.Next() {
		pod, err := scanPod(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan pod: %w", err)
		}
		items = append(items, pod)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate pods: %w", err)
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

// UpdatePod applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdatePod(ctx context.Context, id uuid.UUID, in api.PodUpdate) (api.Pod, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.NodeName != nil {
		appendSet("node_name", *in.NodeName)
	}
	if in.PodIp != nil {
		appendSet("pod_ip", *in.PodIp)
	}
	if in.WorkloadId != nil {
		appendSet("workload_id", *in.WorkloadId)
	}
	if in.Containers != nil {
		b, err := marshalPorts(in.Containers)
		if err != nil {
			return api.Pod{}, err
		}
		appendSet("containers", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Pod{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE pods SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		if pErr := classifyPodFKError(err, uuid.Nil, in.WorkloadId); pErr != nil {
			return api.Pod{}, pErr
		}
		return api.Pod{}, fmt.Errorf("update pod: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Pod{}, api.ErrNotFound
	}
	return p.GetPod(ctx, id)
}

// DeletePod removes a pod by id.
func (p *PG) DeletePod(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM pods WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete pod: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertPod inserts-or-updates a pod keyed by (namespace_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Pod{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Pod{}, err
	}

	const q = `
		INSERT INTO pods (
			id, namespace_id, name, phase, node_name, pod_ip,
			workload_id, containers, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (namespace_id, name) DO UPDATE SET
			phase       = EXCLUDED.phase,
			node_name   = EXCLUDED.node_name,
			pod_ip      = EXCLUDED.pod_ip,
			workload_id = EXCLUDED.workload_id,
			containers  = EXCLUDED.containers,
			labels      = EXCLUDED.labels,
			updated_at  = EXCLUDED.updated_at
		RETURNING id, namespace_id, name, phase, node_name, pod_ip,
		          workload_id, containers, labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, in.Phase, in.NodeName, in.PodIp,
		in.WorkloadId, containersJSON, labelsJSON, now,
	)
	pod, err := scanPod(row)
	if err != nil {
		if pErr := classifyPodFKError(err, in.NamespaceId, in.WorkloadId); pErr != nil {
			return api.Pod{}, pErr
		}
		return api.Pod{}, fmt.Errorf("upsert pod: %w", err)
	}
	return pod, nil
}

// DeletePodsNotIn removes every pod in the given namespace whose name is not
// in keepNames. Same COALESCE safety against pgx encoding a nil slice as NULL.
func (p *PG) DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM pods
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete pods not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CreateWorkload inserts a new workload.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Workload{}, err
	}
	specJSON, err := marshalSpec(in.Spec)
	if err != nil {
		return api.Workload{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Workload{}, err
	}

	const q = `
		INSERT INTO workloads (
			id, namespace_id, kind, name, replicas, ready_replicas,
			containers, labels, spec, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, string(in.Kind), in.Name, in.Replicas, in.ReadyReplicas,
		containersJSON, labelsJSON, specJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Workload{}, fmt.Errorf(
					"workload %s/%q in namespace %s already exists: %w",
					in.Kind, in.Name, in.NamespaceId, api.ErrConflict,
				)
			case "23503":
				return api.Workload{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Workload{}, fmt.Errorf("insert workload: %w", err)
	}

	return api.Workload{
		Id:            &id,
		NamespaceId:   in.NamespaceId,
		Kind:          in.Kind,
		Name:          in.Name,
		Replicas:      in.Replicas,
		ReadyReplicas: in.ReadyReplicas,
		Containers:    in.Containers,
		Labels:        in.Labels,
		Spec:          in.Spec,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}, nil
}

// GetWorkload fetches a workload by id.
func (p *PG) GetWorkload(ctx context.Context, id uuid.UUID) (api.Workload, error) {
	const q = `
		SELECT id, namespace_id, kind, name, replicas, ready_replicas,
		       containers, labels, spec, created_at, updated_at
		FROM workloads
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	w, err := scanWorkload(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Workload{}, api.ErrNotFound
		}
		return api.Workload{}, fmt.Errorf("select workload: %w", err)
	}
	return w, nil
}

// ListWorkloads returns up to limit workloads, optionally filtered by
// namespace, kind, and/or container image substring.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListWorkloads(ctx context.Context, filter api.WorkloadListFilter, limit int, cursor string) ([]api.Workload, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, namespace_id, kind, name, replicas, ready_replicas,
	                       containers, labels, spec, created_at, updated_at
	                FROM workloads`)
	args := make([]any, 0, 6)
	conds := make([]string, 0, 4)

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
	}
	if filter.Kind != nil {
		args = append(args, string(*filter.Kind))
		conds = append(conds, fmt.Sprintf("kind = $%d", len(args)))
	}
	if filter.ImageSubstring != nil && *filter.ImageSubstring != "" {
		args = append(args, "%"+*filter.ImageSubstring+"%")
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM jsonb_array_elements(containers) elem WHERE elem->>'image' ILIKE $%d)",
			len(args),
		))
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
		return nil, "", fmt.Errorf("query workloads: %w", err)
	}
	defer rows.Close()

	items := make([]api.Workload, 0, limit)
	for rows.Next() {
		w, err := scanWorkload(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan workload: %w", err)
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate workloads: %w", err)
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

// UpdateWorkload applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateWorkload(ctx context.Context, id uuid.UUID, in api.WorkloadUpdate) (api.Workload, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Replicas != nil {
		appendSet("replicas", *in.Replicas)
	}
	if in.ReadyReplicas != nil {
		appendSet("ready_replicas", *in.ReadyReplicas)
	}
	if in.Containers != nil {
		b, err := marshalPorts(in.Containers)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("containers", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("labels", b)
	}
	if in.Spec != nil {
		b, err := marshalSpec(in.Spec)
		if err != nil {
			return api.Workload{}, err
		}
		appendSet("spec", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE workloads SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Workload{}, fmt.Errorf("update workload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Workload{}, api.ErrNotFound
	}
	return p.GetWorkload(ctx, id)
}

// DeleteWorkload removes a workload by id.
func (p *PG) DeleteWorkload(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM workloads WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete workload: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertWorkload inserts-or-updates a workload keyed by (namespace_id, kind, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Workload{}, err
	}
	specJSON, err := marshalSpec(in.Spec)
	if err != nil {
		return api.Workload{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Workload{}, err
	}

	const q = `
		INSERT INTO workloads (
			id, namespace_id, kind, name, replicas, ready_replicas,
			containers, labels, spec, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		ON CONFLICT (namespace_id, kind, name) DO UPDATE SET
			replicas       = EXCLUDED.replicas,
			ready_replicas = EXCLUDED.ready_replicas,
			containers     = EXCLUDED.containers,
			labels         = EXCLUDED.labels,
			spec           = EXCLUDED.spec,
			updated_at     = EXCLUDED.updated_at
		RETURNING id, namespace_id, kind, name, replicas, ready_replicas,
		          containers, labels, spec, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, string(in.Kind), in.Name, in.Replicas, in.ReadyReplicas,
		containersJSON, labelsJSON, specJSON, now,
	)
	w, err := scanWorkload(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Workload{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return api.Workload{}, fmt.Errorf("upsert workload: %w", err)
	}
	return w, nil
}

// DeleteWorkloadsNotIn removes workloads in the namespace whose (kind, name)
// tuple is not in the parallel keep arrays. COALESCE guards against pgx
// encoding nil slices as SQL NULL (same class of fix as node/namespace/pod
// reconcile). An empty keep list clears every workload for that namespace.
func (p *PG) DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM workloads
		 WHERE namespace_id = $1
		   AND (kind, name) NOT IN (
		     SELECT k, n FROM UNNEST(
		       COALESCE($2::text[], ARRAY[]::text[]),
		       COALESCE($3::text[], ARRAY[]::text[])
		     ) AS t(k, n)
		   )`,
		namespaceID, keepKinds, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete workloads not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

func marshalSpec(spec *map[string]interface{}) ([]byte, error) { //nolint:gocritic // ptrToRefParam: callers pass *map from generated API types
	if spec == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	return b, nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanWorkload(row pgx.Row) (api.Workload, error) {
	var (
		w              api.Workload
		id             uuid.UUID
		namespaceID    uuid.UUID
		kind           string
		replicas       sql.NullInt32
		readyReplicas  sql.NullInt32
		createdAt      time.Time
		updatedAt      time.Time
		containersJSON []byte
		labelsJSON     []byte
		specJSON       []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &kind, &w.Name,
		&replicas, &readyReplicas,
		&containersJSON, &labelsJSON, &specJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Workload{}, fmt.Errorf("scan workload: %w", err)
	}
	w.Id = &id
	w.NamespaceId = namespaceID
	w.Kind = api.WorkloadKind(kind)
	w.CreatedAt = &createdAt
	w.UpdatedAt = &updatedAt
	if replicas.Valid {
		v := int(replicas.Int32)
		w.Replicas = &v
	}
	if readyReplicas.Valid {
		v := int(readyReplicas.Int32)
		w.ReadyReplicas = &v
	}
	if cs, err := unmarshalContainers(containersJSON); err != nil {
		return api.Workload{}, fmt.Errorf("unmarshal workload containers: %w", err)
	} else if cs != nil {
		w.Containers = cs
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Workload{}, fmt.Errorf("unmarshal workload labels: %w", err)
		}
		if len(labels) > 0 {
			w.Labels = &labels
		}
	}
	if len(specJSON) > 0 {
		var spec map[string]interface{}
		if err := json.Unmarshal(specJSON, &spec); err != nil {
			return api.Workload{}, fmt.Errorf("unmarshal workload spec: %w", err)
		}
		if len(spec) > 0 {
			w.Spec = &spec
		}
	}
	return w, nil
}

// CreateService inserts a new service.
// serviceColumns — same pattern as nodeColumns / ingressColumns.
const serviceColumns = `id, namespace_id, name, type, cluster_ip,
	selector, ports, load_balancer, labels, created_at, updated_at`

// CreateService inserts a new service into the given namespace.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreateService(ctx context.Context, in api.ServiceCreate) (api.Service, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Service{}, err
	}
	selectorJSON, err := marshalLabels(in.Selector)
	if err != nil {
		return api.Service{}, err
	}
	portsJSON, err := marshalPorts(in.Ports)
	if err != nil {
		return api.Service{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Service{}, err
	}

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	q := `INSERT INTO services (` + serviceColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, lbJSON, labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Service{}, fmt.Errorf("service %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
			case "23503":
				return api.Service{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Service{}, fmt.Errorf("insert service: %w", err)
	}

	return api.Service{
		Id:           &id,
		NamespaceId:  in.NamespaceId,
		Name:         in.Name,
		Type:         in.Type,
		ClusterIp:    in.ClusterIp,
		Selector:     in.Selector,
		Ports:        in.Ports,
		LoadBalancer: in.LoadBalancer,
		Labels:       in.Labels,
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}, nil
}

// GetService fetches a service by id.
func (p *PG) GetService(ctx context.Context, id uuid.UUID) (api.Service, error) {
	q := `SELECT ` + serviceColumns + ` FROM services WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	s, err := scanService(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Service{}, api.ErrNotFound
		}
		return api.Service{}, fmt.Errorf("select service: %w", err)
	}
	return s, nil
}

// ListServices returns up to limit services, optionally filtered by namespace.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListServices(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Service, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(serviceColumns)
	sb.WriteString(` FROM services`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if namespaceID != nil {
		args = append(args, *namespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query services: %w", err)
	}
	defer rows.Close()

	items := make([]api.Service, 0, limit)
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan service: %w", err)
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate services: %w", err)
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

// UpdateService applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateService(ctx context.Context, id uuid.UUID, in api.ServiceUpdate) (api.Service, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Type != nil {
		appendSet("type", string(*in.Type))
	}
	if in.ClusterIp != nil {
		appendSet("cluster_ip", *in.ClusterIp)
	}
	if in.Selector != nil {
		b, err := marshalLabels(in.Selector)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("selector", b)
	}
	if in.Ports != nil {
		b, err := marshalPorts(in.Ports)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("ports", b)
	}
	if in.LoadBalancer != nil {
		b, err := marshalPorts(in.LoadBalancer)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("load_balancer", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Service{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE services SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Service{}, fmt.Errorf("update service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Service{}, api.ErrNotFound
	}
	return p.GetService(ctx, id)
}

// DeleteService removes a service by id.
func (p *PG) DeleteService(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM services WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertService inserts-or-updates a service keyed by (namespace_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Service{}, err
	}
	selectorJSON, err := marshalLabels(in.Selector)
	if err != nil {
		return api.Service{}, err
	}
	portsJSON, err := marshalPorts(in.Ports)
	if err != nil {
		return api.Service{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Service{}, err
	}

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	q := `
		INSERT INTO services (` + serviceColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		ON CONFLICT (namespace_id, name) DO UPDATE SET
			type          = EXCLUDED.type,
			cluster_ip    = EXCLUDED.cluster_ip,
			selector      = EXCLUDED.selector,
			ports         = EXCLUDED.ports,
			load_balancer = EXCLUDED.load_balancer,
			labels        = EXCLUDED.labels,
			updated_at    = EXCLUDED.updated_at
		RETURNING ` + serviceColumns
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, lbJSON, labelsJSON, now,
	)
	s, err := scanService(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Service{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return api.Service{}, fmt.Errorf("upsert service: %w", err)
	}
	return s, nil
}

// DeleteServicesNotIn mirrors DeletePodsNotIn.
func (p *PG) DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM services
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete services not in: %w", err)
	}
	return tag.RowsAffected(), nil
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

// CreateIngress inserts a new ingress.
// ingressColumns is the full INSERT / SELECT / RETURNING column list for
// the ingresses table. Kept as a single const so the several SQL paths
// stay in sync; the load_balancer JSONB sits between tls and labels.
const ingressColumns = `id, namespace_id, name, ingress_class_name,
	rules, tls, load_balancer, labels, created_at, updated_at`

// CreateIngress inserts a new ingress into the given namespace.
func (p *PG) CreateIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Ingress{}, err
	}
	rulesJSON, err := marshalPorts(in.Rules)
	if err != nil {
		return api.Ingress{}, err
	}
	tlsJSON, err := marshalPorts(in.Tls)
	if err != nil {
		return api.Ingress{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Ingress{}, err
	}

	q := `INSERT INTO ingresses (` + ingressColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, lbJSON, labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.Ingress{}, fmt.Errorf("ingress %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
			case "23503":
				return api.Ingress{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
			}
		}
		return api.Ingress{}, fmt.Errorf("insert ingress: %w", err)
	}

	return api.Ingress{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		IngressClassName: in.IngressClassName,
		Rules:            in.Rules,
		Tls:              in.Tls,
		LoadBalancer:     in.LoadBalancer,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}, nil
}

// GetIngress fetches an ingress by id.
func (p *PG) GetIngress(ctx context.Context, id uuid.UUID) (api.Ingress, error) {
	q := `SELECT ` + ingressColumns + ` FROM ingresses WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	ing, err := scanIngress(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Ingress{}, api.ErrNotFound
		}
		return api.Ingress{}, fmt.Errorf("select ingress: %w", err)
	}
	return ing, nil
}

// ListIngresses returns up to limit ingresses, optionally filtered by namespace.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Ingress, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(ingressColumns)
	sb.WriteString(` FROM ingresses`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if namespaceID != nil {
		args = append(args, *namespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query ingresses: %w", err)
	}
	defer rows.Close()

	items := make([]api.Ingress, 0, limit)
	for rows.Next() {
		ing, err := scanIngress(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan ingress: %w", err)
		}
		items = append(items, ing)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate ingresses: %w", err)
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

// UpdateIngress applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateIngress(ctx context.Context, id uuid.UUID, in api.IngressUpdate) (api.Ingress, error) {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 6)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.IngressClassName != nil {
		appendSet("ingress_class_name", *in.IngressClassName)
	}
	if in.Rules != nil {
		b, err := marshalPorts(in.Rules)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("rules", b)
	}
	if in.Tls != nil {
		b, err := marshalPorts(in.Tls)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("tls", b)
	}
	if in.LoadBalancer != nil {
		b, err := marshalPorts(in.LoadBalancer)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("load_balancer", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Ingress{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE ingresses SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Ingress{}, fmt.Errorf("update ingress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Ingress{}, api.ErrNotFound
	}
	return p.GetIngress(ctx, id)
}

// DeleteIngress removes an ingress by id.
func (p *PG) DeleteIngress(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM ingresses WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete ingress: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertIngress inserts-or-updates an ingress keyed by (namespace_id, name).
func (p *PG) UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Ingress{}, err
	}
	rulesJSON, err := marshalPorts(in.Rules)
	if err != nil {
		return api.Ingress{}, err
	}
	tlsJSON, err := marshalPorts(in.Tls)
	if err != nil {
		return api.Ingress{}, err
	}
	lbJSON, err := marshalPorts(in.LoadBalancer)
	if err != nil {
		return api.Ingress{}, err
	}

	q := `
		INSERT INTO ingresses (` + ingressColumns + `) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		ON CONFLICT (namespace_id, name) DO UPDATE SET
			ingress_class_name = EXCLUDED.ingress_class_name,
			rules              = EXCLUDED.rules,
			tls                = EXCLUDED.tls,
			load_balancer      = EXCLUDED.load_balancer,
			labels             = EXCLUDED.labels,
			updated_at         = EXCLUDED.updated_at
		RETURNING ` + ingressColumns
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, lbJSON, labelsJSON, now,
	)
	ing, err := scanIngress(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Ingress{}, fmt.Errorf("namespace %s does not exist: %w", in.NamespaceId, api.ErrNotFound)
		}
		return api.Ingress{}, fmt.Errorf("upsert ingress: %w", err)
	}
	return ing, nil
}

// DeleteIngressesNotIn mirrors DeleteServicesNotIn.
func (p *PG) DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM ingresses
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete ingresses not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanIngress(row pgx.Row) (api.Ingress, error) {
	var (
		i                api.Ingress
		id               uuid.UUID
		namespaceID      uuid.UUID
		createdAt        time.Time
		updatedAt        time.Time
		ingressClassName sql.NullString
		rulesJSON        []byte
		tlsJSON          []byte
		lbJSON           []byte
		labelsJSON       []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &i.Name,
		&ingressClassName,
		&rulesJSON, &tlsJSON, &lbJSON, &labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Ingress{}, fmt.Errorf("scan ingress: %w", err)
	}
	i.Id = &id
	i.NamespaceId = namespaceID
	i.CreatedAt = &createdAt
	i.UpdatedAt = &updatedAt
	i.IngressClassName = nullableString(ingressClassName)
	if len(rulesJSON) > 0 {
		var rules []map[string]interface{}
		if err := json.Unmarshal(rulesJSON, &rules); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress rules: %w", err)
		}
		if len(rules) > 0 {
			i.Rules = &rules
		}
	}
	if len(tlsJSON) > 0 {
		var tls []map[string]interface{}
		if err := json.Unmarshal(tlsJSON, &tls); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress tls: %w", err)
		}
		if len(tls) > 0 {
			i.Tls = &tls
		}
	}
	if lb, err := unmarshalMapArray(lbJSON); err != nil {
		return api.Ingress{}, fmt.Errorf("unmarshal ingress load_balancer: %w", err)
	} else {
		i.LoadBalancer = lb
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Ingress{}, fmt.Errorf("unmarshal ingress labels: %w", err)
		}
		if len(labels) > 0 {
			i.Labels = &labels
		}
	}
	return i, nil
}

//nolint:gocyclo // JSONB unmarshal branches add inherent cyclomatic complexity
func scanService(row pgx.Row) (api.Service, error) {
	var (
		s            api.Service
		id           uuid.UUID
		namespaceID  uuid.UUID
		createdAt    time.Time
		updatedAt    time.Time
		svcType      sql.NullString
		clusterIP    sql.NullString
		selectorJSON []byte
		portsJSON    []byte
		lbJSON       []byte
		labelsJSON   []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &s.Name,
		&svcType, &clusterIP,
		&selectorJSON, &portsJSON, &lbJSON, &labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Service{}, fmt.Errorf("scan service: %w", err)
	}
	s.Id = &id
	s.NamespaceId = namespaceID
	s.CreatedAt = &createdAt
	s.UpdatedAt = &updatedAt
	s.ClusterIp = nullableString(clusterIP)
	if svcType.Valid {
		t := api.ServiceType(svcType.String)
		s.Type = &t
	}
	if len(selectorJSON) > 0 {
		var sel map[string]string
		if err := json.Unmarshal(selectorJSON, &sel); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service selector: %w", err)
		}
		if len(sel) > 0 {
			s.Selector = &sel
		}
	}
	if len(portsJSON) > 0 {
		var ports []map[string]interface{}
		if err := json.Unmarshal(portsJSON, &ports); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service ports: %w", err)
		}
		if len(ports) > 0 {
			s.Ports = &ports
		}
	}
	if lb, err := unmarshalMapArray(lbJSON); err != nil {
		return api.Service{}, fmt.Errorf("unmarshal service load_balancer: %w", err)
	} else {
		s.LoadBalancer = lb
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Service{}, fmt.Errorf("unmarshal service labels: %w", err)
		}
		if len(labels) > 0 {
			s.Labels = &labels
		}
	}
	return s, nil
}

func scanPod(row pgx.Row) (api.Pod, error) {
	var (
		p              api.Pod
		id             uuid.UUID
		namespaceID    uuid.UUID
		createdAt      time.Time
		updatedAt      time.Time
		phase          sql.NullString
		nodeName       sql.NullString
		podIP          sql.NullString
		workloadID     *uuid.UUID
		containersJSON []byte
		labelsJSON     []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &p.Name,
		&phase, &nodeName, &podIP,
		&workloadID, &containersJSON, &labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Pod{}, fmt.Errorf("scan pod: %w", err)
	}
	p.Id = &id
	p.NamespaceId = namespaceID
	p.CreatedAt = &createdAt
	p.UpdatedAt = &updatedAt
	p.Phase = nullableString(phase)
	p.NodeName = nullableString(nodeName)
	p.PodIp = nullableString(podIP)
	if workloadID != nil {
		p.WorkloadId = workloadID
	}
	if cs, err := unmarshalContainers(containersJSON); err != nil {
		return api.Pod{}, fmt.Errorf("unmarshal pod containers: %w", err)
	} else if cs != nil {
		p.Containers = cs
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Pod{}, fmt.Errorf("unmarshal pod labels: %w", err)
		}
		if len(labels) > 0 {
			p.Labels = &labels
		}
	}
	return p, nil
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

func scanNamespace(row pgx.Row) (api.Namespace, error) {
	var (
		n               api.Namespace
		id              uuid.UUID
		clusterID       uuid.UUID
		createdAt       time.Time
		updatedAt       time.Time
		displayName     sql.NullString
		phase           sql.NullString
		labelsJSON      []byte
		owner           sql.NullString
		criticality     sql.NullString
		notes           sql.NullString
		runbookURL      sql.NullString
		annotationsJSON []byte
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name,
		&displayName, &phase, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Namespace{}, fmt.Errorf("scan namespace: %w", err)
	}
	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.DisplayName = nullableString(displayName)
	n.Phase = nullableString(phase)
	n.Owner = nullableString(owner)
	n.Criticality = nullableString(criticality)
	n.Notes = nullableString(notes)
	n.RunbookUrl = nullableString(runbookURL)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Namespace{}, fmt.Errorf("unmarshal namespace labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}
	if len(annotationsJSON) > 0 {
		var annotations map[string]string
		if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
			return api.Namespace{}, fmt.Errorf("unmarshal namespace annotations: %w", err)
		}
		if len(annotations) > 0 {
			n.Annotations = &annotations
		}
	}
	return n, nil
}

// UpsertNode inserts-or-updates a node keyed by (cluster_id, name). The
// unique index on (cluster_id, name) drives the ON CONFLICT target. On
// conflict only mutable columns are overwritten so created_at is preserved.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	id := uuid.New()
	now := time.Now().UTC()

	values, err := nodeInsertValues(&in, id, now)
	if err != nil {
		return api.Node{}, err
	}

	const q = `
		INSERT INTO nodes (` + nodeColumns + `)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$38)
		ON CONFLICT (cluster_id, name) DO UPDATE SET
			display_name                  = EXCLUDED.display_name,
			role                          = EXCLUDED.role,
			kubelet_version               = EXCLUDED.kubelet_version,
			kube_proxy_version            = EXCLUDED.kube_proxy_version,
			container_runtime_version     = EXCLUDED.container_runtime_version,
			os_image                      = EXCLUDED.os_image,
			operating_system              = EXCLUDED.operating_system,
			kernel_version                = EXCLUDED.kernel_version,
			architecture                  = EXCLUDED.architecture,
			internal_ip                   = EXCLUDED.internal_ip,
			external_ip                   = EXCLUDED.external_ip,
			pod_cidr                      = EXCLUDED.pod_cidr,
			provider_id                   = EXCLUDED.provider_id,
			instance_type                 = EXCLUDED.instance_type,
			zone                          = EXCLUDED.zone,
			capacity_cpu                  = EXCLUDED.capacity_cpu,
			capacity_memory               = EXCLUDED.capacity_memory,
			capacity_pods                 = EXCLUDED.capacity_pods,
			capacity_ephemeral_storage    = EXCLUDED.capacity_ephemeral_storage,
			allocatable_cpu               = EXCLUDED.allocatable_cpu,
			allocatable_memory            = EXCLUDED.allocatable_memory,
			allocatable_pods              = EXCLUDED.allocatable_pods,
			allocatable_ephemeral_storage = EXCLUDED.allocatable_ephemeral_storage,
			conditions                    = EXCLUDED.conditions,
			taints                        = EXCLUDED.taints,
			unschedulable                 = EXCLUDED.unschedulable,
			ready                         = EXCLUDED.ready,
			labels                        = EXCLUDED.labels,
			updated_at                    = EXCLUDED.updated_at
		RETURNING ` + nodeColumns + `
	`
	row := p.pool.QueryRow(ctx, q, values...)
	n, err := scanNode(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.Node{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
		}
		return api.Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return n, nil
}

func scanNode(row pgx.Row) (api.Node, error) {
	var (
		n                       api.Node
		id                      uuid.UUID
		clusterID               uuid.UUID
		createdAt               time.Time
		updatedAt               time.Time
		displayName             sql.NullString
		role                    sql.NullString
		kubeletVersion          sql.NullString
		kubeProxyVersion        sql.NullString
		containerRuntimeVersion sql.NullString
		osImage                 sql.NullString
		operatingSystem         sql.NullString
		kernelVersion           sql.NullString
		architecture            sql.NullString
		internalIP              sql.NullString
		externalIP              sql.NullString
		podCIDR                 sql.NullString
		providerID              sql.NullString
		instanceType            sql.NullString
		zone                    sql.NullString
		capacityCPU             sql.NullString
		capacityMemory          sql.NullString
		capacityPods            sql.NullString
		capacityEphemeral       sql.NullString
		allocatableCPU          sql.NullString
		allocatableMemory       sql.NullString
		allocatablePods         sql.NullString
		allocatableEphemeral    sql.NullString
		conditionsJSON          []byte
		taintsJSON              []byte
		unschedulable           bool
		ready                   bool
		labelsJSON              []byte
		owner                   sql.NullString
		criticality             sql.NullString
		notes                   sql.NullString
		runbookURL              sql.NullString
		annotationsJSON         []byte
		hardwareModel           sql.NullString
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name, &displayName, &role,
		&kubeletVersion, &kubeProxyVersion, &containerRuntimeVersion,
		&osImage, &operatingSystem, &kernelVersion, &architecture,
		&internalIP, &externalIP, &podCIDR,
		&providerID, &instanceType, &zone,
		&capacityCPU, &capacityMemory, &capacityPods, &capacityEphemeral,
		&allocatableCPU, &allocatableMemory, &allocatablePods, &allocatableEphemeral,
		&conditionsJSON, &taintsJSON, &unschedulable, &ready,
		&labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON, &hardwareModel,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Node{}, fmt.Errorf("scan node: %w", err)
	}

	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.DisplayName = nullableString(displayName)
	n.Role = nullableString(role)
	n.KubeletVersion = nullableString(kubeletVersion)
	n.KubeProxyVersion = nullableString(kubeProxyVersion)
	n.ContainerRuntimeVersion = nullableString(containerRuntimeVersion)
	n.OsImage = nullableString(osImage)
	n.OperatingSystem = nullableString(operatingSystem)
	n.KernelVersion = nullableString(kernelVersion)
	n.Architecture = nullableString(architecture)
	n.InternalIp = nullableString(internalIP)
	n.ExternalIp = nullableString(externalIP)
	n.PodCidr = nullableString(podCIDR)
	n.ProviderId = nullableString(providerID)
	n.InstanceType = nullableString(instanceType)
	n.Zone = nullableString(zone)
	n.CapacityCpu = nullableString(capacityCPU)
	n.CapacityMemory = nullableString(capacityMemory)
	n.CapacityPods = nullableString(capacityPods)
	n.CapacityEphemeralStorage = nullableString(capacityEphemeral)
	n.AllocatableCpu = nullableString(allocatableCPU)
	n.AllocatableMemory = nullableString(allocatableMemory)
	n.AllocatablePods = nullableString(allocatablePods)
	n.AllocatableEphemeralStorage = nullableString(allocatableEphemeral)
	n.Unschedulable = &unschedulable
	n.Ready = &ready

	if cs, err := unmarshalMapArray(conditionsJSON); err != nil {
		return api.Node{}, fmt.Errorf("unmarshal node conditions: %w", err)
	} else {
		n.Conditions = cs
	}
	if ts, err := unmarshalMapArray(taintsJSON); err != nil {
		return api.Node{}, fmt.Errorf("unmarshal node taints: %w", err)
	} else {
		n.Taints = ts
	}

	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Node{}, fmt.Errorf("unmarshal node labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}

	n.Owner = nullableString(owner)
	n.Criticality = nullableString(criticality)
	n.Notes = nullableString(notes)
	n.RunbookUrl = nullableString(runbookURL)
	n.HardwareModel = nullableString(hardwareModel)
	if len(annotationsJSON) > 0 {
		var annotations map[string]string
		if err := json.Unmarshal(annotationsJSON, &annotations); err != nil {
			return api.Node{}, fmt.Errorf("unmarshal node annotations: %w", err)
		}
		if len(annotations) > 0 {
			n.Annotations = &annotations
		}
	}
	return n, nil
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

func scanCluster(row pgx.Row) (api.Cluster, error) {
	var (
		c                 api.Cluster
		id                uuid.UUID
		createdAt         time.Time
		updatedAt         time.Time
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
		&createdAt, &updatedAt,
	); err != nil {
		return api.Cluster{}, fmt.Errorf("scan cluster: %w", err)
	}

	c.Id = &id
	c.CreatedAt = &createdAt
	c.UpdatedAt = &updatedAt
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

func nullableString(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode cursor: %w", err)
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, errCursorFormatInvalid
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor timestamp: %w", err)
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("cursor id: %w", err)
	}
	return ts, id, nil
}

// accessModesValue normalises a nullable *[]string access-modes slice into a
// never-nil []string that pgx will send as a TEXT[] (empty slice => empty
// array, not NULL). Matches the store's DEFAULT '{}' column definition.
func accessModesValue(modes *[]string) []string {
	if modes == nil {
		return []string{}
	}
	return *modes
}

func accessModesPointer(modes []string) *[]string {
	if len(modes) == 0 {
		return nil
	}
	return &modes
}

// CreatePersistentVolume inserts a cluster-scoped PV.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreatePersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolume{}, err
	}

	const q = `
		INSERT INTO persistent_volumes (
			id, cluster_id, name, capacity, access_modes,
			reclaim_policy, phase, storage_class_name,
			csi_driver, volume_handle,
			claim_ref_namespace, claim_ref_name,
			labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name,
		in.Capacity, accessModesValue(in.AccessModes),
		in.ReclaimPolicy, in.Phase, in.StorageClassName,
		in.CsiDriver, in.VolumeHandle,
		in.ClaimRefNamespace, in.ClaimRefName,
		labelsJSON, now,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				return api.PersistentVolume{}, fmt.Errorf(
					"persistent volume %q in cluster %s already exists: %w",
					in.Name, in.ClusterId, api.ErrConflict,
				)
			case "23503":
				return api.PersistentVolume{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
			}
		}
		return api.PersistentVolume{}, fmt.Errorf("insert persistent volume: %w", err)
	}

	return api.PersistentVolume{
		Id:                &id,
		ClusterId:         in.ClusterId,
		Name:              in.Name,
		Capacity:          in.Capacity,
		AccessModes:       in.AccessModes,
		ReclaimPolicy:     in.ReclaimPolicy,
		Phase:             in.Phase,
		StorageClassName:  in.StorageClassName,
		CsiDriver:         in.CsiDriver,
		VolumeHandle:      in.VolumeHandle,
		ClaimRefNamespace: in.ClaimRefNamespace,
		ClaimRefName:      in.ClaimRefName,
		Labels:            in.Labels,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}, nil
}

// GetPersistentVolume fetches a PV by id.
func (p *PG) GetPersistentVolume(ctx context.Context, id uuid.UUID) (api.PersistentVolume, error) {
	const q = `
		SELECT id, cluster_id, name, capacity, access_modes,
		       reclaim_policy, phase, storage_class_name,
		       csi_driver, volume_handle,
		       claim_ref_namespace, claim_ref_name,
		       labels, created_at, updated_at
		FROM persistent_volumes
		WHERE id = $1
	`
	row := p.pool.QueryRow(ctx, q, id)
	pv, err := scanPersistentVolume(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.PersistentVolume{}, api.ErrNotFound
		}
		return api.PersistentVolume{}, fmt.Errorf("select persistent volume: %w", err)
	}
	return pv, nil
}

// ListPersistentVolumes returns up to limit PVs, optionally filtered by cluster.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPersistentVolumes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolume, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, cluster_id, name, capacity, access_modes,
	                       reclaim_policy, phase, storage_class_name,
	                       csi_driver, volume_handle,
	                       claim_ref_namespace, claim_ref_name,
	                       labels, created_at, updated_at
	                FROM persistent_volumes`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if clusterID != nil {
		args = append(args, *clusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
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
		return nil, "", fmt.Errorf("query persistent volumes: %w", err)
	}
	defer rows.Close()

	items := make([]api.PersistentVolume, 0, limit)
	for rows.Next() {
		pv, err := scanPersistentVolume(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan persistent volume: %w", err)
		}
		items = append(items, pv)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate persistent volumes: %w", err)
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

// UpdatePersistentVolume applies merge-patch on mutable PV fields.
//
//nolint:gocyclo,gocritic // merge-patch nil checks are inherently repetitive; hugeParam: Store interface requires value param
func (p *PG) UpdatePersistentVolume(ctx context.Context, id uuid.UUID, in api.PersistentVolumeUpdate) (api.PersistentVolume, error) {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 10)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Capacity != nil {
		appendSet("capacity", *in.Capacity)
	}
	if in.AccessModes != nil {
		appendSet("access_modes", accessModesValue(in.AccessModes))
	}
	if in.ReclaimPolicy != nil {
		appendSet("reclaim_policy", *in.ReclaimPolicy)
	}
	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.StorageClassName != nil {
		appendSet("storage_class_name", *in.StorageClassName)
	}
	if in.CsiDriver != nil {
		appendSet("csi_driver", *in.CsiDriver)
	}
	if in.VolumeHandle != nil {
		appendSet("volume_handle", *in.VolumeHandle)
	}
	if in.ClaimRefNamespace != nil {
		appendSet("claim_ref_namespace", *in.ClaimRefNamespace)
	}
	if in.ClaimRefName != nil {
		appendSet("claim_ref_name", *in.ClaimRefName)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.PersistentVolume{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE persistent_volumes SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.PersistentVolume{}, fmt.Errorf("update persistent volume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.PersistentVolume{}, api.ErrNotFound
	}
	return p.GetPersistentVolume(ctx, id)
}

// DeletePersistentVolume removes a PV by id.
func (p *PG) DeletePersistentVolume(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM persistent_volumes WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete persistent volume: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertPersistentVolume inserts-or-updates a PV keyed by (cluster_id, name).
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.PersistentVolume{}, err
	}

	const q = `
		INSERT INTO persistent_volumes (
			id, cluster_id, name, capacity, access_modes,
			reclaim_policy, phase, storage_class_name,
			csi_driver, volume_handle,
			claim_ref_namespace, claim_ref_name,
			labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
		ON CONFLICT (cluster_id, name) DO UPDATE SET
			capacity            = EXCLUDED.capacity,
			access_modes        = EXCLUDED.access_modes,
			reclaim_policy      = EXCLUDED.reclaim_policy,
			phase               = EXCLUDED.phase,
			storage_class_name  = EXCLUDED.storage_class_name,
			csi_driver          = EXCLUDED.csi_driver,
			volume_handle       = EXCLUDED.volume_handle,
			claim_ref_namespace = EXCLUDED.claim_ref_namespace,
			claim_ref_name      = EXCLUDED.claim_ref_name,
			labels              = EXCLUDED.labels,
			updated_at          = EXCLUDED.updated_at
		RETURNING id, cluster_id, name, capacity, access_modes,
		          reclaim_policy, phase, storage_class_name,
		          csi_driver, volume_handle,
		          claim_ref_namespace, claim_ref_name,
		          labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.ClusterId, in.Name,
		in.Capacity, accessModesValue(in.AccessModes),
		in.ReclaimPolicy, in.Phase, in.StorageClassName,
		in.CsiDriver, in.VolumeHandle,
		in.ClaimRefNamespace, in.ClaimRefName,
		labelsJSON, now,
	)
	pv, err := scanPersistentVolume(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return api.PersistentVolume{}, fmt.Errorf("cluster %s does not exist: %w", in.ClusterId, api.ErrNotFound)
		}
		return api.PersistentVolume{}, fmt.Errorf("upsert persistent volume: %w", err)
	}
	return pv, nil
}

// DeletePersistentVolumesNotIn removes cluster-scoped PVs whose name is not in keepNames.
func (p *PG) DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM persistent_volumes
		 WHERE cluster_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		clusterID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete persistent volumes not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanPersistentVolume(row pgx.Row) (api.PersistentVolume, error) {
	var (
		pv                api.PersistentVolume
		id                uuid.UUID
		clusterID         uuid.UUID
		createdAt         time.Time
		updatedAt         time.Time
		capacity          sql.NullString
		accessModes       []string
		reclaimPolicy     sql.NullString
		phase             sql.NullString
		storageClassName  sql.NullString
		csiDriver         sql.NullString
		volumeHandle      sql.NullString
		claimRefNamespace sql.NullString
		claimRefName      sql.NullString
		labelsJSON        []byte
	)
	if err := row.Scan(
		&id, &clusterID, &pv.Name,
		&capacity, &accessModes,
		&reclaimPolicy, &phase, &storageClassName,
		&csiDriver, &volumeHandle,
		&claimRefNamespace, &claimRefName,
		&labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.PersistentVolume{}, fmt.Errorf("scan persistent volume: %w", err)
	}
	pv.Id = &id
	pv.ClusterId = clusterID
	pv.CreatedAt = &createdAt
	pv.UpdatedAt = &updatedAt
	pv.Capacity = nullableString(capacity)
	pv.AccessModes = accessModesPointer(accessModes)
	pv.ReclaimPolicy = nullableString(reclaimPolicy)
	pv.Phase = nullableString(phase)
	pv.StorageClassName = nullableString(storageClassName)
	pv.CsiDriver = nullableString(csiDriver)
	pv.VolumeHandle = nullableString(volumeHandle)
	pv.ClaimRefNamespace = nullableString(claimRefNamespace)
	pv.ClaimRefName = nullableString(claimRefName)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.PersistentVolume{}, fmt.Errorf("unmarshal pv labels: %w", err)
		}
		if len(labels) > 0 {
			pv.Labels = &labels
		}
	}
	return pv, nil
}

// classifyPVCFKError disambiguates 23503 foreign-key violations on the
// persistent_volume_claims table into namespace vs bound-volume misses.
func classifyPVCFKError(err error, namespaceID uuid.UUID, boundVolumeID *uuid.UUID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return nil
	}
	if strings.Contains(pgErr.ConstraintName, "bound_volume_id") {
		target := "<nil>"
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

// GetPersistentVolumeClaim fetches a PVC by id.
func (p *PG) GetPersistentVolumeClaim(ctx context.Context, id uuid.UUID) (api.PersistentVolumeClaim, error) {
	const q = `
		SELECT id, namespace_id, name, phase, storage_class_name,
		       volume_name, bound_volume_id, access_modes, requested_storage,
		       labels, created_at, updated_at
		FROM persistent_volume_claims
		WHERE id = $1
	`
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

// ListPersistentVolumeClaims returns up to limit PVCs, optionally filtered by namespace.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPersistentVolumeClaims(
	ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string,
) ([]api.PersistentVolumeClaim, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, namespace_id, name, phase, storage_class_name,
	                       volume_name, bound_volume_id, access_modes, requested_storage,
	                       labels, created_at, updated_at
	                FROM persistent_volume_claims`)
	args := make([]any, 0, 4)
	conds := make([]string, 0, 2)

	if namespaceID != nil {
		args = append(args, *namespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
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
		last := items[limit-1]
		if last.CreatedAt != nil && last.Id != nil {
			next = encodeCursor(*last.CreatedAt, *last.Id)
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
func (p *PG) UpsertPersistentVolumeClaim(ctx context.Context, in api.PersistentVolumeClaimCreate) (api.PersistentVolumeClaim, error) {
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
		          labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name,
		in.Phase, in.StorageClassName,
		in.VolumeName, in.BoundVolumeId,
		accessModesValue(in.AccessModes), in.RequestedStorage,
		labelsJSON, now,
	)
	pvc, err := scanPersistentVolumeClaim(row)
	if err != nil {
		if pErr := classifyPVCFKError(err, in.NamespaceId, in.BoundVolumeId); pErr != nil {
			return api.PersistentVolumeClaim{}, pErr
		}
		return api.PersistentVolumeClaim{}, fmt.Errorf("upsert pvc: %w", err)
	}
	return pvc, nil
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
		pvc              api.PersistentVolumeClaim
		id               uuid.UUID
		namespaceID      uuid.UUID
		createdAt        time.Time
		updatedAt        time.Time
		phase            sql.NullString
		storageClassName sql.NullString
		volumeName       sql.NullString
		boundVolumeID    *uuid.UUID
		accessModes      []string
		requestedStorage sql.NullString
		labelsJSON       []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &pvc.Name,
		&phase, &storageClassName,
		&volumeName, &boundVolumeID,
		&accessModes, &requestedStorage,
		&labelsJSON,
		&createdAt, &updatedAt,
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
