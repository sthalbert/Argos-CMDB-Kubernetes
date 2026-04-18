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
