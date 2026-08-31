import { FormEvent, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import * as api from '../../api';
import { useResource, usePagedList, useLocalListControls } from '../../hooks';
import { AsyncView, Dash, formatTs, KV, SectionTitle } from '../../components';
import { CuratedMetadataCard } from '../../components/inventory/CuratedMetadataCard';
import { ListSection } from '../../components/ListSection';
import { CloudAccountStatusBadge } from './CloudAccounts';

// CloudAccountDetail — admin-only drill-down for one cloud_account row.
// Mirrors the shape of the Node detail page: identity card → curated
// metadata card → operational sub-cards (credentials, VMs, status).
//
// Security guard rails per ADR-0015 §4: the SK never leaves the API
// server's memory, so this page never displays it. The AK is shown in
// truncated form (last 4 chars) so admins can confirm rotation visually
// without leaking the full value into screenshots / browser history.

function maskAccessKey(ak?: string | null): string {
  if (!ak) return '';
  if (ak.length <= 8) return '****';
  // GitHub-style: prefix + ... + last 4. Outscale AKIDs are 32 chars.
  return ak.slice(0, 4) + '…' + ak.slice(-4);
}

export default function CloudAccountDetail() {
  const { id = '' } = useParams();
  const navigate = useNavigate();
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.getCloudAccount(id), [id, nonce]);
  const vmControls = useLocalListControls();
  const vmList = usePagedList<api.VirtualMachine>(
    (cursor, limit) =>
      api.listVirtualMachines({ cloud_account_id: id, ...vmControls.params, cursor, limit }),
    [id, ...vmControls.deps],
  );

  const onDelete = async (account: api.CloudAccount) => {
    const typed = prompt(
      `This will also tombstone all its virtual machines in this account.\n\nType the account name to confirm:`,
    );
    if (typed === null) return;
    if (typed !== account.name) {
      alert(`Name does not match. Expected "${account.name}".`);
      return;
    }
    try {
      await api.deleteCloudAccount(account.id);
      navigate('/admin/cloud-accounts', { replace: true });
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    }
  };

  return (
    <>
      <div className="breadcrumb">
        <Link to="/admin/cloud-accounts">Cloud accounts</Link> / <span>this account</span>
      </div>
      <AsyncView state={state}>
        {(account) => {
          return (
            <>
              <h2>
                {account.name} <CloudAccountStatusBadge status={account.status} />
              </h2>

              <SectionTitle>Identity</SectionTitle>
              <dl className="kv-list">
                <KV k="Name" v={<code>{account.name}</code>} />
                <KV k="Provider" v={<span className="pill">{account.provider}</span>} />
                <KV k="Region" v={<code>{account.region}</code>} />
                <KV k="Status" v={<CloudAccountStatusBadge status={account.status} />} />
                <KV
                  k="Access key"
                  v={
                    account.access_key ? (
                      <code title="last 4 chars only">{maskAccessKey(account.access_key)}</code>
                    ) : (
                      <span className="muted">not set</span>
                    )
                  }
                />
                <KV k="Last seen" v={formatTs(account.last_seen_at)} />
                <KV
                  k="Last error"
                  v={
                    account.last_error ? (
                      <span className="vm-last-error">
                        {account.last_error}
                        <span className="muted" style={{ marginLeft: '0.5rem' }}>
                          {formatTs(account.last_error_at)}
                        </span>
                      </span>
                    ) : undefined
                  }
                />
                <KV k="Created" v={formatTs(account.created_at)} />
                <KV k="Updated" v={formatTs(account.updated_at)} />
                <KV k="Disabled" v={formatTs(account.disabled_at)} />
              </dl>

              <CuratedMetadataCard
                values={{
                  owner: account.owner,
                  criticality: account.criticality,
                  notes: account.notes,
                  runbook_url: account.runbook_url,
                  annotations: account.annotations,
                }}
                onSave={async (values) => {
                  await api.updateCloudAccount(account.id, {
                    owner: values.owner,
                    criticality: values.criticality,
                    notes: values.notes,
                    runbook_url: values.runbook_url,
                    annotations: values.annotations,
                  });
                }}
                onSaved={reload}
              />

              <CredentialsCard account={account} onSaved={reload} />

              <SectionTitle>Lifecycle actions</SectionTitle>
              <div className="admin-form-actions" style={{ marginTop: 0 }}>
                {account.status === 'disabled' ? (
                  <button
                    className="primary"
                    onClick={async () => {
                      await api.enableCloudAccount(account.id);
                      reload();
                    }}
                  >
                    Enable
                  </button>
                ) : (
                  <button
                    onClick={async () => {
                      if (
                        !confirm(
                          `Disable "${account.name}"? The collector will keep heartbeating but credential reads will return 403.`,
                        )
                      ) {
                        return;
                      }
                      await api.disableCloudAccount(account.id);
                      reload();
                    }}
                  >
                    Disable
                  </button>
                )}
                <Link to={`/admin/tokens?bind=${account.id}`} className="link-btn">
                  Issue collector token
                </Link>
                <button className="danger" onClick={() => onDelete(account)}>
                  Delete account
                </button>
              </div>

              <ListSection
                title="Virtual machines"
                list={vmList}
                controls={vmControls}
                emptyMessage="No VMs in this account yet — wait for the collector's next tick."
                rowKey={(vm) => vm.id}
                columns={[
                  {
                    key: 'name',
                    label: 'Name',
                    sortKey: 'name',
                    link: (vm) => `/virtual-machines/${vm.id}`,
                    render: (vm) => vm.display_name || vm.name,
                  },
                  {
                    key: 'power_state',
                    label: 'Power state',
                    sortKey: 'power_state',
                    render: (vm) => <span className="pill">{vm.power_state}</span>,
                  },
                  {
                    key: 'region_zone',
                    label: 'Region / Zone',
                    sortKey: 'region',
                    render: (vm) => (
                      <>
                        {vm.region ? <code>{vm.region}</code> : <Dash />}
                        {vm.zone && (
                          <span className="muted" style={{ marginLeft: '0.4rem' }}>
                            {vm.zone}
                          </span>
                        )}
                      </>
                    ),
                  },
                  {
                    key: 'instance_type',
                    label: 'Instance type',
                    sortKey: 'instance_type',
                    render: (vm) => (vm.instance_type ? <code>{vm.instance_type}</code> : <Dash />),
                  },
                  {
                    key: 'last_seen',
                    label: 'Last seen',
                    sortKey: 'last_seen_at',
                    render: (vm) => (
                      <span className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
                        {formatTs(vm.last_seen_at)}
                      </span>
                    ),
                  },
                ]}
              />
            </>
          );
        }}
      </AsyncView>
    </>
  );
}

