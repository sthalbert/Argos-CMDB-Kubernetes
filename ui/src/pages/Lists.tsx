// One-file home for every top-level list page. All of them use EntityListPage
// with per-entity column configs — sortable headers, search input, and
// pagination come for free. Adding a new kind means adding one config here.

import { useMemo, useState } from 'react';
import { Link } from 'react-router';
import * as api from '../api';
import { useResource } from '../hooks';
import { Dash, formatTs, IdLink, LayerPill, LoadBalancerAddresses, NamespaceLink } from '../components';
import { EntityListPage } from '../components/EntityListPage';
import {
  ClusterIcon, NodeIcon, NamespaceIcon, WorkloadIcon, PodIcon,
  ServiceIcon, IngressIcon, VolumeIcon,
} from '../icons';

export function Clusters() {
  const [staleFilter, setStaleFilter] = useState<'all' | 'stale' | 'active'>('all');
  const stale = staleFilter === 'all' ? undefined : staleFilter === 'stale';
  return (
    <EntityListPage<api.Cluster>
      title="Clusters"
      icon={<ClusterIcon size={20} />}
      storageKey="lists.clusters"
      emptyMessage="No clusters yet. Connect a collector to start populating your inventory."
      fetchPage={(params, cursor, limit) => api.listClusters({ ...params, stale, cursor, limit })}
      rowKey={(c) => c.id}
      extraDeps={[staleFilter]}
      extraFilters={
        <label className="muted" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          Status
          <select
            value={staleFilter}
            onChange={(e) => setStaleFilter(e.target.value as 'all' | 'stale' | 'active')}
          >
            <option value="all">All</option>
            <option value="stale">Stale only</option>
            <option value="active">Active only</option>
          </select>
        </label>
      }
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (c) => (
            <>
              <Link to={`/clusters/${c.id}`}>
                <strong>{c.display_name || c.name}</strong>
              </Link>
              {c.display_name && (
                <div className="muted" style={{ fontSize: '0.8rem' }}>
                  {c.name}
                </div>
              )}
            </>
          ),
        },
        { key: 'environment', label: 'Environment', sortKey: 'environment', render: (c) => c.environment || <Dash /> },
        { key: 'provider', label: 'Provider', sortKey: 'provider', render: (c) => c.provider || <Dash /> },
        { key: 'region', label: 'Region', sortKey: 'region', render: (c) => c.region || <Dash /> },
        {
          key: 'k8s_version',
          label: 'K8s version',
          sortKey: 'kubernetes_version',
          render: (c) => (c.kubernetes_version ? <code>{c.kubernetes_version}</code> : <Dash />),
        },
        {
          key: 'last_seen',
          label: 'Last seen',
          sortKey: 'last_seen_at',
          render: (c) => (
            <>
              {c.stale && <span className="pill status-bad">stale</span>}{' '}
              {c.last_seen_at ? formatTs(c.last_seen_at) : <Dash />}
            </>
          ),
        },
        { key: 'layer', label: 'Layer', render: (c) => <LayerPill layer={c.layer} /> },
      ]}
    />
  );
}

// Lookup map only — not user-facing. Walks every server page so id→name
// resolution stays complete regardless of the UI's selected page size.
async function fetchAllClusters(): Promise<api.Cluster[]> {
  const items: api.Cluster[] = [];
  let cursor: string | undefined = undefined;
  for (let i = 0; i < 1000; i++) {
    const page = await api.listClusters({ cursor, limit: 500 });
    items.push(...page.items);
    if (!page.next_cursor) break;
    cursor = page.next_cursor;
  }
  return items;
}

