import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ApplicationBlocksPage from './ApplicationBlocks';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { fixtureApplicationBlock, paged } from '../../test/fixtures';

describe('ApplicationBlocksPage', () => {
  it('renders the block list', async () => {
    renderWithRouter(<ApplicationBlocksPage />, { initialPath: '/admin/application-blocks' });
    await waitFor(() =>
      expect(screen.getByText(fixtureApplicationBlock.name)).toBeInTheDocument(),
    );
    // Display name + owner + count surface in the row.
    expect(screen.getByText(fixtureApplicationBlock.display_name!)).toBeInTheDocument();
    expect(screen.getByText(fixtureApplicationBlock.owner!)).toBeInTheDocument();
  });

  it('inline create posts and refreshes', async () => {
    const posted = vi.fn();
    server.use(
      http.post('/v1/application-blocks', async ({ request }) => {
        posted(await request.json());
        return HttpResponse.json(fixtureApplicationBlock);
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<ApplicationBlocksPage />, { initialPath: '/admin/application-blocks' });
    await waitFor(() =>
      expect(screen.getByText(fixtureApplicationBlock.name)).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /\+ Add block/i }));
    await user.type(screen.getByPlaceholderText('finance'), 'office-it');
    await user.click(screen.getByRole('button', { name: /^Create$/i }));
    await waitFor(() =>
      expect(posted).toHaveBeenCalledWith(expect.objectContaining({ name: 'office-it' })),
    );
  });

  it('inline edit PATCHes the changed owner', async () => {
    const patched = vi.fn();
    server.use(
      http.patch('/v1/application-blocks/:id', async ({ request }) => {
        patched(await request.json());
        return HttpResponse.json(fixtureApplicationBlock);
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<ApplicationBlocksPage />, { initialPath: '/admin/application-blocks' });
    await waitFor(() =>
      expect(screen.getByText(fixtureApplicationBlock.name)).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /^Edit$/i }));
    const ownerInput = screen.getByLabelText('Owner');
    await user.clear(ownerInput);
    await user.type(ownerInput, 'new-owner');
    await user.click(screen.getByRole('button', { name: /^Save$/i }));
    await waitFor(() =>
      expect(patched).toHaveBeenCalledWith(expect.objectContaining({ owner: 'new-owner' })),
    );
  });

  it('delete confirms then DELETEs', async () => {
    const deleted = vi.fn();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    server.use(
      http.delete('/v1/application-blocks/:id', () => {
        deleted();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const user = userEvent.setup();
    renderWithRouter(<ApplicationBlocksPage />, { initialPath: '/admin/application-blocks' });
    await waitFor(() =>
      expect(screen.getByText(fixtureApplicationBlock.name)).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /^Delete$/i }));
    await waitFor(() => expect(deleted).toHaveBeenCalled());
    vi.restoreAllMocks();
  });

  it('renders the empty state', async () => {
    server.use(http.get('/v1/application-blocks', () => HttpResponse.json(paged([]))));
    renderWithRouter(<ApplicationBlocksPage />, { initialPath: '/admin/application-blocks' });
    await waitFor(() =>
      expect(screen.getByText(/No application blocks configured yet/i)).toBeInTheDocument(),
    );
  });
});
