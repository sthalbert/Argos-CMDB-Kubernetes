import { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router';
import { ApiError } from './api';
import type { ListControlParams, PagedResponse } from './api';

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
  // errorCause keeps the original thrown value so callers can branch on
  // typed errors (e.g. a feature-disabled 409); error is its flattened
  // message for plain display.
  errorCause: unknown;
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
  const [errorCause, setErrorCause] = useState<unknown>(null);
  const navigate = useNavigate();

  const cursor = stack.length === 0 ? undefined : stack[stack.length - 1];

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    setErrorCause(null);
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
        setErrorCause(err);
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
    errorCause,
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

// useClientSort is the client-side twin of the server sort: local key/direction
// plus a generic comparator application. For full-payload tables only.
export function useClientSort<T>(
  initialKey: string,
  accessors: Record<string, (item: T) => string | number | null | undefined>,
) {
  const [sort, setSort] = useState(initialKey);
  const [asc, setAsc] = useState(true);
  const toggleSort = (key: string) => {
    if (key === sort) {
      setAsc(!asc);
    } else {
      setSort(key);
      setAsc(true);
    }
  };
  const sortItems = (items: T[]): T[] => {
    const get = accessors[sort];
    if (!get) return items;
    const dir = asc ? 1 : -1;
    return [...items].sort((a, b) => {
      const va = get(a);
      const vb = get(b);
      if (typeof va === 'number' && typeof vb === 'number') return (va - vb) * dir;
      return String(va ?? '').toLowerCase().localeCompare(String(vb ?? '').toLowerCase()) * dir;
    });
  };
  return { sort, asc, toggleSort, sortItems };
}

export type ListControls = {
  nameInput: string;
  setNameInput: (v: string) => void;
  name: string;
  sort: string;
  order: 'asc' | 'desc';
  toggleSort: (key: string) => void;
  params: ListControlParams;
  deps: unknown[];
};

// useListControls owns the uniform list controls (ADR-0042 phase 2): a
// debounced free-text name filter and a sort key/direction, all mirrored
// into the URL query string so any list view is a shareable link. The URL
// is the source of truth; typing debounces 300 ms before landing there.
// Cursors deliberately never reach the URL — a shared link opens page 1.
export function useListControls(): ListControls {
  const [searchParams, setSearchParams] = useSearchParams();
  const urlName = searchParams.get('name') ?? '';
  const sort = searchParams.get('sort') ?? '';
  const order: 'asc' | 'desc' = searchParams.get('order') === 'desc' ? 'desc' : 'asc';

  const [nameInput, setNameInput] = useState(urlName);
  const debouncedName = useDebouncedValue(nameInput.trim(), 300);

  // Debounced input → URL. replace:true so typing doesn't spam history.
  useEffect(() => {
    if (debouncedName === urlName) return;
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (debouncedName) next.set('name', debouncedName);
        else next.delete('name');
        return next;
      },
      { replace: true },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedName]);

  // External URL change (back/forward, shared link, block-pill link) →
  // input. Guarded so our own debounced write doesn't loop.
  useEffect(() => {
    if (urlName !== debouncedName) setNameInput(urlName);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlName]);

  const toggleSort = (key: string) => {
    // replace: sort clicks shouldn't pile up in browser history.
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set('sort', key);
      next.set('order', sort === key && order === 'asc' ? 'desc' : 'asc');
      return next;
    }, { replace: true });
  };

  return {
    nameInput,
    setNameInput,
    name: urlName,
    sort,
    order,
    toggleSort,
    params: {
      name: urlName || undefined,
      sort: sort || undefined,
      order: sort ? order : undefined,
    },
    deps: [urlName, sort, order],
  };
}

// useLocalListControls is the component-local twin of useListControls:
// same ListControls contract, but nothing is mirrored into the URL.
// Detail pages embed several list sections at once — URL params would
// collide, so sections keep their search/sort state to themselves
// (spec decision #5).
export function useLocalListControls(): ListControls {
  const [nameInput, setNameInput] = useState('');
  const [sort, setSort] = useState('');
  const [order, setOrder] = useState<'asc' | 'desc'>('asc');
  const name = useDebouncedValue(nameInput.trim(), 300);

  const toggleSort = (key: string) => {
    if (sort === key) {
      setOrder(order === 'asc' ? 'desc' : 'asc');
    } else {
      setSort(key);
      setOrder('asc');
    }
  };

  return {
    nameInput,
    setNameInput,
    name,
    sort,
    order,
    toggleSort,
    params: {
      name: name || undefined,
      sort: sort || undefined,
      order: sort ? order : undefined,
    },
    deps: [name, sort, order],
  };
}