export function Nodes() {
  const clustersState = useResource(() => fetchAllClusters(), []);
  const clustersById = useMemo(() => {
    if (clustersState.status !== 'ready') return new Map<string, api.Cluster>();
    return new Map(clustersState.data.map((c) => [c.id, c]));
  }, [clustersState]);
  return (
    <EntityListPage<api.Node>
      title="Nodes"
      icon={<NodeIcon size={20} />}
      storageKey="lists.nodes"
      emptyMessage="No nodes found. Ensure a collector is running and connected to a cluster."
      fetchPage={(params, cursor, limit) => api.listNodes({ ...params, cursor, limit })}
      rowKey={(n) => n.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (n) => (
            <Link to={`/nodes/${n.id}`}>
              <strong>{n.display_name || n.name}</strong>
            </Link>
          ),
        },
        {
          key: 'cluster',
          label: 'Cluster',
          render: (n) => {
            const cluster = clustersById.get(n.cluster_id);
            return cluster ? (
              <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
            ) : (
              <IdLink to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />
            );
          },
        },
        { key: 'role', label: 'Role', sortKey: 'role', render: (n) => n.role ? <span className="pill">{n.role}</span> : <Dash /> },
        { key: 'zone', label: 'Zone', sortKey: 'zone', render: (n) => n.zone ? <code>{n.zone}</code> : <Dash /> },
        { key: 'instance_type', label: 'Instance type', sortKey: 'instance_type', render: (n) => n.instance_type ? <code>{n.instance_type}</code> : <Dash /> },
        {
          key: 'cpu_mem',
          label: 'CPU / Mem',
          render: (n) => (
            n.capacity_cpu || n.capacity_memory ? (
              <code>{n.capacity_cpu || '?'} / {n.capacity_memory || '?'}</code>
            ) : (
              <Dash />
            )
          ),
        },
        { key: 'status', label: 'Status', render: (n) => <NodeStatusBadge ready={n.ready} unschedulable={n.unschedulable} /> },
      ]}
    />
  );
}

// Compact at-a-glance status: green Ready, orange cordoned, red NotReady.
function NodeStatusBadge({
  ready,
  unschedulable,
}: {
  ready?: boolean | null;
  unschedulable?: boolean | null;
}) {
  if (ready === null || ready === undefined) return <Dash />;
  const parts = [ready ? 'Ready' : 'NotReady'];
  if (unschedulable) parts.push('Cordoned');
  const cls = ready ? (unschedulable ? 'status-warn' : 'status-ok') : 'status-bad';
  return <span className={`pill ${cls}`}>{parts.join(' · ')}</span>;
}

export function Namespaces() {
  return (
    <EntityListPage<api.Namespace>
      title="Namespaces"
      icon={<NamespaceIcon size={20} />}
      storageKey="lists.namespaces"
      emptyMessage="No namespaces found. They are collected automatically from your clusters."
      fetchPage={(params, cursor, limit) => api.listNamespaces({ ...params, cursor, limit })}
      rowKey={(n) => n.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (n) => (
            <Link to={`/namespaces/${n.id}`}>
              <strong>{n.name}</strong>
            </Link>
          ),
        },
        {
          key: 'cluster',
          label: 'Cluster',
          render: (n) => (
            <Link to={`/clusters/${n.cluster_id}`}>
              {n.cluster_name ?? <span title="cluster row missing">(orphan)</span>}
            </Link>
          ),
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (n) => n.phase || <Dash /> },
      ]}
    />
  );
}

export function Workloads() {
  return (
    <EntityListPage<api.Workload>
      title="Workloads"
      icon={<WorkloadIcon size={20} />}
      storageKey="lists.workloads"
      emptyMessage="No workloads found. Deployments, StatefulSets and DaemonSets will appear here once collected."
      fetchPage={(params, cursor, limit) => api.listWorkloads({ ...params, cursor, limit })}
      rowKey={(w) => w.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (w) => (
            <Link to={`/workloads/${w.id}`}>
              <strong>{w.name}</strong>
            </Link>
          ),
        },
        { key: 'kind', label: 'Kind', sortKey: 'kind', render: (w) => <span className="pill">{w.kind}</span> },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (w) => (
            <NamespaceLink
              namespaceId={w.namespace_id}
              namespaceName={w.namespace_name}
              clusterId={w.cluster_id}
              clusterName={w.cluster_name}
            />
          ),
        },
        {
          key: 'replicas',
          label: 'Replicas',
          render: (w) => (
            <>
              {w.ready_replicas ?? '?'}
              <span className="muted">/{w.replicas ?? '?'}</span>
            </>
          ),
        },
        {
          key: 'containers',
          label: 'Containers',
          render: (w) => (
            w.containers?.length ? (
              <code>{w.containers.map((c) => c.image).join(', ')}</code>
            ) : (
              <Dash />
            )
          ),
        },
      ]}
    />
  );
}

