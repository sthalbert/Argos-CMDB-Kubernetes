import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VirtualMachineDetail from './VirtualMachineDetail';
import { renderWithRouter } from '../test/render';
import { MeProvider } from '../me';
import type { Me } from '../api';
import { server } from '../test/server';
import { fixtureApplication, fixtureMe, fixtureVirtualMachine } from '../test/fixtures';

// renderVM wraps the page in a MeProvider (defaults to the admin fixture so
// the role-gated ApplicationCard affordances are exercised) and a router.
function renderVM(me: Me | null = fixtureMe) {
  return render(
    <MeProvider value={me}>
      <MemoryRouter initialEntries={[`/virtual-machines/${fixtureVirtualMachine.id}`]}>
        <Routes>
          <Route path="/virtual-machines/:id" element={<VirtualMachineDetail />} />
        </Routes>
      </MemoryRouter>
    </MeProvider>,
  );
}

describe('VirtualMachineDetail', () => {
  it('renders the VM name on ready', async () => {
    renderWithRouter(<VirtualMachineDetail />, {
      initialPath: `/virtual-machines/${fixtureVirtualMachine.id}`,
      routePath: '/virtual-machines/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(fixtureVirtualMachine.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/virtual-machines/:id', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<VirtualMachineDetail />, {
      initialPath: `/virtual-machines/${fixtureVirtualMachine.id}`,
      routePath: '/virtual-machines/:id',
    });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });

  it('renders the row-level ApplicationCard', async () => {
    renderVM();
    await waitFor(() =>
      expect(screen.getByTestId('application-card')).toBeInTheDocument(),
    );
  });

  it('resolves the linked application name via getApplication', async () => {
    server.use(
      http.get('/v1/virtual-machines/:id', () =>
        HttpResponse.json({
          ...fixtureVirtualMachine,
          application_id: fixtureApplication.id,
        }),
      ),
    );
    renderVM();
    const card = await screen.findByTestId('application-card');
    // getApplication returns fixtureApplication → display_name wins.
    await waitFor(() =>
      expect(
        within(card).getByText(fixtureApplication.display_name!),
      ).toBeInTheDocument(),
    );
  });

  it('links the VM via the patch fetcher with application_id', async () => {
    let patchedBody: Record<string, unknown> | null = null;
    server.use(
      http.patch('/v1/virtual-machines/:id', async ({ request }) => {
        patchedBody = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(fixtureVirtualMachine);
      }),
    );
    renderVM();
    const card = await screen.findByTestId('application-card');
    await userEvent.click(within(card).getByRole('button', { name: /link/i }));
    await userEvent.type(
      within(card).getByLabelText('Search applications'),
      'bill',
    );
    const pick = await within(card).findByRole('button', { name: /billing/i });
    await userEvent.click(pick);
    await waitFor(() =>
      expect(patchedBody).toEqual({ application_id: fixtureApplication.id }),
    );
  });

  it('persists application_id on a software row through the applications PATCH', async () => {
    let patchedBody: { applications?: Array<Record<string, unknown>> } | null = null;
    server.use(
      http.patch('/v1/virtual-machines/:id', async ({ request }) => {
        patchedBody = (await request.json()) as typeof patchedBody;
        return HttpResponse.json(fixtureVirtualMachine);
      }),
    );
    renderVM();
    // Enter the VM applications edit form.
    await screen.findByText('Applications');
    const editButtons = screen.getAllByRole('button', { name: 'Edit' });
    await userEvent.click(editButtons[editButtons.length - 1]);
    // The per-row picker is labelled "Search applications"; link the first row.
    const rowPicker = await screen.findByLabelText('Search applications');
    await userEvent.type(rowPicker, 'bill');
    const pick = await screen.findByRole('button', { name: /^billing$/i });
    await userEvent.click(pick);
    await userEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(patchedBody).not.toBeNull());
    expect(patchedBody!.applications?.[0].application_id).toBe(fixtureApplication.id);
  });
});
