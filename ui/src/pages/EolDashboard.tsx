import { useState, useMemo } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResources } from '../hooks';
import { AsyncView, Dash } from '../components';
import { useEntityTable } from '../components/column_filters';
import { EolIcon } from '../icons';
import { ExtractButton } from '../components/ExtractButton';
import { TruncationBanner } from '../components/TruncationBanner';

// --- EOL annotation parsing -----------------------------------------------

interface EolAnnotation {
  product: string;
  cycle: string;
  eol?: string;
  eol_status: 'eol' | 'approaching_eol' | 'supported' | 'unknown';
  support?: string;
  latest?: string;
  latest_available?: string;
  checked_at?: string;
}

type EolStatus = EolAnnotation['eol_status'];

const EOL_PREFIX = 'longue-vue.io/eol.';

// imageRepo returns the last path segment of an image ref's repo for display
// (e.g. "nginx" from "docker.io/library/nginx:1.25.3").
function imageRepo(image: string): string {
  const noDigest = image.split('@')[0];
  const lastColon = noDigest.lastIndexOf(':');
  const lastSlash = noDigest.lastIndexOf('/');
  const repo = lastColon > lastSlash ? noDigest.slice(0, lastColon) : noDigest;
  const seg = repo.split('/');
  return seg[seg.length - 1];
}

// majorMinor returns "X.Y" from the leading semver of an image tag.
function majorMinor(image: string): string {
  const noDigest = image.split('@')[0];
  const lastColon = noDigest.lastIndexOf(':');
  const lastSlash = noDigest.lastIndexOf('/');
  const tag = lastColon > lastSlash ? noDigest.slice(lastColon + 1) : '';
  const m = tag.match(/^v?(\d+)\.(\d+)/);
  return m ? `${m[1]}.${m[2]}` : '';
}

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

function statusClass(status: EolStatus): string {
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

function statusLabel(status: EolStatus): string {
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

export const EolBadge = ({ status }: { status: EolStatus }) => (
  <span className={statusClass(status)}>{statusLabel(status)}</span>
);

// --- Flat row for the table -----------------------------------------------

interface EolRow {
  entityType: 'cluster' | 'node' | 'vm' | 'workload';
  entityId: string;
  entityName: string;
  clusterName: string;
  product: string;
  cycle: string;
  eolStatus: EolStatus;
  eolDate: string | undefined;
  latest: string | undefined;
  latestAvailable: string | undefined;
  checkedAt: string | undefined;
  // ADR-0029 linkage. Only VM rows can carry a row-level Application link
  // today (clusters/nodes are not linkable); resolved client-side from the
  // applications list. Best-effort until Phase 5's per-application EOL
  // aggregation lands a server-side denorm.
  applicationId: string | undefined;
  applicationName: string | undefined;
}

const STATUS_ORDER: Record<string, number> = { eol: 0, approaching_eol: 1, supported: 2, unknown: 3 };

function buildRows(
  clusters: api.Cluster[],
  nodes: api.Node[],
  vms: api.VirtualMachine[],
  appNameById: Map<string, string> = new Map(),
  workloads: api.Workload[] = [],
): EolRow[] {
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
        latestAvailable: ann.latest_available,
        checkedAt: ann.checked_at,
        applicationId: undefined,
        applicationName: undefined,
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
        latestAvailable: ann.latest_available,
        checkedAt: ann.checked_at,
        applicationId: undefined,
        applicationName: undefined,
      });
    }
  }

  for (const vm of vms) {
    const appId = vm.application_id ?? undefined;
    for (const ann of parseEolAnnotations(vm.annotations)) {
      rows.push({
        entityType: 'vm',
        entityId: vm.id,
        entityName: vm.display_name || vm.name,
        clusterName: '',
        product: ann.product,
        cycle: ann.cycle,
        eolStatus: ann.eol_status,
        eolDate: ann.eol,
        latest: ann.latest,
        latestAvailable: ann.latest_available,
        checkedAt: ann.checked_at,
        applicationId: appId,
        applicationName: appId
          ? appNameById.get(appId) ?? `${appId.slice(0, 8)}…`
          : undefined,
      });
    }
  }

  for (const wl of workloads) {
    const cvs = wl.containers_versions ?? {};
    const containers = wl.containers ?? [];
    const appId = wl.application_id ?? undefined;
    const worstByRepo = new Map<string, EolRow>();
    for (const c of containers) {
      if (!c.name || !c.image || !cvs[c.name]?.eol_status) continue;
      const cv = cvs[c.name];
      const repo = imageRepo(c.image);
      const row: EolRow = {
        entityType: 'workload',
        entityId: wl.id,
        entityName: wl.name,
        clusterName: wl.cluster_name ?? '',
        product: repo,
        cycle: majorMinor(c.image),
        eolStatus: cv.eol_status as EolStatus,
        eolDate: undefined,
        latest: cv.latest_tag,
        latestAvailable: cv.latest_tag,
        checkedAt: cv.last_checked_at,
        applicationId: appId,
        applicationName: appId
          ? appNameById.get(appId) ?? `${appId.slice(0, 8)}…`
          : undefined,
      };
      const prev = worstByRepo.get(repo);
      if (!prev || (STATUS_ORDER[row.eolStatus] ?? 4) < (STATUS_ORDER[prev.eolStatus] ?? 4)) {
        worstByRepo.set(repo, row);
      }
    }
    for (const row of worstByRepo.values()) rows.push(row);
  }

  rows.sort((a, b) => (STATUS_ORDER[a.eolStatus] ?? 4) - (STATUS_ORDER[b.eolStatus] ?? 4));
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

