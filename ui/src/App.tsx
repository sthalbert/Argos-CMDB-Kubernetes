import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import { clearToken, getToken } from './api';
import Login from './pages/Login';
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
} from './pages/Details';

function RequireAuth({ children }: { children: React.ReactNode }) {
  if (!getToken()) {
    return <Navigate to="/login" replace />;
  }
  return <>{children}</>;
}

function Chrome({ children }: { children: React.ReactNode }) {
  const navigate = useNavigate();
  const logout = () => {
    clearToken();
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
          </nav>
        </div>
        <button onClick={logout}>Sign out</button>
      </header>
      <main>{children}</main>
    </>
  );
}

// Wraps a page element in the auth gate + header/main chrome. Kept as a
// helper so every route below stays a one-liner.
const authed = (el: React.ReactNode) => (
  <RequireAuth>
    <Chrome>{el}</Chrome>
  </RequireAuth>
);

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />

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
      <Route path="/persistentvolumes" element={authed(<PersistentVolumes />)} />
      <Route path="/persistentvolumeclaims" element={authed(<PersistentVolumeClaims />)} />

      <Route path="/search/image" element={authed(<ImageSearch />)} />

      <Route path="*" element={<Navigate to="/clusters" replace />} />
    </Routes>
  );
}
