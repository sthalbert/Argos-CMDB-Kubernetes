import { NavLink, Outlet } from 'react-router-dom';

// AdminLayout wraps the three admin sub-pages with a shared tab-style
// sub-nav. The outer <Chrome> (top nav + role pill + sign-out) comes
// from App.tsx; this layout sits inside <main>.

export default function AdminLayout() {
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
        {tab('/admin/users', 'Users')}
        {tab('/admin/tokens', 'Machine tokens')}
        {tab('/admin/sessions', 'Active sessions')}
      </nav>
      <Outlet />
    </>
  );
}
