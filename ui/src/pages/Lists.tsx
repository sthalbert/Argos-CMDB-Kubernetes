// One-file home for every top-level list page. All of them use the same
// pattern — usePagedList(listFn) → Paginator → table with a few columns →
// id links through to the detail page. Kept together so adding a new kind
// means editing one file.

import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { usePagedList } from '../hooks';
import { fetchAllPages } from '../lib/paginate';
import { Dash, IdLink, LayerPill, LoadBalancerAddresses, Empty, NamespaceLink, Paginator } from '../components';
import { useEntityTable } from '../components/column_filters';
import {
  ClusterIcon, NodeIcon, NamespaceIcon, WorkloadIcon, PodIcon,
  ServiceIcon, IngressIcon, VolumeIcon,
} from '../icons';

export function Clusters() {
  const list = usePagedList<api.Cluster>(
    (cursor, limit) => api.listClusters({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.clusters');
  return (
    <>
      <h2><ClusterIcon size={20} /> Clusters</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No clusters yet. Connect a collector to start populating your inventory." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Environment</th>
                <th>Provider</th>
                <th>Region</th>
                <th>K8s version</th>
                <th>Layer</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((c) => (
                <tr key={c.id}>
                  <td>
                    <Link to={`/clusters/${c.id}`}>
                      <strong>{c.display_name || c.name}</strong>
                    </Link>
                    {c.display_name && (
                      <div className="muted" style={{ fontSize: '0.8rem' }}>
                        {c.name}
                      </div>
                    )}
                  </td>
                  <td>{c.environment || <Dash />}</td>
                  <td>{c.provider || <Dash />}</td>
                  <td>{c.region || <Dash />}</td>
                  <td>{c.kubernetes_version ? <code>{c.kubernetes_version}</code> : <Dash />}</td>
                  <td><LayerPill layer={c.layer} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

// Lookup map only — not user-facing. Keep fetchAllPages so we have every
// cluster regardless of UI page size.
const fetchAllClusters = () =>
  fetchAllPages((cursor) => api.listClusters({ cursor, limit: 500 })).then((r) => r.items);

export function Nodes() {
  const list = usePagedList<api.Node>(
    (cursor, limit) => api.listNodes({ cursor, limit }),
    [],
  );
  const clusters = useResource(() => fetchAllClusters(), []);
  const clustersById =
    clusters.status === 'ready'
      ? new Map(clusters.data.map((c) => [c.id, c]))
      : new Map<string, api.Cluster>();
  const tableRef = useEntityTable('lists.nodes');
  return (
    <>
      <h2><NodeIcon size={20} /> Nodes</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No nodes found. Ensure a collector is running and connected to a cluster." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Cluster</th>
                <th>Role</th>
                <th>Zone</th>
                <th>Instance type</th>
                <th>CPU / Mem</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((n) => {
                const cluster = clustersById.get(n.cluster_id);
                return (
                  <tr key={n.id}>
                    <td>
                      <Link to={`/nodes/${n.id}`}>
                        <strong>{n.display_name || n.name}</strong>
                      </Link>
                    </td>
                    <td>
                      {cluster ? (
                        <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
                      ) : (
                        <IdLink to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />
                      )}
                    </td>
                    <td>{n.role ? <span className="pill">{n.role}</span> : <Dash />}</td>
                    <td>{n.zone ? <code>{n.zone}</code> : <Dash />}</td>
                    <td>{n.instance_type ? <code>{n.instance_type}</code> : <Dash />}</td>
                    <td>
                      {n.capacity_cpu || n.capacity_memory ? (
                        <code>{n.capacity_cpu || '?'} / {n.capacity_memory || '?'}</code>
                      ) : (
                        <Dash />
                      )}
                    </td>
                    <td>
                      <NodeStatusBadge ready={n.ready} unschedulable={n.unschedulable} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
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
  const list = usePagedList<api.Namespace>(
    (cursor, limit) => api.listNamespaces({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.namespaces');
  return (
    <>
      <h2><NamespaceIcon size={20} /> Namespaces</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No namespaces found. They are collected automatically from your clusters." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Cluster</th>
                <th>Phase</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((n) => (
                <tr key={n.id}>
                  <td>
                    <Link to={`/namespaces/${n.id}`}>
                      <strong>{n.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <Link to={`/clusters/${n.cluster_id}`}>
                      {n.cluster_name ?? <span title="cluster row missing">(orphan)</span>}
                    </Link>
                  </td>
                  <td>{n.phase || <Dash />}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function Workloads() {
  const list = usePagedList<api.Workload>(
    (cursor, limit) => api.listWorkloads({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.workloads');

  return (
    <>
      <h2><WorkloadIcon size={20} /> Workloads</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No workloads found. Deployments, StatefulSets and DaemonSets will appear here once collected." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Kind</th>
                <th>Namespace</th>
                <th>Replicas</th>
                <th>Containers</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((w) => (
                <tr key={w.id}>
                  <td>
                    <Link to={`/workloads/${w.id}`}>
                      <strong>{w.name}</strong>
                    </Link>
                  </td>
                  <td><span className="pill">{w.kind}</span></td>
                  <td>
                    <NamespaceLink
                      namespaceId={w.namespace_id}
                      namespaceName={w.namespace_name}
                      clusterId={w.cluster_id}
                      clusterName={w.cluster_name}
                    />
                  </td>
                  <td>
                    {w.ready_replicas ?? '?'}
                    <span className="muted">/{w.replicas ?? '?'}</span>
                  </td>
                  <td>
                    {w.containers?.length ? (
                      <code>{w.containers.map((c) => c.image).join(', ')}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function Pods() {
  const list = usePagedList<api.Pod>(
    (cursor, limit) => api.listPods({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.pods');
  return (
    <>
      <h2><PodIcon size={20} /> Pods</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No pods found. Pods are collected from all connected clusters." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Phase</th>
                <th>Node</th>
                <th>Pod IP</th>
                <th>Workload</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((p) => (
                <tr key={p.id}>
                  <td>
                    <Link to={`/pods/${p.id}`}>
                      <strong>{p.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={p.namespace_id}
                      namespaceName={p.namespace_name}
                      clusterId={p.cluster_id}
                      clusterName={p.cluster_name}
                    />
                  </td>
                  <td>{p.phase || <Dash />}</td>
                  <td>{p.node_name ? <code>{p.node_name}</code> : <Dash />}</td>
                  <td>{p.pod_ip ? <code>{p.pod_ip}</code> : <Dash />}</td>
                  <td>
                    {p.workload_id ? (
                      <Link to={`/workloads/${p.workload_id}`}>
                        {p.workload_name ?? <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />}
                      </Link>
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function Services() {
  const list = usePagedList<api.Service>(
    (cursor, limit) => api.listServices({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.services');
  return (
    <>
      <h2><ServiceIcon size={20} /> Services</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No services found. Kubernetes Services are collected automatically." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Type</th>
                <th>ClusterIP</th>
                <th>Ports</th>
                <th>Load balancer</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link to={`/services/${s.id}`}>
                      <strong>{s.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={s.namespace_id}
                      namespaceName={s.namespace_name}
                      clusterId={s.cluster_id}
                      clusterName={s.cluster_name}
                    />
                  </td>
                  <td><span className="pill">{s.type || 'ClusterIP'}</span></td>
                  <td>{s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />}</td>
                  <td>
                    {s.ports?.length ? (
                      <code>{s.ports.map((p) => `${p.port}/${p.protocol || 'TCP'}`).join(', ')}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                  <td><LoadBalancerAddresses entries={s.load_balancer} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function Ingresses() {
  const list = usePagedList<api.Ingress>(
    (cursor, limit) => api.listIngresses({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.ingresses');
  return (
    <>
      <h2><IngressIcon size={20} /> Ingresses</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No ingresses found. Ingress resources are collected from all namespaces." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Class</th>
                <th>Hosts</th>
                <th>Load balancer</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((i) => (
                <tr key={i.id}>
                  <td>
                    <Link to={`/ingresses/${i.id}`}>
                      <strong>{i.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={i.namespace_id}
                      namespaceName={i.namespace_name}
                      clusterId={i.cluster_id}
                      clusterName={i.cluster_name}
                    />
                  </td>
                  <td>{i.ingress_class_name || <Dash />}</td>
                  <td>
                    {i.rules?.length ? (
                      <code>{i.rules.map((r) => r.host).filter(Boolean).join(', ')}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                  <td><LoadBalancerAddresses entries={i.load_balancer} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function PersistentVolumes() {
  const list = usePagedList<api.PersistentVolume>(
    (cursor, limit) => api.listPersistentVolumes({ cursor, limit }),
    [],
  );
  const clusters = useResource(() => fetchAllClusters(), []);
  const clustersById =
    clusters.status === 'ready'
      ? new Map(clusters.data.map((c) => [c.id, c]))
      : new Map<string, api.Cluster>();
  const tableRef = useEntityTable('lists.persistent_volumes');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volumes</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No persistent volumes found. PVs are collected cluster-wide by the collector." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Cluster</th>
                <th>Capacity</th>
                <th>Storage class</th>
                <th>CSI driver</th>
                <th>Phase</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((pv) => {
                const cluster = clustersById.get(pv.cluster_id);
                return (
                  <tr key={pv.id}>
                    <td>
                      <Link to={`/persistentvolumes/${pv.id}`}>
                        <strong>{pv.name}</strong>
                      </Link>
                    </td>
                    <td>
                      {cluster ? (
                        <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
                      ) : (
                        <IdLink to={`/clusters/${pv.cluster_id}`} id={pv.cluster_id} />
                      )}
                    </td>
                    <td>{pv.capacity ? <code>{pv.capacity}</code> : <Dash />}</td>
                    <td>{pv.storage_class_name || <Dash />}</td>
                    <td>{pv.csi_driver ? <code>{pv.csi_driver}</code> : <Dash />}</td>
                    <td>{pv.phase || <Dash />}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}

export function PersistentVolumeClaims() {
  const list = usePagedList<api.PersistentVolumeClaim>(
    (cursor, limit) => api.listPersistentVolumeClaims({ cursor, limit }),
    [],
  );
  const tableRef = useEntityTable('lists.persistent_volume_claims');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volume Claims</h2>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        <div className="error">Failed to load: {list.error}</div>
      ) : list.items.length === 0 ? (
        <Empty message="No persistent volume claims found. PVCs are collected from all namespaces." />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Namespace</th>
                <th>Phase</th>
                <th>Requested</th>
                <th>Storage class</th>
                <th>Bound PV</th>
              </tr>
            </thead>
            <tbody>
              {list.items.map((pvc) => (
                <tr key={pvc.id}>
                  <td>
                    <Link to={`/persistentvolumeclaims/${pvc.id}`}>
                      <strong>{pvc.name}</strong>
                    </Link>
                  </td>
                  <td>
                    <NamespaceLink
                      namespaceId={pvc.namespace_id}
                      namespaceName={pvc.namespace_name}
                      clusterId={pvc.cluster_id}
                      clusterName={pvc.cluster_name}
                    />
                  </td>
                  <td>{pvc.phase || <Dash />}</td>
                  <td>{pvc.requested_storage ? <code>{pvc.requested_storage}</code> : <Dash />}</td>
                  <td>{pvc.storage_class_name || <Dash />}</td>
                  <td>{pvc.volume_name ? <code>{pvc.volume_name}</code> : <Dash />}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
