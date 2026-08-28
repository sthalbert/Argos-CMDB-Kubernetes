package api

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/secrets"
)

// Source discriminators for reconcilable rows. Collector-originated rows
// are swept on each tick; API-originated rows survive reconcile.
const (
	SourceCollector = "collector"
	SourceAPI       = "api"
)

// AuditSourceAPI discriminates API-originated audit_events.source rows
// (ADR-0016; the column also admits "ingest_gw" and "system"). Separate
// from the Kyverno reconcilable-source constants to avoid cross-domain
// coupling — the values happen to overlap but the domains are distinct.
const AuditSourceAPI = "api"

// Sentinel errors returned by Store implementations. Handlers translate these
// into RFC 7807 responses with the matching HTTP status.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	// ErrLastAdmin is returned by UpdateUserGuarded / DeleteUserGuarded
	// when a patch or delete would leave the deployment with zero active
	// admin users. The transactional guard closes the TOCTOU race that a
	// handler-level CountActiveAdmins + UPDATE pair would otherwise leave
	// open under concurrent admin-degrading requests (audit finding H1).
	ErrLastAdmin = errors.New("last admin")
	// ErrInvalidCursor is returned by List* methods when a pagination
	// cursor is malformed, or was minted under different sort/order
	// parameters than the current request. Handlers translate it into
	// a 400 problem+json.
	ErrInvalidCursor = errors.New("invalid cursor")
	// ErrInvalidSort is returned by List* methods when sort/order are
	// not in the entity's allowlist. Handlers translate it into a 400
	// problem+json.
	ErrInvalidSort = errors.New("invalid sort")
)

// ListPage carries the uniform pagination + sort controls shared by
// every paginated List* method (ADR-0042). The zero value means: first
// page, default page size, the entity's historical default order.
type ListPage struct {
	Limit  int
	Cursor string
	// Sort is the API sort key ("" = entity default order). Keys are
	// validated against a per-entity allowlist in the store layer;
	// unknown keys yield ErrInvalidSort.
	Sort string
	// Order is "asc" or "desc". Empty means: desc for the default
	// sort (preserving historical order), asc when Sort is set.
	Order string
}

// PodListFilter collects the optional filters accepted by ListPods. Nil
// fields are ignored; all present fields are AND-combined. Stored as a
// struct (not positional args) so future filters are additive.
type PodListFilter struct {
	NamespaceID *uuid.UUID
	NodeName    *string
	WorkloadID  *uuid.UUID
	// ImageSubstring matches any container (init included) whose `image`
	// field case-insensitively contains the substring.
	ImageSubstring *string
	// Name is the uniform name= filter: ci substring, or anchored
	// glob when the term contains `*` (spec 2026-07-10).
	Name *string
}

// WorkloadListFilter mirrors PodListFilter for ListWorkloads.
type WorkloadListFilter struct {
	NamespaceID    *uuid.UUID
	Kind           *WorkloadKind
	ImageSubstring *string
	// IncludeTerminated, when true, returns soft-deleted workloads in addition
	// to live ones. Default (false) hides rows whose terminated_at is set.
	// ADR-0021 phase 1.
	IncludeTerminated bool
	// ApplicationID, ApplicationName, Unlinked — ADR-0029 link-aware filters.
	// ApplicationID wins on conflict with ApplicationName (the handler resolves
	// names server-side; the store layer also accepts a bare name for callers
	// that bypass the handler, e.g. MCP). Unlinked = true returns only rows
	// with application_id IS NULL.
	ApplicationID   *uuid.UUID
	ApplicationName *string
	Unlinked        *bool
	// ApplicationNameSubstring is a case-insensitive substring match on the
	// linked application's name, used by the cross-entity Search endpoint
	// (ADR-0029 §2.4). LIKE metacharacters are escaped at the SQL layer
	// (ESCAPE '\\'). Ignored when empty. AND-combined with the other
	// link-aware filters.
	ApplicationNameSubstring *string
	// Name is the uniform name= filter: ci substring, or anchored
	// glob when the term contains `*` (spec 2026-07-10).
	Name *string
}

// CascadeCounts holds the number of child resources that will be removed
// when a cluster is deleted via ON DELETE CASCADE. Used by the DeleteCluster
// handler to enrich the audit event with a pre-deletion impact snapshot.
type CascadeCounts struct {
	Namespaces             int `json:"namespaces"`
	Nodes                  int `json:"nodes"`
	Pods                   int `json:"pods"`
	Workloads              int `json:"workloads"`
	Services               int `json:"services"`
	Ingresses              int `json:"ingresses"`
	PersistentVolumes      int `json:"persistent_volumes"`
	PersistentVolumeClaims int `json:"persistent_volume_claims"`
}

// Store is the persistence contract consumed by the REST handlers,
// composed of per-domain interfaces so callers and test doubles can
// depend on just the slice they use.
// Implementations must be safe for concurrent use by multiple goroutines.
type Store interface {
	ClusterStore
	NodeStore
	NamespaceStore
	PodStore
	WorkloadStore
	ServiceStore
	IngressStore
	PersistentVolumeStore
	PersistentVolumeClaimStore
	AuthStore
	SettingsStore
	AuditStore
	CloudAccountStore
	VirtualMachineStore
	OSImageStore
	HistoryStore
	ImageStore
	ApplicationStore
	SecurityGroupStore
	NetworkPolicyStore
	KyvernoStore
	FlowStore

	// Ping verifies that the underlying database is reachable.
	Ping(ctx context.Context) error
}

// ClusterListFilter — uniform list filter for clusters. Name matches
// case-insensitively over BOTH name and display_name (substring, or
// anchored glob when the term contains `*`).
type ClusterListFilter struct {
	Name              *string
	IncludeTerminated bool
}

// ClusterStore covers cluster CRUD, the idempotent EnsureCluster, and the
// cascade soft-delete helpers.
type ClusterStore interface {
	// EnsureCluster reconciles a cluster row keyed by name with one of three
	// outcomes:
	//   - CREATE: no row exists, a new one is inserted and created=true.
	//   - NO-OP: a live row exists, it is returned unchanged with created=false.
	//   - RESTORE: a soft-deleted row exists (terminated_at IS NOT NULL); its
	//     terminated_at is cleared, a change_type='restore' history row is
	//     written, and created=false is returned.
	//
	// The request body is otherwise ignored on hit — callers wanting to update
	// fields on an existing cluster must follow up with UpdateCluster.
	//
	// EnsureCluster never returns ErrConflict; concurrent inserts of the same
	// name are serialised at the database via INSERT ... ON CONFLICT DO
	// NOTHING, falling back to a SELECT for the losing writer. Lightweight
	// in-memory implementations that do not track terminated_at may safely
	// skip the RESTORE branch and treat any existing row as NO-OP.
	EnsureCluster(ctx context.Context, in ClusterCreate) (cluster Cluster, created bool, err error)

	// GetCluster fetches a cluster by id. Returns ErrNotFound if absent.
	GetCluster(ctx context.Context, id uuid.UUID) (Cluster, error)

	// GetClusterByName fetches a cluster by its unique slug-like name.
	// Returns ErrNotFound when no cluster carries that name.
	GetClusterByName(ctx context.Context, name string) (Cluster, error)

	// ListClusters returns up to page.Limit clusters after the given cursor,
	// filtered by filter, plus the cursor for the next page (empty when exhausted).
	ListClusters(ctx context.Context, filter ClusterListFilter, page ListPage) (items []Cluster, nextCursor string, err error)

	// UpdateCluster applies the merge-patch fields set in in. Returns
	// ErrNotFound if the cluster does not exist.
	UpdateCluster(ctx context.Context, id uuid.UUID, in ClusterUpdate) (Cluster, error)

	// DeleteCluster removes a cluster by id. Returns ErrNotFound if absent.
	DeleteCluster(ctx context.Context, id uuid.UUID) error

	// SoftDeleteCluster marks the cluster and its live children
	// (namespaces, nodes, workloads) as terminated in a single transaction.
	// See ADR-0021 §IMP-007. Idempotent on already-terminated rows; returns
	// ErrNotFound when the cluster does not exist.
	SoftDeleteCluster(ctx context.Context, id uuid.UUID) error

	// CountClusterChildren counts child resources that will be cascade-deleted
	// when the given cluster is removed. Returns ErrNotFound if the cluster
	// does not exist. Used to build the pre-deletion audit snapshot (ADR-0010).
	CountClusterChildren(ctx context.Context, clusterID uuid.UUID) (CascadeCounts, error)
}

// NodeListFilter — nil fields are ignored; set fields AND-combine
// (same contract as PodListFilter).
type NodeListFilter struct {
	ClusterID *uuid.UUID
	// Name is the uniform name= filter: ci substring, or anchored
	// glob when the term contains `*` (spec 2026-07-10).
	Name              *string
	IncludeTerminated bool
}

