import { FormEvent, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';
import { Pill } from '../../components/lv/Pill';

// CloudAccountsPage — admin tab for ADR-0015 cloud-provider accounts.
// Shape mirrors the Tokens page: list-then-action, with an inline create
// form that opens on demand. Per-row actions cover the common admin
// operations (view / disable / enable / delete). The "pending credentials"
// banner is the load-bearing affordance — it tells the admin which
// collectors are stuck waiting for AK/SK input.

type Reload = () => void;

function CloudAccountStatusBadge({ status }: { status: api.CloudAccountStatus }) {
  switch (status) {
    case 'active':
      return <Pill status="ok">active</Pill>;
    case 'pending_credentials':
      return <Pill status="bad">pending credentials</Pill>;
    case 'error':
      return <Pill status="warn">error</Pill>;
    case 'disabled':
      return <Pill>disabled</Pill>;
    default:
      return <Pill>{status}</Pill>;
  }
}

export { CloudAccountStatusBadge };

function formatTs(ts?: string | null): string {
  if (!ts) return '';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

export default function CloudAccountsPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.listCloudAccounts(), [nonce]);
  const [searchParams, setSearchParams] = useSearchParams();
  const statusFilter = searchParams.get('status') || '';

  const setStatus = (s: string) => {
    const next = new URLSearchParams(searchParams);
    if (s) next.set('status', s);
    else next.delete('status');
    setSearchParams(next, { replace: true });
  };

  return (
    <div className="lv-card">
      <AsyncView state={state}>
        {(resp) => {
          const accounts = resp.items;
          // Plain filter (no useMemo) — render-prop callbacks aren't a stable
          // hook scope. Both filters are O(N) over the page's 200-row max so
          // recomputing every render is cheap.
          const pending = accounts.filter((a) => a.status === 'pending_credentials');
          const filtered = statusFilter
            ? accounts.filter((a) => a.status === statusFilter)
            : accounts;

          return (
            <>
              {pending.length > 0 && (
                <div className="vm-banner">
                  <strong>{pending.length}</strong> account{pending.length === 1 ? '' : 's'} pending
                  credentials — collector is stuck waiting for AK/SK input.{' '}
                  <button
                    type="button"
                    className="link-btn"
                    onClick={() => setStatus('pending_credentials')}
                  >
                    Filter table
                  </button>
                </div>
              )}

              <CreateForm reload={reload} />

              <SectionTitle count={filtered.length}>
                {statusFilter ? `Cloud accounts (${statusFilter})` : 'Cloud accounts'}
              </SectionTitle>

              {statusFilter && (
                <p style={{ margin: '0 0 0.5rem' }}>
                  Filtering by <code>{statusFilter}</code>.{' '}
                  <button type="button" className="link-btn" onClick={() => setStatus('')}>
                    clear
                  </button>
                </p>
              )}

              {filtered.length === 0 ? (
                <p className="muted">No cloud accounts registered yet.</p>
              ) : (
                <table className="entities">
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Provider</th>
                      <th>Region</th>
                      <th>Status</th>
                      <th>Last seen</th>
                      <th>Owner</th>
                      <th>Criticality</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((a) => (
                      <CloudAccountRow key={a.id} account={a} reload={reload} />
                    ))}
                  </tbody>
                </table>
              )}
            </>
          );
        }}
      </AsyncView>
    </div>
  );
}

