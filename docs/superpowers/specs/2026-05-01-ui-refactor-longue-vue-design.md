# UI refactor — adopt the longue-vue prototype

**Date:** 2026-05-01
**Branch:** `feat/ui-refactor-longue-vue`
**Status:** design approved; implementation plan to follow.

## 1. Summary

Replace the current Argos-era UI chrome with the longue-vue prototype's top-bar design and component vocabulary, restyling every existing page in a single pull request. Keep all current routes, bookmarks, and behaviour. Time-travel/diff is explicitly out of scope and will be tracked under a separate ADR.

## 2. Background

### 2.1 Inputs

- **Current UI** (`ui/src/`): Vite + React 18 + TypeScript SPA, dark theme, cyan accent (`#5cc8ff`), Space Grotesk body, JetBrains Mono code, left sidebar with sections (Kubernetes / Cloud Infrastructure / Tools / Admin) holding ~14 entry points. ~25 page components covering K8s entities, virtual machines, search, EOL, impact graph, admin (users / tokens / sessions / cloud-accounts / audit / settings), login, change-password.
- **Prototype** (`longue-vue.zip`): self-contained Babel-in-browser prototype. Top-bar nav, Newsreader serif for display, Inter/Space-Grotesk for body, JetBrains Mono for code. Five mocked surfaces: login splash, clusters list, cluster detail, lifecycle (EOL), audit log, plus a time-travel/diff surface that has no backend. Theming knobs (accent / density / pill-style) exposed via a developer Tweaks panel and a `<body data-*>` attribute system.

### 2.2 Personality of the prototype

The prototype is described in its own CSS as "lighter / more humane" than Argos: cool-slate neutrals, optional warmer accents, breathing room, and an italic-· wordmark `longue·vue`. The Newsreader serif on display headings is the prototype's signature personality marker.

## 3. Decisions

| # | Question | Answer |
|---|----------|--------|
| Q1 | Refactor goal | **Full chrome replacement.** Adopt the prototype's top-bar nav, login splash, component vocabulary, theming knobs as real user settings; re-skin every existing page. |
| Q2 | Page coverage | **Every page.** All ~25 pages restyled. Pages without prototype mocks extrapolate from the prototype's component vocabulary. |
| Q3 | Migration shape | **Single PR, single branch.** All chrome + page restyles + test updates land together; no in-tree feature flag. |
| Q4 | Mocks for unmocked pages | **Extrapolate.** No additional design loop; trust the implementer to apply the prototype's patterns consistently. |
| Q5 | Theming-knobs persistence | **`localStorage` only.** Single JSON blob keyed `lv:ui-prefs`. Per-browser, no backend changes. |
| Q6 | Top-bar shape | **Top bar + "More ▾" overflow.** All current routes stay reachable; high-traffic at top level, less-frequent K8s entity types under "More ▾". |
| Q7 | CSS class naming | **Hybrid.** Net-new components introduced by the refactor use `lv-*`. Existing class names (`pill`, `kv`, `entities`, `app-header`, `vm-filters`, `eol-summary-card`, etc.) stay; their CSS bodies are rewritten against the new tokens. |
| Q8 | Typography | **Newsreader display only.** Add Newsreader (display: page titles, brand mark, large numbers); keep Space Grotesk as body / UI; keep JetBrains Mono. |

Implementation sequence: **chrome-first** (Approach 1) — tokens → new chrome → primitives → page-by-page restyle → tests in lockstep.

## 4. Non-goals

- **Time-travel / diff view.** The prototype mocks it; the codebase has no backend for snapshots. This work is gated on a separate ADR + spec + plan and is not part of this refactor.
- **Per-user DB-backed UI preferences.** Decided against in Q5; cheap to add later if a user requests cross-device sync.
- **Light mode.** Dark-only stays.
- **Mobile-first redesign.** The new chrome stacks responsively at the same breakpoint the current app uses (~880–900 px); no dedicated mobile work.
- **New routes, pages, or features.** No additions to the IA. Existing URLs unchanged.
- **API or backend changes.** The Go side, OpenAPI spec, and database are untouched.

## 5. Architecture

### 5.1 File layout