// NodeStore covers node CRUD, upsert, and reconcile.
type NodeStore interface {
	// CreateNode inserts a new node. Returns ErrNotFound when the parent
	// cluster does not exist; ErrConflict when (cluster_id, name) already
	// has a node.
	CreateNode(ctx context.Context, in NodeCreate) (Node, error)

	// GetNode fetches a node by id. Returns ErrNotFound if absent.
	GetNode(ctx context.Context, id uuid.UUID) (Node, error)

	// ListNodes returns a paged list of nodes matching filter, sorted by
	// page.Sort/page.Order. Unknown sort keys → ErrInvalidSort; mismatched
	// cursor → ErrInvalidCursor.
	ListNodes(ctx context.Context, filter NodeListFilter, page ListPage) (items []Node, nextCursor string, err error)

	// UpdateNode applies the merge-patch fields set in in. Returns
	// ErrNotFound if the node does not exist.
	UpdateNode(ctx context.Context, id uuid.UUID, in NodeUpdate) (Node, error)

	// DeleteNode removes a node by id. Returns ErrNotFound if absent.
	DeleteNode(ctx context.Context, id uuid.UUID) error

	// UpsertNode inserts a node when no row exists for (cluster_id, name),
	// or updates the mutable fields of the existing row when it does. The
	// returned Node always reflects the post-operation state. The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	// Returns ErrNotFound if the parent cluster does not exist.
	UpsertNode(ctx context.Context, in NodeCreate) (Node, UpsertOutcome, error)

	// DeleteNodesNotIn removes every node of the given cluster whose name is
	// not in keepNames. When keepNames is empty the entire set of nodes for
	// that cluster is removed. Returns the number of rows deleted.
	DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)

	// BackfillNodeImages sets image_id/image_name on every node whose
	// provider_id contains a reported provider_vm_id (substring match).
	// Idempotent: a node is updated only when a value actually changes.
	// Returns matched (nodes whose provider_id matched a mapping) and
	// updated (nodes whose image fields actually changed). Empty image
	// strings are stored as NULL. Used by the vm-collector node-image
	// ingest endpoint (ADR-0040).
	BackfillNodeImages(ctx context.Context, images []NodeImage) (matched, updated int, err error)
}

// NamespaceListFilter — nil fields are ignored; set fields AND-combine
// (same contract as NodeListFilter).
type NamespaceListFilter struct {
	ClusterID *uuid.UUID
	// Name is the uniform name= filter: ci substring, or anchored
	// glob when the term contains `*` (spec 2026-07-10).
	Name              *string
	IncludeTerminated bool
}

// NamespaceStore covers namespace CRUD, upsert, soft-delete, and reconcile.
type NamespaceStore interface {
	// CreateNamespace inserts a new namespace. Returns ErrNotFound when the
	// parent cluster does not exist; ErrConflict when (cluster_id, name)
	// already has a namespace.
	CreateNamespace(ctx context.Context, in NamespaceCreate) (Namespace, error)

	// GetNamespace fetches a namespace by id. Returns ErrNotFound if absent.
	GetNamespace(ctx context.Context, id uuid.UUID) (Namespace, error)

	// ListNamespaces returns a paged list of namespaces matching filter,
	// sorted by page.Sort/page.Order. Unknown sort keys → ErrInvalidSort;
	// mismatched cursor → ErrInvalidCursor.
	ListNamespaces(ctx context.Context, filter NamespaceListFilter, page ListPage) (items []Namespace, nextCursor string, err error)

	// UpdateNamespace applies the merge-patch fields set in in. Returns
	// ErrNotFound if the namespace does not exist.
	UpdateNamespace(ctx context.Context, id uuid.UUID, in NamespaceUpdate) (Namespace, error)

	// DeleteNamespace removes a namespace by id. Returns ErrNotFound if absent.
	DeleteNamespace(ctx context.Context, id uuid.UUID) error

	// SoftDeleteNamespace marks the namespace and its live workloads as
	// terminated in a single transaction. See ADR-0021 §IMP-007.
	SoftDeleteNamespace(ctx context.Context, id uuid.UUID) error

	// UpsertNamespace mirrors UpsertNode for namespaces. The second return
	// value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertNamespace(ctx context.Context, in NamespaceCreate) (Namespace, UpsertOutcome, error)

	// DeleteNamespacesNotIn mirrors DeleteNodesNotIn for namespaces.
	DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
}

// PodStore covers pod CRUD, upsert, and reconcile.
type PodStore interface {
	// CreatePod inserts a new pod. Returns ErrNotFound when the parent
	// namespace does not exist; ErrConflict when (namespace_id, name) already
	// has a pod.
	CreatePod(ctx context.Context, in PodCreate) (Pod, error)

	// GetPod fetches a pod by id. Returns ErrNotFound if absent.
	GetPod(ctx context.Context, id uuid.UUID) (Pod, error)

	// ListPods returns a paged list of pods matching filter, sorted by
	// page.Sort/page.Order. Unknown sort keys → ErrInvalidSort; mismatched
	// cursor → ErrInvalidCursor.
	ListPods(ctx context.Context, filter PodListFilter, page ListPage) (items []Pod, nextCursor string, err error)

	// UpdatePod applies the merge-patch fields set in in. Returns
	// ErrNotFound if the pod does not exist.
	UpdatePod(ctx context.Context, id uuid.UUID, in PodUpdate) (Pod, error)

	// DeletePod removes a pod by id. Returns ErrNotFound if absent.
	DeletePod(ctx context.Context, id uuid.UUID) error

	// UpsertPod mirrors UpsertNode, keyed on (namespace_id, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertPod(ctx context.Context, in PodCreate) (Pod, UpsertOutcome, error)

	// DeletePodsNotIn mirrors DeleteNodesNotIn, scoped to a single namespace.
	DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}

// WorkloadStore covers workload CRUD, upsert, and reconcile.
type WorkloadStore interface {
	// CreateWorkload inserts a new workload. Returns ErrNotFound when the
	// parent namespace does not exist; ErrConflict when (namespace_id, kind,
	// name) already has a workload.
	CreateWorkload(ctx context.Context, in WorkloadCreate) (Workload, error)

	// GetWorkload fetches a workload by id. Returns ErrNotFound if absent.
	GetWorkload(ctx context.Context, id uuid.UUID) (Workload, error)

	// ListWorkloads returns a paged list of workloads matching filter, sorted by
	// page.Sort/page.Order. Unknown sort keys → ErrInvalidSort; mismatched
	// cursor → ErrInvalidCursor.
	ListWorkloads(ctx context.Context, filter WorkloadListFilter, page ListPage) (items []Workload, nextCursor string, err error)

	// UpdateWorkload applies merge-patch on mutable fields. Returns
	// ErrNotFound if the workload does not exist. clearApplication carries the
	// three-state ADR-0029 link semantics the *uuid.UUID field can't express:
	// when true (explicit `"application_id": null` in the body) the link is
	// cleared; an explicit application_id value still wins over the null.
	UpdateWorkload(ctx context.Context, id uuid.UUID, in WorkloadUpdate, clearApplication bool) (Workload, error)

	// DeleteWorkload removes a workload by id.
	DeleteWorkload(ctx context.Context, id uuid.UUID) error

	// UpsertWorkload mirrors UpsertPod; keyed on (namespace_id, kind, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertWorkload(ctx context.Context, in WorkloadCreate) (Workload, UpsertOutcome, error)

	// DeleteWorkloadsNotIn removes workloads in the namespace whose
	// (kind, name) tuple is not in keep. An empty keep slice clears every
	// workload for that namespace. The two slices are parallel; callers
	// must ensure len(keepKinds) == len(keepNames).
	DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error)
}

// ServiceListFilter is the predicate set accepted by ListServices.
type ServiceListFilter struct {
	NamespaceID *uuid.UUID
	Name        *string
}

// ServiceStore covers service CRUD, upsert, and reconcile.
type ServiceStore interface {
	// CreateService inserts a new service.
	CreateService(ctx context.Context, in ServiceCreate) (Service, error)

	// GetService fetches a service by id.
	GetService(ctx context.Context, id uuid.UUID) (Service, error)

	// ListServices returns a cursor-paginated page of services, optionally
	// filtered by namespace and/or name. See ServiceListFilter for the
	// accepted predicates.
	ListServices(ctx context.Context, filter ServiceListFilter, page ListPage) (items []Service, nextCursor string, err error)

	// UpdateService applies merge-patch.
	UpdateService(ctx context.Context, id uuid.UUID, in ServiceUpdate) (Service, error)

	// DeleteService removes by id.
	DeleteService(ctx context.Context, id uuid.UUID) error

	// UpsertService mirrors UpsertPod; keyed on (namespace_id, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertService(ctx context.Context, in ServiceCreate) (Service, UpsertOutcome, error)

	// DeleteServicesNotIn mirrors DeletePodsNotIn, scoped to a single namespace.
	DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}

// IngressListFilter is the predicate set accepted by ListIngresses.
type IngressListFilter struct {
	NamespaceID *uuid.UUID
	Name        *string
}

