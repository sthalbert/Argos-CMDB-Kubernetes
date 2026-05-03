import { useEffect, useState } from 'react';
import { NavLink, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import * as api from './api';
import Login from './pages/Login';
import ChangePassword from './pages/ChangePassword';
import ImageSearch from './pages/Search';
import {
  Clusters,
  Nodes,
  Namespaces,
  Workloads,
  Pods,
  Services,
  Ingresses,
  PersistentVolumes,
  PersistentVolumeClaims,
} from './pages/Lists';
import {
  ClusterDetail,
  NamespaceDetail,
  WorkloadDetail,
  PodDetail,
  NodeDetail,
  IngressDetail,
} from './pages/Details';
import EolDashboard from './pages/EolDashboard';
import VirtualMachines from './pages/VirtualMachines';
import VirtualMachineDetail from './pages/VirtualMachineDetail';
import AdminLayout from './pages/admin/AdminLayout';
import UsersPage from './pages/admin/Users';
import TokensPage from './pages/admin/Tokens';
import SessionsPage from './pages/admin/Sessions';
import AuditPage from './pages/admin/Audit';
import SettingsPage from './pages/admin/Settings';
import CloudAccountsPage from './pages/admin/CloudAccounts';
import CloudAccountDetail from './pages/admin/CloudAccountDetail';
import { MeProvider } from './me';
import { Brand } from './components/lv/Brand';
import { Pill } from './components/lv/Pill';
import { Disclosure } from './components/lv/Disclosure';
import { UiPrefsProvider } from './ui-prefs';
import { UiPrefsPanel } from './components/lv/UiPrefsPanel';

// --- auth gate ----------------------------------------------------------

// AuthState mirrors GET /v1/auth/me. `none` = not logged in,
// `loading` = probe in flight, `ready` = me known.
type AuthState =
  | { status: 'loading' }
  | { status: 'none' }
  | { status: 'ready'; me: api.Me };

// useAuth probes /v1/auth/me on mount and on route changes. A 401 from
// the probe just means "not logged in"; network errors also land on
// `none` (the login page surfaces them on the next attempt).
function useAuth(): AuthState {
  const [state, setState] = useState<AuthState>({ status: 'loading' });
  const location = useLocation();
  useEffect(() => {
    let cancelled = false;
    api
      .getMe()
      .then((me) => {
        if (!cancelled) setState({ status: 'ready', me });
      })
      .catch(() => {
        if (!cancelled) setState({ status: 'none' });
      });
    return () => {
      cancelled = true;
    };
  }, [location.pathname]);
  return state;
}

function RequireAuth({ auth, children }: { auth: AuthState; children: React.ReactNode }) {
  if (auth.status === 'loading') return <p className="loading">Checking session…</p>;
  if (auth.status === 'none') return <Navigate to="/login" replace />;
  // Forced rotation: the API will 403 every other endpoint until the
  // user rotates. Redirect proactively so pages don't flash errors.
  if (auth.me.must_change_password) {
    return <Navigate to="/change-password" replace />;
  }
  return <>{children}</>;
}

// RequireAdmin wraps the admin routes. Server-side the /v1/admin/*
// endpoints already enforce admin / audit scope; this is just UX —
// redirect non-admin-non-auditor roles back to /clusters instead of
// letting them browse pages that will 403 every request. Auditors get
// in so they can reach /admin/audit, but individual tabs gate on
// role === 'admin' inside AdminLayout.
function RequireAdmin({ auth, children }: { auth: AuthState; children: React.ReactNode }) {
  if (auth.status === 'ready' && auth.me.role !== 'admin' && auth.me.role !== 'auditor') {
    return <Navigate to="/clusters" replace />;
  }
  return <>{children}</>;
}

// --- chrome -------------------------------------------------------------

const PRIMARY_NAV: Array<{ to: string; label: string; roles?: api.Me['role'][] }> = [
  { to: '/clusters',         label: 'Clusters' },
  { to: '/workloads',        label: 'Workloads' },
  { to: '/nodes',            label: 'Nodes' },
  { to: '/virtual-machines', label: 'Virtual Machines' },
  { to: '/eol',              label: 'Lifecycle' },
  { to: '/search/image',     label: 'Search' },
];

const MORE_ITEMS: Array<{ to: string; label: string }> = [
  { to: '/namespaces',             label: 'Namespaces' },
  { to: '/pods',                   label: 'Pods' },
  { to: '/services',               label: 'Services' },
  { to: '/ingresses',              label: 'Ingresses' },
  { to: '/persistentvolumes',      label: 'PVs' },
  { to: '/persistentvolumeclaims', label: 'PVCs' },
];

function pathPrefix(pathname: string): string {
  const m = /^\/[^/]+/.exec(pathname);
  return m ? m[0] : '/';
}

function Chrome({ me, children }: { me: api.Me; children: React.ReactNode }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(id);
  }, []);

  const ts = now.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  const activePrefix = pathPrefix(location.pathname);

  const signOut = async () => {
    try { await api.logout(); } catch { /* ignore */ }
    navigate('/login', { replace: true });
  };

  const adminEntry = me.role === 'admin'
    ? { to: '/admin/users', label: 'Admin' }
    : me.role === 'auditor'
    ? { to: '/admin/audit', label: 'Admin' }
    : null;

  const auditNavEntry = (me.role === 'admin' || me.role === 'auditor');

  return (
    <div className="lv-app">
      <header className="lv-header">
        <Brand />
        <nav className="lv-nav">
          {PRIMARY_NAV.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              className={() => `lv-nav-link ${pathPrefix(it.to) === activePrefix ? 'active' : ''}`}
            >
              {it.label}
            </NavLink>
          ))}
          <Disclosure
            trigger={({ open, toggle }) => (
              <button
                type="button"
                className={`lv-nav-link${open ? ' active' : ''}`}
                aria-haspopup="menu"
                aria-expanded={open}
                onClick={toggle}
              >
                More ▾
              </button>
            )}
          >
            {({ close }) => (
              <div role="menu">
                {MORE_ITEMS.map((it) => (
                  <NavLink
                    key={it.to}
                    to={it.to}
                    className="lv-popover-item"
                    onClick={close}
                  >
                    {it.label}
                  </NavLink>
                ))}
              </div>
            )}
          </Disclosure>
          {auditNavEntry && (
            <NavLink
              to="/admin/audit"
              className={() => `lv-nav-link ${activePrefix === '/admin' ? 'active' : ''}`}
            >
              Audit
            </NavLink>
          )}
        </nav>
        <div className="lv-header-right">
          <span className="lv-time" aria-label={`polled ${ts}`}>
            <span className="lv-time-dot" />
            polled {ts}
          </span>
          <Disclosure
            trigger={({ open, toggle }) => (
              <button
                type="button"
                className="lv-iconbtn"
                aria-haspopup="menu"
                aria-expanded={open}
                onClick={toggle}
              >
                <span className="lv-user">
                  <span>{me.username}</span>
                  <Pill status="accent">{me.role}</Pill>
                </span>
              </button>
            )}
          >
            {({ close }) => (
              <div role="menu">
                <div className="lv-popover-section-label">Signed in as {me.username}</div>
                <div className="lv-popover-divider" />
                <UiPrefsPanel />
                {adminEntry && (
                  <>
                    <div className="lv-popover-divider" />
                    <NavLink to={adminEntry.to} className="lv-popover-item" onClick={close}>
                      {adminEntry.label}
                    </NavLink>
                  </>
                )}
                <div className="lv-popover-divider" />
                <button type="button" className="lv-popover-item" onClick={() => { close(); signOut(); }}>
                  Sign out
                </button>
              </div>
            )}
          </Disclosure>
        </div>
      </header>
      <main className="lv-main">{children}</main>
    </div>
  );
}

