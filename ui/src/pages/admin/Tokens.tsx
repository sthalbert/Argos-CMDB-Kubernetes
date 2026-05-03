import { FormEvent, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';
import { Callout } from '../../components/lv/Callout';
import { Pill } from '../../components/lv/Pill';

// Token presets per ADR-0007 + ADR-0015.
type Preset = 'standard' | 'vm-collector';

// Admin Tokens page. The one-shot plaintext reveal is the load-bearing
// piece: after a successful mint we render a callout with the full
// plaintext + a copy button, and this is the only time it's ever shown.
// Dismissing the callout doesn't let the value come back — persist it
// to state, but the backend does not.

type Reload = () => void;

export default function TokensPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const [minted, setMinted] = useState<api.ApiTokenMint | null>(null);
  const state = useResource(() => api.listApiTokens(), [nonce]);

  return (
    <div className="lv-card">
      <AsyncView state={state}>
        {(resp) => (
          <>
            {minted && <MintedReveal minted={minted} onDismiss={() => setMinted(null)} />}
            <MintForm
              reload={() => {
                reload();
              }}
              onMinted={setMinted}
            />
            <SectionTitle count={resp.items.length}>Machine tokens</SectionTitle>
            <TokenTable tokens={resp.items} reload={reload} />
          </>
        )}
      </AsyncView>
    </div>
  );
}

function MintedReveal({
  minted,
  onDismiss,
}: {
  minted: api.ApiTokenMint;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(minted.token);
      setCopied(true);
      setTimeout(() => setCopied(false), 2500);
    } catch {
      // Some browsers require https or explicit user gesture; fall back
      // to selection. The value is in the <code> below either way.
    }
  };

  return (
    <Callout title="Token minted — shown once, copy it now" status="ok">
      <p className="muted" style={{ marginTop: '0.25rem', fontSize: '0.85rem' }}>
        longue-vue stores only the argon2id hash. Once you dismiss this banner the
        plaintext is gone — if you lose it, revoke the token and mint a new one.
      </p>
      <div className="reveal-value">
        <code>{minted.token}</code>
        <button onClick={copy} className="lv-btn lv-btn-primary">
          {copied ? 'Copied ✓' : 'Copy'}
        </button>
      </div>
      <p className="muted" style={{ fontSize: '0.8rem' }}>
        Name: <strong>{minted.name}</strong> · Scopes:{' '}
        <code>{minted.scopes.join(', ')}</code> · Prefix: <code>{minted.prefix}</code>
      </p>
      <div style={{ marginTop: '0.5rem' }}>
        <button onClick={onDismiss} className="lv-btn lv-btn-ghost">Dismiss</button>
      </div>
    </Callout>
  );
}