// IngressStore covers ingress CRUD, upsert, and reconcile.
type IngressStore interface {
	// CreateIngress inserts a new ingress.
	CreateIngress(ctx context.Context, in IngressCreate) (Ingress, error)

	// GetIngress fetches an ingress by id.
	GetIngress(ctx context.Context, id uuid.UUID) (Ingress, error)

	// ListIngresses returns a cursor-paginated page of ingresses, optionally
	// filtered by namespace and/or name. See IngressListFilter for the
	// accepted predicates.
	ListIngresses(ctx context.Context, filter IngressListFilter, page ListPage) (items []Ingress, nextCursor string, err error)

	// UpdateIngress applies merge-patch.
	UpdateIngress(ctx context.Context, id uuid.UUID, in IngressUpdate) (Ingress, error)

	// DeleteIngress removes by id.
	DeleteIngress(ctx context.Context, id uuid.UUID) error

	// UpsertIngress mirrors UpsertService; keyed on (namespace_id, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertIngress(ctx context.Context, in IngressCreate) (Ingress, UpsertOutcome, error)

	// DeleteIngressesNotIn mirrors DeleteServicesNotIn.
	DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}

// PersistentVolumeListFilter is the predicate set accepted by ListPersistentVolumes.
type PersistentVolumeListFilter struct {
	ClusterID *uuid.UUID
	Name      *string
}

// PersistentVolumeStore covers cluster-scoped PV CRUD, upsert, and reconcile.
type PersistentVolumeStore interface {
	// CreatePersistentVolume inserts a new cluster-scoped PV. Returns
	// ErrNotFound when the parent cluster does not exist; ErrConflict when
	// (cluster_id, name) already has a PV.
	CreatePersistentVolume(ctx context.Context, in PersistentVolumeCreate) (PersistentVolume, error)

	// GetPersistentVolume fetches a PV by id.
	GetPersistentVolume(ctx context.Context, id uuid.UUID) (PersistentVolume, error)

	// ListPersistentVolumes returns a cursor-paginated page of PVs, optionally
	// filtered by cluster and/or name. See PersistentVolumeListFilter for the
	// accepted predicates.
	ListPersistentVolumes(
		ctx context.Context,
		filter PersistentVolumeListFilter,
		page ListPage,
	) (items []PersistentVolume, nextCursor string, err error)

	// UpdatePersistentVolume applies merge-patch.
	UpdatePersistentVolume(ctx context.Context, id uuid.UUID, in PersistentVolumeUpdate) (PersistentVolume, error)

	// DeletePersistentVolume removes by id.
	DeletePersistentVolume(ctx context.Context, id uuid.UUID) error

	// UpsertPersistentVolume mirrors UpsertNode; keyed on (cluster_id, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertPersistentVolume(ctx context.Context, in PersistentVolumeCreate) (PersistentVolume, UpsertOutcome, error)

	// DeletePersistentVolumesNotIn removes cluster-scoped PVs whose name is
	// not in keepNames. An empty keep slice clears every PV in that cluster.
	DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
}

// PersistentVolumeClaimListFilter is the predicate set accepted by ListPersistentVolumeClaims.
type PersistentVolumeClaimListFilter struct {
	NamespaceID *uuid.UUID
	Name        *string
}

// PersistentVolumeClaimStore covers PVC CRUD, upsert, and reconcile.
type PersistentVolumeClaimStore interface {
	// CreatePersistentVolumeClaim inserts a new PVC. Returns ErrNotFound
	// when the parent namespace or the bound volume does not exist;
	// ErrConflict when (namespace_id, name) already has a PVC.
	CreatePersistentVolumeClaim(ctx context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, error)

	// GetPersistentVolumeClaim fetches a PVC by id.
	GetPersistentVolumeClaim(ctx context.Context, id uuid.UUID) (PersistentVolumeClaim, error)

	// ListPersistentVolumeClaims returns a cursor-paginated page of PVCs, optionally
	// filtered by namespace and/or name. See PersistentVolumeClaimListFilter for the
	// accepted predicates.
	ListPersistentVolumeClaims(
		ctx context.Context,
		filter PersistentVolumeClaimListFilter,
		page ListPage,
	) (items []PersistentVolumeClaim, nextCursor string, err error)

	// UpdatePersistentVolumeClaim applies merge-patch.
	UpdatePersistentVolumeClaim(ctx context.Context, id uuid.UUID, in PersistentVolumeClaimUpdate) (PersistentVolumeClaim, error)

	// DeletePersistentVolumeClaim removes by id.
	DeletePersistentVolumeClaim(ctx context.Context, id uuid.UUID) error

	// UpsertPersistentVolumeClaim mirrors UpsertPod; keyed on (namespace_id, name). The second
	// return value classifies the operation for audit filtering (ADR-0024):
	// OutcomeInserted for a fresh insert, OutcomeBusinessChanged when a
	// business field changed, OutcomeNoChange when only clock fields moved.
	UpsertPersistentVolumeClaim(ctx context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, UpsertOutcome, error)

	// DeletePersistentVolumeClaimsNotIn mirrors DeletePodsNotIn.
	DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}

// UserListFilter is the predicate set accepted by ListUsers.
// Name matches the username field (case-insensitive).
type UserListFilter struct {
	Name *string
}

// APITokenListFilter is the predicate set accepted by ListAPITokens.
type APITokenListFilter struct { //nolint:revive // stutter is acceptable here for clarity alongside the APIToken generated type
	Name *string
}

// SessionListFilter is the predicate set accepted by ListSessions.
// Name matches the owning user's username (case-insensitive).
type SessionListFilter struct {
	Name *string
}