function CreateForm({ reload }: { reload: Reload }) {
  const [open, setOpen] = useState(false);
  const [provider, setProvider] = useState('outscale');
  const [name, setName] = useState('');
  const [region, setRegion] = useState('');
  const [accessKey, setAccessKey] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [owner, setOwner] = useState('');
  const [criticality, setCriticality] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setProvider('outscale');
    setName('');
    setRegion('');
    setAccessKey('');
    setSecretKey('');
    setOwner('');
    setCriticality('');
    setError(null);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!provider.trim() || !name.trim() || !region.trim()) {
      setError('provider, name and region are required');
      return;
    }
    setBusy(true);
    try {
      await api.createCloudAccount({
        provider: provider.trim(),
        name: name.trim(),
        region: region.trim(),
        access_key: accessKey.trim() || undefined,
        secret_key: secretKey || undefined,
        owner: owner.trim() || undefined,
        criticality: criticality.trim() || undefined,
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
        <button className="lv-btn lv-btn-primary" onClick={() => setOpen(true)}>
          + Add cloud account
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Register cloud account</h3>
      <p className="muted" style={{ marginTop: 0, fontSize: 'var(--fs-base)' }}>
        Create a placeholder before deploying the collector, or leave AK/SK blank now and set them
        later via the detail page.
      </p>
      <div className="admin-form-row">
        <div>
          <label>Provider</label>
          <select value={provider} onChange={(e) => setProvider(e.target.value)}>
            <option value="outscale">outscale</option>
            <option value="aws">aws</option>
            <option value="ovh">ovh</option>
            <option value="scaleway">scaleway</option>
            <option value="azure">azure</option>
          </select>
        </div>
        <div>
          <label>Name</label>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="acme-prod"
            autoFocus
          />
        </div>
        <div>
          <label>Region</label>
          <input
            value={region}
            onChange={(e) => setRegion(e.target.value)}
            placeholder="eu-west-2"
          />
        </div>
      </div>
      <div className="admin-form-row">
        <div>
          <label>Access key (optional)</label>
          <input
            type="text"
            value={accessKey}
            onChange={(e) => setAccessKey(e.target.value)}
            placeholder="AKIA…"
          />
        </div>
        <div>
          <label>Secret key (optional)</label>
          <input
            type="password"
            value={secretKey}
            onChange={(e) => setSecretKey(e.target.value)}
          />
        </div>
      </div>
      <div className="admin-form-row">
        <div>
          <label>Owner</label>
          <input
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
            placeholder="team-platform"
          />
        </div>
        <div>
          <label>Criticality</label>
          <input
            value={criticality}
            onChange={(e) => setCriticality(e.target.value)}
            placeholder="high"
          />
        </div>
      </div>
      <p className="muted" style={{ fontSize: 'var(--fs-sm)', margin: '0.5rem 0 0' }}>
        SK is encrypted at rest. It is decrypted in memory only when the collector pulls it via the
        narrow vm-collector token.
      </p>
      <div className="admin-form-actions">
        <button type="submit" className="lv-btn lv-btn-primary" disabled={busy}>
          {busy ? 'Registering…' : 'Register account'}
        </button>
        <button
          type="button"
          onClick={() => {
            reset();
            setOpen(false);
          }}
          disabled={busy}
          className="lv-btn lv-btn-ghost"
        >
          Cancel
        </button>
      </div>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

function CloudAccountRow({ account, reload }: { account: api.CloudAccount; reload: Reload }) {
  const [busy, setBusy] = useState(false);

  const onDisable = async () => {
    if (!confirm(`Disable account "${account.name}"? Credentials reads will start failing.`)) return;
    setBusy(true);
    try {
      await api.disableCloudAccount(account.id);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onEnable = async () => {
    setBusy(true);
    try {
      await api.enableCloudAccount(account.id);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const onDelete = async () => {
    const typed = prompt(
      `This will delete account "${account.name}" and tombstone every VM in it.\n\nType the account name to confirm:`,
    );
    if (typed === null) return;
    if (typed !== account.name) {
      alert(`Name does not match. Expected "${account.name}".`);
      return;
    }
    setBusy(true);
    try {
      await api.deleteCloudAccount(account.id);
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
        <Link to={`/admin/cloud-accounts/${account.id}`}>
          <strong>{account.name}</strong>
        </Link>
      </td>
      <td>
        <Pill>{account.provider}</Pill>
      </td>
      <td>
        <code>{account.region}</code>
      </td>
      <td>
        <CloudAccountStatusBadge status={account.status} />
      </td>
      <td className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
        {account.last_seen_at ? formatTs(account.last_seen_at) : <Dash />}
      </td>
      <td>{account.owner || <Dash />}</td>
      <td>
        {account.criticality ? <Pill>{account.criticality}</Pill> : <Dash />}
      </td>
      <td style={{ textAlign: 'right' }}>
        <Link to={`/admin/cloud-accounts/${account.id}`} className="link-btn">
          View
        </Link>{' '}
        {account.status === 'disabled' ? (
          <button onClick={onEnable} disabled={busy}>
            Enable
          </button>
        ) : (
          <button onClick={onDisable} disabled={busy}>
            Disable
          </button>
        )}{' '}
        <button onClick={onDelete} disabled={busy} className="lv-btn lv-btn-ghost">
          Delete
        </button>
      </td>
    </tr>
  );
}
