# Longue-vue UI Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Argos-era left-sidebar chrome and ad-hoc styles with the longue-vue prototype's top-bar chrome, a small `lv-*` primitive library, and `localStorage`-backed theming knobs (accent / density / pill style). Restyle every existing page in a single PR. No backend changes.

**Architecture:** Token rewrite + Newsreader font → `ui-prefs` context → `lv-*` primitive library → new top-bar `Chrome` (replaces `App.tsx`'s sidebar) → per-page restyle. Hybrid CSS naming (Q7.C in spec): existing class names like `pill`, `kv-list`, `section-title`, `breadcrumb`, `entities`, `eol-summary`, `eol-summary-card`, `detail-grid` stay; their CSS bodies get rewritten against the new tokens. Net-new components (Callout, StatRow, Tabs, PageHead, AuditRow, the chrome itself) render `lv-*` class names. Pill modifiers move from `status-ok|warn|bad` to `ok|warn|bad|accent`.

**Tech Stack:** Vite + React 18 + TypeScript SPA, Vitest + Testing Library, react-router-dom v6. CSS via custom properties + body data-attribute switches (no CSS-in-JS, no Tailwind). Newsreader (display) + Space Grotesk (body) + JetBrains Mono (code) via Google Fonts.

**Reference docs:**
- Spec: `docs/superpowers/specs/2026-05-01-ui-refactor-longue-vue-design.md`
- Prototype assets: `/tmp/lv-zip/` (or re-extract from `longue-vue.zip` at repo root)
- CLAUDE.md project guide for backend context
- `ui/src/App.tsx` (current chrome) and `ui/src/styles.css` (current tokens) — both will be substantially rewritten

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `ui/src/ui-prefs.tsx` | `UiPrefsProvider`, `useUiPrefs()` hook, `bootstrapBodyDataset()` helper. Single source of truth for accent/density/pill-style. localStorage-backed under key `lv:ui-prefs`. |
| `ui/src/ui-prefs.test.tsx` | Tests for provider, hook, and bootstrap. |
| `ui/src/components/lv/Logomark.tsx` | Helm-in-lens SVG (compact + large variants). |
| `ui/src/components/lv/Brand.tsx` | Brand mark: Logomark + italic-`·` wordmark. |
| `ui/src/components/lv/Pill.tsx` | Wraps existing `<span class="pill {status}">`. |
| `ui/src/components/lv/KvList.tsx` | Wraps existing `<dl class="kv-list">` markup. |
| `ui/src/components/lv/Section.tsx` | Wraps existing `<h3 class="section-title">` markup with rule + count. |
| `ui/src/components/lv/Breadcrumb.tsx` | Wraps existing `.breadcrumb` markup; uses `react-router-dom` `<Link>`. |
| `ui/src/components/lv/EolCard.tsx` | Wraps existing `.eol-summary-card` markup; click-to-filter. |
| `ui/src/components/lv/Callout.tsx` | NEW `lv-callout` accent-left-border block. |
| `ui/src/components/lv/StatRow.tsx` | NEW `lv-stat-row` + `lv-stat` cell. Exports `StatRow` and `Stat`. |
| `ui/src/components/lv/Tabs.tsx` | NEW `lv-tabs` strip; controlled. |
| `ui/src/components/lv/PageHead.tsx` | NEW `lv-page-head` + `lv-page-title`. |
| `ui/src/components/lv/AuditRow.tsx` | NEW `lv-audit-row` 4-column grid. |
| `ui/src/components/lv/UiPrefsPanel.tsx` | Three radio sections (accent / density / pill style). |
| `ui/src/components/lv/Disclosure.tsx` | Headless reusable disclosure for "More ▾" and user menu (open state, outside-click + ESC + route-change close). |
| `ui/src/components/lv/*.test.tsx` | One test file per primitive (12 files). |
| `ui/src/App.chrome.test.tsx` | Tests for the rewritten Chrome (nav, role-gating, More dropdown, user menu, sign-out). |

### Modified files

| File | Change |
|------|--------|
| `ui/index.html` | Add Newsreader to the Google Fonts link. |
| `ui/src/main.tsx` | Call `bootstrapBodyDataset()` before `ReactDOM.createRoot(...).render(...)`. |
| `ui/src/styles.css` | Wholesale token rewrite. New typography vars (`--font-display`). New density / radius / accent-soft vars. New `lv-header`, `lv-nav`, `lv-time`, `lv-user`, `lv-iconbtn`, `lv-page-head`, `lv-stat-row`, `lv-callout`, `lv-tabs`, `lv-audit-row` rules. Existing class bodies (`.pill`, `.kv-list`, `.section-title`, `.breadcrumb`, `.entities`, `.eol-summary`, `.eol-summary-card`, `.detail-grid`, `.vm-filters`, `.vm-search`, `.curated-card`, `.admin-form`, etc.) rewritten to use the new tokens; sidebar rules removed. |
| `ui/src/App.tsx` | Replace `Chrome` (sidebar layout) with the top-bar layout. Hard-coded primary nav table + role-gated Audit + "More ▾" overflow + user-menu disclosure. Routes block unchanged. |
| `ui/src/pages/Login.tsx` | Two-column splash: spyglass mark left, form right. OIDC button preserved below `lv-login-divider`. |
| `ui/src/pages/ChangePassword.tsx` | Centered `lv-card` with `<PageHead>`; forced banner becomes `<Callout>`. |
| `ui/src/pages/Lists.tsx` | All 9 list pages use `<PageHead>` + `lv-toolbar` + existing `entities` table. |
| `ui/src/pages/Details.tsx` | Cluster/Namespace/Node/Workload/Pod/Ingress detail pages use `<Breadcrumb>` + `<PageHead>` + `<KvList>` + `<Section>` + `<Tabs>` (cluster only) + `<StatRow>` (cluster only). |
| `ui/src/pages/cluster_curated.tsx`, `namespace_curated.tsx`, `node_curated.tsx` | Edit/Save buttons use `lv-btn lv-btn-primary` / `lv-btn lv-btn-ghost`. Wrapper becomes `<lv-card>` with `<lv-card-header>`. |
| `ui/src/pages/EolDashboard.tsx` | `<PageHead>` + `eol-summary` (3 `<EolCard>`) + filterable `eol-table`. |
| `ui/src/pages/Search.tsx` | `<PageHead>` + `<Tabs>` (Image / Application) + `<Section>` blocks. |
| `ui/src/pages/VirtualMachines.tsx` | `<PageHead>` + filters block + search block + table. Cascading product/version dropdown + Search/Clear buttons preserved. |
| `ui/src/pages/VirtualMachineDetail.tsx` | Mirrors `ClusterDetail`. Applications card via `<Section count>` + `<KvList>`. |
| `ui/src/pages/ImpactGraph.tsx` | `<PageHead>` + full-width `lv-card` containing existing SVG. Depth controls become a small tab-styled toggle. SVG renderer untouched. |
| `ui/src/pages/admin/AdminLayout.tsx` | `<PageHead>` + role-aware `<Tabs>` replacing `admin-subnav`. |
| `ui/src/pages/admin/Users.tsx`, `Tokens.tsx`, `Sessions.tsx`, `Settings.tsx`, `CloudAccounts.tsx`, `CloudAccountDetail.tsx` | `<PageHead>` + `<lv-card>` wrappers. Reveal callouts (PAT, cloud-account creation) become `<Callout>` ok-tone. |
| `ui/src/pages/admin/Audit.tsx` | Uses `<AuditRow>` primitive; existing pagination control kept. |
| `ui/src/pages/*.test.tsx`, `ui/src/pages/admin/*.test.tsx` | Update selectors that asserted on `.sidebar*` or `.app-header h1`. Switch to `lv-header` / role-based queries. |
| `ui/src/components.tsx` | Already exports shared widgets the inventory module reuses; left structurally untouched but its imports may be re-pointed if a primitive is moved into `lv/`. (No action expected — verify in self-check.) |

---

## Conventions for every commit

- Branch: `feat/ui-refactor-longue-vue` (already checked out).
- Commit format: `type(scope): description` (Conventional Commits). Scope is `ui` for almost everything; tests can land in the same commit as their code.
- Git: always pass `-c commit.gpgsign=false`. Never use `--no-verify`.
- After each commit: `git status` to confirm a clean tree.
- After steps that change React components: run `npm --prefix ui run test -- --run <file>` (NOT watch mode) and `npm --prefix ui run typecheck`.
- After Task 1 lands (CSS rewrite), run `npm --prefix ui run build` once to confirm Vite still bundles.

---

## Task 1: Token rewrite + Newsreader font

**Files:**
- Modify: `ui/index.html` (line 8)
- Modify: `ui/src/styles.css` (full rewrite of `:root` block, sidebar rules deleted, new chrome rules added, existing class bodies rewritten)

The current `styles.css` is ~1035 lines; this task swaps in a new top section and rewrites bodies of existing rules. Pages that have not yet been touched will still render — they reference the same class names.

- [ ] **Step 1: Add Newsreader to the Google Fonts link**

Edit `ui/index.html` line 8. Replace:

```html
<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;600&family=Space+Grotesk:wght@400;500;600;700&display=swap" rel="stylesheet">
```

with:

```html
<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;600&family=Newsreader:ital,wght@0,400;0,500;0,600;1,400;1,500&family=Space+Grotesk:wght@400;500;600;700&display=swap" rel="stylesheet">
```

- [ ] **Step 2: Rewrite the `:root` block in `ui/src/styles.css`**

Replace lines 1–77 (the whole `:root { ... }` block) with:

```css
:root {
  /* ---- Backgrounds (cool slate, matched to cyan accent) ---- */
  --bg-0: #0e1116;
  --bg-1: #131720;
  --bg-2: #1a1f2a;
  --bg-3: #212735;
  --bg-hover: #1a1f2a;
  --bg-pill: #252b38;
  --bg-label-k: #232936;

  /* ---- Foregrounds ---- */
  --fg-1: #e4e8ee;
  --fg-2: #eef1f6;
  --fg-3: #c1c7d1;
  --fg-muted: #7d8593;
  --fg-pill: #a6aeba;

  /* ---- Brand accent (cyan = default; switched at runtime via [data-accent]) ---- */
  --accent: #5cc8ff;
  --accent-dim: #3a7fa3;
  --accent-soft: #15303f;
  --accent-ink: #0e1116;

  /* ---- Borders ---- */
  --border: #2c3340;
  --border-soft: #232935;

  /* ---- Status ---- */
  --ok-bg: #1a3a23;
  --ok-fg: #82d68f;
  --ok-border: #245a32;
  --ok-ink: #c5f0cc;
  --warn-bg: #3d2c14;
  --warn-fg: #fab661;
  --warn-border: #5a4320;
  --bad-bg: #3a191c;
  --bad-fg: #f7716e;
  --bad-border: #5a2528;
  --danger: #f87171;

  /* ---- Reveal (existing PAT/secret-reveal callout) ---- */
  --reveal-bg: #1a3a23;
  --reveal-border: #245a32;
  --reveal-accent: #82d68f;

  /* ---- Diff (forward-compat reservation for future time-travel ADR) ---- */
  --add-bg: rgba(130, 214, 143, 0.10);
  --add-mark: #82d68f;
  --rem-bg: rgba(247, 113, 110, 0.10);
  --rem-mark: #f7716e;
  --mod-bg: rgba(230, 180, 80, 0.10);
  --mod-mark: #e6b450;

  /* ---- Typography ---- */
  --font-sans: 'Space Grotesk', system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  --font-mono: 'JetBrains Mono', ui-monospace, SFMono-Regular, "SF Mono", Consolas, "Liberation Mono", Menlo, monospace;
  --font-display: 'Newsreader', 'Source Serif Pro', Georgia, serif;

  --fs-xs: 0.72rem;
  --fs-2xs: 0.75rem;
  --fs-sm: 0.80rem;
  --fs-base: 0.85rem;
  --fs-body: 0.90rem;
  --fs-md: 0.92rem;
  --fs-lg: 0.95rem;
  --fs-h2: 1.05rem;
  --fs-h1: 1.20rem;
  --fs-display: 1.7rem;

  --fw-regular: 400;
  --fw-medium: 500;
  --fw-semi: 600;
  --fw-bold: 700;

  --ls-label: 0.04em;
  --ls-caps: 0.05em;
  --ls-divider: 0.08em;
  --ls-wide: 0.02em;

  /* ---- Density (overridden under [data-density="compact|comfortable"]) ---- */
  --pad-cell-y: 0.6rem;
  --pad-cell-x: 0.85rem;
  --pad-card: 1.1rem 1.35rem;
  --row-gap: 0.5rem;

  /* ---- Radius / strokes ---- */
  --radius-sm: 3px;
  --radius-md: 4px;
  --radius-lg: 8px;
  --stroke: 1px;
  --stroke-thick: 2px;
  --stroke-accent: 3px;
  --container-max: 1240px;
}

body[data-density="compact"] {
  --pad-cell-y: 0.45rem;
  --pad-cell-x: 0.7rem;
  --pad-card: 0.85rem 1rem;
  --row-gap: 0.35rem;
}
body[data-density="comfortable"] {
  --pad-cell-y: 0.85rem;
  --pad-cell-x: 1rem;
  --pad-card: 1.4rem 1.65rem;
  --row-gap: 0.7rem;
}

/* Accent overrides via <body data-accent="..."> */
body[data-accent="amber"] {
  --accent: #e6b450;
  --accent-dim: #8a6a2a;
  --accent-soft: #3d2c14;
  --accent-ink: #0e1116;
}
body[data-accent="sage"] {
  --accent: #82d68f;
  --accent-dim: #3f6e48;
  --accent-soft: #1a3a23;
  --accent-ink: #0e1116;
}
body[data-accent="coral"] {
  --accent: #f7716e;
  --accent-dim: #8a3a38;
  --accent-soft: #3a191c;
  --accent-ink: #0e1116;
}
body[data-accent="violet"] {
  --accent: #b794f6;
  --accent-dim: #6c4eb0;
  --accent-soft: #2a1f4a;
  --accent-ink: #0e1116;
}
```

- [ ] **Step 3: Delete the old sidebar rules in `ui/src/styles.css`**

Delete every selector under `/* --- Sidebar layout --- */` and `.sidebar*` rules (the `.app-layout`, `.sidebar`, `.sidebar-collapsed`, `.sidebar-toggle`, `.sidebar-nav`, `.sidebar-divider`, `.sidebar-section-label`, `.app-main`, `.app-header` blocks). Use Grep to confirm zero remaining matches:

Run: `grep -n -E '\.(app-layout|sidebar(-[a-z]+)?|app-main|app-header)\b' ui/src/styles.css`
Expected: no matches after edit.

- [ ] **Step 4: Add new chrome rules to `ui/src/styles.css`**

Append to the end of `styles.css` (after the existing rules, before any media queries):

```css
/* =============== app shell (new top-bar chrome) =============== */
.lv-app {
  min-height: 100vh;
  background: var(--bg-1);
  color: var(--fg-1);
  display: flex;
  flex-direction: column;
}
.lv-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.85rem 1.5rem;
  background: var(--bg-2);
  border-bottom: 1px solid var(--border);
  gap: 1.5rem;
  position: sticky;
  top: 0;
  z-index: 30;
}
.lv-brand {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  flex-shrink: 0;
}
.lv-brand-mark {
  width: 28px;
  height: 28px;
  display: block;
  color: var(--accent);
}
.lv-brand-name {
  font-family: var(--font-display);
  font-size: 1.15rem;
  font-weight: 500;
  letter-spacing: -0.01em;
  color: var(--fg-2);
}
.lv-brand-name em {
  font-style: italic;
  color: var(--accent);
  font-weight: 400;
}
.lv-nav {
  display: flex;
  gap: 0.25rem;
  flex: 1;
  margin-left: 1rem;
  flex-wrap: wrap;
}
.lv-nav-link {
  padding: 0.4rem 0.75rem;
  font-size: 0.86rem;
  color: var(--fg-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  white-space: nowrap;
  background: transparent;
  border: none;
  font-family: inherit;
}
.lv-nav-link:hover { background: var(--bg-3); color: var(--fg-1); text-decoration: none; }
.lv-nav-link.active {
  color: var(--accent);
  background: var(--accent-soft);
}
.lv-header-right {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-shrink: 0;
}
.lv-time {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--fg-muted);
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.lv-time-dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--ok-fg);
  box-shadow: 0 0 0 3px rgba(130, 214, 143, 0.18);
}
.lv-user {
  display: flex; align-items: center; gap: 0.5rem;
  font-size: 0.85rem; color: var(--fg-3);
}
.lv-iconbtn {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--fg-3);
  padding: 0.35rem 0.7rem;
  font-size: 0.82rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  font-family: inherit;
}
.lv-iconbtn:hover { color: var(--fg-1); border-color: var(--fg-muted); }
.lv-iconbtn[aria-expanded="true"] { color: var(--fg-1); border-color: var(--fg-muted); }
.lv-main {
  flex: 1;
  padding: 1.75rem 1.5rem 3rem;
  max-width: var(--container-max);
  width: 100%;
  margin: 0 auto;
}

/* =============== disclosure popovers (More ▾, user menu) =============== */
.lv-popover {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  background: var(--bg-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 0.4rem;
  min-width: 220px;
  z-index: 40;
  box-shadow: 0 8px 28px rgba(0,0,0,0.35);
}
.lv-popover[data-side="left"] { left: 0; right: auto; }
.lv-popover-item {
  display: block;
  width: 100%;
  text-align: left;
  padding: 0.45rem 0.65rem;
  font-size: 0.86rem;
  color: var(--fg-2);
  background: transparent;
  border: none;
  border-radius: var(--radius-sm);
  cursor: pointer;
  font-family: inherit;
}
.lv-popover-item:hover, .lv-popover-item:focus-visible {
  background: var(--bg-3);
  color: var(--fg-1);
  outline: none;
}
.lv-popover-item.active { color: var(--accent); }
.lv-popover-divider {
  height: 1px;
  background: var(--border-soft);
  margin: 0.35rem 0;
}
.lv-popover-section-label {
  font-size: 0.68rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--fg-muted);
  font-weight: 600;
  padding: 0.4rem 0.65rem 0.2rem;
}
.lv-popover-relative {
  position: relative;
  display: inline-flex;
}

/* =============== net-new lv-* primitives =============== */
.lv-page-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin: 0.25rem 0 1.5rem;
  gap: 1rem;
  flex-wrap: wrap;
}
.lv-page-title {
  font-family: var(--font-display);
  font-size: 1.7rem;
  font-weight: 500;
  color: var(--fg-2);
  letter-spacing: -0.015em;
  line-height: 1.15;
  margin: 0;
}
.lv-page-sub {
  font-size: 0.88rem;
  color: var(--fg-muted);
  margin-top: 0.3rem;
}
.lv-page-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.lv-callout {
  background: var(--bg-3);
  border: 1px solid var(--border);
  border-left: 3px solid var(--accent);
  padding: 0.8rem 1.1rem;
  border-radius: var(--radius-md);
  font-size: 0.88rem;
  margin: 0.75rem 0 1.25rem;
  color: var(--fg-2);
}
.lv-callout strong { color: var(--fg-2); font-weight: 600; }
.lv-callout.warn { border-left-color: var(--warn-fg); }
.lv-callout.bad { border-left-color: var(--bad-fg); }
.lv-callout.ok { border-left-color: var(--ok-fg); }

.lv-stat-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 0;
  background: var(--bg-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  margin-bottom: 1.25rem;
}
.lv-stat {
  padding: 0.85rem 1.1rem;
  border-right: 1px solid var(--border-soft);
}
.lv-stat:last-child { border-right: none; }
.lv-stat-label {
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--fg-muted);
  font-weight: 600;
  margin-bottom: 0.25rem;
}
.lv-stat-value {
  font-family: var(--font-display);
  font-size: 1.45rem;
  font-weight: 500;
  letter-spacing: -0.01em;
  color: var(--fg-2);
  line-height: 1.1;
}
.lv-stat-value.accent { color: var(--accent); }
.lv-stat-value.bad { color: var(--bad-fg); }
.lv-stat-value.warn { color: var(--warn-fg); }
.lv-stat-value.ok { color: var(--ok-fg); }
.lv-stat-meta {
  font-size: 0.76rem;
  color: var(--fg-muted);
  margin-top: 0.15rem;
}

.lv-tabs {
  display: flex;
  gap: 0;
  border-bottom: 1px solid var(--border);
  margin-bottom: 1.25rem;
  flex-wrap: wrap;
}
.lv-tab {
  padding: 0.6rem 1rem;
  font-size: 0.86rem;
  color: var(--fg-muted);
  cursor: pointer;
  border-bottom: 2px solid transparent;
  margin-bottom: -1px;
  background: transparent;
  border-left: none;
  border-right: none;
  border-top: none;
  font-family: inherit;
}
.lv-tab:hover { color: var(--fg-2); }
.lv-tab.active {
  color: var(--accent);
  border-bottom-color: var(--accent);
}

.lv-toolbar {
  display: flex; align-items: center; gap: 0.6rem;
  margin-bottom: 1rem; flex-wrap: wrap;
}
.lv-toolbar input, .lv-toolbar select { flex: 0 1 auto; min-width: 220px; }

.lv-card {
  background: var(--bg-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: var(--pad-card);
}
.lv-card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.85rem;
}
.lv-card-title {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--fg-2);
}
.lv-card-subtle {
  font-size: 0.82rem;
  color: var(--fg-muted);
}

.lv-audit-row {
  display: grid;
  grid-template-columns: 130px 80px 1fr 100px;
  gap: 1rem;
  padding: 0.7rem 0.9rem;
  border-bottom: 1px solid var(--border-soft);
  align-items: center;
  font-size: 0.86rem;
}
.lv-audit-time {
  font-family: var(--font-mono);
  font-size: 0.78rem;
  color: var(--fg-muted);
}
.lv-audit-actor {
  font-family: var(--font-mono);
  font-size: 0.82rem;
  color: var(--fg-2);
}
.lv-audit-msg { color: var(--fg-1); }
.lv-audit-result {
  font-size: 0.78rem;
  text-align: right;
}

.lv-btn {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--fg-2);
  padding: 0.45rem 0.9rem;
  font-size: 0.85rem;
  border-radius: var(--radius-md);
  cursor: pointer;
  font-family: inherit;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.lv-btn:hover { border-color: var(--fg-muted); color: var(--fg-1); }
.lv-btn-primary {
  background: var(--accent);
  color: var(--accent-ink);
  border-color: var(--accent);
  font-weight: 600;
}
.lv-btn-primary:hover { filter: brightness(1.08); }
.lv-btn-ghost {
  background: var(--accent-soft);
  border-color: transparent;
  color: var(--accent);
}
.lv-btn-ghost:hover { filter: brightness(1.15); }
.lv-btn-danger {
  color: var(--danger);
}
.lv-btn-danger:hover { border-color: var(--danger); color: var(--danger); }

/* =============== pill modifier rename (status-* → bare) =============== */
/* Existing .pill rule body is rewritten in step 5; this block adds the new modifiers. */
.pill.ok { background: var(--ok-bg); color: var(--ok-fg); }
.pill.warn { background: var(--warn-bg); color: var(--warn-fg); }
.pill.bad { background: var(--bad-bg); color: var(--bad-fg); }
.pill.accent { background: var(--accent-soft); color: var(--accent); }

body[data-pill-style="outline"] .pill {
  background: transparent;
  border: 1px solid var(--border);
}
body[data-pill-style="outline"] .pill.ok { border-color: var(--ok-border); color: var(--ok-fg); }
body[data-pill-style="outline"] .pill.warn { border-color: var(--warn-border); color: var(--warn-fg); }
body[data-pill-style="outline"] .pill.bad { border-color: var(--bad-border); color: var(--bad-fg); }
body[data-pill-style="outline"] .pill.accent { border-color: var(--accent-dim); color: var(--accent); }

body[data-pill-style="dot"] .pill {
  background: transparent;
  border: 1px solid var(--border);
  padding-left: 0.65rem;
  position: relative;
}
body[data-pill-style="dot"] .pill::before {
  content: "";
  width: 6px; height: 6px;
  border-radius: 50%;
  background: currentColor;
  margin-right: 0.4rem;
  display: inline-block;
}
body[data-pill-style="dot"] .pill.ok { color: var(--ok-fg); }
body[data-pill-style="dot"] .pill.warn { color: var(--warn-fg); }
body[data-pill-style="dot"] .pill.bad { color: var(--bad-fg); }
body[data-pill-style="dot"] .pill.accent { color: var(--accent); }

/* =============== login splash (lv-login-page) =============== */
.lv-login-page {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 1fr 1fr;
  background: var(--bg-1);
}
@media (max-width: 880px) {
  .lv-login-page { grid-template-columns: 1fr; }
  .lv-login-side { display: none; }
}
.lv-login-side {
  background:
    radial-gradient(ellipse at 30% 30%, rgba(92, 200, 255, 0.15), transparent 55%),
    radial-gradient(ellipse at 70% 75%, rgba(58, 127, 163, 0.14), transparent 60%),
    var(--bg-2);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  padding: 3rem;
  position: relative;
  overflow: hidden;
}
.lv-login-side::before, .lv-login-side::after {
  content: "";
  position: absolute;
  top: 50%; left: 50%;
  transform: translate(-50%, -50%);
  border-radius: 50%;
  border: 1px solid rgba(92, 200, 255, 0.12);
  pointer-events: none;
}
.lv-login-side::before { width: 520px; height: 520px; }
.lv-login-side::after { width: 320px; height: 320px; border-color: rgba(92, 200, 255, 0.18); }
.lv-login-mark {
  position: absolute;
  top: 50%; left: 50%;
  transform: translate(-50%, -50%);
  color: var(--accent);
  width: 140px; height: 140px;
  opacity: 0.85;
}
.lv-login-tagline {
  font-family: var(--font-display);
  font-size: 2rem;
  line-height: 1.2;
  color: var(--fg-2);
  letter-spacing: -0.015em;
  font-weight: 400;
  position: relative;
  z-index: 2;
  max-width: 24ch;
}
.lv-login-tagline em { color: var(--accent); font-style: italic; }
.lv-login-foot {
  font-size: 0.78rem;
  color: var(--fg-muted);
  font-family: var(--font-mono);
  position: relative;
  z-index: 2;
  display: flex;
  justify-content: space-between;
}
.lv-login-form-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 3rem 2rem;
}
.lv-login-form { width: 100%; max-width: 380px; }
.lv-login-form h2 {
  font-family: var(--font-display);
  font-size: 1.45rem;
  font-weight: 500;
  color: var(--fg-2);
  letter-spacing: -0.015em;
  margin-bottom: 0.3rem;
}
.lv-login-divider {
  display: flex; align-items: center; gap: 0.75rem;
  margin: 1.25rem 0;
  color: var(--fg-muted);
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.1em;
}
.lv-login-divider::before, .lv-login-divider::after {
  content: ""; flex: 1; height: 1px; background: var(--border);
}
```

- [ ] **Step 5: Rewrite existing class bodies in `ui/src/styles.css` to use the new tokens**

For each existing class body that references token variables only (most of them), no change is needed — the variable values changed, the references stayed. The classes that need explicit body edits:

1. The `.pill` rule (existing): keep its base body but **rename modifier selectors** in the same file. Search for `.pill.status-ok`, `.pill.status-warn`, `.pill.status-bad` and **delete those rules** (the new `.pill.ok|warn|bad|accent` rules in Step 4 replace them). Also rewrite the base `.pill` body to:

```css
.pill {
  display: inline-flex;
  align-items: center;
  font-size: 0.74rem;
  padding: 0.15rem 0.5rem;
  border-radius: var(--radius-md);
  background: var(--bg-pill);
  color: var(--fg-pill);
  letter-spacing: 0.02em;
  border: 1px solid transparent;
  font-weight: 500;
  white-space: nowrap;
}
```

2. `.kv-list` and `.kv` rules: rewrite to:

```css
dl.kv-list, .kv-list {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--row-gap) 1.5rem;
  margin: 0;
}
.kv-list dt, .kv dt {
  color: var(--fg-muted);
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-weight: 600;
  padding-top: 0.15rem;
}
.kv-list dd, .kv dd {
  margin: 0;
  font-size: 0.9rem;
  color: var(--fg-1);
  word-break: break-word;
}
```

3. `.section-title` rule: rewrite to:

```css
.section-title {
  font-size: 0.78rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--fg-muted);
  font-weight: 600;
  margin: 1.75rem 0 0.65rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.section-title .section-rule {
  flex: 1;
  height: 1px;
  background: var(--border-soft);
}
.section-title .count {
  color: var(--fg-muted);
  text-transform: none;
  letter-spacing: 0;
  font-weight: 400;
  font-size: 0.78rem;
}
```

4. `.breadcrumb` rule: rewrite to:

```css
.breadcrumb {
  font-size: 0.84rem;
  color: var(--fg-muted);
  margin-bottom: 0.4rem;
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.breadcrumb a { color: var(--fg-muted); }
.breadcrumb a:hover { color: var(--accent); text-decoration: none; }
.breadcrumb-sep { color: var(--border); }
```

5. `.entities` table: rewrite to use the new density vars:

```css
table.entities { width: 100%; border-collapse: collapse; font-size: 0.88rem; }
table.entities th, table.entities td {
  text-align: left;
  padding: var(--pad-cell-y) var(--pad-cell-x);
  border-bottom: 1px solid var(--border-soft);
  vertical-align: middle;
}
table.entities th {
  font-weight: 600;
  color: var(--fg-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 0.72rem;
  white-space: nowrap;
}
table.entities tbody tr { transition: background 0.08s ease; }
table.entities tbody tr:hover td { background: var(--bg-2); }
```

6. `.detail-grid`: rewrite to:

```css
.detail-grid {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 3fr);
  gap: 2rem;
  align-items: start;
}
@media (max-width: 900px) {
  .detail-grid { grid-template-columns: 1fr; }
}
```

7. `.eol-summary` and `.eol-summary-card`: rewrite to:

```css
.eol-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 1rem;
  margin-bottom: 1.75rem;
}
.eol-summary-card {
  padding: 1.1rem 1.35rem;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--bg-2);
  cursor: pointer;
  transition: outline 0.1s, opacity 0.1s, transform 0.12s ease;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  position: relative;
  overflow: hidden;
  text-align: left;
  font-family: inherit;
}
.eol-summary-card:hover, .eol-summary-card.active {
  outline: 2px solid currentColor;
  outline-offset: -1px;
}
.eol-summary-card.eol-bad,
.eol-summary-card.bad { background: linear-gradient(180deg, var(--bad-bg), var(--bg-2) 65%); color: var(--bad-fg); border-color: var(--bad-border); }
.eol-summary-card.eol-warn,
.eol-summary-card.warn { background: linear-gradient(180deg, var(--warn-bg), var(--bg-2) 65%); color: var(--warn-fg); border-color: var(--warn-border); }
.eol-summary-card.eol-ok,
.eol-summary-card.ok { background: linear-gradient(180deg, var(--ok-bg), var(--bg-2) 65%); color: var(--ok-fg); border-color: var(--ok-border); }
.eol-summary-card .eol-count {
  font-family: var(--font-display);
  font-size: 2.2rem;
  font-weight: 500;
  letter-spacing: -0.02em;
  line-height: 1;
}
.eol-summary-card .eol-label { font-size: 0.84rem; font-weight: 500; opacity: 0.95; }
.eol-summary-card .eol-meta { font-size: 0.76rem; opacity: 0.7; }

.eol-table tr.row-bad td { background: rgba(247, 113, 110, 0.07); }
.eol-table tr.row-warn td { background: rgba(250, 182, 97, 0.05); }
.eol-table tr.row-bad:hover td { background: rgba(247, 113, 110, 0.14); }
.eol-table tr.row-warn:hover td { background: rgba(250, 182, 97, 0.10); }
```

For all other existing class bodies (`.vm-filters`, `.vm-search`, `.vm-banner`, `.curated-card`, `.admin-form`, `.impact-graph-container`, `.reveal-callout`, `.entities`, etc.), if the rule already references token variables only, leave it alone — values flow through. Only adjust spacing / padding values that hardcode rems if they look noticeably out of line with the new density.

- [ ] **Step 6: Confirm Vite still bundles**

Run: `npm --prefix ui run build`
Expected: build succeeds; `ui/dist/` produced. Warnings about unused selectors are fine.

- [ ] **Step 7: Confirm typecheck still passes**

Run: `npm --prefix ui run typecheck`
Expected: clean exit (existing code untouched at compile level).

- [ ] **Step 8: Commit**

```bash
git add ui/index.html ui/src/styles.css
git -c commit.gpgsign=false commit -m "chore(ui): rewrite styles.css tokens, add Newsreader font"
```

---

## Task 2: ui-prefs context with localStorage persistence

**Files:**
- Create: `ui/src/ui-prefs.tsx`
- Create: `ui/src/ui-prefs.test.tsx`
- Modify: `ui/src/main.tsx`

- [ ] **Step 1: Write the ui-prefs test first**

Create `ui/src/ui-prefs.test.tsx`:

```tsx
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, act } from '@testing-library/react';
import { UiPrefsProvider, useUiPrefs, bootstrapBodyDataset, STORAGE_KEY, DEFAULTS } from './ui-prefs';

const Probe = () => {
  const { prefs, setPref } = useUiPrefs();
  return (
    <div>
      <span data-testid="accent">{prefs.accent}</span>
      <span data-testid="density">{prefs.density}</span>
      <span data-testid="pillStyle">{prefs.pillStyle}</span>
      <button onClick={() => setPref('accent', 'amber')}>amber</button>
      <button onClick={() => setPref('density', 'compact')}>compact</button>
      <button onClick={() => setPref('pillStyle', 'dot')}>dot</button>
    </div>
  );
};

describe('UiPrefs', () => {
  beforeEach(() => {
    localStorage.clear();
    document.body.removeAttribute('data-accent');
    document.body.removeAttribute('data-density');
    document.body.removeAttribute('data-pill-style');
  });

  it('falls back to DEFAULTS when localStorage is empty', () => {
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe(DEFAULTS.accent);
    expect(getByTestId('density').textContent).toBe(DEFAULTS.density);
    expect(getByTestId('pillStyle').textContent).toBe(DEFAULTS.pillStyle);
  });

  it('reads persisted prefs from localStorage on mount', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'sage', density: 'compact', pillStyle: 'outline' }));
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe('sage');
    expect(getByTestId('density').textContent).toBe('compact');
    expect(getByTestId('pillStyle').textContent).toBe('outline');
  });

  it('sets body data-attributes on mount and on change', () => {
    const { getByText } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(document.body.dataset.accent).toBe(DEFAULTS.accent);
    act(() => { getByText('amber').click(); });
    expect(document.body.dataset.accent).toBe('amber');
    expect(JSON.parse(localStorage.getItem(STORAGE_KEY)!).accent).toBe('amber');
  });

  it('shallow-merges unknown / partial localStorage payloads', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'violet', extra: 'ignored' }));
    const { getByTestId } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(getByTestId('accent').textContent).toBe('violet');
    expect(getByTestId('density').textContent).toBe(DEFAULTS.density);
  });

  it('survives a localStorage write throw without crashing', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const { getByText } = render(<UiPrefsProvider><Probe /></UiPrefsProvider>);
    expect(() => act(() => { getByText('compact').click(); })).not.toThrow();
    expect(warn).toHaveBeenCalled();
    setItem.mockRestore();
    warn.mockRestore();
  });

  it('bootstrapBodyDataset applies persisted prefs synchronously', () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ accent: 'coral', density: 'comfortable', pillStyle: 'dot' }));
    bootstrapBodyDataset();
    expect(document.body.dataset.accent).toBe('coral');
    expect(document.body.dataset.density).toBe('comfortable');
    expect(document.body.dataset.pillStyle).toBe('dot');
  });

  it('bootstrapBodyDataset applies DEFAULTS when no persisted prefs', () => {
    bootstrapBodyDataset();
    expect(document.body.dataset.accent).toBe(DEFAULTS.accent);
    expect(document.body.dataset.pillStyle).toBe(DEFAULTS.pillStyle);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `npm --prefix ui run test -- --run src/ui-prefs.test.tsx`
Expected: FAIL with module-not-found on `./ui-prefs`.

- [ ] **Step 3: Implement `ui/src/ui-prefs.tsx`**

```tsx
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Accent = 'cyan' | 'amber' | 'sage' | 'coral' | 'violet';
export type Density = 'compact' | 'standard' | 'comfortable';
export type PillStyle = 'solid' | 'outline' | 'dot';

export type UiPrefs = {
  accent: Accent;
  density: Density;
  pillStyle: PillStyle;
};

export const DEFAULTS: UiPrefs = { accent: 'cyan', density: 'standard', pillStyle: 'solid' };
export const STORAGE_KEY = 'lv:ui-prefs';

const ACCENTS: ReadonlySet<Accent> = new Set(['cyan', 'amber', 'sage', 'coral', 'violet']);
const DENSITIES: ReadonlySet<Density> = new Set(['compact', 'standard', 'comfortable']);
const PILL_STYLES: ReadonlySet<PillStyle> = new Set(['solid', 'outline', 'dot']);

function readPrefs(): UiPrefs {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return { ...DEFAULTS };
    const parsed = JSON.parse(raw) as Partial<UiPrefs>;
    return {
      accent: ACCENTS.has(parsed.accent as Accent) ? (parsed.accent as Accent) : DEFAULTS.accent,
      density: DENSITIES.has(parsed.density as Density) ? (parsed.density as Density) : DEFAULTS.density,
      pillStyle: PILL_STYLES.has(parsed.pillStyle as PillStyle) ? (parsed.pillStyle as PillStyle) : DEFAULTS.pillStyle,
    };
  } catch {
    return { ...DEFAULTS };
  }
}

