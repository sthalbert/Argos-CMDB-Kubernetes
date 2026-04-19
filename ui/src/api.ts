// Thin fetch wrapper around the Argos REST API. Hand-written for v0 — a
// generated OpenAPI client can replace this when the surface grows.
//
// The bearer token is stored in sessionStorage (cleared on tab close),
// matching ADR-0006's auth choice. Callers read it via getToken() and
// pass it via request() which injects the Authorization header.

const TOKEN_KEY = 'argos.token';

export function getToken(): string | null {
  return sessionStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string): void {
  sessionStorage.setItem(TOKEN_KEY, token);
}

export function clearToken(): void {
  sessionStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message);
    this.name = 'ApiError';
  }
}

async function request<T>(path: string): Promise<T> {
  const token = getToken();
  const res = await fetch(path, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
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
  // DELETE endpoints return 204 with an empty body; surface undefined.
  if (res.status === 204) {
    return undefined as unknown as T;
  }
  return res.json() as Promise<T>;
}

// query flattens a (sparse) record into a ?k1=v1&k2=v2 suffix. Keys whose
// value is undefined/null/empty are dropped. Values are percent-encoded.
function query(params?: Record<string, string | number | undefined | null>): string {
  if (!params) return '';
  const entries = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== null && v !== '')
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
  return entries.length ? `?${entries.join('&')}` : '';
}

// Shared shapes -----------------------------------------------------------

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

// Entity schemas (subset of openapi.yaml — kept narrow to what the UI uses).

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
  layer: Layer;
  created_at: string;
  updated_at: string;
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
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export interface Namespace {
  id: string;
  cluster_id: string;
  name: string;
  phase?: string | null;
  labels?: Record<string, string> | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
}

export interface Workload {
  id: string;
  namespace_id: string;
  kind: WorkloadKind;
  name: string;
  replicas?: number | null;
  ready_replicas?: number | null;
  containers?: Container[] | null;
  labels?: Record<string, string> | null;
  layer: Layer;
  created_at: string;
  updated_at: string;
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
  labels?: Record<string, string> | null;
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

// One entry from status.loadBalancer.ingress[] — mirrors the Kubernetes
// shape. IP for VIP-style setups (MetalLB, Kube-VIP, hardware LB), or
// hostname for cloud-managed LBs (AWS ELB / GCLB-style DNS).
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
  layer: Layer;
  created_at: string;
  updated_at: string;
}

// Endpoints ---------------------------------------------------------------

export function listClusters() {
  return request<PagedResponse<Cluster>>('/v1/clusters?limit=200');
}
export function getCluster(id: string) {
  return request<Cluster>(`/v1/clusters/${id}`);
}

export function listNodes(filter?: { cluster_id?: string }) {
  return request<PagedResponse<Node>>('/v1/nodes' + query({ limit: 200, ...filter }));
}
export function getNode(id: string) {
  return request<Node>(`/v1/nodes/${id}`);
}

export function listNamespaces(filter?: { cluster_id?: string }) {
  return request<PagedResponse<Namespace>>('/v1/namespaces' + query({ limit: 200, ...filter }));
}
export function getNamespace(id: string) {
  return request<Namespace>(`/v1/namespaces/${id}`);
}

export function listWorkloads(filter?: {
  namespace_id?: string;
  kind?: WorkloadKind;
  image?: string;
}) {
  return request<PagedResponse<Workload>>('/v1/workloads' + query({ limit: 200, ...filter }));
}
export function getWorkload(id: string) {
  return request<Workload>(`/v1/workloads/${id}`);
}

export function listPods(filter?: {
  namespace_id?: string;
  node_name?: string;
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
