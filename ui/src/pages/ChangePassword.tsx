import { FormEvent, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError, changePassword } from '../api';
import { PageHead } from '../components/lv/PageHead';
import { Callout } from '../components/lv/Callout';

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
      // Server cleared every session for this user, including ours.
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

  return (
    <div style={{ display: 'flex', justifyContent: 'center', padding: '3rem 1.5rem' }}>
      <div className="lv-card" style={{ width: '100%', maxWidth: 460 }}>
        <PageHead
          title={forced ? 'Rotate your password' : 'Change password'}
          sub={forced ? 'You must rotate before continuing.' : undefined}
        />
        {forced && (
          <Callout title="Rotation required" status="warn">
            Your administrator requires you to set a new password.
          </Callout>
        )}
        <form className="login" onSubmit={onSubmit}>
          <label htmlFor="current">Current password</label>
          <input
            id="current"
            type="password"
            autoComplete="current-password"
            autoFocus
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />

          <label htmlFor="next" style={{ marginTop: '0.75rem' }}>
            New password (12+ characters)
          </label>
          <input
            id="next"
            type="password"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />

          <label htmlFor="confirm" style={{ marginTop: '0.75rem' }}>
            Confirm new password
          </label>
          <input
            id="confirm"
            type="password"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
          />

          <button type="submit" disabled={busy}>
            {busy ? 'Rotating…' : 'Change password'}
          </button>
          {error && <div className="error">{error}</div>}
        </form>
      </div>
    </div>
  );
}
