import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import * as api from '../api';
import EolDashboard from './EolDashboard';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

const eolAnnotation = JSON.stringify({
  product: 'kubernetes',
  cycle: '1.28',
  eol_status: 'eol',
  eol: '2024-10-28',
});

const clusterWithEol = {
  id: 'c1',
  name: 'prod',
  display_name: 'prod',
  annotations: { 'longue-vue.io/eol.kubernetes': eolAnnotation },
};

describe('EolDashboard', () => {
  it('renders without crashing', () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    expect(screen.getByRole('heading', { name: /end-of-life inventory/i })).toBeInTheDocument();
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

describe('EOL Dashboard extract button', () => {
  beforeEach(() => {
    server.use(
      http.get('/v1/clusters', () =>
        HttpResponse.json({ items: [clusterWithEol], next_cursor: null }),
      ),
      http.get('/v1/nodes', () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get('/v1/virtual-machines', () => HttpResponse.json({ items: [], next_cursor: null })),
    );
  });

  it('renders an Extract button when rows are non-empty', async () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /extract/i })).toBeInTheDocument(),
    );
  });

  it('clicking CSV invokes api.extractEol with format=csv', async () => {
    const spy = vi
      .spyOn(api, 'extractEol')
      .mockResolvedValue({ truncated: false, filename: null });
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    const btn = await screen.findByRole('button', { name: /extract/i });
    fireEvent.click(btn);
    fireEvent.click(screen.getByRole('menuitem', { name: /csv/i }));
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith(expect.objectContaining({ format: 'csv' })),
    );
    spy.mockRestore();
  });
});
