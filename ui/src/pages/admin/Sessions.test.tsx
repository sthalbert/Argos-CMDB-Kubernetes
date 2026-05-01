import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import SessionsPage from './Sessions';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureSession } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('SessionsPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<SessionsPage />), { initialPath: '/admin/sessions' });
    // Wait for AsyncView to resolve; "Active sessions" is the SectionTitle from the page itself.
    await waitFor(() =>
      expect(screen.getByText(/active sessions/i)).toBeInTheDocument(),
    );
  });

  it('renders the session list on ready', async () => {
    renderWithRouter(withAdmin(<SessionsPage />), { initialPath: '/admin/sessions' });
    await waitFor(() =>
      expect(screen.getByText(fixtureSession.username!)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/sessions', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<SessionsPage />), { initialPath: '/admin/sessions' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
