import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../api';
import { useResource, useDebouncedValue } from '../hooks';
import { AsyncView, Dash } from '../components';

// Applications is the top-level list page for the ADR-0029 first-class
// Application entity. It mirrors the VirtualMachines page shape: a
// toolbar of filters above a table. The default view groups rows by
// application_block_name (with an "Unblocked" group last); a toggle
// flips to a flat alphabetical sort. Cursor pagination via "Load more".

// dictMax returns the maximum DICT axis value across the four axes, or
// null when none are recorded. The list badge renders "DICT max: N".
export function dictMax(a: api.Application): number | null {
  const axes = [
    a.sec_disponibilite,
    a.sec_integrite,
    a.sec_confidentialite,
    a.sec_tracabilite,
  ].filter((v): v is number => typeof v === 'number');
  if (axes.length === 0) return null;
  return Math.max(...axes);
}

// memberSummary renders "5 workloads / 2 VMs / 3 VM-apps".
function memberSummary(c: api.ApplicationMemberCounts): string {
  return `${c.workloads} workloads · ${c.virtual_machines} VMs · ${c.vm_applications} VM-apps`;
}

// Sentinel key for apps with no block. Distinct from any real (lowercased)
// block name; the group ordering below always sorts it last regardless of
// the literal value.
const UNBLOCKED = '__unblocked__';

