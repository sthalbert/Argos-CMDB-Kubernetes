---
title: "ADR-0014: MCP server for AI-driven CMDB queries"
status: "Proposed"
date: "2026-04-25"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "mcp", "ai", "integration", "api"]
supersedes: ""
superseded_by: ""
---

# ADR-0014: MCP server for AI-driven CMDB queries

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

longue-vue exposes a REST API that humans use via the web UI and machines use via bearer tokens. However, the growing adoption of AI assistants (Claude, ChatGPT, Copilot) in operations teams creates a new interaction pattern: operators want to ask natural-language questions about their Kubernetes inventory.

Examples of questions operators ask today that require manual navigation:

- "Which clusters are running end-of-life software?"
- "What pods run on node worker-03 and what workloads own them?"
- "Show me the blast radius if I drain node ip-10-34-0-245"
- "Which namespaces have no owner assigned?"
- "List all StatefulSets across all clusters with their PVC bindings"

The [Model Context Protocol (MCP)](https://modelcontextprotocol.io) is an open standard that lets AI agents discover and call tools exposed by servers. By embedding an MCP server in longue-vue, any MCP-compatible AI client can query the CMDB without custom integration code.

SecNumCloud chapter 8 encourages automated asset interrogation. An MCP server makes the CMDB queryable by AI assistants while respecting the existing auth model.

## Decision

**Embed an MCP server in longue-vue** that exposes the CMDB inventory as read-only tools. The server runs as an additional listener alongside the HTTP API, using the stdio or SSE transport. AI agents authenticate with the same bearer tokens (PATs) used by machines today.

### Transport

The MCP server supports two transports:

| Transport | Use case | Configuration |
|-----------|----------|---------------|
| **stdio** | Local AI agents (Claude Code, Copilot) running on the same machine or connected via SSH | Default when `LONGUE_VUE_MCP_TRANSPORT=stdio` |
| **SSE** (Server-Sent Events over HTTP) | Remote AI agents connecting over the network | `LONGUE_VUE_MCP_TRANSPORT=sse` on a separate port (`LONGUE_VUE_MCP_ADDR`, default `:8090`) |

SSE transport reuses the existing bearer token auth middleware. stdio transport inherits the caller's identity from the environment (the operator who launched the AI agent is responsible for the token).

### Tools

Each CMDB query maps to an MCP tool. Tools are read-only — no mutations. The tool set mirrors the REST API's GET endpoints but is designed for AI consumption (structured descriptions, typed parameters, concise responses):

| Tool | Parameters | Returns |
|------|-----------|---------|
| `list_clusters` | `name?` | Cluster list with version, provider, region, EOL status |
| `get_cluster` | `id` | Full cluster detail including curated metadata |
| `list_nodes` | `cluster_id?` | Node list with role, status, instance type, capacity |
| `get_node` | `id` | Full node detail including conditions, taints, EOL |
| `list_namespaces` | `cluster_id?` | Namespace list with phase, owner, criticality |
| `get_namespace` | `id` | Full namespace detail |
| `list_workloads` | `namespace_id?`, `kind?`, `image?` | Workload list with replicas, containers |
| `get_workload` | `id` | Full workload detail |
| `list_pods` | `namespace_id?`, `node_name?`, `workload_id?`, `image?` | Pod list with phase, node, workload |
| `get_pod` | `id` | Full pod detail with containers |
| `list_services` | `namespace_id?` | Service list with type, ports, LB addresses |
| `list_ingresses` | `namespace_id?` | Ingress list with rules, TLS, LB |
| `list_persistent_volumes` | `cluster_id?` | PV list with capacity, phase, CSI driver |
| `list_persistent_volume_claims` | `namespace_id?` | PVC list with phase, bound volume |
| `get_impact_graph` | `entity_type`, `id`, `depth?` | Impact analysis graph (nodes + edges) |
| `get_eol_summary` | — | Aggregated EOL status counts across all clusters |
| `search_images` | `query` | Workloads and pods matching a container image substring |

Each tool returns structured JSON that the AI agent can interpret and present to the user in natural language.

### Resources

MCP resources expose static/slow-changing reference data:

| Resource | URI | Description |
|----------|-----|-------------|
| `longue-vue://schema` | — | CMDB entity types, their fields, and relationships |
| `longue-vue://eol-products` | — | List of products tracked for EOL enrichment |

### Authentication

- **SSE transport**: The MCP client sends `Authorization: Bearer longue_vue_pat_...` in the HTTP headers. The existing auth middleware validates the token and extracts scopes. All MCP tools require `read` scope.
- **stdio transport**: The token is passed via `LONGUE_VUE_MCP_TOKEN` environment variable. The server validates it on startup and uses its scopes for all subsequent tool calls.

No new auth mechanism is introduced. The existing PAT system (ADR-0007) covers both human and AI callers.

### Architecture

```
AI Agent (Claude Code, Copilot, etc.)
    |
    | MCP protocol (stdio or SSE)
    v
┌────────────────────────────────┐
│           longue-vue               │
│                                │
│  ┌──────────┐  ┌────────────┐ │
│  │ HTTP API │  │ MCP Server │ │
│  │ (:8080)  │  │ (:8090/io) │ │
│  └────┬─────┘  └─────┬──────┘ │
│       │               │       │
│       v               v       │
│  ┌──────────────────────────┐ │
│  │     Store (PostgreSQL)   │ │
│  └──────────────────────────┘ │
└────────────────────────────────┘
```

The MCP server is a separate goroutine in longue-vue (same pattern as the collector and EOL enricher). It shares the same `api.Store` — no new database queries needed. Each MCP tool is a thin wrapper that calls the existing store methods and formats the response.

### Configuration

```
LONGUE_VUE_MCP_ENABLED=true                  # seeds the DB setting on startup (default: false)
LONGUE_VUE_MCP_TRANSPORT=sse                 # default: stdio
LONGUE_VUE_MCP_ADDR=:8090                    # default: :8090 (SSE only)
LONGUE_VUE_MCP_TOKEN=longue_vue_pat_...           # required for stdio transport
```

Disabled by default so existing deployments are unaffected.

### Runtime toggle (Admin panel)

The MCP server is controlled at runtime via the `mcp_enabled` setting in the `settings` table — the same single-row table used by the EOL enricher toggle (`eol_enabled`). Admins toggle it from **Admin > Settings** in the web UI without restarting longue-vue.

- `LONGUE_VUE_MCP_ENABLED` env var **seeds** the database setting on startup (same semantics as `LONGUE_VUE_EOL_ENABLED`). The admin UI overrides it at runtime.
- When disabled via the admin panel, the MCP server rejects all tool calls with an error ("MCP server is disabled by administrator") but keeps the listener alive — no restart needed to re-enable.
- `GET /v1/admin/settings` and `PATCH /v1/admin/settings` are extended with the `mcp_enabled` field alongside the existing `eol_enabled`.
- The settings table migration adds `mcp_enabled BOOLEAN NOT NULL DEFAULT FALSE`.

This gives admins full control: enable MCP for a demo, disable it after an audit, re-enable for daily operations — all without touching the deployment.

### Rate limiting and safety

- All tools are read-only. No mutations are possible through MCP.
- The `read` scope is enforced on every tool call.
- The existing HTTP server timeouts and body size limits apply to SSE transport.
- Tool responses are capped at 200 items (same as REST API pagination limit).
- Prometheus metrics: `longue_vue_mcp_tool_calls_total{tool}`, `longue_vue_mcp_tool_duration_seconds{tool}`.

## Consequences

### Positive

- **POS-001**: Operators can query the CMDB in natural language from any MCP-compatible AI assistant without learning the REST API.
- **POS-002**: No new auth mechanism — existing PATs work for both human and AI callers.
- **POS-003**: Read-only by design — AI agents cannot modify the CMDB, only query it.
- **POS-004**: The MCP server reuses the existing store layer — no new database queries, no data duplication.
- **POS-005**: SecNumCloud auditors can use AI assistants to interrogate the CMDB during audits, with full audit trail via the existing token-based identity.

### Negative

- **NEG-001**: Adds a new listener (SSE transport) that must be secured. Mitigated by requiring bearer token auth and binding to a configurable address.
- **NEG-002**: Adds a Go dependency on an MCP SDK. Mitigated by choosing a well-maintained library (`github.com/mark3labs/mcp-go` or equivalent).
- **NEG-003**: AI agents may generate high query volumes if not rate-limited. Mitigated by the existing 200-item pagination cap and the per-token rate limiting already in place for the REST API.

## Alternatives Considered

### Expose the REST API directly to AI agents

- **Description**: AI agents call the REST API endpoints directly without MCP.
- **Rejection reason**: AI agents need tool discovery (what can I query?) and structured parameter descriptions to work effectively. The REST API lacks the semantic metadata that MCP provides. MCP also enables stdio transport for local agents without network configuration.

### Build a separate MCP proxy service

- **Description**: A standalone binary that connects to longue-vue's REST API and exposes MCP tools.
- **Rejection reason**: Adds a second deployment target, a second token to manage, and network round-trips. The in-process approach (same as collector and EOL enricher) is simpler and consistent with the existing architecture.

### Use OpenAI function calling format instead of MCP

- **Description**: Expose tools in OpenAI's function calling JSON schema format.
- **Rejection reason**: Vendor-specific. MCP is an open standard supported by multiple AI providers. OpenAI function calling can be mapped to MCP tools by the AI client — we don't need to support it directly.

## Implementation Notes

- **IMP-001**: Create `internal/mcp/` package. `Server` struct with `Run(ctx)` method (same pattern as collector and EOL enricher). Uses the existing `api.Store` interface.
- **IMP-002**: Add `github.com/mark3labs/mcp-go` (or `github.com/anthropics/mcp-go` if available) to `go.mod` for MCP protocol handling.
- **IMP-003**: Each tool is a function registered with the MCP server. Tool implementations are thin wrappers around existing store methods.
- **IMP-004**: Wire the MCP server in `cmd/longue-vue/main.go` behind `LONGUE_VUE_MCP_ENABLED`. Start after the store is ready, stop on context cancellation.
- **IMP-005**: Add Prometheus metrics: `longue_vue_mcp_tool_calls_total{tool}`, `longue_vue_mcp_tool_duration_seconds{tool}`.
- **IMP-006**: Tests: unit tests for each tool with a fake store, integration test that starts the MCP server and calls tools via the SDK client.

## References

- **REF-001**: Model Context Protocol specification — `https://modelcontextprotocol.io`
- **REF-002**: MCP Go SDK — `https://github.com/mark3labs/mcp-go`
- **REF-003**: ADR-0007 — Dual-path auth and RBAC (bearer tokens)
- **REF-004**: ADR-0013 — Impact analysis graph (reused as MCP tool)
- **REF-005**: ADR-0012 — EOL enrichment (EOL summary exposed as MCP tool)
