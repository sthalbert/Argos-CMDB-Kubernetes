import '@testing-library/jest-dom/vitest';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

// Node 22+ ships an experimental native `localStorage` on globalThis,
// which vitest's jsdom env leaves in place instead of installing jsdom's.
// The native stub lacks .clear/.setItem in this configuration, so swap
// in a small in-memory Storage polyfill for both globalThis and window.
function makeStorage(): Storage {
  const data = new Map<string, string>();
  return {
    get length() { return data.size; },
    clear: () => data.clear(),
    getItem: (k: string) => (data.has(k) ? data.get(k)! : null),
    key: (i: number) => Array.from(data.keys())[i] ?? null,
    removeItem: (k: string) => { data.delete(k); },
    setItem: (k: string, v: string) => { data.set(k, String(v)); },
  };
}
for (const key of ['localStorage', 'sessionStorage'] as const) {
  const store = makeStorage();
  Object.defineProperty(globalThis, key, { configurable: true, value: store });
  Object.defineProperty(window, key, { configurable: true, value: store });
}

// `error` makes the test fail loudly when a request hits no handler —
// surfaces drift between api.ts and handlers.ts immediately.
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