function CredentialsCard({
  account,
  onSaved,
}: {
  account: api.CloudAccount;
  onSaved: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [accessKey, setAccessKey] = useState('');
  const [secretKey, setSecretKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!accessKey.trim() || !secretKey) {
      setError('Both access key and secret key are required.');
      return;
    }
    setBusy(true);
    try {
      await api.setCloudAccountCredentials(account.id, {
        access_key: accessKey.trim(),
        secret_key: secretKey,
      });
      setAccessKey('');
      setSecretKey('');
      setEditing(false);
      onSaved();
    } catch (err) {
      setError(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  if (!editing) {
    return (
      <section className="curated-card">
        <div className="curated-card-header">
          <h3>Credentials</h3>
          <button className="primary" onClick={() => setEditing(true)}>
            {account.access_key ? 'Rotate credentials' : 'Set credentials'}
          </button>
        </div>
        <p className="muted" style={{ marginTop: 0 }}>
          {account.access_key
            ? `Access key set (${maskAccessKey(account.access_key)}). Secret key is encrypted at rest and never shown again.`
            : 'No credentials set yet — the collector will keep heartbeating but cannot pull VMs.'}
        </p>
      </section>
    );
  }

  return (
    <section className="curated-card">
      <div className="curated-card-header">
        <h3>{account.access_key ? 'Rotate credentials' : 'Set credentials'}</h3>
      </div>
      <form className="admin-form" onSubmit={submit}>
        <div className="admin-form-row">
          <div>
            <label>Access key</label>
            <input
              type="text"
              autoFocus
              value={accessKey}
              onChange={(e) => setAccessKey(e.target.value)}
              placeholder="AKIA…"
            />
          </div>
          <div>
            <label>Secret key</label>
            <input
              type="password"
              value={secretKey}
              onChange={(e) => setSecretKey(e.target.value)}
              placeholder="••••••••"
            />
          </div>
        </div>
        <p className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
          The SK is encrypted with AES-256-GCM (AAD bound to the row UUID) and only ever decrypted
          in longue-vue's memory when the collector pulls it.
        </p>
        {error && <div className="error">{error}</div>}
        <div className="admin-form-actions">
          <button type="submit" className="primary" disabled={busy}>
            {busy ? 'Saving…' : 'Save credentials'}
          </button>
          <button
            type="button"
            onClick={() => {
              setAccessKey('');
              setSecretKey('');
              setEditing(false);
            }}
            disabled={busy}
          >
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}
