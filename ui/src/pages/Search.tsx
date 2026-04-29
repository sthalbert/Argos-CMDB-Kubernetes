import { FormEvent, useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView, Dash, IdLink, SectionTitle, Empty } from '../components';
import { isAdmin, useMe } from '../me';

// Image search — answers "which applications run component X in version Y?"
// The query is kept in the URL (?q=) so auditors can bookmark / share.
//
// Server-side filters:
//  - /v1/workloads?image= and /v1/pods?image= do case-insensitive substring
//    match on the `image` field of every entry in the containers JSON
//    (init containers included).
//  - /v1/virtual-machines?image= does the same case-insensitive substring
//    match on the VM's image_name and image_id (the cloud AMI fields).
//  - /v1/virtual-machines?application= does an exact match on the
//    NORMALIZED product name (ADR-0019); typing "vault" surfaces every VM
//    whose applications array contains the product `vault`. The two VM
//    queries are unioned so platform VMs running operator-declared apps
//    show up alongside K8s workloads.

export default function ImageSearch() {
  const [searchParams, setSearchParams] = useSearchParams();
  const initialQ = searchParams.get('q') || '';
  const [input, setInput] = useState(initialQ);
  const q = searchParams.get('q') || '';

  // Sync local input if the URL changes out from under us (e.g. browser back).
  useEffect(() => {
    setInput(initialQ);
  }, [initialQ]);

  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSearchParams(input.trim() ? { q: input.trim() } : {});
  };

  return (
    <>
      <h2>Search by image or application</h2>
      <p className="muted" style={{ marginTop: 0 }}>
        Case-insensitive substring match against container images on Workloads
        and Pods, and against AMI <code>image_name</code> / <code>image_id</code>{' '}
        on Virtual Machines. Also matches platform VMs whose declared
        applications include the typed product (e.g. <code>vault</code>,{' '}
        <code>cyberwatch</code>). Type a partial like <code>log4j:2.15</code> or
        <code>vault</code> to find everything that runs it.
      </p>

      <form className="search-form" onSubmit={submit}>
        <input
          type="text"
          placeholder="e.g. log4j:2.15, nginx:1.27, postgres:16"
          autoFocus
          value={input}
          onChange={(e) => setInput(e.target.value)}
        />
        <button type="submit" disabled={!input.trim()}>
          Search
        </button>
      </form>

      {q ? <Results q={q} /> : <Empty message="Enter an image to search." />}
    </>
  );
}

function Results({ q }: { q: string }) {
  const me = useMe();
  const canListAccounts = isAdmin(me);
  const state = useResource(
    () =>
      Promise.all([
        api.listWorkloads({ image: q }),
        api.listPods({ image: q }),
        // Two parallel VM queries: image substring (image_name/image_id) and
        // application exact-match (normalized product). Union by id so a VM
        // whose AMI name AND applications both match isn't counted twice.
        api.listVirtualMachines({ image: q }),
        api.listVirtualMachines({ application: q }),
        api.listNamespaces(),
        api.listClusters(),
        // Cloud accounts are admin-only; non-admins fall back to the UUID
        // prefix in the rendered table.
        canListAccounts ? api.listCloudAccounts() : Promise.resolve(null),
      ]).then(([wls, pods, vmsByImage, vmsByApp, ns, cl, accounts]) => {
        const vmsById = new Map<string, api.VirtualMachine>();
        for (const vm of vmsByImage.items) vmsById.set(vm.id, vm);
        for (const vm of vmsByApp.items) vmsById.set(vm.id, vm);
        return {
          workloads: wls.items,
          pods: pods.items,
          vms: Array.from(vmsById.values()),
          namespacesById: new Map(ns.items.map((n) => [n.id, n])),
          clustersById: new Map(cl.items.map((c) => [c.id, c])),
          accountsById: new Map((accounts?.items ?? []).map((a) => [a.id, a])),
        };
      }),
    [q, canListAccounts],
  );

  return (
    <AsyncView state={state}>
      {({ workloads, pods, vms, namespacesById, clustersById, accountsById }) => {
        // Unique namespace count across the union of workload + pod hits —
        // the top-line "affected apps" number shown in the callout.
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

            <SectionTitle count={workloads.length}>Matching workloads</SectionTitle>
            {workloads.length === 0 ? (
              <Empty message="No workloads match." />
            ) : (
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

            <SectionTitle count={pods.length}>Matching pods</SectionTitle>
            {pods.length === 0 ? (
              <Empty message="No pods match." />
            ) : (
              <table className="entities">
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

            <SectionTitle count={vms.length}>Matching virtual machines</SectionTitle>
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
                    <th>Match</th>
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
                        <td>{renderVMMatch(vm, q)}</td>
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

// renderVMMatch shows what made the VM hit the search — substring on the
// image_name/image_id, or one or more application entries whose normalized
// product matches the query. Both cases can fire simultaneously.
function renderVMMatch(vm: api.VirtualMachine, q: string) {
  const qLower = q.toLowerCase();
  const qNormalized = normalizeProductName(q);
  const parts: string[] = [];
  const imageBag = `${vm.image_name ?? ''} ${vm.image_id ?? ''}`.toLowerCase();
  if (imageBag.includes(qLower)) {
    parts.push(`image: ${vm.image_name || vm.image_id}`);
  }
  for (const app of vm.applications ?? []) {
    if (app.product === qNormalized) {
      parts.push(`${app.product} ${app.version}`);
    }
  }
  if (parts.length === 0) return <Dash />;
  return <code>{parts.join(', ')}</code>;
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

