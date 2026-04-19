import { FormEvent, useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { AsyncView, Dash, IdLink, SectionTitle, Empty } from '../components';

// Image search — answers "which applications run component X in version Y?"
// The query is kept in the URL (?q=) so auditors can bookmark / share.
//
// Server-side filter: /v1/workloads?image= and /v1/pods?image= both do
// case-insensitive substring match on the `image` field of every entry in
// the containers JSON. Init containers included.

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
      <h2>Search by container image</h2>
      <p className="muted" style={{ marginTop: 0 }}>
        Case-insensitive substring match against the <code>image</code> field of every
        container (init containers included) on both Workloads and Pods. Type a partial
        like <code>log4j:2.15</code> to find everything on that version line.
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
  const state = useResource(
    () =>
      Promise.all([
        api.listWorkloads({ image: q }),
        api.listPods({ image: q }),
        api.listNamespaces(),
        api.listClusters(),
      ]).then(([wls, pods, ns, cl]) => ({
        workloads: wls.items,
        pods: pods.items,
        namespacesById: new Map(ns.items.map((n) => [n.id, n])),
        clustersById: new Map(cl.items.map((c) => [c.id, c])),
      })),
    [q],
  );

  return (
    <AsyncView state={state}>
      {({ workloads, pods, namespacesById, clustersById }) => {
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
              <strong>{pods.length}</strong> pods, spanning <strong>{affectedApps}</strong>{' '}
              namespace{affectedApps === 1 ? '' : 's'}.
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
          </>
        );
      }}
    </AsyncView>
  );
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