function writeBodyDataset(p: UiPrefs) {
  document.body.dataset.accent = p.accent;
  document.body.dataset.density = p.density;
  document.body.dataset.pillStyle = p.pillStyle;
}

// Called once from main.tsx before React renders so the login splash already
// reflects the user's accent / density / pill choice before the provider mounts.
export function bootstrapBodyDataset(): void {
  writeBodyDataset(readPrefs());
}

type Ctx = { prefs: UiPrefs; setPref: <K extends keyof UiPrefs>(key: K, value: UiPrefs[K]) => void };

const UiPrefsContext = createContext<Ctx | null>(null);

export function UiPrefsProvider({ children }: { children: ReactNode }) {
  const [prefs, setPrefs] = useState<UiPrefs>(readPrefs);

  useEffect(() => {
    writeBodyDataset(prefs);
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(prefs));
    } catch (err) {
      console.warn('UI preferences not persisted:', err);
    }
  }, [prefs]);

  const setPref = useCallback(<K extends keyof UiPrefs>(key: K, value: UiPrefs[K]) => {
    setPrefs((p) => ({ ...p, [key]: value }));
  }, []);

  const value = useMemo(() => ({ prefs, setPref }), [prefs, setPref]);
  return <UiPrefsContext.Provider value={value}>{children}</UiPrefsContext.Provider>;
}

