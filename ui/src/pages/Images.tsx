import { useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { isAdmin, useMe } from '../me';
import { AsyncView } from '../components';
import { useDebouncedValue } from '../hooks';

// Images is the inventory page for container image versions tracked by the
// image-versions enricher. Each row is one image_repo; the variant count and
// registry are shown at a glance, with an error indicator when any variant
// has a non-null last_error.

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
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [refreshMsg, setRefreshMsg] = useState<string | null>(null);
  const [nonce, setNonce] = useState(0);

  // Debounce the search input so we don't fire a request on every keystroke.
  const debouncedSearch = useDebouncedValue(searchInput, 300);

  const filter: api.ImageVersionListFilter = {
    image_repo: debouncedSearch || undefined,
    has_error: errorsOnly ? true : undefined,
  };

  const listState = useResource(
    () => api.listImageVersions(filter),
    [debouncedSearch, errorsOnly, nonce],
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

  return (
    <>
      <h2>Container images</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Image version inventory tracked by the enricher. Click a row to see per-variant detail.
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
        <label className="vm-filter-checkbox" style={{ alignSelf: 'flex-end' }}>
          <input
            type="checkbox"
            checked={errorsOnly}
            onChange={(e) => setErrorsOnly(e.target.checked)}
          />
          <span>Errors only</span>
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
          if (items.length === 0) {
            return (
              <p className="muted empty">
                No image versions found{errorsOnly ? ' with errors' : ''}.
              </p>
            );
          }

          // Summary counts for the top cards.
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

          return (
            <>
              <div className="eol-summary" style={{ marginBottom: '1rem' }}>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{items.length}</span>
                  <span className="eol-summary-label">Repos</span>
                </div>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{totalVariants}</span>
                  <span className="eol-summary-label">Variants</span>
                </div>
                <div className={`eol-summary-card${errorCount > 0 ? ' eol-bad' : ' eol-ok'}`}>
                  <span className="eol-summary-count">{errorCount}</span>
                  <span className="eol-summary-label">With errors</span>
                </div>
                <div className="eol-summary-card">
                  <span className="eol-summary-count">{checkedRepos}</span>
                  <span className="eol-summary-label">Checked</span>
                </div>
              </div>

              <table className="entities">
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
                      (acc, v) => (v.last_checked_at > (acc ?? '') ? v.last_checked_at : acc),
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