// AuthStore covers the auth substrate (ADR-0007): users, sessions, API
// tokens, and the one-shot OIDC state rows. The auth package also defines
// a narrower `auth.Store` interface with just the lookup methods the
// middleware needs. The PG store satisfies both; see
// `internal/auth/middleware.go` for the contract.
type AuthStore interface {
	// CountActiveAdmins returns the number of `admin`-role users without a
	// `disabled_at` timestamp. Used by the first-install bootstrap check.
	CountActiveAdmins(ctx context.Context) (int, error)

	// CountActiveUnlockedAdmins returns the number of admins with
	// disabled_at IS NULL AND locked_at IS NULL.
	CountActiveUnlockedAdmins(ctx context.Context) (int, error)

	// PickRescueTarget returns the most-recently-active admin row.
	// Used by the boot-time rescue when CountActiveUnlockedAdmins == 0.
	// ORDER BY last_login_at DESC NULLS LAST, created_at ASC, LIMIT 1.
	PickRescueTarget(ctx context.Context) (User, error)

	// RescueAdmin atomically resets the rescue target: sets the new
	// password hash, clears locked_at, zeroes failed_login_count, sets
	// disabled_at = NULL, forces must_change_password = true, deletes
	// all of the user's sessions.
	RescueAdmin(ctx context.Context, id uuid.UUID, hash string) error

	// CreateUser inserts a new human user. Returns ErrConflict on
	// case-insensitive username collision.
	CreateUser(ctx context.Context, in UserInsert) (User, error)

	// GetUser fetches by id. ErrNotFound if absent.
	GetUser(ctx context.Context, id uuid.UUID) (User, error)

	// GetUserByUsername looks up by case-insensitive username — the login
	// path. Returns ErrNotFound when no such user exists or the account
	// is disabled, to prevent username enumeration via timing differences
	// (callers always do an argon2 verify regardless).
	GetUserByUsername(ctx context.Context, username string) (UserWithSecret, error)

	// ListUsers returns a page of users (admin view), sorted and filtered per ListPage/UserListFilter.
	ListUsers(ctx context.Context, filter UserListFilter, page ListPage) (items []User, nextCursor string, err error)

	// UpdateUser applies merge-patch on role / disabled / must_change_password.
	// Password changes go through SetUserPassword because they need the
	// hashed form, not plaintext.
	UpdateUser(ctx context.Context, id uuid.UUID, in UserPatch) (User, error)

	// UpdateUserGuarded is the transactional wrapper around UpdateUser
	// that enforces the last-admin invariant atomically. If the patch
	// would demote (role != admin) or disable an active admin and no
	// other active admin exists, it returns ErrLastAdmin without
	// mutating the row. Implementations MUST hold a row-level lock on
	// the candidate-admin set across the count + update so two
	// concurrent demotions cannot both observe `n=2` and commit.
	UpdateUserGuarded(ctx context.Context, id uuid.UUID, in UserPatch) (User, error)

	// SetUserPassword stores a new argon2id hash, toggling the
	// must_change_password flag as specified. On success also deletes every
	// active session for the user so a password change effectively logs
	// out other tabs/devices.
	SetUserPassword(ctx context.Context, id uuid.UUID, hash string, mustChange bool) error

	// TouchUserLogin refreshes last_login_at — called on successful login.
	TouchUserLogin(ctx context.Context, id uuid.UUID, now time.Time) error

	// IncrementFailedLogin bumps users.failed_login_count by one. If the
	// new count is >= threshold and the account was not already locked,
	// sets locked_at = now() in the same statement and returns
	// (locked=true). Idempotent on already-locked accounts: returns
	// (locked=false) and leaves the row untouched.
	//
	// No last-admin guard; the lockout fires uniformly. Recovery is via
	// the boot-time admin-rescue hook (cmd/longue-vue/main.go).
	IncrementFailedLogin(ctx context.Context, id uuid.UUID, threshold int) (locked bool, err error)

	// ResetFailedLogin sets failed_login_count = 0 and locked_at = NULL.
	// Called on a successful password verification. Safe when already
	// at zero (UPDATE is a no-op on the row).
	ResetFailedLogin(ctx context.Context, id uuid.UUID) error

	// DeleteUser removes a user. ON DELETE CASCADE sweeps their sessions
	// and identities; api_tokens they minted are retained (ON DELETE
	// RESTRICT) so CI pipelines don't silently break on admin churn.
	DeleteUser(ctx context.Context, id uuid.UUID) error

	// DeleteUserGuarded is the transactional wrapper around DeleteUser
	// that enforces the last-admin invariant atomically (audit finding
	// H1). Returns ErrLastAdmin when the target is the only currently
	// active admin. Implementations MUST hold a row-level lock on the
	// active-admin set across the count + delete.
	DeleteUserGuarded(ctx context.Context, id uuid.UUID) error

	// CreateSession inserts a new session row.
	CreateSession(ctx context.Context, in SessionInsert) error

	// GetActiveSession, TouchSession — the auth.Store methods, declared
	// here so a single PG implementation satisfies both interfaces.
	GetActiveSession(ctx context.Context, id string) (auth.Session, error)
	TouchSession(ctx context.Context, id string, now time.Time, newExpiry time.Time) error

	// GetUserForAuth — auth.Store lookup: lightweight view the middleware
	// needs after a session resolves.
	GetUserForAuth(ctx context.Context, id uuid.UUID) (auth.User, error)

	// DeleteSession revokes a single session by its cookie-value id.
	// Used by the logout handler which reads the cookie from ctx.
	DeleteSession(ctx context.Context, id string) error

	// DeleteSessionByPublicID revokes by the UUID public handle. Used
	// by the admin revoke endpoint so cookie values never leave the DB.
	DeleteSessionByPublicID(ctx context.Context, publicID uuid.UUID) error

	// DeleteSessionsForUser revokes all active sessions for a user. Called
	// when the user is disabled or changes their password.
	DeleteSessionsForUser(ctx context.Context, userID uuid.UUID) error

	// ListSessions returns a page of active sessions with denormalised
	// username for admin display.
	ListSessions(ctx context.Context, filter SessionListFilter, page ListPage) (items []Session, nextCursor string, err error)

	// CreateAPIToken inserts a new token row. `hash` is argon2id of the
	// full plaintext; `prefix` is the first 8 chars of the plaintext
	// stored in the clear for O(1) lookup.
	CreateAPIToken(ctx context.Context, in APITokenInsert) (ApiToken, error)

	// GetActiveTokenByPrefix, TouchToken — auth.Store lookup path.
	GetActiveTokenByPrefix(ctx context.Context, prefix string) (auth.APIToken, error)
	TouchToken(ctx context.Context, id uuid.UUID, now time.Time) error

	// ListAPITokens (admin view, metadata only — plaintext is never in
	// responses except at creation).
	ListAPITokens(ctx context.Context, filter APITokenListFilter, page ListPage) (items []ApiToken, nextCursor string, err error)

	// RevokeAPIToken sets revoked_at. Idempotent: revoking an
	// already-revoked token returns nil.
	RevokeAPIToken(ctx context.Context, id uuid.UUID, now time.Time) error

	// --- OIDC auth substrate (ADR-0007 PR 3) ----------------------------

	// GetUserByIdentity returns the user linked to (issuer, subject) via
	// the user_identities table, or ErrNotFound when no identity row is
	// present — i.e., the IdP user has never logged in before. Disabled
	// users are treated as NotFound to match local-login semantics.
	GetUserByIdentity(ctx context.Context, issuer, subject string) (User, error)

	// CreateUserWithIdentity inserts a user and its OIDC identity row in
	// one transaction. On username collision the caller is expected to
	// pick a new one and retry.
	CreateUserWithIdentity(ctx context.Context, in UserInsert, ident UserIdentityInsert) (User, error)

	// TouchUserIdentity refreshes last_seen_at on the identity row.
	TouchUserIdentity(ctx context.Context, userID uuid.UUID, issuer, subject string, now time.Time) error

	// CreateOidcAuthState persists the in-flight auth-code state.
	CreateOidcAuthState(ctx context.Context, in OidcAuthStateInsert) error

	// ConsumeOidcAuthState atomically reads and deletes the row keyed on
	// state, returning the code_verifier + nonce. Rejects expired rows
	// with ErrNotFound. One-shot by design.
	ConsumeOidcAuthState(ctx context.Context, state string) (codeVerifier, nonce string, err error)
}

// SettingsStore covers the single-row runtime feature-toggle table.
type SettingsStore interface {
	// GetSettings returns the current runtime settings (single-row table).
	GetSettings(ctx context.Context) (Settings, error)

	// UpdateSettings applies the merge-patch on the settings row.
	UpdateSettings(ctx context.Context, in SettingsPatch) (Settings, error)
}

// AuditStore covers the append-only audit_events table (ADR-0010).
type AuditStore interface {
	// InsertAuditEvent appends one row to audit_events. Called from the
	// audit middleware after the wrapped handler has produced a status.
	// Never returns ErrConflict — id collisions are caller bugs.
	InsertAuditEvent(ctx context.Context, in AuditEventInsert) error

	// ListAuditEvents returns audit events paged by opaque cursor. filter
	// fields are AND-combined; nil fields are ignored. page.Sort selects
	// the sort column (default: occurred_at DESC); ErrInvalidSort is
	// returned for unknown keys and ErrInvalidCursor for stale/mismatched
	// cursors.
	ListAuditEvents(ctx context.Context, filter AuditEventFilter, page ListPage) (items []AuditEvent, nextCursor string, err error)
}

// CloudAccountStore covers cloud accounts (ADR-0015), including the
// encrypted-credential paths.
type CloudAccountStore interface {
	// UpsertCloudAccount idempotently registers a cloud account by
	// (provider, name). New rows are created in status='pending_credentials'.
	UpsertCloudAccount(ctx context.Context, in CloudAccountUpsert) (CloudAccount, error)

	// GetCloudAccount fetches by id. ErrNotFound when absent.
	GetCloudAccount(ctx context.Context, id uuid.UUID) (CloudAccount, error)

	// GetCloudAccountByName fetches by (provider, name). ErrNotFound when absent.
	GetCloudAccountByName(ctx context.Context, provider, name string) (CloudAccount, error)

	// GetCloudAccountByNameAny fetches by name across every provider
	// in a single query. Used by credential-fetch handlers so a
	// caller-by-name lookup doesn't fan out to one SQL round-trip per
	// supported provider. Returns ErrNotFound when no row matches.
	GetCloudAccountByNameAny(ctx context.Context, name string) (CloudAccount, error)

	// ListCloudAccounts returns a cursor-paginated page of cloud accounts,
	// optionally filtered by CloudAccountListFilter.
	ListCloudAccounts(ctx context.Context, filter CloudAccountListFilter, page ListPage) (items []CloudAccount, nextCursor string, err error)

	// UpdateCloudAccount applies merge-patch on curated metadata + name.
	// Status transitions to/from `disabled` and `pending_credentials` are
	// rejected here — see DisableCloudAccount / EnableCloudAccount and
	// SetCloudAccountCredentials. Status field on the patch is allowed
	// only between `active` and `error`.
	UpdateCloudAccount(ctx context.Context, id uuid.UUID, in CloudAccountPatch) (CloudAccount, error)

	// SetCloudAccountCredentials writes AK plaintext + SK ciphertext+nonce+kid
	// and transitions status to `active`. ErrNotFound if the account is missing.
	SetCloudAccountCredentials(ctx context.Context, id uuid.UUID, accessKey string, encSK secrets.Ciphertext) (CloudAccount, error)

	// GetCloudAccountCredentials returns AK + SK ciphertext for callers
	// (the handler decrypts). Returns ErrNotFound when status =
	// `pending_credentials` or the row is absent. Returns ErrConflict
	// when status = `disabled` (caller maps to 403).
	GetCloudAccountCredentials(ctx context.Context, id uuid.UUID) (accessKey string, encSK secrets.Ciphertext, err error)

	// UpdateCloudAccountStatus is the collector heartbeat path. Only
	// allows transitions between `active` and `error`; rejects to/from
	// `disabled` or `pending_credentials`.
	UpdateCloudAccountStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastError *string) error

	// DisableCloudAccount sets disabled_at and status='disabled'.
	DisableCloudAccount(ctx context.Context, id uuid.UUID) error

	// EnableCloudAccount clears disabled_at and resets status (active if
	// credentials are present, otherwise pending_credentials).
	EnableCloudAccount(ctx context.Context, id uuid.UUID) error

	// DeleteCloudAccount removes a cloud account (cascades to VMs and tokens).
	DeleteCloudAccount(ctx context.Context, id uuid.UUID) error

	// CountCloudAccountsWithSecrets is used at startup to decide whether
	// missing master-key configuration is fatal (see ADR-0015 §4).
	CountCloudAccountsWithSecrets(ctx context.Context) (int, error)
}

