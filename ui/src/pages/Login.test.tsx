import { describe, expect, it } from 'vitest';
import { http, HttpResponse } from 'msw';
import { screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import Login from './Login';
import { renderWithRouter } from '../test/render';
import { server } from '../test/server';

describe('Login', () => {
  it('renders username and password inputs', () => {
    renderWithRouter(<Login />, { initialPath: '/login' });
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
  });

  it('renders the Sign in button', () => {
    renderWithRouter(<Login />, { initialPath: '/login' });
    expect(screen.getByRole('button', { name: /sign in/i })).toBeInTheDocument();
  });

  it('shows a validation error when fields are empty and submitted', async () => {
    const user = userEvent.setup();
    renderWithRouter(<Login />, { initialPath: '/login' });
    await user.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() =>
      expect(screen.getByText(/username and password required/i)).toBeInTheDocument(),
    );
  });

  it('shows an error when /v1/auth/login returns 401', async () => {
    server.use(
      http.post('/v1/auth/login', () =>
        HttpResponse.json({ detail: 'invalid credentials' }, { status: 401 }),
      ),
    );
    const user = userEvent.setup();
    renderWithRouter(<Login />, { initialPath: '/login' });
    await user.type(screen.getByLabelText(/username/i), 'alice');
    await user.type(screen.getByLabelText(/password/i), 'wrong');
    await user.click(screen.getByRole('button', { name: /sign in/i }));
    await waitFor(() =>
      expect(screen.getByText(/invalid credentials/i)).toBeInTheDocument(),
    );
  });
});
