import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import ChangePassword from './ChangePassword';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

describe('ChangePassword', () => {
  it('renders the rotation form when not forced', () => {
    renderWithRouter(<ChangePassword forced={false} />, { initialPath: '/change-password' });
    expect(screen.getByLabelText(/current password/i)).toBeInTheDocument();
    expect(screen.getAllByLabelText(/new password/i).length).toBeGreaterThanOrEqual(1);
  });

  it('renders "Rotate your password" heading when forced=true', () => {
    renderWithRouter(<ChangePassword forced={true} />, { initialPath: '/change-password' });
    expect(screen.getByRole('heading', { name: /rotate your password/i })).toBeInTheDocument();
  });

  it('renders "Change password" heading when not forced', () => {
    renderWithRouter(<ChangePassword forced={false} />, { initialPath: '/change-password' });
    expect(screen.getByRole('heading', { name: /change password/i })).toBeInTheDocument();
  });

  it('shows a client-side error when new password is too short', async () => {
    const user = userEvent.setup();
    renderWithRouter(<ChangePassword />, { initialPath: '/change-password' });
    await user.type(screen.getByLabelText(/current password/i), 'oldpassword');
    // Use "New password" label (first match — the "new password" field, not confirm)
    const newFields = screen.getAllByLabelText(/new password/i);
    await user.type(newFields[0], 'short');
    await user.type(screen.getByLabelText(/confirm new password/i), 'short');
    await user.click(screen.getByRole('button', { name: /change password/i }));
    await waitFor(() =>
      expect(screen.getByText(/at least 12 characters/i)).toBeInTheDocument(),
    );
  });

  it('shows a client-side error when passwords do not match', async () => {
    const user = userEvent.setup();
    renderWithRouter(<ChangePassword />, { initialPath: '/change-password' });
    await user.type(screen.getByLabelText(/current password/i), 'oldpassword123');
    const newFields = screen.getAllByLabelText(/new password/i);
    await user.type(newFields[0], 'newpassword12345');
    await user.type(screen.getByLabelText(/confirm new password/i), 'differentpass12');
    await user.click(screen.getByRole('button', { name: /change password/i }));
    await waitFor(() =>
      expect(screen.getByText(/confirmation does not match/i)).toBeInTheDocument(),
    );
  });

  it('shows a server error when /v1/auth/change-password returns 400', async () => {
    server.use(
      http.post('/v1/auth/change-password', () =>
        HttpResponse.json({ detail: 'current password is wrong' }, { status: 400 }),
      ),
    );
    const user = userEvent.setup();
    renderWithRouter(<ChangePassword />, { initialPath: '/change-password' });
    await user.type(screen.getByLabelText(/current password/i), 'wrongcurrent123');
    const newFields = screen.getAllByLabelText(/new password/i);
    await user.type(newFields[0], 'newpassword12345');
    await user.type(screen.getByLabelText(/confirm new password/i), 'newpassword12345');
    await user.click(screen.getByRole('button', { name: /change password/i }));
    await waitFor(() =>
      expect(screen.getByText(/current password is wrong/i)).toBeInTheDocument(),
    );
  });
});