// --- Sortable column header -----------------------------------------------

type SortKey = 'status' | 'product' | 'entity' | 'cluster' | 'application' | 'eolDate';

function compareRows(a: EolRow, b: EolRow, key: SortKey, asc: boolean): number {
  let cmp = 0;
  switch (key) {
    case 'status':
      cmp = (STATUS_ORDER[a.eolStatus] ?? 4) - (STATUS_ORDER[b.eolStatus] ?? 4);
      break;
    case 'product':
      cmp = a.product.localeCompare(b.product);
      break;
    case 'entity':
      cmp = a.entityName.localeCompare(b.entityName);
      break;
    case 'cluster':
      cmp = a.clusterName.localeCompare(b.clusterName);
      break;
    case 'application':
      cmp = (a.applicationName ?? '￿').localeCompare(b.applicationName ?? '￿');
      break;
    case 'eolDate':
      cmp = (a.eolDate ?? '9999').localeCompare(b.eolDate ?? '9999');
      break;
  }
  return asc ? cmp : -cmp;
}

function SortHeader({
  label,
  sortKey,
  currentKey,
  asc,
  onClick,
}: {
  label: string;
  sortKey: SortKey;
  currentKey: SortKey;
  asc: boolean;
  onClick: (key: SortKey) => void;
}) {
  const arrow = currentKey === sortKey ? (asc ? ' \u25b2' : ' \u25bc') : '';
  return (
    <th className="sortable" onClick={() => onClick(sortKey)}>
      {label}{arrow}
    </th>
  );
}

// --- Page component -------------------------------------------------------

