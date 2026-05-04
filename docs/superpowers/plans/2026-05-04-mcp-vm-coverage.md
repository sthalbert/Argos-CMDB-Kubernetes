# MCP VM Coverage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Expose `virtual_machines`, `cloud_accounts`, and per-VM applications to AI agents through the MCP tool set with the same read-only, scope-checked, audit-logged posture as the K8s tools.

**Architecture:** Five new tools + two extensions in `internal/mcp/tools.go`, registered alongside the existing 17. All flow through `recordToolCall` / `finishDeferred` (audit + rate limit + panic recovery from `feat/mcp-security-hardening`). Cloud-account responses redact AK/SK fields. Spec: `docs/superpowers/specs/2026-05-04-mcp-vm-coverage.md`.

**Tech Stack:** Go 1.23, `mark3labs/mcp-go`, existing `api.Store` methods (no new store work).

---

## Files

- Modify: `internal/mcp/tools.go` — register 5 new tools + extend 2; add 7 handler funcs.
- Modify: `internal/mcp/audit.go` — add `cloud_account_id`, `provider_vm_id` to `sensitiveArgKeys` (NO — UUIDs/opaque IDs are not sensitive; product names are. Add `product` instead, since application filter values can be PII-adjacent. Reconsider: product names are public software identifiers like "vault", "nginx" — not sensitive. **Decision: no changes to sensitiveArgKeys** — `name` and `image` are already covered.)
- Create: `internal/mcp/redact.go` — `redactCloudAccount(CloudAccount) CloudAccount` strips `AccessKey` (sets to nil). Tiny file because the redaction must be auditable in one place.
- Create: `internal/mcp/vm_tools_test.go` — unit tests per new tool (happy path + redaction + scope-denial via store fake).
- Modify: `internal/mcp/e2e_test.go` — add `TestE2E_VMTools` subtest verifying audit row + denial-on-vm-collector.
- Modify: `internal/mcp/tools_test.go` (or eol_test) — extend EOL summary test to assert VM bucket present.

## Task 1: Cloud account redaction helper

**Files:**
- Create: `internal/mcp/redact.go`
- Create: `internal/mcp/redact_test.go`

- [ ] Step 1: Write failing test asserting `AccessKey` is nilled and all other fields preserved.
- [ ] Step 2: Implement `redactCloudAccount(in api.CloudAccount) api.CloudAccount` that copies the struct and sets `AccessKey = nil`.
- [ ] Step 3: Run test, commit.

```go
// redact.go
package mcp

import "github.com/sthalbert/longue-vue/internal/api"

// redactCloudAccount returns a copy with credential fields stripped.
// MCP exposes cloud accounts at the read scope; AK/SK never leave the
// vm-collector path (ADR-0015 §5).
func redactCloudAccount(in api.CloudAccount) api.CloudAccount {
    in.AccessKey = nil
    return in
}
```

(The `CloudAccount` struct already omits SK fields entirely — only `AccessKey` is plaintext. Verify import path against existing `tools.go`.)

## Task 2: `list_cloud_accounts` tool

**Files:**
- Modify: `internal/mcp/tools.go`
- Create/extend: `internal/mcp/vm_tools_test.go`

- [ ] Step 1: Write failing handler test against `fakeStore` returning two accounts; assert response is JSON array, `access_key` field absent in each entry.
- [ ] Step 2: Register tool in `registerTools()` with optional `cursor` (string) and `limit` (number, default 100, cap 200) params.
- [ ] Step 3: Implement `handleListCloudAccounts` mirroring `handleListClusters`: call `s.store.ListCloudAccounts(ctx, limit, cursor)`, map each through `redactCloudAccount`, marshal `{items, next_cursor}`.
- [ ] Step 4: Wrap with `recordToolCall` / `finishDeferred` exactly as the existing list tools.
- [ ] Step 5: Run test, commit.

## Task 3: `get_cloud_account` tool

**Files:** same as Task 2.

- [ ] Step 1: Write failing test: happy path (returns account, no `access_key` in JSON) + not-found (returns `NewToolResultError` with masked message).
- [ ] Step 2: Register tool with required `id` (uuid string) param.
- [ ] Step 3: Implement `handleGetCloudAccount`: parse uuid → `s.store.GetCloudAccount(ctx, id)` → redact → marshal. Map `api.ErrNotFound` to a generic `"cloud account not found"` (mirror `storeError` pattern from existing handlers).
- [ ] Step 4: Run test, commit.

