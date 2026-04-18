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
	return p.pool.Ping(ctx)
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
func (p *PG) CreateCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Cluster{}, err
	}

	const q = `
		INSERT INTO clusters (
			id, name, display_name, environment, provider, region,
			kubernetes_version, api_endpoint, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.Name, in.DisplayName, in.Environment, in.Provider, in.Region,
		in.KubernetesVersion, in.ApiEndpoint, labelsJSON, now,
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
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}, nil
}

// GetCluster fetches a cluster by id.
func (p *PG) GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels, created_at, updated_at
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
		       kubernetes_version, api_endpoint, labels, created_at, updated_at
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
			       kubernetes_version, api_endpoint, labels, created_at, updated_at
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
			       kubernetes_version, api_endpoint, labels, created_at, updated_at
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

// CreateNode inserts a new node. Returns api.ErrNotFound when the parent
// cluster does not exist (FK violation), api.ErrConflict on duplicate
// (cluster_id, name).
func (p *PG) CreateNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Node{}, err
	}

	const q = `
		INSERT INTO nodes (
			id, cluster_id, name, display_name, kubelet_version,
			os_image, architecture, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.KubeletVersion,
		in.OsImage, in.Architecture, labelsJSON, now,
	)
	if err != nil {
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

	return api.Node{
		Id:             &id,
		ClusterId:      in.ClusterId,
		Name:           in.Name,
		DisplayName:    in.DisplayName,
		KubeletVersion: in.KubeletVersion,
		OsImage:        in.OsImage,
		Architecture:   in.Architecture,
		Labels:         in.Labels,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}, nil
}