export default function EolDashboard() {
  const state = useResources(
    [
      () => api.listClusters(),
      () => api.listNodes(),
      () => api.listVirtualMachines(),
      () => api.listApplications({ limit: 200 }),
      () => api.listWorkloads({ include: 'containers_versions', limit: 200 }),
    ] as const,
    [],
  );
  const [statusFilter, setStatusFilter] = useState<EolStatus | null>(null);
  const [appFilter, setAppFilter] = useState('');
  const [sortKey, setSortKey] = useState<SortKey>('status');
  const [sortAsc, setSortAsc] = useState(true);
  const [truncated, setTruncated] = useState(false);

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortAsc(!sortAsc);
    } else {
      setSortKey(key);
      setSortAsc(true);
    }
  };

  const handleCardClick = (status: EolStatus) => {
    setStatusFilter((prev) => (prev === status ? null : status));
    setSortKey('status');
    setSortAsc(true);
  };

  return (
    <>
      <h2><EolIcon size={20} /> End-of-Life Inventory</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Lifecycle status of inventoried software, enriched from endoflife.date.
      </p>
      <TruncationBanner visible={truncated} />
      <AsyncView state={state}>
        {([clustersResp, nodesResp, vmsResp, appsResp, workloadsResp]) => {
          const appNameById = new Map(
            (appsResp.items ?? []).map((a) => [a.id, a.display_name || a.name]),
          );
          return (
            <>
              {(() => {
                const rows = buildRows(
                  clustersResp.items,
                  nodesResp.items,
                  vmsResp.items,
                  appNameById,
                  workloadsResp.items,
                );
                if (rows.length === 0) return null;
                return (
                  <div className="eol-extract">
                    <ExtractButton
                      label="Extract"
                      onExtract={(format) =>
                        api.extractEol({
                          format,
                          status: statusFilter ?? undefined,
                        })
                      }
                      onTruncation={setTruncated}
                    />
                  </div>
                );
              })()}
              <EolTable
                clusters={clustersResp.items}
                nodes={nodesResp.items}
                vms={vmsResp.items}
                workloads={workloadsResp.items}
                appNameById={appNameById}
                statusFilter={statusFilter}
                appFilter={appFilter}
                onAppFilterChange={setAppFilter}
                sortKey={sortKey}
                sortAsc={sortAsc}
                onCardClick={handleCardClick}
                onSort={handleSort}
              />
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function EolTable({
  clusters,
  nodes,
  vms,
  workloads,
  appNameById,
  statusFilter,
  appFilter,
  onAppFilterChange,
  sortKey,
  sortAsc,
  onCardClick,
  onSort,
}: {
  clusters: api.Cluster[];
  nodes: api.Node[];
  vms: api.VirtualMachine[];
  workloads: api.Workload[];
  appNameById: Map<string, string>;
  statusFilter: EolStatus | null;
  appFilter: string;
  onAppFilterChange: (v: string) => void;
  sortKey: SortKey;
  sortAsc: boolean;
  onCardClick: (status: EolStatus) => void;
  onSort: (key: SortKey) => void;
}) {
  const allRows = useMemo(
    () => buildRows(clusters, nodes, vms, appNameById, workloads),
    [clusters, nodes, vms, appNameById, workloads],
  );
  const counts = useMemo(() => countStatuses(allRows), [allRows]);
  const tableRef = useEntityTable('eol.table');

  const appNeedle = appFilter.trim().toLowerCase();
  const filtered = useMemo(
    () =>
      allRows.filter((r) => {
        if (statusFilter && r.eolStatus !== statusFilter) return false;
        if (appNeedle && !(r.applicationName ?? '').toLowerCase().includes(appNeedle))
          return false;
        return true;
      }),
    [allRows, statusFilter, appNeedle],
  );

  const sorted = useMemo(
    () => [...filtered].sort((a, b) => compareRows(a, b, sortKey, sortAsc)),
    [filtered, sortKey, sortAsc],
  );

  if (allRows.length === 0) {
    return (
      <p className="muted">
        No EOL data available. Enable the enricher in{' '}
        <strong>Admin &gt; Settings</strong>.
      </p>
    );
  }

  const cardClass = (status: EolStatus, colorClass: string) =>
    `eol-summary-card ${colorClass}${statusFilter === status ? ' eol-active' : ''}`;

  return (
    <>
      <div className="eol-summary">
        <div className={cardClass('eol', 'eol-bad')} onClick={() => onCardClick('eol')}>
          <span className="eol-summary-count">{counts.eol}</span>
          <span className="eol-summary-label">End of Life</span>
        </div>
        <div className={cardClass('approaching_eol', 'eol-warn')} onClick={() => onCardClick('approaching_eol')}>
          <span className="eol-summary-count">{counts.approaching_eol}</span>
          <span className="eol-summary-label">Approaching EOL</span>
        </div>
        <div className={cardClass('supported', 'eol-ok')} onClick={() => onCardClick('supported')}>
          <span className="eol-summary-count">{counts.supported}</span>
          <span className="eol-summary-label">Supported</span>
        </div>
      </div>

      <div className="eol-app-filter" style={{ marginBottom: '0.75rem' }}>
        <input
          type="text"
          value={appFilter}
          placeholder="Filter by application…"
          aria-label="Filter by application"
          onChange={(e) => onAppFilterChange(e.target.value)}
        />
        {appFilter && (
          <button
            className="link-btn"
            style={{ marginLeft: '0.5rem' }}
            onClick={() => onAppFilterChange('')}
          >
            clear
          </button>
        )}
      </div>

      {(statusFilter || appNeedle) && (
        <p style={{ marginBottom: '0.75rem' }}>
          Filtering:{' '}
          {statusFilter && (
            <>
              <EolBadge status={statusFilter} />{' '}
              <button className="link-btn" onClick={() => onCardClick(statusFilter)}>
                clear
              </button>{' '}
            </>
          )}
          {appNeedle && (
            <span className="pill">application ~ {appFilter}</span>
          )}
          <span className="muted" style={{ marginLeft: '0.5rem' }}>
            ({sorted.length} of {allRows.length})
          </span>
        </p>
      )}

      <table className="entities eol-table" ref={tableRef}>
        <thead>
          <tr>
            <SortHeader label="Status" sortKey="status" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <SortHeader label="Product" sortKey="product" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <th>Version</th>
            <th>Patch</th>
            <SortHeader label="Entity" sortKey="entity" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <SortHeader label="Application" sortKey="application" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <SortHeader label="Cluster" sortKey="cluster" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <th>Latest Available</th>
            <SortHeader label="EOL Date" sortKey="eolDate" currentKey={sortKey} asc={sortAsc} onClick={onSort} />
            <th>Checked</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map((r, i) => (
            <tr key={i} className={r.eolStatus === 'eol' ? 'eol-row-bad' : r.eolStatus === 'approaching_eol' ? 'eol-row-warn' : ''}>
              <td>
                <EolBadge status={r.eolStatus} />
              </td>
              <td>
                <code>{r.product}</code>
              </td>
              <td>
                <code>{r.cycle}</code>
              </td>
              <td>{r.latest ? <code>{r.latest}</code> : <Dash />}</td>
              <td>
                <Link
                  to={`/${
                    r.entityType === 'cluster'
                      ? 'clusters'
                      : r.entityType === 'vm'
                      ? 'virtual-machines'
                      : r.entityType === 'workload'
                      ? 'workloads'
                      : 'nodes'
                  }/${r.entityId}`}
                >
                  <strong>{r.entityName}</strong>
                </Link>
                <span className="muted" style={{ marginLeft: '0.4rem', fontSize: '0.8rem' }}>
                  {r.entityType}
                </span>
              </td>
              <td>
                {r.applicationId ? (
                  <Link to={`/applications/${r.applicationId}`}>{r.applicationName}</Link>
                ) : (
                  <Dash />
                )}
              </td>
              <td>{r.clusterName || <Dash />}</td>
              <td className="eol-col-separator">{r.latestAvailable ? <code>{r.latestAvailable}</code> : <Dash />}</td>
              <td>{r.eolDate ? <code>{r.eolDate}</code> : <Dash />}</td>
              <td className="muted" style={{ fontSize: '0.8rem' }}>
                {r.checkedAt ? new Date(r.checkedAt).toLocaleDateString() : <Dash />}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
