import { FormEvent, useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ApiError, getAuthConfig, login } from '../api';
import { LogoIcon } from '../Logo';

// Username + password login per ADR-0007. A successful POST sets the
// session cookie server-side; we never see the cookie value in JS.
//
// OIDC button, when the backend reports oidc.enabled=true, takes the
// browser to /v1/auth/oidc/authorize. longue-vue redirects to the IdP; the
// IdP bounces back to /v1/auth/oidc/callback, which finishes the
// exchange, mints a session cookie, and redirects to /ui/.

const OIDC_ERROR_MESSAGES: Record<string, string> = {
  state_expired_or_unknown: 'Sign-in link expired or already used. Try again.',
  exchange_failed: 'The identity provider rejected the sign-in. Try again or contact an admin.',
  user_lookup_failed: 'Could not load your user account. An admin should check longue-vue logs.',
  session_create_failed: 'Could not create your session. An admin should check longue-vue logs.',
  session_mint_failed: 'Could not mint a session id. An admin should check longue-vue logs.',
};

export default function Login() {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [oidc, setOidc] = useState<{ enabled: boolean; label?: string } | null>(null);
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  useEffect(() => {
    let cancelled = false;
    getAuthConfig()
      .then((cfg) => { if (!cancelled) setOidc(cfg.oidc); })
      .catch(() => { if (!cancelled) setOidc({ enabled: false }); });
    return () => { cancelled = true; };
  }, []);

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
    window.location.href = '/v1/auth/oidc/authorize';
  };

  return (
    <div className="auth-page">
      <form className="auth-card" onSubmit={onSubmit} noValidate>

        {/* ── Header ─────────────────────────────────────── */}
        <div className="auth-header">
          <div className="auth-logo-ring">
            <LogoIcon size={48} />
          </div>
          <div className="auth-brand">
            <span className="auth-brand-light">longue</span>
            <span className="auth-brand-bold">-vue</span>
          </div>
          <div className="auth-tagline">CMDB · SecNumCloud</div>
        </div>

        {/* ── Body ───────────────────────────────────────── */}
        <div className="auth-body">
          <h2 className="auth-title">Sign in</h2>

          <p className="auth-hint">
            First install? Check the <code>LONGUE-VUE FIRST-RUN BOOTSTRAP</code> banner
            in the startup log for the initial admin password.
          </p>

          {oidcErrorMessage && (
            <div className="auth-error">{oidcErrorMessage}</div>
          )}

          <div className="auth-field">
            <label htmlFor="username">Username</label>
            <div className="auth-input-wrap">
              <svg className="auth-input-icon" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                <path d="M10 9a3 3 0 100-6 3 3 0 000 6zm-7 9a7 7 0 1114 0H3z" />
              </svg>
              <input
                id="username"
                type="text"
                autoComplete="username"
                autoFocus
                placeholder="your username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </div>
          </div>

          <div className="auth-field">
            <label htmlFor="password">Password</label>
            <div className="auth-input-wrap">
              <svg className="auth-input-icon" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                <path fillRule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clipRule="evenodd" />
              </svg>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                placeholder="••••••••••••"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
          </div>

          <button className="auth-submit" type="submit" disabled={busy}>
            {busy ? (
              <>
                <span className="auth-spinner" aria-hidden="true" />
                Signing in…
              </>
            ) : 'Sign in'}
          </button>

          {error && <div className="auth-error">{error}</div>}

          {oidc?.enabled && (
            <>
              <div className="auth-divider"><span>or</span></div>
              <button type="button" className="auth-oidc" onClick={startOidc}>
                <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
                  <circle cx="10" cy="10" r="8" />
                  <path d="M10 2a8 8 0 010 16M2 10h16" strokeLinecap="round" />
                </svg>
                Sign in with {oidc.label || 'OIDC'}
              </button>
            </>
          )}
        </div>
      </form>
    </div>
  );
}
