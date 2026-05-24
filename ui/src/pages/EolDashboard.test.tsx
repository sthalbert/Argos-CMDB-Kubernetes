import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as api from '../api';
import EolDashboard from './EolDashboard';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureApplication } from '../test/fixtures';

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

const vmEolAnnotation = JSON.stringify({
  product: 'vault',
  cycle: '1.15',
  eol_status: 'approaching_eol',
  eol: '2025-12-01',
});

// A VM EOL row linked to fixtureApplication, plus an unlinked cluster row.
const linkedVm = {
  ...({} as Record<string, unknown>),
  id: 'vm-linked',
  name: 'vault-01',
  display_name: 'vault-01',
  application_id: fixtureApplication.id,
  annotations: { 'longue-vue.io/eol.vault': vmEolAnnotation },
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

describe('EOL Dashboard application column', () => {
  beforeEach(() => {
    server.use(
      http.get('/v1/clusters', () =>
        HttpResponse.json({ items: [clusterWithEol], next_cursor: null }),
      ),
      http.get('/v1/nodes', () => HttpResponse.json({ items: [], next_cursor: null })),
      http.get('/v1/virtual-machines', () =>
        HttpResponse.json({ items: [linkedVm], next_cursor: null }),
      ),
      http.get('/v1/applications', () =>
        HttpResponse.json({ items: [fixtureApplication], next_cursor: null }),
      ),
    );
  });

  it('renders the Application column header', async () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    await waitFor(() =>
      expect(
        screen.getByRole('columnheader', { name: /application/i }),
      ).toBeInTheDocument(),
    );
  });

  it('shows the linked application name on the VM row and a dash on the cluster row', async () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    // The linked VM row shows the application display name as a link.
    const appLink = await screen.findByRole('link', {
      name: fixtureApplication.display_name!,
    });
    expect(appLink).toHaveAttribute('href', `/applications/${fixtureApplication.id}`);
    // The cluster row (product kubernetes) carries no application: no app
    // link in the row, and at least one dash placeholder is present.
    const k8sRow = (await screen.findByText('kubernetes')).closest('tr')!;
    expect(within(k8sRow).queryByRole('link', { name: /billing/i })).toBeNull();
    expect(within(k8sRow).getAllByText('—').length).toBeGreaterThan(0);
  });

  it('narrows the table when the application filter matches', async () => {
    renderWithRouter(<EolDashboard />, { initialPath: '/eol' });
    // Both rows present initially.
    await screen.findByText('vault');
    expect(screen.getByText('kubernetes')).toBeInTheDocument();
    // Filter to the linked application; the cluster row drops out.
    await userEvent.type(
      screen.getByLabelText('Filter by application'),
      fixtureApplication.display_name!,
    );
    await waitFor(() => expect(screen.queryByText('kubernetes')).toBeNull());
    expect(screen.getByText('vault')).toBeInTheDocument();
  });
});
