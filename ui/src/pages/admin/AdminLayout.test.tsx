import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import AdminLayout from './AdminLayout';
import { MeProvider } from '../../me';
import { fixtureMe } from '../../test/fixtures';

describe('AdminLayout', () => {
  it('renders the admin chrome for an admin role', () => {
    render(
      <MemoryRouter initialEntries={['/admin/users']}>
        <MeProvider value={fixtureMe}>
          <Routes>
            <Route path="/admin/*" element={<AdminLayout role="admin" />} />
          </Routes>
        </MeProvider>
      </MemoryRouter>,
    );
    expect(screen.getByText('Users')).toBeInTheDocument();
  });

  it('renders only the audit tab for an auditor role', () => {
    render(
      <MemoryRouter initialEntries={['/admin/audit']}>
        <MeProvider value={{ ...fixtureMe, role: 'auditor' }}>
          <Routes>
            <Route path="/admin/*" element={<AdminLayout role="auditor" />} />
          </Routes>
        </MeProvider>
      </MemoryRouter>,
    );
    expect(screen.getByText('Audit')).toBeInTheDocument();
    expect(screen.queryByText('Users')).toBeNull();
  });
});
