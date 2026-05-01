---
title: "ADR-0006: Web UI for audit and curated asset metadata"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "ui", "audit", "curated-metadata"]
supersedes: ""
superseded_by: ""
---

# ADR-0006: Web UI for audit and curated asset metadata

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

longue-vue is API-only today. Every interaction — register a cluster, list pods, inspect an ingress, check freshness — goes through `GET /v1/...` with a bearer token, typically via `curl` or a future scripted client. For the solo developer and the collector that's fine. For the two primary human user roles SecNumCloud assumes, it isn't:

- **Auditors** need to *read* the cartography layer-by-layer: "show me everything classified `applicative` in `prod-eu-west-1` as of today", "which pods front an internet-facing Ingress", "what changed in the last 30 days". Issuing those as raw REST queries is neither practical nor defensible in an audit interview — the tooling itself is part of the qualification evidence.
- **Operators** need to *annotate* assets with information Kubernetes doesn't carry: the business owner of a workload, the SNC criticality tier of a namespace, a free-text operational note, a link to the runbook, the ticket that justified a given cluster's existence. That metadata has to live *somewhere*, and shoving it into K8s annotations (the only place the collector would see it) defeats the point — it would require write access back into every catalogued cluster and couple CMDB state to cluster state.

General-purpose CMDBs in the same space typically ship with a UI that does exactly these two things. longue-vue cannot offer less without asking users to regress.

**Two tensions force the decision:**

1. **Observed vs. curated data.** The collector reconciles every tick against the live cluster — anything it writes gets overwritten (or deleted via `Delete*NotIn`) on the next pass. If a UI lets users edit `Pod.Phase` or a namespace's `labels`, those edits disappear within one interval. A UI that *feels* like it can update anything but silently drops the update is worse than no UI at all.
2. **Deployment shape.** longue-vue today is one Go binary serving one REST API behind bearer auth. A UI can either ride along inside that binary (single Deployment, shared auth) or live as a separate frontend (decoupled releases, separate repo, cross-origin concerns). The project is solo-maintained; every extra moving part has a cost.

This ADR decides the UI's scope, its data model contract with the collector, and its deployment shape. It does not decide framework, component library, or visual design — those are deferred to implementation PRs.

## Decision

**Adopt a read-plus-curate web UI, served by longue-vue itself, with a strict separation between collector-owned fields and user-curated fields.**

**Scope (what the UI does):**

1. **Browse and audit the CMDB.** Layer-scoped views (one page per ANSSI cartography layer per ADR-0002), cluster-scoped views, namespace drill-down, entity detail pages. Every list backed by the existing cursor-paginated REST endpoints. Filters match the existing query params (`cluster_id`, `namespace_id`, `kind`).
2. **Edit curated metadata.** New fields that the collector never reads or writes — owner, criticality tier, free-text notes, runbook URL, arbitrary curated labels. Editing is gated by the `write` scope on the operator's bearer token.
3. **Show freshness and collector health.** Surface `longue_vue_collector_last_poll_timestamp_seconds` per `(cluster, resource)` so auditors can see at a glance which data is stale. Reuses the `/metrics` endpoint added in ADR context.
4. **Read-only for observed fields.** `Pod.Phase`, `Workload.Kind`, `Ingress.Rules`, etc. render but are not editable in the UI — editing them would be pointless (the next collector tick would revert).

**Out of scope (v1):** dashboards, charts, full-text search, diff view across time, relationship graphs, full cartography map rendering. All defensible future work; none block the audit + annotation use case this ADR unlocks.

**Curated-metadata model.** Every entity kind that accepts human annotations gets a **separate `curated_metadata` column or adjacent table** whose contents the collector is forbidden to touch. The existing `labels` JSONB column stays reserved for Kubernetes-origin labels (what the cluster says). A new `annotations` JSONB column (or equivalent) is added, plus a small set of first-class curated fields:

- `owner` — free-text, typically a team or person (e.g. `"platform-team"`, `"alice@example.com"`)
- `criticality` — enumerated (`critical` / `high` / `medium` / `low`)
- `notes` — free-text, multi-line, `maxLength: 4000`
- `runbook_url` — string, validated as URI when non-empty
- `annotations` — arbitrary `{string: string}` map the user fills in

