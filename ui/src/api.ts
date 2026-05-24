// Thin fetch wrapper around the longue-vue REST API. Hand-written for v0 — a
// generated OpenAPI client can replace this when the surface grows.
//
// Auth per ADR-0007: humans use server-side sessions via an HttpOnly
// SameSite=Strict cookie that the browser sets automatically after
// POST /v1/auth/login. Machines use `Authorization: Bearer <token>`
// with tokens minted in the admin panel. The browser SPA lives in the
// session world; it never touches `Authorization` headers.

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.headers || {}),
    },
  });
  if (!res.ok) {
    // RFC 7807 problem+json bodies carry a useful 'detail'. Fall back to status text.
    let detail = res.statusText;
    try {
      const body = await res.json();
      if (body && typeof body.detail === 'string') {
        detail = body.detail;
      } else if (body && typeof body.title === 'string') {
        detail = body.title;
      }
    } catch {
      // Non-JSON body — keep statusText.
    }
    throw new ApiError(res.status, detail);
  }
  // 204 is used by login / logout / delete; don't try to parse a body.
  if (res.status === 204) {
    return undefined as unknown as T;
  }
  return res.json() as Promise<T>;
}

function query(params?: Record<string, string | number | undefined | null>): string {
  if (!params) return '';
  const entries = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return entries.length ? `?${entries.join('&')}` : '';
}

// --- Auth ---------------------------------------------------------------

export type Role = 'admin' | 'editor' | 'auditor' | 'viewer';

export interface Me {
  kind: 'user' | 'token';
  id?: string;
  username?: string;
  token_name?: string;
  role?: Role;
  scopes: string[];
  must_change_password?: boolean;
}

