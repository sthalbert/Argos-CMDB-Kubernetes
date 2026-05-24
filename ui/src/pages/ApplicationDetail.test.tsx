import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import ApplicationDetail from './ApplicationDetail';
import { server } from '../test/server';
import { MeProvider } from '../me';
import {
  fixtureApplication,
  fixtureMe,
  fixtureWorkloadMember,
  fixtureVirtualMachineMember,
  paged,
} from '../test/fixtures';
import type { Me, Role } from '../api';

// renderDetail mounts the page at /applications/:id with a MeProvider so
// role gating is exercised. Default role is admin (full edit affordances).
function renderDetail(role: Role = 'admin', el: ReactElement = <ApplicationDetail />) {
  const me: Me = { ...fixtureMe, role, scopes: ['read', 'write'] };
  return render(
    <MemoryRouter initialEntries={[`/applications/${fixtureApplication.id}`]}>
      <MeProvider value={me}>
        <Routes>
          <Route path="/applications/:id" element={el} />
        </Routes>
      </MeProvider>
    </MemoryRouter>,
  );
}

describe('ApplicationDetail', () => {
  it('renders header + cards in read mode', async () => {
    renderDetail();
    // Header shows the display name.
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: fixtureApplication.display_name })).toBeInTheDocument(),
    );
    // Cards present.
    expect(screen.getByRole('heading', { name: /Ownership & context/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /Classification \(DICT\)/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /^Members$/i })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: /End-of-life summary/i })).toBeInTheDocument();
    // Default test handler returns no EOL rows → empty state copy.
    expect(
      screen.getByText(/No end-of-life signal for this application's members yet/i),
    ).toBeInTheDocument();
  });

  it('hides Edit buttons for a viewer', async () => {
    renderDetail('viewer');
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: fixtureApplication.display_name })).toBeInTheDocument(),
    );
    expect(screen.queryByRole('button', { name: /^Edit$/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /\+ Add member/i })).toBeNull();
  });

  it('shows Edit buttons for an editor', async () => {
    renderDetail('editor');
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: fixtureApplication.display_name })).toBeInTheDocument(),
    );
    // Header + Ownership + DICT each carry an Edit button.
    expect(screen.getAllByRole('button', { name: /^Edit$/i }).length).toBeGreaterThanOrEqual(3);
  });

  it('PATCHes the header on save', async () => {
    const patched = vi.fn();
    server.use(
      http.patch('/v1/applications/:id', async ({ request }) => {
        patched(await request.json());
        return HttpResponse.json(fixtureApplication);
      }),
    );
    const user = userEvent.setup();
    renderDetail('editor');
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: fixtureApplication.display_name })).toBeInTheDocument(),
    );
    // The header card's Edit button is the first one.
    await user.click(screen.getAllByRole('button', { name: /^Edit$/i })[0]);
    const input = screen.getByLabelText('Display name');
    await user.clear(input);
    await user.type(input, 'Renamed app');
    await user.click(screen.getByRole('button', { name: /^Save$/i }));
    await waitFor(() =>
      expect(patched).toHaveBeenCalledWith(expect.objectContaining({ display_name: 'Renamed app' })),
    );
  });

  it('validates DICT axis range (rejects > 4)', async () => {
    const user = userEvent.setup();
    renderDetail('editor');
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /Classification \(DICT\)/i })).toBeInTheDocument(),
    );
    // The DICT card Edit button is the last of the three.
    const edits = screen.getAllByRole('button', { name: /^Edit$/i });
    await user.click(edits[edits.length - 1]);
    const dispo = screen.getByLabelText('Disponibilité');
    await user.clear(dispo);
    await user.type(dispo, '7');
    await user.click(screen.getByRole('button', { name: /^Save$/i }));
    await waitFor(() =>
      expect(screen.getByText(/must be an integer between 0 and 4/i)).toBeInTheDocument(),
    );
  });

  it('PATCHes DICT axes + sec_notes on save', async () => {
    const patched = vi.fn();
    server.use(
      http.patch('/v1/applications/:id', async ({ request }) => {
        patched(await request.json());
        return HttpResponse.json(fixtureApplication);
      }),
    );
    const user = userEvent.setup();
    renderDetail('editor');
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /Classification \(DICT\)/i })).toBeInTheDocument(),
    );
    const edits = screen.getAllByRole('button', { name: /^Edit$/i });
    await user.click(edits[edits.length - 1]);
    const dispo = screen.getByLabelText('Disponibilité');
    await user.clear(dispo);
    await user.type(dispo, '4');
    await user.click(screen.getByRole('button', { name: /^Save$/i }));
    await waitFor(() =>
      expect(patched).toHaveBeenCalledWith(expect.objectContaining({ sec_disponibilite: 4 })),
    );
  });

  it('renders member sub-tables from listApplicationMembers', async () => {
    server.use(
      http.get('/v1/applications/:id/members', () =>
        HttpResponse.json(paged([fixtureWorkloadMember, fixtureVirtualMachineMember])),
      ),
    );
    renderDetail();
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /^Members$/i })).toBeInTheDocument(),
    );
    const summaryWithText = (text: string) =>
      (_content: string, el: Element | null) =>
        el?.tagName === 'SUMMARY' && el.textContent?.replace(/\s+/g, ' ').trim() === text;
    expect(screen.getByText(summaryWithText('Workloads (1)'))).toBeInTheDocument();
    expect(screen.getByText(summaryWithText('Virtual Machines (1)'))).toBeInTheDocument();
    expect(screen.getByText(summaryWithText('VM applications (0)'))).toBeInTheDocument();
    expect(screen.getByRole('link', { name: fixtureWorkloadMember.name })).toBeInTheDocument();
  });

  it('Unlink on a workload row PATCHes the workload with application_id: null', async () => {
    const wlPatch = vi.fn();
    server.use(
      http.get('/v1/applications/:id/members', () =>
        HttpResponse.json(paged([fixtureWorkloadMember])),
      ),
      http.patch('/v1/workloads/:id', async ({ request }) => {
        wlPatch(await request.json());
        return HttpResponse.json({});
      }),
    );
    const user = userEvent.setup();
    renderDetail('editor');
    await waitFor(() =>
      expect(screen.getByRole('link', { name: fixtureWorkloadMember.name })).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /^Unlink$/i }));
    await waitFor(() => expect(wlPatch).toHaveBeenCalledWith({ application_id: null }));
  });

  it('renders EOL rows from applicationEOL', async () => {
    server.use(
      http.get('/v1/applications/:id/eol', () =>
        HttpResponse.json({
          items: [
            {
              product: 'vault',
              cycle: '1.13',
              eol_status: 'eol',
              latest_available: '1.18.2',
              sources: [{ kind: 'virtual_machine', id: 'v1', name: 'bastion-eu' }],
            },
          ],
        }),
      ),
    );
    renderDetail();
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /End-of-life summary/i })).toBeInTheDocument(),
    );
    expect(screen.getByText('vault')).toBeInTheDocument();
    expect(screen.getByText('1.18.2')).toBeInTheDocument();
    expect(screen.getByText('End of Life')).toBeInTheDocument();
    expect(screen.getByText('bastion-eu')).toBeInTheDocument();
  });

  it('shows the empty state when applicationEOL returns no rows', async () => {
    server.use(http.get('/v1/applications/:id/eol', () => HttpResponse.json({ items: [] })));
    renderDetail();
    await waitFor(() =>
      expect(screen.getByRole('heading', { name: /End-of-life summary/i })).toBeInTheDocument(),
    );
    expect(
      screen.getByText(/No end-of-life signal for this application's members yet/i),
    ).toBeInTheDocument();
  });

  it('renders one chip per source on a merged EOL row', async () => {
    server.use(
      http.get('/v1/applications/:id/eol', () =>
        HttpResponse.json({
          items: [
            {
              product: 'vault',
              cycle: '1.13',
              eol_status: 'eol',
              latest_available: '1.18.2',
              sources: [
                { kind: 'virtual_machine', id: 'v1', name: 'bastion-eu' },
                { kind: 'workload', id: 'w1', name: 'kube-prod/vault' },
              ],
            },
          ],
        }),
      ),
    );
    renderDetail();
    await waitFor(() =>
      expect(screen.getByText('bastion-eu')).toBeInTheDocument(),
    );
    expect(screen.getByText('kube-prod/vault')).toBeInTheDocument();
  });

  it('renders a Not found message on 404', async () => {
    server.use(
      http.get('/v1/applications/:id', () =>
        HttpResponse.json({ title: 'Not Found', detail: 'application not found' }, { status: 404 }),
      ),
    );
    renderDetail();
    await waitFor(() =>
      expect(screen.getByText(/Application not found/i)).toBeInTheDocument(),
    );
    expect(screen.getByRole('link', { name: /Back to the list/i })).toBeInTheDocument();
  });
});
