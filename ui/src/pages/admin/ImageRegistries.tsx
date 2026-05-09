import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, SectionTitle } from '../../components';

// ImageRegistriesPage — admin tab for managing image registry poll targets.
// Shape mirrors other admin list pages: list with inline create form,
// per-row enable/disable toggle and delete.

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
                  <th>Rate limit (req/s)</th>
                  <th>Status</th>
                  <th>Notes</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {resp.items.map((r) => (
                  <RegistryRow key={r.hostname} registry={r} reload={reload} />
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
  const [rate, setRate] = useState('5');
  const [notes, setNotes] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setHostname('');
    setRate('5');
    setNotes('');
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
        rate_limit_per_sec: rateNum,
        notes: notes.trim() || undefined,
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
        <code>docker.io</code> for Docker Hub.
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

  const onToggleEnabled = async () => {
    setBusy(true);
    try {
      await api.updateImageRegistry(registry.hostname, { enabled: !registry.enabled });
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async () => {
    if (!confirm(`Delete registry "${registry.hostname}"?`)) return;
    setBusy(true);
    try {
      await api.deleteImageRegistry(registry.hostname);
      reload();
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
      </td>
    </tr>
  );
}