```
ui/src/
├── styles.css                 ← REWRITTEN: tokens, fonts, density vars, accent vars
├── ui-prefs.tsx               ← NEW: UiPrefsProvider + useUiPrefs(), localStorage-backed
├── App.tsx                    ← REWRITTEN: top-bar Chrome, nav table, "More ▾" overflow, user menu
├── components/
│   ├── lv/                    ← NEW: net-new lv-* primitives
│   │   ├── Logomark.tsx
│   │   ├── Brand.tsx
│   │   ├── Pill.tsx
│   │   ├── KvList.tsx
│   │   ├── Section.tsx
│   │   ├── Callout.tsx
│   │   ├── StatRow.tsx        (exports StatRow + Stat)
│   │   ├── Breadcrumb.tsx
│   │   ├── Tabs.tsx
│   │   ├── PageHead.tsx
│   │   ├── EolCard.tsx
│   │   ├── AuditRow.tsx
│   │   ├── UiPrefsPanel.tsx
│   │   └── *.test.tsx         (one test file per primitive)
│   └── inventory/             ← existing module, surviving structurally
├── pages/                     ← every page rewritten to compose lv-* primitives
│   ├── Login.tsx
│   ├── ChangePassword.tsx
│   ├── Lists.tsx              (Clusters/Namespaces/Nodes/Workloads/Pods/Services/Ingresses/PVs/PVCs)
│   ├── Details.tsx            (ClusterDetail/NamespaceDetail/NodeDetail/WorkloadDetail/PodDetail/IngressDetail)
│   ├── EolDashboard.tsx
│   ├── ImpactGraph.tsx
│   ├── Search.tsx
│   ├── VirtualMachines.tsx
│   ├── VirtualMachineDetail.tsx
│   ├── cluster_curated.tsx, namespace_curated.tsx, node_curated.tsx
│   └── admin/
│       ├── AdminLayout.tsx
│       ├── Users.tsx, Tokens.tsx, Sessions.tsx
│       ├── CloudAccounts.tsx, CloudAccountDetail.tsx
│       ├── Audit.tsx
│       └── Settings.tsx
└── icons.tsx                  ← kept; SVG icon set still referenced by page tables
```

### 5.2 Module boundaries

- `ui-prefs.tsx` is a leaf module: depends on `localStorage` only, exports a context, hook, and provider. Read by `App.tsx` (chrome, user menu) and indirectly by every primitive that responds to body data attrs (CSS-only — no JS coupling).
- `components/lv/` primitives are leaves: each takes minimal props, renders fixed `lv-*` class names, no state, no data fetching. The only primitive that reads `useUiPrefs` is `UiPrefsPanel` (for the radios in the user menu).
- `App.tsx` knows the route table and the role-aware nav. It does not know any page's internals.
- Pages compose primitives; they do not import from each other.

## 6. CSS tokens and fonts

### 6.1 Token rewrite (`styles.css`)

Replace the current variable set with the prototype's, preserving `#5cc8ff` cyan as the default accent for Argos parity. Token names stay unprefixed (existing `--bg-1`, `--accent`, etc.) so existing class bodies don't need to be re-keyed when their values change.

Token groups:

- **Backgrounds:** `--bg-0`, `--bg-1`, `--bg-2`, `--bg-3`, `--bg-pill`, `--bg-label-k`. Cool-slate values from the prototype.
- **Foregrounds:** `--fg-1`, `--fg-2`, `--fg-3`, `--fg-muted`, `--fg-pill`.
- **Accent:** `--accent`, `--accent-dim`, `--accent-soft`, `--accent-ink`. Default cyan; switched at runtime by `<body data-accent>`.
- **Status:** `--ok-*`, `--warn-*`, `--bad-*`, `--danger`. Argos-aligned values, slightly warmer to match the prototype.
- **Diff (forward-compatible for time-travel ADR):** `--add-*`, `--rem-*`, `--mod-*` defined but unused by this refactor.
- **Density:** `--pad-cell-y`, `--pad-cell-x`, `--pad-card`, `--row-gap`. Defaults match `data-density="standard"`; overridden under `[data-density="compact"]` and `[data-density="comfortable"]`.
- **Radius:** `--radius-sm`, `--radius-md`, `--radius-lg`.
- **Type:** `--font-display: "Newsreader", "Source Serif Pro", Georgia, serif` (NEW); `--font-sans: "Space Grotesk", system-ui, …` (kept); `--font-mono: "JetBrains Mono", …` (kept).

