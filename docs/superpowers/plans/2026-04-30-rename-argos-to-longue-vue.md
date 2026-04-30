# Rename Argos → longue-vue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the product from "Argos" to "longue-vue" everywhere in code, documentation, charts, CI, and ADRs, while preserving all PATs already issued in production.

**Architecture:** Clean rename across all subsystems with zero legacy compatibility, **except** for PAT verification which keeps reading the legacy `argos_pat_*` prefix forever (new tokens emitted as `longue_vue_pat_*`). Naming follows each ecosystem's standard: `longue-vue` (with hyphen) for Go module, binaries, charts, images, npm package, annotation domain, and documentation; `LONGUE_VUE_*` / `longue_vue_*` (snake_case) for env vars, Prometheus metrics, cookie name, and PAT prefix. The daemon binary is `longue-vue` (not `longue-vue-d`). A new ADR-0020 records the rename and the PAT dual-prefix strategy; ADR-0001 to ADR-0019 are amended in-place to reflect the new product name in their bodies.

**Tech Stack:** Go 1.25, PostgreSQL + pgx/v5, Vite/React/TypeScript, Helm, Docker, GitHub Actions, OpenAPI 3.1, Prometheus.

---

## File Structure

This plan touches ~219 files. Files reorganized by responsibility:

**Renamed directories (move operations, no content split):**
- `cmd/argosd/` → `cmd/longue-vue/`
- `cmd/argos-collector/` → `cmd/longue-vue-collector/`
- `cmd/argos-vm-collector/` → `cmd/longue-vue-vm-collector/`
- `cmd/argos-ingest-gw/` → `cmd/longue-vue-ingest-gw/`
- `charts/argos/` → `charts/longue-vue/`
- `charts/argos-collector/` → `charts/longue-vue-collector/`
- `charts/argos-vm-collector/` → `charts/longue-vue-vm-collector/`
- `charts/argos-ingest-gw/` → `charts/longue-vue-ingest-gw/`

**New files:**
- `docs/adr/adr-0020-rename-argos-to-longue-vue.md` — records the rename decision + PAT dual-prefix strategy
- `migrations/00029_strip_argos_io_annotations.sql` — strips legacy `argos.io/*` keys from JSONB annotation columns

**Modified files (one-line summaries of responsibilities; full code in tasks below):**
- `go.mod` — module path
- `internal/auth/tokens.go` — accept both PAT scheme prefixes; emit only the new one
- `internal/auth/session.go` — cookie name constant
- `internal/auth/middleware.go` — `WWW-Authenticate` realm
- `internal/eol/enricher.go` — annotation prefix constant
- `internal/ingestgw/proxy.go` — `X-Longue-Vue-Verified-*` reserved-headers list
- `internal/metrics/metrics.go`, `internal/ingestgw/metrics.go`, `internal/vmcollector/metrics.go` — Prometheus namespace
- `internal/mcp/server.go` — MCP server name string
- `internal/collector/apiclient/client.go` — `X-Route-Key` header value
- `internal/vmcollector/filter/filter.go` — `longue-vue.io/ignore` annotation read
- `cmd/longue-vue/main.go` (was argosd) — bootstrap banner string, slog logger names, env var lookups
- `Makefile` — `BINARY`, `COLLECTOR_BINARY`, `VM_COLLECTOR_BINARY`, `IMAGE_NAME`
- `Dockerfile`, `Dockerfile.collector`, `Dockerfile.vm-collector`, `Dockerfile.ingest-gw` — output paths and entrypoints
- `api/openapi/openapi.yaml` — `info.title`, `info.description`
- `ui/package.json`, `ui/index.html`, `ui/src/App.tsx`, `ui/src/pages/Login.tsx`, `ui/src/pages/EolDashboard.tsx`, `ui/src/test/fixtures.ts`, `ui/src/test/handlers.ts`, `ui/src/components/inventory/AnnotationsCard.test.tsx`
- `.github/workflows/ci.yml`, `.github/workflows/release.yml`
- `scripts/seed-demo.sh`
- `README.md`, `CHANGELOG.md`
- All files under `docs/` (mass replace)
- All files under `docs/adr/` (mass replace except ADR-0020 which is new)
- Every `*.go` file with an import of `github.com/sthalbert/argos/...` (~106 files)
- Every `charts/*/values.yaml`, `charts/*/Chart.yaml`, `charts/*/templates/*.yaml` (label and image refs)

---

## Phase 0 — Setup, ADR, and worktree branch

### Task 0.1: Create a feature branch

**Files:** none

- [ ] **Step 1: Confirm clean working tree**

Run: `git -C /Users/Steve.Albert/GolandProjects/Argos-CMDB-Kubernetes status --short`

Expected: only the untracked files visible at session start (`.claude/`, `argos-ingest-gw`, `argosd`, `comprehensive_security_assessment_report.md`, `deploy/collector/deployment.airgap.yaml`, `values.yml`). No staged or modified tracked files. If anything is staged, stop and ask the user.

- [ ] **Step 2: Create and switch to the rename branch**

```bash
git -c commit.gpgsign=false checkout -b feat/rename-to-longue-vue
```

Expected output: `Switched to a new branch 'feat/rename-to-longue-vue'`.

- [ ] **Step 3: Verify**

Run: `git -C . branch --show-current`

Expected: `feat/rename-to-longue-vue`.

### Task 0.2: Write ADR-0020 documenting the rename

**Files:**
- Create: `docs/adr/adr-0020-rename-argos-to-longue-vue.md`

- [ ] **Step 1: Create the ADR**

Write `docs/adr/adr-0020-rename-argos-to-longue-vue.md` with:

```markdown
# ADR-0020 — Rename product "Argos" to "longue-vue"

- **Status:** Accepted
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
- Environment variables (`ARGOS_*` → `LONGUE_VUE_*`)
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

## Consequences

- **Positive:** consistent naming end-to-end; no permanent dual-name confusion
  outside the PAT verifier.
- **Negative:** all in-flight feature branches must be rebased after the merge
  (mechanical conflict on imports / env vars / strings).
- **Risk:** the PAT dual-prefix logic must have explicit test coverage so it
  doesn't silently break during a future refactor. See §6 below.

## Implementation references

- Plan: `docs/superpowers/plans/2026-04-30-rename-argos-to-longue-vue.md`
- Migration: `migrations/00029_strip_argos_io_annotations.sql`
- PAT verifier: `internal/auth/tokens.go` (`ParseToken`, `TokenScheme`,
  `TokenSchemeLegacy`)
```

- [ ] **Step 2: Commit**

```bash
git -c commit.gpgsign=false add docs/adr/adr-0020-rename-argos-to-longue-vue.md
git -c commit.gpgsign=false commit -m "docs(adr): add ADR-0020 for rename to longue-vue"
```

---

## Phase 1 — Go module path + import rewrite

### Task 1.1: Edit go.mod

**Files:**
- Modify: `go.mod` (line 1)

- [ ] **Step 1: Rewrite the module path**

```bash
go mod edit -module github.com/sthalbert/longue-vue
```

- [ ] **Step 2: Verify**

Run: `head -3 go.mod`

Expected first line: `module github.com/sthalbert/longue-vue`.

### Task 1.2: Rewrite all Go imports

**Files:** every `.go` file under the repo (~106 files).

- [ ] **Step 1: Discover the count of impacted files**

Run from the repo root:

```bash
grep -rl "github.com/sthalbert/argos" --include='*.go' . | wc -l
```

Note the count — should be in the range 100–110.

- [ ] **Step 2: Replace the import path everywhere**

```bash
grep -rl "github.com/sthalbert/argos" --include='*.go' . | xargs sed -i '' 's|github.com/sthalbert/argos|github.com/sthalbert/longue-vue|g'
```

(macOS BSD `sed` requires the empty `''` after `-i`; on Linux drop it.)

- [ ] **Step 3: Verify zero remaining old-path imports in Go files**

Run: `grep -rn "github.com/sthalbert/argos" --include='*.go' . || echo "OK: no remaining matches"`

Expected: `OK: no remaining matches`.

- [ ] **Step 4: Verify the build still compiles**

Run: `go build ./...`

Expected: no output (clean build). If any package fails to build, stop and read the error — likely a stray non-`.go` file (codegen template) hardcoding the import.