// VirtualMachineStore covers cloud VMs (ADR-0015): push-collector upsert,
// curated patches, and soft-delete reconcile.
type VirtualMachineStore interface {
	// UpsertVirtualMachine inserts a new VM or updates the existing row by
	// (cloud_account_id, provider_vm_id). Server-side dedup against
	// nodes.provider_id: returns ErrConflict if the provider_vm_id already
	// appears in any node's provider_id (the VM is already inventoried as
	// a Kubernetes node). The second return value classifies the operation
	// for audit filtering (ADR-0024): OutcomeInserted for a fresh insert,
	// OutcomeBusinessChanged when a business field changed, OutcomeNoChange
	// when only clock fields moved.
	UpsertVirtualMachine(ctx context.Context, in VirtualMachineUpsert) (VirtualMachine, UpsertOutcome, error)

	// GetVirtualMachine fetches by id. ErrNotFound when absent.
	GetVirtualMachine(ctx context.Context, id uuid.UUID) (VirtualMachine, error)

	// ListVirtualMachines returns paged VMs filtered by VirtualMachineListFilter.
	// terminated rows are excluded unless filter.IncludeTerminated.
	ListVirtualMachines(
		ctx context.Context,
		filter VirtualMachineListFilter,
		page ListPage,
	) (items []VirtualMachine, nextCursor string, err error)

	// UpdateVirtualMachine applies merge-patch on curated-only fields.
	UpdateVirtualMachine(ctx context.Context, id uuid.UUID, in VirtualMachinePatch) (VirtualMachine, error)

	// DeleteVirtualMachine soft-deletes by setting terminated_at,
	// power_state='terminated', ready=false. Hard delete is left to retention.
	DeleteVirtualMachine(ctx context.Context, id uuid.UUID) error

	// ReconcileVirtualMachines soft-deletes every row of the given account
	// whose provider_vm_id is not in keep AND terminated_at IS NULL.
	// Returns the count of rows tombstoned.
	ReconcileVirtualMachines(ctx context.Context, accountID uuid.UUID, keepProviderVMIDs []string) (tombstoned int64, err error)

	// ListDistinctVMApplications returns the distinct products and, for
	// each, the sorted list of distinct versions seen across every
	// non-terminated VM's applications array. Drives the cascading
	// product → version dropdown in the VM list UI (ADR-0019 §3).
	ListDistinctVMApplications(ctx context.Context) ([]VMApplicationDistinct, error)

	// ListVMsWithApplicationEntry returns every non-terminated VM that has
	// at least one applications[] JSONB entry whose per-entry
	// application_id matches the given application — regardless of the VM's
	// row-level application_id link. Used by the per-application EOL
	// aggregator (ADR-0029 §5 source 3) to surface VM-application entries
	// whose parent VM is not itself linked. The full VM is returned so the
	// caller can read its annotations and filter the matching entries.
	ListVMsWithApplicationEntry(ctx context.Context, appID uuid.UUID) ([]VirtualMachine, error)
}

// OSImageStore exposes the deduplicated inventory of OS images in service
// (cloud VMs ∪ cluster nodes), keyed by image name (ADR-0040).
type OSImageStore interface {
	// ListOSImages returns one row per distinct image_name referenced by a
	// non-terminated VM or an active node, with the distinct image ids and
	// per-source counts. Ordered by image_name.
	ListOSImages(ctx context.Context) ([]OSImage, error)
}

// HistoryStore covers time-travel history reads (ADR-0021 Phase 3).
type HistoryStore interface {
	// ListEntityHistory returns up to limit history rows for one entity,
	// newest-first. kind must be one of "clusters", "namespaces", "nodes",
	// "workloads". cursor is the opaque pagination token from a prior call
	// (empty = first page). nextCursor is empty when there are no further pages.
	ListEntityHistory(
		ctx context.Context,
		kind string,
		entityID uuid.UUID,
		limit int,
		cursor string,
	) (rows []HistoryRow, nextCursor string, err error)

	// GetEntityAsOf returns the entity's history row that was valid at time t
	// for the given kind and entityID. Returns ErrNotFound when no row covers t.
	GetEntityAsOf(ctx context.Context, kind string, entityID uuid.UUID, t time.Time) (map[string]any, error)

	// IsTimeTravelEnabled reports whether the time_travel_enabled setting is
	// currently true. Used by history handlers to return 503 when disabled.
	IsTimeTravelEnabled(ctx context.Context) (bool, error)
}

// ImageStore covers the image-versions subsystem: registries allowlist
// (ADR-0022), discovered versions, mirror-origin resolutions (ADR-0026),
// and manual origin mappings (ADR-0030).
type ImageStore interface {
	// Image registries
	ListImageRegistries(ctx context.Context) ([]ImageRegistry, error)
	GetImageRegistry(ctx context.Context, hostname, pathPrefix string) (ImageRegistry, error)
	CreateImageRegistry(ctx context.Context, in ImageRegistryUpsert) (ImageRegistry, error)
	UpdateImageRegistry(ctx context.Context, hostname, pathPrefix string, p ImageRegistryPatch) (ImageRegistry, error)
	DeleteImageRegistry(ctx context.Context, hostname, pathPrefix string) error
	FindMirrorForRef(ctx context.Context, hostname, imagePath string) (ImageRegistry, error)
	GetMirrorAuthToken(ctx context.Context, hostname, pathPrefix string) (string, error)

	// Image versions
	UpsertImageVersion(ctx context.Context, in ImageVersionUpsert) (ImageVersionRow, error)
	GetImageVersionsByRepo(ctx context.Context, imageRepo string) ([]ImageVersionRow, error)
	ListImageVersionsByRepo(ctx context.Context, p ImageVersionListParams) (items []ImageVersionRepoView, nextCursor string, err error)
	DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
	DistinctImageRefs(ctx context.Context) ([]string, error)

	// Image origin resolutions (ADR-0026 extension)
	UpsertImageOriginResolution(ctx context.Context, in ImageOriginResolutionUpsert) (ImageOriginResolution, error)
	GetImageOriginResolution(ctx context.Context, mirrorImageRepo, variant string) (ImageOriginResolution, error)
	DeleteImageOriginResolutionsNotIn(ctx context.Context, keep [][2]string) (int64, error)

	// Image origin mappings (ADR-0030).
	ListImageOriginMappings(ctx context.Context, p StoreListImageOriginMappingsParams) (items []ImageOriginMapping, nextCursor string, err error)
	GetImageOriginMapping(ctx context.Context, imageName string) (ImageOriginMapping, error)
	CreateImageOriginMapping(ctx context.Context, in ImageOriginMappingCreate, createdBy string) (ImageOriginMapping, error)
	PatchImageOriginMapping(ctx context.Context, imageName string, p ImageOriginMappingPatch, updatedBy string) (ImageOriginMapping, error)
	DeleteImageOriginMapping(ctx context.Context, imageName string) error
	FindImageOrigin(ctx context.Context, imageName string) (publicRegistry string, err error)
}

// ApplicationStore covers the operator-curated applicative layer
// (ADR-0029): application blocks and applications, including the
// effective-DICT helpers.
type ApplicationStore interface {
	// Application blocks (ADR-0029).
	CreateApplicationBlock(ctx context.Context, in ApplicationBlockCreate) (ApplicationBlock, error)
	GetApplicationBlock(ctx context.Context, id uuid.UUID) (ApplicationBlock, error)
	GetApplicationBlockByName(ctx context.Context, name string) (ApplicationBlock, error)
	ListApplicationBlocks(
		ctx context.Context,
		filter ApplicationBlockListFilter,
		page ListPage,
	) (items []ApplicationBlock, nextCursor string, err error)
	UpdateApplicationBlock(ctx context.Context, id uuid.UUID, in ApplicationBlockPatch) (ApplicationBlock, error)
	DeleteApplicationBlock(ctx context.Context, id uuid.UUID) error

	// Applications (ADR-0029).
	CreateApplication(ctx context.Context, in ApplicationCreate) (Application, error)
	GetApplication(ctx context.Context, id uuid.UUID) (Application, error)
	// GetApplicationsByIDs bulk-fetches applications by id in a single query,
	// returned keyed by id. Unknown ids are silently omitted from the map
	// (no error). Used by the effective-DICT decoration on workload + VM
	// list responses to avoid an N+1 (ADR-0029 §6).
	GetApplicationsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Application, error)
	GetApplicationByName(ctx context.Context, name string) (Application, error)
	ListApplications(ctx context.Context, filter ApplicationListFilter, page ListPage) (items []Application, nextCursor string, err error)
	UpdateApplication(ctx context.Context, id uuid.UUID, in ApplicationPatch) (Application, error)
	DeleteApplication(ctx context.Context, id uuid.UUID) error
	ListApplicationMembers(
		ctx context.Context,
		id uuid.UUID,
		kind string,
		limit int,
		cursor string,
	) (items []ApplicationMember, nextCursor string, err error)
	// DICTCoverageCounts returns the number of workloads in each
	// effective-DICT source bucket (application | workload | none), feeding
	// the longue_vue_dict_coverage gauge (ADR-0029 §6).
	DICTCoverageCounts(ctx context.Context) (application, workload, none int, err error)
}

