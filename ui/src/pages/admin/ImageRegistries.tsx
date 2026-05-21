import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, SectionTitle } from '../../components';

// ImageRegistriesPage — admin tab for managing image registry poll targets
// and Harbor-style mirror rows (ADR-0026).

type Reload = () => void;

export default function ImageRegistriesPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.listImageRegistries(), [nonce]);

  return (
    <AsyncView state={state}>
      {(resp) => (
        <>
          <CreateForm reload={reload} />
          <SectionTitle count={resp.items.length}>Image registries</SectionTitle>
          {resp.items.length === 0 ? (
            <p className="muted">No registries configured yet.</p>
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>Hostname</th>
                  <th>Path prefix</th>
                  <th>Type</th>
                  <th>Rate limit (req/s)</th>
                  <th>Status</th>
                  <th>Notes</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {[...resp.items]
                  .sort((a, b) =>
                    a.hostname === b.hostname
                      ? a.path_prefix.localeCompare(b.path_prefix)
                      : a.hostname.localeCompare(b.hostname),
                  )
                  .map((r) => (
                    <RegistryRow
                      key={`${r.hostname}|${r.path_prefix}`}
                      registry={r}
                      reload={reload}
                    />
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
  const [hostname, setHostname] = useState('');
  const [pathPrefix, setPathPrefix] = useState('');
  const [isMirror, setIsMirror] = useState(false);
  const [rate, setRate] = useState('5');
  const [notes, setNotes] = useState('');
  const [authUser, setAuthUser] = useState('');
  const [authToken, setAuthToken] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setHostname('');
    setPathPrefix('');
    setIsMirror(false);
    setRate('5');
    setNotes('');
    setAuthUser('');
    setAuthToken('');
    setError(null);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    const rateNum = parseFloat(rate);
    if (!hostname.trim()) {
      setError('hostname is required');
      return;
    }
    if (!rateNum || rateNum <= 0) {
      setError('rate limit must be a positive number');
      return;
    }
    setBusy(true);
    try {
      await api.createImageRegistry({
        hostname: hostname.trim(),
        path_prefix: isMirror ? pathPrefix.trim() : '',
        rate_limit_per_sec: rateNum,
        notes: notes.trim() || undefined,
        is_mirror: isMirror,
        auth_username: isMirror && authUser.trim() ? authUser.trim() : undefined,
        auth_token: isMirror && authToken !== '' ? authToken : undefined,
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
          + Add registry
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Add registry</h3>
      <p className="muted" style={{ marginTop: 0, fontSize: 'var(--fs-base)' }}>
        Wildcards like <code>*.example.com</code> match any subdomain. Use{' '}
        <code>docker.io</code> for Docker Hub. Mark as a mirror when this row points
        at a Harbor-style replication target.
      </p>
      <div className="admin-form-row">
        <div>
          <label>Hostname</label>
          <input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder="docker.io"
            autoFocus
          />
        </div>
        <div>
          <label>Rate limit (req/s)</label>
          <input
            type="number"
            min="0.1"
            step="0.1"
            value={rate}
            onChange={(e) => setRate(e.target.value)}
          />
        </div>
        <div>
          <label>
            <input
              type="checkbox"
              checked={isMirror}
              onChange={(e) => setIsMirror(e.target.checked)}
            />{' '}
            Mirror registry
          </label>
        </div>
      </div>
      {isMirror && (
        <div className="admin-form-row">
          <div>
            <label>Path prefix</label>
            <input
              value={pathPrefix}
              onChange={(e) => setPathPrefix(e.target.value)}
              placeholder="container/"
            />
          </div>
          <div>
            <label>Auth username</label>
            <input
              value={authUser}
              onChange={(e) => setAuthUser(e.target.value)}
              placeholder="robot$lv"
            />
          </div>
          <div>
            <label>Auth token</label>
            <input
              type="password"
              value={authToken}
              onChange={(e) => setAuthToken(e.target.value)}
              autoComplete="new-password"
            />
          </div>
        </div>
      )}
      <div className="admin-form-row">
        <div style={{ flex: 1 }}>
          <label>Notes (optional)</label>
          <input
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="e.g. internal mirror"
          />
        </div>
      </div>
      <div className="admin-form-actions">
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Adding…' : 'Add registry'}
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

function RegistryRow({ registry, reload }: { registry: api.ImageRegistry; reload: Reload }) {
  const [busy, setBusy] = useState(false);
  const [revealed, setRevealed] = useState<api.ImageRegistryCredentials | null>(null);

  const onToggleEnabled = async () => {
    setBusy(true);
    try {
      await api.updateImageRegistry(registry.hostname, registry.path_prefix, {
        enabled: !registry.enabled,
      });
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async () => {
    const label = registry.path_prefix
      ? `${registry.hostname}/${registry.path_prefix}`
      : registry.hostname;
    if (!confirm(`Delete registry "${label}"?`)) return;
    setBusy(true);
    try {
      await api.deleteImageRegistry(registry.hostname, registry.path_prefix);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onReveal = async () => {
    setBusy(true);
    try {
      const creds = await api.getImageRegistryCredentials(
        registry.hostname,
        registry.path_prefix,
      );
      setRevealed(creds);
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <tr>
      <td>
        <code>{registry.hostname}</code>
      </td>
      <td>
        <code>{registry.path_prefix || '—'}</code>
      </td>
      <td>
        <span className={`pill ${registry.is_mirror ? 'status-warn' : 'status-ok'}`}>
          {registry.is_mirror ? 'Mirror' : 'Source'}
        </span>
        {registry.is_mirror && registry.auth_configured && (
          <span className="pill" style={{ marginLeft: '0.25rem' }}>auth</span>
        )}
      </td>
      <td>{registry.rate_limit_per_sec}</td>
      <td>
        <span className={`pill ${registry.enabled ? 'status-ok' : ''}`}>
          {registry.enabled ? 'Enabled' : 'Disabled'}
        </span>
      </td>
      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        {registry.notes ?? ''}
      </td>
      <td style={{ textAlign: 'right' }}>
        {registry.is_mirror && registry.auth_configured && (
          <>
            <button onClick={onReveal} disabled={busy}>
              {revealed ? 'Hide' : 'Reveal'}
            </button>{' '}
          </>
        )}
        {registry.enabled ? (
          <button onClick={onToggleEnabled} disabled={busy}>
            Disable
          </button>
        ) : (
          <button onClick={onToggleEnabled} disabled={busy} className="primary">
            Enable
          </button>
        )}{' '}
        <button onClick={onDelete} disabled={busy} className="danger">
          Delete
        </button>
        {revealed && (
          <div className="muted" style={{ marginTop: '0.5rem', fontSize: 'var(--fs-sm)' }}>
            <div>
              <strong>user:</strong> <code>{revealed.auth_username}</code>
            </div>
            <div>
              <strong>token:</strong> <code>{revealed.auth_token}</code>
            </div>
          </div>
        )}
      </td>
    </tr>
  );
}
