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

	// DeleteNodesNotIn removes every node of the given cluster whose name is
	// not in keepNames. When keepNames is empty the entire set of nodes for
	// that cluster is removed. Returns the number of rows deleted.
	DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)

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

	// DeleteNamespacesNotIn mirrors DeleteNodesNotIn for namespaces.
	DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)

	// CreatePod inserts a new pod. Returns ErrNotFound when the parent
	// namespace does not exist; ErrConflict when (namespace_id, name) already
	// has a pod.
	CreatePod(ctx context.Context, in PodCreate) (Pod, error)

	// GetPod fetches a pod by id. Returns ErrNotFound if absent.
	GetPod(ctx context.Context, id uuid.UUID) (Pod, error)

	// ListPods returns up to limit pods after the given opaque cursor. When
	// namespaceID is non-nil, results are filtered to that namespace.
	ListPods(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) (items []Pod, nextCursor string, err error)

	// UpdatePod applies the merge-patch fields set in in. Returns
	// ErrNotFound if the pod does not exist.
	UpdatePod(ctx context.Context, id uuid.UUID, in PodUpdate) (Pod, error)

	// DeletePod removes a pod by id. Returns ErrNotFound if absent.
	DeletePod(ctx context.Context, id uuid.UUID) error

	// UpsertPod mirrors UpsertNode, keyed on (namespace_id, name).
	UpsertPod(ctx context.Context, in PodCreate) (Pod, error)

	// DeletePodsNotIn mirrors DeleteNodesNotIn, scoped to a single namespace.
	DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)

	// CreateWorkload inserts a new workload. Returns ErrNotFound when the
	// parent namespace does not exist; ErrConflict when (namespace_id, kind,
	// name) already has a workload.
	CreateWorkload(ctx context.Context, in WorkloadCreate) (Workload, error)

	// GetWorkload fetches a workload by id. Returns ErrNotFound if absent.
	GetWorkload(ctx context.Context, id uuid.UUID) (Workload, error)

	// ListWorkloads returns up to limit workloads after the given opaque
	// cursor, optionally filtered by namespace and/or kind.
	ListWorkloads(ctx context.Context, namespaceID *uuid.UUID, kind *WorkloadKind, limit int, cursor string) (items []Workload, nextCursor string, err error)

	// UpdateWorkload applies merge-patch on mutable fields. Returns
	// ErrNotFound if the workload does not exist.
	UpdateWorkload(ctx context.Context, id uuid.UUID, in WorkloadUpdate) (Workload, error)

	// DeleteWorkload removes a workload by id.
	DeleteWorkload(ctx context.Context, id uuid.UUID) error

	// UpsertWorkload mirrors UpsertPod; keyed on (namespace_id, kind, name).
	UpsertWorkload(ctx context.Context, in WorkloadCreate) (Workload, error)

	// DeleteWorkloadsNotIn removes workloads in the namespace whose
	// (kind, name) tuple is not in keep. An empty keep slice clears every
	// workload for that namespace. The two slices are parallel; callers
	// must ensure len(keepKinds) == len(keepNames).
	DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error)

	// CreateService inserts a new service.
	CreateService(ctx context.Context, in ServiceCreate) (Service, error)

	// GetService fetches a service by id.
	GetService(ctx context.Context, id uuid.UUID) (Service, error)

	// ListServices returns up to limit services, optionally filtered by namespace.
	ListServices(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) (items []Service, nextCursor string, err error)

	// UpdateService applies merge-patch.
	UpdateService(ctx context.Context, id uuid.UUID, in ServiceUpdate) (Service, error)

	// DeleteService removes by id.
	DeleteService(ctx context.Context, id uuid.UUID) error

	// UpsertService mirrors UpsertPod; keyed on (namespace_id, name).
	UpsertService(ctx context.Context, in ServiceCreate) (Service, error)

	// DeleteServicesNotIn mirrors DeletePodsNotIn, scoped to a single namespace.
	DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}
