import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import Applications from './Applications';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureApplication, paged } from '../test/fixtures';
import type { Application } from '../api';

// Three apps across two blocks: two in "finance", one unblocked.
const billing: Application = {
  ...fixtureApplication,
  id: 'app-1',
  name: 'billing',
  display_name: undefined,
};
const ledger: Application = {
  ...fixtureApplication,
  id: 'app-2',
  name: 'ledger',
  display_name: 'Ledger',
};
const wiki: Application = {
  ...fixtureApplication,
  id: 'app-3',
  name: 'wiki',
  display_name: 'Wiki',
  application_block_id: null,
  application_block_name: null,
  sec_disponibilite: 1,
  sec_integrite: 4,
  sec_confidentialite: 2,
  sec_tracabilite: 0,
  owner: 'it-team',
  criticality: 'low',
  member_counts: { workloads: 5, virtual_machines: 2, vm_applications: 3 },
};

describe('Applications list', () => {
  it('renders block headers and rows', async () => {
    server.use(
      http.get('/v1/applications', () => HttpResponse.json(paged([billing, ledger, wiki]))),
    );
    renderWithRouter(<Applications />, { initialPath: '/applications' });

    await waitFor(() =>
      expect(screen.getByRole('link', { name: /billing/i })).toBeInTheDocument(),
    );
    // Two block group headers (the <summary> rows): "finance (2)" and
    // "Unblocked (1)".
    const summaryWithText = (text: string) =>
      (_content: string, el: Element | null) =>
        el?.tagName === 'SUMMARY' && el.textContent?.replace(/\s+/g, ' ').trim() === text;
    expect(screen.getByText(summaryWithText('finance (2)'))).toBeInTheDocument();
    expect(screen.getByText(summaryWithText('Unblocked (1)'))).toBeInTheDocument();
    // Three rows linking to detail.
    expect(screen.getByRole('link', { name: /^billing$/ })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /^Ledger$/ })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /^Wiki$/ })).toBeInTheDocument();
  });

  it('shows the correct DICT max badge', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged([wiki]))));
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    // wiki's max axis is I=4.
    await waitFor(() => expect(screen.getByText('DICT max: 4')).toBeInTheDocument());
  });

  it('shows the member-count summary', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged([wiki]))));
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    await waitFor(() =>
      expect(screen.getByText('5 workloads · 2 VMs · 3 VM-apps')).toBeInTheDocument(),
    );
  });

  it('debounced name filter updates the query payload', async () => {
    const seen: string[] = [];
    server.use(
      http.get('/v1/applications', ({ request }) => {
        seen.push(new URL(request.url).searchParams.get('name') ?? '');
        return HttpResponse.json(paged([billing]));
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    await waitFor(() => expect(screen.getByRole('link', { name: /billing/ })).toBeInTheDocument());

    await user.type(screen.getByPlaceholderText('prefix or substring'), 'bill');
    await waitFor(() => expect(seen).toContain('bill'));
  });

  it('"Load more" calls the fetcher with the previous cursor', async () => {
    const cursors: (string | null)[] = [];
    let call = 0;
    server.use(
      http.get('/v1/applications', ({ request }) => {
        cursors.push(new URL(request.url).searchParams.get('cursor'));
        call += 1;
        if (call === 1) {
          return HttpResponse.json({ items: [billing], next_cursor: 'CURSOR123' });
        }
        return HttpResponse.json({ items: [ledger], next_cursor: null });
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    await waitFor(() => expect(screen.getByRole('link', { name: /billing/ })).toBeInTheDocument());

    await user.click(screen.getByRole('button', { name: /load more/i }));
    await waitFor(() => expect(cursors).toContain('CURSOR123'));
    await waitFor(() => expect(screen.getByRole('link', { name: /^Ledger$/ })).toBeInTheDocument());
  });

  it('renders the empty state', async () => {
    server.use(http.get('/v1/applications', () => HttpResponse.json(paged([]))));
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    await waitFor(() =>
      expect(
        screen.getByText(/No applications yet — create one from a workload or VM detail page/i),
      ).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(http.get('/v1/applications', () => new HttpResponse(null, { status: 500 })));
    renderWithRouter(<Applications />, { initialPath: '/applications' });
    await waitFor(() => expect(screen.getByText(/failed to load/i)).toBeInTheDocument());
  });
});
