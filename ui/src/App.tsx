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
import AdminLayout from './pages/admin/AdminLayout';
import UsersPage from './pages/admin/Users';
import TokensPage from './pages/admin/Tokens';
import SessionsPage from './pages/admin/Sessions';
import AuditPage from './pages/admin/Audit';
import { MeProvider } from './me';

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

function Chrome({ me, children }: { me: api.Me; children: React.ReactNode }) {
  const navigate = useNavigate();
  const signOut = async () => {
    try {
      await api.logout();
    } catch {
      // Ignore; we're clearing local state either way.
    }
    navigate('/login', { replace: true });
  };
  const link = (to: string, label: string) => (
    <NavLink to={to} className={({ isActive }) => (isActive ? 'active' : '')}>
      {label}
    </NavLink>
  );
  return (
    <>
      <header className="app">
        <div style={{ display: 'flex', alignItems: 'center', gap: '2rem', flexWrap: 'wrap' }}>
          <h1>Argos CMDB</h1>
          <nav style={{ display: 'flex', gap: '0.75rem', flexWrap: 'wrap' }}>
            {link('/clusters', 'Clusters')}
            {link('/namespaces', 'Namespaces')}
            {link('/nodes', 'Nodes')}
            {link('/workloads', 'Workloads')}
            {link('/pods', 'Pods')}
            {link('/services', 'Services')}
            {link('/ingresses', 'Ingresses')}
            {link('/persistentvolumes', 'PVs')}
            {link('/persistentvolumeclaims', 'PVCs')}
            {link('/search/image', 'Search')}
            {(me.role === 'admin' || me.role === 'auditor') &&
              link(me.role === 'admin' ? '/admin/users' : '/admin/audit', 'Admin')}
          </nav>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
          <span className="muted" style={{ fontSize: '0.85rem' }}>
            {me.username} <span className="pill">{me.role}</span>
          </span>
          <button onClick={signOut}>Sign out</button>
        </div>
      </header>
      <main>{children}</main>
    </>
  );
}

// --- routes -------------------------------------------------------------

export default function App() {
  const auth = useAuth();

  const authed = (el: React.ReactNode) => (
    <RequireAuth auth={auth}>
      {auth.status === 'ready' && (
        <MeProvider value={auth.me}>
          <Chrome me={auth.me}>{el}</Chrome>
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
        <Route path="audit" element={<AuditPage />} />
      </Route>

      <Route path="*" element={<Navigate to="/clusters" replace />} />
    </Routes>
  );
}
