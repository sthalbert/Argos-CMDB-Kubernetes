import type {
  ApiToken, AuditEvent, AuthConfig, Cluster, CloudAccount, Container,
  Ingress, ImpactGraph, Me, Namespace, Node, NodeCondition, NodeTaint,
  PagedResponse, PersistentVolume, PersistentVolumeClaim, Pod, Service,
  Session, Settings, User, VirtualMachine, VMApplication, Workload,
} from '../api';

export const fixtureCluster: Cluster = {
  id: '11111111-1111-1111-1111-111111111111',
  name: 'prod-eu-west',
  display_name: 'Prod EU West',
  environment: 'prod',
  provider: 'outscale',
  region: 'eu-west-2',
  kubernetes_version: '1.30.4',
  api_endpoint: 'https://k8s.example.com',
  labels: { tier: 'prod' },
  owner: 'platform',
  criticality: 'high',
  notes: null,
  runbook_url: null,
  annotations: null,
  layer: 'infrastructure_logical',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-02T00:00:00Z',
};

export const fixtureNamespace: Namespace = {
  id: '22222222-2222-2222-2222-222222222222',
  cluster_id: fixtureCluster.id,
  name: 'payments',
  display_name: null,
  phase: 'Active',
  labels: { team: 'payments' },
  owner: null,
  criticality: null,
  notes: null,
  runbook_url: null,
  annotations: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

const fixtureCondition: NodeCondition = {
  type: 'Ready',
  status: 'True',
  reason: 'KubeletReady',
  message: 'kubelet is posting ready status',
  last_transition_time: '2025-01-01T00:00:00Z',
};

const fixtureTaint: NodeTaint = { key: 'role', value: 'gpu', effect: 'NoSchedule' };

export const fixtureNode: Node = {
  id: '33333333-3333-3333-3333-333333333333',
  cluster_id: fixtureCluster.id,
  name: 'node-1',
  display_name: null,
  role: 'worker',
  kubelet_version: 'v1.30.4',
  kube_proxy_version: 'v1.30.4',
  container_runtime_version: 'containerd://1.7.13',
  os_image: 'Ubuntu 22.04.4 LTS',
  operating_system: 'linux',
  kernel_version: '5.15.0-89-generic',
  architecture: 'amd64',
  internal_ip: '10.0.0.1',
  external_ip: null,
  pod_cidr: '10.244.0.0/24',
  provider_id: 'aws:///eu-west-2a/i-deadbeef',
  instance_type: 'tinav5.c4r8p1',
  zone: 'eu-west-2a',
  capacity_cpu: '4',
  capacity_memory: '8Gi',
  capacity_pods: '110',
  capacity_ephemeral_storage: '50Gi',
  allocatable_cpu: '3800m',
  allocatable_memory: '7.5Gi',
  allocatable_pods: '110',
  allocatable_ephemeral_storage: '45Gi',
  conditions: [fixtureCondition],
  taints: [fixtureTaint],
  unschedulable: false,
  ready: true,
  labels: { 'kubernetes.io/hostname': 'node-1' },
  owner: null,
  criticality: null,
  notes: null,
  runbook_url: null,
  annotations: null,
  hardware_model: null,
  layer: 'infrastructure_physical',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

const fixtureContainer: Container = {
  name: 'app',
  image: 'ghcr.io/longue-vue/app:1.2.3',
  image_id: 'sha256:abc123',
  init: false,
};

export const fixtureWorkload: Workload = {
  id: '44444444-4444-4444-4444-444444444444',
  namespace_id: fixtureNamespace.id,
  kind: 'Deployment',
  name: 'web',
  replicas: 3,
  ready_replicas: 3,
  containers: [fixtureContainer],
  labels: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixturePod: Pod = {
  id: '55555555-5555-5555-5555-555555555555',
  namespace_id: fixtureNamespace.id,
  name: 'web-abcd-efgh',
  phase: 'Running',
  node_name: fixtureNode.name,
  pod_ip: '10.244.0.5',
  workload_id: fixtureWorkload.id,
  containers: [fixtureContainer],
  labels: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixtureService: Service = {
  id: '66666666-6666-6666-6666-666666666666',
  namespace_id: fixtureNamespace.id,
  name: 'web',
  type: 'ClusterIP',
  cluster_ip: '10.96.0.10',
  selector: { app: 'web' },
  ports: [{ port: 80, protocol: 'TCP', target_port: '8080' }],
  load_balancer: null,
  labels: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixtureIngress: Ingress = {
  id: '77777777-7777-7777-7777-777777777777',
  namespace_id: fixtureNamespace.id,
  name: 'web-ingress',
  ingress_class_name: 'nginx',
  rules: [{
    host: 'web.example.com',
    paths: [{ path: '/', path_type: 'Prefix', backend: { service_name: 'web', service_port_number: 80 } }],
  }],
  tls: null,
  load_balancer: [{ ip: '203.0.113.1', ports: [{ port: 443, protocol: 'TCP' }] }],
  labels: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixturePV: PersistentVolume = {
  id: '88888888-8888-8888-8888-888888888888',
  cluster_id: fixtureCluster.id,
  name: 'pv-1',
  capacity: '10Gi',
  access_modes: ['ReadWriteOnce'],
  reclaim_policy: 'Retain',
  phase: 'Bound',
  storage_class_name: 'standard',
  csi_driver: 'osc.csi.outscale.com',
  volume_handle: 'vol-deadbeef',
  claim_ref_namespace: fixtureNamespace.name,
  claim_ref_name: 'data',
  labels: null,
  layer: 'infrastructure_logical',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixturePVC: PersistentVolumeClaim = {
  id: '99999999-9999-9999-9999-999999999999',
  namespace_id: fixtureNamespace.id,
  name: 'data',
  phase: 'Bound',
  storage_class_name: 'standard',
  volume_name: fixturePV.name,
  bound_volume_id: fixturePV.id,
  access_modes: ['ReadWriteOnce'],
  requested_storage: '10Gi',
  labels: null,
  layer: 'applicative',
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixtureCloudAccount: CloudAccount = {
  id: 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
  provider: 'outscale',
  name: 'prod-eu',
  region: 'eu-west-2',
  status: 'active',
  access_key: 'AKIAIOSFODNN7EXAMPLE',
  last_seen_at: '2025-01-01T00:00:00Z',
  last_error: null,
  last_error_at: null,
  owner: 'platform',
  criticality: 'high',
  notes: null,
  runbook_url: null,
  annotations: null,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  disabled_at: null,
};

const fixtureApplication: VMApplication = {
  product: 'nginx',
  version: '1.27.0',
  name: 'edge',
  notes: null,
  added_at: '2025-01-01T00:00:00Z',
  added_by: 'sysadmin',
};

export const fixtureVirtualMachine: VirtualMachine = {
  id: 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',
  cloud_account_id: fixtureCloudAccount.id,
  provider_vm_id: 'i-cafef00d',
  name: 'vpn-01',
  display_name: 'VPN gateway 01',
  role: 'vpn',
  private_ip: '10.0.0.10',
  public_ip: '203.0.113.10',
  private_dns_name: null,
  vpc_id: 'vpc-1',
  subnet_id: 'subnet-1',
  nics: null,
  security_groups: null,
  instance_type: 'tinav5.c2r4p1',
  architecture: 'x86_64',
  zone: 'eu-west-2a',
  region: 'eu-west-2',
  image_id: 'ami-deadbeef',
  image_name: 'ubuntu-22.04',
  keypair_name: null,
  boot_mode: 'uefi',
  provider_account_id: null,
  provider_creation_date: '2025-01-01T00:00:00Z',
  power_state: 'running',
  state_reason: null,
  ready: true,
  deletion_protection: false,
  kernel_version: null,
  operating_system: 'ubuntu',
  capacity_cpu: '2',
  capacity_memory: '4Gi',
  block_devices: null,
  root_device_type: 'bsu',
  root_device_name: '/dev/sda1',
  tags: null,
  labels: null,
  annotations: null,
  applications: [fixtureApplication],
  owner: 'platform',
  criticality: 'high',
  notes: null,
  runbook_url: null,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  last_seen_at: '2025-01-01T00:00:00Z',
  terminated_at: null,
};

export const fixtureMe: Me = {
  kind: 'user',
  id: 'cccccccc-cccc-cccc-cccc-cccccccccccc',
  username: 'alice',
  role: 'admin',
  scopes: ['read', 'write', 'delete', 'admin', 'audit'],
  must_change_password: false,
};

export const fixtureUser: User = {
  id: fixtureMe.id!,
  username: fixtureMe.username!,
  role: 'admin',
  must_change_password: false,
  created_at: '2025-01-01T00:00:00Z',
  updated_at: '2025-01-01T00:00:00Z',
  last_login_at: '2025-01-02T00:00:00Z',
  disabled_at: null,
};

export const fixtureToken: ApiToken = {
  id: 'dddddddd-dddd-dddd-dddd-dddddddddddd',
  name: 'collector',
  prefix: 'longue_vu',
  scopes: ['read', 'write'],
  created_by_user_id: fixtureUser.id,
  created_at: '2025-01-01T00:00:00Z',
  last_used_at: '2025-01-02T00:00:00Z',
  expires_at: null,
  revoked_at: null,
};

export const fixtureSession: Session = {
  id: 'a1b2c3d4…',
  user_id: fixtureUser.id,
  username: fixtureUser.username,
  created_at: '2025-01-01T00:00:00Z',
  last_used_at: '2025-01-02T00:00:00Z',
  expires_at: '2025-01-02T08:00:00Z',
  user_agent: 'Mozilla/5.0',
  source_ip: '198.51.100.1',
};

export const fixtureAuditEvent: AuditEvent = {
  id: 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee',
  occurred_at: '2025-01-01T00:00:00Z',
  actor_id: fixtureUser.id,
  actor_kind: 'user',
  actor_username: fixtureUser.username,
  actor_role: 'admin',
  action: 'cluster.update',
  resource_type: 'cluster',
  resource_id: fixtureCluster.id,
  http_method: 'PATCH',
  http_path: `/v1/clusters/${fixtureCluster.id}`,
  http_status: 200,
  source_ip: '198.51.100.1',
  user_agent: 'Mozilla/5.0',
  details: null,
};

export const fixtureSettings: Settings = {
  eol_enabled: true,
  mcp_enabled: false,
  updated_at: '2025-01-01T00:00:00Z',
};

export const fixtureAuthConfig: AuthConfig = {
  oidc: { enabled: false },
};

export const fixtureImpactGraph: ImpactGraph = {
  root: { id: fixtureCluster.id, type: 'cluster', name: fixtureCluster.name },
  nodes: [
    { id: fixtureCluster.id, type: 'cluster', name: fixtureCluster.name },
    { id: fixtureNamespace.id, type: 'namespace', name: fixtureNamespace.name },
  ],
  edges: [{ from: fixtureCluster.id, to: fixtureNamespace.id, relation: 'contains' }],
};

export function paged<T>(items: T[]): PagedResponse<T> {
  return { items, next_cursor: null };
}