The collector's existing `Upsert*` paths **only set collector-owned columns** (explicit column list in every INSERT and in the ON CONFLICT DO UPDATE clause; curated columns are omitted from both). `Delete*NotIn` already deletes the whole row when the K8s entity disappears, which is correct behaviour — if the pod is gone, its curated notes go with it. Operators who need longer-lived annotations attach them to the parent Workload / Namespace instead (both survive pod churn by design).

**Deployment shape.** Bundle a SPA's built static assets into the longue-vue binary via `go:embed`, serve them under `/ui/` from the same HTTP mux that serves `/v1/*`. Same bearer-token auth, same CORS story (there isn't one — the SPA is same-origin). The SPA consumes a TypeScript client generated from `openapi.yaml` at build time (the OpenAPI spec is already the contract source of truth — generating both the Go server and the TS client from it keeps them in lockstep).

**Auth in the browser.** Bearer token is entered once via a login page and held in `sessionStorage` (not `localStorage` — reduces XSS blast radius, survives page reloads within a tab, clears on tab close). Tokens continue to be issued out-of-band through `LONGUE_VUE_API_TOKENS`; the UI never mints them. A dedicated `ui` scope is **not** added — the UI uses whatever scopes the operator's token already grants (`read` for auditors, `read`+`write` for annotators, `admin` for superusers). This reuses the mechanism already in place and matches the "the UI is a thin client of the API" framing.

**Framework.** React + TypeScript + Vite, with the component library left to implementation. Rationale: biggest ecosystem for OpenAPI-generated clients, most candidates familiar with it (a solo-maintained project still benefits from a mainstream stack if help is ever needed), Vite's output is a static bundle that embeds cleanly. This is the least load-bearing part of the decision — ripping out React for something else later is a contained refactor because the data layer is defined by the OpenAPI spec, not the framework.

**Source layout.** The SPA lives in a **sibling directory** (`ui/`) inside the same repo, not a separate repository. Single-repo keeps the OpenAPI spec, the server, and the client in one place with one CI pipeline. The Go binary's build step runs `npm run build` in `ui/` and consumes the `dist/` output via `go:embed`. Go developers who never touch the frontend get an informative error if Node isn't installed; a `make ui-skip` target skips the frontend build entirely for backend-only workflows.

## Consequences

### Positive

- **POS-001**: Auditors get a first-class read surface for the cartography, layer-filterable, without writing REST queries. SNC qualification evidence can reference UI screenshots instead of `curl` transcripts.
- **POS-002**: Operators get a place to record business-owner / criticality / runbook information that's stable across pod churn (it lives on Workloads and Namespaces, which don't vanish every deploy).
- **POS-003**: The collector vs. curated separation is enforced at the column level — there is no code path in the collector that touches curated columns, so user edits can never be silently lost. This is testable (an integration test asserts that `UpsertPod` followed by a manual `UpdatePod` annotation keeps the annotation after the next `UpsertPod`).
- **POS-004**: Same Deployment, same Pod, same bearer auth. The operational footprint doesn't grow. Matches the solo-maintainer constraint that shaped ADR-0005.
- **POS-005**: OpenAPI stays the contract source of truth. TS client regenerates in CI alongside the Go server; drift is caught the same way codegen drift already is.
- **POS-006**: Same-origin SPA means no CORS, no preflight, no cookie/SameSite gymnastics. `sessionStorage` for the token is the simplest workable pattern for a read-plus-curate tool (stronger options like cookie-based sessions are a natural follow-up if a broader user base appears).

### Negative

