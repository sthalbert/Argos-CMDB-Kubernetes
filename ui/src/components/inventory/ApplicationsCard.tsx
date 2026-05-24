import { FormEvent, useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../../api';
import { canEdit, useMe } from '../../me';
import { Dash, Empty, SectionTitle } from '../../components';

// ApplicationsCard renders the operator-declared applications running on
// a VM (ADR-0019). Each row is a (product, version, name?, notes?) tuple
// stamped server-side with added_at / added_by. The EOL enricher reads
// this list and writes longue-vue.io/eol.<product> annotations, so editing
// the card directly drives the lifecycle scan.

export function ApplicationsCard({
  applications,
  onSave,
  onSaved,
}: {
  applications?: api.VMApplication[] | null;
  onSave: (apps: api.VMApplication[]) => Promise<void>;
  onSaved: () => void;
}) {
  const me = useMe();
  const [editing, setEditing] = useState(false);
  const list = applications ?? [];

  if (editing && canEdit(me)) {
    return (
      <ApplicationsForm
        initial={list}
        onSave={onSave}
        onCancel={() => setEditing(false)}
        onSaved={() => {
          setEditing(false);
          onSaved();
        }}
      />
    );
  }

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Applications</h3>
        {canEdit(me) && (
          <button type="button" className="primary" onClick={() => setEditing(true)}>
            Edit
          </button>
        )}
      </div>
      {list.length === 0 ? (
        <Empty
          message={
            canEdit(me)
              ? 'No applications declared. Use Edit to record what runs on this VM (Vault, DNS, …) — the EOL scanner uses this to track product lifecycle.'
              : 'No applications declared.'
          }
        />
      ) : (
        <table className="entities">
          <thead>
            <tr>
              <th>Product</th>
              <th>Version</th>
              <th>Instance name</th>
              <th>Linked application</th>
              <th>Notes</th>
              <th>Added</th>
            </tr>
          </thead>
          <tbody>
            {list.map((a, i) => (
              <tr key={`${a.product}|${a.version}|${a.name ?? ''}|${i}`}>
                <td>
                  <code>{a.product}</code>
                </td>
                <td>
                  <code>{a.version}</code>
                </td>
                <td>{a.name ? <code>{a.name}</code> : <Dash />}</td>
                <td>
                  {a.application_id ? (
                    <Link to={`/applications/${a.application_id}`} className="pill">
                      {a.application_name ?? `${a.application_id.slice(0, 8)}…`}
                    </Link>
                  ) : (
                    <Dash />
                  )}
                </td>
                <td>
                  {a.notes ? (
                    <span style={{ whiteSpace: 'pre-wrap' }}>{a.notes}</span>
                  ) : (
                    <Dash />
                  )}
                </td>
                <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                  {fmt(a.added_at)} by {a.added_by}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function fmt(ts?: string): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleDateString();
  } catch {
    return ts;
  }
}

interface DraftApp {
  product: string;
  version: string;
  name: string;
  notes: string;
  // ADR-0029 per-entry linkage. application_id is the soft pointer; the
  // name is carried for display and as a write-only resolve convenience
  // (the server resolves application_name → id, id wins on conflict).
  application_id?: string | null;
  application_name?: string | null;
  // Preserved across edits so we can hand the row back unchanged on save
  // and let the server keep its added_at / added_by stamps.
  added_at?: string;
  added_by?: string;
}

function ApplicationsForm({
  initial,
  onSave,
  onCancel,
  onSaved,
}: {
  initial: api.VMApplication[];
  onSave: (apps: api.VMApplication[]) => Promise<void>;
  onCancel: () => void;
  onSaved: () => void;
}) {
  const [drafts, setDrafts] = useState<DraftApp[]>(() =>
    initial.map((a) => ({
      product: a.product,
      version: a.version,
      name: a.name ?? '',
      notes: a.notes ?? '',
      application_id: a.application_id ?? null,
      application_name: a.application_name ?? null,
      added_at: a.added_at,
      added_by: a.added_by,
    })),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const update = (i: number, patch: Partial<DraftApp>) =>
    setDrafts((prev) => prev.map((d, idx) => (idx === i ? { ...d, ...patch } : d)));
  const remove = (i: number) =>
    setDrafts((prev) => prev.filter((_, idx) => idx !== i));
  const add = () =>
    setDrafts((prev) => [
      ...prev,
      {
        product: '',
        version: '',
        name: '',
        notes: '',
        application_id: null,
        application_name: null,
      },
    ]);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    // Drop fully-empty rows (operator added a row then changed their mind)
    // and validate the rest before sending.
    const cleaned = drafts
      .map((d) => ({
        product: d.product.trim(),
        version: d.version.trim(),
        name: d.name.trim(),
        notes: d.notes,
        application_id: d.application_id ?? null,
        application_name: d.application_name ?? null,
        added_at: d.added_at,
        added_by: d.added_by,
      }))
      .filter(
        (d) => d.product || d.version || d.name || d.notes || d.application_id,
      );
    for (const d of cleaned) {
      if (!d.product) {
        setError('Every row needs a product (e.g. vault, cyberwatch).');
        return;
      }
      if (!d.version) {
        setError(`Row "${d.product}" needs a version.`);
        return;
      }
    }
    // The server stamps added_at / added_by for new rows and preserves
    // them for matching (product, version, name) keys; on input these are
    // omitted entirely when empty (Go time.Time can't decode "").
    const payload = cleaned.map((d) => ({
      product: d.product,
      version: d.version,
      ...(d.name ? { name: d.name } : {}),
      ...(d.notes ? { notes: d.notes } : {}),
      // Send the per-entry link as application_id. A null clears the link;
      // the server preserves application_name's denorm on resolve.
      application_id: d.application_id ?? null,
      ...(d.added_at ? { added_at: d.added_at } : {}),
      ...(d.added_by ? { added_by: d.added_by } : {}),
    })) as api.VMApplication[];
    setBusy(true);
    try {
      await onSave(payload);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>Edit applications</h3>
      </div>
      <form className="admin-form" onSubmit={onSubmit}>
        <p className="muted" style={{ marginTop: 0 }}>
          Declare what runs on this VM. Product names are normalised
          (lowercased, spaces → hyphens) before lookup against
          endoflife.date.
        </p>
        {drafts.length === 0 ? (
          <p className="muted">No applications. Click "Add application" to declare one.</p>
        ) : (
          <table className="entities">
            <thead>
              <tr>
                <th>Product</th>
                <th>Version</th>
                <th>Instance name</th>
                <th>Linked application</th>
                <th>Notes</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {drafts.map((d, i) => (
                <tr key={i}>
                  <td>
                    <input
                      type="text"
                      value={d.product}
                      placeholder="vault"
                      onChange={(e) => update(i, { product: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      value={d.version}
                      placeholder="1.15.4"
                      onChange={(e) => update(i, { version: e.target.value })}
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      value={d.name}
                      placeholder="vault-prod-01 (optional)"
                      onChange={(e) => update(i, { name: e.target.value })}
                    />
                  </td>
                  <td>
                    <EntryAppPicker
                      linkedId={d.application_id ?? null}
                      linkedName={d.application_name ?? null}
                      disabled={busy}
                      onPick={(app) =>
                        update(i, {
                          application_id: app?.id ?? null,
                          application_name: app?.name ?? null,
                        })
                      }
                    />
                  </td>
                  <td>
                    <input
                      type="text"
                      value={d.notes}
                      placeholder="optional"
                      onChange={(e) => update(i, { notes: e.target.value })}
                    />
                  </td>
                  <td>
                    <button
                      type="button"
                      className="danger"
                      onClick={() => remove(i)}
                      disabled={busy}
                    >
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div style={{ marginTop: '0.5rem' }}>
          <button type="button" onClick={add} disabled={busy}>
            + Add application
          </button>
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
      <SectionTitle>EOL annotations</SectionTitle>
      <p className="muted">
        Once saved, the EOL enricher writes <code>longue-vue.io/eol.&lt;product&gt;</code> annotations
        on the VM with lifecycle status, latest available version, and EOL date. Run a manual
        enrichment cycle from <strong>Admin &gt; Settings</strong>, or wait for the next tick.
      </p>
    </section>
  );
}

// EntryAppPicker is the lightweight per-software-row Application picker for
// the VM applications edit form (ADR-0029 §3). When a row is already linked
// it shows the application name + an Unlink button; otherwise it offers a
// type-ahead search backed by listApplications. Picking an entry hands the
// chosen Application back to the parent so the row's application_id /
// application_name are persisted through the existing applications[] save.
function EntryAppPicker({
  linkedId,
  linkedName,
  disabled,
  onPick,
}: {
  linkedId: string | null;
  linkedName: string | null;
  disabled?: boolean;
  onPick: (app: api.Application | null) => void;
}) {
  const [query, setQuery] = useState('');
  const [matches, setMatches] = useState<api.Application[]>([]);

  async function onSearchChange(v: string) {
    setQuery(v);
    if (!v) {
      setMatches([]);
      return;
    }
    try {
      const page = await api.listApplications({ name: v, limit: 10 });
      setMatches(page.items ?? []);
    } catch {
      setMatches([]);
    }
  }

  if (linkedId) {
    return (
      <span>
        <span className="pill">{linkedName ?? `${linkedId.slice(0, 8)}…`}</span>{' '}
        <button
          type="button"
          className="danger"
          disabled={disabled}
          onClick={() => onPick(null)}
        >
          Unlink
        </button>
      </span>
    );
  }

  return (
    <div>
      <input
        type="text"
        value={query}
        placeholder="link application…"
        aria-label="Search applications"
        disabled={disabled}
        onChange={(e) => onSearchChange(e.target.value)}
      />
      {matches.length > 0 && (
        <ul style={{ listStyle: 'none', padding: 0, margin: '0.25rem 0 0' }}>
          {matches.map((a) => (
            <li key={a.id}>
              <button
                type="button"
                disabled={disabled}
                onClick={() => {
                  onPick(a);
                  setQuery('');
                  setMatches([]);
                }}
              >
                {a.name}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
