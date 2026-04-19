import { Link } from 'react-router-dom';
import type { AsyncState } from './hooks';

// Muted dash used where a field is null / absent.
export const Dash = () => <span className="muted">—</span>;

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

export const Code = ({ children }: { children: React.ReactNode }) => (
  <code className="inline-code">{children}</code>
);

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