## Task 4: `list_virtual_machines` tool

**Files:** modify `tools.go` + extend `vm_tools_test.go`.

- [ ] Step 1: Write failing tests for: (a) no filters, (b) filter by `cloud_account_id`, (c) filter by `application` + `application_version`, (d) `include_terminated=true`. Use fakeStore that records the `VirtualMachineListFilter` it received and asserts on it.
- [ ] Step 2: Register tool with optional params: `cloud_account_id` (uuid), `cloud_account_name` (string), `region`, `role`, `power_state`, `name`, `image`, `application`, `application_version`, `include_terminated` (bool), `cursor`, `limit`.
- [ ] Step 3: Implement `handleListVirtualMachines`: build `api.VirtualMachineListFilter` from request args (all optional, length-cap each string at 100 like the REST handler), call `s.store.ListVirtualMachines(ctx, filter, limit, cursor)`, marshal `{items, next_cursor}`. No redaction needed — VMs don't carry secrets.
- [ ] Step 4: Run tests, commit.

## Task 5: `get_virtual_machine` tool

- [ ] Step 1: Write failing test for happy path + not-found.
- [ ] Step 2: Register tool with required `id` (uuid string) param.
- [ ] Step 3: Implement `handleGetVirtualMachine`: parse uuid → `s.store.GetVirtualMachine` → marshal full struct (applications included).
- [ ] Step 4: Run tests, commit.

## Task 6: `list_vm_applications_distinct` tool

- [ ] Step 1: Write failing test: fakeStore returns two products with versions; assert MCP response carries `{products: [{product, versions}]}`.
- [ ] Step 2: Register tool (no params).
- [ ] Step 3: Implement `handleListVMApplicationsDistinct`: call `s.store.ListDistinctVMApplications(ctx)`, marshal `{products: items}`.
- [ ] Step 4: Run tests, commit.

## Task 7: Extend `search_images` to include VMs

**Files:** modify `tools.go` + add subtest in existing search_images test.

- [ ] Step 1: Write failing test asserting that when fakeStore has a VM matching by `image_name` substring, the response carries a `virtual_machines` section alongside `pods` / `workloads`.
- [ ] Step 2: In `handleSearchImages`, after the existing pod/workload listing, also call `s.store.ListVirtualMachines(ctx, api.VirtualMachineListFilter{Image: &q}, 100, "")` and include in the response under `virtual_machines`.
- [ ] Step 3: Run tests, commit.

(Backwards compatibility: existing `pods` / `workloads` keys preserved. Adding a key is non-breaking for MCP clients.)

## Task 8: Extend `get_eol_summary` to include VMs

**Files:** modify `tools.go` + extend EOL summary test.

- [ ] Step 1: Write failing test: fakeStore returns one VM with annotation `longue-vue.io/eol.vault` set to `eol`; assert response includes `vms_eol >= 1` (or per-product breakdown — pick what matches existing summary shape).
- [ ] Step 2: Read current `handleGetEOLSummary` to understand shape (cluster + node buckets). Mirror the pattern: list non-terminated VMs, walk `Annotations` for keys prefixed `longue-vue.io/eol.`, count by status.
- [ ] Step 3: Run tests, commit.

## Task 9: E2E composition test

**Files:** modify `internal/mcp/e2e_test.go`.

- [ ] Step 1: Add `TestE2E_VMTools` subtest that:
  - Calls `list_virtual_machines` with a valid `read` token → asserts 1 audit row recorded with `tool="list_virtual_machines"`, `success=true`.
  - Calls `list_cloud_accounts` → asserts the returned JSON does NOT contain the string `"access_key"`.
  - Calls `list_virtual_machines` with a vm-collector-scoped token → asserts denial (401) and audit row with `success=false`, `error="forbidden"`.
- [ ] Step 2: Run `go test -race -tags noui ./internal/mcp/...`, commit.

## Task 10: Final commit + push

- [ ] Step 1: Run full lint + tests: `golangci-lint run --build-tags noui ./internal/mcp/...` and `go test -race -tags noui ./internal/mcp/... ./cmd/longue-vue/...` — both clean.
- [ ] Step 2: Verify ADR-0014 tool-count text is still accurate; if not, bump "17 read-only CMDB tools" to "22" in CLAUDE.md and ADR-0014.
- [ ] Step 3: Single combined push to `feat/mcp-vm-coverage` (do not push automatically — wait for user).
