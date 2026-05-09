<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# MCP Server

longue-vue exposes its CMDB inventory through the [Model Context Protocol](https://modelcontextprotocol.io/) (MCP), allowing AI agents to query clusters, nodes, workloads, and other Kubernetes entities conversationally. The server is strictly read-only and authenticates callers with the same bearer tokens used by the REST API.

## Enable the MCP server

The MCP server is **disabled by default**. An admin enables it from the UI:

1. Sign in as an `admin` user.
2. Navigate to **Admin > Settings**.
3. Click **Enable** on the "MCP server" card.

The server checks the setting on every tool call. No pod restart is required to enable or disable it.

> **Alternative: env var.** Setting `LONGUE_VUE_MCP_ENABLED=true` on the longue-vue Deployment seeds the database setting to `true` on startup. The UI toggle overrides it at runtime.

## Connect from Claude Code

Add the longue-vue MCP server to your Claude Code settings (`.claude/settings.json` or project-level):

```json
{
  "mcpServers": {
    "longue-vue": {
      "type": "sse",
      "url": "https://longue-vue.example.com:8090/sse",
      "headers": {
        "Authorization": "Bearer longue_vue_pat_<prefix>_<suffix>"
      }
    }
  }
}
```

Replace the URL with your longue-vue SSE endpoint and the token with a valid PAT (created under **Admin > Tokens** in the longue-vue UI). Any role with `read` scope works.

## Connect from other MCP clients

Any MCP client that supports the SSE transport can connect. Point it at:

```
https://<longue-vue-host>:8090/sse
```

Set the HTTP header `Authorization: Bearer longue_vue_pat_<prefix>_<suffix>` on the connection. The token must have at least `read` scope.

For the **stdio** transport (local agent on the same machine), set `LONGUE_VUE_MCP_TRANSPORT=stdio` and provide the token via `LONGUE_VUE_MCP_TOKEN`.

## Available tools

All tools are read-only. List tools return up to 1000 items (silently truncated beyond that). There are 25 tools total.

### Kubernetes inventory

| Tool | Parameters | Returns |
|------|-----------|---------|
| `list_clusters` | `name` (optional, substring filter) | All clusters with version, provider, region, EOL status |
| `get_cluster` | `id` (required, UUID) | Single cluster detail |
| `list_nodes` | `cluster_id` (optional, UUID) | Nodes, optionally scoped to a cluster |
| `get_node` | `id` (required, UUID) | Single node detail (role, OS, capacity, conditions) |
| `list_namespaces` | `cluster_id` (optional, UUID) | Namespaces, optionally scoped to a cluster |
| `get_namespace` | `id` (required, UUID) | Single namespace detail |
| `list_workloads` | `namespace_id` (optional, UUID), `kind` (optional: Deployment, StatefulSet, DaemonSet), `image` (optional, substring) | Workloads matching filters |
| `get_workload` | `id` (required, UUID) | Single workload detail (spec, containers, replicas) |
| `list_pods` | `namespace_id` (optional, UUID), `node_name` (optional), `workload_id` (optional, UUID), `image` (optional, substring) | Pods matching filters |
| `get_pod` | `id` (required, UUID) | Single pod detail (phase, containers, node) |
| `list_services` | `namespace_id` (optional, UUID) | Services, optionally scoped to a namespace |
| `list_ingresses` | `namespace_id` (optional, UUID) | Ingresses, optionally scoped to a namespace |
| `list_persistent_volumes` | `cluster_id` (optional, UUID) | PVs, optionally scoped to a cluster |
| `list_persistent_volume_claims` | `namespace_id` (optional, UUID) | PVCs, optionally scoped to a namespace |

### Cross-cutting

| Tool | Parameters | Returns |
|------|-----------|---------|
| `get_impact_graph` | `entity_type` (required), `id` (required, UUID), `depth` (optional, 1-3, default 2) | Upstream and downstream dependency graph |
| `get_eol_summary` | _(none)_ | EOL status counts and per-entity breakdown across all clusters, nodes, and VMs |
| `search_images` | `query` (required, substring) | Workloads and pods running a matching container image |

### Cloud accounts and platform VMs (ADR-0015 / ADR-0019)

| Tool | Parameters | Returns |
|------|-----------|---------|
| `list_cloud_accounts` | _(none)_ | All registered cloud accounts with status |
| `get_cloud_account` | `id` (required, UUID) | Single cloud account detail |
| `list_virtual_machines` | `cloud_account_id` (optional, UUID), `power_state` (optional), `name` (optional, substring), `application` (optional, normalized product) | Platform VMs matching filters |
| `get_virtual_machine` | `id` (required, UUID) | Single VM detail including applications list and EOL annotations |
| `list_vm_applications_distinct` | _(none)_ | Distinct normalized product names across the fleet with their declared version strings |

### Container image versions (ADR-0022)

These tools surface the `image_versions` enrichment store: for each image used in any K8s workload or pod, the latest tag fetched from the source registry, plus per-variant freshness and any registry errors. Companion to the Container images UI under **Tools → Container images**.

| Tool | Parameters | Returns |
|------|-----------|---------|
| `list_image_versions` | `registry` (optional, e.g. `docker.io`), `image_repo` (optional, substring), `variant` (optional, e.g. `alpine`, `debian-12`), `has_error` (optional bool — `true` returns only repos with at least one variant in error, `false` only fully-ok repos) | One entry per `image_repo` with all variants nested |
| `get_image_version` | `image_repo` (required, fully-qualified, e.g. `docker.io/library/nginx`) | All enriched variants for the given repo |
| `get_image_versions_summary` | _(none)_ | Aggregate snapshot: `total_repos`, `total_variants`, `repos_with_errors`, `repos_all_ok`, `variants_with_error`, plus a `by_registry` breakdown and up to 50 sampled errored variants for diagnosis |

Typical agent workflows:

- *"Are any of my container images outdated or failing?"* — call `get_image_versions_summary` first for a one-shot snapshot, then drill into a specific repo with `get_image_version`.
- *"List all Docker Hub images currently failing to enrich."* — `list_image_versions` with `registry=docker.io` + `has_error=true`.
- *"What's the latest available alpine variant of nginx?"* — `get_image_version` with `image_repo=docker.io/library/nginx`, then filter the response to `variant=alpine`.

The enrichment runs in the background (default 24 h cadence, configurable, manual `Refresh now` button in the UI). Repos that have never been queried do not appear in any of these tools — the table only contains repos referenced by at least one workload or pod since the enricher last ticked.

`entity_type` for `get_impact_graph` accepts: `cluster`, `node`, `namespace`, `pod`, `workload`, `service`, `ingress`, `persistentvolume`, `persistentvolumeclaim`.

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LONGUE_VUE_MCP_ENABLED` | -- | Seeds the `mcp_enabled` database setting on startup. The UI toggle overrides it at runtime. |
| `LONGUE_VUE_MCP_TRANSPORT` | `sse` | MCP transport: `sse` or `stdio`. |
| `LONGUE_VUE_MCP_ADDR` | `127.0.0.1:8090` | Listen address for the SSE transport. Ignored when transport is `stdio`. |
| `LONGUE_VUE_MCP_TOKEN` | -- | PAT for stdio transport authentication. Required when transport is `stdio`. |
| `LONGUE_VUE_MCP_TLS_CERT` | -- | Path to a PEM-encoded TLS certificate. When set with `LONGUE_VUE_MCP_TLS_KEY`, the SSE listener terminates TLS. Strongly recommended for production. |
| `LONGUE_VUE_MCP_TLS_KEY` | -- | Path to the private key matching `LONGUE_VUE_MCP_TLS_CERT`. |
| `LONGUE_VUE_MCP_ALLOW_PLAINTEXT` | `false` | When `true`, allows the SSE transport to start without TLS. Intended for local development only; bearer tokens are not protected on the wire. |

## Monitoring

The MCP server exports Prometheus metrics on the `/metrics` endpoint:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_mcp_tool_calls_total` | counter | `tool` | Number of tool invocations, per tool name. |
| `longue_vue_mcp_tool_duration_seconds` | histogram | `tool` | Tool call duration in seconds, per tool name. |

Alert on sustained error rates or slow tool calls:

```
rate(longue_vue_mcp_tool_calls_total[5m]) > 0
  and
histogram_quantile(0.95, rate(longue_vue_mcp_tool_duration_seconds_bucket[5m])) > 5
```

## Security

- **Read-only.** The MCP server exposes no write operations. It cannot create, update, or delete any CMDB entity.
- **Bearer token auth.** Every tool call requires a valid longue-vue PAT with `read` scope. Tokens are validated against the same argon2id-hashed store used by the REST API.
- **Admin toggle.** An administrator can disable the MCP server at runtime from the UI. When disabled, all tool calls are rejected with an error; the listener stays alive so re-enabling does not require a restart.
- **Result cap.** List tools paginate internally but cap total results at 1000 items to prevent memory exhaustion on large clusters. Results beyond the cap are silently truncated.
- **Separate port.** The SSE transport listens on its own port (default `:8090`), independent of the main API on `:8080`. Network policies can restrict MCP access without affecting human users or the collector.

## References

- [ADR-0014](adr/adr-0014-mcp-server.md) — MCP server design, transport options, tool catalogue, auth, and runtime toggle
- [ADR-0022](adr/adr-0022-image-versions-enrichment.md) — Container image versions enrichment (source for `list_image_versions`, `get_image_version`, `get_image_versions_summary`)
- [Container image versions](image-versions.md) — User-facing companion doc covering the UI surfaces and the underlying `image_versions` store
