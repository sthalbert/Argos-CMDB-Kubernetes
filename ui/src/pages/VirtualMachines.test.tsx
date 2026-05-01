import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import VirtualMachines from './VirtualMachines';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';
import { fixtureVirtualMachine } from '../test/fixtures';

describe('VirtualMachines list', () => {
  it('renders without crashing', () => {
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    expect(screen.getByRole('heading', { name: /virtual machines/i })).toBeInTheDocument();
  });

  it('renders the VM name once data arrives', async () => {
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(fixtureVirtualMachine.name)).toBeInTheDocument(),
    );
  });

  it('renders error state on 500', async () => {
    server.use(
      http.get('/v1/virtual-machines', () => new HttpResponse(null, { status: 500 })),
    );
    renderWithRouter(<VirtualMachines />, { initialPath: '/virtual-machines' });
    await waitFor(() =>
      expect(screen.getByText(/failed to load/i)).toBeInTheDocument(),
    );
  });
});
