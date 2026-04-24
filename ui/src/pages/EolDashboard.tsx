import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResources } from '../hooks';
import { AsyncView, Dash } from '../components';

// --- EOL annotation parsing -----------------------------------------------

interface EolAnnotation {
  product: string;
  cycle: string;
  eol?: string;
  eol_status: 'eol' | 'approaching_eol' | 'supported' | 'unknown';
  support?: string;
  latest?: string;
  checked_at?: string;
}

const EOL_PREFIX = 'argos.io/eol.';

function parseEolAnnotations(annotations?: Record<string, string> | null): EolAnnotation[] {
  if (!annotations) return [];
  const results: EolAnnotation[] = [];
  for (const [key, value] of Object.entries(annotations)) {
    if (!key.startsWith(EOL_PREFIX)) continue;
    try {
      results.push(JSON.parse(value) as EolAnnotation);
    } catch {
      // skip malformed annotation
    }
  }
  return results;
}

// --- Status badge ---------------------------------------------------------

function statusClass(status: EolAnnotation['eol_status']): string {
  switch (status) {
    case 'eol':
      return 'pill status-bad';
    case 'approaching_eol':
      return 'pill status-warn';
    case 'supported':
      return 'pill status-ok';
    default:
      return 'pill';
  }
}

function statusLabel(status: EolAnnotation['eol_status']): string {
  switch (status) {
    case 'eol':
      return 'End of Life';
    case 'approaching_eol':
      return 'Approaching EOL';
    case 'supported':
      return 'Supported';
    default:
      return 'Unknown';
  }
}

export const EolBadge = ({ status }: { status: EolAnnotation['eol_status'] }) => (
  <span className={statusClass(status)}>{statusLabel(status)}</span>
);

// --- Flat row for the table -----------------------------------------------

interface EolRow {
  entityType: 'cluster' | 'node';
  entityId: string;
  entityName: string;
  clusterName: string;
  product: string;
  cycle: string;
  eolStatus: EolAnnotation['eol_status'];
  eolDate: string | undefined;
  latest: string | undefined;
  checkedAt: string | undefined;
}

function buildRows(clusters: api.Cluster[], nodes: api.Node[]): EolRow[] {
  const clusterById = new Map(clusters.map((c) => [c.id, c]));
  const rows: EolRow[] = [];

  for (const cluster of clusters) {
    for (const ann of parseEolAnnotations(cluster.annotations)) {
      rows.push({
        entityType: 'cluster',
        entityId: cluster.id,
        entityName: cluster.display_name || cluster.name,
        clusterName: cluster.display_name || cluster.name,
        product: ann.product,
        cycle: ann.cycle,
        eolStatus: ann.eol_status,
        eolDate: ann.eol,
        latest: ann.latest,
        checkedAt: ann.checked_at,
      });
    }
  }

  for (const node of nodes) {
    const cluster = clusterById.get(node.cluster_id);
    for (const ann of parseEolAnnotations(node.annotations)) {
      rows.push({
        entityType: 'node',
        entityId: node.id,
        entityName: node.name,
        clusterName: cluster ? cluster.display_name || cluster.name : node.cluster_id.slice(0, 8),
        product: ann.product,
        cycle: ann.cycle,
        eolStatus: ann.eol_status,
        eolDate: ann.eol,
        latest: ann.latest,
        checkedAt: ann.checked_at,
      });
    }
  }

  // Sort: eol first, then approaching_eol, then supported
  const order: Record<string, number> = { eol: 0, approaching_eol: 1, supported: 2, unknown: 3 };
  rows.sort((a, b) => (order[a.eolStatus] ?? 4) - (order[b.eolStatus] ?? 4));
  return rows;
}

// --- Summary counters -----------------------------------------------------

interface StatusCounts {
  eol: number;
  approaching_eol: number;
  supported: number;
  unknown: number;
}

function countStatuses(rows: EolRow[]): StatusCounts {
  const counts: StatusCounts = { eol: 0, approaching_eol: 0, supported: 0, unknown: 0 };
  for (const r of rows) {
    counts[r.eolStatus] = (counts[r.eolStatus] || 0) + 1;
  }
  return counts;
}

// --- Page component -------------------------------------------------------

export default function EolDashboard() {
  const state = useResources(
    [() => api.listClusters(), () => api.listNodes()] as const,
    [],
  );

  return (
    <>
      <h2>End-of-Life Dashboard</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Lifecycle status of inventoried software, enriched from endoflife.date.
      </p>
      <AsyncView state={state}>
        {([clustersResp, nodesResp]) => {
          const rows = buildRows(clustersResp.items, nodesResp.items);
          if (rows.length === 0) {
            return (
              <p className="muted">
                No EOL data available. Enable the enricher with{' '}
                <code>ARGOS_EOL_ENABLED=true</code>.
              </p>
            );
          }
          const counts = countStatuses(rows);
          return (
            <>
              <div className="eol-summary">
                <div className="eol-summary-card eol-bad">
                  <span className="eol-summary-count">{counts.eol}</span>
                  <span className="eol-summary-label">End of Life</span>
                </div>
                <div className="eol-summary-card eol-warn">
                  <span className="eol-summary-count">{counts.approaching_eol}</span>
                  <span className="eol-summary-label">Approaching EOL</span>
                </div>
                <div className="eol-summary-card eol-ok">
                  <span className="eol-summary-count">{counts.supported}</span>
                  <span className="eol-summary-label">Supported</span>
                </div>
              </div>

              <table className="entities">
                <thead>
                  <tr>
                    <th>Status</th>
                    <th>Product</th>
                    <th>Cycle</th>
                    <th>Entity</th>
                    <th>Cluster</th>
                    <th>EOL Date</th>
                    <th>Latest</th>
                    <th>Checked</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((r, i) => (
                    <tr key={i}>
                      <td>
                        <EolBadge status={r.eolStatus} />
                      </td>
                      <td>
                        <code>{r.product}</code>
                      </td>
                      <td>
                        <code>{r.cycle}</code>
                      </td>
                      <td>
                        <Link to={`/${r.entityType === 'cluster' ? 'clusters' : 'nodes'}/${r.entityId}`}>
                          <strong>{r.entityName}</strong>
                        </Link>
                        <span className="muted" style={{ marginLeft: '0.4rem', fontSize: '0.8rem' }}>
                          {r.entityType}
                        </span>
                      </td>
                      <td>{r.clusterName}</td>
                      <td>{r.eolDate ? <code>{r.eolDate}</code> : <Dash />}</td>
                      <td>{r.latest ? <code>{r.latest}</code> : <Dash />}</td>
                      <td className="muted" style={{ fontSize: '0.8rem' }}>
                        {r.checkedAt ? new Date(r.checkedAt).toLocaleDateString() : <Dash />}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          );
        }}
      </AsyncView>
    </>
  );
}
