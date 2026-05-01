import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import AuditPage from './Audit';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureAuditEvent } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('AuditPage', () => {
  it('renders without crashing', () => {
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    expect(screen.getByText(/audit log/i)).toBeInTheDocument();
  });

  it('renders the audit event list on ready', async () => {
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() =>
      expect(screen.getByText(fixtureAuditEvent.action)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/audit', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<AuditPage />), { initialPath: '/admin/audit' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
