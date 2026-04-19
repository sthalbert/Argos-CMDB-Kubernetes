import { FormEvent, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, login } from '../api';

// Username + password login per ADR-0007. A successful POST sets the
// session cookie server-side; we never see the cookie value in JS.
// OIDC button space-reserved for PR #3 (the OIDC landing PR).

export default function Login() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!username.trim() || !password) {
      setError('Username and password required.');
      return;
    }
    setBusy(true);
    try {
      await login(username.trim(), password);
      // The server responds 204 and sets the session cookie; the UI
      // doesn't need to read anything back. Land on /clusters; the
      // /auth/me probe wired into the Chrome will redirect to the
      // change-password page if must_change_password is true.
      navigate('/clusters', { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        // 401 is the common case — opaque "invalid credentials" per
        // the ADR. Surface it as-is.
        setError(err.message || 'Invalid credentials.');
      } else {
        setError(`Network error: ${String(err)}`);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="login" onSubmit={onSubmit}>
      <h2>Sign in</h2>
      <p className="muted" style={{ marginTop: 0, fontSize: '0.85rem' }}>
        First install? Read the <code>ARGOS FIRST-RUN BOOTSTRAP</code> banner
        in the argosd startup log for the initial admin password.
      </p>

      <label htmlFor="username">Username</label>
      <input
        id="username"
        type="text"
        autoComplete="username"
        autoFocus
        value={username}
        onChange={(e) => setUsername(e.target.value)}
      />

      <label htmlFor="password" style={{ marginTop: '0.75rem' }}>
        Password
      </label>
      <input
        id="password"
        type="password"
        autoComplete="current-password"
        value={password}
        onChange={(e) => setPassword(e.target.value)}
      />

      <button type="submit" disabled={busy}>
        {busy ? 'Signing in…' : 'Sign in'}
      </button>
      {error && <div className="error">{error}</div>}
    </form>
  );
}
