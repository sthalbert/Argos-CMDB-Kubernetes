# Argos UI

Vite + React + TypeScript SPA. Served by `argosd` at `/ui/` in production via
`go:embed`; run standalone during development.

See [ADR-0006](../docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md) for
scope and rationale.

## What it does today

- **List pages** for all nine top-level kinds — Clusters, Namespaces, Nodes,
  Workloads, Pods, Services, Ingresses, PersistentVolumes,
  PersistentVolumeClaims — each with kind-specific columns (Node role / zone /
  instance-type / CPU-mem / Ready status; Ingress / Service load-balancer
  address; PVC bound PV; Workload container images; etc.).
- **Detail pages** for the core drill-down chain:
  - **Cluster** → namespaces + nodes + PVs inside it.
  - **Namespace** → workloads + pods + services + ingresses + PVCs
    ("application = namespace" view).
  - **Workload** → its pods (via `workload_id`), unique nodes they run on,
    container template ("application = workload" view).
  - **Pod** → containers with `image_id` digests + backlinks to parent
    workload / namespace.
  - **Node** → full enriched view (Identity, OS & runtime,
    Networking, capacity + allocatable table, Conditions with per-row
    health colouring, Taints, Labels) plus an impact-analysis callout
    and a workload-grouped pod breakdown.
  - **Ingress** → load-balancer block first (MetalLB / Kube-VIP / hardware
    LB VIP), then routing rules and TLS.
- **Component search** (`/ui/search/image`) — case-insensitive substring
  match across every container's `image` string on both Workloads and Pods
  (init containers included). Query is kept in the URL so auditors can
  bookmark or share a hit list ("every asset running `log4j:2.15`").
- **Auth** — paste a bearer token on `/ui/login`, held in `sessionStorage`
  (cleared on tab close, per ADR-0006). No cookies, no CORS — the SPA is
  same-origin as the API.

## Development loop

```bash
# 1. Once per checkout (or after dep bumps):
make ui-install

# 2. Terminal A — start argosd (API + embedded production bundle):
LONGUE_VUE_DATABASE_URL=postgres://... LONGUE_VUE_API_TOKEN=dev ./bin/argosd

# 3. Terminal B — Vite dev server with HMR, proxies /v1 /healthz /metrics -> :8080:
make ui-dev
# → open http://localhost:5173/ui/
```

For a one-command demo with seeded data, run `bash scripts/seed-demo.sh`
after argosd is up — it populates a realistic prod / staging inventory so
every list page has something to show.

## Production build

```bash
make ui-build      # produces ui/dist/
make build         # argosd binary embeds ui/dist via //go:embed
```

## Tests

The UI is covered by a Vitest suite — foundation tests over `api.ts`,
`hooks.ts`, `kv.ts`, `me.tsx`, and shared components, plus
render-level smoke tests for every page. The network is mocked with
[MSW](https://mswjs.io/); default handlers live in
`src/test/handlers.ts` and tests override them per-case.

```bash
make ui-test         # one-shot, used by CI and `make check`
cd ui && npm run test:watch     # interactive
cd ui && npm run test:coverage  # writes coverage/ HTML report
```

Test files are co-located next to the unit they cover (`foo.tsx` →
`foo.test.tsx`). Shared infrastructure (MSW server, fixtures, render
helper) lives in `src/test/`.

## Skipping the UI

Backend-only workflows (no Node toolchain) can use `make build-noui`. `/ui/`
then replies 404; `/v1/*` is unaffected.

## Layout

```
ui/
├── embed.go              # //go:embed all:dist (default build)
├── embed_noui.go         # stub under -tags noui
├── vite.config.ts        # base: '/ui/' + dev proxy to argosd
├── tsconfig.json
├── package.json
├── index.html
└── src/
    ├── main.tsx          # router + strict mode
    ├── App.tsx           # routes + auth gate + chrome
    ├── api.ts            # hand-written typed fetch wrapper
    ├── hooks.ts          # useResource / useResources (loading/error/data)
    ├── components.tsx    # shared primitives: AsyncView, LayerPill,
    │                     # LoadBalancerAddresses, Labels, KV, IdLink, …
    ├── styles.css        # dark theme, status pills, LB chips, kv-list grid
    └── pages/
        ├── Login.tsx     # bearer token -> sessionStorage
        ├── Lists.tsx     # all nine list pages in one file
        ├── Details.tsx   # Cluster / Namespace / Workload / Pod / Node / Ingress detail
        └── Search.tsx    # image-substring search (workloads + pods)
```

## Design choices

- **Hand-written API client** (`src/api.ts`) over OpenAPI codegen — the
  surface is still small and the hand-written form is easier to skim for
  now. Swap to codegen (`openapi-typescript-codegen` or similar) when the
  number of endpoints makes drift likely.
- **List and detail pages in two files** (`Lists.tsx`, `Details.tsx`)
  instead of one-file-per-page. Keeps imports and shared helpers
  (`NamespaceLink`, `useNamespaceIndex`) close to their call sites; every
  page stays small enough that a single file scan is fine.
- **`useResource` / `useResources`** hooks fold the loading / error / data
  tri-state plus the "401 → drop token → /login" rule in one place so
  every page reads like a straight render.
- **`AsyncView` nesting** instead of `Promise.all` in every page — a top-
  level `useResources(() => [...])` works when all fetches share the same
  input keys, but when a second fetch depends on the first's result
  (NamespaceDetail → Cluster, Node → pods filtered by node.name) a chain
  of `useResource` is clearer than building a reducer.