export function useUiPrefs(): Ctx {
  const ctx = useContext(UiPrefsContext);
  if (!ctx) throw new Error('useUiPrefs must be used inside <UiPrefsProvider>');
  return ctx;
}
```

- [ ] **Step 4: Run the tests to verify pass**

Run: `npm --prefix ui run test -- --run src/ui-prefs.test.tsx`
Expected: 7 passing.

- [ ] **Step 5: Wire `bootstrapBodyDataset` into `main.tsx`**

Replace `ui/src/main.tsx` with:

```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import App from './App';
import { bootstrapBodyDataset } from './ui-prefs';
import './styles.css';

bootstrapBodyDataset();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter basename="/ui">
      <App />
    </BrowserRouter>
  </React.StrictMode>,
);
```

- [ ] **Step 6: Verify typecheck**

Run: `npm --prefix ui run typecheck`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add ui/src/ui-prefs.tsx ui/src/ui-prefs.test.tsx ui/src/main.tsx
git -c commit.gpgsign=false commit -m "feat(ui): add ui-prefs context with localStorage persistence"
```

---

## Task 3: Logomark + Brand primitives

**Files:**
- Create: `ui/src/components/lv/Logomark.tsx`
- Create: `ui/src/components/lv/Logomark.test.tsx`
- Create: `ui/src/components/lv/Brand.tsx`
- Create: `ui/src/components/lv/Brand.test.tsx`

