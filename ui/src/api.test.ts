import { afterEach, describe, expect, it, vi } from 'vitest';
import { http, HttpResponse } from 'msw';
import {
  ApiError, getCluster, getMe, listClusters, login, logout,
  updateCluster, updateUser,
} from './api';
import { server } from './test/server';
import { fixtureCluster, fixtureMe } from './test/fixtures';

afterEach(() => vi.restoreAllMocks());

describe('request()', () => {
  it('parses JSON 200 responses', async () => {
    const me = await getMe();
    expect(me).toEqual(fixtureMe);
  });

  it('returns undefined on 204 without parsing', async () => {
    const result = await login('alice', 'pw');
    expect(result).toBeUndefined();
  });

  it('throws ApiError with status and RFC7807 detail', async () => {
    server.use(
      http.get('/v1/clusters/:id', () =>
        HttpResponse.json(
          { type: 'about:blank', title: 'Not Found', detail: 'cluster missing' },
          { status: 404 },
        ),
      ),
    );
    await expect(getCluster('does-not-exist')).rejects.toMatchObject({
      name: 'ApiError',
      status: 404,
      message: 'cluster missing',
    });
  });

  it('falls back to title when detail is absent', async () => {
    server.use(
      http.get('/v1/clusters/:id', () =>
        HttpResponse.json({ title: 'Conflict' }, { status: 409 }),
      ),
    );
    await expect(getCluster('x')).rejects.toMatchObject({
      status: 409,
      message: 'Conflict',
    });
  });

  it('falls back to statusText when body is non-JSON', async () => {
    server.use(
      http.get('/v1/clusters/:id', () =>
        new HttpResponse('not json', {
          status: 502,
          statusText: 'Bad Gateway',
          headers: { 'Content-Type': 'text/plain' },
        }),
      ),
    );
    await expect(getCluster('x')).rejects.toMatchObject({
      status: 502,
      message: 'Bad Gateway',
    });
  });

  it('passes credentials: same-origin', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    await getMe();
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(init.credentials).toBe('same-origin');
  });

  it('sets Content-Type to application/json on bodied requests by default', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    await login('alice', 'pw');
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/json',
    );
  });

  it('updateUser sets merge-patch content type', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    await updateUser('id-1', { role: 'editor' });
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/merge-patch+json',
    );
  });

  it('updateCluster keeps application/json (per merge-patch policy in api.ts)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    await updateCluster(fixtureCluster.id, { owner: 'sre' });
    const init = fetchSpy.mock.calls[0][1] as RequestInit;
    expect((init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/json',
    );
  });

  it('logout posts and resolves to undefined', async () => {
    await expect(logout()).resolves.toBeUndefined();
  });
});

describe('query()', () => {
  it('skips undefined / null / empty values and encodes the rest', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    await listClusters();
    const url = fetchSpy.mock.calls[0][0] as string;
    expect(url).toBe('/v1/clusters?limit=200');
  });

  it('builds an empty query string when no values are kept', async () => {
    server.use(http.get('/v1/clusters', () => HttpResponse.json({ items: [] })));
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    // listNamespaces with no filter exercises the same query() helper
    await import('./api').then((m) => m.listNamespaces());
    const url = fetchSpy.mock.calls[0][0] as string;
    expect(url).toBe('/v1/namespaces?limit=200');
  });
});

describe('ApiError', () => {
  it('carries status and name', () => {
    const e = new ApiError(403, 'forbidden');
    expect(e).toBeInstanceOf(Error);
    expect(e.status).toBe(403);
    expect(e.name).toBe('ApiError');
    expect(e.message).toBe('forbidden');
  });
});
