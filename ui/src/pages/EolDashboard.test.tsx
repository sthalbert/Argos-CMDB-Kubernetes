import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import EolDashboard from './EolDashboard';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

describe('EolDashboard', () => {
  it('renders without crashing', () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    expect(screen.getByRole('heading', { name: /lifecycle/i })).toBeInTheDocument();
  });

  it('shows empty-state message once fetches resolve with no annotations', async () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    await waitFor(() =>
      expect(screen.getByText(/no eol data available/i)).toBeInTheDocument(),
    );
  });

  it('handles an upstream error', async () => {
    server.use(
      http.get('/v1/clusters', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/nodes', () => new HttpResponse(null, { status: 500 })),
      http.get('/v1/virtual-machines', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
