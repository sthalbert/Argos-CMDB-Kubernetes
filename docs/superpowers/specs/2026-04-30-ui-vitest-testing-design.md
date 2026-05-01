# UI testing with Vitest — design

- **Date:** 2026-04-30
- **Status:** Approved (brainstorm complete; pending implementation plan)
- **Scope:** `ui/` only (the React + TypeScript SPA embedded into `longue-vue`)

## Goals

Stand up a Vitest-based test suite for the SPA covering two layers:

1. **Foundation** — unit tests for the pure / near-pure modules: `api.ts`,
   `hooks.ts`, `kv.ts`, `me.tsx`, and the helper components in
   `components.tsx`.
2. **Page rendering** — render-level smoke tests for each page file
   (Lists, Details, Search, Login, ChangePassword, EolDashboard,
   ImpactGraph, VirtualMachines, VirtualMachineDetail, the `*_curated`
   pages, and the `pages/admin/*` set). Each smoke test asserts the page
   renders without crashing and exposes the loading → ready → error
   tri-state with the network mocked.

Network mocking goes through **MSW** (Mock Service Worker). Tests run
in CI and locally via `make`.

## Non-goals

- No enforced coverage threshold. Vitest produces a coverage report; the
  build does not fail on coverage. Threshold enforcement is a follow-up
  once the suite stabilises.
- No interaction or flow tests in the first cut (no click-through
  assertions, no edit-flow exercises, no navigation chains). Follow-up.
- No SVG / layout assertions on `ImpactGraph` or `EolDashboard` charts.
- No visual-regression / screenshot testing.
- No end-to-end browser tests (Playwright / Cypress). Out of scope.

## Stack

All entries land in `ui/package.json` `devDependencies`:

| Package | Purpose |
|---|---|
| `vitest` | Test runner |
| `@vitest/coverage-v8` | Coverage report (no threshold enforced) |
| `jsdom` | DOM environment for React rendering |
| `@testing-library/react` | Render + query helpers |
| `@testing-library/jest-dom` | DOM matchers (`toBeInTheDocument`, …) |
| `@testing-library/user-event` | User interaction primitives (kept in scope so the follow-up PR doesn't have to add it) |
| `msw` | HTTP request interception for `fetch` |

Rationale for **MSW over `vi.fn()`-stubbed fetch**: the page tests all hit
the same handful of endpoints; centralising default responses behind MSW
handlers keeps page tests focused on rendering rather than re-stubbing
`fetch` per test.

Rationale for **jsdom over happy-dom**: jsdom is the Vitest-recommended
default and matches the React Testing Library docs; happy-dom is faster
but its compatibility surface drifts. The suite is small enough that the
speed delta does not matter.

## File layout

Tests are co-located next to the unit they cover (matching the Go-side
convention of `foo.go` + `foo_test.go`):

```
ui/
├── vitest.config.ts          # NEW — separate from vite.config.ts
└── src/
    ├── api.ts
    ├── api.test.ts           # NEW
    ├── hooks.ts
    ├── hooks.test.tsx        # NEW (uses RTL, hence .tsx)
    ├── kv.ts
    ├── kv.test.ts            # NEW
    ├── components.tsx
    ├── components.test.tsx   # NEW
    ├── me.tsx
    ├── me.test.tsx           # NEW
    ├── pages/
    │   ├── Lists.tsx
    │   ├── Lists.test.tsx    # NEW
    │   ├── Details.tsx
    │   ├── Details.test.tsx  # NEW
    │   ├── … one .test.tsx per page file
    │   └── admin/
    │       └── … one .test.tsx per admin page file
    ├── components/
    │   └── inventory/
    │       ├── AnnotationsCard.tsx
    │       ├── AnnotationsCard.test.tsx   # NEW
    │       └── … one .test.tsx per inventory card
    └── test/                 # NEW — shared test infrastructure
        ├── setup.ts          # jest-dom matchers + MSW server lifecycle
        ├── handlers.ts       # default MSW handlers (one per api.ts endpoint)
        ├── server.ts         # setupServer(...handlers)
        ├── fixtures.ts       # canonical Cluster / Node / Pod / etc. shapes
        └── render.tsx        # renderWithRouter helper (MemoryRouter + initial route)
```

`ui/src/test/` is deliberately not named `__tests__/` — that suffix is a
Jest-era convention; under Vitest the `test/` folder is fine and avoids
the implication that `__tests__/` will auto-glob test files.

## Configuration

`ui/vitest.config.ts` (separate file from `vite.config.ts`):

```ts
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
      // No thresholds enforced; report only.
    },
  },
});
```

`globals: true` means tests don't have to import `describe / it / expect`
explicitly. The matching TS types are picked up via a triple-slash
reference in `src/test/setup.ts` (`/// <reference types="vitest/globals" />`)
or a `types` entry in `tsconfig.json`.

`src/test/setup.ts` registers `@testing-library/jest-dom`, then wires the
MSW server lifecycle:

```ts
import '@testing-library/jest-dom/vitest';
import { afterAll, afterEach, beforeAll } from 'vitest';
import { server } from './server';

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
```

`onUnhandledRequest: 'error'` is the deliberate strict choice: any page
test that hits a URL without a matching handler fails loudly. This is
how we catch the case where `api.ts` grew a new endpoint and `handlers.ts`
hasn't been updated.

## Scripts and Make integration

`ui/package.json` scripts:

```json
"scripts": {
  "dev": "vite",
  "build": "tsc --noEmit && vite build",
  "preview": "vite preview",
  "typecheck": "tsc --noEmit",
  "test": "vitest run",
  "test:watch": "vitest",
  "test:coverage": "vitest run --coverage"
}
```

Makefile additions:

```makefile
ui-test: ## Run UI tests (Vitest)
\tcd ui && npm test
```

`make check` is extended to chain `ui-test` after the existing `test`
target so the local pre-push gate covers both Go and the SPA.

`make ui-install` is unchanged but now also pulls the new dev deps.

## CI integration

`.github/workflows/ci.yml` gets a new step in the existing job that
already runs `npm ci && npm run build` (so the Node 22 setup and
`npm ci` cache are reused — no new job, no new cache key):

```yaml
- name: UI tests
  working-directory: ui
  run: npm test
```

The step lands after `npm run build` so type errors surface first
(faster failure) and tests run against the same install. Failures block
merge. No coverage upload step in this PR; can be added later if the
team wants Codecov / similar.

## Initial test corpus (what ships in PR #1)

### `api.test.ts`

- `request()` query-string encoding skips `undefined`, `null`, and empty
  string parameters; encodes specials.
- JSON body sets `Content-Type: application/json`; merge-patch endpoints
  override to `application/merge-patch+json`.
- 204 response returns `undefined` without parsing a body.
- Non-2xx response throws `ApiError` carrying `status` and the RFC 7807
  `detail` field; falls back to `title`, then to `statusText`.
- Non-JSON error body falls back to `statusText` without throwing.
- A representative endpoint per category (auth, admin, CMDB list, CMDB
  patch, impact, cloud-accounts, virtual-machines) — enough that
  refactors to `request()` show their blast radius.

### `hooks.test.tsx`

- `useResource`: loading → ready transition; ready data matches the
  fetcher result.
- `useResource`: error path surfaces `err.message`.
- `useResource`: a fetcher rejecting with `new ApiError(401, ...)`
  triggers `navigate('/login', { replace: true })` and does not transition
  to error.
- `useResource`: changing `deps` re-runs the fetcher.
- `useResource`: unmount before resolution does not call `setState`
  (verified by a fetcher that resolves after unmount and asserting no
  React warning logs).
- `useResources`: same five cases above for the parallel variant.
- `useDebouncedValue`: value propagates only after the debounce window
  with `vi.useFakeTimers()`.

### `kv.test.ts` and helpers in `components.test.tsx`

- `kv.ts` pure helpers — every exported function gets a happy-path and an
  edge-case test.
- `components.tsx` pure renderers: `LoadBalancerAddresses`, `Labels`,
  `LayerPill`, `IdLink`, `KV` — input props → output DOM.

### `me.test.tsx`

- Renders the username for `kind: 'user'`.
- Renders the token name for `kind: 'token'`.
- Renders the role badge for each `Role` value.

### Page smoke tests

For each page file under `ui/src/pages/` (and `ui/src/pages/admin/`), one
test file with three tests:

1. **Renders without crashing.** Mount with `renderWithRouter` at the
   page's route. Assert a stable element from the page chrome
   (heading, sidebar entry, page title) is present.
2. **Loading → ready.** With the default MSW handlers returning the
   fixture data, assert the loading indicator appears and is then
   replaced by an element that proves the data was rendered (e.g., a
   table row containing the fixture cluster's name).
3. **Error state.** Override the handler for the page's primary fetch to
   return 500. Assert the error UI renders and the page does not crash.

Pages in scope:

- `Lists.tsx` — one render assertion per list view (Clusters, Namespaces,
  Nodes, Workloads, Pods, Services, Ingresses, PVs, PVCs).
- `Details.tsx` — one render assertion per exported detail view that
  actually exists today: `ClusterDetail`, `NamespaceDetail`,
  `WorkloadDetail`, `PodDetail`, `NodeDetail`, `IngressDetail`. (No
  Service / PV / PVC detail page is exported from this file at the time
  of writing — confirmed against `pages/Details.tsx`.)
- `Search.tsx`
- `Login.tsx`
- `ChangePassword.tsx`
- `EolDashboard.tsx`
- `ImpactGraph.tsx` (smoke only — no SVG layout asserts)
- `VirtualMachines.tsx`
- `VirtualMachineDetail.tsx`
- `cluster_curated.tsx`, `namespace_curated.tsx`, `node_curated.tsx`
- Under `ui/src/pages/admin/`: `Audit.tsx`, `CloudAccounts.tsx`,
  `CloudAccountDetail.tsx`, `Sessions.tsx`, `Settings.tsx`, `Tokens.tsx`,
  `Users.tsx`. `AdminLayout.tsx` is layout chrome and gets a single
  "renders without crashing" test only (no loading/error variants).
- Under `ui/src/components/inventory/`: `AnnotationsCard.tsx`,
  `ApplicationsCard.tsx`, `CapacityCard.tsx`, `CuratedMetadataCard.tsx`,
  `IdentityCard.tsx`, `LabelsCard.tsx`, `NetworkingCard.tsx` — these are
  pure presentational cards. Treated as foundation-level tests (props →
  DOM, no MSW), one `*.test.tsx` per card.

If a page renders multiple sub-views inside a single file (Lists.tsx,
Details.tsx), each sub-view counts as one of the three-test groups above
inside the same `*.test.tsx` file.

### Test fixtures

`ui/src/test/fixtures.ts` exports canonical instances of every `api.ts`
type (Cluster, Node, Namespace, Pod, Workload, Service, Ingress, PV, PVC,
VirtualMachine, CloudAccount, User, ApiToken, AuditEvent, ImpactGraph,
Settings, AuthConfig, Me). Tests import and partially override per case.
This keeps fixture drift in one file when `api.ts` types change.

### MSW handler set

`ui/src/test/handlers.ts` ships one default handler per `api.ts`
endpoint, returning the matching fixture. Tests override per-test via
`server.use(...)` for error / edge cases. The strict
`onUnhandledRequest: 'error'` setting ensures a missing handler fails
the test rather than the request silently 404ing in jsdom.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| MSW handler set drifts from `api.ts` endpoints. | Strict `onUnhandledRequest: 'error'` makes the drift fail loudly the first time a page calls an un-handled URL. |
| Co-located `*.test.tsx` files end up in the embedded UI bundle. | `vite.config.ts` `build.rollupOptions` already only consumes what `index.html` references; `*.test.tsx` is unreferenced. Verify with a `du -sh ui/dist` check before vs after to confirm bundle size is unchanged. |
| Test types pollute production `tsc --noEmit` build. | Vitest types are scoped via `vitest/globals` reference inside `setup.ts`; `tsconfig.json` `include` already covers `src/`, so no separate `tsconfig.test.json` is needed. If the strict `noUnusedLocals` flag fights with the test files, isolate test-only TS settings via `tsconfig.test.json` referenced from `vitest.config.ts`. |
| `useResource`'s navigate-on-401 path is hard to test without a router. | `renderWithRouter` mounts the hook under a `MemoryRouter`; the test asserts on the resulting location after the rejection settles. |
| Coverage of `Lists.tsx` / `Details.tsx` is low because each smoke test only exercises one sub-view. | Acceptable for the first cut. The follow-up interaction PR will broaden it. The non-goal is documented above. |

## What this design does NOT decide

- Whether to enforce coverage thresholds in a follow-up. (Out of scope.)
- Which interaction flows the follow-up PR will cover. (Out of scope —
  pick the highest-leverage flows after the smoke suite is green.)
- Whether to add Playwright for true end-to-end tests later. (Out of
  scope.)
- Whether to swap the hand-written `api.ts` for an OpenAPI-generated
  client. (Out of scope; orthogonal to testing strategy.)

## Open questions

None. All decisions confirmed during brainstorming on 2026-04-30.

## Implementation order (for the follow-up plan)

1. Add dev deps + `vitest.config.ts` + `src/test/` infra (setup,
   handlers, server, fixtures, render helper). Land a single sanity test
   to prove the wiring works.
2. Add `api.test.ts` and `hooks.test.tsx` (foundation tests).
3. Add `kv.test.ts`, `components.test.tsx`, `me.test.tsx`.
4. Add page smoke tests in batches by file (Lists → Details → Search →
   admin → the rest).
5. Wire `make ui-test`, extend `make check`, add the CI step.
6. Update `ui/README.md` with a short "Tests" section.

The implementation plan (next skill) sequences these into commits.
