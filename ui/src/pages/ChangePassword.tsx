import { FormEvent, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, changePassword } from '../api';

// Forced-rotation page per ADR-0007. Reached automatically on first
// login for the bootstrap admin (must_change_password=true) and for
// any user whose password was admin-reset. After a successful rotate
// the server invalidates every session for this user — including ours —
// so we land back on /login.

export default function ChangePassword({ forced }: { forced?: boolean }) {
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    if (next.length < 12) {
      setError('New password must be at least 12 characters.');
      return;
    }
    if (next !== confirm) {
      setError('Confirmation does not match.');
      return;
    }
    if (next === current) {
      setError('New password must differ from the current one.');
      return;
    }
    setBusy(true);
    try {
      await changePassword(current, next);
      navigate('/login', { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError(`Network error: ${String(err)}`);
      }
    } finally {
      setBusy(false);
    }
  };

  // Password strength indicator (length-based, simple)
  const strength = next.length === 0 ? 0 : next.length < 12 ? 1 : next.length < 16 ? 2 : 3;
  const strengthLabel = ['', 'Weak', 'Good', 'Strong'][strength];
  const strengthClass = ['', 'auth-strength-weak', 'auth-strength-good', 'auth-strength-strong'][strength];

  return (
    <div className="auth-page">
      <form className="auth-card" onSubmit={onSubmit} noValidate>

        {/* ── Header ─────────────────────────────────────── */}
        <div className="auth-header">
          <div className="auth-logo-ring">
            <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="var(--accent)" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
              <path d="M7 11V7a5 5 0 0110 0v4" />
              <circle cx="12" cy="16" r="1.5" fill="var(--accent)" stroke="none" />
            </svg>
          </div>
          <div className="auth-brand">
            <span className="auth-brand-light">longue</span>
            <span className="auth-brand-bold">-vue</span>
          </div>
          <div className="auth-tagline">
            {forced ? 'Password rotation required' : 'Change your password'}
          </div>
        </div>

        {/* ── Body ───────────────────────────────────────── */}
        <div className="auth-body">
          <h2 className="auth-title">
            {forced ? 'Rotate your password' : 'Change password'}
          </h2>

          {forced && (
            <p className="auth-hint">
              Your account is flagged <code>must_change_password</code>. Set a new password
              to unlock access — this is the only page available until you do.
            </p>
          )}

          <div className="auth-field">
            <label htmlFor="current">Current password</label>
            <div className="auth-input-wrap">
              <svg className="auth-input-icon" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                <path fillRule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clipRule="evenodd" />
              </svg>
              <input
                id="current"
                type="password"
                autoComplete="current-password"
                autoFocus
                placeholder="••••••••••••"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
              />
            </div>
          </div>

          <div className="auth-field">
            <label htmlFor="next">New password <span className="auth-field-hint">12+ characters</span></label>
            <div className="auth-input-wrap">
              <svg className="auth-input-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" strokeWidth={1.5} aria-hidden="true">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.232 5.232l-7.5 7.5M9 4l7 7-3.5 3.5a4.95 4.95 0 01-7-7L9 4z" />
              </svg>
              <input
                id="next"
                type="password"
                autoComplete="new-password"
                placeholder="••••••••••••"
                value={next}
                onChange={(e) => setNext(e.target.value)}
              />
            </div>
            {next.length > 0 && (
              <div className={`auth-strength ${strengthClass}`}>
                <div className="auth-strength-bar">
                  <span style={{ width: `${(strength / 3) * 100}%` }} />
                </div>
                <span className="auth-strength-label">{strengthLabel}</span>
              </div>
            )}
          </div>

          <div className="auth-field">
            <label htmlFor="confirm">
              Confirm new password
              {confirm.length > 0 && next === confirm && (
                <span className="auth-match-ok" aria-label="Passwords match">✓</span>
              )}
            </label>
            <div className="auth-input-wrap">
              <svg className="auth-input-icon" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                <path fillRule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clipRule="evenodd" />
              </svg>
              <input
                id="confirm"
                type="password"
                autoComplete="new-password"
                placeholder="••••••••••••"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
              />
            </div>
          </div>

          <button className="auth-submit" type="submit" disabled={busy}>
            {busy ? (
              <>
                <span className="auth-spinner" aria-hidden="true" />
                Rotating…
              </>
            ) : forced ? 'Rotate password' : 'Change password'}
          </button>

          {error && <div className="auth-error">{error}</div>}
        </div>
      </form>
    </div>
  );
}
