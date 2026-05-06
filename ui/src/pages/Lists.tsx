// One-file home for every top-level list page. All of them use the same
// pattern — useResource(listFn) → table with a few columns → id links
// through to the detail page. Kept together so adding a new kind means
// editing one file.

import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView, Dash, IdLink, LayerPill, LoadBalancerAddresses, Empty } from '../components';
import { useEntityTable } from '../components/column_filters';
import {
  ClusterIcon, NodeIcon, NamespaceIcon, WorkloadIcon, PodIcon,
  ServiceIcon, IngressIcon, VolumeIcon,
} from '../icons';

export function Clusters() {
  const state = useResource(() => api.listClusters(), []);
  const tableRef = useEntityTable('lists.clusters');
  return (
    <>
      <h2><ClusterIcon size={20} /> Clusters</h2>
      <AsyncView state={state}>
        {(resp) =>
          resp.items.length === 0 ? (
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
                  {resp.items.map((c) => (
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
          )
        }
      </AsyncView>
    </>
  );
}

export function Nodes() {
  const state = useResource(
    () =>
      Promise.all([api.listNodes(), fetchAllClusters()]).then(([nodes, clItems]) => ({
        nodes: nodes.items,
        clustersById: new Map(clItems.map((c) => [c.id, c])),
      })),
    [],
  );
  const tableRef = useEntityTable('lists.nodes');
  return (
    <>
      <h2><NodeIcon size={20} /> Nodes</h2>
      <AsyncView state={state}>
        {({ nodes, clustersById }) =>
          nodes.length === 0 ? (
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
                  {nodes.map((n) => {
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
          )
        }
      </AsyncView>
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
  const state = useResource(
    () =>
      Promise.all([api.listNamespaces(), fetchAllClusters()]).then(([ns, clItems]) => ({
        namespaces: ns.items,
        clustersById: new Map(clItems.map((c) => [c.id, c])),
      })),
    [],
  );
  const tableRef = useEntityTable('lists.namespaces');
  return (
    <>
      <h2><NamespaceIcon size={20} /> Namespaces</h2>
      <AsyncView state={state}>
        {({ namespaces, clustersById }) =>
          namespaces.length === 0 ? (
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
                  {namespaces.map((n) => {
                    const cluster = clustersById.get(n.cluster_id);
                    return (
                      <tr key={n.id}>
                        <td>
                          <Link to={`/namespaces/${n.id}`}>
                            <strong>{n.name}</strong>
                          </Link>
                        </td>
                        <td>
                          {cluster ? (
                            <Link to={`/clusters/${cluster.id}`}>{cluster.name}</Link>
                          ) : (
                            <IdLink to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />
                          )}
                        </td>
                        <td>{n.phase || <Dash />}</td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          )
        }
      </AsyncView>
    </>
  );
}

// Shared helper: fetch namespaces + clusters so a list of namespace-scoped
// rows can show "cluster / namespace" breadcrumbs without per-row calls.
async function fetchAllPaged<T>(
  fetchPage: (cursor?: string) => Promise<api.PagedResponse<T>>,
): Promise<T[]> {
  const out: T[] = [];
  let cursor: string | undefined = undefined;
  for (let i = 0; i < 200; i++) {
    const page = await fetchPage(cursor);
    out.push(...page.items);
    if (!page.next_cursor) break;
    cursor = page.next_cursor;
  }
  return out;
}

const fetchAllNamespaces = () => fetchAllPaged((cursor) => api.listNamespaces({ cursor, limit: 500 }));
const fetchAllClusters = () => fetchAllPaged((cursor) => api.listClusters({ cursor, limit: 500 }));
const fetchAllWorkloads = () => fetchAllPaged((cursor) => api.listWorkloads({ cursor, limit: 500 }));

function useNamespaceIndex() {
  return useResource(
    () =>
      Promise.all([fetchAllNamespaces(), fetchAllClusters()]).then(([nsItems, clItems]) => ({
        namespacesById: new Map(nsItems.map((n) => [n.id, n])),
        clustersById: new Map(clItems.map((c) => [c.id, c])),
      })),
    [],
  );
}

function NamespaceLink({
  namespaceId,
  namespacesById,
  clustersById,
}: {
  namespaceId: string;
  namespacesById: Map<string, api.Namespace>;
  clustersById: Map<string, api.Cluster>;
}) {
  const ns = namespacesById.get(namespaceId);
  if (!ns) return <IdLink to={`/namespaces/${namespaceId}`} id={namespaceId} />;
  const cluster = clustersById.get(ns.cluster_id);
  return (
    <>
      {cluster && (
        <Link to={`/clusters/${cluster.id}`} className="muted">
          {cluster.name}
        </Link>
      )}
      {cluster && <span className="muted"> / </span>}
      <Link to={`/namespaces/${ns.id}`}>{ns.name}</Link>
    </>
  );
}

export function Workloads() {
  const index = useNamespaceIndex();
  const workloads = useResource(() => api.listWorkloads(), []);
  const tableRef = useEntityTable('lists.workloads');

  return (
    <>
      <h2><WorkloadIcon size={20} /> Workloads</h2>
      <AsyncView state={index}>
        {({ namespacesById, clustersById }) => (
          <AsyncView state={workloads}>
            {(resp) =>
              resp.items.length === 0 ? (
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
                      {resp.items.map((w) => (
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
                              namespacesById={namespacesById}
                              clustersById={clustersById}
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
              )
            }
          </AsyncView>
        )}
      </AsyncView>
    </>
  );
}

export function Pods() {
  const index = useNamespaceIndex();
  const pods = useResource(() => api.listPods(), []);
  const workloads = useResource(() => fetchAllWorkloads(), []);
  const tableRef = useEntityTable('lists.pods');
  return (
    <>
      <h2><PodIcon size={20} /> Pods</h2>
      <AsyncView state={index}>
        {({ namespacesById, clustersById }) => (
          <AsyncView state={pods}>
            {(resp) => (
              <AsyncView state={workloads}>
                {(wlItems) => {
                  const wlById = new Map(wlItems.map((w) => [w.id, w]));
                  return resp.items.length === 0 ? (
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
                          {resp.items.map((p) => {
                            const wl = p.workload_id ? wlById.get(p.workload_id) : undefined;
                            return (
                              <tr key={p.id}>
                                <td>
                                  <Link to={`/pods/${p.id}`}>
                                    <strong>{p.name}</strong>
                                  </Link>
                                </td>
                                <td>
                                  <NamespaceLink
                                    namespaceId={p.namespace_id}
                                    namespacesById={namespacesById}
                                    clustersById={clustersById}
                                  />
                                </td>
                                <td>{p.phase || <Dash />}</td>
                                <td>{p.node_name ? <code>{p.node_name}</code> : <Dash />}</td>
                                <td>{p.pod_ip ? <code>{p.pod_ip}</code> : <Dash />}</td>
                                <td>
                                  {wl ? (
                                    <Link to={`/workloads/${wl.id}`}>
                                      {wl.name}{' '}
                                      <span className="muted" style={{ fontSize: '0.8rem' }}>{wl.kind}</span>
                                    </Link>
                                  ) : p.workload_id ? (
                                    <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />
                                  ) : (
                                    <Dash />
                                  )}
                                </td>
                              </tr>
                            );
                          })}
                        </tbody>
                      </table>
                    </div>
                  );
                }}
              </AsyncView>
            )}
          </AsyncView>
        )}
      </AsyncView>
    </>
  );
}

export function Services() {
  const index = useNamespaceIndex();
  const services = useResource(() => api.listServices(), []);
  const tableRef = useEntityTable('lists.services');
  return (
    <>
      <h2><ServiceIcon size={20} /> Services</h2>
      <AsyncView state={index}>
        {({ namespacesById, clustersById }) => (
          <AsyncView state={services}>
            {(resp) =>
              resp.items.length === 0 ? (
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
                      {resp.items.map((s) => (
                        <tr key={s.id}>
                          <td><strong>{s.name}</strong></td>
                          <td>
                            <NamespaceLink
                              namespaceId={s.namespace_id}
                              namespacesById={namespacesById}
                              clustersById={clustersById}
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
              )
            }
          </AsyncView>
        )}
      </AsyncView>
    </>
  );
}

export function Ingresses() {
  const index = useNamespaceIndex();
  const ingresses = useResource(() => api.listIngresses(), []);
  const tableRef = useEntityTable('lists.ingresses');
  return (
    <>
      <h2><IngressIcon size={20} /> Ingresses</h2>
      <AsyncView state={index}>
        {({ namespacesById, clustersById }) => (
          <AsyncView state={ingresses}>
            {(resp) =>
              resp.items.length === 0 ? (
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
                      {resp.items.map((i) => (
                        <tr key={i.id}>
                          <td>
                            <Link to={`/ingresses/${i.id}`}>
                              <strong>{i.name}</strong>
                            </Link>
                          </td>
                          <td>
                            <NamespaceLink
                              namespaceId={i.namespace_id}
                              namespacesById={namespacesById}
                              clustersById={clustersById}
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
              )
            }
          </AsyncView>
        )}
      </AsyncView>
    </>
  );
}

export function PersistentVolumes() {
  const state = useResource(
    () =>
      Promise.all([api.listPersistentVolumes(), api.listClusters()]).then(([pvs, clusters]) => ({
        pvs: pvs.items,
        clustersById: new Map(clusters.items.map((c) => [c.id, c])),
      })),
    [],
  );
  const tableRef = useEntityTable('lists.persistent_volumes');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volumes</h2>
      <AsyncView state={state}>
        {({ pvs, clustersById }) =>
          pvs.length === 0 ? (
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
                  {pvs.map((pv) => {
                    const cluster = clustersById.get(pv.cluster_id);
                    return (
                      <tr key={pv.id}>
                        <td><strong>{pv.name}</strong></td>
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
          )
        }
      </AsyncView>
    </>
  );
}

export function PersistentVolumeClaims() {
  const index = useNamespaceIndex();
  const pvcs = useResource(() => api.listPersistentVolumeClaims(), []);
  const tableRef = useEntityTable('lists.persistent_volume_claims');
  return (
    <>
      <h2><VolumeIcon size={20} /> Persistent Volume Claims</h2>
      <AsyncView state={index}>
        {({ namespacesById, clustersById }) => (
          <AsyncView state={pvcs}>
            {(resp) =>
              resp.items.length === 0 ? (
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
                      {resp.items.map((pvc) => (
                        <tr key={pvc.id}>
                          <td><strong>{pvc.name}</strong></td>
                          <td>
                            <NamespaceLink
                              namespaceId={pvc.namespace_id}
                              namespacesById={namespacesById}
                              clustersById={clustersById}
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
              )
            }
          </AsyncView>
        )}
      </AsyncView>
    </>
  );
}
