import { FormEvent, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import * as api from '../api';
import { useResource } from '../hooks';
import { canEdit, useMe } from '../me';
import { AsyncView, Dash } from '../components';
import { CuratedMetadataCard } from '../components/inventory/CuratedMetadataCard';

// ApplicationDetail is the per-application drill-down (ADR-0029 §3). It
// stacks cards in the established detail-page style: a header card, the
// reusable Ownership & context card, a Classification (DICT) card, a
// Members card with three sub-tables + add/unlink, and an EOL summary
// placeholder that lights up in Phase 5.

// The four SecNumCloud DICT axes, rendered in their French (EBIOS-RM)
// names per ADR-0008. The `key` maps to the Application/ApplicationPatch
// field.
const DICT_AXES: { key: keyof DictValues; label: string; abbr: string }[] = [
  { key: 'sec_disponibilite', label: 'Disponibilité', abbr: 'D' },
  { key: 'sec_integrite', label: 'Intégrité', abbr: 'I' },
  { key: 'sec_confidentialite', label: 'Confidentialité', abbr: 'C' },
  { key: 'sec_tracabilite', label: 'Traçabilité', abbr: 'T' },
];

interface DictValues {
  sec_disponibilite: number | null;
  sec_integrite: number | null;
  sec_confidentialite: number | null;
  sec_tracabilite: number | null;
}

export default function ApplicationDetail() {
  const { id = '' } = useParams();
  const me = useMe();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);

  const appState = useResource(() => api.getApplication(id), [id, nonce]);
  const membersState = useResource(() => api.listApplicationMembers(id), [id, nonce]);

  // 404 handling: a missing application renders a friendly message + a
  // link back to the list rather than the generic "Failed to load".
  if (appState.status === 'error') {
    const notFound =
      /not.?found/i.test(appState.error) || /\b404\b/.test(appState.error);
    if (notFound) {
      return (
        <>
          <div className="breadcrumb">
            <Link to="/applications">Applications</Link> / <span>not found</span>
          </div>
          <p className="muted empty">
            Application not found. <Link to="/applications">Back to the list</Link>.
          </p>
        </>
      );
    }
  }

  return (
    <>
      <div className="breadcrumb">
        <Link to="/applications">Applications</Link> / <span>this application</span>
      </div>
      <AsyncView state={appState}>
        {(app) => (
          <>
            <HeaderCard app={app} editable={canEdit(me)} onSaved={reload} />

            <CuratedMetadataCard
              values={{
                owner: app.owner,
                criticality: app.criticality,
                notes: app.notes,
                runbook_url: app.runbook_url,
                annotations: app.annotations,
              }}
              onSave={async (values) => {
                await api.patchApplication(app.id, {
                  owner: values.owner ?? undefined,
                  criticality: values.criticality ?? undefined,
                  notes: values.notes ?? undefined,
                  runbook_url: values.runbook_url ?? undefined,
                  annotations: values.annotations ?? undefined,
                });
              }}
              onSaved={reload}
            />

            <DictCard app={app} editable={canEdit(me)} onSaved={reload} />

            <MembersCard
              appId={app.id}
              membersState={membersState}
              editable={canEdit(me)}
              onChanged={reload}
            />

            <EolSummaryCard appId={app.id} />
          </>
        )}
      </AsyncView>
    </>
  );
}

// --- Header card ---------------------------------------------------------