### 6.2 Font loading

Update `ui/index.html` Google Fonts link to add `Newsreader:ital,wght@0,400;0,500;0,600;1,400;1,500` alongside the existing Space Grotesk and JetBrains Mono families. No runtime-only font loading.

### 6.3 Body data-attribute switches

Set on `<body>` from `UiPrefsProvider`'s effect; CSS rules read them.

- `<body data-accent="cyan|amber|sage|coral|violet">` — overrides `--accent*` group.
- `<body data-density="standard|compact|comfortable">` — overrides density group.
- `<body data-pill-style="solid|outline|dot">` — selects `.pill` / `.lv-pill` rule branch.

## 7. Chrome (rewritten `App.tsx`)

### 7.1 Layout

- Single horizontal `lv-header` at the top of the viewport. No sidebar. No collapse toggle.
- Three-zone flex layout: brand left, nav center (flex: 1), right cluster (polled-time pill + user-menu button) right.
- `<main>` below the header retains the current `max-width: var(--container-max)` constraint.

### 7.2 Brand

`<Brand>` renders the `<Logomark size={26}>` (helm-in-a-lens SVG, 7 spokes, central hub) followed by the wordmark `Longue·vue` with the `·` italicised in the accent color. Uses `--font-display`.

### 7.3 Primary nav

A single ordered table drives nav rendering:

```
[
  ['/clusters',          'Clusters'],
  ['/workloads',         'Workloads'],
  ['/nodes',             'Nodes'],
  ['/virtual-machines',  'Virtual Machines'],
  ['/eol',               'Lifecycle'],
  ['/search/image',      'Search'],
  // 'More ▾' renders inline here
  ['/admin/audit',       'Audit', { roles: ['admin', 'auditor'] }],
]
```

- Active-route detection matches the path *prefix*: `/clusters/:id` keeps Clusters lit.
- The Audit entry is filtered by the active user's role.
- The "More ▾" disclosure is hard-coded between Search and Audit.

### 7.4 "More ▾" overflow

A headless disclosure (button + popover positioned with `position: absolute` under the trigger). Items: Namespaces, Pods, Services, Ingresses, PVs, PVCs.

- `aria-haspopup="menu"`, `aria-expanded` on the trigger.
- Closes on: outside click, ESC, route change (subscribe to `useLocation`).
- Keyboard navigation: arrow keys move focus among items; Enter activates; Tab closes.

### 7.5 Right cluster

- Polled-time pill (`lv-time` with green dot), text `polled <ISO time>`. Updates every 30 s via a small `useEffect` interval.
- User-menu button: shows username + role pill. Clicking opens the user menu (same disclosure pattern as "More ▾").

### 7.6 User menu contents (in order)

1. Username + role pill (read-only header inside the menu).
2. UI prefs panel (`<UiPrefsPanel>`) — three radio sections: Accent (5 options) / Density (3) / Pill style (3).
3. Admin link (only for `admin | auditor`) — points to `/admin/users` for admins, `/admin/audit` for auditors.
4. Sign out button (calls `api.logout()` then `navigate('/login', { replace: true })`).

## 8. `lv-*` primitive library

All under `ui/src/components/lv/`. One file per primitive. Each is a pure functional component with no internal state (except where a sub-disclosure inherently has open/closed state — none of the listed primitives do).

**Class-name policy (per Q7.C).** A primitive that wraps an existing element (e.g. `Pill`, `KvList`, `Section`, `Breadcrumb`, `EolCard`) keeps the existing class name (`pill`, `kv-list`/`kv`, `section-title`, `breadcrumb`, `eol-summary-card`) — its CSS body is rewritten against the new tokens but consumers and tests need not change selectors. Primitives introduced by this refactor that have no current analogue (`Callout`, `StatRow`, `Stat`, `Tabs`, `PageHead`, `AuditRow`, `UiPrefsPanel`, `Logomark`, `Brand`) render `lv-*` class names. Pill modifiers move from `status-ok|warn|bad` to `ok|warn|bad|accent` (dropping the `status-` prefix and adding `accent`); selector updates on the small handful of test files asserting on `.pill.status-*` land in the same PR.

