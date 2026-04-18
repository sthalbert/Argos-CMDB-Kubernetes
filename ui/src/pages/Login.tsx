import { FormEvent, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, getHealthz, setToken } from '../api';

export default function Login() {
  const [token, setTokenInput] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!token.trim()) {
      setError('Token required.');
      return;
    }
    setBusy(true);
    // Stash the token so the first authenticated call can read it, then
    // probe a cheap endpoint to confirm the token is accepted. /healthz is
    // unauthenticated but exercises the network path; a follow-up
    // whoami / scope-probe endpoint will replace this once it exists.
    setToken(token.trim());
    try {
      await getHealthz();
      navigate('/clusters', { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(`Server rejected the token (${err.status}): ${err.message}`);
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
        Paste an API token issued via <code>ARGOS_API_TOKEN</code> or{' '}
        <code>ARGOS_API_TOKENS</code>. The token is kept in this tab only.
      </p>
      <label htmlFor="token">Bearer token</label>
      <input
        id="token"
        type="password"
        autoComplete="off"
        autoFocus
        value={token}
        onChange={(e) => setTokenInput(e.target.value)}
        placeholder="e.g. 1f8b0a…"
      />
      <button type="submit" disabled={busy}>
        {busy ? 'Signing in…' : 'Sign in'}
      </button>
      {error && <div className="error">{error}</div>}
    </form>
  );
}
