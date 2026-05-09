import { useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { isAdmin, useMe } from '../me';
import { AsyncView } from '../components';
import { useDebouncedValue } from '../hooks';
import { useEntityTable } from '../components/column_filters';

// Images is the inventory page for container image versions tracked by the
// image-versions enricher. Each row is one image_repo; the variant count and
// registry are shown at a glance, with an error indicator when any variant
// has a non-null last_error.
//
// Filtering composes two layers, mirroring other panels:
//   1. Server-side: image_repo substring (debounced search) + has_error
//      (driven by clickable summary cards → 'all' / 'errors' / 'ok').
//   2. Client-side: per-column funnel filters via useEntityTable. The
//      header funnel pickers narrow the rendered page without an extra
//      round-trip — they apply on top of whatever the server returned.

type StatusFilter = 'all' | 'errors' | 'ok';

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function Images() {
  const me = useMe();
  const admin = isAdmin(me);

  const [searchInput, setSearchInput] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [refreshing, setRefreshing] = useState(false);
  const [refreshMsg, setRefreshMsg] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  // Wires per-column funnel + resizable header to the table by ref. Same
  // storage key prefix as Lists pages so the UX feels consistent.
  const tableRef = useEntityTable('lists.images');

  // Debounce the search input so we don't fire a request on every keystroke.
  const debouncedSearch = useDebouncedValue(searchInput, 300);

  const filter: api.ImageVersionListFilter = {
    image_repo: debouncedSearch || undefined,
    has_error:
      statusFilter === 'errors'
        ? true
        : statusFilter === 'ok'
          ? false
          : undefined,
  };

  const listState = useResource(
    () => api.listImageVersions(filter),
    [debouncedSearch, statusFilter, nonce],
  );

  const onRefresh = async () => {
    setRefreshing(true);
    setRefreshMsg(null);
    try {
      const r = await api.refreshImageVersions();
      if (r.already_running) {
        setRefreshMsg('A refresh is already running — results will appear shortly.');
      } else {
        setRefreshMsg('Refresh queued. Results will appear in a few seconds.');
        // Give the enricher a few seconds to complete then reload.
        setTimeout(() => setNonce((n) => n + 1), 4000);
      }
    } catch (e) {
      setRefreshMsg(`Refresh failed: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setRefreshing(false);
    }
  };

  // Toggle behavior for the summary cards. Clicking a "filtering" card a
  // second time clears it (returns to 'all'); clicking the totals cards
  // (Repos / Variants) always clears the filter.
  const onCardClick = (target: StatusFilter) => {
    if (target === 'all') {
      setStatusFilter('all');
      return;
    }
    setStatusFilter((prev) => (prev === target ? 'all' : target));
  };

  return (
    <>
      <h2>Container images</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Image version inventory tracked by the enricher. Click a row to see per-variant detail. Click a summary card to filter; click a column header funnel to narrow further.
      </p>

      <div className="vm-search" style={{ marginBottom: '0.75rem' }}>
        <label>
          <span>Image repo</span>
          <input
            type="search"
            value={searchInput}
            placeholder="e.g. library/nginx"
            onChange={(e) => setSearchInput(e.target.value)}
          />
        </label>
        {admin && (
          <div className="vm-filter-actions" style={{ alignSelf: 'flex-end' }}>
            <button
              type="button"
              className="primary"
              onClick={onRefresh}
              disabled={refreshing}
            >
              {refreshing ? 'Refreshing…' : 'Refresh now'}
            </button>
          </div>
        )}
      </div>

      {refreshMsg && (
        <p className="muted" style={{ marginBottom: '0.75rem' }}>
          {refreshMsg}
        </p>
      )}

      <AsyncView state={listState}>
        {(page) => {
          const items = page.items;

          // Summary counts for the top cards. Errored / checked are
          // computed from the items currently in the page so they reflect
          // the active server-side filter.
          let totalVariants = 0;
          let errorCount = 0;
          let checkedRepos = 0;
          for (const iv of items) {
            totalVariants += iv.variants.length;
            const hasErr = iv.variants.some((v) => v.last_error);
            if (hasErr) errorCount++;
            const latestCheck = iv.variants.reduce<string | null>(
              (acc, v) => (v.last_checked_at > (acc ?? '') ? v.last_checked_at : acc),
              null,
            );
            if (latestCheck) checkedRepos++;
          }

          const cardClass = (target: StatusFilter, colorClass: string) =>
            `eol-summary-card ${colorClass}${statusFilter === target ? ' eol-active' : ''}`;

          return (
            <>
              <div className="eol-summary" style={{ marginBottom: '1rem' }}>
                <div
                  className={cardClass('all', '')}
                  onClick={() => onCardClick('all')}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') onCardClick('all');
                  }}
                >
                  <span className="eol-summary-count">{items.length}</span>
                  <span className="eol-summary-label">Repos</span>
                </div>
                <div
                  className={cardClass('all', '')}
                  onClick={() => onCardClick('all')}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') onCardClick('all');
                  }}
                >
                  <span className="eol-summary-count">{totalVariants}</span>
                  <span className="eol-summary-label">Variants</span>
                </div>
                <div
                  className={cardClass('errors', errorCount > 0 ? 'eol-bad' : 'eol-ok')}
                  onClick={() => onCardClick('errors')}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') onCardClick('errors');
                  }}
                >
                  <span className="eol-summary-count">{errorCount}</span>
                  <span className="eol-summary-label">With errors</span>
                </div>
                <div
                  className={cardClass('ok', 'eol-ok')}
                  onClick={() => onCardClick('ok')}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') onCardClick('ok');
                  }}
                >
                  <span className="eol-summary-count">{checkedRepos}</span>
                  <span className="eol-summary-label">Checked</span>
                </div>
              </div>

              {statusFilter !== 'all' && (
                <p style={{ marginBottom: '0.75rem' }}>
                  Filtering:{' '}
                  <span
                    className={`pill ${statusFilter === 'errors' ? 'status-bad' : 'status-ok'}`}
                  >
                    {statusFilter === 'errors' ? 'with errors' : 'ok'}
                  </span>{' '}
                  <button
                    className="link-btn"
                    type="button"
                    onClick={() => setStatusFilter('all')}
                  >
                    clear
                  </button>
                </p>
              )}

              {items.length === 0 ? (
                <p className="muted empty">
                  No image versions found
                  {statusFilter === 'errors'
                    ? ' with errors'
                    : statusFilter === 'ok'
                      ? ' without errors'
                      : ''}
                  .
                </p>
              ) : (
                <table className="entities" ref={tableRef}>
                  <thead>
                    <tr>
                      <th>Image repo</th>
                      <th>Registry</th>
                      <th>Variants</th>
                      <th>Last checked</th>
                      <th>Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((iv) => {
                      const hasErr = iv.variants.some((v) => v.last_error);
                      const lastChecked = iv.variants.reduce<string | null>(
                        (acc, v) =>
                          v.last_checked_at > (acc ?? '') ? v.last_checked_at : acc,
                        null,
                      );
                      return (
                        <tr key={iv.image_repo}>
                          <td>
                            <Link to={`/images/${encodeURIComponent(iv.image_repo)}`}>
                              <strong>{iv.image_repo}</strong>
                            </Link>
                          </td>
                          <td>
                            <code>{iv.registry}</code>
                          </td>
                          <td>{iv.variants.length}</td>
                          <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                            {formatTs(lastChecked) || '—'}
                          </td>
                          <td>
                            {hasErr ? (
                              <span className="pill status-bad">error</span>
                            ) : (
                              <span className="pill status-ok">ok</span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              )}
              {page.next_cursor && (
                <p className="muted" style={{ marginTop: '0.75rem' }}>
                  More results available — refine filters to narrow the page.
                </p>
              )}
            </>
          );
        }}
      </AsyncView>
    </>
  );
}
