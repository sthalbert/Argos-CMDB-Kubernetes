---
title: "ADR-0020: Rename product Argos to longue-vue"
status: "Accepted"
date: "2026-04-30"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "rename", "branding", "secnumcloud"]
supersedes: ""
superseded_by: ""
---

# ADR-0020 — Rename product "Argos" to "longue-vue"

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-04-30
- **Supersedes:** none
- **Superseded by:** none

## Context

The product was originally codenamed "Argos". The 2026-04-30 product review
decided to rebrand to "longue-vue" before the first external release. Renaming
late costs us 219 files and ~2,400 token replacements; renaming after we have
external users would be far more expensive (broken Helm release names, broken
dashboards, broken bookmarks).

The rename touches every layer:

- Go module path (`github.com/sthalbert/argos` → `github.com/sthalbert/longue-vue`)
- Binary names (`argosd` → `longue-vue`, plus the three collectors / gateway)
- Helm chart names and labels
- Docker image names (`ghcr.io/sthalbert/argos*` → `ghcr.io/sthalbert/longue-vue*`)
- Environment variables (`LONGUE_VUE_*` → `LONGUE_VUE_*`)
- Prometheus metric namespace (`argos_*` → `longue_vue_*`)
- Kubernetes annotation domain (`argos.io/*` → `longue-vue.io/*`)
- Session cookie (`argos_session` → `longue_vue_session`)
- HTTP custom headers (`X-Argos-Verified-*` → `X-Longue-Vue-Verified-*`)
- PAT prefix (`argos_pat_*` → `longue_vue_pat_*`) — see §Decision below
- npm package name, OpenAPI title, MCP server name, UI branding, all docs/ADRs

## Decision

### 1. Naming conventions per ecosystem

| Context                   | Form                  | Rationale                              |
|---------------------------|-----------------------|----------------------------------------|
| Go module path            | `longue-vue`          | Hyphens allowed, idiomatic for Go      |
| Binary names              | `longue-vue`, `longue-vue-collector`, `longue-vue-vm-collector`, `longue-vue-ingest-gw` | Hyphens allowed; daemon is plain `longue-vue` (no `-d` suffix — cleaner) |
| Helm charts               | `longue-vue*`         | Hyphens allowed; Helm idiom            |
| Docker images             | `longue-vue*`         | Hyphens allowed; Docker idiom          |
| npm package               | `longue-vue-ui`       | Hyphens allowed; npm idiom             |
| Annotation domain         | `longue-vue.io/*`     | Valid DNS label                        |
| Documentation prose       | `longue-vue` (hyphen) | Reads naturally                        |
| Environment variables     | `LONGUE_VUE_*`        | POSIX forbids hyphens                  |
| Prometheus metrics        | `longue_vue_*`        | Regex `[a-zA-Z_:][a-zA-Z0-9_:]*` forbids hyphens |
| PAT scheme prefix         | `longue_vue_pat_*`    | snake_case for parseable identifiers   |
| Session cookie            | `longue_vue_session`  | snake_case                             |
| HTTP custom headers       | `X-Longue-Vue-Verified-*` | Title-Case-With-Hyphens, RFC 7230  |

### 2. PAT prefix dual-support (the only legacy compatibility we keep)

Tokens already issued in production use the `argos_pat_<8hex>_<32urlb64>` format
and are stored hashed in the `tokens` table — they cannot be regenerated without
disrupting every collector deployment and every human PAT user. Therefore:

- **Verification path:** `auth.ParseToken` accepts **both** `argos_pat_*` and
  `longue_vue_pat_*` schemes. The 8-char prefix lookup column is unchanged
  (the scheme prefix is stripped before lookup; only the 8-char prefix maps
  to a row). Both schemes hit the same store rows.
- **Issuance path:** every newly minted token uses `longue_vue_pat_*`.
- **No deprecation deadline:** legacy `argos_pat_*` tokens remain valid until
  manually revoked. There is no flag day. The dual-acceptance code path is
  permanent until a future ADR explicitly drops it (which would require a
  large-scale token re-issuance campaign).

This is the only legacy alias we keep. Every other rename is a clean cutover.

### 3. Other surfaces — clean cutover, no aliases

For env vars, metrics, annotations, cookies, headers, and chart/image names,
operators redeploy with the new configuration. Acceptable because:

- Env vars: deployments are reconfigured during chart upgrade.
- Metrics: dashboards must be updated regardless of release timing; doing it
  now (before scale) is cheaper than later.
- Annotations: the `00029_strip_argos_io_annotations.sql` migration drops
  legacy keys from JSONB columns; the EOL enricher repopulates with the new
  domain on its next tick.
- Cookies: 8-hour sliding-expiry sessions naturally turn over.
- Headers: gateway and argosd are deployed together (same release).

### 4. ADR-0001 to ADR-0019: amended in place

The product name in the body of historical ADRs is replaced. The decisions
themselves are unchanged. This ADR is the single source of truth for the
rename history.

## Alternatives Considered

1. **Keep "Argos" indefinitely.** Rejected: the product review committed to
   the rebrand for external go-to-market reasons, and renaming costs grow
   linearly with the user base.

2. **Run dual-name compatibility for every surface (env vars, metrics,
   annotations, cookies, headers).** Rejected: the operational burden of
   maintaining N legacy aliases forever is high, and the only surface where
   the legacy name is *uncomputable* from external state is the PAT verifier
   (existing tokens are hashed and cannot be reissued without disrupting
   every collector deployment). Every other surface can be reconfigured
   during a coordinated chart upgrade. ADR §2 keeps the legacy alias only
   where it is operationally necessary.

3. **Renaming the GitHub repo without redirect.** Not chosen: GitHub
   provides automatic redirects on repo rename, so the operator-side rename
   is a single click with no broken-link consequence. Documented as a
   manual follow-up step in the PR body.

## Consequences

- **Positive:** consistent naming end-to-end; no permanent dual-name confusion
  outside the PAT verifier.
- **Negative:** all in-flight feature branches must be rebased after the merge
  (mechanical conflict on imports / env vars / strings).
- **Risk:** the PAT dual-prefix logic must have explicit test coverage so it
  doesn't silently break during a future refactor. The required tests
  (`TestParseToken_AcceptsBothSchemes` and `TestMintToken_UsesNewSchemeOnly`)
  are specified in Phase 5 Task 5.2 of the implementation plan.

## Implementation Notes

- Plan: `docs/superpowers/plans/2026-04-30-rename-argos-to-longue-vue.md`
- Migration: `migrations/00029_strip_argos_io_annotations.sql`
- PAT verifier: `internal/auth/tokens.go` (`ParseToken`, `TokenScheme`,
  `TokenSchemeLegacy`)
