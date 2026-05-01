package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/secrets"
)

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
)

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
}

// WorkloadListFilter mirrors PodListFilter for ListWorkloads.
type WorkloadListFilter struct {
	NamespaceID    *uuid.UUID
	Kind           *WorkloadKind
	ImageSubstring *string
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

// Store is the persistence contract consumed by the REST handlers.
// Implementations must be safe for concurrent use by multiple goroutines.
type Store interface {
	// Ping verifies that the underlying database is reachable.
	Ping(ctx context.Context) error

	// EnsureCluster inserts a cluster if no row with the same name exists, or
	// returns the existing row unchanged when one does. The created flag is
	// true when a new row was inserted, false when an existing row was
	// returned. The request body is ignored on hit — callers wanting to
	// update fields on an existing cluster must follow up with UpdateCluster.
	//
	// EnsureCluster never returns ErrConflict; concurrent inserts of the same
	// name are serialised at the database via INSERT ... ON CONFLICT DO
	// NOTHING, falling back to a SELECT for the losing writer.
	EnsureCluster(ctx context.Context, in ClusterCreate) (cluster Cluster, created bool, err error)

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

	// CountClusterChildren counts child resources that will be cascade-deleted
	// when the given cluster is removed. Returns ErrNotFound if the cluster
	// does not exist. Used to build the pre-deletion audit snapshot (ADR-0010).
	CountClusterChildren(ctx context.Context, clusterID uuid.UUID) (CascadeCounts, error)

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

	// ListPods returns up to limit pods after the given opaque cursor,
	// optionally filtered. See PodListFilter for the accepted predicates.
	ListPods(ctx context.Context, filter PodListFilter, limit int, cursor string) (items []Pod, nextCursor string, err error)

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
	// cursor, optionally filtered. See WorkloadListFilter for the accepted
	// predicates.
	ListWorkloads(ctx context.Context, filter WorkloadListFilter, limit int, cursor string) (items []Workload, nextCursor string, err error)

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

	// CreateIngress inserts a new ingress.
	CreateIngress(ctx context.Context, in IngressCreate) (Ingress, error)

	// GetIngress fetches an ingress by id.
	GetIngress(ctx context.Context, id uuid.UUID) (Ingress, error)

	// ListIngresses returns up to limit ingresses, optionally filtered by namespace.
	ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) (items []Ingress, nextCursor string, err error)

	// UpdateIngress applies merge-patch.
	UpdateIngress(ctx context.Context, id uuid.UUID, in IngressUpdate) (Ingress, error)

	// DeleteIngress removes by id.
	DeleteIngress(ctx context.Context, id uuid.UUID) error

	// UpsertIngress mirrors UpsertService; keyed on (namespace_id, name).
	UpsertIngress(ctx context.Context, in IngressCreate) (Ingress, error)

	// DeleteIngressesNotIn mirrors DeleteServicesNotIn.
	DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)

	// CreatePersistentVolume inserts a new cluster-scoped PV. Returns
	// ErrNotFound when the parent cluster does not exist; ErrConflict when
	// (cluster_id, name) already has a PV.
	CreatePersistentVolume(ctx context.Context, in PersistentVolumeCreate) (PersistentVolume, error)

	// GetPersistentVolume fetches a PV by id.
	GetPersistentVolume(ctx context.Context, id uuid.UUID) (PersistentVolume, error)

	// ListPersistentVolumes returns up to limit PVs, optionally filtered by cluster.
	ListPersistentVolumes(
		ctx context.Context, clusterID *uuid.UUID, limit int, cursor string,
	) (items []PersistentVolume, nextCursor string, err error)

	// UpdatePersistentVolume applies merge-patch.
	UpdatePersistentVolume(ctx context.Context, id uuid.UUID, in PersistentVolumeUpdate) (PersistentVolume, error)

	// DeletePersistentVolume removes by id.
	DeletePersistentVolume(ctx context.Context, id uuid.UUID) error

	// UpsertPersistentVolume mirrors UpsertNode; keyed on (cluster_id, name).
	UpsertPersistentVolume(ctx context.Context, in PersistentVolumeCreate) (PersistentVolume, error)

	// DeletePersistentVolumesNotIn removes cluster-scoped PVs whose name is
	// not in keepNames. An empty keep slice clears every PV in that cluster.
	DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)

	// CreatePersistentVolumeClaim inserts a new PVC. Returns ErrNotFound
	// when the parent namespace or the bound volume does not exist;
	// ErrConflict when (namespace_id, name) already has a PVC.
	CreatePersistentVolumeClaim(ctx context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, error)

	// GetPersistentVolumeClaim fetches a PVC by id.
	GetPersistentVolumeClaim(ctx context.Context, id uuid.UUID) (PersistentVolumeClaim, error)

	// ListPersistentVolumeClaims returns up to limit PVCs, optionally filtered by namespace.
	ListPersistentVolumeClaims(
		ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string,
	) (items []PersistentVolumeClaim, nextCursor string, err error)

	// UpdatePersistentVolumeClaim applies merge-patch.
	UpdatePersistentVolumeClaim(ctx context.Context, id uuid.UUID, in PersistentVolumeClaimUpdate) (PersistentVolumeClaim, error)

	// DeletePersistentVolumeClaim removes by id.
	DeletePersistentVolumeClaim(ctx context.Context, id uuid.UUID) error

	// UpsertPersistentVolumeClaim mirrors UpsertPod; keyed on (namespace_id, name).
	UpsertPersistentVolumeClaim(ctx context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, error)

	// DeletePersistentVolumeClaimsNotIn mirrors DeletePodsNotIn.
	DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)

	// --- Auth substrate (ADR-0007) ---------------------------------------
	//
	// The auth package also defines a narrower `auth.Store` interface with
	// just the lookup methods the middleware needs. The PG store satisfies
	// both; see `internal/auth/middleware.go` for the contract.

	// CountActiveAdmins returns the number of `admin`-role users without a
	// `disabled_at` timestamp. Used by the first-install bootstrap check.
	CountActiveAdmins(ctx context.Context) (int, error)

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

	// ListUsers returns a page of users (admin view).
	ListUsers(ctx context.Context, limit int, cursor string) (items []User, nextCursor string, err error)

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
	ListSessions(ctx context.Context, limit int, cursor string) (items []Session, nextCursor string, err error)

	// CreateAPIToken inserts a new token row. `hash` is argon2id of the
	// full plaintext; `prefix` is the first 8 chars of the plaintext
	// stored in the clear for O(1) lookup.
	CreateAPIToken(ctx context.Context, in APITokenInsert) (ApiToken, error)

	// GetActiveTokenByPrefix, TouchToken — auth.Store lookup path.
	GetActiveTokenByPrefix(ctx context.Context, prefix string) (auth.APIToken, error)
	TouchToken(ctx context.Context, id uuid.UUID, now time.Time) error

	// ListAPITokens (admin view, metadata only — plaintext is never in
	// responses except at creation).
	ListAPITokens(ctx context.Context, limit int, cursor string) (items []ApiToken, nextCursor string, err error)

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

	// GetSettings returns the current runtime settings (single-row table).
	GetSettings(ctx context.Context) (Settings, error)

	// UpdateSettings applies the merge-patch on the settings row.
	UpdateSettings(ctx context.Context, in SettingsPatch) (Settings, error)

	// InsertAuditEvent appends one row to audit_events. Called from the
	// audit middleware after the wrapped handler has produced a status.
	// Never returns ErrConflict — id collisions are caller bugs.
	InsertAuditEvent(ctx context.Context, in AuditEventInsert) error

	// ListAuditEvents returns the newest events first, paged by opaque
	// cursor. filter fields are AND-combined; nil fields are ignored.
	ListAuditEvents(ctx context.Context, filter AuditEventFilter, limit int, cursor string) (items []AuditEvent, nextCursor string, err error)

	// --- Cloud accounts (ADR-0015) -------------------------------------

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

	// ListCloudAccounts returns up to limit accounts after the given opaque cursor.
	ListCloudAccounts(ctx context.Context, limit int, cursor string) (items []CloudAccount, nextCursor string, err error)

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

	// --- Virtual machines (ADR-0015) -----------------------------------

	// UpsertVirtualMachine inserts a new VM or updates the existing row by
	// (cloud_account_id, provider_vm_id). Server-side dedup against
	// nodes.provider_id: returns ErrConflict if the provider_vm_id already
	// appears in any node's provider_id (the VM is already inventoried as
	// a Kubernetes node).
	UpsertVirtualMachine(ctx context.Context, in VirtualMachineUpsert) (VirtualMachine, error)

	// GetVirtualMachine fetches by id. ErrNotFound when absent.
	GetVirtualMachine(ctx context.Context, id uuid.UUID) (VirtualMachine, error)

	// ListVirtualMachines returns paged VMs filtered by VirtualMachineListFilter.
	// terminated rows are excluded unless filter.IncludeTerminated.
	ListVirtualMachines(
		ctx context.Context,
		filter VirtualMachineListFilter,
		limit int,
		cursor string,
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
type UserPatch struct {
	Role               *string
	MustChangePassword *bool
	Disabled           *bool
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
	EOLEnabled bool      `json:"eol_enabled"`
	MCPEnabled bool      `json:"mcp_enabled"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// SettingsPatch is the merge-patch for UpdateSettings. Nil fields are
// left unchanged.
type SettingsPatch struct {
	EOLEnabled *bool `json:"eol_enabled,omitempty"`
	MCPEnabled *bool `json:"mcp_enabled,omitempty"`
}

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
