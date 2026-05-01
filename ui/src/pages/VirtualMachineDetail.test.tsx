import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import VirtualMachineDetail from './VirtualMachineDetail';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureVirtualMachine } from '../test/fixtures';

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
});
