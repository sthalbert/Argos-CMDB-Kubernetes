import { FormEvent, useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError, getAuthConfig, login } from '../api';
import { ClusterIcon } from '../icons';

// Username + password login per ADR-0007. A successful POST sets the
// session cookie server-side; we never see the cookie value in JS.
//
// OIDC button, when the backend reports oidc.enabled=true, takes the
// browser to /v1/auth/oidc/authorize. argosd redirects to the IdP; the
// IdP bounces back to /v1/auth/oidc/callback, which finishes the
// exchange, mints a session cookie, and redirects to /ui/.

const OIDC_ERROR_MESSAGES: Record<string, string> = {
  state_expired_or_unknown: 'Sign-in link expired or already used. Try again.',
  exchange_failed: 'The identity provider rejected the sign-in. Try again or contact an admin.',
  user_lookup_failed: 'Could not load your user account. An admin should check argosd logs.',
  session_create_failed: 'Could not create your session. An admin should check argosd logs.',
  session_mint_failed: 'Could not mint a session id. An admin should check argosd logs.',
};

export default function Login() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [oidc, setOidc] = useState<{ enabled: boolean; label?: string } | null>(null);
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  // Hit /v1/auth/config once on mount to learn whether OIDC is
  // configured and which label to show on the button. Failures are
  // silent — worst case the OIDC button just doesn't render.
  useEffect(() => {
    let cancelled = false;
    getAuthConfig()
      .then((cfg) => {
        if (!cancelled) setOidc(cfg.oidc);
      })
      .catch(() => {
        if (!cancelled) setOidc({ enabled: false });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Surface any `oidc_error=…` the callback bounced us back with.
  const oidcError = searchParams.get('oidc_error');
  const oidcErrorMessage = oidcError
    ? OIDC_ERROR_MESSAGES[oidcError] || `OIDC sign-in failed: ${oidcError}`
    : null;

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
      navigate('/clusters', { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message || 'Invalid credentials.');
      } else {
        setError(`Network error: ${String(err)}`);
      }
    } finally {
      setBusy(false);
    }
  };

  const startOidc = () => {
    // Full-page navigation — we need the browser to follow the 302 the
    // backend emits, and the IdP's page will redirect back on its own.
    window.location.href = '/v1/auth/oidc/authorize';
  };

  return (
    <form className="login" onSubmit={onSubmit}>
      <div style={{ textAlign: 'center', marginBottom: '1rem' }}>
        <ClusterIcon size={32} style={{ color: 'var(--accent)' }} />
        <div style={{ fontSize: '1.05rem', fontWeight: 600, letterSpacing: '0.02em', marginTop: '0.5rem' }}>
          Argos CMDB
        </div>
      </div>
      <h2>Sign in</h2>
      <p className="muted" style={{ marginTop: 0, fontSize: '0.85rem' }}>
        First install? Read the <code>ARGOS FIRST-RUN BOOTSTRAP</code> banner
        in the argosd startup log for the initial admin password.
      </p>

      {oidcErrorMessage && <div className="error">{oidcErrorMessage}</div>}

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

      {oidc?.enabled && (
        <>
          <div className="login-divider">
            <span>or</span>
          </div>
          <button type="button" className="login-oidc" onClick={startOidc}>
            Sign in with {oidc.label || 'OIDC'}
          </button>
        </>
      )}
    </form>
  );
}