- [ ] **Step 1: Write the Logomark test**

Create `ui/src/components/lv/Logomark.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Logomark, LogomarkLarge } from './Logomark';

describe('Logomark', () => {
  it('renders an SVG with the requested size', () => {
    const { container } = render(<Logomark size={32} />);
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('width')).toBe('32');
    expect(svg.getAttribute('height')).toBe('32');
    expect(svg.getAttribute('aria-hidden')).toBe('true');
  });

  it('renders 7 spokes', () => {
    const { container } = render(<Logomark size={28} />);
    const lines = container.querySelectorAll('svg line');
    expect(lines.length).toBe(7);
  });
});

describe('LogomarkLarge', () => {
  it('renders cross-hairs for the lens detail', () => {
    const { container } = render(<LogomarkLarge size={180} />);
    const svg = container.querySelector('svg')!;
    expect(svg.getAttribute('viewBox')).toBe('0 0 180 180');
    // Cross-hair lines = 2 (horizontal + vertical) plus the 7 spokes.
    const lines = container.querySelectorAll('svg line');
    expect(lines.length).toBe(9);
  });
});
```

- [ ] **Step 2: Run — expect FAIL**

Run: `npm --prefix ui run test -- --run src/components/lv/Logomark.test.tsx`
Expected: module not found.

- [ ] **Step 3: Implement `Logomark.tsx`**

```tsx
type SpokeProps = { cx: number; cy: number; r: number; hub: number; strokeWidth?: number };

function Spokes({ cx, cy, r, hub, strokeWidth = 1.5 }: SpokeProps) {
  const out = [];
  for (let i = 0; i < 7; i++) {
    const a = (i / 7) * Math.PI * 2 - Math.PI / 2;
    const x = cx + Math.cos(a) * r;
    const y = cy + Math.sin(a) * r;
    const xi = cx + Math.cos(a) * hub;
    const yi = cy + Math.sin(a) * hub;
    out.push(<line key={i} x1={xi} y1={yi} x2={x} y2={y} strokeWidth={strokeWidth} />);
  }
  return <>{out}</>;
}

function heptagon(cx: number, cy: number, r: number): string {
  return Array.from({ length: 7 }, (_, i) => {
    const a = (i / 7) * Math.PI * 2 - Math.PI / 2;
    return `${(cx + Math.cos(a) * r).toFixed(2)},${(cy + Math.sin(a) * r).toFixed(2)}`;
  }).join(' ');
}

export function Logomark({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <svg
      viewBox="0 0 32 32"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <circle cx={16} cy={16} r={13} strokeWidth={1.75} />
      <polygon points={heptagon(16, 16, 8)} strokeWidth={1.5} />
      <Spokes cx={16} cy={16} r={8} hub={2.5} strokeWidth={1.5} />
      <circle cx={16} cy={16} r={1.6} strokeWidth={1.5} fill="currentColor" />
    </svg>
  );
}

export function LogomarkLarge({ size = 180 }: { size?: number }) {
  return (
    <svg
      viewBox="0 0 180 180"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle cx={90} cy={90} r={75} strokeWidth={1.5} />
      <circle cx={90} cy={90} r={62} strokeWidth={0.75} opacity={0.35} />
      <polygon points={heptagon(90, 90, 50)} strokeWidth={1.5} />
      <Spokes cx={90} cy={90} r={50} hub={14} strokeWidth={1.5} />
      <circle cx={90} cy={90} r={8} strokeWidth={1.5} fill="currentColor" />
      <line x1={15} y1={90} x2={165} y2={90} strokeWidth={0.5} opacity={0.2} />
      <line x1={90} y1={15} x2={90} y2={165} strokeWidth={0.5} opacity={0.2} />
    </svg>
  );
}
```

- [ ] **Step 4: Verify Logomark passes**

Run: `npm --prefix ui run test -- --run src/components/lv/Logomark.test.tsx`
Expected: PASS.

- [ ] **Step 5: Write the Brand test**

Create `ui/src/components/lv/Brand.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Brand } from './Brand';

describe('Brand', () => {
  it('renders the wordmark with italic dot in accent', () => {
    const { container } = render(<Brand />);
    const wordmark = container.querySelector('.lv-brand-name');
    expect(wordmark?.textContent).toBe('Longue·vue');
    expect(wordmark?.querySelector('em')?.textContent).toBe('·');
  });

  it('renders the logomark', () => {
    const { container } = render(<Brand />);
    expect(container.querySelector('svg')).toBeTruthy();
  });
});
```

- [ ] **Step 6: Implement `Brand.tsx`**

```tsx
import { Logomark } from './Logomark';

export function Brand({ size = 26 }: { size?: number }) {
  return (
    <div className="lv-brand">
      <Logomark size={size} className="lv-brand-mark" />
      <div className="lv-brand-name">
        Longue<em>·</em>vue
      </div>
    </div>
  );
}
```

- [ ] **Step 7: Run both tests**

Run: `npm --prefix ui run test -- --run src/components/lv/`
Expected: 4 passing across the two files.

- [ ] **Step 8: Commit**

```bash
git add ui/src/components/lv/Logomark.tsx ui/src/components/lv/Logomark.test.tsx \
        ui/src/components/lv/Brand.tsx ui/src/components/lv/Brand.test.tsx
git -c commit.gpgsign=false commit -m "feat(ui): add Logomark and Brand primitives"
```

---

## Task 4: Pill primitive (existing class, new modifiers)

**Files:**
- Create: `ui/src/components/lv/Pill.tsx`
- Create: `ui/src/components/lv/Pill.test.tsx`

- [ ] **Step 1: Write the Pill test**

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Pill } from './Pill';

describe('Pill', () => {
  it('renders <span class="pill"> with no modifier when no status given', () => {
    const { container } = render(<Pill>hello</Pill>);
    const el = container.querySelector('span.pill');
    expect(el?.className.trim()).toBe('pill');
    expect(el?.textContent).toBe('hello');
  });

  it.each(['ok', 'warn', 'bad', 'accent'] as const)('adds the %s status modifier', (status) => {
    const { container } = render(<Pill status={status}>x</Pill>);
    const el = container.querySelector('span.pill');
    expect(el?.classList.contains(status)).toBe(true);
  });

  it('passes className through', () => {
    const { container } = render(<Pill className="extra">x</Pill>);
    expect(container.querySelector('span.pill')?.classList.contains('extra')).toBe(true);
  });
});
```

- [ ] **Step 2: Run — expect FAIL**

Run: `npm --prefix ui run test -- --run src/components/lv/Pill.test.tsx`

- [ ] **Step 3: Implement Pill.tsx**

```tsx
import type { ReactNode } from 'react';

export type PillStatus = 'ok' | 'warn' | 'bad' | 'accent';

