import { useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';

// Admin Sessions page. Read-only table with a revoke action. The `id`
// column is the server-side public UUID, never the cookie value —
// admins can address a row without being able to impersonate the user
// by copying it into their own browser.

export default function SessionsPage() {
  const [nonce, setNonce] = useState(0);
  const reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.listSessions(), [nonce]);

  return (
    <AsyncView state={state}>
      {(resp) => (
        <>
          <SectionTitle count={resp.items.length}>Active sessions</SectionTitle>
          <p className="muted" style={{ fontSize: '0.85rem', marginTop: 0 }}>
            Only non-expired sessions are listed. Revoking a session logs that
            user's tab out server-side — the next request the browser makes
            bounces back to the login page.
          </p>
          {resp.items.length === 0 ? (
            <p className="muted">No active sessions.</p>
          ) : (
            <table className="entities">
              <thead>
                <tr>
                  <th>User</th>
                  <th>Created</th>
                  <th>Last used</th>
                  <th>Expires</th>
                  <th>User agent</th>
                  <th>Source IP</th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {resp.items.map((s) => (
                  <SessionRow key={s.id} session={s} reload={reload} />
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
    </AsyncView>
  );
}

function SessionRow({ session, reload }: { session: api.Session; reload: () => void }) {
  const [busy, setBusy] = useState(false);
  const revoke = async () => {
    if (!confirm(`Revoke session for ${session.username || session.user_id}?`)) return;
    setBusy(true);
    try {
      await api.revokeSession(session.id);
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
        <strong>{session.username || session.user_id}</strong>
      </td>
      <td>{formatTs(session.created_at)}</td>
      <td>{formatTs(session.last_used_at)}</td>
      <td>{formatTs(session.expires_at)}</td>
      <td>
        {session.user_agent ? (
          <span className="muted" style={{ fontSize: '0.8rem' }}>
            {session.user_agent}
          </span>
        ) : (
          <Dash />
        )}
      </td>
      <td>{session.source_ip ? <code>{session.source_ip}</code> : <Dash />}</td>
      <td style={{ textAlign: 'right' }}>
        <button onClick={revoke} disabled={busy} className="danger">
          Revoke
        </button>
      </td>
    </tr>
  );
}

function formatTs(ts: string): string {
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}
