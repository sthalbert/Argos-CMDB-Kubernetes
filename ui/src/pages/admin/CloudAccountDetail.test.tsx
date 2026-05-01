import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import CloudAccountDetailPage from './CloudAccountDetail';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe, fixtureCloudAccount } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('CloudAccountDetailPage', () => {
  it('renders without crashing', () => {
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
  });

  it('renders the account detail on ready', async () => {
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    await waitFor(() =>
      expect(
        screen.getByRole('heading', { level: 2, name: new RegExp(fixtureCloudAccount.name, 'i') }),
      ).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/cloud-accounts/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<CloudAccountDetailPage />), {
      initialPath: `/admin/cloud-accounts/${fixtureCloudAccount.id}`,
      routePath: '/admin/cloud-accounts/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
