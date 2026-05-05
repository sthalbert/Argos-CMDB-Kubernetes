# MCP VM Coverage — Spec

**Date:** 2026-05-04
**Branch:** `feat/mcp-vm-coverage` (separate from `feat/mcp-security-hardening`)
**Related ADRs:** ADR-0014 (MCP), ADR-0015 (vm-collector / virtual_machines), ADR-0019 (VM applications)

## Problem

The MCP server (ADR-0014) ships 17 read-only tools covering the Kubernetes side of the CMDB (clusters, namespaces, nodes, workloads, pods, services, ingresses, PVs, PVCs, plus impact + EOL + image search). The non-Kubernetes platform inventory introduced by ADR-0015 (`virtual_machines`, `cloud_accounts`) and extended by ADR-0019 (per-VM `applications` list, EOL enrichment for VM apps) is **not exposed to AI agents**. An operator asking "which VMs are running an EOL Vault version?" or "show me everything in cloud account `prod-eu`" cannot answer through MCP today.

## Goal

Extend the MCP tool set so AI agents can interrogate the VM/cloud-account portion of the CMDB with the same read-only, scope-checked, audit-logged posture the K8s tools use.

## In scope

New tools:

| Tool | Maps to | Purpose |
|---|---|---|
| `list_virtual_machines` | `Store.ListVirtualMachines` | Filterable VM list (cloud_account_id/name, region, role, power_state, name, image, application, application_version, include_terminated). Paginated, capped. |
| `get_virtual_machine` | `Store.GetVirtualMachine` | Single VM by id, including `applications` and curated metadata. |
| `list_cloud_accounts` | `Store.ListCloudAccounts` | All cloud-provider accounts with status (`pending_credentials` / `active` / `error` / `disabled`), heartbeat, owner/criticality. **Never** returns `access_key` or `secret_key_*`. |
| `get_cloud_account` | `Store.GetCloudAccount` | Single account, same redaction. |
| `list_vm_applications_distinct` | `Store.ListDistinctVMApplications` | Cascading product → versions list (drives "find VMs running X v1.2.3" workflows). |

Extensions to existing tools:

- `get_eol_summary` — add VM bucket alongside cluster/node summaries (covers ADR-0019 VM-application EOL enrichment).
- `search_images` — already supports image substring; add a VM section to results so the existing tool answers "image-or-application" search the same way the UI does (`/ui/search/image`, ADR-0019).

## Out of scope

- Mutating tools (create/edit/disable VMs or accounts) — MCP is read-only by ADR-0014 and stays that way.
- Plaintext credential exposure — `GET /credentials` semantics (vm-collector scope only) do not apply through MCP. The `read` scope must never see SK.
- Helm/ADR updates beyond a one-line note in ADR-0014's tool catalog.

## Security requirements

- All new tools gated on `read` scope via `auth.HasScope` (NEG-001 from ADR-0014: vm-collector scope must NOT be accepted on MCP — covered by `mcpScopeAllowed` from the security-hardening branch).
- `list_cloud_accounts` / `get_cloud_account` responses MUST strip `access_key`, `secret_key_encrypted`, `secret_key_nonce`, `secret_key_kid`. Add a unit test that asserts the JSON response contains none of those keys.
- All calls flow through `recordToolCall` / `finishDeferred` (audit + rate limit + panic recovery) introduced in `feat/mcp-security-hardening`. Sensitive args (`name`, `image`, `application`) already in `sensitiveArgKeys`.
- Result size: reuse `maxTotalItems=1000` cap shared with the K8s list tools.

## Dependencies

- **Blocked on:** `feat/mcp-security-hardening` merging first. The credential-redaction test relies on the new audit recorder + auth helpers; the rate limiter and TLS posture should land before adding more attack surface.
- **Not blocked:** Store methods all exist (`internal/store/pg_virtual_machines.go`, `pg_cloud_accounts.go`).

## Testing

- Unit: per-tool happy-path + scope-denial + credential-redaction (cloud accounts).
- Integration: extend `internal/mcp/e2e_test.go` with one VM-tool subtest verifying audit row + denial-on-vm-collector-token.
- No new migrations.

## Plan (next step)

After this spec is reviewed, draft `docs/superpowers/plans/2026-05-04-mcp-vm-coverage.md` with TDD-shaped tasks: one task per new tool + one for the two extensions + one for the e2e. Estimate ~6 tasks, smaller than the security-hardening plan.
