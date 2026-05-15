---
title: "ADR-0025: Interactive API documentation via embedded Swagger UI"
status: "Accepted"
date: "2026-05-15"
authors: "Steve ALBERT"
tags: ["architecture", "api", "ux", "documentation", "secnumcloud"]
supersedes: ""
superseded_by: ""
---

# ADR-0025: Interactive API documentation via embedded Swagger UI

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-15
- **Supersedes:** none
- **Superseded by:** none

## Context

longue-vue exposes a 4,699-line OpenAPI 3.1 contract at
`api/openapi/openapi.yaml`. Today the spec is consumed only at build time
(by `oapi-codegen` and the validation test in
`internal/api/openapi_validation_test.go`) and is **not served over HTTP**.
Operators who want to learn the API have three options:

- read raw YAML in their editor;
- read regenerated Go types in `internal/api/api.gen.go`; or
- copy/paste the spec into an external Swagger UI / Postman.

The first two require Go familiarity and a checkout. The third defeats the
airgap posture: pasting an internal spec into a public Swagger Editor leaks
the API map, and many operator workstations cannot reach the public
internet at all.

This is friction every time an operator needs to call a non-trivial
endpoint — and the API is the only path for everything ADR-0007 covers
(PAT issuance, user management, audit reads).

## Decision

**Ship Swagger UI as an embedded, vendored, browser-based reference at
`/docs/` on the public listener.**

Concretely:

- Swagger UI 5.x is vendored under `internal/api/swagger/dist/`, committed
  to git, embedded via `//go:embed all:dist`. No CDN, no runtime npm.
- The OpenAPI spec is copied at build time into
  `internal/api/swagger/openapi.yaml` and embedded the same way. A
  `make swagger-sync-check` target enforces no drift between the embedded
  copy and `api/openapi/openapi.yaml`.
- Two new routes register on the public listener (`:8080`), not on the
  ingest listener (`:8443`):
  - `GET /docs/*` — Swagger UI shell, **unauthenticated**, consistent with
    the `/ui/*` SPA precedent.
  - `GET /openapi.yaml` — the spec, gated under `requireReadScope`,
    consistent with every other `/v1` read.
- A hand-written `internal/api/swagger/index.html` bootstraps Swagger UI
  with `url: '/openapi.yaml'`, `withCredentials: true` (session cookie
  carried automatically on "Try it out"), `persistAuthorization: true`
  (PAT survives reload), and a `responseInterceptor` that replaces the
  page with a sign-in prompt when the spec fetch returns 401.
- The SPA top-nav grows a single "API Docs" link to `/docs/`. The link is
  the only SPA coupling; `/docs/` is reachable by direct URL even under
  the `noui` build tag.

### Why this shape

- **Authenticated audience only** — the docs are an operator tool, not a
  public API reference. There is no reason to expose the API map to
  anonymous internet scanners.
- **Static shell, gated spec** — matches the `/ui/*` model exactly. The
  shell is harmless static JS; the sensitive data (the spec) inherits the
  same auth as the API itself.
- **Vendored, not CDN** — the product is SecNumCloud-aligned and supports
  airgapped deployments (ADR-0009). External CDN dependency is a
  non-starter.
- **Standalone, not inside the SPA** — decouples docs from the React build
  pipeline. Docs survive the `noui` build tag and require no Node tooling.
  The SPA gets a single "API Docs" header link as the only coupling.
- **Same-origin "Try it out"** — `servers: [{url: /}]` makes the docs page
  call the host that served it, eliminating CORS configuration and
  avoiding the worst Swagger UI footgun (a `localhost` server URL baked
  into a production page).
- **Our shell, pristine vendor** — our `index.html` lives outside `dist/`
  so Swagger UI upgrades are a straight directory replacement with no
  patch to reapply.

## Compliance and security invariants

| Property | Before | After | Rationale |
|---|---|---|---|
| `/v1/*` auth posture | session-or-PAT | **unchanged** | docs are additive |
| Anonymous can read the spec | no (spec not served) | **no** (`/openapi.yaml` is `requireReadScope`-gated) | preserves API surface confidentiality |
| Anonymous can load the shell | no (no shell) | yes (static JS, no secrets) | matches `/ui/*` precedent; shell carries no API data |
| Audit coverage | per ADR-0007 + ADR-0024 | **unchanged** | the spec read is a GET outside `/v1/admin/*` — `shouldAudit()` does not record it, same as other `/v1` reads |
| `noui` build availability | no SPA, no docs | no SPA, **docs available** | docs are an independent embed |
| Ingest listener `:8443` | write-only allowlist (ADR-0016) | **unchanged** | `/docs/` and `/openapi.yaml` are public-listener-only |
| Airgap deployability | unaffected | unaffected | bundle is vendored; no external fetch at runtime |
| CSRF on "Try it out" | n/a | covered by `SameSite=Strict` cookie + same-origin docs | no token plumbing needed |

## Consequences

