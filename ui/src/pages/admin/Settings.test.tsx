import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import SettingsPage from './Settings';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { MeProvider } from '../../me';
import { fixtureMe } from '../../test/fixtures';

function withAdmin(el: ReactElement) {
  return <MeProvider value={fixtureMe}>{el}</MeProvider>;
}

describe('SettingsPage', () => {
  it('renders without crashing', () => {
    renderWithRouter(withAdmin(<SettingsPage />), { initialPath: '/admin/settings' });
    expect(screen.getByRole('heading', { level: 3, name: /^settings$/i })).toBeInTheDocument();
  });

  it('renders the settings toggles on ready', async () => {
    renderWithRouter(withAdmin(<SettingsPage />), { initialPath: '/admin/settings' });
    await waitFor(() =>
      expect(screen.getByText(/end-of-life enrichment/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/mcp server/i)).toBeInTheDocument();
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/admin/settings', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(withAdmin(<SettingsPage />), { initialPath: '/admin/settings' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