export function login(username: string, password: string): Promise<void> {
  return request<void>('/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
}

export function logout(): Promise<void> {
  return request<void>('/v1/auth/logout', { method: 'POST' });
}

export function getMe(): Promise<Me> {
  return request<Me>('/v1/auth/me');
}

export interface AuthConfig {
  oidc: {
    enabled: boolean;
    label?: string;
  };
}

export function getAuthConfig(): Promise<AuthConfig> {
  return request<AuthConfig>('/v1/auth/config');
}

export function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  return request<void>('/v1/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  });
}

// --- Admin --------------------------------------------------------------

export interface User {
  id: string;
  username: string;
  role: Role;
  must_change_password?: boolean;
  created_at: string;
  updated_at: string;
  last_login_at?: string | null;
  disabled_at?: string | null;
  failed_login_count?: number;
  locked_at?: string | null;
}

export interface UserCreate {
  username: string;
  password: string;
  role: Role;
  must_change_password?: boolean;
}

export interface UserUpdate {
  role?: Role;
  password?: string;
  must_change_password?: boolean;
  disabled?: boolean;
  unlock?: boolean;
}

export function listUsers() {
  return request<PagedResponse<User>>('/v1/admin/users?limit=200');
}
export function createUser(in_: UserCreate) {
  return request<User>('/v1/admin/users', { method: 'POST', body: JSON.stringify(in_) });
}
export function updateUser(id: string, patch: UserUpdate) {
  return request<User>(`/v1/admin/users/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
    headers: { 'Content-Type': 'application/merge-patch+json' },
  });
}
export function deleteUser(id: string) {
  return request<void>(`/v1/admin/users/${id}`, { method: 'DELETE' });
}

// Scopes that an admin may grant to a machine token. The backend
// rejects `admin` and `audit` for tokens (admin-only endpoints are
// session-only for accountability).
export type TokenScope = 'read' | 'write' | 'delete';

export interface ApiToken {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  created_by_user_id: string;
  created_at: string;
  last_used_at?: string | null;
  expires_at?: string | null;
  revoked_at?: string | null;
}

// Returned only by createApiToken — carries the plaintext token that
// must be shown to the user exactly once.
export interface ApiTokenMint extends ApiToken {
  token: string;
}

export interface ApiTokenCreate {
  name: string;
  scopes: TokenScope[];
  expires_at?: string | null;
}

export function listApiTokens() {
  return request<PagedResponse<ApiToken>>('/v1/admin/tokens?limit=200');
}
export function createApiToken(in_: ApiTokenCreate) {
  return request<ApiTokenMint>('/v1/admin/tokens', { method: 'POST', body: JSON.stringify(in_) });
}
export function revokeApiToken(id: string) {
  return request<void>(`/v1/admin/tokens/${id}`, { method: 'DELETE' });
}

// VM-collector PATs are bound to a specific cloud_account at issue time
// (ADR-0015 §5). The endpoint is hand-written and lives outside the
// OpenAPI-generated `/v1/admin/tokens` route.
export interface CloudAccountTokenMint {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  bound_cloud_account_id: string;
  created_by_user_id: string;
  created_at: string;
  expires_at?: string | null;
  token: string;
}
export function createCloudAccountToken(
  cloudAccountId: string,
  in_: { name: string; expires_at?: string | null },
) {
  return request<CloudAccountTokenMint>(
    `/v1/admin/cloud-accounts/${cloudAccountId}/tokens`,
    { method: 'POST', body: JSON.stringify(in_) },
  );
}

export interface Session {
  id: string; // server returns a masked "<first8>…" value, never the full cookie value
  user_id: string;
  username?: string;
  created_at: string;
  last_used_at: string;
  expires_at: string;
  user_agent?: string | null;
  source_ip?: string | null;
}

export function listSessions() {
  return request<PagedResponse<Session>>('/v1/admin/sessions?limit=200');
}
export function revokeSession(id: string) {
  return request<void>(`/v1/admin/sessions/${id}`, { method: 'DELETE' });
}

// Audit events -----------------------------------------------------------

export type AuditActorKind = 'user' | 'token' | 'anonymous' | 'system';

export interface AuditEvent {
  id: string;
  occurred_at: string;
  actor_id?: string | null;
  actor_kind: AuditActorKind;
  actor_username?: string | null;
  actor_role?: string | null;
  action: string;
  resource_type?: string | null;
  resource_id?: string | null;
  http_method: string;
  http_path: string;
  http_status: number;
  source_ip?: string | null;
  user_agent?: string | null;
  details?: Record<string, unknown> | null;
}

export interface AuditFilter {
  actorId?: string;
  resourceType?: string;
  resourceId?: string;
  action?: string;
  since?: string;
  until?: string;
  cursor?: string;
}

export function listAuditEvents(filter: AuditFilter = {}) {
  const q = new URLSearchParams();
  q.set('limit', '100');
  if (filter.actorId) q.set('actor_id', filter.actorId);
  if (filter.resourceType) q.set('resource_type', filter.resourceType);
  if (filter.resourceId) q.set('resource_id', filter.resourceId);
  if (filter.action) q.set('action', filter.action);
  if (filter.since) q.set('since', filter.since);
  if (filter.until) q.set('until', filter.until);
  if (filter.cursor) q.set('cursor', filter.cursor);
  return request<PagedResponse<AuditEvent>>(`/v1/admin/audit?${q.toString()}`);
}

// Settings -----------------------------------------------------------------

export interface Settings {
  eol_enabled: boolean;
  mcp_enabled: boolean;
  image_versions_enabled: boolean;
  updated_at: string;
}

export interface SettingsPatch {
  eol_enabled?: boolean;
  mcp_enabled?: boolean;
  image_versions_enabled?: boolean;
}

export function getSettings() {
  return request<Settings>('/v1/admin/settings');
}

export function updateSettings(patch: SettingsPatch) {
  return request<Settings>('/v1/admin/settings', {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}

// Shared shapes ------------------------------------------------------------

export interface PagedResponse<T> {
  items: T[];
  next_cursor?: string | null;
}

export type Layer =
  | 'ecosystem'
  | 'business'
  | 'applicative'
  | 'administration'
  | 'infrastructure_logical'
  | 'infrastructure_physical';

export type WorkloadKind = 'Deployment' | 'StatefulSet' | 'DaemonSet';

export interface Container {
  name: string;
  image: string;
  image_id?: string;
  init?: boolean;
}

export interface Cluster {
  id: string;
  name: string;
  display_name?: string | null;
  environment?: string | null;
  provider?: string | null;
  region?: string | null;
  kubernetes_version?: string | null;
  api_endpoint?: string | null;
  labels?: Record<string, string> | null;
  // Curated metadata — not derived from Kubernetes, set by operators.
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

// ClusterPatch is the merge-patch shape the UI posts on inline edits.
// Omitted keys leave the field unchanged; null clears it.
export type ClusterPatch = Partial<Pick<
  Cluster,
  | 'display_name'
  | 'environment'
  | 'provider'
  | 'region'
  | 'api_endpoint'
  | 'owner'
  | 'criticality'
  | 'notes'
  | 'runbook_url'
  | 'annotations'
  | 'labels'
>>;

export function updateCluster(id: string, patch: ClusterPatch) {
  return request<Cluster>(`/v1/clusters/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
}

export interface NodeCondition {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  last_transition_time?: string;
}

export interface NodeTaint {
  key: string;
  value?: string;
  effect: string;
}

export interface Node {
  id: string;
  cluster_id: string;
  name: string;
  display_name?: string | null;
  role?: string | null;
  kubelet_version?: string | null;
  kube_proxy_version?: string | null;
  container_runtime_version?: string | null;
  os_image?: string | null;
  operating_system?: string | null;
  kernel_version?: string | null;
  architecture?: string | null;
  internal_ip?: string | null;
  external_ip?: string | null;
  pod_cidr?: string | null;
  provider_id?: string | null;
  instance_type?: string | null;
  zone?: string | null;
  capacity_cpu?: string | null;
  capacity_memory?: string | null;
  capacity_pods?: string | null;
  capacity_ephemeral_storage?: string | null;
  allocatable_cpu?: string | null;
  allocatable_memory?: string | null;
  allocatable_pods?: string | null;
  allocatable_ephemeral_storage?: string | null;
  conditions?: NodeCondition[] | null;
  taints?: NodeTaint[] | null;
  unschedulable?: boolean | null;
  ready?: boolean | null;
  labels?: Record<string, string> | null;
  // Curated metadata — operator-owned, never touched by the collector.
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
  // Bare-metal complement to instance_type; set by operators.
  hardware_model?: string | null;
  layer: Layer;
  // Denormalized clusters.name — null on orphans. See ADR-0027.
  cluster_name?: string | null;
  created_at: string;
  updated_at: string;
}

export type NodePatch = Partial<Pick<
  Node,
  | 'display_name'
  | 'owner'
  | 'criticality'
  | 'notes'
  | 'runbook_url'
  | 'annotations'
  | 'hardware_model'
>>;

export function updateNode(id: string, patch: NodePatch) {
  return request<Node>(`/v1/nodes/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
}

export interface Namespace {
  id: string;
  cluster_id: string;
  name: string;
  display_name?: string | null;
  phase?: string | null;
  labels?: Record<string, string> | null;
  // Curated metadata — operator-owned, never touched by the collector.
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
  // Denormalized parent name (ADR-0027). Null on orphan.
  cluster_name?: string | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export type NamespacePatch = Partial<Pick<
  Namespace,
  | 'display_name'
  | 'phase'
  | 'labels'
  | 'owner'
  | 'criticality'
  | 'notes'
  | 'runbook_url'
  | 'annotations'
>>;

export function updateNamespace(id: string, patch: NamespacePatch) {
  return request<Namespace>(`/v1/namespaces/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
}

// EffectiveDICT is the read-only inherited DICT classification shipped on
// workload + VM responses (ADR-0029 §6). The four axes are EBIOS-RM security
// needs; `source` records where the classification was inherited from. In
// practice today `source` is only ever "application" or "none" because
// workloads/namespaces never got their own DICT columns — DICT lives on
// applications. The field is always read-only; classification is edited on
// the linked application's detail page.
export interface EffectiveDICT {
  disponibilite?: number | null;
  integrite?: number | null;
  confidentialite?: number | null;
  tracabilite?: number | null;
  notes?: string | null;
  source: 'application' | 'workload' | 'namespace' | 'none';
}

export interface Workload {
  id: string;
  namespace_id: string;
  kind: WorkloadKind;
  name: string;
  replicas?: number | null;
  ready_replicas?: number | null;
  containers?: Container[] | null;
  containers_versions?: Record<string, ContainerVersionInfo> | null;
  labels?: Record<string, string> | null;
  // Denormalized parent names (ADR-0027). Null on orphan.
  namespace_name?: string | null;
  cluster_id?: string | null;
  cluster_name?: string | null;
  // ADR-0029 linkage. Null when unlinked.
  application_id?: string | null;
  application_name?: string | null;
  // ADR-0029 §6 read-only inherited classification. Absent if the server
  // omits it; otherwise carries source="none" when nothing is classified.
  effective_dict?: EffectiveDICT | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

// WorkloadPatch is the merge-patch shape the UI posts. `namespace_id`,
// `kind`, and `name` are immutable post-create. For ADR-0029 the
// application linkage may be set by id OR by name (server resolves the
// name; sending null on `application_id` unlinks).
export interface WorkloadPatch {
  application_id?: string | null;
  application_name?: string | null;
  labels?: Record<string, string> | null;
}

export function updateWorkload(id: string, patch: WorkloadPatch) {
  return request<Workload>(`/v1/workloads/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/merge-patch+json' },
    body: JSON.stringify(patch),
  });
}

export interface Pod {
  id: string;
  namespace_id: string;
  name: string;
  phase?: string | null;
  node_name?: string | null;
  pod_ip?: string | null;
  workload_id?: string | null;
  containers?: Container[] | null;
  containers_versions?: Record<string, ContainerVersionInfo> | null;
  labels?: Record<string, string> | null;
  // Denormalized parent names (ADR-0027). Null on orphan.
  namespace_name?: string | null;
  cluster_id?: string | null;
  cluster_name?: string | null;
  workload_name?: string | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export type ServiceType = 'ClusterIP' | 'NodePort' | 'LoadBalancer' | 'ExternalName';

export interface ServicePort {
  name?: string;
  port: number;
  protocol?: string;
  target_port?: string;
  node_port?: number;
}

export interface LoadBalancerPort {
  port: number;
  protocol?: string;
  error?: string;
}

export interface LoadBalancerAddress {
  ip?: string;
  hostname?: string;
  ports?: LoadBalancerPort[];
}

export interface Service {
  id: string;
  namespace_id: string;
  name: string;
  type?: ServiceType | null;
  cluster_ip?: string | null;
  selector?: Record<string, string> | null;
  ports?: ServicePort[] | null;
  load_balancer?: LoadBalancerAddress[] | null;
  labels?: Record<string, string> | null;
  // Denormalized parent names (ADR-0027). Null on orphan.
  namespace_name?: string | null;
  cluster_id?: string | null;
  cluster_name?: string | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export interface IngressRule {
  host?: string;
  paths?: Array<{
    path?: string;
    path_type?: string;
    backend?: { service_name?: string; service_port_number?: number; service_port_name?: string };
  }>;
}

export interface IngressTLS {
  hosts?: string[];
  secret_name?: string;
}

export interface Ingress {
  id: string;
  namespace_id: string;
  name: string;
  ingress_class_name?: string | null;
  rules?: IngressRule[] | null;
  tls?: IngressTLS[] | null;
  load_balancer?: LoadBalancerAddress[] | null;
  labels?: Record<string, string> | null;
  // Denormalized parent names (ADR-0027). Null on orphan.
  namespace_name?: string | null;
  cluster_id?: string | null;
  cluster_name?: string | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export interface PersistentVolume {
  id: string;
  cluster_id: string;
  name: string;
  capacity?: string | null;
  access_modes?: string[] | null;
  reclaim_policy?: string | null;
  phase?: string | null;
  storage_class_name?: string | null;
  csi_driver?: string | null;
  volume_handle?: string | null;
  claim_ref_namespace?: string | null;
  claim_ref_name?: string | null;
  labels?: Record<string, string> | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export interface PersistentVolumeClaim {
  id: string;
  namespace_id: string;
  name: string;
  phase?: string | null;
  storage_class_name?: string | null;
  volume_name?: string | null;
  bound_volume_id?: string | null;
  access_modes?: string[] | null;
  requested_storage?: string | null;
  labels?: Record<string, string> | null;
  // Denormalized parent names (ADR-0027). Null on orphan.
  namespace_name?: string | null;
  cluster_id?: string | null;
  cluster_name?: string | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

// Endpoints ---------------------------------------------------------------

export function listClusters(filter?: { cursor?: string; limit?: number }) {
  return request<PagedResponse<Cluster>>('/v1/clusters' + query({ limit: 200, ...filter }));
}
export function getCluster(id: string) {
  return request<Cluster>(`/v1/clusters/${id}`);
}
export function deleteCluster(id: string) {
  return request<void>(`/v1/clusters/${id}`, { method: 'DELETE' });
}

export function listNodes(filter?: { cluster_id?: string }) {
  return request<PagedResponse<Node>>('/v1/nodes' + query({ limit: 200, ...filter }));
}
export function getNode(id: string) {
  return request<Node>(`/v1/nodes/${id}`);
}

export function listNamespaces(filter?: { cluster_id?: string; cursor?: string; limit?: number }) {
  return request<PagedResponse<Namespace>>('/v1/namespaces' + query({ limit: 200, ...filter }));
}
export function getNamespace(id: string) {
  return request<Namespace>(`/v1/namespaces/${id}`);
}

export function listWorkloads(filter?: {
  namespace_id?: string;
  kind?: WorkloadKind;
  image?: string;
  cursor?: string;
  limit?: number;
}) {
  return request<PagedResponse<Workload>>('/v1/workloads' + query({ limit: 200, ...filter }));
}
export function getWorkload(id: string) {
  return request<Workload>(`/v1/workloads/${id}`);
}

export function listPods(filter?: {
  namespace_id?: string;
  node_name?: string;
  workload_id?: string;
  image?: string;
}) {
  return request<PagedResponse<Pod>>('/v1/pods' + query({ limit: 200, ...filter }));
}
export function getPod(id: string) {
  return request<Pod>(`/v1/pods/${id}`);
}

export function listServices(filter?: { namespace_id?: string }) {
  return request<PagedResponse<Service>>('/v1/services' + query({ limit: 200, ...filter }));
}
export function getService(id: string) {
  return request<Service>(`/v1/services/${id}`);
}

export function listIngresses(filter?: { namespace_id?: string }) {
  return request<PagedResponse<Ingress>>('/v1/ingresses' + query({ limit: 200, ...filter }));
}
export function getIngress(id: string) {
  return request<Ingress>(`/v1/ingresses/${id}`);
}

export function listPersistentVolumes(filter?: { cluster_id?: string }) {
  return request<PagedResponse<PersistentVolume>>(
    '/v1/persistentvolumes' + query({ limit: 200, ...filter }),
  );
}
export function getPersistentVolume(id: string) {
  return request<PersistentVolume>(`/v1/persistentvolumes/${id}`);
}

export function listPersistentVolumeClaims(filter?: { namespace_id?: string }) {
  return request<PagedResponse<PersistentVolumeClaim>>(
    '/v1/persistentvolumeclaims' + query({ limit: 200, ...filter }),
  );
}
export function getPersistentVolumeClaim(id: string) {
  return request<PersistentVolumeClaim>(`/v1/persistentvolumeclaims/${id}`);
}

export interface Health {
  status: string;
  version?: string;
}

export function getHealthz(): Promise<Health> {
  return request<Health>('/healthz');
}

// --- Impact analysis -----------------------------------------------------

export type ImpactEntityType =
  | 'cluster'
  | 'node'
  | 'namespace'
  | 'pod'
  | 'workload'
  | 'service'
  | 'ingress'
  | 'persistentvolume'
  | 'persistentvolumeclaim';

export interface ImpactGraphNode {
  id: string;
  type: ImpactEntityType;
  name: string;
  status?: string;
  kind?: string;
}

export interface ImpactGraphEdge {
  from: string;
  to: string;
  relation: 'contains' | 'owns' | 'hosts' | 'binds';
}

export interface ImpactGraph {
  root: ImpactGraphNode;
  nodes: ImpactGraphNode[];
  edges: ImpactGraphEdge[];
}

export function getImpactGraph(
  entityType: ImpactEntityType,
  id: string,
  depth: number = 2,
): Promise<ImpactGraph> {
  return request<ImpactGraph>(
    `/v1/impact/${entityType}/${id}` + query({ depth }),
  );
}

// --- Cloud accounts (ADR-0015) ------------------------------------------

// CloudAccountStatus — must match the CHECK constraint on cloud_accounts.
export type CloudAccountStatus =
  | 'pending_credentials'
  | 'active'
  | 'error'
  | 'disabled';

export interface CloudAccount {
  id: string;
  provider: string;
  name: string;
  region: string;
  status: CloudAccountStatus;
  access_key?: string | null;
  last_seen_at?: string | null;
  last_error?: string | null;
  last_error_at?: string | null;
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
  created_at: string;
  updated_at: string;
  disabled_at?: string | null;
}

export interface CloudAccountCreate {
  provider: string;
  name: string;
  region: string;
  access_key?: string;
  secret_key?: string;
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
}

export interface CloudAccountPatch {
  name?: string | null;
  region?: string | null;
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  annotations?: Record<string, string> | null;
}

export interface CloudAccountCredentials {
  access_key: string;
  secret_key: string;
}

export function listCloudAccounts() {
  return request<PagedResponse<CloudAccount>>('/v1/admin/cloud-accounts?limit=200');
}
export function getCloudAccount(id: string) {
  return request<CloudAccount>(`/v1/admin/cloud-accounts/${id}`);
}
export function createCloudAccount(in_: CloudAccountCreate) {
  return request<CloudAccount>('/v1/admin/cloud-accounts', {
    method: 'POST',
    body: JSON.stringify(in_),
  });
}
export function updateCloudAccount(id: string, patch: CloudAccountPatch) {
  return request<CloudAccount>(`/v1/admin/cloud-accounts/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}
export function setCloudAccountCredentials(id: string, creds: CloudAccountCredentials) {
  return request<CloudAccount>(`/v1/admin/cloud-accounts/${id}/credentials`, {
    method: 'PATCH',
    body: JSON.stringify(creds),
  });
}
export function disableCloudAccount(id: string) {
  return request<void>(`/v1/admin/cloud-accounts/${id}/disable`, { method: 'POST' });
}
export function enableCloudAccount(id: string) {
  return request<void>(`/v1/admin/cloud-accounts/${id}/enable`, { method: 'POST' });
}
export function deleteCloudAccount(id: string) {
  return request<void>(`/v1/admin/cloud-accounts/${id}`, { method: 'DELETE' });
}

// --- Virtual machines (ADR-0015) ----------------------------------------

// PowerState values reported by providers. The DB has no CHECK constraint,
// these are just the canonical names we render in the UI.
export type VMPowerState =
  | 'running'
  | 'stopped'
  | 'stopping'
  | 'pending'
  | 'shutting_down'
  | 'terminated'
  | 'error'
  | 'unknown'
  | string;

export interface VMNic {
  id?: string;
  device_index?: number;
  mac_address?: string;
  private_ip?: string;
  public_ip?: string;
  subnet_id?: string;
  vpc_id?: string;
  security_group_ids?: string[];
}

export interface VMSecurityGroup {
  id?: string;
  name?: string;
}

export interface VMBlockDevice {
  device_name?: string;
  volume_id?: string;
  volume_type?: string;
  size_gib?: number;
  delete_on_termination?: boolean;
  iops?: number;
}

// VMApplication is an operator-declared application running on a VM
// (ADR-0019). The EOL enricher consumes the (product, version) pair to
// write longue-vue.io/eol.<product> annotations, so the catalog drives the
// lifecycle scan.
export interface VMApplication {
  product: string;
  version: string;
  name?: string | null;
  notes?: string | null;
  added_at: string;
  added_by: string;
  // ADR-0029 per-entry linkage: this specific product/version pair on the
  // VM may point at an Application independently of the VM's row-level link.
  // application_name is the denormalized display name (server-resolved).
  application_id?: string | null;
  application_name?: string | null;
}

export interface VirtualMachine {
  id: string;
  cloud_account_id: string;
  provider_vm_id: string;
  name: string;
  display_name?: string | null;
  role?: string | null;
  private_ip?: string | null;
  public_ip?: string | null;
  private_dns_name?: string | null;
  vpc_id?: string | null;
  subnet_id?: string | null;
  // The backend serialises the columns as raw JSON; render-time we cast to
  // the structured shapes above. They're optional because providers vary.
  nics?: VMNic[] | null;
  security_groups?: VMSecurityGroup[] | null;
  instance_type?: string | null;
  architecture?: string | null;
  zone?: string | null;
  region?: string | null;
  image_id?: string | null;
  image_name?: string | null;
  keypair_name?: string | null;
  boot_mode?: string | null;
  provider_account_id?: string | null;
  provider_creation_date?: string | null;
  power_state: VMPowerState;
  state_reason?: string | null;
  ready: boolean;
  deletion_protection: boolean;
  kernel_version?: string | null;
  operating_system?: string | null;
  capacity_cpu?: string | null;
  capacity_memory?: string | null;
  block_devices?: VMBlockDevice[] | null;
  root_device_type?: string | null;
  root_device_name?: string | null;
  tags?: Record<string, string> | null;
  labels?: Record<string, string> | null;
  annotations?: Record<string, string> | null;
  applications?: VMApplication[] | null;
  owner?: string | null;
  criticality?: string | null;
  notes?: string | null;
  runbook_url?: string | null;
  // ADR-0029 row-level linkage: links the whole VM to an Application.
  // The GET response carries only the id (no denormalized name yet), so
  // the detail page resolves the display name via getApplication.
  application_id?: string | null;
  // ADR-0029 §6 read-only inherited classification. VMs have no DICT columns
  // of their own, so source is only ever "application" or "none".
  effective_dict?: EffectiveDICT | null;
  created_at: string;
  updated_at: string;
  last_seen_at: string;
  terminated_at?: string | null;
}

export interface VirtualMachineListFilter {
  cloud_account_id?: string;
  cloud_account_name?: string;
  region?: string;
  role?: string;
  power_state?: string;
  include_terminated?: boolean;
  name?: string;
  image?: string;
  application?: string;
  application_version?: string;
}

// VirtualMachinePatch is the merge-patch shape posted to PATCH
// /v1/virtual-machines/{id}. ADR-0029 adds the row-level application
// linkage: application_id sets the link directly, application_name is a
// write-only convenience the server resolves to an id (id wins on
// conflict; null on application_id unlinks).
export type VirtualMachinePatch = Partial<Pick<
  VirtualMachine,
  | 'display_name'
  | 'role'
  | 'owner'
  | 'criticality'
  | 'notes'
  | 'runbook_url'
  | 'annotations'
  | 'applications'
  | 'application_id'
>> & {
  application_name?: string | null;
};

export function listVirtualMachines(filter: VirtualMachineListFilter = {}) {
  return request<PagedResponse<VirtualMachine>>(
    '/v1/virtual-machines' +
      query({
        limit: 200,
        cloud_account_id: filter.cloud_account_id,
        cloud_account_name: filter.cloud_account_name,
        region: filter.region,
        role: filter.role,
        power_state: filter.power_state,
        include_terminated: filter.include_terminated ? 'true' : undefined,
        name: filter.name,
        image: filter.image,
        application: filter.application,
        application_version: filter.application_version,
      }),
  );
}
export function getVirtualMachine(id: string) {
  return request<VirtualMachine>(`/v1/virtual-machines/${id}`);
}
export function updateVirtualMachine(id: string, patch: VirtualMachinePatch) {
  return request<VirtualMachine>(`/v1/virtual-machines/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(patch),
  });
}
export function deleteVirtualMachine(id: string) {
  return request<void>(`/v1/virtual-machines/${id}`, { method: 'DELETE' });
}

// VMApplicationDistinct is one row of the distinct-applications response —
// a product name and the sorted, deduplicated list of versions seen for
// that product across every non-terminated VM. Drives the cascading
// product → version dropdown on the Virtual Machines page.
export interface VMApplicationDistinct {
  product: string;
  versions: string[];
}

// listDistinctVMApplications returns the distinct (product, versions) pairs
// across all non-terminated VMs. Used by the VirtualMachines list page to
// populate the application + version filter dropdowns.
export function listDistinctVMApplications() {
  return request<{ products: VMApplicationDistinct[] }>(
    '/v1/virtual-machines/applications/distinct',
  );
}

// --- Applications + Application blocks (ADR-0029) -------------------------

// ApplicationBlock is the optional grouping layer above Application — a
// portfolio / business domain (e.g. "Finance" or "Office IT"). The
// backend keeps name unique and stores curated metadata alongside.
export interface ApplicationBlock {
  id: string;
  name: string;
  display_name?: string;
  description?: string;
  owner?: string;
  notes?: string;
  annotations: Record<string, string>;
  created_at: string;
  updated_at: string;
  // Convenience counter: how many applications belong to the block.
  // Optional because list-vs-detail responses include it inconsistently.
  application_count?: number;
}

export interface ApplicationBlockCreate {
  name: string;
  display_name?: string;
  description?: string;
  owner?: string;
  notes?: string;
  annotations?: Record<string, string>;
}

// ApplicationBlockPatch is the merge-patch shape. `name` is immutable
// once issued (mirrors clusters/cloud-accounts).
export type ApplicationBlockPatch = Partial<Omit<ApplicationBlockCreate, 'name'>>;

// ApplicationMemberCounts is the inline summary the backend embeds on
// every Application response so we don't have to round-trip /members
// just to render a count badge.
export interface ApplicationMemberCounts {
  workloads: number;
  virtual_machines: number;
  vm_applications: number;
}

export interface Application {
  id: string;
  name: string;
  display_name?: string;
  description?: string;
  application_block_id?: string | null;
  application_block_name?: string | null;
  owner?: string;
  criticality?: string;
  notes?: string;
  runbook_url?: string;
  annotations: Record<string, string>;
  // SecNumCloud DICT vector (Disponibilité / Intégrité /
  // Confidentialité / Traçabilité). null until the operator records it.
  sec_disponibilite?: number | null;
  sec_integrite?: number | null;
  sec_confidentialite?: number | null;
  sec_tracabilite?: number | null;
  sec_notes?: string | null;
  created_at: string;
  updated_at: string;
  member_counts: ApplicationMemberCounts;
}

export interface ApplicationCreate {
  name: string;
  display_name?: string;
  description?: string;
  // Either link by id or by name — backend resolves the name to an id
  // on create (returns 409 if both are set and disagree).
  application_block_id?: string;
  application_block_name?: string;
  owner?: string;
  criticality?: string;
  notes?: string;
  runbook_url?: string;
  annotations?: Record<string, string>;
  sec_disponibilite?: number;
  sec_integrite?: number;
  sec_confidentialite?: number;
  sec_tracabilite?: number;
  sec_notes?: string;
}

export type ApplicationPatch = Partial<Omit<ApplicationCreate, 'name'>>;

// ApplicationMember is one row of GET /v1/applications/{id}/members —
// a workload, VM, or specific vm_application linked to this app.
export interface ApplicationMember {
  kind: 'workload' | 'virtual_machine' | 'vm_application';
  id: string;
  name: string;
  parent?: { kind: string; id?: string; name: string };
  linked_at: string;
  linked_by: string;
}

export interface ApplicationListFilter {
  name?: string;
  application_block_id?: string;
  application_block_name?: string;
  criticality?: string;
  // has_dict / dict_min surface the DICT-coverage filter set in
  // ADR-0029 Phase 4.
  has_dict?: boolean;
  dict_min?: number;
  cursor?: string;
  limit?: number;
}

export interface ApplicationBlockListFilter {
  name?: string;
  owner?: string;
  cursor?: string;
  limit?: number;
}

export function listApplicationBlocks(filter: ApplicationBlockListFilter = {}) {
  return request<PagedResponse<ApplicationBlock>>(
    '/v1/application-blocks' +
      query({
        limit: filter.limit ?? 200,
        name: filter.name,
        owner: filter.owner,
        cursor: filter.cursor,
      }),
  );
}

export function getApplicationBlock(id: string) {
  return request<ApplicationBlock>(`/v1/application-blocks/${id}`);
}

export function createApplicationBlock(body: ApplicationBlockCreate) {
  return request<ApplicationBlock>('/v1/application-blocks', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function patchApplicationBlock(id: string, body: ApplicationBlockPatch) {
  return request<ApplicationBlock>(`/v1/application-blocks/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  });
}

export function deleteApplicationBlock(id: string) {
  return request<void>(`/v1/application-blocks/${id}`, { method: 'DELETE' });
}

export function listApplications(filter: ApplicationListFilter = {}) {
  return request<PagedResponse<Application>>(
    '/v1/applications' +
      query({
        limit: filter.limit ?? 200,
        name: filter.name,
        application_block_id: filter.application_block_id,
        application_block_name: filter.application_block_name,
        criticality: filter.criticality,
        has_dict: filter.has_dict !== undefined ? String(filter.has_dict) : undefined,
        dict_min: filter.dict_min,
        cursor: filter.cursor,
      }),
  );
}

export function getApplication(id: string) {
  return request<Application>(`/v1/applications/${id}`);
}

export function getApplicationByName(name: string) {
  return request<Application>(`/v1/applications/by-name/${encodeURIComponent(name)}`);
}

export function createApplication(body: ApplicationCreate) {
  return request<Application>('/v1/applications', {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

export function patchApplication(id: string, body: ApplicationPatch) {
  return request<Application>(`/v1/applications/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  });
}

export function deleteApplication(id: string) {
  return request<void>(`/v1/applications/${id}`, { method: 'DELETE' });
}

export function listApplicationMembers(id: string, cursor?: string, limit?: number) {
  return request<PagedResponse<ApplicationMember>>(
    `/v1/applications/${id}/members` + query({ cursor, limit }),
  );
}

// ApplicationEOLSource names one member asset contributing an EOL row
// (ADR-0029 §5).
export type ApplicationEOLSource = {
  kind: 'workload' | 'virtual_machine' | 'vm_application' | string;
  id: string;
  name: string;
};

// ApplicationEOLRow is one product rolled up across an application's
// members, with the contributing sources listed.
export type ApplicationEOLRow = {
  product: string;
  cycle: string;
  eol_status: string;
  latest_available?: string;
  evaluated_at?: string;
  sources: ApplicationEOLSource[];
};

// applicationEOL returns the read-time aggregated EOL signal across an
// application's members (workloads via image-versions, linked VMs, and
// matching VM-application entries). ADR-0029 §5.
export function applicationEOL(id: string): Promise<{ items: ApplicationEOLRow[] }> {
  return request<{ items: ApplicationEOLRow[] }>(`/v1/applications/${id}/eol`);
}

// --- Image versions ---------------------------------------------------------

export interface ContainerVersionInfo {
  // Present on passthrough images and on resolved-origin images.
  latest_tag?: string;
  is_behind?: boolean;
  last_checked_at?: string;
  // Origin fields. Absent on passthrough images. Present on mirrored refs.
  origin_image_repo?: string;
  origin_status?: 'resolved' | 'unresolved';
  origin_error?: string;
}

export interface ImageVersionVariant {
  variant: string;
  latest_tag: string | null;
  annotation: Record<string, unknown>;
  source: 'registry';
  last_checked_at: string;
  last_error: string | null;
  last_error_at: string | null;
}

export interface ImageVersion {
  image_repo: string;
  registry: string;
  variants: ImageVersionVariant[];
}

export interface ImageVersionListFilter {
  limit?: number;
  cursor?: string;
  registry?: string;
  image_repo?: string;
  variant?: string;
  has_error?: boolean;
}

export function listImageVersions(filter: ImageVersionListFilter = {}) {
  return request<PagedResponse<ImageVersion>>(
    '/v1/image-versions' +
      query({
        limit: filter.limit,
        cursor: filter.cursor,
        registry: filter.registry,
        image_repo: filter.image_repo,
        variant: filter.variant,
        has_error: filter.has_error !== undefined ? String(filter.has_error) : undefined,
      }),
  );
}

export function getImageVersion(imageRepo: string) {
  return request<ImageVersion>(`/v1/image-versions/${encodeURIComponent(imageRepo)}`);
}

export function refreshImageVersions() {
  return request<{ queued: boolean; already_running: boolean }>('/v1/image-versions/refresh', {
    method: 'POST',
  });
}

// --- Image registries (admin) -----------------------------------------------

export interface ImageRegistry {
  hostname: string;
  path_prefix: string;
  rate_limit_per_sec: number;
  enabled: boolean;
  notes: string | null;
  is_mirror: boolean;
  replicates_from_hostname?: string | null;
  auth_username: string | null;
  auth_configured: boolean;
  created_at: string;
  updated_at: string;
}

export interface ImageRegistryCreate {
  hostname: string;
  path_prefix?: string;
  rate_limit_per_sec: number;
  enabled?: boolean;
  notes?: string;
  is_mirror?: boolean;
  replicates_from_hostname?: string;
  auth_username?: string;
  auth_token?: string;
}

export interface ImageRegistryPatch {
  rate_limit_per_sec?: number;
  enabled?: boolean;
  notes?: string | null;
  is_mirror?: boolean;
  replicates_from_hostname?: string | null;
  auth_username?: string | null;
  auth_token?: string;
}

export interface ImageRegistryCredentials {
  auth_username: string;
  auth_token: string;
}

// pathPrefixSegment maps the empty prefix to the "_root" sentinel used in URLs.
function pathPrefixSegment(prefix: string): string {
  return prefix === '' ? '_root' : encodeURIComponent(prefix);
}

export function listImageRegistries() {
  return request<PagedResponse<ImageRegistry>>('/v1/admin/image-versions/registries');
}

export function createImageRegistry(in_: ImageRegistryCreate) {
  return request<ImageRegistry>('/v1/admin/image-versions/registries', {
    method: 'POST',
    body: JSON.stringify(in_),
  });
}

export function updateImageRegistry(
  hostname: string,
  pathPrefix: string,
  patch: ImageRegistryPatch,
) {
  return request<ImageRegistry>(
    `/v1/admin/image-versions/registries/${encodeURIComponent(hostname)}/${pathPrefixSegment(pathPrefix)}`,
    {
      method: 'PATCH',
      body: JSON.stringify(patch),
    },
  );
}

export function deleteImageRegistry(hostname: string, pathPrefix: string) {
  return request<void>(
    `/v1/admin/image-versions/registries/${encodeURIComponent(hostname)}/${pathPrefixSegment(pathPrefix)}`,
    { method: 'DELETE' },
  );
}

export function getImageRegistryCredentials(hostname: string, pathPrefix: string) {
  return request<ImageRegistryCredentials>(
    `/v1/admin/image-versions/registries/${encodeURIComponent(hostname)}/${pathPrefixSegment(pathPrefix)}/credentials`,
  );
}

// --- Time-travel history (ADR-0021 Phase 3) ---------------------------------

export interface HistoryPatchOp {
  op: 'replace' | 'add' | 'remove';
  path: string;
  from?: unknown;
  to?: unknown;
}

export interface HistoryRow {
  history_id: string;
  entity_id: string;
  valid_from: string; // RFC3339
  valid_to?: string | null; // RFC3339, null for the current row
  change_type: 'create' | 'update' | 'soft_delete' | 'restore';
  actor_id?: string | null;
  actor_kind?: string | null;
  diff: HistoryPatchOp[];
}

export interface EntityHistoryPage {
  items: HistoryRow[];
  next_cursor: string;
}

export type HistoryKind = 'clusters' | 'namespaces' | 'nodes' | 'workloads';

export function listEntityHistory(
  kind: HistoryKind,
  id: string,
  cursor?: string,
  limit?: number,
): Promise<EntityHistoryPage> {
  return request<EntityHistoryPage>(
    `/v1/${kind}/${id}/history` + query({ cursor, limit }),
  );
}

// --- Extracts (CSV/JSON/ZIP downloads) ----------------------------------

import { downloadExtract, type DownloadResult } from './lib/download';

export type ExtractFormat = 'csv' | 'json';
export type SearchExtractKind = 'workloads' | 'pods' | 'virtual_machines';
export type { DownloadResult as ExtractResult } from './lib/download';

export function extractSearch(
  q: string,
  kind: SearchExtractKind,
  format: ExtractFormat,
): Promise<DownloadResult> {
  const params = new URLSearchParams({ q, kind, format });
  return downloadExtract(
    `/v1/search/extract?${params.toString()}`,
    `longue-vue-search-${kind}.${format}`,
  );
}

export function extractSearchZip(q: string): Promise<DownloadResult> {
  const params = new URLSearchParams({ q });
  return downloadExtract(
    `/v1/search/extract.zip?${params.toString()}`,
    `longue-vue-search.zip`,
  );
}

export interface EolExtractParams {
  format: ExtractFormat;
  entity_type?: 'cluster' | 'node' | 'vm';
  status?: 'eol' | 'approaching_eol' | 'supported' | 'unknown';
}

export function extractEol(p: EolExtractParams): Promise<DownloadResult> {
  const params = new URLSearchParams({ format: p.format });
  if (p.entity_type) params.set('entity_type', p.entity_type);
  if (p.status) params.set('status', p.status);
  return downloadExtract(
    `/v1/eol/extract?${params.toString()}`,
    `longue-vue-eol.${p.format}`,
  );
}