| Primitive | Props | Render |
|-----------|-------|--------|
| `Logomark` | `size?: number` | Helm-in-a-lens SVG, `currentColor` strokes (so it inherits accent in nav, fg in user menu). |
| `LogomarkLarge` | `size?: number` | 180 px variant for the login splash with cross-hair lens detail. |
| `Brand` | none | Logomark (26 px) + wordmark `Longue·vue` with italic `·`. |
| `Pill` | `status?: 'ok' \| 'warn' \| 'bad' \| 'accent'`, `children` | `<span class="pill {status}">`. Pill style (solid/outline/dot) is CSS-driven via `<body data-pill-style>`. |
| `KvList` | `items: Array<[label: ReactNode, value: ReactNode]>` | `<dl class="kv-list">` with `<dt>`/`<dd>` pairs. |
| `Section` | `count?: number`, `children` (title) | `<h3 class="section-title"><span>{children}</span><span class="count">· {count}</span><span class="section-rule" /></h3>`. |
| `Callout` | `title: ReactNode`, `children?: ReactNode` | Accent left-border block. |
| `StatRow` | `children: ReactNode` | Flex container; intended to wrap `Stat` children. |
| `Stat` | `label: string`, `value: ReactNode`, `tone?: 'accent' \| 'ok' \| 'warn' \| 'bad'`, `meta?: ReactNode` | Single stat cell. |
| `Breadcrumb` | `parts: Array<{ label: string; to?: string }>` | Renders separators automatically; non-last `to` parts become `<Link>`. |
| `Tabs` | `items: Array<{ key: string; label: string }>`, `active: string`, `onChange: (key) => void` | Uncontrolled-friendly tab strip. |
| `PageHead` | `title: ReactNode`, `sub?: ReactNode`, `actions?: ReactNode` | Standard top-of-page block; `actions` is a flex row aligned right. |
| `EolCard` | `status: 'ok' \| 'warn' \| 'bad'`, `count: number`, `label: string`, `meta: string`, `active?: boolean`, `onClick?: () => void` | Filter-toggling EOL summary card. |
| `AuditRow` | `time: string`, `actor: string`, `message: ReactNode`, `result: ReactNode` | Single audit row in the new grid layout. |
| `UiPrefsPanel` | none (reads `useUiPrefs`) | Three radio sections used inside the user menu. |

Primitives accept `className` and `style` for one-off overrides. None forward arbitrary `...rest` props; we keep the prop surface explicit.

## 9. Theming knobs persistence (`ui-prefs.tsx`)

```
type UiPrefs = {
  accent:    'cyan' | 'amber' | 'sage' | 'coral' | 'violet';
  density:   'compact' | 'standard' | 'comfortable';
  pillStyle: 'solid' | 'outline' | 'dot';
};

const DEFAULTS: UiPrefs = { accent: 'cyan', density: 'standard', pillStyle: 'solid' };
const STORAGE_KEY = 'lv:ui-prefs';
```

### 9.1 Provider

`<UiPrefsProvider>` wraps the authed branch of `App.tsx` (login screen renders with defaults).

- On mount: read `localStorage[STORAGE_KEY]`, JSON-parse, shallow-merge over `DEFAULTS` (forward-compat — unknown keys ignored, missing keys fall back to defaults).
- Effect on `prefs` change: write JSON back; set `document.body.dataset.accent`, `dataset.density`, `dataset.pillStyle`.
- Storage failures (private mode, quota): catch the throw, keep in-memory state, log a single `console.warn` ("UI preferences not persisted: <reason>").

### 9.2 Hook contract

```
const { prefs, setPref } = useUiPrefs();
setPref('accent', 'amber');
```

`setPref` is stable across renders. The provider stores prefs in `useState`, so consumers re-render when any pref changes.

### 9.3 Default load on Login

The login page renders before the provider mounts in the authed tree. To still apply persisted theming on the login splash, `ui-prefs.tsx` exports a `bootstrapBodyDataset()` function called once from `main.tsx` before React renders. This reads `localStorage` and sets the body dataset synchronously, so the login splash already shows the user's chosen accent before the provider takes over.

## 10. Per-page restyle plan

Every page rewrites its top to a `<PageHead title sub? actions />`, replaces ad-hoc heading/pill/kv markup with the new primitives, and updates table headers and class names that became inconsistent with the new tokens.