### Positive

- Operators discover and call endpoints from a browser, with their
  existing session — no editor diving, no Postman import.
- Spec drift between source and docs is impossible (CI check + runtime
  byte-equality test).
- New endpoints become self-documenting the moment their OpenAPI entry
  lands.
- Foundation for future docs improvements (try-it-out filters, role-based
  endpoint hiding, theming) is in place at zero ongoing cost beyond
  Swagger UI version bumps.

### Negative

- ~1.5 MB of static JS/CSS now lives in the binary. Negligible against the
  existing SPA bundle, but non-zero.
- Swagger UI has a CVE history (typically XSS via crafted specs). Our
  spec is embedded, so spec-injection is not a vector — but the version
  pin must be maintained. `dist/.version` and the maintainer README make
  the upgrade trivial; CVEs against the pinned version are tracked
  alongside other dependency CVEs.
- The hand-written `index.html` is an additional surface to maintain. Kept
  small (~30 lines) and deliberately separate from `dist/` so Swagger UI
  upgrades do not touch it.

### Neutral

- No database migration. No new env vars. No new listener.
- No feature flag. The feature is purely additive on the read side and
  easy to disable by code revert.

## Alternatives considered

### Redoc instead of Swagger UI

Redoc renders the spec beautifully and handles very large specs better
than Swagger UI. **Rejected** because it is read-only — no "Try it out".
The user-stated goal is "help users use the API", which means interactive
calls. Redoc would still leave operators copy/pasting into Postman.

### Scalar instead of Swagger UI

Modern, smaller bundle (~300 KB), native OpenAPI 3.1, includes
try-it-out. **Considered seriously**; Swagger UI wins on name recognition
and mature ecosystem (Bearer + cookie auth flows have been battle-tested
for a decade). Scalar can be revisited as a follow-up if Swagger UI's UX
becomes a blocker on the 4,699-line spec.

### Mount inside the SPA at `/ui/docs`

Loads Swagger UI as a React route inside the existing SPA. **Rejected**:
couples the docs lifecycle to the SPA build (dies under `noui`), adds an
npm dependency, and offers no real upside — Swagger UI is not a
React-shaped widget. The SPA gets a header link to `/docs/` instead.

### Public docs, gated try-it-out

Spec viewing is anonymous; only "Try it out" calls require auth.
**Rejected**: exposes the full API surface map to anonymous scanners,
which is exactly the disclosure SecNumCloud-aligned operators want to
avoid. The operator audience already has credentials.

### CDN-loaded Swagger UI

Load Swagger UI assets from `unpkg.com` or `cdn.jsdelivr.net`.
**Rejected**: incompatible with airgapped deployments (ADR-0009),
introduces a runtime supply-chain dependency on a third party, and adds
a network hop on every docs page load.

### Hand-rolled docs page

Render the OpenAPI spec into a custom HTML page. **Rejected**: years of
Swagger UI engineering reproduce poorly in a side project; gains nothing
for the SecNumCloud audit beyond a smaller bundle.

## Implementation status

- Vendor: download Swagger UI 5.x standalone, commit under
  `internal/api/swagger/dist/`, pin in `.version`.
- Package: `internal/api/swagger/` with `embed.go` + `handlers.go` +
  hand-written `index.html`.
- Build: `make swagger-sync` copies the spec into the package;
  `make swagger-sync-check` enforces no drift.
- Server: register `/docs/*` and `/openapi.yaml` on the public mux in
  `cmd/longue-vue/main.go`.
- Spec: add `servers: - url: /` entry; addendum in `info.description`.
- SPA: add "API Docs" header link.
- Tests: handler tests in `internal/api/swagger/`, integration test in
  `internal/api/` covering auth gating, `noui` build smoke test.
- Docs: maintainer README under `internal/api/swagger/`; new section in
  `CLAUDE.md`.

## Related ADRs

- **ADR-0006** — UI for audit and curated metadata — established the
  embed-the-SPA precedent reused here.
- **ADR-0007** — Auth & RBAC — `/openapi.yaml` rides the existing
  `requireReadScope` path with no auth-model change.
- **ADR-0009** — Push collector for airgapped clusters — motivates the
  vendored, no-CDN posture.
- **ADR-0016** — DMZ ingest gateway — `/docs/` and `/openapi.yaml` are
  explicitly **not** exposed on `:8443`.
- **ADR-0017** — Public listener TLS posture — docs ride the public
  listener and inherit its TLS configuration.
- **ADR-0024** — Audit no-op write filtering — unaffected; the spec fetch
  is a non-admin GET, not audited.

## References

- Design doc: `docs/superpowers/specs/2026-05-15-swagger-api-docs-design.md`
- Implementation plan: TBD
  (`docs/superpowers/plans/2026-05-15-swagger-api-docs.md`)
- Swagger UI upstream: https://github.com/swagger-api/swagger-ui
- CLAUDE.md — to be updated with an "API docs" section.
