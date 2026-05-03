import { useLocation, useNavigate, Outlet } from 'react-router-dom';
import type { Role } from '../../api';
import { PageHead } from '../../components/lv/PageHead';
import { Tabs } from '../../components/lv/Tabs';

// AdminLayout wraps the admin sub-pages with a shared tab-style sub-nav.
// The outer top-bar (role pill + sign-out) comes from App.tsx; this
// layout sits inside <main>. Admins see every tab; auditors only get the
// read-only Audit tab.

const TABS = [
  { key: 'users', label: 'Users', roles: ['admin'] },
  { key: 'tokens', label: 'Tokens', roles: ['admin'] },
  { key: 'sessions', label: 'Sessions', roles: ['admin'] },
  { key: 'cloud-accounts', label: 'Cloud accounts', roles: ['admin'] },
  { key: 'audit', label: 'Audit', roles: ['admin', 'auditor'] },
  { key: 'settings', label: 'Settings', roles: ['admin'] },
];

export default function AdminLayout({ role }: { role: Role }) {
  const location = useLocation();
  const navigate = useNavigate();
  const visible = TABS.filter((t) => t.roles.includes(role));
  // The second segment of /admin/<key>[/...] identifies the active tab.
  // Cloud-account detail pages sit under cloud-accounts/ so startsWith
  // keeps the tab highlighted on drill-down.
  const segment = location.pathname.split('/')[2] ?? 'users';
  const active = visible.find((t) => segment.startsWith(t.key))?.key ?? segment;
  return (
    <>
      <PageHead title="Admin" />
      <Tabs
        items={visible.map(({ key, label }) => ({ key, label }))}
        active={active}
        onChange={(k) => navigate(`/admin/${k}`)}
      />
      <Outlet />
    </>
  );
}
