import { useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, SectionTitle } from '../../components';
import { ExtractButton } from '../../components/ExtractButton';
import { TruncationBanner } from '../../components/TruncationBanner';

// ClassificationHeatmap is the admin DICT portfolio view (ADR-0029 §3,
// closing ADR-0008 IMP-007). It lists every Application whose maximum DICT
// axis is at or above a threshold, heat-coloured per axis so an auditor can
// scan the security posture of the estate at a glance. Admin scope only —
// the route is gated in App.tsx + AdminLayout, matching the other admin
// tabs.

// The four SecNumCloud DICT axes, in EBIOS-RM order (ADR-0008). `key` maps
// to the Application field; the abbreviation labels the column. Mirrors
// DICT_AXES in ApplicationDetail.tsx.
const AXES: { key: keyof DictAxes; abbr: string; label: string }[] = [
  { key: 'sec_disponibilite', abbr: 'D', label: 'Disponibilité' },
  { key: 'sec_integrite', abbr: 'I', label: 'Intégrité' },
  { key: 'sec_confidentialite', abbr: 'C', label: 'Confidentialité' },
  { key: 'sec_tracabilite', abbr: 'T', label: 'Traçabilité' },
];

interface DictAxes {
  sec_disponibilite?: number | null;
  sec_integrite?: number | null;
  sec_confidentialite?: number | null;
  sec_tracabilite?: number | null;
}

const SEC_NOTES_EXCERPT = 80;

export default function ClassificationHeatmap() {
  // dict_min threshold (0..4). Default 3 — the SecNumCloud "high" band.
  // Changing it is the only dep, so useResource refetches with the new
  // server-side filter.
  const [threshold, setThreshold] = useState(3);
  const [block, setBlock] = useState('');
  const [truncated, setTruncated] = useState(false);

  const state = useResource(
    () => api.listApplications({ dict_min: threshold, limit: 200 }),
    [threshold],
  );

  return (
    <>
      <h2>Classification (DICT) heat-map</h2>
      <p className="muted" style={{ marginBottom: '1rem' }}>
        Applications at or above a DICT classification level, coloured by axis
        severity (SecNumCloud / EBIOS-RM). Inherited from members where an
        application has no value of its own.
      </p>

      <TruncationBanner visible={truncated} />

      <div className="admin-actions" style={{ alignItems: 'flex-end', gap: '1rem' }}>
        <label>
          Minimum classification level
          <select
            aria-label="Minimum classification level"
            value={threshold}
            onChange={(e) => setThreshold(Number(e.target.value))}
            style={{ marginLeft: '0.5rem' }}
          >
            {[0, 1, 2, 3, 4].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <ExtractButton
          label="Export CSV"
          onExtract={(format) =>
            api.extractApplications({ format, dict_min: threshold })
          }
          onTruncation={setTruncated}
        />
      </div>

      <AsyncView state={state}>
        {(resp) => {
          const needle = block.trim().toLowerCase();
          const apps = needle
            ? resp.items.filter((a) =>
                (a.application_block_name ?? '').toLowerCase().includes(needle),
              )
            : resp.items;

          return (
            <>
              <div style={{ margin: '0.75rem 0' }}>
                <input
                  type="text"
                  value={block}
                  placeholder="Filter by block…"
                  aria-label="Filter by block"
                  onChange={(e) => setBlock(e.target.value)}
                />
                {block && (
                  <button
                    className="link-btn"
                    style={{ marginLeft: '0.5rem' }}
                    onClick={() => setBlock('')}
                  >
                    clear
                  </button>
                )}
              </div>

              <SectionTitle count={apps.length}>Applications</SectionTitle>

              {apps.length === 0 ? (
                <p className="muted empty">
                  No applications at or above classification level {threshold}.
                </p>
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th>Application</th>
                      <th>Block</th>
                      {AXES.map((a) => (
                        <th key={a.key} title={a.label} style={{ textAlign: 'center' }}>
                          {a.abbr}
                        </th>
                      ))}
                      <th>Security notes</th>
                    </tr>
                  </thead>
                  <tbody>
                    {apps.map((app) => (
                      <AppRow key={app.id} app={app} />
                    ))}
                  </tbody>
                </table>
              )}
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function AppRow({ app }: { app: api.Application }) {
  return (
    <tr>
      <td>
        <Link to={`/applications/${app.id}`}>{app.display_name || app.name}</Link>
      </td>
      <td>
        {app.application_block_name ? (
          <span className="pill">{app.application_block_name}</span>
        ) : (
          <span className="muted">—</span>
        )}
      </td>
      {AXES.map((axis) => {
        const v = app[axis.key] as number | null | undefined;
        // Mirror ApplicationDetail.tsx: a recorded axis gets a heat-N cell;
        // a null/absent axis renders a neutral em-dash, NOT an unstyled or
        // heat-null cell.
        return (
          <td key={axis.key} style={{ textAlign: 'center' }}>
            {typeof v === 'number' ? (
              <span className={`dict-value heat-${v}`}>{v}</span>
            ) : (
              <span className="dict-value muted">—</span>
            )}
          </td>
        );
      })}
      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        {excerpt(app.sec_notes)}
      </td>
    </tr>
  );
}

function excerpt(notes: string | null | undefined): string {
  if (!notes) return '';
  const flat = notes.replace(/\s+/g, ' ').trim();
  return flat.length > SEC_NOTES_EXCERPT
    ? `${flat.slice(0, SEC_NOTES_EXCERPT)}…`
    : flat;
}