export function Pods() {
  return (
    <EntityListPage<api.Pod>
      title="Pods"
      icon={<PodIcon size={20} />}
      storageKey="lists.pods"
      emptyMessage="No pods found. Pods are collected from all connected clusters."
      fetchPage={(params, cursor, limit) => api.listPods({ ...params, cursor, limit })}
      rowKey={(p) => p.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (p) => (
            <Link to={`/pods/${p.id}`}>
              <strong>{p.name}</strong>
            </Link>
          ),
        },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (p) => (
            <NamespaceLink
              namespaceId={p.namespace_id}
              namespaceName={p.namespace_name}
              clusterId={p.cluster_id}
              clusterName={p.cluster_name}
            />
          ),
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (p) => p.phase || <Dash /> },
        { key: 'node', label: 'Node', sortKey: 'node_name', render: (p) => p.node_name ? <code>{p.node_name}</code> : <Dash /> },
        { key: 'pod_ip', label: 'Pod IP', sortKey: 'pod_ip', render: (p) => p.pod_ip ? <code>{p.pod_ip}</code> : <Dash /> },
        {
          key: 'workload',
          label: 'Workload',
          render: (p) => (
            p.workload_id ? (
              <Link to={`/workloads/${p.workload_id}`}>
                {p.workload_name ?? <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />}
              </Link>
            ) : (
              <Dash />
            )
          ),
        },
      ]}
    />
  );
}

export function Services() {
  return (
    <EntityListPage<api.Service>
      title="Services"
      icon={<ServiceIcon size={20} />}
      storageKey="lists.services"
      emptyMessage="No services found. Kubernetes Services are collected automatically."
      fetchPage={(params, cursor, limit) => api.listServices({ ...params, cursor, limit })}
      rowKey={(s) => s.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (s) => (
            <Link to={`/services/${s.id}`}>
              <strong>{s.name}</strong>
            </Link>
          ),
        },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (s) => (
            <NamespaceLink
              namespaceId={s.namespace_id}
              namespaceName={s.namespace_name}
              clusterId={s.cluster_id}
              clusterName={s.cluster_name}
            />
          ),
        },
        { key: 'type', label: 'Type', sortKey: 'type', render: (s) => <span className="pill">{s.type || 'ClusterIP'}</span> },
        { key: 'cluster_ip', label: 'ClusterIP', sortKey: 'cluster_ip', render: (s) => s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash /> },
        {
          key: 'ports',
          label: 'Ports',
          render: (s) => (
            s.ports?.length ? (
              <code>{s.ports.map((p) => `${p.port}/${p.protocol || 'TCP'}`).join(', ')}</code>
            ) : (
              <Dash />
            )
          ),
        },
        { key: 'load_balancer', label: 'Load balancer', render: (s) => <LoadBalancerAddresses entries={s.load_balancer} /> },
      ]}
    />
  );
}

export function Ingresses() {
  return (
    <EntityListPage<api.Ingress>
      title="Ingresses"
      icon={<IngressIcon size={20} />}
      storageKey="lists.ingresses"
      emptyMessage="No ingresses found. Ingress resources are collected from all namespaces."
      fetchPage={(params, cursor, limit) => api.listIngresses({ ...params, cursor, limit })}
      rowKey={(i) => i.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (i) => (
            <Link to={`/ingresses/${i.id}`}>
              <strong>{i.name}</strong>
            </Link>
          ),
        },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (i) => (
            <NamespaceLink
              namespaceId={i.namespace_id}
              namespaceName={i.namespace_name}
              clusterId={i.cluster_id}
              clusterName={i.cluster_name}
            />
          ),
        },
        { key: 'class', label: 'Class', sortKey: 'ingress_class_name', render: (i) => i.ingress_class_name || <Dash /> },
        {
          key: 'hosts',
          label: 'Hosts',
          render: (i) => (
            i.rules?.length ? (
              <code>{i.rules.map((r) => r.host).filter(Boolean).join(', ')}</code>
            ) : (
              <Dash />
            )
          ),
        },
        { key: 'load_balancer', label: 'Load balancer', render: (i) => <LoadBalancerAddresses entries={i.load_balancer} /> },
      ]}
    />
  );
}