export function Pill({
  status,
  className,
  children,
}: {
  status?: PillStatus;
  className?: string;
  children: ReactNode;
}) {
  const cls = ['pill', status, className].filter(Boolean).join(' ');
  return <span className={cls}>{children}</span>;
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `npm --prefix ui run test -- --run src/components/lv/Pill.test.tsx`
Expected: 6 passing.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/lv/Pill.tsx ui/src/components/lv/Pill.test.tsx
git -c commit.gpgsign=false commit -m "feat(ui): add Pill primitive (existing class, ok/warn/bad/accent)"
```

---

## Task 5: KvList, Section, Breadcrumb (existing-class wrappers)

**Files:**
- Create: `ui/src/components/lv/KvList.tsx`, `KvList.test.tsx`
- Create: `ui/src/components/lv/Section.tsx`, `Section.test.tsx`
- Create: `ui/src/components/lv/Breadcrumb.tsx`, `Breadcrumb.test.tsx`

- [ ] **Step 1: KvList test**

`ui/src/components/lv/KvList.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { KvList } from './KvList';

describe('KvList', () => {
  it('renders dt/dd pairs in a kv-list dl', () => {
    const { container } = render(
      <KvList items={[['name', 'frodo'], ['region', 'shire']]} />,
    );
    const dl = container.querySelector('dl.kv-list')!;
    expect(dl).toBeTruthy();
    const dts = dl.querySelectorAll('dt');
    const dds = dl.querySelectorAll('dd');
    expect(dts.length).toBe(2);
    expect(dds.length).toBe(2);
    expect(dts[0].textContent).toBe('name');
    expect(dds[1].textContent).toBe('shire');
  });

  it('renders nothing for an empty items array', () => {
    const { container } = render(<KvList items={[]} />);
    expect(container.querySelector('dt')).toBeNull();
  });
});
```

- [ ] **Step 2: KvList implementation**

`ui/src/components/lv/KvList.tsx`:

```tsx
import { Fragment, type ReactNode } from 'react';

export function KvList({ items, className }: { items: Array<[ReactNode, ReactNode]>; className?: string }) {
  return (
    <dl className={['kv-list', className].filter(Boolean).join(' ')}>
      {items.map(([k, v], i) => (
        <Fragment key={i}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}
```

- [ ] **Step 3: Section test**

`ui/src/components/lv/Section.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Section } from './Section';

describe('Section', () => {
  it('renders an h3.section-title with title and rule', () => {
    const { container } = render(<Section>Pods</Section>);
    const h3 = container.querySelector('h3.section-title')!;
    expect(h3.textContent).toContain('Pods');
    expect(h3.querySelector('.section-rule')).toBeTruthy();
  });

  it('renders a count when given', () => {
    const { container } = render(<Section count={12}>Pods</Section>);
    expect(container.querySelector('h3.section-title .count')?.textContent).toBe('· 12');
  });

  it('omits count span when count is undefined', () => {
    const { container } = render(<Section>Pods</Section>);
    expect(container.querySelector('h3.section-title .count')).toBeNull();
  });
});
```

- [ ] **Step 4: Section implementation**

`ui/src/components/lv/Section.tsx`:

```tsx
import type { ReactNode } from 'react';

export function Section({ children, count, className }: { children: ReactNode; count?: number; className?: string }) {
  return (
    <h3 className={['section-title', className].filter(Boolean).join(' ')}>
      <span>{children}</span>
      {count !== undefined && <span className="count">· {count}</span>}
      <span className="section-rule" />
    </h3>
  );
}
```

- [ ] **Step 5: Breadcrumb test**

`ui/src/components/lv/Breadcrumb.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { renderWithRouter } from '../../test/render';
import { Breadcrumb } from './Breadcrumb';

describe('Breadcrumb', () => {
  it('renders parts separated by /', () => {
    const { container } = renderWithRouter(
      <Breadcrumb parts={[{ label: 'Clusters', to: '/clusters' }, { label: 'prod-eu' }]} />,
    );
    const root = container.querySelector('.breadcrumb')!;
    expect(root.textContent).toContain('Clusters');
    expect(root.textContent).toContain('prod-eu');
    expect(root.querySelectorAll('.breadcrumb-sep').length).toBe(1);
  });

  it('renders parts with `to` as links', () => {
    const { container } = renderWithRouter(
      <Breadcrumb parts={[{ label: 'Home', to: '/' }, { label: 'Now' }]} />,
    );
    const links = container.querySelectorAll('a');
    expect(links.length).toBe(1);
    expect(links[0].getAttribute('href')).toBe('/');
  });
});
```

- [ ] **Step 6: Breadcrumb implementation**

`ui/src/components/lv/Breadcrumb.tsx`:

```tsx
import { Fragment } from 'react';
import { Link } from 'react-router-dom';

export type BreadcrumbPart = { label: string; to?: string };

export function Breadcrumb({ parts, className }: { parts: BreadcrumbPart[]; className?: string }) {
  return (
    <div className={['breadcrumb', className].filter(Boolean).join(' ')}>
      {parts.map((p, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="breadcrumb-sep">/</span>}
          {p.to ? <Link to={p.to}>{p.label}</Link> : <span>{p.label}</span>}
        </Fragment>
      ))}
    </div>
  );
}
```

- [ ] **Step 7: Run all three test files**

Run: `npm --prefix ui run test -- --run src/components/lv/KvList.test.tsx src/components/lv/Section.test.tsx src/components/lv/Breadcrumb.test.tsx`
Expected: 8 passing.

- [ ] **Step 8: Commit**

```bash
git add ui/src/components/lv/KvList.* ui/src/components/lv/Section.* ui/src/components/lv/Breadcrumb.*
git -c commit.gpgsign=false commit -m "feat(ui): add KvList, Section, Breadcrumb primitives"
```

---

## Task 6: Callout, StatRow, PageHead (net-new lv-* primitives)

**Files:**
- Create: `ui/src/components/lv/Callout.tsx`, `Callout.test.tsx`
- Create: `ui/src/components/lv/StatRow.tsx`, `StatRow.test.tsx`
- Create: `ui/src/components/lv/PageHead.tsx`, `PageHead.test.tsx`

- [ ] **Step 1: Callout test**

`ui/src/components/lv/Callout.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { Callout } from './Callout';

describe('Callout', () => {
  it('renders a div.lv-callout with title and body', () => {
    const { container } = render(<Callout title="Heads up">body text</Callout>);
    const root = container.querySelector('.lv-callout')!;
    expect(root.querySelector('strong')?.textContent).toBe('Heads up');
    expect(root.textContent).toContain('body text');
  });

  it.each(['warn', 'bad', 'ok'] as const)('adds the %s tone modifier', (status) => {
    const { container } = render(<Callout title="x" status={status}>y</Callout>);
    expect(container.querySelector('.lv-callout')?.classList.contains(status)).toBe(true);
  });

  it('renders without a body', () => {
    const { container } = render(<Callout title="title-only" />);
    expect(container.querySelector('.lv-callout strong')?.textContent).toBe('title-only');
  });
});
```

- [ ] **Step 2: Callout implementation**

`ui/src/components/lv/Callout.tsx`:

```tsx
import type { ReactNode } from 'react';

export type CalloutTone = 'ok' | 'warn' | 'bad';

export function Callout({
  title,
  status,
  className,
  children,
}: {
  title: ReactNode;
  status?: CalloutTone;
  className?: string;
  children?: ReactNode;
}) {
  const cls = ['lv-callout', status, className].filter(Boolean).join(' ');
  return (
    <div className={cls}>
      <strong>{title}</strong>
      {children !== undefined && children !== null && <> — {children}</>}
    </div>
  );
}
```

- [ ] **Step 3: StatRow test**

`ui/src/components/lv/StatRow.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { StatRow, Stat } from './StatRow';

describe('StatRow', () => {
  it('renders children inside .lv-stat-row', () => {
    const { container } = render(
      <StatRow>
        <Stat label="Pods" value={42} />
        <Stat label="Up" value="100%" tone="ok" meta="2/2" />
      </StatRow>,
    );
    expect(container.querySelector('.lv-stat-row')).toBeTruthy();
    expect(container.querySelectorAll('.lv-stat').length).toBe(2);
  });

  it('renders label/value/meta and applies tone class', () => {
    const { container } = render(<Stat label="Drift" value="3" tone="bad" meta="last 1h" />);
    expect(container.querySelector('.lv-stat-label')?.textContent).toBe('Drift');
    const v = container.querySelector('.lv-stat-value')!;
    expect(v.textContent).toBe('3');
    expect(v.classList.contains('bad')).toBe(true);
    expect(container.querySelector('.lv-stat-meta')?.textContent).toBe('last 1h');
  });

  it('omits meta when not given', () => {
    const { container } = render(<Stat label="x" value="1" />);
    expect(container.querySelector('.lv-stat-meta')).toBeNull();
  });
});
```

- [ ] **Step 4: StatRow implementation**

`ui/src/components/lv/StatRow.tsx`:

```tsx
import type { ReactNode } from 'react';

export type StatTone = 'accent' | 'ok' | 'warn' | 'bad';

export function StatRow({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={['lv-stat-row', className].filter(Boolean).join(' ')}>{children}</div>;
}

export function Stat({
  label,
  value,
  tone,
  meta,
}: {
  label: string;
  value: ReactNode;
  tone?: StatTone;
  meta?: ReactNode;
}) {
  return (
    <div className="lv-stat">
      <div className="lv-stat-label">{label}</div>
      <div className={['lv-stat-value', tone].filter(Boolean).join(' ')}>{value}</div>
      {meta !== undefined && meta !== null && <div className="lv-stat-meta">{meta}</div>}
    </div>
  );
}
```

- [ ] **Step 5: PageHead test**

`ui/src/components/lv/PageHead.test.tsx`:

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { PageHead } from './PageHead';

describe('PageHead', () => {
  it('renders title in lv-page-title and sub in lv-page-sub', () => {
    const { container } = render(<PageHead title="Clusters" sub="3 active" />);
    expect(container.querySelector('.lv-page-title')?.textContent).toBe('Clusters');
    expect(container.querySelector('.lv-page-sub')?.textContent).toBe('3 active');
  });

  it('renders actions in lv-page-actions', () => {
    const { container } = render(
      <PageHead title="x" actions={<button>save</button>} />,
    );
    expect(container.querySelector('.lv-page-actions button')?.textContent).toBe('save');
  });

  it('omits sub and actions when not given', () => {
    const { container } = render(<PageHead title="x" />);
    expect(container.querySelector('.lv-page-sub')).toBeNull();
    expect(container.querySelector('.lv-page-actions')).toBeNull();
  });
});
```

- [ ] **Step 6: PageHead implementation**

`ui/src/components/lv/PageHead.tsx`:

```tsx
import type { ReactNode } from 'react';

export function PageHead({
  title,
  sub,
  actions,
  className,
}: {
  title: ReactNode;
  sub?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <header className={['lv-page-head', className].filter(Boolean).join(' ')}>
      <div>
        <h1 className="lv-page-title">{title}</h1>
        {sub !== undefined && sub !== null && <div className="lv-page-sub">{sub}</div>}
      </div>
      {actions !== undefined && actions !== null && <div className="lv-page-actions">{actions}</div>}
    </header>
  );
}
```

- [ ] **Step 7: Run all three**

Run: `npm --prefix ui run test -- --run src/components/lv/Callout.test.tsx src/components/lv/StatRow.test.tsx src/components/lv/PageHead.test.tsx`
Expected: 9 passing.

- [ ] **Step 8: Commit**

```bash
git add ui/src/components/lv/Callout.* ui/src/components/lv/StatRow.* ui/src/components/lv/PageHead.*
git -c commit.gpgsign=false commit -m "feat(ui): add Callout, StatRow, PageHead primitives"
```

---

## Task 7: Tabs primitive

**Files:**
- Create: `ui/src/components/lv/Tabs.tsx`, `Tabs.test.tsx`

- [ ] **Step 1: Tabs test**

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { Tabs } from './Tabs';

const items = [
  { key: 'overview', label: 'Overview' },
  { key: 'workloads', label: 'Workloads' },
];

describe('Tabs', () => {
  it('renders all tabs and marks the active one', () => {
    const { container } = render(<Tabs items={items} active="workloads" onChange={() => {}} />);
    const tabs = container.querySelectorAll('button.lv-tab');
    expect(tabs.length).toBe(2);
    expect(tabs[1].classList.contains('active')).toBe(true);
    expect(tabs[0].classList.contains('active')).toBe(false);
  });

  it('calls onChange with the clicked key', () => {
    const onChange = vi.fn();
    const { getByText } = render(<Tabs items={items} active="overview" onChange={onChange} />);
    fireEvent.click(getByText('Workloads'));
    expect(onChange).toHaveBeenCalledWith('workloads');
  });

  it('exposes role="tab" for a11y', () => {
    const { container } = render(<Tabs items={items} active="overview" onChange={() => {}} />);
    const tabs = container.querySelectorAll('[role="tab"]');
    expect(tabs.length).toBe(2);
    expect(tabs[0].getAttribute('aria-selected')).toBe('true');
    expect(tabs[1].getAttribute('aria-selected')).toBe('false');
  });
});
```

- [ ] **Step 2: Tabs implementation**

```tsx
export type TabItem = { key: string; label: string };

export function Tabs({
  items,
  active,
  onChange,
  className,
}: {
  items: TabItem[];
  active: string;
  onChange: (key: string) => void;
  className?: string;
}) {
  return (
    <div role="tablist" className={['lv-tabs', className].filter(Boolean).join(' ')}>
      {items.map((it) => {
        const isActive = it.key === active;
        return (
          <button
            key={it.key}
            type="button"
            role="tab"
            aria-selected={isActive}
            className={['lv-tab', isActive && 'active'].filter(Boolean).join(' ')}
            onClick={() => onChange(it.key)}
          >
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/components/lv/Tabs.test.tsx
git add ui/src/components/lv/Tabs.* && git -c commit.gpgsign=false commit -m "feat(ui): add Tabs primitive"
```

---

## Task 8: EolCard primitive

**Files:**
- Create: `ui/src/components/lv/EolCard.tsx`, `EolCard.test.tsx`

- [ ] **Step 1: EolCard test**

```tsx
import { describe, it, expect, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { EolCard } from './EolCard';

describe('EolCard', () => {
  it('renders count, label, meta with the right modifier class', () => {
    const { container } = render(
      <EolCard status="bad" count={3} label="Past EOL" meta="critical" />,
    );
    const card = container.querySelector('.eol-summary-card')!;
    expect(card.classList.contains('eol-bad')).toBe(true);
    expect(card.classList.contains('bad')).toBe(true);
    expect(card.querySelector('.eol-count')?.textContent).toBe('3');
    expect(card.querySelector('.eol-label')?.textContent).toBe('Past EOL');
    expect(card.querySelector('.eol-meta')?.textContent).toBe('critical');
  });

  it('marks .active when active=true', () => {
    const { container } = render(<EolCard status="ok" count={0} label="Safe" meta="" active />);
    expect(container.querySelector('.eol-summary-card')?.classList.contains('active')).toBe(true);
  });

  it('fires onClick when clicked', () => {
    const onClick = vi.fn();
    const { getByRole } = render(<EolCard status="warn" count={1} label="x" meta="y" onClick={onClick} />);
    fireEvent.click(getByRole('button'));
    expect(onClick).toHaveBeenCalled();
  });

  it('renders a non-button div when no onClick is given', () => {
    const { container } = render(<EolCard status="ok" count={0} label="x" meta="y" />);
    expect(container.querySelector('button')).toBeNull();
    expect(container.querySelector('div.eol-summary-card')).toBeTruthy();
  });
});
```

- [ ] **Step 2: EolCard implementation**

```tsx
export type EolStatus = 'ok' | 'warn' | 'bad';

export function EolCard({
  status,
  count,
  label,
  meta,
  active,
  onClick,
  className,
}: {
  status: EolStatus;
  count: number;
  label: string;
  meta: string;
  active?: boolean;
  onClick?: () => void;
  className?: string;
}) {
  const cls = [
    'eol-summary-card',
    status,
    `eol-${status}`,
    active && 'active',
    className,
  ].filter(Boolean).join(' ');
  const body = (
    <>
      <div className="eol-count">{count}</div>
      <div className="eol-label">{label}</div>
      <div className="eol-meta">{meta}</div>
    </>
  );
  return onClick
    ? <button type="button" className={cls} onClick={onClick} aria-pressed={active}>{body}</button>
    : <div className={cls}>{body}</div>;
}
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/components/lv/EolCard.test.tsx
git add ui/src/components/lv/EolCard.* && git -c commit.gpgsign=false commit -m "feat(ui): add EolCard primitive"
```

---

## Task 9: AuditRow primitive

**Files:**
- Create: `ui/src/components/lv/AuditRow.tsx`, `AuditRow.test.tsx`

- [ ] **Step 1: Test**

```tsx
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import { AuditRow } from './AuditRow';

describe('AuditRow', () => {
  it('renders all four columns', () => {
    const { container } = render(
      <AuditRow time="2026-05-01 09:14" actor="alice" message="updated cluster prod" result="ok" />,
    );
    const row = container.querySelector('.lv-audit-row')!;
    expect(row.querySelector('.lv-audit-time')?.textContent).toBe('2026-05-01 09:14');
    expect(row.querySelector('.lv-audit-actor')?.textContent).toBe('alice');
    expect(row.querySelector('.lv-audit-msg')?.textContent).toBe('updated cluster prod');
    expect(row.querySelector('.lv-audit-result')?.textContent).toBe('ok');
  });
});
```

- [ ] **Step 2: Implementation**

```tsx
import type { ReactNode } from 'react';

export function AuditRow({
  time,
  actor,
  message,
  result,
  className,
}: {
  time: string;
  actor: string;
  message: ReactNode;
  result: ReactNode;
  className?: string;
}) {
  return (
    <div className={['lv-audit-row', className].filter(Boolean).join(' ')}>
      <div className="lv-audit-time">{time}</div>
      <div className="lv-audit-actor">{actor}</div>
      <div className="lv-audit-msg">{message}</div>
      <div className="lv-audit-result">{result}</div>
    </div>
  );
}
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/components/lv/AuditRow.test.tsx
git add ui/src/components/lv/AuditRow.* && git -c commit.gpgsign=false commit -m "feat(ui): add AuditRow primitive"
```

---

## Task 10: Disclosure (headless reusable popover)

**Files:**
- Create: `ui/src/components/lv/Disclosure.tsx`, `Disclosure.test.tsx`

This is reused by both "More ▾" and the user menu. Closes on outside click, ESC, route change.

- [ ] **Step 1: Test**

```tsx
import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/react';
import { renderWithRouter } from '../../test/render';
import { Disclosure } from './Disclosure';

const Trigger = ({ open, toggle }: { open: boolean; toggle: () => void }) => (
  <button onClick={toggle} aria-expanded={open}>Menu</button>
);
const Body = () => <div data-testid="popover">items</div>;

describe('Disclosure', () => {
  it('starts closed', () => {
    const { queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    expect(queryByTestId('popover')).toBeNull();
  });

  it('opens on trigger click', () => {
    const { getByText, queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    fireEvent.click(getByText('Menu'));
    expect(queryByTestId('popover')).not.toBeNull();
  });

  it('closes on outside click', () => {
    const { getByText, queryByTestId } = renderWithRouter(
      <div>
        <Disclosure trigger={Trigger}>{Body}</Disclosure>
        <span data-testid="outside">x</span>
      </div>,
    );
    fireEvent.click(getByText('Menu'));
    expect(queryByTestId('popover')).not.toBeNull();
    fireEvent.mouseDown(document.querySelector('[data-testid="outside"]')!);
    expect(queryByTestId('popover')).toBeNull();
  });

  it('closes on ESC', () => {
    const { getByText, queryByTestId } = renderWithRouter(<Disclosure trigger={Trigger}>{Body}</Disclosure>);
    fireEvent.click(getByText('Menu'));
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(queryByTestId('popover')).toBeNull();
  });

  it('exposes a render prop for closing programmatically', () => {
    const { getByText, queryByTestId } = renderWithRouter(
      <Disclosure trigger={Trigger}>
        {({ close }) => <button onClick={close} data-testid="popover">close-me</button>}
      </Disclosure>,
    );
    fireEvent.click(getByText('Menu'));
    fireEvent.click(getByText('close-me'));
    expect(queryByTestId('popover')).toBeNull();
  });
});
```

- [ ] **Step 2: Implementation**

```tsx
import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';

type TriggerProps = { open: boolean; toggle: () => void };
type BodyProps = { close: () => void };

export function Disclosure({
  trigger: Trigger,
  children,
  side = 'right',
}: {
  trigger: (p: TriggerProps) => ReactNode;
  children: ((p: BodyProps) => ReactNode) | ReactNode;
  side?: 'left' | 'right';
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLSpanElement | null>(null);
  const location = useLocation();
  const close = () => setOpen(false);
  const toggle = () => setOpen((o) => !o);

  useEffect(() => { setOpen(false); }, [location.pathname]);

  useEffect(() => {
    if (!open) return;
    const onDocDown = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setOpen(false); };
    document.addEventListener('mousedown', onDocDown);
    window.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDocDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <span className="lv-popover-relative" ref={wrapRef}>
      {Trigger({ open, toggle })}
      {open && (
        <div className="lv-popover" data-side={side}>
          {typeof children === 'function' ? (children as (p: BodyProps) => ReactNode)({ close }) : children}
        </div>
      )}
    </span>
  );
}
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/components/lv/Disclosure.test.tsx
git add ui/src/components/lv/Disclosure.* && git -c commit.gpgsign=false commit -m "feat(ui): add Disclosure primitive (popover with outside-click/ESC/route-change close)"
```

---

## Task 11: UiPrefsPanel primitive

**Files:**
- Create: `ui/src/components/lv/UiPrefsPanel.tsx`, `UiPrefsPanel.test.tsx`

- [ ] **Step 1: Test**

```tsx
import { describe, it, expect } from 'vitest';
import { render, fireEvent } from '@testing-library/react';
import { UiPrefsProvider } from '../../ui-prefs';
import { UiPrefsPanel } from './UiPrefsPanel';

describe('UiPrefsPanel', () => {
  it('renders three sections with radios', () => {
    const { container } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    const radios = container.querySelectorAll('input[type="radio"]');
    // 5 accents + 3 densities + 3 pill styles
    expect(radios.length).toBe(11);
  });

  it('clicking a radio updates body dataset', () => {
    const { getByLabelText } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    fireEvent.click(getByLabelText('amber'));
    expect(document.body.dataset.accent).toBe('amber');
  });

  it('marks the current pref as checked', () => {
    localStorage.setItem('lv:ui-prefs', JSON.stringify({ accent: 'sage', density: 'standard', pillStyle: 'dot' }));
    const { getByLabelText } = render(<UiPrefsProvider><UiPrefsPanel /></UiPrefsProvider>);
    expect((getByLabelText('sage') as HTMLInputElement).checked).toBe(true);
    expect((getByLabelText('dot') as HTMLInputElement).checked).toBe(true);
  });
});
```

- [ ] **Step 2: Implementation**

```tsx
import { useUiPrefs, type Accent, type Density, type PillStyle } from '../../ui-prefs';

const ACCENTS: Accent[] = ['cyan', 'amber', 'sage', 'coral', 'violet'];
const DENSITIES: Density[] = ['compact', 'standard', 'comfortable'];
const PILL_STYLES: PillStyle[] = ['solid', 'outline', 'dot'];

export function UiPrefsPanel() {
  const { prefs, setPref } = useUiPrefs();
  return (
    <div>
      <div className="lv-popover-section-label">Accent</div>
      {ACCENTS.map((a) => (
        <label key={a} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-accent"
            value={a}
            checked={prefs.accent === a}
            onChange={() => setPref('accent', a)}
          />
          {a}
        </label>
      ))}
      <div className="lv-popover-divider" />
      <div className="lv-popover-section-label">Density</div>
      {DENSITIES.map((d) => (
        <label key={d} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-density"
            value={d}
            checked={prefs.density === d}
            onChange={() => setPref('density', d)}
          />
          {d}
        </label>
      ))}
      <div className="lv-popover-divider" />
      <div className="lv-popover-section-label">Pill style</div>
      {PILL_STYLES.map((p) => (
        <label key={p} className="lv-popover-item" style={{ display: 'flex', alignItems: 'center', gap: '0.4rem' }}>
          <input
            type="radio"
            name="lv-pill-style"
            value={p}
            checked={prefs.pillStyle === p}
            onChange={() => setPref('pillStyle', p)}
          />
          {p}
        </label>
      ))}
    </div>
  );
}
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/components/lv/UiPrefsPanel.test.tsx
git add ui/src/components/lv/UiPrefsPanel.* && git -c commit.gpgsign=false commit -m "feat(ui): add UiPrefsPanel primitive"
```

---

## Task 12: Replace Chrome with top-bar layout

**Files:**
- Modify: `ui/src/App.tsx` — replace `Chrome` component (lines ~100–171); routes block stays.
- Create: `ui/src/App.chrome.test.tsx`

- [ ] **Step 1: Write `App.chrome.test.tsx` first**

```tsx
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithRouter } from './test/render';
import App from './App';
import * as api from './api';
import { server } from './test/server';
import { http, HttpResponse } from 'msw';
import type { Me } from './api';

const adminMe: Me = { id: 'u1', username: 'alice', role: 'admin', kind: 'user', must_change_password: false };
const auditorMe: Me = { id: 'u2', username: 'bob', role: 'auditor', kind: 'user', must_change_password: false };
const viewerMe: Me = { id: 'u3', username: 'carol', role: 'viewer', kind: 'user', must_change_password: false };

function mockMe(me: Me) {
  server.use(http.get('/v1/auth/me', () => HttpResponse.json(me)));
}

beforeEach(() => { localStorage.clear(); });
afterEach(() => { vi.restoreAllMocks(); });

describe('Chrome (top-bar)', () => {
  it('renders all primary nav links for an admin', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.getByRole('link', { name: 'Clusters' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Workloads' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Nodes' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Virtual Machines' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Lifecycle' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Search' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Audit' })).toBeTruthy();
  });

  it('hides Audit for viewer', async () => {
    mockMe(viewerMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Clusters' });
    expect(screen.queryByRole('link', { name: 'Audit' })).toBeNull();
  });

  it('shows Audit for auditor', async () => {
    mockMe(auditorMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    await screen.findByRole('link', { name: 'Audit' });
    expect(screen.getByRole('link', { name: 'Audit' })).toBeTruthy();
  });

  it('marks Clusters active on /clusters/:id', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters/c-123' });
    const link = await screen.findByRole('link', { name: 'Clusters' });
    expect(link.classList.contains('active')).toBe(true);
  });

  it('opens "More" dropdown and lists overflow routes', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    const moreBtn = await screen.findByRole('button', { name: /more/i });
    fireEvent.click(moreBtn);
    expect(screen.getByRole('link', { name: 'Namespaces' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Pods' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Services' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Ingresses' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'PVs' })).toBeTruthy();
    expect(screen.getByRole('link', { name: 'PVCs' })).toBeTruthy();
  });

  it('opens user menu and signs out', async () => {
    mockMe(adminMe);
    const logoutSpy = vi.spyOn(api, 'logout').mockResolvedValue(undefined as unknown as void);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    const userBtn = await screen.findByRole('button', { name: /alice/i });
    fireEvent.click(userBtn);
    fireEvent.click(screen.getByRole('button', { name: /sign out/i }));
    await waitFor(() => expect(logoutSpy).toHaveBeenCalled());
  });

  it('user menu sets body data-accent when accent radio clicked', async () => {
    mockMe(adminMe);
    renderWithRouter(<App />, { initialPath: '/clusters' });
    const userBtn = await screen.findByRole('button', { name: /alice/i });
    fireEvent.click(userBtn);
    fireEvent.click(screen.getByLabelText('amber'));
    expect(document.body.dataset.accent).toBe('amber');
  });
});
```

- [ ] **Step 2: Run — expect FAIL**

Run: `npm --prefix ui run test -- --run src/App.chrome.test.tsx`
Expected: failures because the new chrome doesn't exist yet.

- [ ] **Step 3: Replace `Chrome` in `ui/src/App.tsx`**

Open `ui/src/App.tsx`. Keep the imports for routing/auth/pages/icons; remove imports tied to the old sidebar (the icon imports are no longer needed in `App.tsx`, but leave them — they may be referenced by pages later. If `npm run typecheck` flags them as unused, remove them in this same step).

Replace the entire `// --- chrome` section (the `Chrome` function, ~lines 100–171) with:

```tsx
// --- chrome -------------------------------------------------------------

import { Brand } from './components/lv/Brand';
import { Pill } from './components/lv/Pill';
import { Disclosure } from './components/lv/Disclosure';
import { UiPrefsProvider } from './ui-prefs';
import { UiPrefsPanel } from './components/lv/UiPrefsPanel';

const PRIMARY_NAV: Array<{ to: string; label: string; roles?: api.Me['role'][] }> = [
  { to: '/clusters',         label: 'Clusters' },
  { to: '/workloads',        label: 'Workloads' },
  { to: '/nodes',            label: 'Nodes' },
  { to: '/virtual-machines', label: 'Virtual Machines' },
  { to: '/eol',              label: 'Lifecycle' },
  { to: '/search/image',     label: 'Search' },
];

const MORE_ITEMS: Array<{ to: string; label: string }> = [
  { to: '/namespaces',             label: 'Namespaces' },
  { to: '/pods',                   label: 'Pods' },
  { to: '/services',               label: 'Services' },
  { to: '/ingresses',              label: 'Ingresses' },
  { to: '/persistentvolumes',      label: 'PVs' },
  { to: '/persistentvolumeclaims', label: 'PVCs' },
];

function pathPrefix(pathname: string): string {
  const m = /^\/[^/]+/.exec(pathname);
  return m ? m[0] : '/';
}

function Chrome({ me, children }: { me: api.Me; children: React.ReactNode }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const id = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(id);
  }, []);

  const ts = now.toISOString().replace('T', ' ').slice(0, 16) + ' UTC';
  const activePrefix = pathPrefix(location.pathname);

  const signOut = async () => {
    try { await api.logout(); } catch { /* ignore */ }
    navigate('/login', { replace: true });
  };

  const adminEntry = me.role === 'admin'
    ? { to: '/admin/users', label: 'Admin' }
    : me.role === 'auditor'
    ? { to: '/admin/audit', label: 'Admin' }
    : null;

  const auditNavEntry = (me.role === 'admin' || me.role === 'auditor');

  return (
    <div className="lv-app">
      <header className="lv-header">
        <Brand />
        <nav className="lv-nav">
          {PRIMARY_NAV.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              className={() => `lv-nav-link ${pathPrefix(it.to) === activePrefix ? 'active' : ''}`}
            >
              {it.label}
            </NavLink>
          ))}
          <Disclosure
            trigger={({ open, toggle }) => (
              <button
                type="button"
                className={`lv-nav-link${open ? ' active' : ''}`}
                aria-haspopup="menu"
                aria-expanded={open}
                onClick={toggle}
              >
                More ▾
              </button>
            )}
          >
            {({ close }) => (
              <div role="menu">
                {MORE_ITEMS.map((it) => (
                  <NavLink
                    key={it.to}
                    to={it.to}
                    role="menuitem"
                    className="lv-popover-item"
                    onClick={close}
                  >
                    {it.label}
                  </NavLink>
                ))}
              </div>
            )}
          </Disclosure>
          {auditNavEntry && (
            <NavLink
              to="/admin/audit"
              className={() => `lv-nav-link ${activePrefix === '/admin' ? 'active' : ''}`}
            >
              Audit
            </NavLink>
          )}
        </nav>
        <div className="lv-header-right">
          <span className="lv-time" aria-label={`polled ${ts}`}>
            <span className="lv-time-dot" />
            polled {ts}
          </span>
          <Disclosure
            trigger={({ open, toggle }) => (
              <button
                type="button"
                className="lv-iconbtn"
                aria-haspopup="menu"
                aria-expanded={open}
                onClick={toggle}
              >
                <span className="lv-user">
                  <span>{me.username}</span>
                  <Pill status="accent">{me.role}</Pill>
                </span>
              </button>
            )}
          >
            {({ close }) => (
              <div role="menu">
                <div className="lv-popover-section-label">Signed in as {me.username}</div>
                <div className="lv-popover-divider" />
                <UiPrefsPanel />
                {adminEntry && (
                  <>
                    <div className="lv-popover-divider" />
                    <NavLink to={adminEntry.to} className="lv-popover-item" onClick={close}>
                      {adminEntry.label}
                    </NavLink>
                  </>
                )}
                <div className="lv-popover-divider" />
                <button type="button" className="lv-popover-item" onClick={() => { close(); signOut(); }}>
                  Sign out
                </button>
              </div>
            )}
          </Disclosure>
        </div>
      </header>
      <main className="lv-main">{children}</main>
    </div>
  );
}
```

Then update the `authed` helper inside `App` to wrap `Chrome` in `<UiPrefsProvider>`:

```tsx
const authed = (el: React.ReactNode) => (
  <RequireAuth auth={auth}>
    {auth.status === 'ready' && (
      <MeProvider value={auth.me}>
        <UiPrefsProvider>
          <Chrome me={auth.me}>{el}</Chrome>
        </UiPrefsProvider>
      </MeProvider>
    )}
  </RequireAuth>
);
```

Remove the unused imports `useCallback` and the icon imports (`ClusterIcon, NamespaceIcon, …`) — typecheck will catch them.

- [ ] **Step 4: Run typecheck**

Run: `npm --prefix ui run typecheck`
Expected: clean. If it complains about unused imports, remove them.

- [ ] **Step 5: Run the chrome test**

Run: `npm --prefix ui run test -- --run src/App.chrome.test.tsx`
Expected: PASS (8 tests).

If any test fails: typical issue is the role-aware "Audit" filter. Re-check the `auditNavEntry` boolean and the Audit `NavLink` block. Another typical issue: the user-menu radio test fails if the `<UiPrefsPanel>` is not yet rendered — confirm the disclosure body renders eagerly when `open=true`.

- [ ] **Step 6: Run a smoke build**

Run: `npm --prefix ui run build`
Expected: bundle succeeds. Existing pages render the new chrome around them; their content still uses old class names (untouched in this task).

- [ ] **Step 7: Commit**

```bash
git add ui/src/App.tsx ui/src/App.chrome.test.tsx
git -c commit.gpgsign=false commit -m "feat(ui): replace Chrome with top-bar nav, More overflow, user menu"
```

---

## Task 13: Restyle Login + ChangePassword

**Files:**
- Modify: `ui/src/pages/Login.tsx`
- Modify: `ui/src/pages/ChangePassword.tsx`
- Modify: `ui/src/pages/Login.test.tsx` (only if existing assertions break)
- Modify: `ui/src/pages/ChangePassword.test.tsx` (same condition)

- [ ] **Step 1: Read the current Login**

Run: `cat ui/src/pages/Login.tsx | head -60`

Note the surface area: the form, OIDC button (gated by `auth/config`), error banner, forced-rotation note. None of this changes — only the surrounding markup.

- [ ] **Step 2: Rewrite Login**

Wrap the existing form contents in:

```tsx
import { LogomarkLarge } from '../components/lv/Logomark';

// at the top of the rendered tree:
<div className="lv-login-page">
  <aside className="lv-login-side">
    <LogomarkLarge size={160} className="lv-login-mark" />
    <div className="lv-login-tagline">
      The <em>long view</em> on your Kubernetes estate.
    </div>
    <div className="lv-login-foot">
      <span>longue-vue CMDB</span>
      <span>SecNumCloud-aligned</span>
    </div>
  </aside>
  <section className="lv-login-form-wrap">
    <div className="lv-login-form">
      <h2>Sign in</h2>
      {/* existing form JSX, including error banner, "Forgot password" link if any, OIDC divider */}
      {/* OIDC button (when configured) sits below: */}
      <div className="lv-login-divider">or</div>
      {/* OIDC button JSX */}
    </div>
  </section>
</div>
```

Replace any forced-rotation banner with `<Callout title="Password rotation required" status="warn">…</Callout>`.

Run the existing Login test to confirm it still locates the username/password inputs by label or placeholder. If a test asserts on a wrapping class like `.login-card`, swap to `.lv-login-form` or query by role.

- [ ] **Step 3: Rewrite ChangePassword**

Wrap existing inputs in:

```tsx
<div style={{ display: 'flex', justifyContent: 'center', padding: '3rem 1.5rem' }}>
  <div className="lv-card" style={{ width: '100%', maxWidth: 460 }}>
    <PageHead title="Change password" sub={forced ? 'You must rotate before continuing.' : undefined} />
    {forced && <Callout title="Rotation required" status="warn">Your administrator requires you to set a new password.</Callout>}
    {/* existing form JSX */}
  </div>
</div>
```

Imports: `import { PageHead } from '../components/lv/PageHead';` and `import { Callout } from '../components/lv/Callout';`.

- [ ] **Step 4: Run page tests**

Run: `npm --prefix ui run test -- --run src/pages/Login.test.tsx src/pages/ChangePassword.test.tsx`
Update any selector-only failures (e.g. `getByText('Sign in')` is fine; `container.querySelector('.login-card')` becomes `.lv-login-form`).

- [ ] **Step 5: Commit**

```bash
git add ui/src/pages/Login.tsx ui/src/pages/Login.test.tsx \
        ui/src/pages/ChangePassword.tsx ui/src/pages/ChangePassword.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle Login and ChangePassword to lv-* primitives"
```

---

## Task 14: Restyle Clusters list + ClusterDetail + curated card

**Files:**
- Modify: `ui/src/pages/Lists.tsx` (Clusters list section only)
- Modify: `ui/src/pages/Details.tsx` (ClusterDetail section)
- Modify: `ui/src/pages/cluster_curated.tsx`
- Modify corresponding `*.test.tsx` only if selectors break.

- [ ] **Step 1: Locate the Clusters list export in `Lists.tsx`**

Run: `grep -n 'export function Clusters' ui/src/pages/Lists.tsx`

The function returns the page body. Wrap its top with `<PageHead title="Clusters" sub={…} />` and replace any ad-hoc `<h1>` / filter-row with:

```tsx
import { PageHead } from '../components/lv/PageHead';

return (
  <>
    <PageHead title="Clusters" sub={`${total} active`} />
    <div className="lv-toolbar">
      {/* existing search / filter inputs unchanged */}
    </div>
    <table className="entities">
      {/* existing thead/tbody */}
    </table>
  </>
);
```

If existing JSX has a wrapping `<section>` or `<div className="page">`, remove it — `lv-main` already provides padding.

- [ ] **Step 2: Rewrite ClusterDetail in `Details.tsx`**

Locate `export function ClusterDetail`. Replace the page-shell JSX with:

```tsx
import { Breadcrumb } from '../components/lv/Breadcrumb';
import { PageHead } from '../components/lv/PageHead';
import { StatRow, Stat } from '../components/lv/StatRow';
import { Callout } from '../components/lv/Callout';
import { Tabs } from '../components/lv/Tabs';
import { Pill } from '../components/lv/Pill';
import { KvList } from '../components/lv/KvList';
import { Section } from '../components/lv/Section';
import { useState } from 'react';
// keep existing data-fetching hooks
const [tab, setTab] = useState<'overview' | 'workloads' | 'nodes' | 'history'>('overview');

const eolPill = cluster.eol_status === 'eol'
  ? <Pill status="bad">past EOL</Pill>
  : cluster.eol_status === 'approaching_eol'
  ? <Pill status="warn">approaching EOL</Pill>
  : <Pill status="ok">supported</Pill>;

return (
  <>
    <Breadcrumb parts={[{ label: 'Clusters', to: '/clusters' }, { label: cluster.name }]} />
    <PageHead
      title={cluster.name}
      sub={cluster.kubernetes_version}
      actions={<>
        {eolPill}
        {cluster.criticality && <Pill status="accent">{cluster.criticality}</Pill>}
        {cluster.environment && <Pill>{cluster.environment}</Pill>}
      </>}
    />
    <StatRow>
      <Stat label="Nodes" value={stats.nodes} />
      <Stat label="Namespaces" value={stats.namespaces} />
      <Stat label="Workloads" value={stats.workloads} />
      <Stat label="Pods" value={stats.pods} />
      <Stat label="Drift" value={stats.drift} tone={stats.drift > 0 ? 'warn' : 'ok'} />
    </StatRow>
    <Callout title="Impact analysis">Review what depends on this cluster before any change.</Callout>
    <Tabs
      items={[
        { key: 'overview', label: 'Overview' },
        { key: 'workloads', label: 'Workloads' },
        { key: 'nodes', label: 'Nodes' },
        { key: 'history', label: 'History' },
      ]}
      active={tab}
      onChange={(k) => setTab(k as typeof tab)}
    />
    {tab === 'overview' && (
      <div className="detail-grid">
        <div>
          <KvList items={[
            ['Kubernetes', cluster.kubernetes_version ?? '—'],
            ['Owner', cluster.owner ?? '—'],
            ['Environment', cluster.environment ?? '—'],
            ['Criticality', cluster.criticality ?? '—'],
            ['Runbook', cluster.runbook_url ? <a href={cluster.runbook_url}>{cluster.runbook_url}</a> : '—'],
          ]} />
          <ClusterCuratedCard cluster={cluster} onSaved={refetch} />
        </div>
        <div>
          {/* existing ImpactSection / workloads table — leave as-is */}
        </div>
      </div>
    )}
    {tab === 'workloads' && <WorkloadsTabBody clusterId={cluster.id} />}
    {tab === 'nodes' && <NodesTabBody clusterId={cluster.id} />}
    {tab === 'history' && <Callout title="History" status="ok">No snapshots yet — see ADR-TBD.</Callout>}
  </>
);
```

If the existing detail page has tabs already implemented inline, fold them into the `Tabs` component and remove the old tab markup. If not, add minimal `WorkloadsTabBody`/`NodesTabBody` wrappers around existing inline JSX.

- [ ] **Step 3: Rewrite `cluster_curated.tsx` outer wrapper**

Find the outermost wrapper (`<section>` or `<div>` around the curated form). Change to:

```tsx
<div className="lv-card" style={{ marginTop: '1rem' }}>
  <div className="lv-card-header">
    <h3 className="lv-card-title">Ownership &amp; context</h3>
    {!editing && <button type="button" className="lv-btn lv-btn-ghost" onClick={() => setEditing(true)}>Edit</button>}
  </div>
  {/* existing form / read view body */}
</div>
```

Save buttons: change `<button type="submit">Save</button>` to `<button type="submit" className="lv-btn lv-btn-primary">Save</button>`. Cancel: `lv-btn lv-btn-ghost`.

- [ ] **Step 4: Run page tests**

Run: `npm --prefix ui run test -- --run src/pages/Lists.test.tsx src/pages/Details.test.tsx src/pages/cluster_curated.test.tsx`

If a test queries `container.querySelector('h1')` and now finds `.lv-page-title`, that should still match `h1` selectors (PageHead renders an h1). Selector failures usually involve `.section-title` or `.curated-card` wrapping classes. Adjust the test to use role-based queries (`screen.getByRole('heading', { name: 'Clusters' })`).

- [ ] **Step 5: Commit**

```bash
git add ui/src/pages/Lists.tsx ui/src/pages/Details.tsx ui/src/pages/cluster_curated.tsx \
        ui/src/pages/Lists.test.tsx ui/src/pages/Details.test.tsx ui/src/pages/cluster_curated.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle clusters list/detail and curated card"
```

---

## Task 15: Restyle remaining K8s entity list pages

**Files:**
- Modify: `ui/src/pages/Lists.tsx` (Namespaces, Nodes, Workloads, Pods, Services, Ingresses, PersistentVolumes, PersistentVolumeClaims sections)
- Modify: `ui/src/pages/Lists.test.tsx` only if selectors break

- [ ] **Step 1: For each list export, replace the page shell**

For each of: `Namespaces`, `Nodes`, `Workloads`, `Pods`, `Services`, `Ingresses`, `PersistentVolumes`, `PersistentVolumeClaims`, locate the export and replace its shell with:

```tsx
return (
  <>
    <PageHead title="<Page name>" sub={total !== undefined ? `${total} total` : undefined} />
    <div className="lv-toolbar">
      {/* existing search/filter inputs */}
    </div>
    <table className="entities">
      {/* existing markup */}
    </table>
  </>
);
```

Ensure `import { PageHead } from '../components/lv/PageHead';` is at the top of `Lists.tsx`.

- [ ] **Step 2: Run tests**

Run: `npm --prefix ui run test -- --run src/pages/Lists.test.tsx`
Update selectors that pinned to old wrappers.

- [ ] **Step 3: Commit**

```bash
git add ui/src/pages/Lists.tsx ui/src/pages/Lists.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle K8s entity list pages"
```

---

## Task 16: Restyle remaining K8s entity detail pages

**Files:**
- Modify: `ui/src/pages/Details.tsx` (Namespace, Node, Workload, Pod, Ingress detail sections)
- Modify: `ui/src/pages/namespace_curated.tsx`, `node_curated.tsx`
- Modify: corresponding test files only if selectors break

- [ ] **Step 1: For each `<X>Detail` export**

Replace the page shell with:

```tsx
return (
  <>
    <Breadcrumb parts={[{ label: '<Plural>', to: '/<route>' }, { label: name }]} />
    <PageHead title={name} sub={subtitle} />
    <div className="detail-grid">
      <div>
        <KvList items={kvPairs} />
        {/* curated card if applicable */}
      </div>
      <div>
        {/* sections: containers / events / impact / etc. — keep existing markup, wrap in <Section count={n}>…</Section> when there is a list */}
      </div>
    </div>
  </>
);
```

Imports: `Breadcrumb`, `PageHead`, `KvList`, `Section`.

- [ ] **Step 2: Update curated cards**

`namespace_curated.tsx` and `node_curated.tsx` follow the same pattern as Task 14 Step 3 — `lv-card` wrapper, `lv-btn lv-btn-primary` save, `lv-btn lv-btn-ghost` cancel/edit.

- [ ] **Step 3: Run tests**

Run: `npm --prefix ui run test -- --run src/pages/Details.test.tsx src/pages/namespace_curated.test.tsx src/pages/node_curated.test.tsx`

- [ ] **Step 4: Commit**

```bash
git add ui/src/pages/Details.tsx ui/src/pages/namespace_curated.tsx ui/src/pages/node_curated.tsx \
        ui/src/pages/Details.test.tsx ui/src/pages/namespace_curated.test.tsx ui/src/pages/node_curated.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle K8s entity detail pages"
```

---

## Task 17: Restyle EolDashboard

**Files:**
- Modify: `ui/src/pages/EolDashboard.tsx`
- Modify: `ui/src/pages/EolDashboard.test.tsx` if selectors break

- [ ] **Step 1: Read current EolDashboard**

Run: `grep -n 'eol-summary' ui/src/pages/EolDashboard.tsx`

It already uses `eol-summary` and `eol-summary-card` markup — Task 1's CSS rewrite already styled them. The only change is to swap the inline summary-card markup for the `<EolCard>` primitive, and add `<PageHead>`.

- [ ] **Step 2: Rewrite the page shell**

```tsx
import { PageHead } from '../components/lv/PageHead';
import { EolCard } from '../components/lv/EolCard';

return (
  <>
    <PageHead title="Lifecycle" sub="Kubernetes / nodes / VMs end-of-life inventory." />
    <div className="eol-summary">
      <EolCard
        status="bad"
        count={summary.eol}
        label="Past EOL"
        meta={`${summary.eol_critical ?? 0} critical`}
        active={filter === 'eol'}
        onClick={() => setFilter(filter === 'eol' ? null : 'eol')}
      />
      <EolCard
        status="warn"
        count={summary.approaching}
        label="Approaching EOL"
        meta="next 90 days"
        active={filter === 'approaching_eol'}
        onClick={() => setFilter(filter === 'approaching_eol' ? null : 'approaching_eol')}
      />
      <EolCard
        status="ok"
        count={summary.supported}
        label="Supported"
        meta=""
        active={filter === 'supported'}
        onClick={() => setFilter(filter === 'supported' ? null : 'supported')}
      />
    </div>
    {/* existing filter chips + table unchanged; ensure root table has class="eol-table" */}
  </>
);
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/pages/EolDashboard.test.tsx
git add ui/src/pages/EolDashboard.tsx ui/src/pages/EolDashboard.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle EOL dashboard"
```

---

## Task 18: Restyle VirtualMachines + VirtualMachineDetail

**Files:**
- Modify: `ui/src/pages/VirtualMachines.tsx`
- Modify: `ui/src/pages/VirtualMachineDetail.tsx`
- Modify: corresponding test files if selectors break

- [ ] **Step 1: VirtualMachines list page shell**

Wrap the page top with `<PageHead title="Virtual machines" sub={…} />`. Keep the cascading product/version dropdown markup (`vm-filters` block) intact — its CSS body referenced tokens that have been rewritten and the layout still works. The Search/Clear buttons become `lv-btn` and `lv-btn lv-btn-primary`.

- [ ] **Step 2: VirtualMachineDetail page shell**

Mirror the ClusterDetail pattern: Breadcrumb + PageHead + `detail-grid` + `<KvList>` for identity/cloud/networking, `<Section count={apps.length}>Applications</Section>` followed by a small KvList per application.

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/pages/VirtualMachines.test.tsx src/pages/VirtualMachineDetail.test.tsx
git add ui/src/pages/VirtualMachines.tsx ui/src/pages/VirtualMachineDetail.tsx \
        ui/src/pages/VirtualMachines.test.tsx ui/src/pages/VirtualMachineDetail.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle virtual machines list and detail"
```

---

## Task 19: Restyle Search + ImpactGraph

**Files:**
- Modify: `ui/src/pages/Search.tsx`
- Modify: `ui/src/pages/ImpactGraph.tsx`
- Modify: corresponding test files if selectors break

- [ ] **Step 1: Search page**

```tsx
import { PageHead } from '../components/lv/PageHead';
import { Tabs } from '../components/lv/Tabs';
import { Section } from '../components/lv/Section';
import { useState } from 'react';

const [tab, setTab] = useState<'image' | 'application'>('image');

return (
  <>
    <PageHead title="Search by image or application" sub="Find K8s workloads/pods and platform VMs." />
    <Tabs
      items={[{ key: 'image', label: 'Image' }, { key: 'application', label: 'Application' }]}
      active={tab}
      onChange={(k) => setTab(k as typeof tab)}
    />
    <div className="lv-toolbar">
      {/* existing input(s) for the active tab */}
    </div>
    {tab === 'image' ? (
      <>
        <Section count={k8sResults.length}>Kubernetes</Section>
        {/* existing K8s results table */}
        <Section count={vmResults.length}>Virtual machines</Section>
        {/* existing VM results table */}
      </>
    ) : (
      <>
        {/* application search — same shape */}
      </>
    )}
  </>
);
```

- [ ] **Step 2: ImpactGraph page (route)**

```tsx
return (
  <>
    <PageHead
      title="Impact analysis"
      actions={<Tabs items={[{key:'1',label:'1 hop'},{key:'2',label:'2 hops'},{key:'3',label:'3 hops'}]} active={String(depth)} onChange={(k) => setDepth(Number(k))} />}
    />
    <div className="lv-card">
      {/* existing SVG graph */}
    </div>
  </>
);
```

- [ ] **Step 3: Run + commit**

```
npm --prefix ui run test -- --run src/pages/Search.test.tsx src/pages/ImpactGraph.test.tsx
git add ui/src/pages/Search.tsx ui/src/pages/ImpactGraph.tsx \
        ui/src/pages/Search.test.tsx ui/src/pages/ImpactGraph.test.tsx
git -c commit.gpgsign=false commit -m "refactor(ui): restyle search and impact graph pages"
```

---

## Task 20: Restyle Admin pages and AdminLayout

**Files:**
- Modify: `ui/src/pages/admin/AdminLayout.tsx`
- Modify: `ui/src/pages/admin/Users.tsx`, `Tokens.tsx`, `Sessions.tsx`, `Settings.tsx`, `CloudAccounts.tsx`, `CloudAccountDetail.tsx`
- Modify: `ui/src/pages/admin/Audit.tsx` (uses new `<AuditRow>`)
- Modify: corresponding test files if selectors break

- [ ] **Step 1: AdminLayout**

```tsx
import { PageHead } from '../../components/lv/PageHead';
import { Tabs } from '../../components/lv/Tabs';
import { useLocation, useNavigate, Outlet } from 'react-router-dom';

const TABS = [
  { key: 'users', label: 'Users', roles: ['admin'] },
  { key: 'tokens', label: 'Tokens', roles: ['admin'] },
  { key: 'sessions', label: 'Sessions', roles: ['admin'] },
  { key: 'cloud-accounts', label: 'Cloud accounts', roles: ['admin'] },
  { key: 'audit', label: 'Audit', roles: ['admin', 'auditor'] },
  { key: 'settings', label: 'Settings', roles: ['admin'] },
];

export default function AdminLayout({ role }: { role: string }) {
  const location = useLocation();
  const navigate = useNavigate();
  const visible = TABS.filter((t) => t.roles.includes(role));
  const active = (location.pathname.split('/')[2]) || 'users';
  return (
    <>
      <PageHead title="Admin" />
      <Tabs items={visible.map(({key,label}) => ({key,label}))} active={active} onChange={(k) => navigate(`/admin/${k}`)} />
      <Outlet />
    </>
  );
}
```

Delete the previous `admin-subnav` block. Update its test to query `getByRole('tab', { name: 'Users' })`.

- [ ] **Step 2: Each admin sub-page**

Wrap content in:

```tsx
<div className="lv-card">
  {/* existing form / table markup */}
</div>
```

Reveal callouts (PAT plaintext shown once, cloud-account creation success): replace `.reveal-callout` markup with `<Callout title="…" status="ok">…</Callout>`.

`Tokens.tsx` PAT-reveal block: keep the underlying secret rendering (the `<code>` block stays); change the wrapper class.

- [ ] **Step 3: Audit page**

```tsx
import { AuditRow } from '../../components/lv/AuditRow';
// inside the body:
<div role="table">
  {events.map((e) => (
    <AuditRow
      key={e.id}
      time={formatTs(e.created_at)}
      actor={e.actor_username ?? e.actor_id}
      message={renderMessage(e)}
      result={e.status_class === '2xx' ? <Pill status="ok">{e.status}</Pill> : <Pill status="bad">{e.status}</Pill>}
    />
  ))}
</div>
```

- [ ] **Step 4: Run admin tests**

Run: `npm --prefix ui run test -- --run src/pages/admin/`
Address selector breaks (typically `.admin-subnav` queries → role queries).

- [ ] **Step 5: Commit**

```bash
git add ui/src/pages/admin/
git -c commit.gpgsign=false commit -m "refactor(ui): restyle admin pages and admin layout"
```

---

## Task 21: Sweep remaining test selector failures

**Goal:** every Vitest run is green.

- [ ] **Step 1: Run the full UI test suite**

Run: `npm --prefix ui run test -- --run`
Note any failing files.

- [ ] **Step 2: Fix selectors that pinned to deleted classes**

For each failing test, the most common fixes are:

| Old selector | Replacement |
|--------------|-------------|
| `.sidebar` / `.sidebar-nav` | `.lv-header` / role: `navigation` |
| `.app-header h1` | `screen.getByRole('heading', { name: '…' })` |
| `.pill.status-ok` | `.pill.ok` |
| `.admin-subnav` | `screen.getByRole('tablist')` |
| `.eol-summary-card.eol-bad` | unchanged (the wrapper component now renders both `eol-bad` and `bad`) |

Limit edits to assertion code — never weaken a behaviour assertion to make it pass.

- [ ] **Step 3: Run typecheck and full test suite**

Run: `npm --prefix ui run typecheck && npm --prefix ui run test -- --run`
Expected: clean exit on both.

- [ ] **Step 4: Run a final build**

Run: `npm --prefix ui run build`
Expected: bundle succeeds.

- [ ] **Step 5: Commit**

```bash
git add ui/
git -c commit.gpgsign=false commit -m "chore(ui): update existing page tests for new chrome and pill modifiers"
```

---

## Task 22: Final verification

- [ ] **Step 1: Confirm Make targets pass end-to-end**

Run from repo root:

```
make ui-check
make ui-build
```

Expected: both clean.

- [ ] **Step 2: Confirm Go test suite still green (no backend changes were intended)**

Run: `make test`
Expected: clean. If anything fails, the change touched something it shouldn't — investigate before continuing.

- [ ] **Step 3: Manual smoke (in dev)**

Open two terminals:

- Term 1: `make build && bin/longue-vue` (or follow the running deployment)
- Term 2: `make ui-dev`

Browse to `http://localhost:5173/ui/`. Verify:
- Top-bar nav renders Brand + 6 primary links + More + Audit (if admin)
- "More ▾" opens, items navigate, popover closes on outside click and ESC
- User menu opens; clicking accent radios changes the live accent; reload preserves choice
- `localStorage.getItem('lv:ui-prefs')` is JSON with `{accent, density, pillStyle}`
- Login page shows the spyglass mark on the left, form on the right
- Cluster detail shows breadcrumb, PageHead, StatRow, Tabs, detail-grid
- EOL page summary cards filter the table on click
- Admin page shows tabs (no left subnav)

- [ ] **Step 4: No leftover dead code**

Run: `grep -RnE 'sidebar(-[a-z]+)?|app-header|app-layout' ui/src/`
Expected: only matches inside test files where they were renamed (none in production code).

- [ ] **Step 5: Push branch**

```bash
git push -u origin feat/ui-refactor-longue-vue
```

The PR is opened by the user.

---

## Self-review checklist (run after Task 22 succeeds)

- All 14 primitives in spec §8 implemented? (Logomark, LogomarkLarge, Brand, Pill, KvList, Section, Callout, StatRow, Stat, Breadcrumb, Tabs, PageHead, EolCard, AuditRow, UiPrefsPanel — plus Disclosure as a helper.) ✓
- All 13 commit groupings in spec §12.1 represented in tasks? Tasks 1, 2, 3-11 (primitives), 12 (chrome), 13–20 (page restyles), 21 (test sweep) — covers every line. ✓
- Theming knobs persist across reload (Q5)? Verified in Task 22 step 3.
- Top-bar with "More ▾" overflow (Q6)? Task 12 + 21.
- Hybrid CSS naming honored (Q7)? Tasks 1 (CSS bodies for existing classes rewritten, new CSS for lv-* additions only) and the primitive tasks (Pill/KvList/Section/Breadcrumb/EolCard render existing class names; the rest are lv-*).
- Newsreader display only (Q8)? Task 1 step 1 + 2 (font include, `--font-display` token, only used in `.lv-page-title`, `.lv-brand-name`, `.lv-stat-value`, `.lv-eol-count`, `.lv-login-tagline`, `.lv-login-form h2`).
- Time-travel/diff explicitly out of scope? Diff tokens reserved in Task 1 for forward-compat; no diff UI component is implemented; ClusterDetail "History" tab is a placeholder Callout pointing to the future ADR.
- No backend changes? No tasks touch `internal/`, `cmd/`, `migrations/`, `api/openapi/`, or `charts/`.

If any item is "no" after execution, raise it as a follow-up — do not silently extend scope.