- [ ] **Step 5: Run vet**

Run: `go vet ./...`

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(module): rename Go module path to longue-vue"
```

---

## Phase 2 — Rename binary directories and Makefile

### Task 2.1: Move the four cmd/ directories

**Files:**
- Rename: `cmd/argosd/` → `cmd/longue-vue/`
- Rename: `cmd/argos-collector/` → `cmd/longue-vue-collector/`
- Rename: `cmd/argos-vm-collector/` → `cmd/longue-vue-vm-collector/`
- Rename: `cmd/argos-ingest-gw/` → `cmd/longue-vue-ingest-gw/`

- [ ] **Step 1: Move the four directories using `git mv` to preserve history**

```bash
git -c commit.gpgsign=false mv cmd/argosd cmd/longue-vue
git -c commit.gpgsign=false mv cmd/argos-collector cmd/longue-vue-collector
git -c commit.gpgsign=false mv cmd/argos-vm-collector cmd/longue-vue-vm-collector
git -c commit.gpgsign=false mv cmd/argos-ingest-gw cmd/longue-vue-ingest-gw
```

- [ ] **Step 2: Verify directory structure**

Run: `ls cmd/`

Expected exactly: `longue-vue  longue-vue-collector  longue-vue-ingest-gw  longue-vue-vm-collector` (plus whatever else was in cmd/ — confirm only these moved).

### Task 2.2: Update Makefile binary names

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Edit the four binary names and the cmd path references**

Read the full Makefile first, then apply these changes:

```diff
-BINARY  := argosd
+BINARY  := longue-vue
...
-COLLECTOR_BINARY    := argos-collector
-VM_COLLECTOR_BINARY := argos-vm-collector
+COLLECTOR_BINARY    := longue-vue-collector
+VM_COLLECTOR_BINARY := longue-vue-vm-collector
...
-IMAGE_NAME ?= argos
+IMAGE_NAME ?= longue-vue
```

Then search for any remaining references to `cmd/argosd`, `cmd/argos-collector`, `cmd/argos-vm-collector`, `cmd/argos-ingest-gw` in the Makefile and rewrite to the new paths.

If the Makefile has an `INGEST_GW_BINARY` (or similar) variable for the gateway, update its value too.

- [ ] **Step 2: Verify the Makefile builds the daemon**

Run: `make build-noui`

Expected: produces `bin/longue-vue` (NOT `bin/argosd`). Confirm with `ls bin/`.

- [ ] **Step 3: Verify all four binaries build**

Run (one at a time, the targets are likely `build-collector`, `build-vm-collector`, `build-ingest-gw` — adapt to actual target names found in the Makefile):

```bash
make build-collector
make build-vm-collector
make build-ingest-gw 2>/dev/null || echo "(check Makefile for the gateway target name)"
```

Expected: each produces a `bin/longue-vue-*` binary.

### Task 2.3: Update Dockerfiles

**Files:**
- Modify: `Dockerfile`
- Modify: `Dockerfile.collector`
- Modify: `Dockerfile.vm-collector`
- Modify: `Dockerfile.ingest-gw`

- [ ] **Step 1: Replace cmd paths and output binary names**

For each Dockerfile, replace:
- `cmd/argosd` → `cmd/longue-vue`
- `cmd/argos-collector` → `cmd/longue-vue-collector`
- `cmd/argos-vm-collector` → `cmd/longue-vue-vm-collector`
- `cmd/argos-ingest-gw` → `cmd/longue-vue-ingest-gw`
- `-o /out/argosd` → `-o /out/longue-vue`
- `-o /out/argos-collector` → `-o /out/longue-vue-collector` (etc.)
- `ENTRYPOINT ["/argosd"]` → `ENTRYPOINT ["/longue-vue"]` (etc.)
- `COPY --from=builder /out/argosd /argosd` → `COPY --from=builder /out/longue-vue /longue-vue` (etc.)

- [ ] **Step 2: Verify a Docker build still works**

Run: `make docker-build`

Expected: produces image `argos:dev` ... but wait — at this stage the image is still tagged `argos:dev` because we haven't updated `IMAGE_NAME` references in CI/release. We DID update `Makefile`'s `IMAGE_NAME` in Task 2.2. Confirm the produced image is `longue-vue:dev`:

```bash
docker images | grep longue-vue
```

Expected: a `longue-vue:dev` row.

### Task 2.4: Commit Phase 2

- [ ] **Step 1: Verify everything still compiles**

```bash
go build ./...
go vet ./...
```

Expected: clean.

- [ ] **Step 2: Commit**

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(cmd): rename binary directories and Makefile to longue-vue"
```

---

## Phase 3 — Environment variables ARGOS_* → LONGUE_VUE_*

### Task 3.1: Rewrite env var references in Go source

**Files:** every `.go` file referencing `ARGOS_` (~50 files including main.go for each binary, tests, configuration parsing).

- [ ] **Step 1: Discover the impacted Go files**

```bash
grep -rln 'ARGOS_' --include='*.go' . | tee /tmp/longuevue-go-env-files.txt | wc -l
```

Note the count.

- [ ] **Step 2: Replace `ARGOS_` with `LONGUE_VUE_` in those files**

```bash
xargs -a /tmp/longuevue-go-env-files.txt sed -i '' 's/ARGOS_/LONGUE_VUE_/g'
```

- [ ] **Step 3: Verify no remaining ARGOS_ in Go**

```bash
grep -rn 'ARGOS_' --include='*.go' . || echo "OK"
```

Expected: `OK`.

- [ ] **Step 4: Build + tests still green**

```bash
go build ./...
go test -short -count=1 -race ./...
```

