import { NavLink, Outlet, useLocation } from 'react-router-dom';
import type { Role } from '../../api';

// AdminLayout wraps the admin sub-pages with a shared tab-style sub-nav.
// The outer <Chrome> (top nav + role pill + sign-out) comes from
// App.tsx; this layout sits inside <main>. Admins see every tab;
// auditors only get the read-only Audit tab (the /v1/admin/users|tokens
// |sessions endpoints require `admin` scope server-side anyway).

export default function AdminLayout({ role }: { role: Role }) {
  const location = useLocation();
  // The Cloud accounts tab covers both list (/admin/cloud-accounts) and
  // detail (/admin/cloud-accounts/:id), so we mark it active for any
  // path under that prefix instead of relying on react-router's strict
  // `end` matching.
  const tab = (to: string, label: string, prefix?: string) => {
    const active = prefix ? location.pathname.startsWith(prefix) : undefined;
    return (
      <NavLink
        to={to}
        className={({ isActive }) =>
          'admin-tab' + ((active ?? isActive) ? ' active' : '')
        }
        end={!prefix}
      >
        {label}
      </NavLink>
    );
  };
  return (
    <>
      <h2>Admin</h2>
      <nav className="admin-subnav">
        {role === 'admin' && tab('/admin/users', 'Users')}
        {role === 'admin' && tab('/admin/tokens', 'Machine tokens')}
        {role === 'admin' && tab('/admin/sessions', 'Active sessions')}
        {role === 'admin' &&
          tab('/admin/cloud-accounts', 'Cloud accounts', '/admin/cloud-accounts')}
        {role === 'admin' && tab('/admin/image-registries', 'Image registries')}
        {role === 'admin' && tab('/admin/application-blocks', 'Application blocks')}
        {role === 'admin' && tab('/admin/classification', 'Classification')}
        {tab('/admin/audit', 'Audit')}
        {role === 'admin' && tab('/admin/settings', 'Settings')}
      </nav>
      <Outlet />
    </>
  );
}
