import '@testing-library/jest-dom/vitest';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

// Node 25 ships a built-in `localStorage` stub (backed by --localstorage-file)
// that lacks .clear() / .setItem() / .getItem() when no file path is supplied.
// vitest's populateGlobal() only overrides globals that appear in its KEYS
// allow-list, and localStorage is absent from that list, so jsdom's full
// implementation is never installed.  Re-assign it explicitly here so every
// test file gets the real jsdom Storage.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const _g = globalThis as any;
if (typeof _g.jsdom !== 'undefined') {
  // vitest's jsdom environment attaches the JSDOM instance as `globalThis.jsdom`.
  // Node 25 ships a built-in `localStorage` stub (backed by --localstorage-file)
  // that lacks .clear() / .setItem() / .getItem() when no file path is supplied.
  // vitest's populateGlobal() only overrides globals that appear in its KEYS
  // allow-list, and localStorage is absent from that list, so jsdom's full
  // implementation is never installed.  Re-assign it explicitly here so every
  // test file gets the real jsdom Storage.
  const jsdomWindow: Window = _g.jsdom.window;
  Object.defineProperty(globalThis, 'localStorage', {
    get: () => jsdomWindow.localStorage,
    configurable: true,
  });
  Object.defineProperty(globalThis, 'sessionStorage', {
    get: () => jsdomWindow.sessionStorage,
    configurable: true,
  });
}

// `error` makes the test fail loudly when a request hits no handler —
// surfaces drift between api.ts and handlers.ts immediately.
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
