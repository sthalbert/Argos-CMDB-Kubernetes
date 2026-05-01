import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import CloudAccountsPage from './CloudAccounts';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureCloudAccount } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('CloudAccountsPage', () => {
  it('renders without crashing', async () => {
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    // Wait for AsyncView to resolve; "Cloud accounts" is the SectionTitle from the page itself.
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /^cloud accounts/i })).toBeInTheDocument(),
    );
  });

  it('renders the cloud account list on ready', async () => {
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureCloudAccount.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/cloud-accounts', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<CloudAccountsPage />), {
      initialPath: '/admin/cloud-accounts',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
