import { NavLink, Navigate, Route, Routes, useNavigate } from 'react-router-dom';
import { clearToken, getToken } from './api';
import Login from './pages/Login';
import Clusters from './pages/Clusters';

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
  return (
    <>
      <header className="app">
        <div style={{ display: 'flex', alignItems: 'center', gap: '2rem' }}>
          <h1>Argos CMDB</h1>
          <nav>
            <NavLink to="/clusters" className={({ isActive }) => (isActive ? 'active' : '')}>
              Clusters
            </NavLink>
          </nav>
        </div>
        <button onClick={logout}>Sign out</button>
      </header>
      <main>{children}</main>
    </>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/clusters"
        element={
          <RequireAuth>
            <Chrome>
              <Clusters />
            </Chrome>
          </RequireAuth>
        }
      />
      <Route path="*" element={<Navigate to="/clusters" replace />} />
    </Routes>
  );
}