| Page | Shell | Notes |
|------|-------|-------|
| `Login` | full-screen split (`lv-login-page`): spyglass mark + tagline left, form right | Only page outside the chrome. OIDC button preserved below `lv-login-divider`. Forced-rotation banner (when applicable) becomes a `<Callout>`. |
| `ChangePassword` | centered `lv-card` with `<PageHead>` | When forced (`?forced=1`-equivalent), banner is a `<Callout status="warn">`. |
| `Clusters` (list) | `<PageHead>` + `lv-toolbar` (filter + env select) + `<table class="entities">` | Existing `entities` class stays; CSS body rewritten. |
| `ClusterDetail` | `<Breadcrumb>` + `<PageHead actions>` (EOL/criticality/env pills as actions) + `<StatRow>` (5 stats) + `<Callout title="Impact analysis">` + `<Tabs>` (Overview / Workloads / Nodes / History) + `detail-grid` (KvList left, ImpactGraph + workloads table right) + curated metadata `<lv-card>` | Reuses `cluster_curated.tsx` content inside the new card. Existing `detail-grid` class kept; CSS body rewritten. |
| `NamespaceDetail`, `NodeDetail`, `WorkloadDetail`, `PodDetail`, `IngressDetail` | Breadcrumb + PageHead + KvList + `<Section>` blocks for related entities | 2/3 detail-grid layout. |
| `Namespaces`, `Nodes`, `Workloads`, `Pods`, `Services`, `Ingresses`, `PersistentVolumes`, `PersistentVolumeClaims` | shared shape: `<PageHead>` + `lv-toolbar` (search) + `<table class="entities">` | One small commit per page covers column-tweaks and toolbar wiring. |
| `EolDashboard` | `<PageHead>` + `eol-summary` (3× `<EolCard>`) + filterable `eol-table` | Closest 1:1 with the prototype. Existing `eol-summary` / `eol-summary-card` classes kept; CSS bodies rewritten. Type column (cluster / node / vm) preserved. |
| `Search` | `<PageHead>` + `<Tabs>` (Image / Application) + result sections via `<Section>` | Two flows (image / app) become tabs. |
| `VirtualMachines` | `<PageHead>` + filters block (`vm-filters` class kept, new tokens) + search block + table | Cascading product/version dropdowns retained; explicit Search and Clear buttons preserved. |
| `VirtualMachineDetail` | shell mirrors ClusterDetail; Applications card via `<Section count>` + `<KvList>` | Ownership / Image / Networking / Apps split into cards. |
| `ImpactGraph` (route view) | `<PageHead>` + full-width `lv-card` containing the existing SVG; depth controls become a small tab-styled toggle row | SVG renderer untouched. |
| `AdminLayout` | `<PageHead>` + role-aware `<Tabs>` (Users / Tokens / Sessions / Cloud Accounts / Audit / Settings) replacing `admin-subnav` | Auditor still sees only Audit. |
| `Users`, `Tokens`, `Sessions`, `Settings`, `CloudAccounts`, `CloudAccountDetail` | shared shape: section-style heading + `lv-card` + form rows or table; reveal callouts (PAT / cloud-account creation) become `<Callout>` with `ok`-tone styling | Forms keep their current logic. |
| `Audit` (admin) | uses the new `<AuditRow>` primitive with the existing pagination control | Single layout, no duplication. |

Sub-views `cluster_curated.tsx`, `namespace_curated.tsx`, `node_curated.tsx` keep their state machines; the Edit/Save buttons become `lv-btn lv-btn-ghost` + `lv-btn lv-btn-primary` styled, and the surrounding wrapper becomes an `<lv-card>` with an `<lv-card-header>` row.

## 11. Testing approach

### 11.1 Selectors

Because existing class names are kept (Q7.C), most page tests do not need selector changes. Tests asserting on:

- `.sidebar`, `.sidebar-collapsed`, `.sidebar-nav` → update to `.lv-header` / `.lv-nav` / nav-link role queries.
- `.app-header h1` → update to `<Brand>` accessible-name query (or equivalent role query).

These updates land in the same PR.

### 11.2 New chrome tests (`App.chrome.test.tsx`)

