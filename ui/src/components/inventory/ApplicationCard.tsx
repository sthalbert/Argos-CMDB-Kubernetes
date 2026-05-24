import { useState } from 'react';
import { Link } from 'react-router-dom';
import * as api from '../../api';

// ApplicationCard is the single-select picker used on Workload, VM,
// and (eventually) Application member-add surfaces to link an entity
// to an Application (ADR-0029 §3). Read mode shows a chip linking
// to the application's detail page; edit mode opens a type-ahead
// search backed by listApplications and a tiny inline create form
// that calls createApplication then onLink(newApp.id).
//
// The card is intentionally dumb about *what* it links to — callers
// own the patch call inside onLink (workload PATCH, VM PATCH, or a
// future application-member POST). That keeps it reusable.

interface Props {
  // Currently linked application id, or null/undefined when not linked.
  linkedId?: string | null;
  // Currently linked application name (typically the denormalized
  // application_name field on the parent entity). Falling back to
  // listApplications round-trips here would be wasteful; the parent
  // detail page already loaded the name.
  linkedName?: string | null;
  // Called when the operator picks (id) or unlinks (null). Failures are
  // surfaced inline; the caller decides whether to refetch.
  onLink: (appId: string | null) => Promise<void> | void;
  // Hide affordances for viewer/auditor roles.
  editable: boolean;
  // Optional override — the Application detail page reuses this card
  // to add members and wants a different label.
  label?: string;
}

export function ApplicationCard({
  linkedId,
  linkedName,
  onLink,
  editable,
  label = 'Application',
}: Props) {
  const [editing, setEditing] = useState(false);
  const [query, setQuery] = useState('');
  const [matches, setMatches] = useState<api.Application[]>([]);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setEditing(false);
    setCreating(false);
    setQuery('');
    setMatches([]);
    setNewName('');
    setError(null);
  };

  async function onSearchChange(v: string) {
    setQuery(v);
    setError(null);
    if (!v) {
      setMatches([]);
      return;
    }
    try {
      const page = await api.listApplications({ name: v, limit: 10 });
      setMatches(page.items ?? []);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setMatches([]);
    }
  }

  async function pick(id: string) {
    setBusy(true);
    setError(null);
    try {
      await onLink(id);
      reset();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function unlink() {
    setBusy(true);
    setError(null);
    try {
      await onLink(null);
      reset();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function createAndLink() {
    const trimmed = newName.trim();
    if (!trimmed) return;
    setBusy(true);
    setError(null);
    try {
      const app = await api.createApplication({ name: trimmed });
      await onLink(app.id);
      reset();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="curated-card" data-testid="application-card">
      <div className="curated-card-header">
        <h3>{label}</h3>
        {!editing && editable && (
          <button type="button" className="primary" onClick={() => setEditing(true)}>
            {linkedName ? 'Change…' : 'Link…'}
          </button>
        )}
      </div>
      {!editing && (
        <div>
          {linkedName && linkedId ? (
            <Link to={`/applications/${linkedId}`}>{linkedName}</Link>
          ) : linkedName ? (
            <span>{linkedName}</span>
          ) : (
            <span className="muted">Not linked</span>
          )}
        </div>
      )}
      {editing && !creating && (
        <div>
          <input
            type="text"
            placeholder="Type to search…"
            value={query}
            onChange={(e) => onSearchChange(e.target.value)}
            aria-label="Search applications"
            disabled={busy}
          />
          <ul style={{ listStyle: 'none', padding: 0, marginTop: '0.5rem' }}>
            {matches.map((a) => (
              <li key={a.id} style={{ marginBottom: '0.25rem' }}>
                <button type="button" disabled={busy} onClick={() => pick(a.id)}>
                  {a.name}
                  {a.display_name && a.display_name !== a.name ? (
                    <span className="muted"> — {a.display_name}</span>
                  ) : null}
                </button>
              </li>
            ))}
            <li style={{ marginTop: '0.5rem' }}>
              <button type="button" onClick={() => setCreating(true)} disabled={busy}>
                + Create new application…
              </button>
            </li>
            {linkedId && (
              <li style={{ marginTop: '0.25rem' }}>
                <button
                  type="button"
                  className="danger"
                  disabled={busy}
                  onClick={unlink}
                >
                  Unlink
                </button>
              </li>
            )}
            <li style={{ marginTop: '0.25rem' }}>
              <button type="button" onClick={reset} disabled={busy}>
                Cancel
              </button>
            </li>
          </ul>
          {error && <div className="error">{error}</div>}
        </div>
      )}
      {creating && (
        <div role="dialog" aria-label="Create application">
          <input
            type="text"
            placeholder="New application name"
            value={newName}
            onChange={(e) => setNewName(e.target.value)}
            aria-label="New application name"
            disabled={busy}
          />
          <div style={{ marginTop: '0.5rem' }}>
            <button
              type="button"
              className="primary"
              onClick={createAndLink}
              disabled={!newName.trim() || busy}
            >
              {busy ? 'Creating…' : 'Create + Link'}
            </button>
            <button
              type="button"
              className="danger"
              onClick={() => {
                setCreating(false);
                setNewName('');
                setError(null);
              }}
              disabled={busy}
              style={{ marginLeft: '0.5rem' }}
            >
              Cancel
            </button>
          </div>
          {error && <div className="error">{error}</div>}
        </div>
      )}
    </section>
  );
}

export default ApplicationCard;