// SecurityGroupStore covers provider security groups, their rules, and
// VM attachments (flow-matrix P1).
type SecurityGroupStore interface {
	// UpsertSecurityGroup inserts or updates by (cloud_account_id,
	// provider_sg_id). Returns the stable row UUID. Called on every
	// collector tick; idempotent.
	UpsertSecurityGroup(ctx context.Context, in SecurityGroupRow) (uuid.UUID, error)

	// GetSecurityGroup fetches a security group by stable UUID.
	// Returns ErrNotFound when the row is absent.
	GetSecurityGroup(ctx context.Context, id uuid.UUID) (SecurityGroupRow, error)

	// ReplaceSecurityGroupRules atomically replaces every rule for the
	// given security_group_id. Delete+insert in one transaction; rule
	// sets are small enough that a finer diff is over-engineering.
	ReplaceSecurityGroupRules(ctx context.Context, sgID uuid.UUID, rules []SecurityGroupRuleRow) error

	// ListSecurityGroupsByAccount returns a page of security groups for
	// the given account, filtered and sorted per filter/page.
	ListSecurityGroupsByAccount(
		ctx context.Context,
		accountID uuid.UUID,
		filter SecurityGroupListFilter,
		page ListPage,
	) ([]SecurityGroupRow, string, error)

	// ListSecurityGroupRules returns all rules for a single security
	// group, in stable insertion order.
	ListSecurityGroupRules(ctx context.Context, sgID uuid.UUID) ([]SecurityGroupRuleRow, error)

	// SweepSecurityGroupsByAccount deletes every security group in the
	// account whose provider_sg_id is NOT in seenProviderIDs. Called once
	// per account refresh tick after all VM upserts are done.
	SweepSecurityGroupsByAccount(ctx context.Context, accountID uuid.UUID, seenProviderIDs []string) error

	// GetSecurityGroupByProviderID fetches a security group by
	// (cloud_account_id, provider_sg_id). Returns ErrNotFound on miss.
	GetSecurityGroupByProviderID(ctx context.Context, accountID uuid.UUID, providerSGID string) (SecurityGroupRow, error)

	// UpsertVMSecurityGroupAttachment inserts or refreshes one
	// (account, vm, sg) attachment, stamping reconcile_seen_at. Called on
	// every collector tick; idempotent.
	UpsertVMSecurityGroupAttachment(ctx context.Context, a VMSecurityGroupAttachment) error

	// SweepVMSecurityGroupAttachments deletes attachments for the account
	// that are not in seen. Called once per account refresh tick after all
	// attachment upserts are done; MUST only run after a successful provider
	// list (CLAUDE.md reconcile contract).
	SweepVMSecurityGroupAttachments(ctx context.Context, accountID uuid.UUID, seen []VMSecurityGroupAttachment) error

	// PerimeterSecurityGroupsForCluster resolves the security groups that
	// protect a cluster's node VMs, joining nodes.provider_id to attachment
	// provider_vm_id via the same substring match the VM dedup trusts
	// (ADR-0015).
	PerimeterSecurityGroupsForCluster(ctx context.Context, clusterID uuid.UUID) ([]SecurityGroupRow, error)
}

// NetworkPolicyStore covers Kubernetes NetworkPolicy rows and rules
// (flow-matrix P1, ADR-0038).
type NetworkPolicyStore interface {
	// ListNetworkPoliciesByCluster returns a page of network policies for
	// the given cluster, filtered and sorted per filter/page. The optional
	// namespace filter moved from positional arg into filter.NamespaceID.
	ListNetworkPoliciesByCluster(
		ctx context.Context,
		clusterID uuid.UUID,
		filter NetworkPolicyListFilter,
		page ListPage,
	) ([]NetworkPolicyRow, string, error)

	// GetNetworkPolicy fetches a network policy by stable UUID.
	// Returns ErrNotFound when the row is absent.
	GetNetworkPolicy(ctx context.Context, id uuid.UUID) (NetworkPolicyRow, error)

	// ListNetworkPolicyRules returns all rules for a single network
	// policy, in stable insertion order.
	ListNetworkPolicyRules(ctx context.Context, policyID uuid.UUID) ([]NetworkPolicyRuleRow, error)

	// ListNetworkPoliciesForWorkload returns every NetworkPolicy in the
	// workload's namespace whose pod_selector matchLabels @> workloadLabels.
	// The empty pod_selector (`{}`) and a missing matchLabels key are treated
	// as "select everything" per the Kubernetes semantics. matchExpressions
	// support is deferred to P2.
	ListNetworkPoliciesForWorkload(ctx context.Context, namespaceID uuid.UUID, workloadLabels json.RawMessage) ([]NetworkPolicyRow, error)

	// NetworkPolicyExists returns true when a row matching (clusterID,
	// namespaceID, name) already exists. Used by CreateNetworkPolicy to
	// distinguish 201 (insert) from 200 (update) without re-reading the
	// whole row.
	NetworkPolicyExists(ctx context.Context, clusterID, namespaceID uuid.UUID, name string) (bool, error)

	// UpsertNetworkPolicy upserts the policy and replaces its rules in one
	// transaction. Returns the stable row UUID. This is the canonical write
	// path (ADR-0038).
	UpsertNetworkPolicy(ctx context.Context, np NetworkPolicyRow, rules []NetworkPolicyRuleRow) (uuid.UUID, error)

	// SweepNetworkPoliciesByNamespaceWithCount deletes any policy in the
	// namespace not in the keep-list and returns the count of deleted rows.
	// Used by ReconcileNetworkPolicies (ADR-0038).
	SweepNetworkPoliciesByNamespaceWithCount(ctx context.Context, nsID uuid.UUID, keep []string) (int64, error)
}

// FlowStore covers the flow-matrix curation tables (R2): endpoint groups,
// per-cluster flow references, and drift-detection bookkeeping.
type FlowStore interface {
	ListEndpointGroups(ctx context.Context) ([]EndpointGroup, error)
	GetEndpointGroup(ctx context.Context, id uuid.UUID) (EndpointGroup, error)
	CreateEndpointGroup(ctx context.Context, in EndpointGroupInput, createdBy *uuid.UUID) (EndpointGroup, error)
	UpdateEndpointGroup(ctx context.Context, id uuid.UUID, in EndpointGroupInput) (EndpointGroup, error)
	DeleteEndpointGroup(ctx context.Context, id uuid.UUID) error

	// --- Cluster flow references (flow-matrix R2) -------------------------

	ListFlowReferences(ctx context.Context, clusterID uuid.UUID) ([]FlowReference, error)
	CreateFlowReference(ctx context.Context, clusterID uuid.UUID, in FlowReferenceInput, createdBy *uuid.UUID) (FlowReference, error)
	UpdateFlowReference(ctx context.Context, id uuid.UUID, in FlowReferenceInput) (FlowReference, error)
	DeleteFlowReference(ctx context.Context, id uuid.UUID) error
	ReplaceFlowReferences(ctx context.Context, clusterID uuid.UUID, ins []FlowReferenceInput, createdBy *uuid.UUID) ([]FlowReference, error)

	// RecordFlowDriftSeen marks (cluster, flowKey) as seen now and returns true
	// when it was NOT seen within `within` (caller should then emit an audit
	// event). Atomic upsert so concurrent reads emit at most once per window.
	RecordFlowDriftSeen(ctx context.Context, clusterID uuid.UUID, flowKey string, within time.Duration) (bool, error)
	// ListClustersWithFlowReferences returns cluster ids having >=1 reference row.
	ListClustersWithFlowReferences(ctx context.Context) ([]uuid.UUID, error)
}

