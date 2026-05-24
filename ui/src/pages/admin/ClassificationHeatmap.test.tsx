import { afterEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ClassificationHeatmap from './ClassificationHeatmap';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { fixtureApplication, paged } from '../../test/fixtures';
import type { Application } from '../../api';

// Three apps with varying DICT. `null-axis` deliberately leaves one axis
// unset to exercise the neutral em-dash guard.
const apps: Application[] = [
  {
    ...fixtureApplication,
    id: 'app-high',
    name: 'high-app',
    display_name: 'High app',
    sec_disponibilite: 4,
    sec_integrite: 3,
    sec_confidentialite: 2,
    sec_tracabilite: 1,
    sec_notes: 'handles regulated PII',
  },
  {
    ...fixtureApplication,
    id: 'app-null',
    name: 'null-app',
    display_name: 'Null app',
    application_block_name: 'platform',
    sec_disponibilite: 3,
    sec_integrite: null,
    sec_confidentialite: 3,
    sec_tracabilite: 0,
    sec_notes: null,
  },
  {
    ...fixtureApplication,
    id: 'app-mid',
    name: 'mid-app',
    display_name: 'Mid app',
    sec_disponibilite: 3,
    sec_integrite: 3,
    sec_confidentialite: 3,
    sec_tracabilite: 3,
    sec_notes: null,
  },
];

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ClassificationHeatmap', () => {
  it('renders a row per application returned', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged(apps))));
    renderWithRouter(<ClassificationHeatmap />, { initialPath: '/admin/classification' });
    await waitFor(() => expect(screen.getByText('High app')).toBeInTheDocument());
    expect(screen.getByText('Null app')).toBeInTheDocument();
    expect(screen.getByText('Mid app')).toBeInTheDocument();
  });

  it('defaults to dict_min=3 and refetches when the threshold changes', async () => {
    const seen: (string | null)[] = [];
    server.use(
      http.get('/v1/applications', ({ request }) => {
        seen.push(new URL(request.url).searchParams.get('dict_min'));
        return HttpResponse.json(paged(apps));
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<ClassificationHeatmap />, { initialPath: '/admin/classification' });
    await waitFor(() => expect(screen.getByText('High app')).toBeInTheDocument());
    expect(seen[0]).toBe('3');

    await user.selectOptions(
      screen.getByLabelText('Minimum classification level'),
      '4',
    );
    await waitFor(() => expect(seen).toContain('4'));
  });

  it('heat-colours DICT cells per value and guards null axes', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged([apps[1]]))));
    const { container } = renderWithRouter(<ClassificationHeatmap />, {
      initialPath: '/admin/classification',
    });
    await waitFor(() => expect(screen.getByText('Null app')).toBeInTheDocument());

    const row = screen.getByText('Null app').closest('tr')!;
    // D=3 → heat-3, C=3 → heat-3 (two cells), T=0 → heat-0; I is null → neutral.
    expect(within(row).getAllByText('3', { selector: '.heat-3' })).toHaveLength(2);
    expect(within(row).getByText('0', { selector: '.heat-0' })).toBeTruthy();
    // No heat class is applied to the null axis; it renders an em-dash in a
    // neutral dict-value cell (not heat-null / unstyled).
    expect(within(row).getByText('—', { selector: '.dict-value.muted' })).toBeTruthy();
    expect(container.querySelector('[class*="heat-null"]')).toBeNull();
  });

  it('CSV export hits /v1/applications/extract.csv with dict_min', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged(apps))));
    Object.defineProperty(URL, 'createObjectURL', {
      value: vi.fn(() => 'blob:fake'),
      configurable: true,
    });
    Object.defineProperty(URL, 'revokeObjectURL', { value: vi.fn(), configurable: true });
    // Capture the extract.csv request without short-circuiting the list
    // fetch that hydrates the table (msw serves that one).
    let extractUrl = '';
    server.use(
      http.get('/v1/applications/extract.csv', ({ request }) => {
        extractUrl = request.url;
        return new HttpResponse('csv', { status: 200, headers: { 'Content-Type': 'text/csv' } });
      }),
    );

    const user = userEvent.setup();
    renderWithRouter(<ClassificationHeatmap />, { initialPath: '/admin/classification' });
    await waitFor(() => expect(screen.getByText('High app')).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: /export csv/i }));
    await user.click(screen.getByRole('menuitem', { name: /^csv$/i }));

    await waitFor(() => expect(extractUrl).not.toBe(''));
    expect(extractUrl).toContain('/v1/applications/extract.csv');
    expect(extractUrl).toContain('dict_min=3');
  });

  it('shows the empty state when no apps are returned', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged([]))));
    renderWithRouter(<ClassificationHeatmap />, { initialPath: '/admin/classification' });
    await waitFor(() =>
      expect(
        screen.getByText(/No applications at or above classification level 3/i),
      ).toBeInTheDocument(),
    );
  });
});