- Renders all primary nav links for an admin user.
- Renders the role-restricted Audit link only for `admin | auditor`.
- Active-route highlight follows path prefix: `/clusters` and `/clusters/c-123` both highlight Clusters.
- "More ▾" dropdown opens on click, closes on outside click, ESC, and route change.
- User menu opens; UI-prefs radios update `localStorage` and `document.body.dataset.{accent,density,pillStyle}`.
- Sign-out triggers `api.logout()` and navigates to `/login`.

### 11.3 Primitive tests (`components/lv/*.test.tsx`)

One file per primitive. Each: a render-with-props test asserting on the rendered DOM shape, plus one snapshot for visual regressions. Roughly 12 small files; each ≤ 30 lines.

### 11.4 Existing page tests

Updated only where the rendered DOM structure changed. ClusterDetail's `detail-grid` rewrite reorders headings, so the test that asserts heading order adapts; most list-page tests are unaffected.

### 11.5 Backend/integration tests

No change. The Go side, OpenAPI spec, and DB are untouched. `go test ./...` and the OpenAPI validation tests stay green.

### 11.6 Make targets

`make ui-check` (TypeScript) and `make ui-build` (Vite) run as part of the pre-commit verification on this PR. `make check` (the umbrella Go target) runs unchanged.

## 12. Migration and rollout

### 12.1 Single-PR commit shape

Even though the PR is monolithic per Q3, commits inside the PR are grouped for review readability. Suggested order (each line ≈ one commit):

1. `chore(ui): rewrite styles.css tokens, add Newsreader font` — token rewrite, font include in `index.html`.
2. `feat(ui): add ui-prefs context with localStorage persistence` — `ui-prefs.tsx` + `bootstrapBodyDataset` call from `main.tsx`.
3. `feat(ui): add lv-* primitive library` — all 14 primitives + their tests under `components/lv/`.
4. `feat(ui): replace Chrome with top-bar + "More ▾" + user menu` — new `App.tsx`, new `App.chrome.test.tsx`, deletion of old Chrome and sidebar code.
5. `refactor(ui): restyle Login and ChangePassword to lv-* primitives`.
6. `refactor(ui): restyle clusters list/detail and curated cards`.
7. `refactor(ui): restyle K8s entity list pages` (namespaces, nodes, workloads, pods, services, ingresses, PVs, PVCs).
8. `refactor(ui): restyle K8s entity detail pages`.
9. `refactor(ui): restyle EOL dashboard`.
10. `refactor(ui): restyle virtual machines list and detail`.
11. `refactor(ui): restyle search and impact graph pages`.
12. `refactor(ui): restyle admin pages and admin layout`.
13. `chore(ui): update existing page tests for new chrome selectors`.

Each step compiles and passes tests on its own. A reviewer reading commits in order sees the chrome land first, then watches each page lift into the new system.

### 12.2 Build / deploy

- `make ui-build` produces `ui/dist/`; `make build` embeds it into the `longue-vue` binary via `//go:embed all:dist` (unchanged).
- `make build-noui` still works; `/ui/` returns 404 in noui builds (unchanged).
- The dev loop `make ui-dev` still proxies `/v1`, `/healthz`, `/metrics` to a local longue-vue on `:8080` (unchanged).

### 12.3 Reverse-out

If the PR has to be reverted post-merge, a single `git revert` of the merge commit restores the pre-refactor UI. Because no migrations, schema changes, or API changes ride along, the backend remains stable across a revert.

## 13. Follow-ups

These are explicitly *not* in this refactor. Each gets its own ADR / spec / plan when picked up.

- **Time-travel / diff view.** Needs an ADR (snapshot strategy, retention, query model) before any UI work. CSS tokens for diff (`--add-*`, `--rem-*`, `--mod-*`) are reserved in this refactor for forward-compat.
- **Per-user DB-backed UI prefs.** Add `users.ui_preferences JSONB` + two endpoints; small, easy to merge once a user asks for cross-device sync.
- **A11y deep pass.** Day-1 ARIA on the new disclosures is correct; a screen-reader walk-through across all pages is a separate task.
- **Mobile layout.** Stacking works at the existing breakpoint; a mobile-first redesign of cluster detail + EOL is a future track.
- **Light mode.** Out of scope. Would require token reorganisation (currently dark-only values).

## 14. Open questions

None at design time — all blocking questions resolved in §3. New questions surfaced during implementation will be raised in the corresponding implementation plan, not by editing this spec.
