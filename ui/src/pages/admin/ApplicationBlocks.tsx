import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, SectionTitle } from '../../components';

// ApplicationBlocksPage — admin tab for managing the optional grouping
// layer above applications (portfolios / business domains, ADR-0029).
// Mirrors the ImageRegistries admin page: an inline create form above a
// table with inline edit + delete. Admin-only (enforced by AdminLayout).

type Reload = () => void;

export default function ApplicationBlocksPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.listApplicationBlocks(), [nonce]);

  return (
    <AsyncView state={state}>
      {(resp) => (
        <>
          <h3>Application blocks</h3>
          <CreateForm reload={reload} />
          <SectionTitle count={resp.items.length}>Blocks</SectionTitle>
          {resp.items.length === 0 ? (
            <p className="muted">No application blocks configured yet.</p>
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Display name</th>
                  <th>Owner</th>
                  <th>Applications</th>
                  <th>Notes</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {[...resp.items]
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map((b) => (
                    <BlockRow key={b.id} block={b} reload={reload} />
                  ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </AsyncView>
  );
}

function CreateForm({ reload }: { reload: Reload }) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [owner, setOwner] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setName('');
    setDisplayName('');
    setOwner('');
    setError(null);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!name.trim()) {
      setError('name is required');
      return;
    }
    setBusy(true);
    try {
      await api.createApplicationBlock({
        name: name.trim(),
        display_name: displayName.trim() || undefined,
        owner: owner.trim() || undefined,
      });
      reset();
      setOpen(false);
      reload();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (!open) {
    return (
      <div className="admin-actions">
        <button className="primary" onClick={() => setOpen(true)}>
          + Add block
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Add application block</h3>
      <div className="admin-form-row">
        <div>
          <label>Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="finance"
            autoFocus
          />
        </div>
        <div>
          <label>Display name (optional)</label>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Finance"
          />
        </div>
        <div>
          <label>Owner (optional)</label>
          <input
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
            placeholder="cfo-team"
          />
        </div>
      </div>
      <div className="admin-form-actions">
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Adding…' : 'Create'}
        </button>
        <button
          type="button"
          onClick={() => {
            reset();
            setOpen(false);
          }}
          disabled={busy}
        >
          Cancel
        </button>
      </div>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

function BlockRow({ block, reload }: { block: api.ApplicationBlock; reload: Reload }) {
  const [editing, setEditing] = useState(false);
  const [displayName, setDisplayName] = useState(block.display_name ?? '');
  const [owner, setOwner] = useState(block.owner ?? '');
  const [busy, setBusy] = useState(false);

  const onSave = async () => {
    setBusy(true);
    try {
      await api.patchApplicationBlock(block.id, {
        display_name: displayName.trim(),
        owner: owner.trim(),
      });
      setEditing(false);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async () => {
    if (!confirm(`Delete application block "${block.name}"?`)) return;
    setBusy(true);
    try {
      await api.deleteApplicationBlock(block.id);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (editing) {
    return (
      <tr>
        <td>
          <code>{block.name}</code>
        </td>
        <td>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            aria-label="Display name"
          />
        </td>
        <td>
          <input
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
            aria-label="Owner"
          />
        </td>
        <td>{block.application_count ?? 0}</td>
        <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
          {block.notes ?? ''}
        </td>
        <td style={{ textAlign: 'right' }}>
          <button onClick={onSave} disabled={busy} className="primary">
            {busy ? 'Saving…' : 'Save'}
          </button>{' '}
          <button onClick={() => setEditing(false)} disabled={busy}>
            Cancel
          </button>
        </td>
      </tr>
    );
  }

  return (
    <tr>
      <td>
        <code>{block.name}</code>
      </td>
      <td>{block.display_name || '—'}</td>
      <td>{block.owner || '—'}</td>
      <td>{block.application_count ?? 0}</td>
      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        {block.notes ? excerpt(block.notes) : ''}
      </td>
      <td style={{ textAlign: 'right' }}>
        <button onClick={() => setEditing(true)} disabled={busy}>
          Edit
        </button>{' '}
        <button onClick={onDelete} disabled={busy} className="danger">
          Delete
        </button>
      </td>
    </tr>
  );
}

// excerpt truncates a notes string to a single-line preview for the table.
function excerpt(s: string, max = 80): string {
  const oneLine = s.replace(/\s+/g, ' ').trim();
  return oneLine.length > max ? `${oneLine.slice(0, max)}…` : oneLine;
}
