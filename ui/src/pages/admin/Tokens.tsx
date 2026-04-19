import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';

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
    <div className="reveal-callout">
      <div className="reveal-header">
        <strong>Token minted — shown once, copy it now</strong>
        <button onClick={onDismiss}>Dismiss</button>
      </div>
      <p className="muted" style={{ marginTop: '0.25rem', fontSize: '0.85rem' }}>
        argosd stores only the argon2id hash. Once you dismiss this banner the
        plaintext is gone — if you lose it, revoke the token and mint a new one.
      </p>
      <div className="reveal-value">
        <code>{minted.token}</code>
        <button onClick={copy} className="primary">
          {copied ? 'Copied ✓' : 'Copy'}
        </button>
      </div>
      <p className="muted" style={{ fontSize: '0.8rem' }}>
        Name: <strong>{minted.name}</strong> · Scopes:{' '}
        <code>{minted.scopes.join(', ')}</code> · Prefix: <code>{minted.prefix}</code>
      </p>
    </div>
  );
}

function MintForm({
  reload,
  onMinted,
}: {
  reload: Reload;
  onMinted: (m: api.ApiTokenMint) => void;
}) {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<Record<api.TokenScope, boolean>>({
    read: true,
    write: false,
    delete: false,
  });
  const [expiresAt, setExpiresAt] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!name.trim()) {
      setError('Name required.');
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
        <button className="primary" onClick={() => setOpen(true)}>
          + Mint token
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>Mint machine token</h3>
      <div className="admin-form-row">
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
      <div className="admin-form-actions">
        <button type="submit" disabled={busy} className="primary">
          {busy ? 'Minting…' : 'Mint token'}
        </button>
        <button type="button" onClick={() => setOpen(false)} disabled={busy}>
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
        <span className={`pill ${status.cls}`}>{status.label}</span>
      </td>
      <td style={{ textAlign: 'right' }}>
        {status.label === 'Active' ? (
          <button onClick={revoke} disabled={busy} className="danger">
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

function tokenStatus(t: api.ApiToken): { label: string; cls: string } {
  if (t.revoked_at) return { label: 'Revoked', cls: 'status-bad' };
  if (t.expires_at && new Date(t.expires_at) < new Date()) {
    return { label: 'Expired', cls: 'status-warn' };
  }
  return { label: 'Active', cls: 'status-ok' };
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}