// --- routes -------------------------------------------------------------

export default function App() {
  const auth = useAuth();

  const authed = (el: React.ReactNode) => (
    <RequireAuth auth={auth}>
      {auth.status === 'ready' && (
        <MeProvider value={auth.me}>
          <UiPrefsProvider>
            <Chrome me={auth.me}>{el}</Chrome>
          </UiPrefsProvider>
        </MeProvider>
      )}
    </RequireAuth>
  );

  return (
    <Routes>
      <Route
        path="/login"
        element={
          auth.status === 'ready' && !auth.me.must_change_password ? (
            <Navigate to="/clusters" replace />
          ) : (
            <Login />
          )
        }
      />
      <Route
        path="/change-password"
        element={
          auth.status === 'none' ? (
            <Navigate to="/login" replace />
          ) : (
            <ChangePassword forced={auth.status === 'ready' && !!auth.me.must_change_password} />
          )
        }
      />

      <Route path="/clusters" element={authed(<Clusters />)} />
      <Route path="/clusters/:id" element={authed(<ClusterDetail />)} />

      <Route path="/namespaces" element={authed(<Namespaces />)} />
      <Route path="/namespaces/:id" element={authed(<NamespaceDetail />)} />

      <Route path="/nodes" element={authed(<Nodes />)} />
      <Route path="/nodes/:id" element={authed(<NodeDetail />)} />

      <Route path="/workloads" element={authed(<Workloads />)} />
      <Route path="/workloads/:id" element={authed(<WorkloadDetail />)} />

      <Route path="/pods" element={authed(<Pods />)} />
      <Route path="/pods/:id" element={authed(<PodDetail />)} />

      <Route path="/services" element={authed(<Services />)} />
      <Route path="/ingresses" element={authed(<Ingresses />)} />
      <Route path="/ingresses/:id" element={authed(<IngressDetail />)} />
      <Route path="/persistentvolumes" element={authed(<PersistentVolumes />)} />
      <Route path="/persistentvolumeclaims" element={authed(<PersistentVolumeClaims />)} />

      <Route path="/search/image" element={authed(<ImageSearch />)} />
      <Route path="/eol" element={authed(<EolDashboard />)} />

      <Route path="/virtual-machines" element={authed(<VirtualMachines />)} />
      <Route path="/virtual-machines/:id" element={authed(<VirtualMachineDetail />)} />

      {/* Admin panel — admins see every tab; auditors only get Audit. */}
      <Route
        path="/admin"
        element={authed(
          <RequireAdmin auth={auth}>
            {auth.status === 'ready' && <AdminLayout role={auth.me.role ?? 'viewer'} />}
          </RequireAdmin>,
        )}
      >
        <Route
          index
          element={
            <Navigate to={auth.status === 'ready' && auth.me.role === 'admin' ? 'users' : 'audit'} replace />
          }
        />
        <Route path="users" element={<UsersPage />} />
        <Route path="tokens" element={<TokensPage />} />
        <Route path="sessions" element={<SessionsPage />} />
        <Route path="cloud-accounts" element={<CloudAccountsPage />} />
        <Route path="cloud-accounts/:id" element={<CloudAccountDetail />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="settings" element={<SettingsPage />} />
      </Route>

      <Route path="*" element={<Navigate to="/clusters" replace />} />
    </Routes>
  );
}
