import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, render, renderHook, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { type ReactNode, useState } from 'react';
import { ApiError } from './api';
import { useDebouncedValue, useResource, useResources } from './hooks';

afterEach(() => vi.useRealTimers());

function withRouter(initialPath = '/'): (props: { children: ReactNode }) => React.ReactElement {
  return ({ children }) => (
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route path="*" element={<>{children}</>} />
        <Route path="/login" element={<span data-testid="login-mark">login</span>} />
      </Routes>
    </MemoryRouter>
  );
}

describe('useResource', () => {
  it('transitions loading -> ready', async () => {
    const { result } = renderHook(
      () => useResource(() => Promise.resolve({ ok: true }), []),
      { wrapper: withRouter() },
    );
    expect(result.current.status).toBe('loading');
    await waitFor(() => expect(result.current.status).toBe('ready'));
    if (result.current.status === 'ready') {
      expect(result.current.data).toEqual({ ok: true });
    }
  });

  it('surfaces errors as { status: error, error: message }', async () => {
    const { result } = renderHook(
      () => useResource(() => Promise.reject(new Error('boom')), []),
      { wrapper: withRouter() },
    );
    await waitFor(() => expect(result.current.status).toBe('error'));
    if (result.current.status === 'error') {
      expect(result.current.error).toBe('boom');
    }
  });

  it('redirects to /login on 401 instead of error state', async () => {
    function Probe() {
      useResource(() => Promise.reject(new ApiError(401, 'expired')), []);
      const loc = useLocation();
      return <span data-testid="path">{loc.pathname}</span>;
    }
    render(
      <MemoryRouter initialEntries={['/clusters']}>
        <Probe />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByTestId('path').textContent).toBe('/login'),
    );
  });

  it('re-runs when deps change', async () => {
    const fetcher = vi.fn(async (n: number) => n * 2);
    const { result, rerender } = renderHook(
      ({ n }: { n: number }) => useResource(() => fetcher(n), [n]),
      { wrapper: withRouter(), initialProps: { n: 1 } },
    );
    await waitFor(() => expect(result.current.status).toBe('ready'));
    rerender({ n: 5 });
    await waitFor(() => {
      if (result.current.status === 'ready') {
        expect(result.current.data).toBe(10);
      }
    });
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it('does not call setState after unmount (no React act warnings)', async () => {
    let resolve!: (v: number) => void;
    const fetcher = () => new Promise<number>((r) => { resolve = r; });
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    const { unmount } = renderHook(() => useResource(fetcher, []), {
      wrapper: withRouter(),
    });
    unmount();
    resolve(42);
    // Allow microtask queue to drain
    await new Promise((r) => setTimeout(r, 10));
    expect(errSpy).not.toHaveBeenCalled();
    errSpy.mockRestore();
  });
});

describe('useResources', () => {
  it('resolves once all fetchers resolve', async () => {
    const { result } = renderHook(
      () =>
        useResources(
          [() => Promise.resolve('a'), () => Promise.resolve(2)] as const,
          [],
        ),
      { wrapper: withRouter() },
    );
    await waitFor(() => expect(result.current.status).toBe('ready'));
    if (result.current.status === 'ready') {
      expect(result.current.data).toEqual(['a', 2]);
    }
  });

  it('short-circuits to /login on 401 from any fetcher', async () => {
    function Probe() {
      useResources(
        [() => Promise.resolve(1), () => Promise.reject(new ApiError(401, ''))] as const,
        [],
      );
      const loc = useLocation();
      return <span data-testid="path">{loc.pathname}</span>;
    }
    render(
      <MemoryRouter initialEntries={['/x']}>
        <Probe />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByTestId('path').textContent).toBe('/login'),
    );
  });

  it('surfaces errors other than 401', async () => {
    const { result } = renderHook(
      () =>
        useResources(
          [() => Promise.resolve(1), () => Promise.reject(new Error('nope'))] as const,
          [],
        ),
      { wrapper: withRouter() },
    );
    await waitFor(() => expect(result.current.status).toBe('error'));
  });
});

describe('useDebouncedValue', () => {
  it('propagates value only after delay', async () => {
    vi.useFakeTimers();
    function Wrapper() {
      const [v, setV] = useState('a');
      const debounced = useDebouncedValue(v, 100);
      return (
        <>
          <span data-testid="value">{debounced}</span>
          <button onClick={() => setV('b')}>change</button>
        </>
      );
    }
    render(<Wrapper />);
    expect(screen.getByTestId('value').textContent).toBe('a');
    act(() => screen.getByText('change').click());
    expect(screen.getByTestId('value').textContent).toBe('a');
    await act(async () => {
      vi.advanceTimersByTime(100);
    });
    expect(screen.getByTestId('value').textContent).toBe('b');
  });
});
