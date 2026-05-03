import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithRouter } from './test/render';
import App from './App';
import * as api from './api';
import { server } from './test/server';
import { http, HttpResponse } from 'msw';
import type { Me } from './api';

const adminMe: Me = { id: 'u1', username: 'alice', role: 'admin', kind: 'user', must_change_password: false, scopes: ['read', 'write', 'delete', 'admin', 'audit'] };
const auditorMe: Me = { id: 'u2', username: 'bob', role: 'auditor', kind: 'user', must_change_password: false, scopes: ['read', 'audit'] };
const viewerMe: Me = { id: 'u3', username: 'carol', role: 'viewer', kind: 'user', must_change_password: false, scopes: ['read'] };

function mockMe(me: Me) {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json(me)));
}

beforeEach(() => { localStorage.clear(); });
afterEach(() => { vi.restoreAllMocks(); });

describe('Chrome (sidebar)', () => {
  it('renders every primary nav link for an admin', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.getByRole('link', { name: 'Clusters' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Workloads' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Nodes' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Virtual Machines' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Lifecycle' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Search' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Audit' })).toBeTruthy();
  });

  it('hides the Admin group for viewer', async () => {
    mockMe(viewerMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.queryByRole('link', { name: 'Audit' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull();
  });

  it('shows only Audit under Admin for auditor', async () => {
    mockMe(auditorMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Audit' });
    expect(screen.getByRole('link', { name: 'Audit' })).toBeTruthy();
    expect(screen.queryByRole('link', { name: 'Users' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Settings' })).toBeNull();
  });

  it('marks Clusters active on /clusters/:id', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters/c-123' });
    const link = await screen.findByRole('link', { name: 'Clusters' });
    expect(link.classList.contains('active')).toBe(true);
  });

  it('renders every overflow route directly in the sidebar (no More dropdown)', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.getByRole('link', { name: 'Namespaces' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Pods' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Services' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Ingresses' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'PVs' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'PVCs' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /more/i })).toBeNull();
  });

  it('groups admin tabs in the sidebar Admin section for admin role', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.getByRole('link', { name: 'Users' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Tokens' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Sessions' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Cloud accounts' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Settings' })).toBeTruthy();
  });

  it('opens user menu and signs out', async () => {
    mockMe(adminMe);
    const logoutSpy = vi.spyOn(api, 'logout').mockResolvedValue(undefined as unknown as void);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    const userBtn = await screen.findByRole('button', { name: /alice/i });
    fireEvent.click(userBtn);
    fireEvent.click(screen.getByRole('button', { name: /sign out/i }));
    await waitFor(() => expect(logoutSpy).toHaveBeenCalled());
  });

  it('user menu sets body data-accent when accent radio clicked', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    const userBtn = await screen.findByRole('button', { name: /alice/i });
    fireEvent.click(userBtn);
    fireEvent.click(screen.getByLabelText('amber'));
    expect(document.body.dataset.accent).toBe('amber');
  });
});