export function PersistentVolumes() {
  const clustersState = useResource(() => fetchAllClusters(), []);
  const clustersById = useMemo(() => {
    if (clustersState.status !== 'ready') return new Map<string, api.Cluster>();
    return new Map(clustersState.data.map((c) => [c.id, c]));
  }, [clustersState]);
  return (
    <EntityListPage<api.PersistentVolume>
      title="Persistent Volumes"
      icon={<VolumeIcon size={20} />}
      storageKey="lists.persistent_volumes"
      emptyMessage="No persistent volumes found. PVs are collected cluster-wide by the collector."
      fetchPage={(params, cursor, limit) => api.listPersistentVolumes({ ...params, cursor, limit })}
      rowKey={(pv) => pv.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (pv) => (
            <Link to={`/persistentvolumes/${pv.id}`}>
              <strong>{pv.name}</strong>
            </Link>
          ),
        },
        {
          key: 'cluster',
          label: 'Cluster',
          render: (pv) => {
            const cluster = clustersById.get(pv.cluster_id);
            return cluster ? (
              <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
            ) : (
              <IdLink to={`/clusters/${pv.cluster_id}`} id={pv.cluster_id} />
            );
          },
        },
        { key: 'capacity', label: 'Capacity', sortKey: 'capacity', render: (pv) => pv.capacity ? <code>{pv.capacity}</code> : <Dash /> },
        { key: 'storage_class', label: 'Storage class', sortKey: 'storage_class_name', render: (pv) => pv.storage_class_name || <Dash /> },
        { key: 'csi_driver', label: 'CSI driver', sortKey: 'csi_driver', render: (pv) => pv.csi_driver ? <code>{pv.csi_driver}</code> : <Dash /> },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (pv) => pv.phase || <Dash /> },
      ]}
    />
  );
}

export function PersistentVolumeClaims() {
  return (
    <EntityListPage<api.PersistentVolumeClaim>
      title="Persistent Volume Claims"
      icon={<VolumeIcon size={20} />}
      storageKey="lists.persistent_volume_claims"
      emptyMessage="No persistent volume claims found. PVCs are collected from all namespaces."
      fetchPage={(params, cursor, limit) => api.listPersistentVolumeClaims({ ...params, cursor, limit })}
      rowKey={(pvc) => pvc.id}
      columns={[
        {
          key: 'name',
          label: 'Name',
          sortKey: 'name',
          render: (pvc) => (
            <Link to={`/persistentvolumeclaims/${pvc.id}`}>
              <strong>{pvc.name}</strong>
            </Link>
          ),
        },
        {
          key: 'namespace',
          label: 'Namespace',
          render: (pvc) => (
            <NamespaceLink
              namespaceId={pvc.namespace_id}
              namespaceName={pvc.namespace_name}
              clusterId={pvc.cluster_id}
              clusterName={pvc.cluster_name}
            />
          ),
        },
        { key: 'phase', label: 'Phase', sortKey: 'phase', render: (pvc) => pvc.phase || <Dash /> },
        { key: 'requested', label: 'Requested', sortKey: 'requested_storage', render: (pvc) => pvc.requested_storage ? <code>{pvc.requested_storage}</code> : <Dash /> },
        { key: 'storage_class', label: 'Storage class', sortKey: 'storage_class_name', render: (pvc) => pvc.storage_class_name || <Dash /> },
        { key: 'bound_pv', label: 'Bound PV', render: (pvc) => pvc.volume_name ? <code>{pvc.volume_name}</code> : <Dash /> },
      ]}
    />
  );
}
