import { describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ApplicationCard } from './ApplicationCard';
import { renderWithRouter } from '../../test/render';
import { server } from '../../test/server';
import { fixtureApplication, paged } from '../../test/fixtures';

describe('ApplicationCard', () => {
  it('renders the linked application name as a link to the detail page', () => {
    renderWithRouter(
      <ApplicationCard
        linkedId={fixtureApplication.id}
        linkedName={fixtureApplication.name}
        onLink={vi.fn()}
        editable
      />,
    );
    const link = screen.getByRole('link', { name: fixtureApplication.name });
    expect(link).toBeInTheDocument();
    expect(link.getAttribute('href')).toBe(`/applications/${fixtureApplication.id}`);
  });

  it('renders "Not linked" when no application is linked', () => {
    renderWithRouter(
      <ApplicationCard
        linkedId={null}
        linkedName={null}
        onLink={vi.fn()}
        editable
      />,
    );
    expect(screen.getByText('Not linked')).toBeInTheDocument();
  });

  it('hides the edit button when editable is false', () => {
    renderWithRouter(
      <ApplicationCard
        linkedId={null}
        linkedName={null}
        onLink={vi.fn()}
        editable={false}
      />,
    );
    expect(screen.queryByRole('button', { name: /link/i })).toBeNull();
    expect(screen.queryByRole('button', { name: /change/i })).toBeNull();
  });

  it('honours the optional label prop', () => {
    renderWithRouter(
      <ApplicationCard
        linkedId={null}
        linkedName={null}
        onLink={vi.fn()}
        editable
        label="Member application"
      />,
    );
    expect(screen.getByText('Member application')).toBeInTheDocument();
  });

  it('shows search results when typing into the input and calls onLink when picked', async () => {
    const user = userEvent.setup();
    const onLink = vi.fn().mockResolvedValue(undefined);
    renderWithRouter(
      <ApplicationCard
        linkedId={null}
        linkedName={null}
        onLink={onLink}
        editable
      />,
    );
    await user.click(screen.getByRole('button', { name: /link/i }));
    const input = screen.getByLabelText('Search applications');
    await user.type(input, 'bill');
    // Default MSW handler returns [fixtureApplication], so the button labelled
    // 'billing' should appear in the result list.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /^billing/ })).toBeInTheDocument(),
    );
    await user.click(screen.getByRole('button', { name: /^billing/ }));
    await waitFor(() => expect(onLink).toHaveBeenCalledWith(fixtureApplication.id));
  });

  it('calls onLink(null) when Unlink is clicked', async () => {
    const user = userEvent.setup();
    const onLink = vi.fn().mockResolvedValue(undefined);
    renderWithRouter(
      <ApplicationCard
        linkedId={fixtureApplication.id}
        linkedName={fixtureApplication.name}
        onLink={onLink}
        editable
      />,
    );
    await user.click(screen.getByRole('button', { name: /change/i }));
    await user.click(screen.getByRole('button', { name: /^unlink$/i }));
    await waitFor(() => expect(onLink).toHaveBeenCalledWith(null));
  });

  it('create-and-link flow calls createApplication then onLink with the new id', async () => {
    const newApp = { ...fixtureApplication, id: 'new-app-id', name: 'fresh-app' };
    server.use(
      http.post('/v1/applications', () => HttpResponse.json(newApp)),
      // Empty search results — the operator opens the create flow without picking.
      http.get('/v1/applications', () => HttpResponse.json(paged([]))),
    );
    const user = userEvent.setup();
    const onLink = vi.fn().mockResolvedValue(undefined);
    renderWithRouter(
      <ApplicationCard
        linkedId={null}
        linkedName={null}
        onLink={onLink}
        editable
      />,
    );
    await user.click(screen.getByRole('button', { name: /link/i }));
    await user.click(screen.getByRole('button', { name: /\+ Create new application/i }));
    const dialog = await screen.findByRole('dialog', { name: /Create application/i });
    expect(dialog).toBeInTheDocument();
    await user.type(screen.getByLabelText('New application name'), 'fresh-app');
    await user.click(screen.getByRole('button', { name: /^Create \+ Link$/i }));
    await waitFor(() => expect(onLink).toHaveBeenCalledWith(newApp.id));
  });

  it('surfaces an inline error when onLink rejects', async () => {
    const user = userEvent.setup();
    const onLink = vi.fn().mockRejectedValue(new Error('boom'));
    renderWithRouter(
      <ApplicationCard
        linkedId={fixtureApplication.id}
        linkedName={fixtureApplication.name}
        onLink={onLink}
        editable
      />,
    );
    await user.click(screen.getByRole('button', { name: /change/i }));
    await user.click(screen.getByRole('button', { name: /^unlink$/i }));
    await waitFor(() => expect(screen.getByText(/boom/)).toBeInTheDocument());
  });
});