function MintForm({
  reload,
  onMinted,
}: {
  reload: Reload;
  onMinted: (m: api.ApiTokenMint) => void;
}) {
  const [searchParams] = useSearchParams();
  const initialBind = searchParams.get('bind') ?? '';

  const [open, setOpen] = useState(initialBind !== '');
  const [preset, setPreset] = useState<Preset>(
    initialBind ? 'vm-collector' : 'standard',
  );
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<Record<api.TokenScope, boolean>>({
    read: true,
    write: false,
    delete: false,
  });
  const [expiresAt, setExpiresAt] = useState('');
  const [boundCloudAccountId, setBoundCloudAccountId] = useState<string>(initialBind);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Cloud accounts dropdown for the vm-collector preset. Loads only when
  // the user actually selects the preset.
  const accountsState = useResource(
    () =>
      preset === 'vm-collector'
        ? api.listCloudAccounts()
        : Promise.resolve(null),
    [preset],
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!name.trim()) {
      setError('Name required.');
      return;
    }
    if (preset === 'vm-collector') {
      if (!boundCloudAccountId) {
        setError('Pick a cloud account to bind the token to.');
        return;
      }
      setBusy(true);
      try {
        // Hand-written endpoint POST /v1/admin/cloud-accounts/{id}/tokens
        // mints a PAT scoped to "vm-collector" and bound to the URL's
        // cloud_account id (ADR-0015 §5).
        const minted = await api.createCloudAccountToken(boundCloudAccountId, {
          name: name.trim(),
          expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
        });
        // Adapt to ApiTokenMint shape so the existing minted-display card works.
        onMinted({
          id: minted.id,
          name: minted.name,
          prefix: minted.prefix,
          scopes: minted.scopes as api.TokenScope[],
          created_by_user_id: minted.created_by_user_id,
          created_at: minted.created_at,
          expires_at: minted.expires_at ?? null,
          token: minted.token,
        });
        setName('');
        setExpiresAt('');
        setOpen(false);
        reload();
      } catch (err) {
        setError(err instanceof api.ApiError ? err.message : String(err));
      } finally {
        setBusy(false);
      }
      return;
    }
    const sel: api.TokenScope[] = (Object.keys(scopes) as api.TokenScope[]).filter(
      (k) => scopes[k],
    );
    if (sel.length === 0) {
      setError('At least one scope required.');
      return;
    }
    setBusy(true);
    try {
      const minted = await api.createApiToken({
        name: name.trim(),
        scopes: sel,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : null,
      });
      onMinted(minted);
      setName('');
      setExpiresAt('');
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
          + Mint token
        </button>
      </div>
    );
  }

  const accountList =
    accountsState.status === 'ready' && accountsState.data
      ? accountsState.data.items
      : [];
  const isVmCollector = preset === 'vm-collector';

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Mint machine token</h3>
      <div className="admin-form-row">
        <div>
          <label>Preset</label>
          <select value={preset} onChange={(e) => setPreset(e.target.value as Preset)}>
            <option value="standard">Standard (read / write / delete)</option>
            <option value="vm-collector">VM Collector</option>
          </select>
        </div>
        <div>
          <label>Name</label>
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. ci-release-pipeline"
          />
        </div>
        <div>
          <label>Expires (optional)</label>
          <input
            type="datetime-local"
            value={expiresAt}
            onChange={(e) => setExpiresAt(e.target.value)}
          />
        </div>
      </div>
      {isVmCollector ? (
        <div>
          <label>Bound to cloud account</label>
          <select
            value={boundCloudAccountId}
            onChange={(e) => setBoundCloudAccountId(e.target.value)}
          >
            <option value="">Select a cloud account…</option>
            {accountList.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name} ({a.provider}/{a.region})
              </option>
            ))}
          </select>
          <p className="muted" style={{ fontSize: 'var(--fs-sm)' }}>
            VM-collector tokens carry the narrow <code>vm-collector</code> scope and
            are bound to one cloud account at issue time (ADR-0015 §5). A leaked
            token can only access this one account&apos;s credentials and VMs.
          </p>
        </div>
      ) : (
        <div>
          <label>Scopes</label>
          <div className="scope-checkboxes">
            {(['read', 'write', 'delete'] as api.TokenScope[]).map((s) => (
              <label key={s} className="scope-checkbox">
                <input
                  type="checkbox"
                  checked={scopes[s]}
                  onChange={(e) => setScopes({ ...scopes, [s]: e.target.checked })}
                />
                <code>{s}</code>
              </label>
            ))}
          </div>
          <p className="muted" style={{ fontSize: '0.8rem' }}>
            Tokens can only carry read / write / delete. Admin-only endpoints
            are session-only for accountability.
          </p>
        </div>
      )}
      <div className="admin-form-actions">
        <button type="submit" disabled={busy} className="lv-btn lv-btn-primary">
          {busy ? 'Minting…' : 'Mint token'}
        </button>
        <button type="button" onClick={() => setOpen(false)} disabled={busy} className="lv-btn lv-btn-ghost">
          Cancel
        </button>
      </div>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

function TokenTable({ tokens, reload }: { tokens: api.ApiToken[]; reload: Reload }) {
  if (tokens.length === 0) return <p className="muted">No tokens minted yet.</p>;
  return (
    <table className="entities">
      <thead>
        <tr>
          <th>Name</th>
          <th>Prefix</th>
          <th>Scopes</th>
          <th>Last used</th>
          <th>Expires</th>
          <th>Status</th>
          <th style={{ textAlign: 'right' }}>Actions</th>
        </tr>
      </thead>
      <tbody>
        {tokens.map((t) => (
          <TokenRow key={t.id} token={t} reload={reload} />
        ))}
      </tbody>
    </table>
  );
}

function TokenRow({ token, reload }: { token: api.ApiToken; reload: Reload }) {
  const [busy, setBusy] = useState(false);
  const revoke = async () => {
    if (!confirm(`Revoke token "${token.name}"? API calls carrying it start failing immediately.`)) {
      return;
    }
    setBusy(true);
    try {
      await api.revokeApiToken(token.id);
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const status = tokenStatus(token);
  return (
    <tr>
      <td>
        <strong>{token.name}</strong>
      </td>
      <td>
        <code>{token.prefix}</code>
      </td>
      <td>
        <code>{token.scopes.join(', ')}</code>
      </td>
      <td>{token.last_used_at ? formatTs(token.last_used_at) : <Dash />}</td>
      <td>{token.expires_at ? formatTs(token.expires_at) : <Dash />}</td>
      <td>
        <Pill status={status.pillStatus}>{status.label}</Pill>
      </td>
      <td style={{ textAlign: 'right' }}>
        {status.label === 'Active' ? (
          <button onClick={revoke} disabled={busy} className="lv-btn lv-btn-ghost">
            Revoke
          </button>
        ) : (
          <span className="muted" style={{ fontSize: '0.85rem' }}>
            —
          </span>
        )}
      </td>
    </tr>
  );
}

function tokenStatus(t: api.ApiToken): { label: string; pillStatus: 'ok' | 'warn' | 'bad' } {
  if (t.revoked_at) return { label: 'Revoked', pillStatus: 'bad' };
  if (t.expires_at && new Date(t.expires_at) < new Date()) {
    return { label: 'Expired', pillStatus: 'warn' };
  }
  return { label: 'Active', pillStatus: 'ok' };
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}
