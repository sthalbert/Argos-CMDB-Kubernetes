import { NavLink, Outlet } from 'react-router-dom';
import type { Role } from '../../api';

// AdminLayout wraps the admin sub-pages with a shared tab-style sub-nav.
// The outer <Chrome> (top nav + role pill + sign-out) comes from
// App.tsx; this layout sits inside <main>. Admins see every tab;
// auditors only get the read-only Audit tab (the /v1/admin/users|tokens
// |sessions endpoints require `admin` scope server-side anyway).

export default function AdminLayout({ role }: { role: Role }) {
  const tab = (to: string, label: string) => (
    <NavLink
      to={to}
      className={({ isActive }) => 'admin-tab' + (isActive ? ' active' : '')}
      end
    >
      {label}
    </NavLink>
  );
  return (
    <>
      <h2>Admin</h2>
      <nav className="admin-subnav">
        {role === 'admin' && tab('/admin/users', 'Users')}
        {role === 'admin' && tab('/admin/tokens', 'Machine tokens')}
        {role === 'admin' && tab('/admin/sessions', 'Active sessions')}
        {tab('/admin/audit', 'Audit')}
        {role === 'admin' && tab('/admin/settings', 'Settings')}
      </nav>
      <Outlet />
    </>
  );
}
