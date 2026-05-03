import { FormEvent, useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView, Dash, IdLink, Empty } from '../components';
import { isAdmin, useMe } from '../me';
import { PageHead } from '../components/lv/PageHead';
import { Tabs } from '../components/lv/Tabs';
import { Section } from '../components/lv/Section';

// Image / application search — answers "which applications run component X?"
// The query is kept in the URL (?q=&tab=) so auditors can bookmark / share.
//
// Image tab:
//  - /v1/workloads?image= and /v1/pods?image= do case-insensitive substring
//    match on the `image` field of every entry in the containers JSON
//    (init containers included).
//  - /v1/virtual-machines?image= does the same case-insensitive substring
//    match on the VM's image_name and image_id (the cloud AMI fields).
//
// Application tab:
//  - /v1/virtual-machines?application= does an exact match on the
//    NORMALIZED product name (ADR-0019); typing "vault" surfaces every VM
//    whose applications array contains the product `vault`.

const TAB_ITEMS = [
  { key: 'image', label: 'Image' },
  { key: 'application', label: 'Application' },
];

export default function ImageSearch() {
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = (searchParams.get('tab') || 'image') as 'image' | 'application';
  const initialQ = searchParams.get('q') || '';
  const [input, setInput] = useState(initialQ);
  const q = searchParams.get('q') || '';

  // Sync local input if the URL changes out from under us (e.g. browser back).
  useEffect(() => {
    setInput(initialQ);
  }, [initialQ]);

  const handleTabChange = (key: string) => {
    const next: Record<string, string> = { tab: key };
    if (q) next.q = q;
    setSearchParams(next);
  };

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const next: Record<string, string> = { tab: activeTab };
    if (input.trim()) next.q = input.trim();
    setSearchParams(next);
  };

  const placeholder =
    activeTab === 'image'
      ? 'e.g. log4j:2.15, nginx:1.27, postgres:16'
      : 'e.g. vault, cyberwatch, wazuh';

  const emptyHint =
    activeTab === 'image'
      ? 'Enter an image to search.'
      : 'Enter a product name to search platform VMs.';

  return (
    <>
      <PageHead
        title="Search by image or application"
        sub="Find K8s workloads/pods and platform VMs."
      />

      <Tabs items={TAB_ITEMS} active={activeTab} onChange={handleTabChange} />

      <div className="lv-toolbar">
        <form className="search-form" onSubmit={submit}>
          <input
            type="text"
            placeholder={placeholder}
            aria-label={activeTab === 'image' ? 'Search by image' : 'Search by application'}
            autoFocus
            value={input}
            onChange={(e) => setInput(e.target.value)}
          />
          <button type="submit" disabled={!input.trim()}>
            Search
          </button>
        </form>
      </div>

      {q ? (
        activeTab === 'image' ? (
          <ImageResults q={q} />
        ) : (
          <AppResults q={q} />
        )
      ) : (
        <Empty message={emptyHint} />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Image-tab results: workloads + pods + VMs by container image / AMI substring
// ---------------------------------------------------------------------------

function ImageResults({ q }: { q: string }) {
  const me = useMe();
  const canListAccounts = isAdmin(me);
  const state = useResource(
    () =>
      Promise.all([
        api.listWorkloads({ image: q }),
        api.listPods({ image: q }),
        api.listVirtualMachines({ image: q }),
        api.listNamespaces(),
        api.listClusters(),
        canListAccounts ? api.listCloudAccounts() : Promise.resolve(null),
      ]).then(([wls, pods, vmsByImage, ns, cl, accounts]) => ({
        workloads: wls.items,
        pods: pods.items,
        vms: vmsByImage.items,
        namespacesById: new Map(ns.items.map((n) => [n.id, n])),
        clustersById: new Map(cl.items.map((c) => [c.id, c])),
        accountsById: new Map((accounts?.items ?? []).map((a) => [a.id, a])),
      })),
    [q, canListAccounts],
  );

  return (
    <AsyncView state={state}>
      {({ workloads, pods, vms, namespacesById, clustersById, accountsById }) => {
        const affectedNamespaces = new Set<string>();
        workloads.forEach((w) => affectedNamespaces.add(w.namespace_id));
        pods.forEach((p) => affectedNamespaces.add(p.namespace_id));
        const affectedApps = affectedNamespaces.size;
        return (
          <>
            <p className="impact-callout">
              Matches for <code>{q}</code>: <strong>{workloads.length}</strong> workloads,{' '}
              <strong>{pods.length}</strong> pods, <strong>{vms.length}</strong>{' '}
              virtual machine{vms.length === 1 ? '' : 's'}, spanning{' '}
              <strong>{affectedApps}</strong> namespace
              {affectedApps === 1 ? '' : 's'}.
            </p>

            <Section count={workloads.length + pods.length}>Kubernetes</Section>
            {workloads.length === 0 && pods.length === 0 ? (
              <Empty message="No K8s workloads or pods match." />
            ) : (
              <>
                {workloads.length > 0 && (
                  <table className="entities">
                    <thead>
                      <tr>
                        <th>Workload</th>
                        <th>Kind</th>
                        <th>Cluster / Namespace</th>
                        <th>Matching images</th>
                      </tr>
                    </thead>
                    <tbody>
                      {workloads.map((w) => (
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
                            <NamespaceCrumb
                              namespaceId={w.namespace_id}
                              namespacesById={namespacesById}
                              clustersById={clustersById}
                            />
                          </td>
                          <td>{renderMatchedImages(w.containers, q)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
                {pods.length > 0 && (
                  <table className="entities" style={{ marginTop: workloads.length > 0 ? '1rem' : undefined }}>
                    <thead>
                      <tr>
                        <th>Pod</th>
                        <th>Phase</th>
                        <th>Cluster / Namespace</th>
                        <th>Workload</th>
                        <th>Matching images</th>
                      </tr>
                    </thead>
                    <tbody>
                      {pods.map((p) => (
                        <tr key={p.id}>
                          <td>
                            <Link to={`/pods/${p.id}`}>
                              <strong>{p.name}</strong>
                            </Link>
                          </td>
                          <td>{p.phase || <Dash />}</td>
                          <td>
                            <NamespaceCrumb
                              namespaceId={p.namespace_id}
                              namespacesById={namespacesById}
                              clustersById={clustersById}
                            />
                          </td>
                          <td>
                            {p.workload_id ? (
                              <IdLink to={`/workloads/${p.workload_id}`} id={p.workload_id} />
                            ) : (
                              <Dash />
                            )}
                          </td>
                          <td>{renderMatchedImages(p.containers, q)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                )}
              </>
            )}

            <Section count={vms.length}>Virtual machines</Section>
            {vms.length === 0 ? (
              <Empty message="No virtual machines match." />
            ) : (
              <table className="entities">
                <thead>
                  <tr>
                    <th>VM</th>
                    <th>Cloud account</th>
                    <th>Region</th>
                    <th>Image</th>
                  </tr>
                </thead>
                <tbody>
                  {vms.map((vm) => {
                    const acct = accountsById.get(vm.cloud_account_id);
                    return (
                      <tr key={vm.id}>
                        <td>
                          <Link to={`/virtual-machines/${vm.id}`}>
                            <strong>{vm.display_name || vm.name}</strong>
                          </Link>
                          {vm.display_name && (
                            <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                              {vm.name}
                            </div>
                          )}
                        </td>
                        <td>
                          {acct ? (
                            <span>
                              {acct.name}{' '}
                              <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                                {acct.provider}
                              </span>
                            </span>
                          ) : (
                            <span className="muted">{vm.cloud_account_id.slice(0, 8)}…</span>
                          )}
                        </td>
                        <td>{vm.region ? <code>{vm.region}</code> : <Dash />}</td>
                        <td>
                          {vm.image_name || vm.image_id ? (
                            <code>{vm.image_name || vm.image_id}</code>
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
}

// ---------------------------------------------------------------------------
// Application-tab results: platform VMs by declared application product name
// ---------------------------------------------------------------------------

function AppResults({ q }: { q: string }) {
  const me = useMe();
  const canListAccounts = isAdmin(me);
  const state = useResource(
    () =>
      Promise.all([
        api.listVirtualMachines({ application: q }),
        canListAccounts ? api.listCloudAccounts() : Promise.resolve(null),
      ]).then(([vmsByApp, accounts]) => ({
        vms: vmsByApp.items,
        accountsById: new Map((accounts?.items ?? []).map((a) => [a.id, a])),
      })),
    [q, canListAccounts],
  );

  return (
    <AsyncView state={state}>
      {({ vms, accountsById }) => (
        <>
          <p className="impact-callout">
            Platform VMs declaring <code>{q}</code>: <strong>{vms.length}</strong>{' '}
            virtual machine{vms.length === 1 ? '' : 's'}.
          </p>

          <Section count={vms.length}>Virtual machines</Section>
          {vms.length === 0 ? (
            <Empty message="No virtual machines declare this application." />
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>VM</th>
                  <th>Cloud account</th>
                  <th>Region</th>
                  <th>Matching applications</th>
                </tr>
              </thead>
              <tbody>
                {vms.map((vm) => {
                  const acct = accountsById.get(vm.cloud_account_id);
                  return (
                    <tr key={vm.id}>
                      <td>
                        <Link to={`/virtual-machines/${vm.id}`}>
                          <strong>{vm.display_name || vm.name}</strong>
                        </Link>
                        {vm.display_name && (
                          <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                            {vm.name}
                          </div>
                        )}
                      </td>
                      <td>
                        {acct ? (
                          <span>
                            {acct.name}{' '}
                            <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                              {acct.provider}
                            </span>
                          </span>
                        ) : (
                          <span className="muted">{vm.cloud_account_id.slice(0, 8)}…</span>
                        )}
                      </td>
                      <td>{vm.region ? <code>{vm.region}</code> : <Dash />}</td>
                      <td>{renderVMApplications(vm, q)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </>
      )}
    </AsyncView>
  );
}

// renderVMApplications shows the application entries that matched the query.
function renderVMApplications(vm: api.VirtualMachine, q: string) {
  const qNormalized = normalizeProductName(q);
  const hits = (vm.applications ?? []).filter((app) => app.product === qNormalized);
  if (hits.length === 0) return <Dash />;
  return <code>{hits.map((a) => `${a.product} ${a.version}`).join(', ')}</code>;
}

// normalizeProductName mirrors api.NormalizeProductName server-side: trim,
// lowercase, collapse whitespace/_/- runs into single hyphens. Kept inline
// so the search UI can reproduce the server's match key without a round
// trip — used only for display ("which application matched?"), never for
// requests (the server normalizes again before applying the filter).
function normalizeProductName(s: string): string {
  const trimmed = s.trim().toLowerCase();
  if (!trimmed) return '';
  let out = '';
  let prevHyphen = false;
  for (const ch of trimmed) {
    if (ch === ' ' || ch === '\t' || ch === '_' || ch === '-') {
      if (!prevHyphen && out.length > 0) {
        out += '-';
        prevHyphen = true;
      }
    } else {
      out += ch;
      prevHyphen = false;
    }
  }
  return out.replace(/-+$/, '');
}

function renderMatchedImages(containers: api.Container[] | null | undefined, q: string) {
  if (!containers?.length) return <Dash />;
  const qLower = q.toLowerCase();
  const hits = containers.filter((c) => c.image.toLowerCase().includes(qLower));
  if (hits.length === 0) return <Dash />;
  return (
    <code>
      {hits
        .map((c) => (c.init ? `${c.image} (init)` : c.image))
        .join(', ')}
    </code>
  );
}

function NamespaceCrumb({
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
