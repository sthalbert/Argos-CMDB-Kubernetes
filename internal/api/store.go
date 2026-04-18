package api

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// Sentinel errors returned by Store implementations. Handlers translate these
// into RFC 7807 responses with the matching HTTP status.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Store is the persistence contract consumed by the REST handlers.
// Implementations must be safe for concurrent use by multiple goroutines.
type Store interface {
	// Ping verifies that the underlying database is reachable.
	Ping(ctx context.Context) error

	// CreateCluster inserts a new cluster. Returns ErrConflict if a cluster
	// with the same name already exists.
	CreateCluster(ctx context.Context, in ClusterCreate) (Cluster, error)

	// GetCluster fetches a cluster by id. Returns ErrNotFound if absent.
	GetCluster(ctx context.Context, id uuid.UUID) (Cluster, error)

	// GetClusterByName fetches a cluster by its unique slug-like name.
	// Returns ErrNotFound when no cluster carries that name.
	GetClusterByName(ctx context.Context, name string) (Cluster, error)

	// ListClusters returns up to limit clusters after the given opaque cursor,
	// plus the cursor for the next page (empty when exhausted).
	ListClusters(ctx context.Context, limit int, cursor string) (items []Cluster, nextCursor string, err error)

	// UpdateCluster applies the merge-patch fields set in in. Returns
	// ErrNotFound if the cluster does not exist.
	UpdateCluster(ctx context.Context, id uuid.UUID, in ClusterUpdate) (Cluster, error)

	// DeleteCluster removes a cluster by id. Returns ErrNotFound if absent.
	DeleteCluster(ctx context.Context, id uuid.UUID) error

	// CreateNode inserts a new node. Returns ErrNotFound when the parent
	// cluster does not exist; ErrConflict when (cluster_id, name) already
	// has a node.
	CreateNode(ctx context.Context, in NodeCreate) (Node, error)

	// GetNode fetches a node by id. Returns ErrNotFound if absent.
	GetNode(ctx context.Context, id uuid.UUID) (Node, error)

	// ListNodes returns up to limit nodes after the given opaque cursor. When
	// clusterID is non-nil, results are filtered to that cluster.
	ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) (items []Node, nextCursor string, err error)

	// UpdateNode applies the merge-patch fields set in in. Returns
	// ErrNotFound if the node does not exist.
	UpdateNode(ctx context.Context, id uuid.UUID, in NodeUpdate) (Node, error)

	// DeleteNode removes a node by id. Returns ErrNotFound if absent.
	DeleteNode(ctx context.Context, id uuid.UUID) error

	// UpsertNode inserts a node when no row exists for (cluster_id, name),
	// or updates the mutable fields of the existing row when it does. The
	// returned Node always reflects the post-operation state. Returns
	// ErrNotFound if the parent cluster does not exist.
	UpsertNode(ctx context.Context, in NodeCreate) (Node, error)

	// CreateNamespace inserts a new namespace. Returns ErrNotFound when the
	// parent cluster does not exist; ErrConflict when (cluster_id, name)
	// already has a namespace.
	CreateNamespace(ctx context.Context, in NamespaceCreate) (Namespace, error)

	// GetNamespace fetches a namespace by id. Returns ErrNotFound if absent.
	GetNamespace(ctx context.Context, id uuid.UUID) (Namespace, error)

	// ListNamespaces returns up to limit namespaces after the given opaque
	// cursor. When clusterID is non-nil, results are filtered to that cluster.
	ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) (items []Namespace, nextCursor string, err error)

	// UpdateNamespace applies the merge-patch fields set in in. Returns
	// ErrNotFound if the namespace does not exist.
	UpdateNamespace(ctx context.Context, id uuid.UUID, in NamespaceUpdate) (Namespace, error)

	// DeleteNamespace removes a namespace by id. Returns ErrNotFound if absent.
	DeleteNamespace(ctx context.Context, id uuid.UUID) error

	// UpsertNamespace mirrors UpsertNode for namespaces.
	UpsertNamespace(ctx context.Context, in NamespaceCreate) (Namespace, error)
}
