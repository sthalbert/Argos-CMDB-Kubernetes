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

import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import * as api from '../api';
import { useResource, useResources } from '../hooks';
import { useMe, isAdmin } from '../me';
import { ClusterCuratedCard } from './cluster_curated';
import { NamespaceCuratedCard } from './namespace_curated';
import { NodeCuratedCard } from './node_curated';
import { ImpactSection } from './ImpactGraph';
import { ClusterHistory } from './ClusterHistory';
import { LabelsCard } from '../components/inventory/LabelsCard';
import { ContainerVersionBadge } from '../components/ContainerVersionBadge';
import {
  ClusterIcon, NamespaceIcon, NodeIcon, WorkloadIcon, PodIcon, IngressIcon,
  ServiceIcon, VolumeIcon,
} from '../icons';
import {
  AsyncView,
  ClusterLink,
  Dash,
  IdLink,
  KV,
  Labels,
  LayerPill,
  SectionTitle,
  Empty,
  WorkloadLink,
} from '../components';

// Inline status badge used in detail-page h2s. Same colour scheme as the
// list-page NodeStatusBadge (green Ready, orange cordoned, red NotReady)
// but smaller and positioned next to the layer pill.
function NodeStatusInline({
  ready,
  unschedulable,
}: {
  ready?: boolean | null;
  unschedulable?: boolean | null;
}) {
  if (ready === null || ready === undefined) return null;
  const label = ready
    ? unschedulable
      ? 'Ready · Cordoned'
      : 'Ready'
    : 'NotReady';
  const cls = ready ? (unschedulable ? 'status-warn' : 'status-ok') : 'status-bad';
  return <span className={`pill ${cls}`} style={{ fontSize: '0.8rem' }}>{label}</span>;
}

// --- Cluster detail -------------------------------------------------------

type ClusterTab = 'overview' | 'impact' | 'history';

// Simple tab bar used by ClusterDetail. Uses existing design-system tokens.
function TabBar({
  active,
  tabs,
  onChange,
}: {
  active: string;
  tabs: { id: string; label: string }[];
  onChange: (id: string) => void;
}) {
  return (
    <div style={{ display: 'flex', gap: 0, borderBottom: '2px solid var(--border)', marginBottom: '1rem' }}>
      {tabs.map((t) => (
        <button
          key={t.id}
          onClick={() => onChange(t.id)}
          style={{
            background: 'none',
            border: 'none',
            borderBottom: active === t.id ? '2px solid var(--accent)' : '2px solid transparent',
            marginBottom: -2,
            padding: '0.5rem 1rem',
            cursor: 'pointer',
            color: active === t.id ? 'var(--accent)' : 'var(--fg-muted)',
            fontWeight: active === t.id ? 600 : 400,
            fontSize: '0.9rem',
          }}
        >
          {t.label}
        </button>
      ))}
    </div>
  );
}