// HistoryRow is a single entry from a <kind>_history table, returned by
// ListEntityHistory and surfaced through the GET /v1/{kind}/{id}/history endpoint.
// Diff is a JSON-Patch-shaped slice describing watched-field changes relative
// to the immediately prior row; empty for create/restore/soft_delete rows where
// there is no meaningful prior row.
type HistoryRow struct {
	HistoryID  string     `json:"history_id"`
	EntityID   string     `json:"entity_id"`
	ValidFrom  time.Time  `json:"valid_from"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	ChangeType string     `json:"change_type"`
	ActorID    *string    `json:"actor_id,omitempty"`
	ActorKind  *string    `json:"actor_kind,omitempty"`
	// Diff is the JSON-Patch-shaped array of watched-field changes.
	// Populated for "update" rows; empty slice for other change types.
	Diff any `json:"diff"`
}

// UserIdentityInsert carries the federation tuple persisted on first
// OIDC login. Email is optional but useful for admin display.
type UserIdentityInsert struct {
	Issuer  string
	Subject string
	Email   string
}

// OidcAuthStateInsert is the transient row stashed during an outbound
// OIDC redirect, consumed on the inbound callback.
type OidcAuthStateInsert struct {
	State        string
	CodeVerifier string
	Nonce        string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// UserInsert carries the data the store needs to create a user. Kept
// separate from the API's UserCreate because the store sees the
// password hash, not the plaintext — hashing happens in the handler.
type UserInsert struct {
	Username           string
	PasswordHash       string
	Role               string
	MustChangePassword bool
}

// UserPatch is the merge-patch view for UpdateUser. All fields optional.
// Nil means "don't touch"; non-nil means "set to this value".
//
// Unlock=true clears failed_login_count and locked_at (admin clears a
// brute-force lockout). Has no effect on accounts that are not locked.
type UserPatch struct {
	Role               *string
	MustChangePassword *bool
	Disabled           *bool
	Unlock             *bool
}

// UserWithSecret extends the outward-facing User with the stored
// password hash — never serialised over the wire.
type UserWithSecret struct {
	User
	PasswordHash string
}

// SessionInsert carries the data for a new session row. The id field
// doubles as the cookie value; it's generated by the login handler
// and handed to CreateSession to persist.
type SessionInsert struct {
	ID        string
	UserID    uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
	UserAgent string
	SourceIP  string
}

// APITokenInsert carries the persistable fields for a new minted token.
// The plaintext itself is never persisted �� only `Prefix` (cleartext)
// and `Hash` (argon2id).
type APITokenInsert struct { //nolint:revive // stutter is acceptable here for clarity alongside the APIToken generated type
	ID              uuid.UUID
	Name            string
	Prefix          string
	Hash            string
	Scopes          []string
	CreatedByUserID uuid.UUID
	ExpiresAt       *time.Time
	// BoundCloudAccountID is set when minting a vm-collector PAT
	// (ADR-0015). The store persists it on the api_tokens row;
	// nullable for every other token kind.
	BoundCloudAccountID *uuid.UUID
}

// AuditEventInsert is the payload the middleware hands the store.
// All fields are snapshot values at the moment the request completed —
// audit rows are immutable, so nothing references the caller's live
// identity after insertion.
type AuditEventInsert struct {
	ID            uuid.UUID
	OccurredAt    time.Time
	ActorID       *uuid.UUID
	ActorKind     string // "user" | "token" | "anonymous" | "system"
	ActorUsername string
	ActorRole     string
	Action        string // dot-separated verb, e.g. "user.create", "cluster.update"
	ResourceType  string // kind name, e.g. "cluster", "user", "api_token"
	ResourceID    string // stringified id — UUID for most kinds, session public_id, token id, …
	HTTPMethod    string
	HTTPPath      string
	HTTPStatus    int
	// Source identifies which listener served the request:
	//   "api"       — the public listener serving humans, admins, and trusted-zone collectors
	//   "ingest_gw" — the mTLS-only ingest listener fronted by the DMZ gateway (ADR-0016)
	//   "system"    — synthetic events emitted by longue-vue itself, not driven by a request
	// Empty string is treated as "api" for backwards compatibility with rows
	// inserted before ADR-0016 added this column.
	Source    string
	SourceIP  string
	UserAgent string
	Details   map[string]any // JSONB payload, nil-friendly
}

// Settings holds runtime feature toggles stored in the single-row
// settings table.
type Settings struct {
	EOLEnabled              bool      `json:"eol_enabled"`
	MCPEnabled              bool      `json:"mcp_enabled"`
	TimeTravelEnabled       bool      `json:"time_travel_enabled"`
	TimeTravelRetentionDays int       `json:"time_travel_retention_days"`
	TimeTravelReaperEnabled bool      `json:"time_travel_reaper_enabled"`
	ImageVersionsEnabled    bool      `json:"image_versions_enabled"`
	FlowMatrixEnabled       bool      `json:"flow_matrix_enabled"`
	PoliciesEnabled         bool      `json:"policies_enabled"`
	ClusterStaleAfterDays   int       `json:"cluster_stale_after_days"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// SettingsPatch is the merge-patch for UpdateSettings. Nil fields are
// left unchanged.
type SettingsPatch struct {
	EOLEnabled              *bool `json:"eol_enabled,omitempty"`
	MCPEnabled              *bool `json:"mcp_enabled,omitempty"`
	TimeTravelEnabled       *bool `json:"time_travel_enabled,omitempty"`
	TimeTravelRetentionDays *int  `json:"time_travel_retention_days,omitempty"`
	TimeTravelReaperEnabled *bool `json:"time_travel_reaper_enabled,omitempty"`
	ImageVersionsEnabled    *bool `json:"image_versions_enabled,omitempty"`
	FlowMatrixEnabled       *bool `json:"flow_matrix_enabled,omitempty"`
	PoliciesEnabled         *bool `json:"policies_enabled,omitempty"`
	ClusterStaleAfterDays   *int  `json:"cluster_stale_after_days,omitempty"`
}

// ImageVersionRow is a row from image_versions — one (image_repo, variant) pair
// with its latest discovered tag and enrichment metadata. This is the flat
// DB-row representation; the grouped API response shape is ImageVersion
// (generated by oapi-codegen from the OpenAPI spec).
type ImageVersionRow struct {
	ImageRepo     string          `json:"image_repo"`
	Variant       string          `json:"variant"`
	Registry      string          `json:"registry"`
	LatestTag     *string         `json:"latest_tag,omitempty"`
	Annotation    json.RawMessage `json:"annotation"`
	Source        string          `json:"source"`
	LastCheckedAt time.Time       `json:"last_checked_at"`
	LastError     *string         `json:"last_error,omitempty"`
	LastErrorAt   *time.Time      `json:"last_error_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// ImageVersionUpsert carries the fields for inserting or updating an
// image_versions row. (image_repo, variant) is the primary key.
type ImageVersionUpsert struct {
	ImageRepo     string
	Variant       string
	Registry      string
	LatestTag     *string
	Annotation    json.RawMessage
	Source        string
	LastCheckedAt time.Time
	LastError     *string
	LastErrorAt   *time.Time
}

// ImageVersionListParams collects the optional filters and pagination
// parameters for ListImageVersionsByRepo.
type ImageVersionListParams struct {
	Limit         int
	Cursor        string
	Registry      string
	ImageRepoLike string // substring match, case-insensitive
	// Variant filters repos that have at least one row with this variant;
	// all variants for matching repos are still returned in the RepoView.
	// To filter at the row level instead, drill down via GetImageVersionsByRepo.
	Variant           string
	HasError          *bool
	LastCheckedBefore *time.Time
}

// ImageVersionRepoView groups all variants of a single image_repo together,
// as returned by ListImageVersionsByRepo.
type ImageVersionRepoView struct {
	ImageRepo string            `json:"image_repo"`
	Registry  string            `json:"registry"`
	Variants  []ImageVersionRow `json:"variants"`
}

// ImageOriginResolution is the persisted outcome of one mirror-origin
// resolution attempt, keyed by the pod-ref's (image_repo, variant) — NOT
// the resolved origin repo. Success rows have origin_image_repo and
// via_hostname populated; failure rows have last_error populated and
// origin_image_repo NULL.
type ImageOriginResolution struct {
	MirrorImageRepo string     `json:"mirror_image_repo"`
	Variant         string     `json:"variant"`
	OriginImageRepo *string    `json:"origin_image_repo,omitempty"`
	ViaHostname     *string    `json:"via_hostname,omitempty"`
	ResolvedAt      time.Time  `json:"resolved_at"`
	LastError       *string    `json:"last_error,omitempty"`
	LastErrorAt     *time.Time `json:"last_error_at,omitempty"`
}

// ImageOriginResolutionUpsert is the input shape for
// UpsertImageOriginResolution.
type ImageOriginResolutionUpsert struct {
	MirrorImageRepo string
	Variant         string
	OriginImageRepo *string
	ViaHostname     *string
	ResolvedAt      time.Time
	LastError       *string
	LastErrorAt     *time.Time
}

// StoreListImageOriginMappingsParams carries cursor-based pagination and
// optional filters for the store layer. Limit <= 0 means "default"
// (resolved in the store). This is distinct from the codegen-produced
// ListImageOriginMappingsParams which carries HTTP query-param pointers.
type StoreListImageOriginMappingsParams struct {
	Limit          int
	Cursor         string
	PublicRegistry string // exact match; empty = no filter
	Q              string // case-insensitive substring on image_name
}

// ContainersVersions maps container.name -> ContainerVersionInfo. Keys are
// absent when the image is not enriched (non-parseable tag, registry
// outside allowlist, or not yet processed).
// ContainerVersionInfo is defined by the generated API types (api.gen.go).
type ContainersVersions map[string]ContainerVersionInfo

// AuditEventFilter collects the optional server-side filters. Nil
// fields are ignored; set fields are AND-combined.
type AuditEventFilter struct {
	ActorID      *uuid.UUID
	ResourceType *string
	ResourceID   *string
	Action       *string
	// Source filters by listener — "api", "ingest_gw", or "system"
	// (ADR-0016 §11). Nil = any source.
	Source *string
	Since  *time.Time
	Until  *time.Time
}

// ClusterPolicyRow is one row from the cluster_policies table — a collected
// Kyverno ClusterPolicy (namespace_id=NULL, scope="cluster") or namespaced
// Policy (namespace_id set, scope="namespace"). ADR-0043.
type ClusterPolicyRow struct {
	ID              uuid.UUID       `json:"id"`
	ClusterID       uuid.UUID       `json:"cluster_id"`
	NamespaceID     *uuid.UUID      `json:"namespace_id,omitempty"`
	Name            string          `json:"name"`
	ResourceType    string          `json:"resource_type"`
	Scope           string          `json:"scope"`
	Description     *string         `json:"description,omitempty"`
	Category        *string         `json:"category,omitempty"`
	Severity        *string         `json:"severity,omitempty"`
	Action          *string         `json:"action,omitempty"`
	FailurePolicy   *string         `json:"failure_policy,omitempty"`
	Background      *bool           `json:"background,omitempty"`
	RuleTypes       []string        `json:"rule_types,omitempty"`
	RulesCount      *int            `json:"rules_count,omitempty"`
	TargetResources []string        `json:"target_resources,omitempty"`
	KeyExclusions   []string        `json:"key_exclusions,omitempty"`
	Ready           *bool           `json:"ready,omitempty"`
	Annotations     json.RawMessage `json:"annotations,omitempty"`
	SpecRaw         json.RawMessage `json:"spec_raw"`
	Source          string          `json:"source"`
	ReconcileSeenAt time.Time       `json:"reconcile_seen_at"`
}

// PolicyReportRow is one row from the policy_reports table — a collected
// Kyverno PolicyReport (namespaced) or ClusterPolicyReport (cluster-scoped).
// ADR-0043.
type PolicyReportRow struct {
	ID              uuid.UUID       `json:"id"`
	ClusterID       uuid.UUID       `json:"cluster_id"`
	NamespaceID     *uuid.UUID      `json:"namespace_id,omitempty"`
	Name            string          `json:"name"`
	ScopeKind       *string         `json:"scope_kind,omitempty"`
	ScopeName       *string         `json:"scope_name,omitempty"`
	SummaryPass     int             `json:"summary_pass"`
	SummaryFail     int             `json:"summary_fail"`
	SummaryWarn     int             `json:"summary_warn"`
	SummaryError    int             `json:"summary_error"`
	SummarySkip     int             `json:"summary_skip"`
	ResultsRaw      json.RawMessage `json:"results_raw,omitempty"`
	Source          string          `json:"source"`
	ReconcileSeenAt time.Time       `json:"reconcile_seen_at"`
}

// ClusterPolicyListFilter — nil fields are ignored; set fields AND-combine.
type ClusterPolicyListFilter struct {
	ClusterID     *uuid.UUID
	NamespaceID   *uuid.UUID
	Name          *string
	ResourceType  *string
	Action        *string
	Severity      *string
	FailurePolicy *string
	Category      *string
}

// PolicyReportListFilter — nil fields are ignored; set fields AND-combine.
type PolicyReportListFilter struct {
	ClusterID   *uuid.UUID
	NamespaceID *uuid.UUID
	Name        *string
	ScopeKind   *string
	ScopeName   *string
}

// ClusterPolicyCreate is the request body for POST /v1/cluster-policies.
// Only the fields required for creation are exposed; server-generated
// fields (id, reconcile_seen_at, source) are set by the handler.
type ClusterPolicyCreate struct {
	ClusterID       uuid.UUID       `json:"cluster_id"`
	NamespaceID     *uuid.UUID      `json:"namespace_id,omitempty"`
	Name            string          `json:"name"`
	ResourceType    string          `json:"resource_type"`
	Scope           string          `json:"scope"`
	Description     *string         `json:"description,omitempty"`
	Category        *string         `json:"category,omitempty"`
	Severity        *string         `json:"severity,omitempty"`
	Action          *string         `json:"action,omitempty"`
	FailurePolicy   *string         `json:"failure_policy,omitempty"`
	Background      *bool           `json:"background,omitempty"`
	RuleTypes       []string        `json:"rule_types,omitempty"`
	RulesCount      *int            `json:"rules_count,omitempty"`
	TargetResources []string        `json:"target_resources,omitempty"`
	KeyExclusions   []string        `json:"key_exclusions,omitempty"`
	Ready           *bool           `json:"ready,omitempty"`
	Annotations     json.RawMessage `json:"annotations,omitempty"`
	SpecRaw         json.RawMessage `json:"spec_raw"`
}

// PolicyReportCreate is the request body for POST /v1/policy-reports.
// Only the fields required for creation are exposed; server-generated
// fields (id, reconcile_seen_at, source) are set by the handler.
type PolicyReportCreate struct {
	ClusterID    uuid.UUID       `json:"cluster_id"`
	NamespaceID  *uuid.UUID      `json:"namespace_id,omitempty"`
	Name         string          `json:"name"`
	ScopeKind    *string         `json:"scope_kind,omitempty"`
	ScopeName    *string         `json:"scope_name,omitempty"`
	SummaryPass  int             `json:"summary_pass"`
	SummaryFail  int             `json:"summary_fail"`
	SummaryWarn  int             `json:"summary_warn"`
	SummaryError int             `json:"summary_error"`
	SummarySkip  int             `json:"summary_skip"`
	ResultsRaw   json.RawMessage `json:"results_raw,omitempty"`
}

// KyvernoStore covers Kyverno ClusterPolicy and PolicyReport rows
// (ADR-0043).
type KyvernoStore interface {
	// --- Cluster policies -------------------------------------------------

	// GetClusterPolicy fetches a cluster policy by stable UUID.
	// Returns ErrNotFound when the row is absent.
	GetClusterPolicy(ctx context.Context, id uuid.UUID) (ClusterPolicyRow, error)

	// ListClusterPolicies returns a paged list of cluster policies matching
	// filter, sorted per page.Sort/page.Order. Satisfies ADR-0042.
	ListClusterPolicies(
		ctx context.Context,
		filter ClusterPolicyListFilter,
		page ListPage,
	) ([]ClusterPolicyRow, string, error)

	// UpsertClusterPolicy upserts by (cluster_id, namespace_id, name).
	// Returns the stable row UUID. The canonical write path (ADR-0043).
	UpsertClusterPolicy(ctx context.Context, cp ClusterPolicyRow) (uuid.UUID, error)

	// DeleteClusterScopedPoliciesNotIn removes every cluster-scoped policy
	// (namespace_id IS NULL) for the given cluster whose ID is NOT in
	// keepIDs. Returns the count of deleted rows.
	DeleteClusterScopedPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)

	// DeleteClusterPoliciesByNamespace removes every namespaced policy in
	// the given cluster+namespace whose ID is NOT in keepIDs. Only collector-originated
	// rows are affected. Unknown-namespace policies survive because they are
	// never swept. Returns the count of deleted rows.
	DeleteClusterPoliciesByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)

	// DeleteClusterPolicy removes a single API-managed cluster policy by UUID.
	// Returns ErrNotFound if the row does not exist or has source='collector'.
	DeleteClusterPolicy(ctx context.Context, id uuid.UUID) error

	// --- Policy reports ---------------------------------------------------

	// GetPolicyReport fetches a policy report by stable UUID.
	// Returns ErrNotFound when the row is absent.
	GetPolicyReport(ctx context.Context, id uuid.UUID) (PolicyReportRow, error)

	// ListPolicyReports returns a paged list of policy reports matching
	// filter, sorted per page.Sort/page.Order. Satisfies ADR-0042.
	ListPolicyReports(
		ctx context.Context,
		filter PolicyReportListFilter,
		page ListPage,
	) ([]PolicyReportRow, string, error)

	// UpsertPolicyReport upserts by (cluster_id, namespace_id, name).
	// Returns the stable row UUID. The canonical write path (ADR-0043).
	UpsertPolicyReport(ctx context.Context, pr PolicyReportRow) (uuid.UUID, error)

	// DeleteClusterScopedPolicyReportsNotIn removes every cluster-scoped report
	// (namespace_id IS NULL) for the given cluster whose ID is NOT in keepIDs.
	// Returns the count of deleted rows.
	DeleteClusterScopedPolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)

	// DeletePolicyReportsByNamespace removes every namespaced report in
	// the given cluster+namespace whose ID is NOT in keepIDs. Only collector-originated
	// rows are affected. Returns the count of deleted rows.
	DeletePolicyReportsByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)

	// DeletePolicyReport removes a single API-managed policy report by UUID.
	// Returns ErrNotFound if the row does not exist or has source='collector'.
	DeletePolicyReport(ctx context.Context, id uuid.UUID) error
}
