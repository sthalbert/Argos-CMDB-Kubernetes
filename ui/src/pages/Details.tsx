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
import { LabelsCard } from '../components/inventory/LabelsCard';
// Icons are imported by list pages; not needed in detail pages (PageHead handles titles).
import {
  AsyncView,
  Dash,
  IdLink,
  Labels,
  LayerPill,
  SectionTitle,
  Empty,
} from '../components';
import { Breadcrumb } from '../components/lv/Breadcrumb';
import { PageHead } from '../components/lv/PageHead';
import { StatRow, Stat } from '../components/lv/StatRow';
import { Callout } from '../components/lv/Callout';
import { Pill } from '../components/lv/Pill';
import { KvList } from '../components/lv/KvList';

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

export function ClusterDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const [deleting, setDeleting] = useState(false);
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
      <AsyncView state={state}>
        {([cluster, nodes, namespaces, pvs]) => {
          const childCount =
            nodes.items.length + namespaces.items.length + pvs.items.length;
          return (
          <>
            <Breadcrumb parts={[{ label: 'Clusters', to: '/clusters', ariaLabel: 'Back to clusters' }, { label: cluster.name }]} />
            <PageHead
              title={cluster.display_name || cluster.name}
              sub={cluster.kubernetes_version ?? undefined}
              actions={<>
                {cluster.criticality && <Pill status="accent">{cluster.criticality}</Pill>}
                {cluster.environment && <Pill>{cluster.environment}</Pill>}
                <LayerPill layer={cluster.layer} />
                {isAdmin(me) && (
                  <button
                    className="lv-btn lv-btn-danger"
                    style={{ fontSize: '0.85rem' }}
                    disabled={deleting}
                    onClick={() => handleDelete(cluster, childCount)}
                  >
                    {deleting ? 'Deleting…' : 'Delete cluster'}
                  </button>
                )}
              </>}
            />

            <StatRow>
              <Stat label="Nodes" value={nodes.items.length} />
              <Stat label="Namespaces" value={namespaces.items.length} />
              <Stat label="PVs" value={pvs.items.length} />
            </StatRow>

            <Callout title="Impact analysis">Review what depends on this cluster before any change.</Callout>

            <div className="detail-grid">
              <div>
                <KvList items={[
                  ['Name', <code key="name">{cluster.name}</code>],
                  ['Environment', cluster.environment ?? '—'],
                  ['Provider', cluster.provider ?? '—'],
                  ['Region', cluster.region ?? '—'],
                  ['K8s version', cluster.kubernetes_version ? <code key="k8s">{cluster.kubernetes_version}</code> : '—'],
                  ['API endpoint', cluster.api_endpoint ? <code key="ep">{cluster.api_endpoint}</code> : '—'],
                  ['Owner', cluster.owner ?? '—'],
                  ['Criticality', cluster.criticality ?? '—'],
                  ['Runbook', cluster.runbook_url ? <a key="rb" href={cluster.runbook_url}>{cluster.runbook_url}</a> : '—'],
                ]} />
                <ClusterCuratedCard cluster={cluster} onSaved={reload} />
              </div>
              <div>
                <ImpactSection entityType="clusters" entityId={id} />
              </div>
            </div>

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

  const clusterResult = useResource(async () => {
    if (state.status !== 'ready') return null;
    return api.getCluster(state.data[0].cluster_id);
  }, [state.status === 'ready' ? state.data[0].cluster_id : '']);

  return (
    <AsyncView state={state}>
      {([ns, workloads, pods, services, ingresses, pvcs]) => (
        <>
          <Breadcrumb parts={[
            { label: 'Namespaces', to: '/namespaces', ariaLabel: 'Back to namespaces' },
            ...(clusterResult.status === 'ready' && clusterResult.data
              ? [{ label: clusterResult.data.name, to: `/clusters/${clusterResult.data.id}` }]
              : []),
            { label: ns.name },
          ]} />
          <PageHead
            title={ns.display_name || ns.name}
            sub={ns.phase ?? undefined}
            actions={<>
              {ns.criticality && <Pill status="accent">{ns.criticality}</Pill>}
              <LayerPill layer={ns.layer} />
            </>}
          />

          <StatRow>
            <Stat label="Workloads" value={workloads.items.length} />
            <Stat label="Pods" value={pods.items.length} />
            <Stat label="Services" value={services.items.length} />
            <Stat label="Ingresses" value={ingresses.items.length} />
            <Stat label="PVCs" value={pvcs.items.length} />
          </StatRow>

          <div className="detail-grid">
            <div>
              <KvList items={[
                ['Cluster', clusterResult.status === 'ready' && clusterResult.data
                  ? <Link key="cl" to={`/clusters/${clusterResult.data.id}`}><strong>{clusterResult.data.name}</strong></Link>
                  : <IdLink key="cl" to={`/clusters/${ns.cluster_id}`} id={ns.cluster_id} />
                ],
                ['Phase', ns.phase ?? '—'],
                ['Labels', <Labels key="lbl" labels={ns.labels} />],
              ]} />
              <NamespaceCuratedCard namespace={ns} onSaved={reload} />
            </div>
            <div>
              <ImpactSection entityType="namespaces" entityId={id} />
            </div>
          </div>

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
  const workloadData = workloadState.status === 'ready' ? workloadState.data : null;
  const namespaceResult = useResource(
    async () => (workloadData ? api.getNamespace(workloadData.namespace_id) : null),
    [workloadData?.namespace_id ?? ''],
  );
  const clusterResult = useResource(
    async () =>
      namespaceResult.status === 'ready' && namespaceResult.data
        ? api.getCluster(namespaceResult.data.cluster_id)
        : null,
    [namespaceResult.status === 'ready' && namespaceResult.data ? namespaceResult.data.cluster_id : ''],
  );

  return (
    <AsyncView state={workloadState}>
      {(workload) => (
        <AsyncView state={podsState}>
          {(pods) => {
            const ownedPods = pods?.items ?? [];
            const nodes = Array.from(new Set(ownedPods.map((p) => p.node_name).filter(Boolean))) as string[];
            return (
              <>
                <Breadcrumb parts={[
                  { label: 'Workloads', to: '/workloads', ariaLabel: 'Back to workloads' },
                  ...(clusterResult.status === 'ready' && clusterResult.data
                    ? [{ label: clusterResult.data.name, to: `/clusters/${clusterResult.data.id}` }]
                    : []),
                  ...(namespaceResult.status === 'ready' && namespaceResult.data
                    ? [{ label: namespaceResult.data.name, to: `/namespaces/${namespaceResult.data.id}` }]
                    : []),
                  { label: workload.name },
                ]} />
                <PageHead
                  title={workload.name}
                  sub={workload.kind}
                  actions={<>
                    <LayerPill layer={workload.layer} />
                  </>}
                />

                <StatRow>
                  <Stat label="Pods" value={ownedPods.length} />
                  <Stat label="Nodes" value={nodes.length} />
                  <Stat label="Ready" value={`${workload.ready_replicas ?? '?'}/${workload.replicas ?? '?'}`} />
                </StatRow>

                <div className="detail-grid">
                  <div>
                    <KvList items={[
                      ['Kind', <Pill key="kind">{workload.kind}</Pill>],
                      ['Replicas', <>
                        {workload.ready_replicas ?? '?'}
                        <span className="muted">/{workload.replicas ?? '?'}</span>
                      </>],
                      ['Namespace', namespaceResult.status === 'ready' && namespaceResult.data
                        ? <Link key="ns" to={`/namespaces/${namespaceResult.data.id}`}><strong>{namespaceResult.data.name}</strong></Link>
                        : <IdLink key="ns" to={`/namespaces/${workload.namespace_id}`} id={workload.namespace_id} />
                      ],
                      ['Cluster', clusterResult.status === 'ready' && clusterResult.data
                        ? <Link key="cl" to={`/clusters/${clusterResult.data.id}`}><strong>{clusterResult.data.name}</strong></Link>
                        : '—'
                      ],
                      ['Labels', <Labels key="lbl" labels={workload.labels} />],
                    ]} />
                  </div>
                  <div>
                    <ImpactSection entityType="workloads" entityId={id} />
                  </div>
                </div>

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
      )}
    </AsyncView>
  );
}

// --- Pod detail -----------------------------------------------------------

export function PodDetail() {
  const { id = '' } = useParams();
  const state = useResource(() => api.getPod(id), [id]);
  const podData = state.status === 'ready' ? state.data : null;
  const nsResult = useResource(
    async () => (podData ? api.getNamespace(podData.namespace_id) : null),
    [podData?.namespace_id ?? ''],
  );
  const wlResult = useResource(
    async () => (podData?.workload_id ? api.getWorkload(podData.workload_id) : null),
    [podData?.workload_id ?? ''],
  );
  return (
    <AsyncView state={state}>
      {(pod) => {
        const ns = nsResult.status === 'ready' ? nsResult.data : null;
        const wl = wlResult.status === 'ready' ? wlResult.data : null;
        return (
          <>
            <Breadcrumb parts={[
              { label: 'Pods', to: '/pods', ariaLabel: 'Back to pods' },
              { label: pod.name },
            ]} />
            <PageHead
              title={pod.name}
              sub={pod.phase ?? undefined}
              actions={<LayerPill layer={pod.layer} />}
            />

            <div className="detail-grid">
              <div>
                <KvList items={[
                  ['Phase', pod.phase ?? '—'],
                  ['Node', pod.node_name ? <code key="nd">{pod.node_name}</code> : '—'],
                  ['Pod IP', pod.pod_ip ? <code key="ip">{pod.pod_ip}</code> : '—'],
                  ['Namespace', ns
                    ? <Link key="ns" to={`/namespaces/${ns.id}`}>{ns.display_name || ns.name}</Link>
                    : <IdLink key="ns" to={`/namespaces/${pod.namespace_id}`} id={pod.namespace_id} />
                  ],
                  ['Workload', wl
                    ? <Link key="wl" to={`/workloads/${wl.id}`}>
                        {wl.name} <span className="muted" style={{ fontSize: '0.8rem' }}>{wl.kind}</span>
                      </Link>
                    : pod.workload_id
                      ? <IdLink key="wl" to={`/workloads/${pod.workload_id}`} id={pod.workload_id} />
                      : <span key="wl" className="muted">(unmanaged / unknown owner kind)</span>
                  ],
                  ['Labels', <Labels key="lbl" labels={pod.labels} />],
                ]} />
              </div>
              <div>
                <ImpactSection entityType="pods" entityId={id} />
              </div>
            </div>

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
        );
      }}
    </AsyncView>
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
  // Resolve parent cluster so the Identity row shows its name, not its UUID.
  const clusterResult = useResource(
    async () => (node.status === 'ready' ? api.getCluster(node.data.cluster_id) : null),
    [node.status === 'ready' ? node.data.cluster_id : ''],
  );

  return (
    <AsyncView state={node}>
      {(n) => (
        <>
          <Breadcrumb parts={[
            { label: 'Nodes', to: '/nodes', ariaLabel: 'Back to nodes' },
            { label: n.display_name || n.name },
          ]} />
          <PageHead
            title={n.display_name || n.name}
            sub={n.instance_type ?? n.role ?? undefined}
            actions={<>
              <NodeStatusInline ready={n.ready} unschedulable={n.unschedulable} />
              <LayerPill layer={n.layer} />
            </>}
          />

          <div className="detail-grid">
            <div>
              <SectionTitle>Identity</SectionTitle>
              <KvList items={[
                ['Name', <code key="nm">{n.name}</code>],
                ['Cluster', clusterResult.status === 'ready' && clusterResult.data
                  ? <Link key="cl" to={`/clusters/${clusterResult.data.id}`}><strong>{clusterResult.data.name}</strong></Link>
                  : <IdLink key="cl" to={`/clusters/${n.cluster_id}`} id={n.cluster_id} />
                ],
                ['Role', n.role ? <Pill key="role">{n.role}</Pill> : '—'],
                ['Provider ID', n.provider_id ? <code key="pid">{n.provider_id}</code> : '—'],
                ['Instance type', n.instance_type ? <code key="it">{n.instance_type}</code> : '—'],
                ['Zone', n.zone ? <code key="zone">{n.zone}</code> : '—'],
              ]} />

              <NodeCuratedCard node={n} onSaved={reload} />

              <SectionTitle>OS &amp; runtime</SectionTitle>
              <KvList items={[
                ['Kubelet', n.kubelet_version ? <code key="kv">{n.kubelet_version}</code> : '—'],
                ['Kube-proxy', n.kube_proxy_version ? <code key="kp">{n.kube_proxy_version}</code> : '—'],
                ['Container runtime', n.container_runtime_version ? <code key="cr">{n.container_runtime_version}</code> : '—'],
                ['OS image', n.os_image ?? '—'],
                ['Operating system', n.operating_system ?? '—'],
                ['Kernel', n.kernel_version ? <code key="kern">{n.kernel_version}</code> : '—'],
                ['Architecture', n.architecture ?? '—'],
              ]} />

              <SectionTitle>Networking</SectionTitle>
              <KvList items={[
                ['Internal IP', n.internal_ip ? <code key="iip">{n.internal_ip}</code> : '—'],
                ['External IP', n.external_ip ? <code key="eip">{n.external_ip}</code> : '—'],
                ['Pod CIDR', n.pod_cidr ? <code key="cidr">{n.pod_cidr}</code> : '—'],
              ]} />
            </div>
            <div>
              <ImpactSection entityType="nodes" entityId={id} />
            </div>
          </div>

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
                        <Callout title="Impact analysis">
                          If this node goes down, {p.items.length} pod{p.items.length === 1 ? '' : 's'} across{' '}
                          {groups.size} workload{groups.size === 1 ? '' : 's'}
                          {unowned.length > 0 && ` (+ ${unowned.length} unmanaged)`} are lost.
                        </Callout>

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
  const ns = useResource(
    async () => (ingress.status === 'ready' ? api.getNamespace(ingress.data.namespace_id) : null),
    [ingress.status === 'ready' ? ingress.data.namespace_id : ''],
  );
  const cluster = useResource(
    async () => (ns.status === 'ready' && ns.data ? api.getCluster(ns.data.cluster_id) : null),
    [ns.status === 'ready' && ns.data ? ns.data.cluster_id : ''],
  );

  return (
    <AsyncView state={ingress}>
      {(i) => (
        <>
          <Breadcrumb parts={[
            { label: 'Ingresses', to: '/ingresses', ariaLabel: 'Back to ingresses' },
            ...(cluster.status === 'ready' && cluster.data
              ? [{ label: cluster.data.name, to: `/clusters/${cluster.data.id}` }]
              : []),
            ...(ns.status === 'ready' && ns.data
              ? [{ label: ns.data.name, to: `/namespaces/${ns.data.id}` }]
              : []),
            { label: i.name },
          ]} />
          <PageHead
            title={i.name}
            sub={i.ingress_class_name ?? undefined}
            actions={<LayerPill layer={i.layer} />}
          />

          <div className="detail-grid">
            <div>
              <KvList items={[
                ['Ingress class', i.ingress_class_name ?? '—'],
                ['Namespace', ns.status === 'ready' && ns.data
                  ? <Link key="ns" to={`/namespaces/${ns.data.id}`}>{ns.data.name}</Link>
                  : <IdLink key="ns" to={`/namespaces/${i.namespace_id}`} id={i.namespace_id} />
                ],
                ['Labels', <Labels key="lbl" labels={i.labels} />],
              ]} />
            </div>
            <div>
              <ImpactSection entityType="ingresses" entityId={id} />
            </div>
          </div>

          <SectionTitle count={i.load_balancer?.length || 0}>Load balancer</SectionTitle>
          {!i.load_balancer?.length ? (
            <Empty message="No address reported yet — Pending for cloud-provisioned, or the on-prem controller (MetalLB / Kube-VIP / hardware LB) hasn't fulfilled this ingress." />
          ) : (
            <>
              <Callout title="External entry point">
                Whichever fulfills this ingress (cloud controller, MetalLB, Kube-VIP, hardware LB) writes
                its address to <code>status.loadBalancer.ingress[]</code>.
              </Callout>
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
  );
}

