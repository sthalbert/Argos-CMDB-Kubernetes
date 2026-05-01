import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import TokensPage from './Tokens';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureToken } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('TokensPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    // Wait for AsyncView to resolve; "Machine tokens" is the SectionTitle from the page itself.
    await waitFor(() =>
      expect(screen.getByText(/machine tokens/i)).toBeInTheDocument(),
    );
  });

  it('renders the token list on ready', async () => {
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    await waitFor(() =>
      expect(screen.getByText(fixtureToken.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/tokens', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<TokensPage />), { initialPath: '/admin/tokens' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
