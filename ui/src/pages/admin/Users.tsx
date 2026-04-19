import { FormEvent, useState } from 'react';
import * as api from '../../api';
import { useResource } from '../../hooks';
import { AsyncView, Dash, SectionTitle } from '../../components';

// Admin Users page: list of humans, new-user collapsible form, inline
// row actions. Destructive actions (delete, disable, reset-password)
// prompt for confirmation before firing.

type Reload = () => void;

export default function UsersPage() {
  const [nonce, setNonce] = useState(0);
  const reload: Reload = () => setNonce((n) => n + 1);
  const state = useResource(() => api.listUsers(), [nonce]);

  return (
    <>
      <AsyncView state={state}>
        {(resp) => (
          <>
            <NewUserForm reload={reload} />
            <SectionTitle count={resp.items.length}>Users</SectionTitle>
            <UserTable users={resp.items} reload={reload} />
          </>
        )}
      </AsyncView>
    </>
  );
}

function NewUserForm({ reload }: { reload: Reload }) {
  const [open, setOpen] = useState(false);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<api.Role>('viewer');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!username.trim() || password.length < 12) {
      setError('Username required, password must be at least 12 characters.');
      return;
    }
    setBusy(true);
    try {
      await api.createUser({
        username: username.trim(),
        password,
        role,
        must_change_password: true,
      });
      setUsername('');
      setPassword('');
      setRole('viewer');
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
          + New user
        </button>
      </div>
    );
  }

  return (
    <form className="admin-form" onSubmit={submit}>
      <h3>New user</h3>
      <div className="admin-form-row">
        <div>
          <label>Username</label>
          <input
            autoFocus
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            pattern="[a-zA-Z0-9._-]+"
          />
        </div>
        <div>
          <label>Password (12+ chars)</label>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <div>
          <label>Role</label>
          <select value={role} onChange={(e) => setRole(e.target.value as api.Role)}>
            <option value="viewer">viewer</option>
            <option value="auditor">auditor</option>
            <option value="editor">editor</option>
            <option value="admin">admin</option>
          </select>
        </div>
      </div>
      <p className="muted" style={{ fontSize: '0.8rem', marginTop: '0.5rem' }}>
        New users have <code>must_change_password=true</code>; they'll rotate on first login.
      </p>
      <div className="admin-form-actions">
        <button type="submit" disabled={busy} className="primary">
          {busy ? 'Creating…' : 'Create user'}
        </button>
        <button type="button" onClick={() => setOpen(false)} disabled={busy}>
          Cancel
        </button>
      </div>
      {error && <div className="error">{error}</div>}
    </form>
  );
}

function UserTable({ users, reload }: { users: api.User[]; reload: Reload }) {
  if (users.length === 0) return <p className="muted">No users.</p>;
  return (
    <table className="entities">
      <thead>
        <tr>
          <th>Username</th>
          <th>Role</th>
          <th>Status</th>
          <th>Last login</th>
          <th>Created</th>
          <th style={{ textAlign: 'right' }}>Actions</th>
        </tr>
      </thead>
      <tbody>
        {users.map((u) => (
          <UserRow key={u.id} user={u} reload={reload} />
        ))}
      </tbody>
    </table>
  );
}

function UserRow({ user, reload }: { user: api.User; reload: Reload }) {
  const [busy, setBusy] = useState(false);

  const withBusy = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      reload();
    } catch (err) {
      alert(err instanceof api.ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const changeRole = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const role = e.target.value as api.Role;
    if (role === user.role) return;
    if (!confirm(`Change ${user.username}'s role to ${role}?`)) return;
    withBusy(() => api.updateUser(user.id, { role }));
  };

  const toggleDisable = () => {
    const disabled = !user.disabled_at;
    const verb = disabled ? 'Disable' : 'Re-enable';
    if (!confirm(`${verb} ${user.username}? ${disabled ? 'All active sessions will be revoked.' : ''}`)) return;
    withBusy(() => api.updateUser(user.id, { disabled }));
  };

  const resetPassword = () => {
    const pw = prompt(`Enter a new password for ${user.username} (12+ chars). They'll be forced to rotate on next login.`);
    if (!pw) return;
    if (pw.length < 12) {
      alert('Password must be at least 12 characters.');
      return;
    }
    withBusy(() => api.updateUser(user.id, { password: pw }));
  };

  const deleteUser = () => {
    if (!confirm(`Delete ${user.username}? This also revokes every session they hold.`)) return;
    withBusy(() => api.deleteUser(user.id));
  };

  return (
    <tr>
      <td>
        <strong>{user.username}</strong>
        {user.must_change_password && (
          <span className="pill status-warn" style={{ marginLeft: '0.5rem', fontSize: '0.7rem' }}>
            must change pw
          </span>
        )}
      </td>
      <td>
        <select value={user.role} onChange={changeRole} disabled={busy}>
          <option value="viewer">viewer</option>
          <option value="auditor">auditor</option>
          <option value="editor">editor</option>
          <option value="admin">admin</option>
        </select>
      </td>
      <td>
        {user.disabled_at ? (
          <span className="pill status-bad">Disabled</span>
        ) : (
          <span className="pill status-ok">Active</span>
        )}
      </td>
      <td>{user.last_login_at ? formatTs(user.last_login_at) : <Dash />}</td>
      <td>{formatTs(user.created_at)}</td>
      <td style={{ textAlign: 'right' }}>
        <button onClick={resetPassword} disabled={busy}>
          Reset pw
        </button>{' '}
        <button onClick={toggleDisable} disabled={busy}>
          {user.disabled_at ? 'Enable' : 'Disable'}
        </button>{' '}
        <button onClick={deleteUser} disabled={busy} className="danger">
          Delete
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
