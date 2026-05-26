import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from './api';
import type { PagedResponse } from './api';

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

export const PAGE_SIZE_OPTIONS = [20, 50, 100] as const;
export type PageSize = (typeof PAGE_SIZE_OPTIONS)[number];

const PAGE_SIZE_STORAGE_KEY = 'lv.ui.pageSize';
const PAGE_SIZE_EVENT = 'lv.ui.pageSize.changed';

function readStoredPageSize(): PageSize {
  const raw = localStorage.getItem(PAGE_SIZE_STORAGE_KEY);
  const n = raw ? parseInt(raw, 10) : NaN;
  return (PAGE_SIZE_OPTIONS as readonly number[]).includes(n) ? (n as PageSize) : 20;
}

// usePageSize is the one global rows-per-page knob shared across every
// paginated panel. Persists in localStorage; broadcasts changes so two
// panels on the same page stay in sync.
export function usePageSize(): [PageSize, (n: PageSize) => void] {
  const [size, setSize] = useState<PageSize>(() => readStoredPageSize());

  useEffect(() => {
    const sync = () => setSize(readStoredPageSize());
    window.addEventListener('storage', sync);
    window.addEventListener(PAGE_SIZE_EVENT, sync);
    return () => {
      window.removeEventListener('storage', sync);
      window.removeEventListener(PAGE_SIZE_EVENT, sync);
    };
  }, []);

  const update = (n: PageSize) => {
    localStorage.setItem(PAGE_SIZE_STORAGE_KEY, String(n));
    window.dispatchEvent(new Event(PAGE_SIZE_EVENT));
    setSize(n);
  };

  return [size, update];
}

export type PagedListState<T> = {
  items: T[];
  loading: boolean;
  error: string | null;
  pageSize: PageSize;
  setPageSize: (n: PageSize) => void;
  hasPrev: boolean;
  hasNext: boolean;
  next: () => void;
  prev: () => void;
};

// usePagedList drives one paginated panel. The cursor stack is local
// to the hook instance — navigating away and back starts fresh, which
// matches the rest of the UI's "fetch on mount" posture.
export function usePagedList<T>(
  fetchPage: (cursor: string | undefined, limit: number) => Promise<PagedResponse<T>>,
  deps: unknown[],
): PagedListState<T> {
  const [pageSize, setPageSizeRaw] = usePageSize();
  const [stack, setStack] = useState<string[]>([]);
  const [items, setItems] = useState<T[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();

  const cursor = stack.length === 0 ? undefined : stack[stack.length - 1];

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    fetchPage(cursor, pageSize)
      .then((page) => {
        if (cancelled) return;
        setItems(page.items);
        setNextCursor(page.next_cursor ?? undefined);
        setLoading(false);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) {
          navigate('/login', { replace: true });
          return;
        }
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cursor, pageSize, ...deps]);

  // Reset to page 1 whenever the caller's deps change (filter switch).
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => setStack([]), deps);

  return {
    items,
    loading,
    error,
    pageSize,
    setPageSize: (n) => {
      setStack([]);
      setPageSizeRaw(n);
    },
    hasPrev: stack.length > 0,
    hasNext: !!nextCursor,
    next: () => {
      if (nextCursor) setStack((s) => [...s, nextCursor]);
    },
    prev: () => setStack((s) => s.slice(0, -1)),
  };
}