Expected: clean build, all tests pass. (PAT-verification tests for legacy prefix may already exist in `internal/auth/tokens_test.go` and they should pass — those aren't env var related.)

### Task 3.2: Rewrite env var references in charts/values.yaml

**Files:** every `charts/*/values.yaml` and any `charts/*/templates/*.yaml` referencing `ARGOS_*`.

- [ ] **Step 1: Replace in charts**

```bash
grep -rln 'ARGOS_' charts/ | xargs sed -i '' 's/ARGOS_/LONGUE_VUE_/g'
```

- [ ] **Step 2: Verify**

```bash
grep -rn 'ARGOS_' charts/ || echo "OK"
```

Expected: `OK`.

- [ ] **Step 3: Lint each chart**

```bash
helm lint charts/longue-vue
helm lint charts/longue-vue-collector
helm lint charts/longue-vue-vm-collector
helm lint charts/longue-vue-ingest-gw
```

Expected: each lints cleanly. (We haven't yet renamed the chart `name:` field — that's Phase 8. So lint still expects the directory name to match the Chart.yaml `name:`. Continue if lint surfaces only that mismatch.)

### Task 3.3: Rewrite env var references in scripts and docs

**Files:**
- Modify: `scripts/seed-demo.sh`
- Modify: every `docs/**/*.md` containing `ARGOS_`
- Modify: `README.md`, `CHANGELOG.md` if they contain `ARGOS_`

- [ ] **Step 1: Scripts**

```bash
grep -rln 'ARGOS_' scripts/ | xargs sed -i '' 's/ARGOS_/LONGUE_VUE_/g'
```

- [ ] **Step 2: Docs**

```bash
grep -rln 'ARGOS_' docs/ README.md CHANGELOG.md 2>/dev/null | xargs sed -i '' 's/ARGOS_/LONGUE_VUE_/g'
```

- [ ] **Step 3: Verify zero remaining `ARGOS_` anywhere**

```bash
grep -rn 'ARGOS_' . --include='*.go' --include='*.yaml' --include='*.yml' --include='*.md' --include='*.sh' || echo "OK"
```

Expected: `OK`.

### Task 3.4: Commit Phase 3

- [ ] **Step 1: Run the full test suite**

```bash
make test
```

Expected: all tests pass (including the integration tests if `PGX_TEST_DATABASE` is set — operator should set it). If integration tests can't run locally, at minimum unit tests must pass.

- [ ] **Step 2: Commit**

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(config): rename ARGOS_ env vars to LONGUE_VUE_"
```

---

## Phase 4 — Internal Go strings (logs, banners, headers, MCP, realm, cookie name, route key)

### Task 4.1: Update bootstrap banner and slog logger names

**Files:**
- Modify: `cmd/longue-vue/main.go`
- Modify: `cmd/longue-vue-collector/main.go`
- Modify: `cmd/longue-vue-vm-collector/main.go`
- Modify: `cmd/longue-vue-ingest-gw/main.go`

- [ ] **Step 1: Find every "argos"/"argosd" string literal in cmd/**

```bash
grep -rn '"argos\|"argosd\|ARGOS FIRST' cmd/
```

- [ ] **Step 2: Replace literally**

In each occurrence:
- `"argosd "` → `"longue-vue "` (log lines like `"argosd listening"`)
- `"argosd starting"` → `"longue-vue starting"` (etc.)
- `"argos-collector "` → `"longue-vue-collector "`
- `"argos-vm-collector "` → `"longue-vue-vm-collector "`
- `"argos-ingest-gw "` → `"longue-vue-ingest-gw "`
- The bootstrap banner `"\n  ARGOS FIRST-RUN BOOTSTRAP"` → `"\n  LONGUE-VUE FIRST-RUN BOOTSTRAP"`

Each Edit must show the exact old_string with surrounding context. Do NOT use blind sed for this task — banner formatting (whitespace, leading newline) varies and a sed mistake silently breaks the banner.

- [ ] **Step 3: Verify**

```bash
grep -rn '"argos\|"argosd\|ARGOS FIRST' cmd/ || echo "OK"
```

Expected: `OK`.

### Task 4.2: Update WWW-Authenticate realm

**Files:**
- Modify: `internal/auth/middleware.go` (line 278)

- [ ] **Step 1: Edit the realm string**

```diff
-w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
+w.Header().Set("WWW-Authenticate", `Bearer realm="longue-vue"`)
```

### Task 4.3: Update MCP server name

**Files:**
- Modify: `internal/mcp/server.go` (line 105)

- [ ] **Step 1: Edit the MCP server name**

```diff
 mcpSrv := server.NewMCPServer(
-    "Argos CMDB",
+    "longue-vue CMDB",
     "0.1.0",
```

### Task 4.4: Update X-Argos-Verified-* headers → X-Longue-Vue-Verified-*

**Files:**
- Modify: `internal/ingestgw/proxy.go` (lines 18, 43–45)
- Modify: `internal/ingestgw/security_test.go` (lines 110, 146–148)
- Modify: `internal/ingestgw/proxy_test.go` (lines 66–68)

- [ ] **Step 1: Replace in proxy.go**

Read the file first (it has multi-line context comments). Then:

```bash
sed -i '' 's/X-Argos-Verified-/X-Longue-Vue-Verified-/g' internal/ingestgw/proxy.go internal/ingestgw/security_test.go internal/ingestgw/proxy_test.go
```

- [ ] **Step 2: Verify**

```bash
grep -rn 'X-Argos-Verified' internal/ || echo "OK"
```

Expected: `OK`.

- [ ] **Step 3: Run the gateway tests**

```bash
go test -count=1 -race ./internal/ingestgw/...
```

Expected: all pass.

### Task 4.5: Update X-Route-Key value in collector apiclient

**Files:**
- Modify: `internal/collector/apiclient/client.go`

- [ ] **Step 1: Find the X-Route-Key value**

```bash
grep -n 'X-Route-Key\|"argos"' internal/collector/apiclient/
```

- [ ] **Step 2: Replace `"argos"` with `"longue-vue"` for the X-Route-Key header value (and only there — the test for this should already exist).**

Use Edit with surrounding context (find the `req.Header.Set("X-Route-Key", "argos")` line and change `"argos"` to `"longue-vue"`).

- [ ] **Step 3: Run the apiclient tests**

```bash
go test -count=1 -race ./internal/collector/apiclient/...
```

Expected: pass.

### Task 4.6: Update session cookie name

**Files:**
- Modify: `internal/auth/session.go` (line 14)

- [ ] **Step 1: Edit the constant**

```diff
-const SessionCookieName = "argos_session"
+const SessionCookieName = "longue_vue_session"
```

- [ ] **Step 2: Run the auth tests**

```bash
go test -count=1 -race ./internal/auth/...
```

Expected: pass. (PAT tests in `tokens_test.go` use `argos_pat_*` literals — they will still pass because Phase 5 hasn't yet introduced dual-prefix support; the existing parser still accepts the legacy prefix.)

### Task 4.7: Update vmcollector filter for argos.io/ignore — DEFERRED to Phase 6

The `argos.io/ignore` annotation read in `internal/vmcollector/filter/filter.go` is renamed in Phase 6 alongside the rest of the annotation domain change. Don't touch it here.

### Task 4.8: Commit Phase 4

- [ ] **Step 1: Build + test**

```bash
go build ./... && go vet ./... && go test -short -count=1 -race ./...
```

Expected: clean.

- [ ] **Step 2: Commit**

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor: rename internal Go strings, headers, MCP name, realm, cookie"
```

---

## Phase 5 — PAT prefix dual-support (TDD)

This is the most delicate phase. We must keep accepting `argos_pat_*` tokens forever (production data), while emitting only `longue_vue_pat_*` for new tokens. Drive it by tests.

### Task 5.1: Read the current PAT parsing code to understand the contract

**Files:**
- Read: `internal/auth/tokens.go`
- Read: `internal/auth/tokens_test.go`

- [ ] **Step 1: Read both files in full**

Use the Read tool on `internal/auth/tokens.go` and `internal/auth/tokens_test.go`. Identify:
- The `TokenScheme` constant
- The `ParseToken` function
- The mint/issue function (likely `MintToken` or `NewToken`)
- How the 8-char prefix is extracted

Note the exact function names and field names you observe — later steps reference them.

### Task 5.2: Write the failing test for dual-prefix verification

**Files:**
- Modify: `internal/auth/tokens_test.go`

- [ ] **Step 1: Add a new test function**

Add at the end of `internal/auth/tokens_test.go`:

```go
func TestParseToken_AcceptsBothSchemes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{"legacy argos_pat_ prefix", "argos_pat_aabbccdd_" + strings.Repeat("x", 32)},
		{"new longue_vue_pat_ prefix", "longue_vue_pat_aabbccdd_" + strings.Repeat("x", 32)},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := ParseToken(tc.raw)
			if err != nil {
				t.Fatalf("ParseToken(%q) returned error: %v", tc.raw, err)
			}
			if parsed.Prefix != "aabbccdd" {
				t.Fatalf("expected prefix %q, got %q", "aabbccdd", parsed.Prefix)
			}
		})
	}
}

func TestMintToken_UsesNewSchemeOnly(t *testing.T) {
	t.Parallel()
	tok, _, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if !strings.HasPrefix(tok.Plaintext, TokenScheme) {
		t.Fatalf("expected plaintext to start with %q, got %q", TokenScheme, tok.Plaintext)
	}
	if strings.HasPrefix(tok.Plaintext, TokenSchemeLegacy) {
		t.Fatalf("MintToken must not emit legacy scheme; got %q", tok.Plaintext)
	}
}
```

(Replace `MintToken`, `tok.Plaintext`, `parsed.Prefix` with the actual identifiers found in Task 5.1 — match the existing API exactly.)

If `strings` is not yet imported in this test file, add `"strings"` to the import block.

- [ ] **Step 2: Run the new tests — they must fail**

```bash
go test -count=1 ./internal/auth -run 'TestParseToken_AcceptsBothSchemes|TestMintToken_UsesNewSchemeOnly' -v
```

Expected: both tests FAIL — `ParseToken` rejects `longue_vue_pat_*`, and `MintToken` still emits `argos_pat_*`. If they pass already (e.g., the constant was already updated by an earlier sed pass), revert and reread Task 5.1.

### Task 5.3: Implement dual-prefix parsing and new-prefix emission

**Files:**
- Modify: `internal/auth/tokens.go`

- [ ] **Step 1: Edit the constants block**

Replace the `TokenScheme` constant with two constants:

```diff
-// Token plaintext format: `argos_pat_<prefix>_<suffix>`
-//   - `argos_pat_` namespaces the value so it's greppable (GitHub's
+// Token plaintext format: `longue_vue_pat_<prefix>_<suffix>`
+// (legacy tokens use `argos_pat_<prefix>_<suffix>` — both are accepted
+// during verification; only the new scheme is emitted by MintToken.)
+//   - the scheme prefix namespaces the value so it's greppable (GitHub's
...
-	TokenScheme      = "argos_pat_"
+	TokenScheme       = "longue_vue_pat_"
+	TokenSchemeLegacy = "argos_pat_"
```

Keep the old block-comment context but adjust it to reflect both schemes.

- [ ] **Step 2: Update `ParseToken` to accept either scheme**

Find the line in `ParseToken` that strips the scheme prefix. Replace the single-prefix logic with:

```go
var stripped string
switch {
case strings.HasPrefix(raw, TokenScheme):
    stripped = strings.TrimPrefix(raw, TokenScheme)
case strings.HasPrefix(raw, TokenSchemeLegacy):
    stripped = strings.TrimPrefix(raw, TokenSchemeLegacy)
default:
    return Token{}, ErrInvalidToken
}
// continue with the existing logic that splits stripped on "_" into prefix + suffix
```

Adapt to whatever the existing function structure is — the goal is: accept either prefix, return the same parsed token (the 8-char prefix and 32-char suffix are unchanged regardless of scheme).

- [ ] **Step 3: Make `MintToken` emit only the new scheme**

If `MintToken` (or the equivalent issuance function) currently builds the plaintext as `TokenScheme + prefix + "_" + suffix`, no code change is needed here once Step 1 redefined `TokenScheme`. Confirm by re-reading the function.

- [ ] **Step 4: Run the new tests — they must pass**

```bash
go test -count=1 ./internal/auth -run 'TestParseToken_AcceptsBothSchemes|TestMintToken_UsesNewSchemeOnly' -v
```

Expected: both PASS.

- [ ] **Step 5: Run the full auth test suite — no regressions**

```bash
go test -count=1 -race ./internal/auth/...
```

Expected: all pass. The existing tests using `argos_pat_*` literals (e.g., `tokens_test.go:79`, `tokens_test.go:95-97`) should still pass because legacy support is preserved.

### Task 5.4: Update other tests that mint tokens (NOT verification tests)

**Files:** any test that creates a token and asserts on its scheme prefix needs review.

- [ ] **Step 1: Find them**

```bash
grep -rln 'argos_pat_' --include='*.go' . | grep -v '/internal/auth/tokens'
```

- [ ] **Step 2: For each file, decide:**
- If the test is checking that `Authorization: Bearer argos_pat_*` is **accepted** (verification path) → leave as-is. This is exactly the legacy-compat we want to preserve.
- If the test is asserting that a freshly-minted token has the `argos_pat_` scheme (i.e., asserting the issuance format) → update the assertion to `longue_vue_pat_`.
- If the test is using a hardcoded mock token value as a fixture → leave as-is (legacy form is still valid).

The default action is **leave it alone**. Only change a test if it asserts on the *output* of token creation.

- [ ] **Step 3: Run the full test suite**

```bash
go test -count=1 -race ./...
```

Expected: all pass.

### Task 5.5: Update UI fixtures emitted-prefix expectations

**Files:**
- Modify: `ui/src/test/fixtures.ts`
- Modify: `ui/src/test/handlers.ts`
- Read first: `ui/src/pages/admin/Tokens.tsx`

- [ ] **Step 1: Read `ui/src/pages/admin/Tokens.tsx` to see how the prefix is displayed**

Identify any UI-side assumption that the prefix is `argos_pa` (the 8 visible chars of `argos_pat_<prefix>`).

If the UI computes the displayed prefix from the full token (substring), no change is needed in the UI. If it hardcodes `argos_pa`, change to `longue_v` (the first 8 chars of `longue_vue_pat_*`).

- [ ] **Step 2: Update fixtures**

In `ui/src/test/fixtures.ts`, the `prefix: 'argos_pa'` literal should be reviewed:
- If it's the **stored** 8-char prefix (the random hex part of the token), the value should look like `'aabbccdd'` (random hex) — not `'argos_pa'`. The current value suggests confusion with the scheme prefix; verify by reading the schema definition.
- If it's the displayed **"first 8 chars of full token"**, then for new tokens it would be `'longue_v'`. Update accordingly.

Read the schema first, then make a precise change.

- [ ] **Step 3: Run vitest**

```bash
make ui-check && cd ui && npx vitest run --silent && cd ..
```

Expected: pass.

### Task 5.6: Commit Phase 5

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "feat(auth): accept legacy argos_pat_ tokens, emit longue_vue_pat_"
```

---

## Phase 6 — Annotation domain argos.io → longue-vue.io (with DB migration)

### Task 6.1: Update the EOL enricher annotation prefix

**Files:**
- Modify: `internal/eol/enricher.go` (line 18 — constant, plus comments at lines 114, 251, 253, 304, 525)
- Modify: `internal/eol/enricher_test.go`

- [ ] **Step 1: Edit the constant**

```diff
-const annotationPrefix = "argos.io/eol."
+const annotationPrefix = "longue-vue.io/eol."
```

- [ ] **Step 2: Update doc comments in the same file**

```bash
sed -i '' 's|argos.io/eol|longue-vue.io/eol|g' internal/eol/enricher.go
```

- [ ] **Step 3: Update test fixtures**

```bash
sed -i '' 's|argos.io/eol|longue-vue.io/eol|g' internal/eol/enricher_test.go
sed -i '' 's|"argos.io/|"longue-vue.io/|g' internal/eol/enricher_test.go
```

- [ ] **Step 4: Run the enricher tests**

```bash
go test -count=1 -race ./internal/eol/...
```

Expected: pass.

### Task 6.2: Update the vmcollector filter

**Files:**
- Modify: `internal/vmcollector/filter/filter.go`
- Modify: `internal/vmcollector/filter/filter_test.go` (if it exists)

- [ ] **Step 1: Replace `argos.io/ignore` with `longue-vue.io/ignore`**

```bash
grep -rln 'argos\.io/ignore' internal/vmcollector/ | xargs sed -i '' 's|argos\.io/ignore|longue-vue.io/ignore|g'
```

- [ ] **Step 2: Verify**

```bash
grep -rn 'argos\.io' internal/vmcollector/ || echo "OK"
```

Expected: `OK`.

- [ ] **Step 3: Run the filter tests**

```bash
go test -count=1 -race ./internal/vmcollector/...
```

Expected: pass.

### Task 6.3: Update the UI EOL_PREFIX constant and tests

**Files:**
- Modify: `ui/src/pages/EolDashboard.tsx`
- Modify: `ui/src/components/inventory/AnnotationsCard.test.tsx`

- [ ] **Step 1: Edit EolDashboard.tsx**

```diff
-const EOL_PREFIX = 'argos.io/eol.';
+const EOL_PREFIX = 'longue-vue.io/eol.';
```

- [ ] **Step 2: Update the AnnotationsCard test fixture**

```bash
sed -i '' "s|'argos.io/|'longue-vue.io/|g" ui/src/components/inventory/AnnotationsCard.test.tsx
```

- [ ] **Step 3: Run vitest**

```bash
cd ui && npx vitest run --silent && cd ..
```

Expected: pass.

### Task 6.4: Add the JSONB-strip migration

**Files:**
- Create: `migrations/00029_strip_argos_io_annotations.sql`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- +goose StatementBegin

-- Strip every legacy `argos.io/*` key from annotations JSONB columns.
-- The EOL enricher (next tick) repopulates the new `longue-vue.io/eol.*`
-- keys; user-curated keys like `argos.io/ignore` must be re-applied on the
-- source resources by operators per ADR-0020. This migration is irreversible
-- because the `argos.io/*` keys cannot be reconstructed from the surviving
-- columns.

UPDATE clusters
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE namespaces
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE nodes
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE workloads
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE pods
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE services
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE ingresses
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE persistent_volumes
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE persistent_volume_claims
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE virtual_machines
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

UPDATE cloud_accounts
SET annotations = COALESCE((
    SELECT jsonb_object_agg(key, value)
    FROM jsonb_each(annotations)
    WHERE key NOT LIKE 'argos.io/%'
), '{}'::jsonb)
WHERE annotations IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM jsonb_object_keys(annotations) k WHERE k LIKE 'argos.io/%'
  );

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Down is intentionally a no-op: stripped argos.io/* keys cannot be
-- reconstructed. To re-test the migration on a fresh DB, re-run the up.
SELECT 1;
-- +goose StatementEnd
```

- [ ] **Step 2: Verify the migration registers**

```bash
go build ./...
```

Expected: clean build (the embed.go in `migrations/` picks the new file up automatically — confirm by listing).

```bash
ls migrations/ | grep 00029
```

Expected: `00029_strip_argos_io_annotations.sql`.

- [ ] **Step 3: Run the migration test (if `PGX_TEST_DATABASE` is set)**

```bash
go test -count=1 -race ./internal/store/...
```

Expected: pass. The store-level tests should run all goose migrations including the new one without error.

### Task 6.5: Final docs/scripts sweep for `argos.io`

**Files:** any remaining references in `docs/`, `README.md`, `CHANGELOG.md`, `scripts/`.

- [ ] **Step 1: Find them**

```bash
grep -rln 'argos\.io' --include='*.md' --include='*.sh' --include='*.yaml' --include='*.yml' . | grep -v migrations/00029
```

- [ ] **Step 2: Replace**

```bash
grep -rln 'argos\.io' --include='*.md' --include='*.sh' --include='*.yaml' --include='*.yml' . | grep -v migrations/00029 | xargs sed -i '' 's|argos\.io|longue-vue.io|g'
```

- [ ] **Step 3: Verify**

```bash
grep -rn 'argos\.io' --include='*.md' --include='*.sh' . | grep -v migrations/00029 || echo "OK"
```

Expected: `OK`.

### Task 6.6: Commit Phase 6

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(annotations): rename argos.io domain to longue-vue.io with strip migration"
```

---

## Phase 7 — Prometheus metric namespace argos → longue_vue

### Task 7.1: Replace the Prometheus namespace constant in three files

**Files:**
- Modify: `internal/metrics/metrics.go`
- Modify: `internal/ingestgw/metrics.go`
- Modify: `internal/vmcollector/metrics.go`

- [ ] **Step 1: Replace literal `Namespace: "argos"` with `Namespace: "longue_vue"`**

```bash
sed -i '' 's/Namespace:[[:space:]]*"argos"/Namespace: "longue_vue"/g' internal/metrics/metrics.go internal/ingestgw/metrics.go internal/vmcollector/metrics.go
```

- [ ] **Step 2: Verify**

```bash
grep -rn 'Namespace:.*"argos"' internal/ || echo "OK"
```

Expected: `OK`.

### Task 7.2: Update test files that assert on full metric names

**Files:** every `*_test.go` that includes a string like `argos_build_info`, `argos_collector_*`, `argos_ingest_gw_*`, `argos_vm_collector_*`, `argos_impact_*`.

- [ ] **Step 1: Find them**

```bash
grep -rln 'argos_build_info\|argos_collector\|argos_ingest_gw\|argos_vm_collector\|argos_impact' --include='*.go' .
```

- [ ] **Step 2: Replace**

```bash
grep -rln 'argos_build_info\|argos_collector\|argos_ingest_gw\|argos_vm_collector\|argos_impact' --include='*.go' . | xargs sed -i '' \
    -e 's/argos_build_info/longue_vue_build_info/g' \
    -e 's/argos_collector/longue_vue_collector/g' \
    -e 's/argos_ingest_gw/longue_vue_ingest_gw/g' \
    -e 's/argos_vm_collector/longue_vue_vm_collector/g' \
    -e 's/argos_impact/longue_vue_impact/g'
```

- [ ] **Step 3: Verify**

```bash
grep -rn 'argos_build_info\|argos_collector\|argos_ingest_gw\|argos_vm_collector\|argos_impact' --include='*.go' . || echo "OK"
```

Expected: `OK`.

- [ ] **Step 4: Run metric tests**

```bash
go test -count=1 -race ./internal/metrics/... ./internal/ingestgw/... ./internal/vmcollector/...
```

Expected: pass.

### Task 7.3: Update docs that reference argos_* metric names

**Files:**
- Modify: `docs/monitoring.md`
- Modify: any other doc mentioning `argos_*` metrics

- [ ] **Step 1: Find them**

```bash
grep -rln 'argos_build_info\|argos_collector\|argos_ingest_gw\|argos_vm_collector\|argos_impact' --include='*.md' .
```

- [ ] **Step 2: Replace using the same sed expression as Task 7.2 Step 2 but for `*.md`.**

- [ ] **Step 3: Verify**

```bash
grep -rn 'argos_build_info\|argos_collector\|argos_ingest_gw\|argos_vm_collector\|argos_impact' --include='*.md' . || echo "OK"
```

Expected: `OK`.

### Task 7.4: Commit Phase 7

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(metrics): rename Prometheus namespace argos to longue_vue"
```

---

## Phase 8 — Helm charts directory rename and metadata update

### Task 8.1: Move the four chart directories

**Files:**
- Rename: `charts/argos/` → `charts/longue-vue/`
- Rename: `charts/argos-collector/` → `charts/longue-vue-collector/`
- Rename: `charts/argos-vm-collector/` → `charts/longue-vue-vm-collector/`
- Rename: `charts/argos-ingest-gw/` → `charts/longue-vue-ingest-gw/`

- [ ] **Step 1: git mv all four**

```bash
git -c commit.gpgsign=false mv charts/argos charts/longue-vue
git -c commit.gpgsign=false mv charts/argos-collector charts/longue-vue-collector
git -c commit.gpgsign=false mv charts/argos-vm-collector charts/longue-vue-vm-collector
git -c commit.gpgsign=false mv charts/argos-ingest-gw charts/longue-vue-ingest-gw
```

### Task 8.2: Update Chart.yaml `name`, `home`, `sources` for each chart

**Files:**
- Modify: `charts/longue-vue/Chart.yaml`
- Modify: `charts/longue-vue-collector/Chart.yaml`
- Modify: `charts/longue-vue-vm-collector/Chart.yaml`
- Modify: `charts/longue-vue-ingest-gw/Chart.yaml`

- [ ] **Step 1: Replace `name:` field**

```bash
sed -i '' 's|^name: argos$|name: longue-vue|' charts/longue-vue/Chart.yaml
sed -i '' 's|^name: argos-collector$|name: longue-vue-collector|' charts/longue-vue-collector/Chart.yaml
sed -i '' 's|^name: argos-vm-collector$|name: longue-vue-vm-collector|' charts/longue-vue-vm-collector/Chart.yaml
sed -i '' 's|^name: argos-ingest-gw$|name: longue-vue-ingest-gw|' charts/longue-vue-ingest-gw/Chart.yaml
```

- [ ] **Step 2: Replace `home:` and `sources:` GitHub URLs**

```bash
grep -rln 'github.com/sthalbert/Argos\|github.com/sthalbert/argos\b' charts/ | xargs sed -i '' 's|github.com/sthalbert/Argos|github.com/sthalbert/longue-vue|g; s|github.com/sthalbert/argos\b|github.com/sthalbert/longue-vue|g'
```

- [ ] **Step 3: Verify each Chart.yaml is internally consistent**

```bash
for c in charts/longue-vue charts/longue-vue-collector charts/longue-vue-vm-collector charts/longue-vue-ingest-gw; do
    grep '^name:' "$c/Chart.yaml"
done
```

Expected: each prints `name: longue-vue*` matching the directory name.

### Task 8.3: Update labels app.kubernetes.io/name and part-of

**Files:** every `charts/*/templates/*.yaml` and every `charts/*/values.yaml`.

- [ ] **Step 1: Replace label values**

```bash
grep -rln 'app.kubernetes.io/name:[[:space:]]*argos\|app.kubernetes.io/part-of:[[:space:]]*argos' charts/ | xargs sed -i '' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argosd$|app.kubernetes.io/name: longue-vue|' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argos-collector$|app.kubernetes.io/name: longue-vue-collector|' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argos-vm-collector$|app.kubernetes.io/name: longue-vue-vm-collector|' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argos-ingest-gw$|app.kubernetes.io/name: longue-vue-ingest-gw|' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argos$|app.kubernetes.io/name: longue-vue|' \
    -e 's|app.kubernetes.io/part-of:[[:space:]]*argos$|app.kubernetes.io/part-of: longue-vue|'
```

- [ ] **Step 2: Catch remaining template-level `argos` references**

```bash
grep -rn '\bargos\b' charts/
```

Review the results. Anything that's a label, selector, configmap key, or release reference should be renamed. Anything that's an env var (`ARGOS_*`) was already renamed in Phase 3 and shouldn't appear here.

- [ ] **Step 3: Helm lint each chart**

```bash
for c in charts/longue-vue charts/longue-vue-collector charts/longue-vue-vm-collector charts/longue-vue-ingest-gw; do
    helm lint "$c" || { echo "FAIL: $c"; exit 1; }
done
echo "all charts lint clean"
```

Expected: `all charts lint clean`.

### Task 8.4: Commit Phase 8

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(charts): rename Helm charts to longue-vue with updated labels"
```

---

## Phase 9 — Docker images and CI/CD

### Task 9.1: Update Dockerfile labels

**Files:**
- Modify: `Dockerfile`, `Dockerfile.collector`, `Dockerfile.vm-collector`, `Dockerfile.ingest-gw`

- [ ] **Step 1: Find `LABEL` lines referencing argos**

```bash
grep -n 'argos\|Argos' Dockerfile*
```

- [ ] **Step 2: Replace `argos` / `Argos` in LABEL values to `longue-vue` / `longue-vue` (preserving case)**

Use Edit per file with surrounding context. Likely candidates:
- `LABEL org.opencontainers.image.title="Argos"` → `LABEL org.opencontainers.image.title="longue-vue"`
- `LABEL org.opencontainers.image.source="https://github.com/sthalbert/Argos"` → update URL

### Task 9.2: Update charts values.yaml image.repository

**Files:** every `charts/*/values.yaml`.

- [ ] **Step 1: Replace `ghcr.io/sthalbert/argos` and `ghcr.io/argos`**

```bash
grep -rln 'ghcr.io/sthalbert/argos\|ghcr.io/argos' charts/ | xargs sed -i '' \
    -e 's|ghcr.io/sthalbert/argos-collector|ghcr.io/sthalbert/longue-vue-collector|g' \
    -e 's|ghcr.io/sthalbert/argos-vm-collector|ghcr.io/sthalbert/longue-vue-vm-collector|g' \
    -e 's|ghcr.io/sthalbert/argos-ingest-gw|ghcr.io/sthalbert/longue-vue-ingest-gw|g' \
    -e 's|ghcr.io/sthalbert/argos\b|ghcr.io/sthalbert/longue-vue|g' \
    -e 's|ghcr.io/argos/argos-ingest-gw|ghcr.io/sthalbert/longue-vue-ingest-gw|g'
```

(Note the `ghcr.io/argos/argos-ingest-gw` outlier from the explore report — confirm it's still pointing at a valid registry path after this edit, or take a manual decision.)

- [ ] **Step 2: Verify**

```bash
grep -rn 'argos' charts/*/values.yaml || echo "OK"
```

Expected: `OK` (all references should now be longue-vue).

### Task 9.3: Update GitHub Actions workflows

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`
- Modify: `.github/workflows/commit-lint.yml` (only if it has argos refs — likely none)

- [ ] **Step 1: Replace POSTGRES_DB and PGX_TEST_DATABASE**

In `.github/workflows/ci.yml`, change:
- `POSTGRES_DB: argos_test` → `POSTGRES_DB: longue_vue_test`
- `POSTGRES_USER: argos` → `POSTGRES_USER: longue_vue`
- `POSTGRES_PASSWORD: argos` → `POSTGRES_PASSWORD: longue_vue`
- `PGX_TEST_DATABASE: postgres://argos:argos@localhost:5432/argos_test?sslmode=disable` → `PGX_TEST_DATABASE: postgres://longue_vue:longue_vue@localhost:5432/longue_vue_test?sslmode=disable`

Use Edit with surrounding context per occurrence.

- [ ] **Step 2: Replace image tags in release.yml**

In `.github/workflows/release.yml`, every reference to `argos:`, `argos-collector:`, `argos-vm-collector:`, `argos-ingest-gw:` (image tags) becomes `longue-vue:`, `longue-vue-collector:`, etc.

```bash
sed -i '' \
    -e 's|sthalbert/argos-collector|sthalbert/longue-vue-collector|g' \
    -e 's|sthalbert/argos-vm-collector|sthalbert/longue-vue-vm-collector|g' \
    -e 's|sthalbert/argos-ingest-gw|sthalbert/longue-vue-ingest-gw|g' \
    -e 's|sthalbert/argos\b|sthalbert/longue-vue|g' \
    .github/workflows/release.yml
```

- [ ] **Step 3: Catch remaining workflow refs**

```bash
grep -rn 'argos\|Argos' .github/
```

Review the output. Any job names, secret names (`ARGOS_*`), or comments still referencing argos should be renamed. Secrets in GitHub UI must be renamed manually after merge — note this in the PR description.

### Task 9.4: Update deploy/ Kustomize manifests

**Files:** every file under `deploy/`.

- [ ] **Step 1: Find argos refs in deploy/**

```bash
grep -rn 'argos\|Argos' deploy/
```

- [ ] **Step 2: Replace**

```bash
grep -rln 'argos\|Argos' deploy/ | xargs sed -i '' \
    -e 's|ghcr.io/sthalbert/argos-collector|ghcr.io/sthalbert/longue-vue-collector|g' \
    -e 's|ghcr.io/sthalbert/argos-vm-collector|ghcr.io/sthalbert/longue-vue-vm-collector|g' \
    -e 's|ghcr.io/sthalbert/argos-ingest-gw|ghcr.io/sthalbert/longue-vue-ingest-gw|g' \
    -e 's|ghcr.io/sthalbert/argos\b|ghcr.io/sthalbert/longue-vue|g' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argosd$|app.kubernetes.io/name: longue-vue|' \
    -e 's|app.kubernetes.io/name:[[:space:]]*argos$|app.kubernetes.io/name: longue-vue|' \
    -e 's|app.kubernetes.io/part-of:[[:space:]]*argos$|app.kubernetes.io/part-of: longue-vue|' \
    -e 's|name:[[:space:]]*argosd$|name: longue-vue|'
```

- [ ] **Step 3: Verify**

```bash
grep -rn '\bargos\b\|argosd' deploy/ || echo "OK"
```

Expected: `OK` or only false-positive matches (e.g., descriptive comments — review each).

### Task 9.5: Commit Phase 9

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(ci,docker): rename images and workflows to longue-vue"
```

---

## Phase 10 — UI rename

### Task 10.1: Update package.json and HTML title

**Files:**
- Modify: `ui/package.json`
- Modify: `ui/index.html`

- [ ] **Step 1: Edit ui/package.json**

```diff
-  "name": "argos-ui",
+  "name": "longue-vue-ui",
```

- [ ] **Step 2: Edit ui/index.html**

```diff
-<title>Argos CMDB</title>
+<title>longue-vue CMDB</title>
```

### Task 10.2: Update App-level branding strings

**Files:**
- Modify: `ui/src/App.tsx`
- Modify: `ui/src/pages/Login.tsx`
- Modify: `ui/src/api.ts`
- Modify: `ui/src/icons.tsx` (comment only)
- Modify: `ui/src/pages/admin/Tokens.tsx`, `ui/src/pages/admin/CloudAccountDetail.tsx`

- [ ] **Step 1: Find every "Argos" / "argos" / "argosd" string in the UI source**

```bash
grep -rn '\bArgos\b\|\bargosd\b\|argos-collector' ui/src/
```

- [ ] **Step 2: Replace per file with Edit (mostly user-visible text)**

Replace `Argos CMDB` → `longue-vue CMDB`, `Argos` → `longue-vue` (in prose), `argosd` → `longue-vue` (in messages like "argosd logs" → "longue-vue logs"), `argos-collector` → `longue-vue-collector`.

For comments that say "Argos REST API" or "Argos Design System", change to `longue-vue REST API` and `longue-vue Design System`.

- [ ] **Step 3: Verify**

```bash
grep -rn '\bArgos\b\|\bargosd\b\|argos-collector' ui/src/ | grep -v 'argos.io' || echo "OK"
```

(`argos.io` was already renamed in Phase 6; the test above filters that out — it should be empty too if Phase 6 was complete.)

Expected: `OK`.

### Task 10.3: Update UI test fixtures and handlers

**Files:**
- Modify: `ui/src/test/fixtures.ts`
- Modify: `ui/src/test/handlers.ts`

- [ ] **Step 1: Replace image refs**

```bash
sed -i '' 's|ghcr.io/argos/app|ghcr.io/longue-vue/app|g' ui/src/test/fixtures.ts
```

- [ ] **Step 2: PAT prefix in fixtures**

The fixtures may reference `argos_pat_*` mock tokens. Per Phase 5, both prefixes are accepted. New tokens are emitted as `longue_vue_pat_*`. Update fixtures that represent **newly issued** tokens to `longue_vue_pat_*`. Leave any fixture explicitly testing **legacy verification** as `argos_pat_*` (and add a code comment to that effect).

Use Edit with surrounding context.

- [ ] **Step 3: Run vitest**

```bash
make ui-check
cd ui && npx vitest run --silent && cd ..
```

Expected: pass.

### Task 10.4: Verify the UI builds

- [ ] **Step 1: Build the UI bundle**

```bash
make ui-build
```

Expected: produces `ui/dist/`.

- [ ] **Step 2: Verify the embedded daemon still builds**

```bash
make build
```

Expected: produces `bin/longue-vue` containing the embedded UI.

### Task 10.5: Commit Phase 10

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(ui): rename branding, package, and fixtures to longue-vue"
```

---

## Phase 11 — OpenAPI spec

### Task 11.1: Update info block + write the validator test

**Files:**
- Modify: `api/openapi/openapi.yaml` (lines 4–8)
- Modify or Create: `internal/api/openapi_test.go` (or whichever file holds the libopenapi-validator test — search to find it)

- [ ] **Step 1: Read the OpenAPI info block**

```bash
sed -n '1,15p' api/openapi/openapi.yaml
```

- [ ] **Step 2: Edit `info.title` and `info.description`**

```diff
-  title: Argos CMDB API
+  title: longue-vue CMDB API
```

```diff
-  description: |
-    Argos, a CMDB ...
+  description: |
+    longue-vue, a CMDB ...
```

(Edit with the exact existing surrounding context — read the file first to capture the multi-line description verbatim.)

- [ ] **Step 3: Find the existing libopenapi-validator test**

```bash
grep -rln 'libopenapi\|pb33f' --include='*.go' .
```

Per the user's repeated guidance (memory: "any openapi.yaml change ships with a pb33f/libopenapi-validator test for spec + request/response"), there is already at least one test that validates the spec. Extend it (or add one) to assert that the rendered title is `"longue-vue CMDB API"`.

If no such test exists yet (which would itself be a violation of the user's standing rule), surface the gap explicitly to the user **before** proceeding.

- [ ] **Step 4: Run the spec test**

```bash
go test -count=1 -race ./internal/api/...
```

Expected: pass.

### Task 11.2: Commit Phase 11

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "refactor(openapi): rename API title and description to longue-vue"
```

---

## Phase 12 — Documentation, ADRs, README, CHANGELOG

### Task 12.1: Mass-replace in markdown files outside docs/adr/

**Files:** every `*.md` file except `docs/adr/adr-0020-rename-argos-to-longue-vue.md` (already correct) and the `docs/superpowers/plans/2026-04-30-rename-argos-to-longue-vue.md` (this very file — leave it as the historical plan).

- [ ] **Step 1: Replace `Argos` (capitalized product name) → `longue-vue` everywhere**

```bash
find . -name '*.md' \
    -not -path './docs/adr/adr-0020-rename-argos-to-longue-vue.md' \
    -not -path './docs/superpowers/plans/2026-04-30-rename-argos-to-longue-vue.md' \
    -not -path './node_modules/*' \
    -not -path './ui/node_modules/*' \
    | xargs sed -i '' \
        -e 's/Argos CMDB/longue-vue CMDB/g' \
        -e 's/\bArgos\b/longue-vue/g' \
        -e 's/\bargosd\b/longue-vue/g' \
        -e 's/\bargos-collector\b/longue-vue-collector/g' \
        -e 's/\bargos-vm-collector\b/longue-vue-vm-collector/g' \
        -e 's/\bargos-ingest-gw\b/longue-vue-ingest-gw/g'
```

- [ ] **Step 2: Verify**

```bash
grep -rn '\bArgos\b\|\bargosd\b\|argos-collector\|argos-vm-collector\|argos-ingest-gw' --include='*.md' . | grep -v 'docs/adr/adr-0020' | grep -v 'docs/superpowers/plans/2026-04-30' || echo "OK"
```

Expected: `OK`.

### Task 12.2: Repo URLs

**Files:** any file referencing `github.com/sthalbert/Argos` (capital A) or `github.com/sthalbert/argos` (lowercase) outside Go source (Go was Phase 1).

- [ ] **Step 1: Find them**

```bash
grep -rn 'github.com/sthalbert/Argos\|github.com/sthalbert/argos\b' --include='*.md' --include='*.yaml' --include='*.yml' .
```

- [ ] **Step 2: Replace**

```bash
grep -rln 'github.com/sthalbert/Argos\|github.com/sthalbert/argos\b' --include='*.md' --include='*.yaml' --include='*.yml' . | xargs sed -i '' 's|github.com/sthalbert/Argos|github.com/sthalbert/longue-vue|g; s|github.com/sthalbert/argos\b|github.com/sthalbert/longue-vue|g'
```

- [ ] **Step 3: Verify**

```bash
grep -rn 'github.com/sthalbert/Argos\|github.com/sthalbert/argos\b' . || echo "OK"
```

Expected: `OK`.

(GitHub auto-redirects after the user renames the repo to `sthalbert/longue-vue`; until then, the new URL also resolves because the old name is the actual repo name in GitHub. After the manual rename, the old URL keeps redirecting via GitHub's permanent redirect.)

### Task 12.3: Final docs sweep — anything still saying "argos"

- [ ] **Step 1: Sweep**

```bash
grep -rn 'argos' --include='*.md' . | grep -v 'docs/adr/adr-0020' | grep -v 'docs/superpowers/plans/2026-04-30' | grep -v 'longue_vue' | grep -v 'longue-vue'
```

- [ ] **Step 2: Review every result**

Each line is a manual decision:
- Is it a code example (`ARGOS_X` or `argos_pat_x`)? Should be `LONGUE_VUE_X` or `longue_vue_pat_x` after Phase 3 / 5 — check if Phase 3 missed it.
- Is it historical context (e.g., describing the legacy PAT prefix in ADR-0020)? Leave it.
- Is it a dropped reference (e.g., a TODO comment)? Decide case by case.

- [ ] **Step 3: Apply Edits per case**

Use Edit individually — sed is too blunt for the tail end.

### Task 12.4: Commit Phase 12

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false commit -m "docs: rebrand documentation, ADRs, README, CHANGELOG to longue-vue"
```

---

## Phase 13 — Final verification

### Task 13.1: Full local build + test sweep

- [ ] **Step 1: Format + vet + lint + test**

```bash
make check
```

Expected: all green.

- [ ] **Step 2: UI typecheck and tests**

```bash
make ui-check
cd ui && npx vitest run --silent && cd ..
```

Expected: pass.

- [ ] **Step 3: Full UI + Go binary build**

```bash
make ui-build
make build
```

Expected: produces `bin/longue-vue` with the embedded `ui/dist/`.

- [ ] **Step 4: Docker build**

```bash
make docker-build
```

Expected: produces `longue-vue:dev` image.

```bash
docker images | grep longue-vue
```

Expected: a `longue-vue:dev` row.

### Task 13.2: Final string sweep — any stragglers

- [ ] **Step 1: Whole-repo search for the old product name**

```bash
grep -rn 'argos\|Argos\|ARGOS' \
    --exclude-dir=.git \
    --exclude-dir=node_modules \
    --exclude-dir=ui/node_modules \
    --exclude-dir=bin \
    --exclude-dir=ui/dist \
    --exclude='*.lock' \
    . | grep -v 'docs/adr/adr-0020-rename' | grep -v 'docs/superpowers/plans/2026-04-30-rename'
```

- [ ] **Step 2: Inspect each remaining result manually**

Expected categories of legitimate residue:
- Inside `migrations/00029_strip_argos_io_annotations.sql` — references to `argos.io/%` are intentional (they describe what we strip).
- Inside `internal/auth/tokens.go` and `internal/auth/tokens_test.go` — `TokenSchemeLegacy = "argos_pat_"` and tests asserting legacy parsing succeeds. Intentional.
- Inside ADR-0020 and this plan — historical context. Intentional.
- Inside the rename commit messages (visible via `git log --oneline`) — intentional.

If anything else surfaces, decide case by case and apply Edits.

### Task 13.3: Manual smoke test — start the daemon and exercise it

- [ ] **Step 1: Start a local PostgreSQL (if not already running)**

(operator-specific — likely `docker compose up -d postgres` or a brew service)

- [ ] **Step 2: Start the daemon**

```bash
LONGUE_VUE_DATABASE_URL='postgres://longue_vue:longue_vue@localhost:5432/longue_vue?sslmode=disable' \
    LONGUE_VUE_AUTO_MIGRATE=true \
    LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD=changeme \
    ./bin/longue-vue 2>&1 | head -40
```

Expected output should include:
- The `LONGUE-VUE FIRST-RUN BOOTSTRAP` banner if this is a fresh DB.
- A line like `longue-vue listening` (not `argosd listening`).
- The migration `00029_strip_argos_io_annotations.sql` running successfully (it's a no-op on an empty DB but still applies).

Stop the daemon after a few seconds (Ctrl-C).

- [ ] **Step 3: Query metrics**

In another terminal while the daemon is running:

```bash
curl -s http://localhost:8080/metrics | grep -E '^(longue_vue|argos)_' | head -5
```

Expected: lines starting with `longue_vue_*`, no `argos_*`.

- [ ] **Step 4: Test legacy-PAT acceptance**

Mint a new token in the admin UI (it should be `longue_vue_pat_*`). Then construct a fake `argos_pat_*` token and verify that `auth.ParseToken` accepts the format. (This is best done as a unit test if not already present — see Task 5.2.)

### Task 13.4: Commit any final fixups + push the branch

- [ ] **Step 1: Stage any final tweaks**

```bash
git -c commit.gpgsign=false add -A
git -c commit.gpgsign=false status --short
```

- [ ] **Step 2: Commit if there are changes**

```bash
git -c commit.gpgsign=false commit -m "chore: final rename fixups" || echo "(no changes)"
```

- [ ] **Step 3: Push the branch**

```bash
GIT_SSH_COMMAND='ssh -i ~/.ssh/id_ed25519_github -F /dev/null -o IdentitiesOnly=yes' \
    git -c commit.gpgsign=false push -u origin feat/rename-to-longue-vue
```

(Per the user's memory: push via id_ed25519_github.)

### Task 13.5: Open a pull request

- [ ] **Step 1: Create the PR**

```bash
gh pr create \
    --title "refactor: rename product Argos → longue-vue" \
    --body "$(cat <<'EOF'
## Summary
- Renames the product from "Argos" to "longue-vue" across code, charts, docs, ADRs, CI, UI, and Prometheus metric namespace.
- Adds ADR-0020 documenting the decision and the PAT dual-prefix strategy.
- Adds migration 00029 to strip legacy `argos.io/*` keys from all annotation JSONB columns.
- Preserves PAT verification for legacy `argos_pat_*` tokens issued in production; new tokens are emitted as `longue_vue_pat_*`.

## Breaking changes (intentional)
- Helm release names: must redeploy with new chart names (`longue-vue`, `longue-vue-collector`, `longue-vue-vm-collector`, `longue-vue-ingest-gw`).
- Docker images: pull from `ghcr.io/sthalbert/longue-vue*`.
- Environment variables: `ARGOS_*` → `LONGUE_VUE_*`.
- Prometheus metrics: `argos_*` → `longue_vue_*`. Update Grafana dashboards and Prometheus rules.
- Annotation domain: `argos.io/*` → `longue-vue.io/*`. Operator-curated tags on K8s resources must be re-applied.
- Session cookie: existing sessions are invalidated on first request after deploy (8-hour sliding expiry restarts).
- HTTP custom headers: `X-Argos-Verified-*` → `X-Longue-Vue-Verified-*` (gateway and argosd deploy together).

## Non-breaking
- PAT tokens already issued in production keep working forever (legacy `argos_pat_*` prefix accepted in verification path; only emission path changed).

## Test plan
- [ ] `make check` passes
- [ ] `make ui-check` and `vitest` pass
- [ ] `make build` produces `bin/longue-vue`
- [ ] `make docker-build` produces `longue-vue:dev`
- [ ] Manual smoke: start the daemon, see `LONGUE-VUE FIRST-RUN BOOTSTRAP` banner, hit `/metrics`, see `longue_vue_*` metrics
- [ ] Legacy PAT verification still accepts `argos_pat_*` tokens (covered by `TestParseToken_AcceptsBothSchemes`)
- [ ] New PAT issuance emits `longue_vue_pat_*` (covered by `TestMintToken_UsesNewSchemeOnly`)
- [ ] Migration 00029 strips `argos.io/*` keys from a populated DB (manual test on a copy of prod data)

## Operator follow-ups (after merge)
- Rename the GitHub repo `sthalbert/Argos` → `sthalbert/longue-vue` (auto-redirect handles old URLs).
- Update GitHub Actions secret names (`ARGOS_*` → `LONGUE_VUE_*`).
- Re-apply any operator-curated `longue-vue.io/ignore` tags on platform VMs / K8s resources.
- Update Grafana dashboards and Prometheus rules to reference `longue_vue_*` metrics.
EOF
)"
```

Expected: a PR URL is printed.

---

## Self-Review Checklist

(Plan author's pass — do not execute, just verify before handing off.)

### 1. Spec coverage

For each numbered category from the original 18-category cartography, confirm a task covers it:

| # | Category | Covered by |
|---|----------|-----------|
| 1 | Go module path | Task 1.1, 1.2 |
| 2 | Binary names | Task 2.1, 2.2, 2.3 |
| 3 | Env vars `ARGOS_*` | Task 3.1, 3.2, 3.3 |
| 4 | PAT prefix | Task 5.1–5.6 (with TDD) |
| 5 | Annotations `argos.io/*` | Task 6.1–6.6 (incl. migration 00029) |
| 6 | Helm charts | Task 8.1–8.4 |
| 7 | Docker images | Task 9.1, 9.2 |
| 8 | Documentation | Task 12.1–12.4 |
| 9 | OpenAPI spec | Task 11.1–11.2 |
| 10 | Go exported identifiers | n/a — none found in original cartography |
| 11 | UI | Task 10.1–10.5 |
| 12 | Logs and bootstrap banner | Task 4.1 |
| 13 | Tests | distributed across each phase, plus Task 13.2 |
| 14 | CI/CD | Task 9.3 |
| 15 | Scripts | Task 3.3 (env vars), Task 12.1 (text) |
| 16 | Prometheus metrics | Task 7.1–7.4 |
| 17 | MCP server name | Task 4.3 |
| 18 | Other (headers, realm, X-Route-Key, cookie, GitHub URLs) | Task 4.2, 4.4, 4.5, 4.6, 8.2, 12.2 |

All 18 covered.

### 2. Placeholder scan

No "TBD", "TODO", "implement later", "fill in details" in any task body. Test code is shown in full where TDD is required (Task 5.2). Migration SQL is shown in full (Task 6.4). Sed commands are shown in full with explicit before/after grepping.

### 3. Type/identifier consistency

- `TokenScheme` and `TokenSchemeLegacy` referenced consistently (Task 5.3 introduces them; Task 5.2's test references them; Task 5.5 mentions them).
- `SessionCookieName` referenced in Task 4.6 — same constant name used in `internal/auth/middleware.go` (verified via grep at planning time).
- `annotationPrefix` referenced in Task 6.1 — exact constant name.
- `Namespace: "argos"` → `Namespace: "longue_vue"` — exact regex applied identically in all three metric files (Task 7.1).
- Migration filename `00029_strip_argos_io_annotations.sql` referenced consistently in Task 6.4 (creation), Task 13.2 (residue allowance), Task 13.5 (PR description).

No drift detected.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-04-30-rename-argos-to-longue-vue.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Good fit for this plan because each phase is mostly mechanical and benefits from a fresh-context agent that can't accidentally take shortcuts based on earlier conversation. Phase 5 (PAT TDD) is the one that benefits most from a fresh subagent following the test-first discipline.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints. Faster turnaround, but the long context will accumulate noise.

**Which approach?**