export default function Applications() {
  // Typed filter inputs — debounced before they reach the fetcher so a
  // fast typist doesn't fan out a request per keystroke.
  const [nameInput, setNameInput] = useState('');
  const [blockInput, setBlockInput] = useState('');
  const [criticalityInput, setCriticalityInput] = useState('');
  const [hasDict, setHasDict] = useState<'any' | 'yes' | 'no'>('any');
  const [dictMin, setDictMin] = useState<number>(0);
  const [grouped, setGrouped] = useState(true);

  // Accumulated pages — "Load more" appends rather than replacing so the
  // operator keeps scroll context. Reset whenever a filter changes.
  const [extraPages, setExtraPages] = useState<api.Application[]>([]);
  const [moreCursor, setMoreCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);

  const name = useDebouncedValue(nameInput.trim(), 250);
  const block = useDebouncedValue(blockInput.trim(), 250);
  const criticality = useDebouncedValue(criticalityInput.trim(), 250);

  const filter: api.ApplicationListFilter = {
    limit: 50,
    name: name || undefined,
    application_block_name: block || undefined,
    criticality: criticality || undefined,
    has_dict: hasDict === 'any' ? undefined : hasDict === 'yes',
    dict_min: dictMin > 0 ? dictMin : undefined,
  };

  const state = useResource(
    async () => {
      // A fresh filter resets any accumulated "Load more" pages.
      setExtraPages([]);
      const page = await api.listApplications(filter);
      setMoreCursor(page.next_cursor ?? null);
      return page;
    },
    [name, block, criticality, hasDict, dictMin],
  );

  const loadMore = async () => {
    if (!moreCursor) return;
    setLoadingMore(true);
    try {
      const page = await api.listApplications({ ...filter, cursor: moreCursor });
      setExtraPages((prev) => [...prev, ...page.items]);
      setMoreCursor(page.next_cursor ?? null);
    } finally {
      setLoadingMore(false);
    }
  };

  return (
    <>
      <h2>Applications</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Business systems spanning Kubernetes workloads + cloud VMs (ADR-0029).
      </p>

      <div className="vm-filters">
        <label>
          <span>Name</span>
          <input
            type="search"
            value={nameInput}
            placeholder="prefix or substring"
            onChange={(e) => setNameInput(e.target.value)}
          />
        </label>
        <label>
          <span>Application block</span>
          <input
            type="search"
            value={blockInput}
            placeholder="block name"
            onChange={(e) => setBlockInput(e.target.value)}
          />
        </label>
        <label>
          <span>Criticality</span>
          <input
            type="search"
            value={criticalityInput}
            placeholder="critical / high / ..."
            onChange={(e) => setCriticalityInput(e.target.value)}
          />
        </label>
        <label>
          <span>Has DICT</span>
          <select
            value={hasDict}
            onChange={(e) => setHasDict(e.target.value as 'any' | 'yes' | 'no')}
          >
            <option value="any">Any</option>
            <option value="yes">Yes</option>
            <option value="no">No</option>
          </select>
        </label>
        <label>
          <span>DICT min</span>
          <select value={dictMin} onChange={(e) => setDictMin(Number(e.target.value))}>
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <label className="vm-filter-checkbox">
          <button type="button" onClick={() => setGrouped((g) => !g)}>
            {grouped ? 'Flat sort' : 'Group by block'}
          </button>
        </label>
      </div>

      <AsyncView state={state}>
        {(firstPage) => {
          const all = [...firstPage.items, ...extraPages];
          if (all.length === 0) {
            return (
              <p className="muted empty">
                No applications yet — create one from a workload or VM detail page.
              </p>
            );
          }
          return (
            <>
              {grouped ? (
                <GroupedTable apps={all} />
              ) : (
                <FlatTable
                  apps={[...all].sort((a, b) =>
                    (a.display_name || a.name).toLowerCase().localeCompare(
                      (b.display_name || b.name).toLowerCase(),
                    ),
                  )}
                />
              )}
              {moreCursor && (
                <div style={{ marginTop: '0.75rem' }}>
                  <button type="button" onClick={loadMore} disabled={loadingMore}>
                    {loadingMore ? 'Loading...' : 'Load more'}
                  </button>
                </div>
              )}
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function GroupedTable({ apps }: { apps: api.Application[] }) {
  // Group case-insensitively on the block name; unblocked apps land in a
  // dedicated bucket keyed by UNBLOCKED so it can sort last.
  const groups = useMemo(() => {
    const m = new Map<string, { label: string; apps: api.Application[] }>();
    for (const a of apps) {
      const raw = a.application_block_name?.trim();
      const key = raw ? raw.toLowerCase() : UNBLOCKED;
      const label = raw || 'Unblocked';
      if (!m.has(key)) m.set(key, { label, apps: [] });
      m.get(key)!.apps.push(a);
    }
    return Array.from(m.entries())
      .sort(([ka], [kb]) => {
        if (ka === UNBLOCKED) return 1;
        if (kb === UNBLOCKED) return -1;
        return ka.localeCompare(kb);
      })
      .map(([, v]) => v);
  }, [apps]);

  return (
    <>
      {groups.map((g) => (
        <details key={g.label} open className="vm-subsection">
          <summary>
            {g.label} <span className="muted">({g.apps.length})</span>
          </summary>
          <FlatTable apps={g.apps} />
        </details>
      ))}
    </>
  );
}

function FlatTable({ apps }: { apps: api.Application[] }) {
  return (
    <div className="table-wrap">
      <table className="entities">
        <thead>
          <tr>
            <th>Name</th>
            <th>Block</th>
            <th>Owner</th>
            <th>Criticality</th>
            <th>DICT</th>
            <th>Members</th>
            <th>Runbook</th>
          </tr>
        </thead>
        <tbody>
          {apps.map((a) => {
            const dm = dictMax(a);
            return (
              <tr key={a.id}>
                <td>
                  <Link to={`/applications/${a.id}`}>
                    <strong>{a.display_name || a.name}</strong>
                  </Link>
                  {a.display_name && a.display_name !== a.name && (
                    <div className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                      {a.name}
                    </div>
                  )}
                </td>
                <td>
                  {a.application_block_name ? (
                    <Link
                      to={`/applications?application_block_name=${encodeURIComponent(a.application_block_name)}`}
                      className="pill"
                    >
                      {a.application_block_name}
                    </Link>
                  ) : (
                    <Dash />
                  )}
                </td>
                <td>{a.owner || <Dash />}</td>
                <td>{a.criticality ? <span className="pill">{a.criticality}</span> : <Dash />}</td>
                <td>
                  {dm === null ? (
                    <span className="muted">no DICT</span>
                  ) : (
                    <span className={`pill heat-${dm}`}>DICT max: {dm}</span>
                  )}
                </td>
                <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                  {memberSummary(a.member_counts)}
                </td>
                <td>
                  {a.runbook_url ? (
                    <a
                      href={a.runbook_url}
                      target="_blank"
                      rel="noreferrer"
                      title={a.runbook_url}
                      aria-label="Open runbook"
                    >
                      &#128279;
                    </a>
                  ) : (
                    <Dash />
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