- **NEG-001**: Curated metadata is a new first-class concern in the data model. Every new entity kind that wants human annotations needs the same column additions (owner / criticality / notes / runbook_url / annotations). Mitigation: provide a reusable migration template and a shared `CuratedMetadata` OpenAPI component to copy-paste.
- **NEG-002**: Row-level delete-on-disappear (the collector's existing `Delete*NotIn`) takes curated pod annotations with it. This is intentional per the decision above — the alternative is soft-delete + tombstones, a significantly bigger design. Operators who need durable annotations attach them to Workloads / Namespaces.
- **NEG-003**: Go developers now need Node + npm to produce a release binary. Mitigation: the `make ui-skip` target and a CI job that still runs when `ui/` didn't change.
- **NEG-004**: Bundled-SPA approach ties frontend and backend release cadence. A UI-only hotfix requires a full `longue-vue` release. Acceptable at current scale; extractable if it becomes painful.
- **NEG-005**: `sessionStorage` tokens are browser-JS-accessible. XSS in the UI is now a token-exfiltration vector. Mitigation: standard CSP + the UI doesn't execute user-supplied HTML (notes are rendered as plain text, not markdown-rendered-to-HTML, in v1). A stricter cookie-based session flow is a follow-up ADR if the threat model tightens.

### Neutral

- **NEU-001**: The existing `labels` JSONB column stays exactly as it is — the collector writes it, the UI displays it, nobody else touches it. Renaming it would cascade into migrations, the store, and every `labels` caller in the collector for cosmetic gain.
- **NEU-002**: Curated endpoints reuse the existing `/v1/<kind>/{id}` URLs with a PATCH body that touches only curated fields; no `/v1/<kind>/{id}/annotations` side-endpoint. Keeps the API surface small.

## Alternatives Considered

### No UI — document `curl` recipes instead

- **ALT-001**: **Description**: Publish a cookbook of `curl` / `jq` invocations for common audit queries; do not build a UI.
- **ALT-002**: **Rejection Reason**: Fails both target users. Auditors can't review thousands of pods through `jq`, and operators have nowhere to record non-K8s metadata. Comparable CMDB UIs set a floor that longue-vue cannot clear by going backwards.

### Separate `longue-vue-ui` repository, deployed independently

- **ALT-003**: **Description**: A separate repo containing the SPA; operators deploy the UI as its own container behind an ingress that routes `/` to the UI and `/v1/*` to longue-vue.
- **ALT-004**: **Rejection Reason**: Two repos, two release cadences, two CI pipelines, cross-origin auth to solve, distinct token-handling code for browsers. Every cost paid to decouple things that aren't today under pressure to decouple. Extracting the UI later is a rename-and-split; embedding it at the start is cheaper than pre-splitting it.

### Allow editing observed fields, let the collector honor a "do-not-overwrite" flag

- **ALT-005**: **Description**: Add a `collector_managed` boolean per row (or per column); when false, the collector skips updates.
- **ALT-006**: **Rejection Reason**: Breaks ANSSI cartography fidelity — "what does the cluster *actually* look like?" stops being answerable if users can freeze fields on their stored values. The collector's job is to mirror reality; losing that invalidates the qualification story in ADR-0001. The clean split (observed columns reconciled, curated columns never touched) delivers the same editing affordance without the fidelity tax.

### Store curated metadata in K8s annotations via a write-back collector

- **ALT-007**: **Description**: Users annotate in longue-vue; a reverse-collector writes the annotations back to the live K8s resources so they're visible in-cluster and survive a CMDB rebuild.
- **ALT-008**: **Rejection Reason**: Requires `update` RBAC on every catalogued cluster, inverting the deliberately read-only RBAC model in `deploy/rbac.yaml`. Expands the blast radius of a longue-vue compromise from "read your inventory" to "mutate your clusters". The CMDB-local curated-column model is simpler and safer; a write-back integration can be built later as an opt-in if a real operational need emerges.

### Server-rendered HTML (no SPA)

- **ALT-009**: **Description**: Use Go templates + HTMX to render pages server-side; avoid the JS toolchain entirely.
- **ALT-010**: **Rejection Reason**: Attractive for the "no new toolchain" angle, and not ruled out forever. Rejected for v1 because the auditor/operator UX benefits from client-side filtering and drill-down (thousands of pods across clusters) that HTMX handles awkwardly compared to a typed SPA consuming the OpenAPI spec. If the SPA toolchain proves to be maintenance overhead that isn't paying its way, reverting to templates + HTMX is a contained rewrite.

### Multiple UIs (one for audit, one for operators)

- **ALT-011**: **Description**: Ship `longue-vue-audit` (read-only, polished for qualification interviews) and `longue-vue-admin` (curation-heavy) as two separate frontends.
- **ALT-012**: **Rejection Reason**: Premature specialization. The two use cases differ by scope, not by shape — auditors and operators both want the same list views; operators additionally see edit buttons. A single UI with role-aware rendering (hide edit affordances when the token lacks `write` scope) covers both without the duplication.

## Implementation Notes

- **IMP-001**: Migration 00012 adds curated columns to the first set of kinds that benefit: `clusters`, `namespaces`, `workloads`, `ingresses` (the "durable" entities — nodes and pods come-and-go). Schema: `owner TEXT`, `criticality TEXT` (with a CHECK constraint on the four enum values, `NULL` allowed), `notes TEXT`, `runbook_url TEXT`, `annotations JSONB NOT NULL DEFAULT '{}'::jsonb`. Indexes: none for now; add when a query demands them.
- **IMP-002**: OpenAPI: introduce a reusable `CuratedMetadata` schema with the five fields above; each `<Kind>Mutable` composes it via `allOf`. Regenerate the Go server; add the fields to PG `scan*` / `Update*` / `Create*` paths. Every `Upsert*` path explicitly excludes curated columns from the ON CONFLICT DO UPDATE set — add a test that asserts this.
- **IMP-003**: UI scaffolding: `ui/` directory with Vite + React + TypeScript, `ui/package.json`, `ui/openapi-codegen` script that reads `../api/openapi/openapi.yaml`. Makefile additions: `make ui-build` (runs `npm ci && npm run build`), `make ui-skip` (touches `ui/dist/.skip` so `go:embed` sees an empty placeholder). `internal/ui/embed.go` owns the `go:embed ui/dist` directive and the `http.FileServer` that serves `/ui/*`.
- **IMP-004**: Login page posts the bearer token to a client-side handler that stores it in `sessionStorage` and uses it for every subsequent fetch via a shared `Authorization` header in the generated client. Invalid token → redirect back to login with an inline error.
- **IMP-005**: Role-aware rendering. The UI probes `GET /v1/clusters` on load and decodes the scope hint from the WWW-Authenticate challenge shape (or from a follow-up `GET /v1/auth/self` if adding a whoami endpoint proves cleaner). Edit affordances only render when `write` is available.
- **IMP-006**: Freshness indicator: `/metrics` is unauthenticated (Prometheus convention). Scraping it from the browser is fine; a thin wrapper endpoint `GET /v1/status/freshness` that returns parsed `(cluster, resource, last_poll_ts)` tuples is an option if parsing Prometheus text format in JS proves awkward.
- **IMP-007**: Tests: UI unit tests via Vitest (happy path per page, role-aware rendering gate, token-absent redirect). A Playwright smoke test that exercises login → list pods → open pod detail → (with write scope) annotate → save → reload → annotation survives. Backend test added in IMP-002 for observed/curated separation.
- **IMP-008**: `CLAUDE.md` updates: add the `ui/` layout paragraph; add a "curated vs observed" note to the store section; document the `make ui-build` / `make ui-skip` targets.
- **IMP-009**: `deploy/` updates: the existing Kustomize manifests keep working unchanged — the UI is served on the same port as the API. Only README tweak needed: "the UI is at `/ui/` behind the same ingress".
- **IMP-010**: Rollout: land IMP-001 (curated columns) and IMP-002 (OpenAPI + server + store) first, in their own PR. Ship a minimal UI skeleton (login + one list page) as a follow-up PR; then iterate view-by-view. Each increment is independently mergeable and deployable.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md` (POS-003 anticipates external push via API; the same affordance underpins a browser client)
- **REF-002**: ADR-0002 — Kubernetes-to-ANSSI cartography layer mapping — `docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md` (layer filter is the UI's primary audit axis)
- **REF-003**: ADR-0005 — Multi-cluster collector topology — `docs/adr/adr-0005-multi-cluster-collector.md` (UI's cluster selector is trivial now that every row carries cluster_id)
- **REF-004**: ANSSI SecNumCloud cartography requirements — https://cyber.gouv.fr/enjeux-technologiques/cloud/
- **REF-005**: OpenAPI 3.1 — https://spec.openapis.org/oas/v3.1.0
- **REF-006**: `openapi-typescript-codegen` (one candidate for TS client generation) — https://github.com/ferdikoomen/openapi-typescript-codegen
- **REF-007**: Vite — https://vitejs.dev/
