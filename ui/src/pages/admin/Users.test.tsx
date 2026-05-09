import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import UsersPage from './Users';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureUser } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('UsersPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    // Wait for AsyncView to resolve; "Users" SectionTitle (h3) is from the page itself.
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 3, name: /^users/i })).toBeInTheDocument(),
    );
  });

  it('renders the user list on ready', async () => {
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() =>
      expect(screen.getByText(fixtureUser.username)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/users', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('shows Locked status and Unlock button when locked_at is set', async () => {
    server.use(
      http.get('/v1/admin/users', () =>
        HttpResponse.json({
          items: [
            {
              ...fixtureUser,
              id: 'cccccccc-cccc-cccc-cccc-cccccccccccc',
              username: 'lockedone',
              role: 'editor',
              failed_login_count: 6,
              locked_at: '2026-05-09T10:00:00Z',
            },
          ],
        }),
      ),
    );
    renderWithRouter(withAdmin(<UsersPage />), { initialPath: '/admin/users' });
    await waitFor(() => expect(screen.getByText('lockedone')).toBeInTheDocument());
    expect(screen.getByText('Locked')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /unlock/i })).toBeInTheDocument();
  });
});
