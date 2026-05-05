// ClusterHistory — History tab component for the cluster detail page (ADR-0021 Phase 4).
// Fetches GET /v1/clusters/{id}/history and renders a reverse-chronological
// timeline with expandable diff rows.

import { useEffect, useState } from 'react';
import * as api from '../api';

// --- Time helpers -----------------------------------------------------------

function formatRelativeTime(isoStr: string): string {
  const diffMs = Date.now() - new Date(isoStr).getTime();
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffH = Math.floor(diffMin / 60);
  if (diffH < 24) return `${diffH}h ago`;
  return `${Math.floor(diffH / 24)}d ago`;
}

function formatAbsoluteTime(isoStr: string): string {
  return new Date(isoStr).toLocaleString();
}

// --- Change type pill -------------------------------------------------------

// Map change_type to the design-system status pill classes + labels.
const CHANGE_TYPE_META: Record<
  api.HistoryRow['change_type'],
  { cls: string; label: string }
> = {
  create:      { cls: 'pill status-ok',   label: 'Create' },
  update:      { cls: 'pill',             label: 'Update' },
  soft_delete: { cls: 'pill status-bad',  label: 'Deleted' },
  restore:     { cls: 'pill status-warn', label: 'Restored' },
};

function ChangeTypePill({ changeType }: { changeType: api.HistoryRow['change_type'] }) {
  const meta = CHANGE_TYPE_META[changeType] ?? { cls: 'pill', label: changeType };
  return (
    <span
      className={meta.cls}
      style={{ fontSize: '0.75rem', fontWeight: 600, whiteSpace: 'nowrap' }}
    >
      {meta.label}
    </span>
  );
}

// --- Diff cells -------------------------------------------------------------

function DiffPreview({ diff }: { diff: api.HistoryPatchOp[] }) {
  if (!diff?.length) {
    return <span className="muted" style={{ fontSize: '0.8rem' }}>no field changes</span>;
  }
  const fields = diff.map((op) => op.path.replace(/^\//, ''));
  const preview =
    fields.length <= 3
      ? fields.join(', ')
      : `${fields.slice(0, 3).join(', ')} +${fields.length - 3} more`;
  return (
    <span className="muted" style={{ fontSize: '0.8rem' }}>
      {diff.length} field{diff.length === 1 ? '' : 's'} changed: <em>{preview}</em>
    </span>
  );
}

function DiffExpanded({ diff }: { diff: api.HistoryPatchOp[] }) {
  if (!diff?.length) {
    return (
      <p className="muted" style={{ margin: '0.5rem 0 0', fontSize: '0.85rem' }}>
        No field-level diff available.
      </p>
    );
  }
  return (
    <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '0.85rem', marginTop: '0.5rem' }}>
      <thead>
        <tr>
          {['Field', 'Before', 'After'].map((h) => (
            <th key={h} style={{ textAlign: 'left', paddingBottom: 4, color: 'var(--color-muted)', fontWeight: 500 }}>
              {h}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {diff.map((op) => (
          <tr key={op.path} style={{ borderTop: '1px solid var(--border)' }}>
            <td style={{ padding: '4px 8px 4px 0', fontFamily: 'var(--font-mono)', fontWeight: 600 }}>
              {op.path.replace(/^\//, '')}
            </td>
            <td style={{ padding: '4px 8px', color: 'var(--bad-fg)' }}>
              {op.from !== undefined
                ? <code>{JSON.stringify(op.from)}</code>
                : <span className="muted">—</span>}
            </td>
            <td style={{ padding: '4px 8px', color: 'var(--ok-fg)' }}>
              {op.to !== undefined
                ? <code>{JSON.stringify(op.to)}</code>
                : <span className="muted">—</span>}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// --- Single history row item ------------------------------------------------

function HistoryRowItem({ row }: { row: api.HistoryRow }) {
  const [expanded, setExpanded] = useState(false);
  const hasDiff = (row.diff?.length ?? 0) > 0;
  const actorLabel = row.actor_kind
    ? `${row.actor_kind}:${row.actor_id ?? 'system'}`
    : 'system';

  return (
    <div style={{ borderBottom: '1px solid var(--border)', padding: '0.75rem 0' }}>
      <div
        style={{ display: 'flex', alignItems: 'center', gap: '0.75rem', flexWrap: 'wrap', cursor: hasDiff ? 'pointer' : 'default' }}
        role={hasDiff ? 'button' : undefined}
        tabIndex={hasDiff ? 0 : undefined}
        aria-expanded={hasDiff ? expanded : undefined}
        onClick={() => hasDiff && setExpanded((e) => !e)}
        onKeyDown={(e) => { if (hasDiff && (e.key === 'Enter' || e.key === ' ')) setExpanded((p) => !p); }}
      >
        <span title={formatAbsoluteTime(row.valid_from)} style={{ fontSize: '0.85rem', whiteSpace: 'nowrap' }}>
          {formatRelativeTime(row.valid_from)}
        </span>
        <ChangeTypePill changeType={row.change_type} />
        <span className="muted" style={{ fontSize: '0.8rem' }}>{actorLabel}</span>
        <DiffPreview diff={row.diff} />
        {hasDiff && (
          <span className="muted" style={{ fontSize: '0.75rem', marginLeft: 'auto' }}>
            {expanded ? '▲' : '▼'}
          </span>
        )}
      </div>
      {expanded && <DiffExpanded diff={row.diff} />}
    </div>
  );
}

// --- Main exported component ------------------------------------------------

export function ClusterHistory({ clusterId }: { clusterId: string }) {
  const [rows, setRows] = useState<api.HistoryRow[] | null>(null);
  const [nextCursor, setNextCursor] = useState('');
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .listEntityHistory('clusters', clusterId)
      .then((page) => {
        if (cancelled) return;
        setRows(page.items ?? []);
        setNextCursor(page.next_cursor ?? '');
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof api.ApiError && err.status === 503) {
          setError('History is disabled in settings. Enable it in Admin → Settings.');
        } else {
          setError('Failed to load history.');
        }
        setRows([]);
      });
    return () => { cancelled = true; };
  }, [clusterId]);

  const handleLoadMore = async () => {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await api.listEntityHistory('clusters', clusterId, nextCursor);
      setRows((prev) => [...(prev ?? []), ...(page.items ?? [])]);
      setNextCursor(page.next_cursor ?? '');
    } catch {
      // silently ignore — button remains for retry
    } finally {
      setLoadingMore(false);
    }
  };

  if (rows === null) {
    return <p className="muted" style={{ marginTop: '1rem' }}>Loading history…</p>;
  }
  if (error) {
    return <p style={{ marginTop: '1rem', color: 'var(--bad-fg)' }}>{error}</p>;
  }
  if (rows.length === 0) {
    return <p className="muted" style={{ marginTop: '1rem' }}>No history recorded yet.</p>;
  }

  return (
    <div style={{ marginTop: '1rem' }}>
      {rows.map((row) => (
        <HistoryRowItem key={row.history_id} row={row} />
      ))}
      {nextCursor && (
        <button style={{ marginTop: '0.75rem' }} onClick={handleLoadMore} disabled={loadingMore}>
          {loadingMore ? 'Loading…' : 'Load older'}
        </button>
      )}
    </div>
  );
}
