import { Link } from 'react-router';
import type { AsyncState } from './hooks';
import { PAGE_SIZE_OPTIONS, type PageSize } from './hooks';

// Muted dash used where a field is null / absent.
export const Dash = () => <span className="muted">—</span>;

/** Locale-formatted timestamp; empty string for null/undefined. */
export function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

// Layer pill — subtle background, always visible.
export const LayerPill = ({ layer }: { layer: string }) => (
  <span className="pill">{layer}</span>
);

// AsyncView is the switch-on-status pattern the list/detail pages share.
// Always render the page chrome (title etc.); just vary the body.
export function AsyncView<T>({
  state,
  children,
}: {
  state: AsyncState<T>;
  children: (data: T) => React.ReactNode;
}) {
  if (state.status === 'loading') return <p className="loading">Loading…</p>;
  if (state.status === 'error') return <div className="error">Failed to load: {state.error}</div>;
  return <>{children(state.data)}</>;
}

export const SectionTitle = ({
  children,
  count,
}: {
  children: React.ReactNode;
  count?: number;
}) => (
  <h3 className="section-title">
    {children}
    {count !== undefined && <span className="muted count"> ({count})</span>}
  </h3>
);

export const Empty = ({ message }: { message: string }) => (
  <p className="muted empty">{message}</p>
);

// Renders any value's short form: UUID truncated to first 8 chars, strings
// as-is, null/undefined as Dash. Used in dense tables.
export const ShortId = ({ id }: { id?: string | null }) =>
  id ? <code className="shortid">{id.slice(0, 8)}…</code> : <Dash />;

// Linked-ID: show the short id as a link to its detail page. Hover reveals
// the full id via the title attribute.
export const IdLink = ({ to, id }: { to: string; id: string }) => (
  <Link to={to} title={id}>
    <code className="shortid">{id.slice(0, 8)}…</code>
  </Link>
);

// KV shows a label/value pair in a two-column "definition list" style —
// used on every detail header.
export const KV = ({ k, v }: { k: string; v: React.ReactNode }) => (
  <div className="kv">
    <dt>{k}</dt>
    <dd>{v || <Dash />}</dd>
  </div>
);

// ClusterLink renders a clickable cluster reference using the denormalized
// `cluster_name` carried on the entity payload (ADR-0027). A null name on a
// non-null id means the cluster row was hard-deleted; surface that as an
// explicit (orphan) badge so operators never see a raw UUID.
export function ClusterLink({
  clusterId,
  clusterName,
}: {
  clusterId?: string | null;
  clusterName?: string | null;
}) {
  if (!clusterId) return <Dash />;
  return (
    <Link to={`/clusters/${clusterId}`}>
      {clusterName ?? <span title="cluster row missing">(orphan)</span>}
    </Link>
  );
}

// NamespaceLink renders the cluster / namespace breadcrumb for entities
// that carry a namespace_id. Reads the denormalized parent names from the
// API response (ADR-0027) — no client-side index needed. A null name on a
// non-null id means the parent row was hard-deleted; we surface that as
// an explicit (orphan) badge so operators don't see a bare UUID.
export function NamespaceLink({
  namespaceId,
  namespaceName,
  clusterId,
  clusterName,
}: {
  namespaceId: string;
  namespaceName?: string | null;
  clusterId?: string | null;
  clusterName?: string | null;
}) {
  return (
    <>
      {clusterId ? (
        <Link to={`/clusters/${clusterId}`} className="muted">
          {clusterName ?? <span title="cluster row missing">(orphan)</span>}
        </Link>
      ) : null}
      {clusterId ? <span className="muted"> / </span> : null}
      <Link to={`/namespaces/${namespaceId}`}>
        {namespaceName ?? <span title="namespace row missing">(orphan)</span>}
      </Link>
    </>
  );
}

// WorkloadLink renders a clickable workload reference using the denormalized
// `workload_name` carried on pods (ADR-0027). Three states:
//   - workload_id is null → pod has no owner (DaemonSet "manual" or naked Pod)
//   - workload_id present, workload_name null → workload row was hard-deleted
//   - both present → name link
export function WorkloadLink({
  workloadId,
  workloadName,
}: {
  workloadId?: string | null;
  workloadName?: string | null;
}) {
  if (!workloadId) {
    return <span className="muted">(unmanaged / unknown owner kind)</span>;
  }
  return (
    <Link to={`/workloads/${workloadId}`}>
      {workloadName ?? <span title="workload row missing">(orphan)</span>}
    </Link>
  );
}

export const Code = ({ children }: { children: React.ReactNode }) => (
  <code className="inline-code">{children}</code>
);

// LoadBalancerAddresses renders each entry from status.loadBalancer.ingress
// as a chip. IPs render monospaced; hostnames italicised so the two modes
// are visually distinct even when an entry carries both (rare but valid —
// cloud LBs sometimes expose both a DNS name and an eventual IP).
export const LoadBalancerAddresses = ({
  entries,
}: {
  entries?:
    | Array<{ ip?: string; hostname?: string; ports?: Array<{ port: number; protocol?: string }> }>
    | null;
}) => {
  if (!entries || entries.length === 0) return <Dash />;
  return (
    <div className="lb-list">
      {entries.map((e, i) => {
        const parts: React.ReactNode[] = [];
        if (e.ip) parts.push(<code key="ip">{e.ip}</code>);
        if (e.hostname) parts.push(<span key="h" className="lb-host">{e.hostname}</span>);
        if (parts.length === 0) parts.push(<Dash key="empty" />);
        const ports = e.ports
          ?.map((p) => `${p.port}/${p.protocol || 'TCP'}`)
          .join(', ');
        return (
          <span key={i} className="lb-entry">
            {parts.map((p, idx) => (
              <span key={idx}>
                {idx > 0 ? <span className="muted"> · </span> : null}
                {p}
              </span>
            ))}
            {ports && <span className="muted lb-ports"> [{ports}]</span>}
          </span>
        );
      })}
    </div>
  );
};

export const Labels = ({ labels }: { labels?: Record<string, string> | null }) => {
  if (!labels) return <Dash />;
  const entries = Object.entries(labels);
  if (entries.length === 0) return <Dash />;
  return (
    <div className="label-list">
      {entries.map(([k, v]) => (
        <span key={k} className="label-chip">
          <span className="label-k">{k}</span>
          <span className="label-v">{v}</span>
        </span>
      ))}
    </div>
  );
};

export interface PaginatorProps {
  pageSize: PageSize;
  hasPrev: boolean;
  hasNext: boolean;
  onPrev: () => void;
  onNext: () => void;
  onPageSize: (n: PageSize) => void;
}

export function Paginator({
  pageSize, hasPrev, hasNext, onPrev, onNext, onPageSize,
}: PaginatorProps) {
  if (!hasPrev && !hasNext) return null;
  return (
    <div
      style={{
        display: 'flex',
        gap: 12,
        alignItems: 'center',
        margin: '8px 0 12px',
        fontSize: 13,
      }}
    >
      <button type="button" onClick={onPrev} disabled={!hasPrev}>← Prev</button>
      <button type="button" onClick={onNext} disabled={!hasNext}>Next →</button>
      <label style={{ marginLeft: 'auto', display: 'flex', gap: 6, alignItems: 'center' }}>
        Rows per page
        <select
          value={pageSize}
          onChange={(e) => onPageSize(Number(e.target.value) as PageSize)}
        >
          {PAGE_SIZE_OPTIONS.map((n) => (
            <option key={n} value={n}>{n}</option>
          ))}
        </select>
      </label>
    </div>
  );
}
