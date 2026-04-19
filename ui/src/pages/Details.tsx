// Detail pages for the drill-down chain:
//   Cluster    → namespaces + nodes + PVs in the cluster
//   Namespace  → workloads + pods + services + ingresses + PVCs in the NS
//                (serves "application = namespace" view)
//   Workload   → its pods (via workload_id) + nodes they run on + containers
//                (serves "application = workload" view)
//   Pod        → containers + backlinks to parent workload / namespace
//   Node       → pods on this node grouped by workload (impact analysis)
//
// The general pattern: a header with key/value facts, then sections of
// related assets. Each section uses the list-page table shape so the UX
// feels consistent across the app.

import { Link, useParams } from 'react-router-dom';
import * as api from '../api';
import { useResource, useResources } from '../hooks';
import { AsyncView, Dash, IdLink, KV, Labels, LayerPill, SectionTitle, Empty } from '../components';

// --- Cluster detail -------------------------------------------------------

export function ClusterDetail() {
  const { id = '' } = useParams();
  const state = useResources(
    [
      () => api.getCluster(id),
      () => api.listNodes({ cluster_id: id }),
      () => api.listNamespaces({ cluster_id: id }),
      () => api.listPersistentVolumes({ cluster_id: id }),
    ] as const,
    [id],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/clusters">Clusters</Link> / <span>this cluster</span>
      </div>
      <AsyncView state={state}>
        {([cluster, nodes, namespaces, pvs]) => (
          <>
            <h2>
              {cluster.display_name || cluster.name} <LayerPill layer={cluster.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Name" v={<code>{cluster.name}</code>} />
              <KV k="Environment" v={cluster.environment} />
              <KV k="Provider" v={cluster.provider} />
              <KV k="Region" v={cluster.region} />
              <KV k="K8s version" v={cluster.kubernetes_version && <code>{cluster.kubernetes_version}</code>} />
              <KV k="API endpoint" v={cluster.api_endpoint && <code>{cluster.api_endpoint}</code>} />
              <KV k="Labels" v={<Labels labels={cluster.labels} />} />
            </dl>

            <SectionTitle count={namespaces.items.length}>Namespaces</SectionTitle>
            {namespaces.items.length === 0 ? (
              <Empty message="No namespaces ingested yet." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Phase</th>
                  </tr>
                </thead>
                <tbody>
                  {namespaces.items.map((n) => (
                    <tr key={n.id}>
                      <td>
                        <Link to={`/namespaces/${n.id}`}>
                          <strong>{n.name}</strong>
                        </Link>
                      </td>
                      <td>{n.phase || <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={nodes.items.length}>Nodes</SectionTitle>
            {nodes.items.length === 0 ? (
              <Empty message="No nodes ingested yet." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Kubelet</th>
                    <th>Arch</th>
                  </tr>
                </thead>
                <tbody>
                  {nodes.items.map((n) => (
                    <tr key={n.id}>
                      <td>
                        <Link to={`/nodes/${n.id}`}>
                          <strong>{n.display_name || n.name}</strong>
                        </Link>
                      </td>
                      <td>{n.kubelet_version ? <code>{n.kubelet_version}</code> : <Dash />}</td>
                      <td>{n.architecture || <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={pvs.items.length}>Persistent Volumes</SectionTitle>
            {pvs.items.length === 0 ? (
              <Empty message="No PVs in this cluster." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Capacity</th>
                    <th>Storage class</th>
                    <th>Phase</th>
                  </tr>
                </thead>
                <tbody>
                  {pvs.items.map((pv) => (
                    <tr key={pv.id}>
                      <td>
                        <strong>{pv.name}</strong>
                      </td>
                      <td>{pv.capacity ? <code>{pv.capacity}</code> : <Dash />}</td>
                      <td>{pv.storage_class_name || <Dash />}</td>
                      <td>{pv.phase || <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
    </>
  );
}

// --- Namespace detail -----------------------------------------------------

export function NamespaceDetail() {
  const { id = '' } = useParams();
  const state = useResources(
    [
      () => api.getNamespace(id),
      () => api.listWorkloads({ namespace_id: id }),
      () => api.listPods({ namespace_id: id }),
      () => api.listServices({ namespace_id: id }),
      () => api.listIngresses({ namespace_id: id }),
      () => api.listPersistentVolumeClaims({ namespace_id: id }),
    ] as const,
    [id],
  );

  const clusterResult = useResource(async () => {
    if (state.status !== 'ready') return null;
    return api.getCluster(state.data[0].cluster_id);
  }, [state.status === 'ready' ? state.data[0].cluster_id : '']);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/namespaces">Namespaces</Link> /{' '}
        {clusterResult.status === 'ready' && clusterResult.data && (
          <>
            <Link to={`/clusters/${clusterResult.data.id}`}>{clusterResult.data.name}</Link>
            {' / '}
          </>
        )}
        <span>this namespace</span>
      </div>
      <AsyncView state={state}>
        {([ns, workloads, pods, services, ingresses, pvcs]) => (
          <>
            <h2>
              {ns.name} <LayerPill layer={ns.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={ns.phase} />
              <KV k="Labels" v={<Labels labels={ns.labels} />} />
            </dl>

            <p className="impact-callout">
              <strong>All assets in this namespace</strong> — the "application = namespace"
              view. {workloads.items.length} workloads, {pods.items.length} pods,{' '}
              {services.items.length} services, {ingresses.items.length} ingresses,{' '}
              {pvcs.items.length} PVCs.
            </p>

            <SectionTitle count={workloads.items.length}>Workloads</SectionTitle>
            {workloads.items.length === 0 ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Kind</th>
                    <th>Ready</th>
                    <th>Containers</th>
                  </tr>
                </thead>
                <tbody>
                  {workloads.items.map((w) => (
                    <tr key={w.id}>
                      <td>
                        <Link to={`/workloads/${w.id}`}>
                          <strong>{w.name}</strong>
                        </Link>
                      </td>
                      <td>
                        <span className="pill">{w.kind}</span>
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
            )}

            <SectionTitle count={pods.items.length}>Pods</SectionTitle>
            {pods.items.length === 0 ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Phase</th>
                    <th>Node</th>
                    <th>Pod IP</th>
                    <th>Workload</th>
                  </tr>
                </thead>
                <tbody>
                  {pods.items.map((p) => (
                    <tr key={p.id}>
                      <td>
                        <Link to={`/pods/${p.id}`}>
                          <strong>{p.name}</strong>
                        </Link>
                      </td>
                      <td>{p.phase || <Dash />}</td>
                      <td>{p.node_name ? <code>{p.node_name}</code> : <Dash />}</td>
                      <td>{p.pod_ip ? <code>{p.pod_ip}</code> : <Dash />}</td>
                      <td>
                        {p.workload_id ? (
                          <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />
                        ) : (
                          <Dash />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={services.items.length}>Services</SectionTitle>
            {services.items.length === 0 ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Type</th>
                    <th>ClusterIP</th>
                  </tr>
                </thead>
                <tbody>
                  {services.items.map((s) => (
                    <tr key={s.id}>
                      <td>
                        <strong>{s.name}</strong>
                      </td>
                      <td>
                        <span className="pill">{s.type || 'ClusterIP'}</span>
                      </td>
                      <td>{s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={ingresses.items.length}>Ingresses</SectionTitle>
            {ingresses.items.length === 0 ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Class</th>
                    <th>Hosts</th>
                  </tr>
                </thead>
                <tbody>
                  {ingresses.items.map((i) => (
                    <tr key={i.id}>
                      <td>
                        <strong>{i.name}</strong>
                      </td>
                      <td>{i.ingress_class_name || <Dash />}</td>
                      <td>
                        {i.rules?.length ? (
                          <code>
                            {i.rules
                              .map((r) => r.host)
                              .filter(Boolean)
                              .join(', ')}
                          </code>
                        ) : (
                          <Dash />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={pvcs.items.length}>Persistent Volume Claims</SectionTitle>
            {pvcs.items.length === 0 ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Phase</th>
                    <th>Requested</th>
                    <th>Bound PV</th>
                  </tr>
                </thead>
                <tbody>
                  {pvcs.items.map((pvc) => (
                    <tr key={pvc.id}>
                      <td>
                        <strong>{pvc.name}</strong>
                      </td>
                      <td>{pvc.phase || <Dash />}</td>
                      <td>{pvc.requested_storage ? <code>{pvc.requested_storage}</code> : <Dash />}</td>
                      <td>{pvc.volume_name ? <code>{pvc.volume_name}</code> : <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
    </>
  );
}

// --- Workload detail ------------------------------------------------------

export function WorkloadDetail() {
  const { id = '' } = useParams();
  const state = useResources(
    [() => api.getWorkload(id), () => api.listPods({ namespace_id: undefined })] as const,
    [id],
  );
  // We fetch ALL pods and filter by workload_id client-side, since
  // /v1/pods doesn't yet have a workload_id query param. Namespace_id
  // narrowing happens after we know the workload's namespace.

  return (
    <>
      <div className="breadcrumb">
        <Link to="/workloads">Workloads</Link> / <span>this workload</span>
      </div>
      <AsyncView state={state}>
        {([workload, pods]) => {
          const ownedPods = pods.items.filter((p) => p.workload_id === workload.id);
          const nodes = Array.from(new Set(ownedPods.map((p) => p.node_name).filter(Boolean))) as string[];
          return (
            <>
              <h2>
                {workload.name} <LayerPill layer={workload.layer} />
              </h2>
              <dl className="kv-list">
                <KV k="Kind" v={<span className="pill">{workload.kind}</span>} />
                <KV
                  k="Replicas"
                  v={
                    <>
                      {workload.ready_replicas ?? '?'}
                      <span className="muted">/{workload.replicas ?? '?'}</span>
                    </>
                  }
                />
                <KV
                  k="Namespace"
                  v={<IdLink to={`/namespaces/${workload.namespace_id}`} id={workload.namespace_id} />}
                />
                <KV k="Labels" v={<Labels labels={workload.labels} />} />
              </dl>

              <p className="impact-callout">
                <strong>All assets hosting this application</strong> — the "application ="
                workload" view. {ownedPods.length} pods spread across {nodes.length} node
                {nodes.length === 1 ? '' : 's'}.
              </p>

              <SectionTitle count={workload.containers?.length || 0}>
                Containers (template)
              </SectionTitle>
              {!workload.containers?.length ? (
                <Empty message="None." />
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Image</th>
                      <th>Init</th>
                    </tr>
                  </thead>
                  <tbody>
                    {workload.containers.map((c) => (
                      <tr key={c.name}>
                        <td>
                          <strong>{c.name}</strong>
                        </td>
                        <td>
                          <code>{c.image}</code>
                        </td>
                        <td>{c.init ? 'yes' : <Dash />}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              <SectionTitle count={ownedPods.length}>Pods</SectionTitle>
              {ownedPods.length === 0 ? (
                <Empty message="No pods currently point at this workload." />
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Phase</th>
                      <th>Node</th>
                      <th>Pod IP</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ownedPods.map((p) => (
                      <tr key={p.id}>
                        <td>
                          <Link to={`/pods/${p.id}`}>
                            <strong>{p.name}</strong>
                          </Link>
                        </td>
                        <td>{p.phase || <Dash />}</td>
                        <td>
                          {p.node_name ? <code>{p.node_name}</code> : <Dash />}
                        </td>
                        <td>{p.pod_ip ? <code>{p.pod_ip}</code> : <Dash />}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              <SectionTitle count={nodes.length}>Nodes running this workload</SectionTitle>
              {nodes.length === 0 ? (
                <Empty message="No pods scheduled yet." />
              ) : (
                <ul className="node-list">
                  {nodes.map((n) => (
                    <li key={n}>
                      <code>{n}</code>
                    </li>
                  ))}
                </ul>
              )}
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

// --- Pod detail -----------------------------------------------------------

export function PodDetail() {
  const { id = '' } = useParams();
  const state = useResource(() => api.getPod(id), [id]);
  return (
    <>
      <div className="breadcrumb">
        <Link to="/pods">Pods</Link> / <span>this pod</span>
      </div>
      <AsyncView state={state}>
        {(pod) => (
          <>
            <h2>
              {pod.name} <LayerPill layer={pod.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={pod.phase} />
              <KV k="Node" v={pod.node_name && <code>{pod.node_name}</code>} />
              <KV k="Pod IP" v={pod.pod_ip && <code>{pod.pod_ip}</code>} />
              <KV
                k="Namespace"
                v={<IdLink to={`/namespaces/${pod.namespace_id}`} id={pod.namespace_id} />}
              />
              <KV
                k="Workload"
                v={
                  pod.workload_id ? (
                    <IdLink to={`/workloads/${pod.workload_id}`} id={pod.workload_id} />
                  ) : (
                    <span className="muted">(unmanaged / unknown owner kind)</span>
                  )
                }
              />
              <KV k="Labels" v={<Labels labels={pod.labels} />} />
            </dl>

            <SectionTitle count={pod.containers?.length || 0}>Containers (runtime)</SectionTitle>
            {!pod.containers?.length ? (
              <Empty message="None." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Image</th>
                    <th>Image ID</th>
                    <th>Init</th>
                  </tr>
                </thead>
                <tbody>
                  {pod.containers.map((c) => (
                    <tr key={c.name}>
                      <td>
                        <strong>{c.name}</strong>
                      </td>
                      <td>
                        <code>{c.image}</code>
                      </td>
                      <td>
                        {c.image_id ? (
                          <code style={{ fontSize: '0.75rem' }}>
                            {c.image_id.length > 60 ? c.image_id.slice(0, 60) + '…' : c.image_id}
                          </code>
                        ) : (
                          <Dash />
                        )}
                      </td>
                      <td>{c.init ? 'yes' : <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
    </>
  );
}

// --- Node detail (impact analysis) ---------------------------------------

export function NodeDetail() {
  const { id = '' } = useParams();
  // 1. Fetch the node record itself.
  // 2. We need pods on this node, but we only have node.name (not node_name
  //    is what pod rows carry). Fetch the node first, then filter pods by
  //    node_name. The server-side ?node_name= filter makes this cheap.
  const node = useResource(() => api.getNode(id), [id]);
  const pods = useResource(
    async () => {
      if (node.status !== 'ready') return null;
      return api.listPods({ node_name: node.data.name });
    },
    [node.status === 'ready' ? node.data.name : ''],
  );
  // Also pull all workloads so we can attach name/kind to each pod's
  // workload_id for the impact grouping.
  const workloads = useResource(() => api.listWorkloads(), []);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/nodes">Nodes</Link> / <span>this node</span>
      </div>
      <AsyncView state={node}>
        {(n) => (
          <>
            <h2>
              {n.display_name || n.name} <LayerPill layer={n.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Name" v={<code>{n.name}</code>} />
              <KV k="Cluster" v={<IdLink to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />} />
              <KV k="Kubelet" v={n.kubelet_version && <code>{n.kubelet_version}</code>} />
              <KV k="OS image" v={n.os_image} />
              <KV k="Arch" v={n.architecture} />
              <KV k="Labels" v={<Labels labels={n.labels} />} />
            </dl>

            <AsyncView state={pods}>
              {(p) => {
                if (p === null) return null;
                return (
                  <AsyncView state={workloads}>
                    {(wls) => {
                      const wlById = new Map(wls.items.map((w) => [w.id, w]));
                      // Group pods by workload_id to give "if this node dies,
                      // workload X loses N pods" an immediate visual.
                      const groups = new Map<string, api.Pod[]>();
                      const unowned: api.Pod[] = [];
                      for (const pod of p.items) {
                        if (!pod.workload_id) {
                          unowned.push(pod);
                          continue;
                        }
                        const list = groups.get(pod.workload_id) || [];
                        list.push(pod);
                        groups.set(pod.workload_id, list);
                      }
                      return (
                        <>
                          <p className="impact-callout">
                            <strong>Impact analysis</strong> — if this node goes down,{' '}
                            {p.items.length} pod{p.items.length === 1 ? '' : 's'} across{' '}
                            {groups.size} workload{groups.size === 1 ? '' : 's'}
                            {unowned.length > 0 && ` (+ ${unowned.length} unmanaged)`} are lost.
                          </p>

                          <SectionTitle count={groups.size}>
                            Affected workloads
                          </SectionTitle>
                          {groups.size === 0 ? (
                            <Empty message="No workload-owned pods on this node." />
                          ) : (
                            <table className="entities">
                              <thead>
                                <tr>
                                  <th>Workload</th>
                                  <th>Kind</th>
                                  <th>Pods on this node</th>
                                  <th>Total replicas</th>
                                </tr>
                              </thead>
                              <tbody>
                                {[...groups.entries()].map(([wid, list]) => {
                                  const wl = wlById.get(wid);
                                  return (
                                    <tr key={wid}>
                                      <td>
                                        {wl ? (
                                          <Link to={`/workloads/${wl.id}`}>
                                            <strong>{wl.name}</strong>
                                          </Link>
                                        ) : (
                                          <IdLink to={`/workloads/${wid}`} id={wid} />
                                        )}
                                      </td>
                                      <td>
                                        {wl ? <span className="pill">{wl.kind}</span> : <Dash />}
                                      </td>
                                      <td>
                                        <strong>{list.length}</strong>
                                      </td>
                                      <td>
                                        {wl?.replicas != null ? (
                                          <>
                                            {list.length}
                                            <span className="muted">/{wl.replicas}</span>
                                          </>
                                        ) : (
                                          <Dash />
                                        )}
                                      </td>
                                    </tr>
                                  );
                                })}
                              </tbody>
                            </table>
                          )}

                          <SectionTitle count={p.items.length}>All pods on this node</SectionTitle>
                          {p.items.length === 0 ? (
                            <Empty message="None." />
                          ) : (
                            <table className="entities">
                              <thead>
                                <tr>
                                  <th>Name</th>
                                  <th>Phase</th>
                                  <th>Workload</th>
                                </tr>
                              </thead>
                              <tbody>
                                {p.items.map((pod) => (
                                  <tr key={pod.id}>
                                    <td>
                                      <Link to={`/pods/${pod.id}`}>
                                        <strong>{pod.name}</strong>
                                      </Link>
                                    </td>
                                    <td>{pod.phase || <Dash />}</td>
                                    <td>
                                      {pod.workload_id ? (
                                        <IdLink
                                          to={`/workloads/${pod.workload_id}`}
                                          id={pod.workload_id}
                                        />
                                      ) : (
                                        <Dash />
                                      )}
                                    </td>
                                  </tr>
                                ))}
                              </tbody>
                            </table>
                          )}
                        </>
                      );
                    }}
                  </AsyncView>
                );
              }}
            </AsyncView>
          </>
        )}
      </AsyncView>
    </>
  );
}