export function ClusterDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const [deleting, setDeleting] = useState(false);
  const [activeTab, setActiveTab] = useState<ClusterTab>('overview');
  const reload = () => setNonce((n) => n + 1);
  const state = useResources(
    [
      () => api.getCluster(id),
      () => api.listNodes({ cluster_id: id }),
      () => api.listNamespaces({ cluster_id: id }),
      () => api.listPersistentVolumes({ cluster_id: id }),
    ] as const,
    [id, nonce],
  );

  const handleDelete = async (cluster: api.Cluster, childCount: number) => {
    const typed = prompt(
      `This will permanently delete cluster "${cluster.name}" and all its ${childCount} child resources.\n\nType the cluster name to confirm:`,
    );
    if (typed === null) return; // cancelled
    if (typed !== cluster.name) {
      alert(`Name does not match. Expected "${cluster.name}".`);
      return;
    }
    setDeleting(true);
    try {
      await api.deleteCluster(cluster.id);
      navigate('/clusters', { replace: true });
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
      setDeleting(false);
    }
  };

  return (
    <>
      <div className="breadcrumb">
        <Link to="/clusters">Clusters</Link> / <span>this cluster</span>
      </div>
      <AsyncView state={state}>
        {([cluster, nodes, namespaces, pvs]) => {
          const childCount =
            nodes.items.length + namespaces.items.length + pvs.items.length;
          return (
          <>
            <h2>
              <ClusterIcon size={20} /> {cluster.display_name || cluster.name} <LayerPill layer={cluster.layer} />
              {isAdmin(me) && (
                <button
                  className="danger"
                  style={{ marginLeft: '1rem', fontSize: '0.85rem' }}
                  disabled={deleting}
                  onClick={() => handleDelete(cluster, childCount)}
                >
                  {deleting ? 'Deleting…' : 'Delete cluster'}
                </button>
              )}
            </h2>

            <TabBar
              active={activeTab}
              tabs={[
                { id: 'overview', label: 'Overview' },
                { id: 'impact',   label: 'Impact' },
                { id: 'history',  label: 'History' },
              ]}
              onChange={(t) => setActiveTab(t as ClusterTab)}
            />

            {activeTab === 'overview' && (
              <>
                <dl className="kv-list">
                  <KV k="Name" v={<code>{cluster.name}</code>} />
                  <KV k="Environment" v={cluster.environment} />
                  <KV k="Provider" v={cluster.provider} />
                  <KV k="Region" v={cluster.region} />
                  <KV k="K8s version" v={cluster.kubernetes_version && <code>{cluster.kubernetes_version}</code>} />
                  <KV k="API endpoint" v={cluster.api_endpoint && <code>{cluster.api_endpoint}</code>} />
                  <KV k="Labels" v={<Labels labels={cluster.labels} />} />
                </dl>

                <ClusterCuratedCard cluster={cluster} onSaved={reload} />

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
                            <Link to={`/persistentvolumes/${pv.id}`}>
                              <strong>{pv.name}</strong>
                            </Link>
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

            {activeTab === 'impact' && <ImpactSection entityType="clusters" entityId={id} />}

            {activeTab === 'history' && <ClusterHistory clusterId={id} />}
          </>
          );
        }}
      </AsyncView>
    </>
  );
}

// --- Namespace detail -----------------------------------------------------

export function NamespaceDetail() {
  const { id = '' } = useParams();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const state = useResources(
    [
      () => api.getNamespace(id),
      () => api.listWorkloads({ namespace_id: id }),
      () => api.listPods({ namespace_id: id }),
      () => api.listServices({ namespace_id: id }),
      () => api.listIngresses({ namespace_id: id }),
      () => api.listPersistentVolumeClaims({ namespace_id: id }),
    ] as const,
    [id, nonce],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/namespaces">Namespaces</Link> /{' '}
        {state.status === 'ready' && state.data[0].cluster_id && (
          <>
            <ClusterLink
              clusterId={state.data[0].cluster_id}
              clusterName={state.data[0].cluster_name}
            />
            {' / '}
          </>
        )}
        <span>this namespace</span>
      </div>
      <AsyncView state={state}>
        {([ns, workloads, pods, services, ingresses, pvcs]) => (
          <>
            <h2>
              <NamespaceIcon size={20} /> {ns.name} <LayerPill layer={ns.layer} />
            </h2>
            <dl className="kv-list">
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={ns.cluster_id} clusterName={ns.cluster_name} />}
              />
              <KV k="Phase" v={ns.phase} />
              <KV k="Labels" v={<Labels labels={ns.labels} />} />
            </dl>

            <NamespaceCuratedCard namespace={ns} onSaved={reload} />

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
              (() => {
                // Build a workload_id -> workload lookup once per render
                // so each pod row can resolve its workload name without
                // a separate network call.
                const wlByID = new Map(workloads.items.map((w) => [w.id, w]));
                return (
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
                      {pods.items.map((p) => {
                        const wl = p.workload_id ? wlByID.get(p.workload_id) : undefined;
                        return (
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
                              {wl ? (
                                <Link to={`/workloads/${wl.id}`}>
                                  <strong>{wl.name}</strong>
                                  {wl.kind && (
                                    <span className="muted" style={{ marginLeft: '0.4rem', fontSize: '0.8rem' }}>
                                      {wl.kind}
                                    </span>
                                  )}
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
                );
              })()
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
                        <Link to={`/services/${s.id}`}>
                          <strong>{s.name}</strong>
                        </Link>
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
                        <Link to={`/ingresses/${i.id}`}>
                          <strong>{i.name}</strong>
                        </Link>
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
                        <Link to={`/persistentvolumeclaims/${pvc.id}`}>
                          <strong>{pvc.name}</strong>
                        </Link>
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
      <ImpactSection entityType="namespaces" entityId={id} />
    </>
  );
}

// --- Workload detail ------------------------------------------------------

export function WorkloadDetail() {
  const { id = '' } = useParams();
  const workloadState = useResource(() => api.getWorkload(id), [id]);
  // Fetch pods server-side filtered by workload_id so the result set
  // is bounded to this workload's pods regardless of cluster size.
  const podsState = useResource(
    () => api.listPods({ workload_id: id }),
    [id],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/workloads">Workloads</Link> /{' '}
        {workloadState.status === 'ready' && workloadState.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={workloadState.data.cluster_id}
              clusterName={workloadState.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {workloadState.status === 'ready' && (
          <>
            <Link to={`/namespaces/${workloadState.data.namespace_id}`}>
              {workloadState.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this workload</span>
      </div>
      <AsyncView state={workloadState}>
        {(workload) => (
          <AsyncView state={podsState}>
            {(pods) => {
          const ownedPods = pods?.items ?? [];
          const nodes = Array.from(new Set(ownedPods.map((p) => p.node_name).filter(Boolean))) as string[];
          return (
            <>
              <h2>
                <WorkloadIcon size={20} /> {workload.name} <LayerPill layer={workload.layer} />
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
                  v={
                    <Link to={`/namespaces/${workload.namespace_id}`}>
                      {workload.namespace_name ?? (
                        <span title="namespace row missing">(orphan)</span>
                      )}
                    </Link>
                  }
                />
                <KV
                  k="Cluster"
                  v={<ClusterLink clusterId={workload.cluster_id} clusterName={workload.cluster_name} />}
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
                      <th>Version</th>
                      <th>Init</th>
                    </tr>
                  </thead>
                  <tbody>
                    {workload.containers.map((c) => {
                      const info = workload.containers_versions?.[c.name]
                      return (
                        <tr key={c.name}>
                          <td>
                            <strong>{c.name}</strong>
                          </td>
                          <td>
                            <code>{c.image}</code>
                          </td>
                          <td><ContainerVersionBadge info={info ?? undefined} /></td>
                          <td>{c.init ? 'yes' : <Dash />}</td>
                        </tr>
                      )
                    })}
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
        )}
      </AsyncView>
      <ImpactSection entityType="workloads" entityId={id} />
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
        <Link to="/pods">Pods</Link> /{' '}
        {state.status === 'ready' && state.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={state.data.cluster_id}
              clusterName={state.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {state.status === 'ready' && (
          <>
            <Link to={`/namespaces/${state.data.namespace_id}`}>
              {state.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this pod</span>
      </div>
      <AsyncView state={state}>
        {(pod) => {
          return (
          <>
            <h2>
              <PodIcon size={20} /> {pod.name} <LayerPill layer={pod.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={pod.phase} />
              <KV k="Node" v={pod.node_name && <code>{pod.node_name}</code>} />
              <KV k="Pod IP" v={pod.pod_ip && <code>{pod.pod_ip}</code>} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${pod.namespace_id}`}>
                    {pod.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Workload"
                v={<WorkloadLink workloadId={pod.workload_id} workloadName={pod.workload_name} />}
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
                    <th>Last version</th>
                    <th>Image ID</th>
                    <th>Init</th>
                  </tr>
                </thead>
                <tbody>
                  {pod.containers.map((c) => {
                    const info = pod.containers_versions?.[c.name]
                    return (
                      <tr key={c.name}>
                        <td>
                          <strong>{c.name}</strong>
                        </td>
                        <td>
                          <code>{c.image}</code>
                        </td>
                        <td>
                          {info?.latest_tag ? (
                            <code
                              className={info.is_behind ? 'pill status-bad' : 'pill status-ok'}
                              title={
                                info.is_behind
                                  ? `Behind: latest available is ${info.latest_tag} (checked ${new Date(info.last_checked_at).toLocaleString()})`
                                  : `Up to date (checked ${new Date(info.last_checked_at).toLocaleString()})`
                              }
                            >
                              {info.latest_tag}
                            </code>
                          ) : (
                            <ContainerVersionBadge info={info ?? undefined} />
                          )}
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
                    )
                  })}
                </tbody>
              </table>
            )}
          </>
          );
        }}
      </AsyncView>
      <ImpactSection entityType="pods" entityId={id} />
    </>
  );
}

// --- Node detail (impact analysis) ---------------------------------------

export function NodeDetail() {
  const { id = '' } = useParams();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  // 1. Fetch the node record itself.
  // 2. We need pods on this node, but we only have node.name (not node_name
  //    is what pod rows carry). Fetch the node first, then filter pods by
  //    node_name. The server-side ?node_name= filter makes this cheap.
  const node = useResource(() => api.getNode(id), [id, nonce]);
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
        <Link to="/nodes">Nodes</Link> /{' '}
        {node.status === 'ready' && (
          <>
            <ClusterLink clusterId={node.data.cluster_id} clusterName={node.data.cluster_name} />
            {' / '}
          </>
        )}
        <span>this node</span>
      </div>
      <AsyncView state={node}>
        {(n) => (
          <>
            <h2>
              <NodeIcon size={20} /> {n.display_name || n.name} <LayerPill layer={n.layer} />{' '}
              <NodeStatusInline ready={n.ready} unschedulable={n.unschedulable} />
            </h2>

            <SectionTitle>Identity</SectionTitle>
            <dl className="kv-list">
              <KV k="Name" v={<code>{n.name}</code>} />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={n.cluster_id} clusterName={n.cluster_name} />}
              />
              <KV k="Role" v={n.role && <span className="pill">{n.role}</span>} />
              <KV k="Provider ID" v={n.provider_id && <code>{n.provider_id}</code>} />
              <KV k="Instance type" v={n.instance_type && <code>{n.instance_type}</code>} />
              <KV k="Zone" v={n.zone && <code>{n.zone}</code>} />
            </dl>

            <NodeCuratedCard node={n} onSaved={reload} />

            <SectionTitle>OS &amp; runtime</SectionTitle>
            <dl className="kv-list">
              <KV k="Kubelet" v={n.kubelet_version && <code>{n.kubelet_version}</code>} />
              <KV k="Kube-proxy" v={n.kube_proxy_version && <code>{n.kube_proxy_version}</code>} />
              <KV k="Container runtime" v={n.container_runtime_version && <code>{n.container_runtime_version}</code>} />
              <KV k="OS image" v={n.os_image} />
              <KV k="Operating system" v={n.operating_system} />
              <KV k="Kernel" v={n.kernel_version && <code>{n.kernel_version}</code>} />
              <KV k="Architecture" v={n.architecture} />
            </dl>

            <SectionTitle>Networking</SectionTitle>
            <dl className="kv-list">
              <KV k="Internal IP" v={n.internal_ip && <code>{n.internal_ip}</code>} />
              <KV k="External IP" v={n.external_ip && <code>{n.external_ip}</code>} />
              <KV k="Pod CIDR" v={n.pod_cidr && <code>{n.pod_cidr}</code>} />
            </dl>

            <SectionTitle>Resources</SectionTitle>
            <table className="entities">
              <thead>
                <tr>
                  <th>Dimension</th>
                  <th>Capacity</th>
                  <th>Allocatable</th>
                </tr>
              </thead>
              <tbody>
                <tr>
                  <td>CPU</td>
                  <td>{n.capacity_cpu ? <code>{n.capacity_cpu}</code> : <Dash />}</td>
                  <td>{n.allocatable_cpu ? <code>{n.allocatable_cpu}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Memory</td>
                  <td>{n.capacity_memory ? <code>{n.capacity_memory}</code> : <Dash />}</td>
                  <td>{n.allocatable_memory ? <code>{n.allocatable_memory}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Pods</td>
                  <td>{n.capacity_pods ? <code>{n.capacity_pods}</code> : <Dash />}</td>
                  <td>{n.allocatable_pods ? <code>{n.allocatable_pods}</code> : <Dash />}</td>
                </tr>
                <tr>
                  <td>Ephemeral storage</td>
                  <td>
                    {n.capacity_ephemeral_storage ? <code>{n.capacity_ephemeral_storage}</code> : <Dash />}
                  </td>
                  <td>
                    {n.allocatable_ephemeral_storage ? (
                      <code>{n.allocatable_ephemeral_storage}</code>
                    ) : (
                      <Dash />
                    )}
                  </td>
                </tr>
              </tbody>
            </table>

            <SectionTitle count={n.conditions?.length || 0}>Conditions</SectionTitle>
            {!n.conditions?.length ? (
              <Empty message="No conditions reported." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Type</th>
                    <th>Status</th>
                    <th>Reason</th>
                    <th>Message</th>
                  </tr>
                </thead>
                <tbody>
                  {n.conditions.map((c) => {
                    const healthy =
                      (c.type === 'Ready' && c.status === 'True') ||
                      (c.type !== 'Ready' && c.status === 'False');
                    return (
                      <tr key={c.type}>
                        <td>
                          <strong>{c.type}</strong>
                        </td>
                        <td>
                          <span className={`pill ${healthy ? 'status-ok' : 'status-bad'}`}>
                            {c.status}
                          </span>
                        </td>
                        <td>{c.reason || <Dash />}</td>
                        <td>
                          <span className="muted" style={{ fontSize: '0.85rem' }}>
                            {c.message}
                          </span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            )}

            <SectionTitle count={n.taints?.length || 0}>Taints</SectionTitle>
            {!n.taints?.length ? (
              <Empty message="No taints — every pod can schedule here." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Key</th>
                    <th>Value</th>
                    <th>Effect</th>
                  </tr>
                </thead>
                <tbody>
                  {n.taints.map((t, i) => (
                    <tr key={i}>
                      <td>
                        <code>{t.key}</code>
                      </td>
                      <td>{t.value ? <code>{t.value}</code> : <Dash />}</td>
                      <td>
                        <span className="pill">{t.effect}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <LabelsCard labels={n.labels} />

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
                                {p.items.map((pod) => {
                                  const wl = pod.workload_id ? wlById.get(pod.workload_id) : undefined;
                                  return (
                                    <tr key={pod.id}>
                                      <td>
                                        <Link to={`/pods/${pod.id}`}>
                                          <strong>{pod.name}</strong>
                                        </Link>
                                      </td>
                                      <td>{pod.phase || <Dash />}</td>
                                      <td>
                                        {wl ? (
                                          <Link to={`/workloads/${wl.id}`}>
                                            {wl.name}{' '}
                                            <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                                              {wl.kind}
                                            </span>
                                          </Link>
                                        ) : pod.workload_id ? (
                                          <IdLink
                                            to={`/workloads/${pod.workload_id}`}
                                            id={pod.workload_id}
                                          />
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
      <ImpactSection entityType="nodes" entityId={id} />
    </>
  );
}

// --- Ingress detail -------------------------------------------------------
// The LB block is the on-prem answer: whichever controller / VIP
// provisioner fulfills the Ingress (cloud CC, MetalLB, Kube-VIP, hardware
// LB) writes its address into status.loadBalancer.ingress[]. Auditors can
// read it directly without bouncing into kubectl.

export function IngressDetail() {
  const { id = '' } = useParams();
  const ingress = useResource(() => api.getIngress(id), [id]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/ingresses">Ingresses</Link> /{' '}
        {ingress.status === 'ready' && ingress.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={ingress.data.cluster_id}
              clusterName={ingress.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {ingress.status === 'ready' && (
          <>
            <Link to={`/namespaces/${ingress.data.namespace_id}`}>
              {ingress.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this ingress</span>
      </div>
      <AsyncView state={ingress}>
        {(i) => (
          <>
            <h2>
              <IngressIcon size={20} /> {i.name} <LayerPill layer={i.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Ingress class" v={i.ingress_class_name} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${i.namespace_id}`}>
                    {i.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={i.cluster_id} clusterName={i.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={i.labels} />} />
            </dl>

            <SectionTitle count={i.load_balancer?.length || 0}>Load balancer</SectionTitle>
            {!i.load_balancer?.length ? (
              <Empty message="No address reported yet — Pending for cloud-provisioned, or the on-prem controller (MetalLB / Kube-VIP / hardware LB) hasn't fulfilled this ingress." />
            ) : (
              <>
                <p className="impact-callout">
                  <strong>External entry point</strong>. Whichever fulfills this ingress
                  (cloud controller, MetalLB, Kube-VIP, hardware LB) writes its address
                  to <code>status.loadBalancer.ingress[]</code>.
                </p>
                <table className="entities">
                  <thead>
                    <tr>
                      <th>IP</th>
                      <th>Hostname</th>
                      <th>Ports</th>
                    </tr>
                  </thead>
                  <tbody>
                    {i.load_balancer.map((lb, idx) => (
                      <tr key={idx}>
                        <td>{lb.ip ? <code>{lb.ip}</code> : <Dash />}</td>
                        <td>
                          {lb.hostname ? <span className="lb-host">{lb.hostname}</span> : <Dash />}
                        </td>
                        <td>
                          {lb.ports?.length ? (
                            <code>
                              {lb.ports
                                .map((p) => `${p.port}/${p.protocol || 'TCP'}`)
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
              </>
            )}

            <SectionTitle count={i.rules?.length || 0}>Routing rules</SectionTitle>
            {!i.rules?.length ? (
              <Empty message="No rules defined." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Host</th>
                    <th>Paths</th>
                  </tr>
                </thead>
                <tbody>
                  {i.rules.map((r, idx) => (
                    <tr key={idx}>
                      <td>
                        {r.host ? <code>{r.host}</code> : <span className="muted">(catch-all)</span>}
                      </td>
                      <td>
                        {r.paths?.length ? (
                          <ul style={{ margin: 0, paddingLeft: '1.1rem' }}>
                            {r.paths.map((p, pi) => (
                              <li key={pi} style={{ fontSize: '0.85rem' }}>
                                <code>{p.path || '/'}</code>{' '}
                                {p.path_type && (
                                  <span className="muted">({p.path_type})</span>
                                )}
                                {p.backend?.service_name && (
                                  <>
                                    {' → '}
                                    <code>
                                      {p.backend.service_name}:
                                      {p.backend.service_port_number ?? p.backend.service_port_name ?? '?'}
                                    </code>
                                  </>
                                )}
                              </li>
                            ))}
                          </ul>
                        ) : (
                          <Dash />
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={i.tls?.length || 0}>TLS</SectionTitle>
            {!i.tls?.length ? (
              <Empty message="No TLS configured — ingress serves plaintext." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Hosts</th>
                    <th>Secret</th>
                  </tr>
                </thead>
                <tbody>
                  {i.tls.map((t, idx) => (
                    <tr key={idx}>
                      <td>
                        {t.hosts?.length ? <code>{t.hosts.join(', ')}</code> : <Dash />}
                      </td>
                      <td>
                        {t.secret_name ? <code>{t.secret_name}</code> : <Dash />}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="ingresses" entityId={id} />
    </>
  );
}

// --- Service detail -------------------------------------------------------

export function ServiceDetail() {
  const { id = '' } = useParams();
  const service = useResource(() => api.getService(id), [id]);

  return (
    <>
      <div className="breadcrumb">
        <Link to="/services">Services</Link> /{' '}
        {service.status === 'ready' && service.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={service.data.cluster_id}
              clusterName={service.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {service.status === 'ready' && (
          <>
            <Link to={`/namespaces/${service.data.namespace_id}`}>
              {service.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this service</span>
      </div>
      <AsyncView state={service}>
        {(s) => (
          <>
            <h2>
              <ServiceIcon size={20} /> {s.name} <LayerPill layer={s.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Type" v={<span className="pill">{s.type || 'ClusterIP'}</span>} />
              <KV k="ClusterIP" v={s.cluster_ip ? <code>{s.cluster_ip}</code> : <Dash />} />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${s.namespace_id}`}>
                    {s.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={s.cluster_id} clusterName={s.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={s.labels} />} />
            </dl>

            <SectionTitle count={s.ports?.length || 0}>Ports</SectionTitle>
            {!s.ports?.length ? (
              <Empty message="No ports defined." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Port</th>
                    <th>Target</th>
                    <th>Protocol</th>
                    <th>NodePort</th>
                  </tr>
                </thead>
                <tbody>
                  {s.ports.map((p, idx) => (
                    <tr key={idx}>
                      <td>{p.name || <Dash />}</td>
                      <td><code>{p.port}</code></td>
                      <td>{p.target_port ? <code>{p.target_port}</code> : <Dash />}</td>
                      <td>{p.protocol || 'TCP'}</td>
                      <td>{p.node_port ? <code>{p.node_port}</code> : <Dash />}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}

            <SectionTitle count={s.load_balancer?.length || 0}>Load balancer</SectionTitle>
            {!s.load_balancer?.length ? (
              <Empty message="No external address — only Services of type LoadBalancer typically carry entries." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>IP</th>
                    <th>Hostname</th>
                    <th>Ports</th>
                  </tr>
                </thead>
                <tbody>
                  {s.load_balancer.map((lb, idx) => (
                    <tr key={idx}>
                      <td>{lb.ip ? <code>{lb.ip}</code> : <Dash />}</td>
                      <td>
                        {lb.hostname ? <span className="lb-host">{lb.hostname}</span> : <Dash />}
                      </td>
                      <td>
                        {lb.ports?.length ? (
                          <code>
                            {lb.ports
                              .map((p) => `${p.port}/${p.protocol || 'TCP'}`)
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

            <SectionTitle count={s.selector ? Object.keys(s.selector).length : 0}>Selector</SectionTitle>
            {!s.selector || Object.keys(s.selector).length === 0 ? (
              <Empty message="No selector — Service is headless / ExternalName, or backed by manually-managed Endpoints." />
            ) : (
              <Labels labels={s.selector} />
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="services" entityId={id} />
    </>
  );
}

// --- PersistentVolume detail ----------------------------------------------

export function PersistentVolumeDetail() {
  const { id = '' } = useParams();
  const pv = useResource(() => api.getPersistentVolume(id), [id]);
  const cluster = useResource(
    async () => (pv.status === 'ready' ? api.getCluster(pv.data.cluster_id) : null),
    [pv.status === 'ready' ? pv.data.cluster_id : ''],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/persistentvolumes">Persistent Volumes</Link> /{' '}
        {cluster.status === 'ready' && cluster.data && (
          <>
            <Link to={`/clusters/${cluster.data.id}`}>{cluster.data.name}</Link>
            {' / '}
          </>
        )}
        <span>this volume</span>
      </div>
      <AsyncView state={pv}>
        {(v) => (
          <>
            <h2>
              <VolumeIcon size={20} /> {v.name} <LayerPill layer={v.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Capacity" v={v.capacity ? <code>{v.capacity}</code> : <Dash />} />
              <KV k="Phase" v={v.phase || <Dash />} />
              <KV k="Reclaim policy" v={v.reclaim_policy || <Dash />} />
              <KV k="Storage class" v={v.storage_class_name || <Dash />} />
              <KV k="CSI driver" v={v.csi_driver ? <code>{v.csi_driver}</code> : <Dash />} />
              <KV k="Volume handle" v={v.volume_handle ? <code>{v.volume_handle}</code> : <Dash />} />
              <KV
                k="Access modes"
                v={v.access_modes?.length ? <code>{v.access_modes.join(', ')}</code> : <Dash />}
              />
              <KV
                k="Cluster"
                v={<IdLink to={`/clusters/${v.cluster_id}`} id={v.cluster_id} />}
              />
              <KV k="Labels" v={<Labels labels={v.labels} />} />
            </dl>

            <SectionTitle count={v.claim_ref_name ? 1 : 0}>Bound claim</SectionTitle>
            {!v.claim_ref_name ? (
              <Empty message="No claim bound to this volume." />
            ) : (
              <dl className="kv-list">
                <KV k="Namespace" v={v.claim_ref_namespace || <Dash />} />
                <KV k="Name" v={<code>{v.claim_ref_name}</code>} />
              </dl>
            )}
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="persistentvolumes" entityId={id} />
    </>
  );
}

// --- PersistentVolumeClaim detail -----------------------------------------

export function PersistentVolumeClaimDetail() {
  const { id = '' } = useParams();
  const pvc = useResource(() => api.getPersistentVolumeClaim(id), [id]);
  const boundPv = useResource(
    async () =>
      pvc.status === 'ready' && pvc.data.bound_volume_id
        ? api.getPersistentVolume(pvc.data.bound_volume_id)
        : null,
    [pvc.status === 'ready' && pvc.data.bound_volume_id ? pvc.data.bound_volume_id : ''],
  );

  return (
    <>
      <div className="breadcrumb">
        <Link to="/persistentvolumeclaims">Persistent Volume Claims</Link> /{' '}
        {pvc.status === 'ready' && pvc.data.cluster_id && (
          <>
            <ClusterLink
              clusterId={pvc.data.cluster_id}
              clusterName={pvc.data.cluster_name}
            />
            {' / '}
          </>
        )}
        {pvc.status === 'ready' && (
          <>
            <Link to={`/namespaces/${pvc.data.namespace_id}`}>
              {pvc.data.namespace_name ?? (
                <span title="namespace row missing">(orphan)</span>
              )}
            </Link>
            {' / '}
          </>
        )}
        <span>this claim</span>
      </div>
      <AsyncView state={pvc}>
        {(c) => (
          <>
            <h2>
              <VolumeIcon size={20} /> {c.name} <LayerPill layer={c.layer} />
            </h2>
            <dl className="kv-list">
              <KV k="Phase" v={c.phase || <Dash />} />
              <KV
                k="Requested storage"
                v={c.requested_storage ? <code>{c.requested_storage}</code> : <Dash />}
              />
              <KV k="Storage class" v={c.storage_class_name || <Dash />} />
              <KV
                k="Access modes"
                v={c.access_modes?.length ? <code>{c.access_modes.join(', ')}</code> : <Dash />}
              />
              <KV
                k="Bound PV"
                v={
                  boundPv.status === 'ready' && boundPv.data ? (
                    <Link to={`/persistentvolumes/${boundPv.data.id}`}>
                      <strong>{boundPv.data.name}</strong>
                    </Link>
                  ) : c.volume_name ? (
                    <code>{c.volume_name}</code>
                  ) : (
                    <Dash />
                  )
                }
              />
              <KV
                k="Namespace"
                v={
                  <Link to={`/namespaces/${c.namespace_id}`}>
                    {c.namespace_name ?? (
                      <span title="namespace row missing">(orphan)</span>
                    )}
                  </Link>
                }
              />
              <KV
                k="Cluster"
                v={<ClusterLink clusterId={c.cluster_id} clusterName={c.cluster_name} />}
              />
              <KV k="Labels" v={<Labels labels={c.labels} />} />
            </dl>
          </>
        )}
      </AsyncView>
      <ImpactSection entityType="persistentvolumeclaims" entityId={id} />
    </>
  );
}