function HeaderCard({
  app,
  editable,
  onSaved,
}: {
  app: api.Application;
  editable: boolean;
  onSaved: () => void;
}) {
  const [editing, setEditing] = useState(false);

  if (editing && editable) {
    return <HeaderForm app={app} onCancel={() => setEditing(false)} onSaved={() => {
      setEditing(false);
      onSaved();
    }} />;
  }

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h2 style={{ margin: 0 }}>{app.display_name || app.name}</h2>
        {editable && (
          <button type="button" className="primary" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      <div style={{ marginTop: '0.5rem' }}>
        <code>{app.name}</code>
        {app.application_block_name && (
          <Link
            to={`/applications?application_block_name=${encodeURIComponent(app.application_block_name)}`}
            className="pill"
            style={{ marginLeft: '0.5rem' }}
          >
            {app.application_block_name}
          </Link>
        )}
      </div>
      {app.description && <p style={{ marginTop: '0.5rem' }}>{app.description}</p>}
    </section>
  );
}

function HeaderForm({
  app,
  onCancel,
  onSaved,
}: {
  app: api.Application;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [displayName, setDisplayName] = useState(app.display_name ?? '');
  const [description, setDescription] = useState(app.description ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api.patchApplication(app.id, {
        display_name: displayName.trim(),
        description: description.trim(),
      });
      onSaved();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Edit application</h3>
      </div>
      <form className="admin-form" onSubmit={onSubmit}>
        <div>
          <label>Display name</label>
          <input
            type="text"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder={app.name}
            aria-label="Display name"
          />
        </div>
        <div style={{ marginTop: '0.75rem' }}>
          <label>Description</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={3}
            style={{ width: '100%' }}
            aria-label="Description"
          />
        </div>
        {error && <div className="error">{error}</div>}
        <div className="admin-form-actions">
          <button type="submit" className="primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="danger" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}

// --- Classification (DICT) card -----------------------------------------

function DictCard({
  app,
  editable,
  onSaved,
}: {
  app: api.Application;
  editable: boolean;
  onSaved: () => void;
}) {
  const [editing, setEditing] = useState(false);

  if (editing && editable) {
    return <DictForm app={app} onCancel={() => setEditing(false)} onSaved={() => {
      setEditing(false);
      onSaved();
    }} />;
  }

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Classification (DICT)</h3>
        {editable && (
          <button type="button" className="primary" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      <div className="dict-grid">
        {DICT_AXES.map((axis) => {
          const v = app[axis.key] as number | null | undefined;
          return (
            <div key={axis.key} className="dict-cell">
              <span className="dict-axis" title={axis.label}>
                {axis.abbr}
              </span>
              <span className={typeof v === 'number' ? `dict-value heat-${v}` : 'dict-value'}>
                {typeof v === 'number' ? v : '—'}
              </span>
              <span className="muted dict-label">{axis.label}</span>
            </div>
          );
        })}
      </div>
      {app.sec_notes ? (
        <pre className="curated-notes" style={{ marginTop: '0.75rem' }}>
          {app.sec_notes}
        </pre>
      ) : (
        <p className="muted" style={{ marginTop: '0.5rem' }}>
          No security notes recorded.
        </p>
      )}
    </section>
  );
}

const MAX_SEC_NOTES = 4096;

function DictForm({
  app,
  onCancel,
  onSaved,
}: {
  app: api.Application;
  onCancel: () => void;
  onSaved: () => void;
}) {
  // String state so an empty input can clear the axis; clamped to 0..4
  // before sending.
  const [axes, setAxes] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      DICT_AXES.map((a) => {
        const v = app[a.key] as number | null | undefined;
        return [a.key, typeof v === 'number' ? String(v) : ''];
      }),
    ),
  );
  const [secNotes, setSecNotes] = useState(app.sec_notes ?? '');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);

    const patch: api.ApplicationPatch = {};
    for (const a of DICT_AXES) {
      const raw = axes[a.key];
      if (raw === '') continue; // omit → leave unchanged
      const n = Number(raw);
      if (!Number.isInteger(n) || n < 0 || n > 4) {
        setError(`${a.label} must be an integer between 0 and 4.`);
        return;
      }
      (patch as Record<string, unknown>)[a.key] = n;
    }
    if (secNotes.length > MAX_SEC_NOTES) {
      setError(`Security notes must be ${MAX_SEC_NOTES} characters or fewer.`);
      return;
    }
    patch.sec_notes = secNotes;

    setBusy(true);
    try {
      await api.patchApplication(app.id, patch);
      onSaved();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Edit classification (DICT)</h3>
      </div>
      <form className="admin-form" onSubmit={onSubmit}>
        <div className="admin-form-row">
          {DICT_AXES.map((a) => (
            <div key={a.key}>
              <label>{a.label}</label>
              <input
                type="number"
                step={1}
                value={axes[a.key]}
                aria-label={a.label}
                onChange={(e) => setAxes((prev) => ({ ...prev, [a.key]: e.target.value }))}
              />
            </div>
          ))}
        </div>
        <div style={{ marginTop: '0.75rem' }}>
          <label>Security notes</label>
          <textarea
            value={secNotes}
            onChange={(e) => setSecNotes(e.target.value)}
            rows={4}
            maxLength={MAX_SEC_NOTES}
            style={{ width: '100%' }}
          />
          <small className="muted">
            {secNotes.length}/{MAX_SEC_NOTES}
          </small>
        </div>
        {error && <div className="error">{error}</div>}
        <div className="admin-form-actions">
          <button type="submit" className="primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save'}
          </button>
          <button type="button" className="danger" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}

// --- Members card --------------------------------------------------------

function MembersCard({
  appId,
  membersState,
  editable,
  onChanged,
}: {
  appId: string;
  membersState: ReturnType<typeof useResource<api.PagedResponse<api.ApplicationMember>>>;
  editable: boolean;
  onChanged: () => void;
}) {
  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Members</h3>
      </div>
      <AsyncView state={membersState}>
        {(page) => {
          const workloads = page.items.filter((m) => m.kind === 'workload');
          const vms = page.items.filter((m) => m.kind === 'virtual_machine');
          const vmApps = page.items.filter((m) => m.kind === 'vm_application');
          return (
            <>
              <MemberTable
                title="Workloads"
                members={workloads}
                linkTo={(m) => `/workloads/${m.id}`}
                parentLabel="Namespace"
                editable={editable}
                onUnlink={async (m) => {
                  await api.updateWorkload(m.id, { application_id: null });
                  onChanged();
                }}
              />
              {/* VMs do have a top-level `application_id` (cleared via
                  PATCH {"application_id": null}), and VM-application linkage
                  lives in the per-app entries of the VM's `applications` JSONB
                  array (ADR-0029). Both are intentionally managed from the VM
                  detail page in v1, so these sub-tables are read-only and link
                  out rather than offering an unlink button here. */}
              <MemberTable
                title="Virtual Machines"
                members={vms}
                linkTo={(m) => `/virtual-machines/${m.id}`}
                parentLabel="Cloud account"
                editable={false}
                onUnlink={async () => undefined}
                note="Manage VM links from the VM detail page."
              />
              <MemberTable
                title="VM applications"
                members={vmApps}
                linkTo={(m) => (m.parent?.id ? `/virtual-machines/${m.parent.id}` : null)}
                parentLabel="VM"
                editable={false}
                onUnlink={async () => undefined}
                note="Manage VM-application links from the VM detail page."
              />
            </>
          );
        }}
      </AsyncView>
      {editable && <AddMemberForm appId={appId} onAdded={onChanged} />}
    </section>
  );
}

function MemberTable({
  title,
  members,
  linkTo,
  parentLabel,
  editable,
  onUnlink,
  note,
}: {
  title: string;
  members: api.ApplicationMember[];
  linkTo: (m: api.ApplicationMember) => string | null;
  parentLabel: string;
  editable: boolean;
  onUnlink: (m: api.ApplicationMember) => Promise<void>;
  note?: string;
}) {
  return (
    <details className="vm-subsection" open>
      <summary>
        {title} <span className="muted">({members.length})</span>
      </summary>
      {members.length === 0 ? (
        <p className="muted empty">No {title.toLowerCase()} linked.</p>
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Name</th>
              <th>{parentLabel}</th>
              {editable && <th style={{ textAlign: 'right' }}>Actions</th>}
            </tr>
          </thead>
          <tbody>
            {members.map((m) => {
              const to = linkTo(m);
              return (
                <tr key={`${m.kind}:${m.id}`}>
                  <td>{to ? <Link to={to}>{m.name}</Link> : m.name}</td>
                  <td>{m.parent?.name || <Dash />}</td>
                  {editable && (
                    <td style={{ textAlign: 'right' }}>
                      <UnlinkButton member={m} onUnlink={onUnlink} />
                    </td>
                  )}
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {note && members.length > 0 && (
        <p className="muted" style={{ fontSize: 'var(--fs-sm)', marginTop: '0.25rem' }}>
          {note}
        </p>
      )}
    </details>
  );
}

function UnlinkButton({
  member,
  onUnlink,
}: {
  member: api.ApplicationMember;
  onUnlink: (m: api.ApplicationMember) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      className="danger"
      disabled={busy}
      onClick={async () => {
        setBusy(true);
        try {
          await onUnlink(member);
        } catch (err) {
          alert(err instanceof api.ApiError ? err.message : String(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      Unlink
    </button>
  );
}

// AddMemberForm is the v1 simplified link flow: a Workload name + a button
// that resolves the name and PATCHes the workload's application_id. No
// search dropdown — the operator already knows what they want to link.
// VMs + per-VM applications link from the VM detail page (their linkage
// lives in the VM's `applications` JSONB array, not a top-level FK), so
// they're out of scope for this v1 add flow. Richer search ergonomics
// land in a follow-up.
function AddMemberForm({ appId, onAdded }: { appId: string; onAdded: () => void }) {
  const [open, setOpen] = useState(false);
  const [workloadName, setWorkloadName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const linkWorkload = async () => {
    const name = workloadName.trim();
    if (!name) return;
    setBusy(true);
    setError(null);
    try {
      // Resolve the name to an id, then link. listWorkloads has no
      // server-side name filter in v1, so we scan the first page; the
      // backend rejects a non-existent id, and an unmatched name surfaces
      // a clear inline error.
      const page = await api.listWorkloads({ limit: 200 });
      const match = page.items.find((w) => w.name === name);
      if (!match) {
        setError(`No workload named "${name}" found.`);
        return;
      }
      await api.updateWorkload(match.id, { application_id: appId });
      setWorkloadName('');
      onAdded();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (!open) {
    return (
      <div className="admin-actions" style={{ marginTop: '0.75rem' }}>
        <button type="button" className="primary" onClick={() => setOpen(true)}>
          + Add member
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={(e) => e.preventDefault()} style={{ marginTop: '0.75rem' }}>
      <div className="admin-form-row">
        <div>
          <label>Workload name</label>
          <input
            type="text"
            value={workloadName}
            onChange={(e) => setWorkloadName(e.target.value)}
            placeholder="web"
            aria-label="Workload name"
          />
        </div>
        <div style={{ display: 'flex', alignItems: 'flex-end' }}>
          <button type="button" className="primary" onClick={linkWorkload} disabled={busy || !workloadName.trim()}>
            Link workload
          </button>
        </div>
      </div>
      <p className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        Link VMs and per-VM applications from the VM detail page.
      </p>
      {error && <div className="error">{error}</div>}
      <div className="admin-form-actions">
        <button type="button" onClick={() => setOpen(false)} disabled={busy}>
          Done
        </button>
      </div>
    </form>
  );
}

// --- End-of-life summary card -------------------------------------------

// eolStatusClass maps an aggregated eol_status to the shared pill classes
// (mirrors statusClass in EolDashboard.tsx). The aggregator can also emit
// "outdated" (workload image-versions behind latest), which we render as a
// warning, and "unknown" / unset as a neutral pill.
function eolStatusClass(status: string): string {
  switch (status) {
    case 'eol':
      return 'pill status-bad';
    case 'approaching_eol':
    case 'outdated':
      return 'pill status-warn';
    case 'supported':
      return 'pill status-ok';
    default:
      return 'pill';
  }
}

function eolStatusLabel(status: string): string {
  switch (status) {
    case 'eol':
      return 'End of Life';
    case 'approaching_eol':
      return 'Approaching EOL';
    case 'supported':
      return 'Supported';
    case 'outdated':
      return 'Outdated';
    default:
      return 'Unknown';
  }
}

function EolSummaryCard({ appId }: { appId: string }) {
  // Keep the try/catch so a transient endpoint error degrades to the empty
  // state rather than crashing the whole detail page.
  const state = useResource(async () => {
    try {
      return await api.applicationEOL(appId);
    } catch {
      return null;
    }
  }, [appId]);

  // Hide entirely if we somehow have no application id.
  if (!appId) return null;

  const rows = state.status === 'ready' && state.data ? state.data.items : [];

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>End-of-life summary</h3>
      </div>
      {rows.length === 0 ? (
        <p className="muted empty">No end-of-life signal for this application&apos;s members yet.</p>
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Product</th>
              <th>Cycle</th>
              <th>Status</th>
              <th>Latest available</th>
              <th>Sources</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.product}>
                <td>{r.product}</td>
                <td>{r.cycle || <Dash />}</td>
                <td>
                  <span className={eolStatusClass(r.eol_status)}>{eolStatusLabel(r.eol_status)}</span>
                </td>
                <td>{r.latest_available || <Dash />}</td>
                <td>
                  {r.sources.length === 0 ? (
                    <Dash />
                  ) : (
                    <span style={{ display: 'inline-flex', flexWrap: 'wrap', gap: '0.25rem' }}>
                      {r.sources.map((s) => (
                        <span key={`${s.kind}:${s.id}`} className="pill" title={s.kind}>
                          {s.name}
                        </span>
                      ))}
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
