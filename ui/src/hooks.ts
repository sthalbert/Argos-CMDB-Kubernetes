import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from './api';

// useDebouncedValue delays propagation of a fast-changing value (typed
// search input) until it has been stable for `delayMs`. Used by list
// pages to keep server-side filter requests cheap.
export function useDebouncedValue<T>(value: T, delayMs: number = 300): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(id);
  }, [value, delayMs]);
  return debounced;
}

// AsyncState is a tri-state discriminated union: loading / error / ready.
// Pages switch on it instead of juggling three separate useState hooks.
export type AsyncState<T> =
  | { status: 'loading' }
  | { status: 'error'; error: string }
  | { status: 'ready'; data: T };

// useResource runs a fetcher once per dependency change, surfaces an
// AsyncState, and routes 401s back to /login (the cookie expired or
// the session was revoked server-side). Cookies are browser-managed,
// so we don't need to clear any local state.
export function useResource<T>(fetcher: () => Promise<T>, deps: unknown[]): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ status: 'loading' });
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    setState({ status: 'loading' });
    fetcher()
      .then((data) => {
        if (!cancelled) setState({ status: 'ready', data });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          navigate('/login', { replace: true });
          return;
        }
        const msg = err instanceof Error ? err.message : String(err);
        setState({ status: 'error', error: msg });
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}

// useResources fans out N fetchers in parallel, resolving when all finish.
// Errors short-circuit to the same 401 handler.
export function useResources<T extends readonly unknown[]>(
  fetchers: { [K in keyof T]: () => Promise<T[K]> },
  deps: unknown[],
): AsyncState<T> {
  const [state, setState] = useState<AsyncState<T>>({ status: 'loading' });
  const navigate = useNavigate();

  useEffect(() => {
    let cancelled = false;
    setState({ status: 'loading' });
    Promise.all(fetchers.map((f) => f()))
      .then((results) => {
        if (!cancelled) setState({ status: 'ready', data: results as unknown as T });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          navigate('/login', { replace: true });
          return;
        }
        const msg = err instanceof Error ? err.message : String(err);
        setState({ status: 'error', error: msg });
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);

  return state;
}