// GetNode fetches a node by id.
func (p *PG) GetNode(ctx context.Context, id uuid.UUID) (api.Node, error) {
	const q = `
		SELECT id, cluster_id, name, display_name, kubelet_version,
		       os_image, architecture, labels, created_at, updated_at
		FROM nodes
		WHERE id = $1
	`
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
func (p *PG) ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Node, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, cluster_id, name, display_name, kubelet_version,
	                       os_image, architecture, labels, created_at, updated_at
	                FROM nodes`)
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

// UpdateNode applies merge-patch semantics on mutable fields only.
func (p *PG) UpdateNode(ctx context.Context, id uuid.UUID, in api.NodeUpdate) (api.Node, error) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.KubeletVersion != nil {
		appendSet("kubelet_version", *in.KubeletVersion)
	}
	if in.OsImage != nil {
		appendSet("os_image", *in.OsImage)
	}
	if in.Architecture != nil {
		appendSet("architecture", *in.Architecture)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Node{}, err
		}
		appendSet("labels", b)
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
func (p *PG) CreateNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
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
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.Phase,
		labelsJSON, now,
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
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// GetNamespace fetches a namespace by id.
func (p *PG) GetNamespace(ctx context.Context, id uuid.UUID) (api.Namespace, error) {
	const q = `
		SELECT id, cluster_id, name, display_name, phase,
		       labels, created_at, updated_at
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
func (p *PG) ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Namespace, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, cluster_id, name, display_name, phase,
	                       labels, created_at, updated_at
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
		RETURNING id, cluster_id, name, display_name, phase,
		          labels, created_at, updated_at
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
func (p *PG) ListPods(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Pod, string, error) {
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
				return api.Workload{}, fmt.Errorf("workload %s/%q in namespace %s already exists: %w", in.Kind, in.Name, in.NamespaceId, api.ErrConflict)
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
// namespace and/or kind.
func (p *PG) ListWorkloads(ctx context.Context, namespaceID *uuid.UUID, kind *api.WorkloadKind, limit int, cursor string) ([]api.Workload, string, error) {
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
	args := make([]any, 0, 5)
	conds := make([]string, 0, 3)

	if namespaceID != nil {
		args = append(args, *namespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
	}
	if kind != nil {
		args = append(args, string(*kind))
		conds = append(conds, fmt.Sprintf("kind = $%d", len(args)))
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

func marshalSpec(spec *map[string]interface{}) ([]byte, error) {
	if spec == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	return b, nil
}

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
		return api.Workload{}, err
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

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	const q = `
		INSERT INTO services (
			id, namespace_id, name, type, cluster_ip,
			selector, ports, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, labelsJSON, now,
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
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Type:        in.Type,
		ClusterIp:   in.ClusterIp,
		Selector:    in.Selector,
		Ports:       in.Ports,
		Labels:      in.Labels,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// GetService fetches a service by id.
func (p *PG) GetService(ctx context.Context, id uuid.UUID) (api.Service, error) {
	const q = `
		SELECT id, namespace_id, name, type, cluster_ip,
		       selector, ports, labels, created_at, updated_at
		FROM services
		WHERE id = $1
	`
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
func (p *PG) ListServices(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Service, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, namespace_id, name, type, cluster_ip,
	                       selector, ports, labels, created_at, updated_at
	                FROM services`)
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

	var svcType *string
	if in.Type != nil {
		t := string(*in.Type)
		svcType = &t
	}

	const q = `
		INSERT INTO services (
			id, namespace_id, name, type, cluster_ip,
			selector, ports, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (namespace_id, name) DO UPDATE SET
			type       = EXCLUDED.type,
			cluster_ip = EXCLUDED.cluster_ip,
			selector   = EXCLUDED.selector,
			ports      = EXCLUDED.ports,
			labels     = EXCLUDED.labels,
			updated_at = EXCLUDED.updated_at
		RETURNING id, namespace_id, name, type, cluster_ip,
		          selector, ports, labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, svcType, in.ClusterIp,
		selectorJSON, portsJSON, labelsJSON, now,
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

	const q = `
		INSERT INTO ingresses (
			id, namespace_id, name, ingress_class_name,
			rules, tls, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, labelsJSON, now,
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
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}, nil
}

// GetIngress fetches an ingress by id.
func (p *PG) GetIngress(ctx context.Context, id uuid.UUID) (api.Ingress, error) {
	const q = `
		SELECT id, namespace_id, name, ingress_class_name,
		       rules, tls, labels, created_at, updated_at
		FROM ingresses
		WHERE id = $1
	`
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
func (p *PG) ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Ingress, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT id, namespace_id, name, ingress_class_name,
	                       rules, tls, labels, created_at, updated_at
	                FROM ingresses`)
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

	const q = `
		INSERT INTO ingresses (
			id, namespace_id, name, ingress_class_name,
			rules, tls, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
		ON CONFLICT (namespace_id, name) DO UPDATE SET
			ingress_class_name = EXCLUDED.ingress_class_name,
			rules              = EXCLUDED.rules,
			tls                = EXCLUDED.tls,
			labels             = EXCLUDED.labels,
			updated_at         = EXCLUDED.updated_at
		RETURNING id, namespace_id, name, ingress_class_name,
		          rules, tls, labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, in.IngressClassName,
		rulesJSON, tlsJSON, labelsJSON, now,
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
		labelsJSON       []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &i.Name,
		&ingressClassName,
		&rulesJSON, &tlsJSON, &labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Ingress{}, err
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
		labelsJSON   []byte
	)
	if err := row.Scan(
		&id, &namespaceID, &s.Name,
		&svcType, &clusterIP,
		&selectorJSON, &portsJSON, &labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Service{}, err
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
		return api.Pod{}, err
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
func unmarshalContainers(b []byte) (*api.ContainerList, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var cs api.ContainerList
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, nil
	}
	return &cs, nil
}

func scanNamespace(row pgx.Row) (api.Namespace, error) {
	var (
		n           api.Namespace
		id          uuid.UUID
		clusterID   uuid.UUID
		createdAt   time.Time
		updatedAt   time.Time
		displayName sql.NullString
		phase       sql.NullString
		labelsJSON  []byte
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name,
		&displayName, &phase,
		&labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Namespace{}, err
	}
	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.DisplayName = nullableString(displayName)
	n.Phase = nullableString(phase)
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Namespace{}, fmt.Errorf("unmarshal namespace labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}
	return n, nil
}

// UpsertNode inserts-or-updates a node keyed by (cluster_id, name). The
// unique index on (cluster_id, name) drives the ON CONFLICT target. On
// conflict only mutable columns are overwritten so created_at is preserved.
func (p *PG) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Node{}, err
	}

	const q = `
		INSERT INTO nodes (
			id, cluster_id, name, display_name, kubelet_version,
			os_image, architecture, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
		ON CONFLICT (cluster_id, name) DO UPDATE SET
			display_name    = EXCLUDED.display_name,
			kubelet_version = EXCLUDED.kubelet_version,
			os_image        = EXCLUDED.os_image,
			architecture    = EXCLUDED.architecture,
			labels          = EXCLUDED.labels,
			updated_at      = EXCLUDED.updated_at
		RETURNING id, cluster_id, name, display_name, kubelet_version,
		          os_image, architecture, labels, created_at, updated_at
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.ClusterId, in.Name, in.DisplayName, in.KubeletVersion,
		in.OsImage, in.Architecture, labelsJSON, now,
	)
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
		n               api.Node
		id              uuid.UUID
		clusterID       uuid.UUID
		createdAt       time.Time
		updatedAt       time.Time
		displayName     sql.NullString
		kubeletVersion  sql.NullString
		osImage         sql.NullString
		architecture    sql.NullString
		labelsJSON      []byte
	)
	if err := row.Scan(
		&id, &clusterID, &n.Name,
		&displayName, &kubeletVersion, &osImage, &architecture,
		&labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Node{}, err
	}

	n.Id = &id
	n.ClusterId = clusterID
	n.CreatedAt = &createdAt
	n.UpdatedAt = &updatedAt
	n.DisplayName = nullableString(displayName)
	n.KubeletVersion = nullableString(kubeletVersion)
	n.OsImage = nullableString(osImage)
	n.Architecture = nullableString(architecture)

	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Node{}, fmt.Errorf("unmarshal node labels: %w", err)
		}
		if len(labels) > 0 {
			n.Labels = &labels
		}
	}
	return n, nil
}

// marshalLabels encodes the optional labels map as JSON, preserving NULL-vs-empty semantics.
func marshalLabels(labels *map[string]string) ([]byte, error) {
	if labels == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(*labels)
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
	)
	if err := row.Scan(
		&id, &c.Name,
		&displayName, &environment, &provider, &region,
		&kubernetesVersion, &apiEndpoint,
		&labelsJSON,
		&createdAt, &updatedAt,
	); err != nil {
		return api.Cluster{}, err
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

	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Cluster{}, fmt.Errorf("unmarshal labels: %w", err)
		}
		if len(labels) > 0 {
			c.Labels = &labels
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
		return time.Time{}, uuid.Nil, errors.New("cursor format invalid")
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
