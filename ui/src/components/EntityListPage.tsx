// EntityListPage factors the top-level list-page pattern shared by the
// nine Lists.tsx pages (ADR-0042 phase 2): heading, uniform SearchInput,
// Paginator, loading/error/empty tri-state, and an `entities` table whose
// sortable headers drive server-side sorting through URL-synced controls.
// Columns without a sortKey (computed/lookup columns) render plain <th>.
import * as api from '../api';
import { usePagedList, useListControls } from '../hooks';
import { Empty, Paginator } from '../components';
import { useEntityTable } from './column_filters';
import { SearchInput } from './SearchInput';
import { SortHeader } from './SortHeader';

export interface EntityColumn<T> {
  key: string;
  label: React.ReactNode;
  // Server sort key from the entity's ADR-0042 allowlist. Omitted for
  // computed/lookup columns — those render with no click affordance.
  sortKey?: string;
  render: (item: T) => React.ReactNode;
}

export interface EntityListPageProps<T> {
  title: string;
  icon: React.ReactNode;
  storageKey: string;
  emptyMessage: string;
  fetchPage: (
    params: api.ListControlParams,
    cursor: string | undefined,
    limit: number,
  ) => Promise<api.PagedResponse<T>>;
  columns: EntityColumn<T>[];
  rowKey: (item: T) => string;
  errorRenderer?: (err: unknown) => React.ReactNode | undefined;
  /** Extra filter controls rendered beside the search input. */
  extraFilters?: React.ReactNode;
  /** Reload dependencies for caller-owned filters (e.g. a stale toggle). */
  extraDeps?: unknown[];
}

export function EntityListPage<T>({
  title,
  icon,
  storageKey,
  emptyMessage,
  fetchPage,
  columns,
  rowKey,
  errorRenderer,
  extraFilters,
  extraDeps,
}: EntityListPageProps<T>) {
  const controls = useListControls();
  const list = usePagedList<T>(
    (cursor, limit) => fetchPage(controls.params, cursor, limit),
    [...controls.deps, ...(extraDeps ?? [])],
  );
  const tableRef = useEntityTable(storageKey);

  return (
    <>
      <h2>
        {icon} {title}
      </h2>
      <div className="vm-filters">
        <SearchInput value={controls.nameInput} onChange={controls.setNameInput} />
        {extraFilters}
      </div>
      <Paginator
        pageSize={list.pageSize}
        hasPrev={list.hasPrev}
        hasNext={list.hasNext}
        onPrev={list.prev}
        onNext={list.next}
        onPageSize={list.setPageSize}
      />
      {list.loading ? (
        <p className="loading">Loading…</p>
      ) : list.error ? (
        // errorRenderer receives the original thrown value (errorCause)
        // so it can branch on typed errors; the fallback div shows the
        // flattened message.
        errorRenderer?.(list.errorCause ?? list.error) ?? (
          <div className="error">Failed to load: {list.error}</div>
        )
      ) : list.items.length === 0 ? (
        <Empty message={emptyMessage} />
      ) : (
        <div className="table-wrap">
          <table className="entities" ref={tableRef}>
            <thead>
              <tr>
                {columns.map((c) =>
                  c.sortKey ? (
                    <SortHeader
                      key={c.key}
                      label={c.label}
                      sortKey={c.sortKey}
                      activeKey={controls.sort}
                      asc={controls.order === 'asc'}
                      onToggle={controls.toggleSort}
                    />
                  ) : (
                    <th key={c.key}>{c.label}</th>
                  ),
                )}
              </tr>
            </thead>
            <tbody>
              {list.items.map((item) => (
                <tr key={rowKey(item)}>
                  {columns.map((c) => (
                    <td key={c.key}>{c.render(item)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
