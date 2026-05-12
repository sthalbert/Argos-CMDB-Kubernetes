<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# EOL API Reference

This document is the API reference for the end-of-life (EOL) feature. For background, UI walkthroughs, and operational guidance, see [EOL Enrichment](eol-enrichment.md).

## Overview

longue-vue does **not** expose a dedicated `/v1/eol/*` namespace. EOL data is surfaced through three existing API surfaces:

| Surface | Purpose |
|---------|---------|
| `GET` / `PATCH /v1/admin/settings` | Toggle the enricher on/off at runtime. |
| `GET /v1/clusters/{id}`, `/v1/nodes/{id}`, `/v1/virtual-machines/{id}` (and their list endpoints) | Read EOL data from the entity's `annotations` map. |
| MCP tool `get_eol_summary` | Aggregated EOL counts and per-entity entries for agents. |

All three are subject to the standard auth model (session cookie or `Authorization: Bearer longue_vue_pat_…`). See [Authentication](api-reference.md#authentication).

## Runtime toggle

The enricher reads the `eol_enabled` flag from the `settings` table on every tick. Changes take effect on the next tick — no pod restart required.

### `GET /v1/admin/settings`

**Scope:** `admin`.

```bash
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/admin/settings | jq .
```

```json
{
  "eol_enabled": true,
  "mcp_enabled": false,
  "image_versions_enabled": false,
  "time_travel_enabled": false,
  "time_travel_retention_days": 30,
  "time_travel_reaper_enabled": true,
  "updated_at": "2026-04-24T10:54:34Z"
}
```

### `PATCH /v1/admin/settings`

Merge-patch — omitted fields are left unchanged.

```bash
curl -sS -b /tmp/longue-vue.cookies -X PATCH http://localhost:8080/v1/admin/settings \
  -H 'Content-Type: application/json' \
  -d '{"eol_enabled": true}'
```

| Field | Type | Description |
|-------|------|-------------|
| `eol_enabled` | boolean | Enable or disable the EOL enricher goroutine at runtime. |

**Responses:**

- `200 OK` — returns the full settings object.
- `401 Unauthorized` — no valid session or PAT.
- `403 Forbidden` — caller is not `admin`.
- `400 Bad Request` — malformed JSON. Body is RFC 7807 `application/problem+json`.

> The `admin/settings` endpoints are hand-written and intentionally **not** in `openapi.yaml`. Treat the schema above as authoritative.

## Reading EOL annotations

When the enricher runs, it writes one entry per matched product into the entity's `annotations` map under the key `longue-vue.io/eol.<product>`. The value is a JSON-encoded **string** — clients must parse it.

### Where to read

| Entity | Endpoint | Annotations type |
|--------|----------|------------------|
| Cluster | `GET /v1/clusters/{id}` or `GET /v1/clusters` | `annotations: { [key]: string } \| null` |
| Node | `GET /v1/nodes/{id}` or `GET /v1/nodes` | `annotations: { [key]: string } \| null` |
| Virtual machine | `GET /v1/virtual-machines/{id}` or `GET /v1/virtual-machines` | `annotations: { [key]: string }` |

### Example: extract EOL data from a cluster

```bash
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/clusters/$ID | \
  jq -r '.annotations["longue-vue.io/eol.kubernetes"]' | jq .
```

```json
{
  "product": "kubernetes",
  "cycle": "1.28",
  "eol": "2025-01-28",
  "eol_status": "eol",
  "support": "2024-11-28",
  "latest": "1.28.15",
  "latest_available": "1.32.3",
  "checked_at": "2026-04-24T10:00:00Z"
}
```

A node typically carries several keys (`longue-vue.io/eol.kubernetes`, `longue-vue.io/eol.containerd`, `longue-vue.io/eol.ubuntu`, `longue-vue.io/eol.linux`). A VM carries one key per declared `applications[]` entry.

### Write semantics (for clients merging annotations)

- **Clusters and nodes:** the enricher overwrites only `longue-vue.io/eol.*` keys. All other annotations are preserved.
- **VMs:** on every tick the enricher **reaps** every existing `longue-vue.io/eol.*` key and rewrites the set from the current `applications[]` list. Annotations under other keys (operator metadata, owner team, custom tags) are preserved untouched.
- **No-op writes are skipped** on VMs to keep audit-log volume bounded.

If you PATCH an entity's `annotations` directly, do not write `longue-vue.io/eol.*` keys — the enricher owns that namespace and will overwrite or reap your value.

## Annotation schema

The value stored under each `longue-vue.io/eol.<product>` key is a JSON-encoded string with this shape:

| Field | Type | Description |
|-------|------|-------------|
| `product` | string | endoflife.date product identifier (e.g. `kubernetes`, `containerd`, `ubuntu`, `vault`). |
| `cycle` | string | Matched major.minor (or major) release cycle (e.g. `1.28`, `22.04`, `15`). |
| `eol` | string | EOL date in `YYYY-MM-DD`. Omitted when the product has no fixed EOL date. |
| `eol_status` | enum | One of `eol`, `approaching_eol`, `supported`, `unknown`. See below. |
| `support` | string | End of active support date in `YYYY-MM-DD`. Omitted when not published. |
| `latest` | string | Latest patch version for the entity's current cycle (e.g. `1.28.15`). |
| `latest_available` | string | Latest version of the product overall (newest cycle's latest patch). |
| `checked_at` | string | RFC 3339 UTC timestamp of the last enrichment check. |

### `eol_status` semantics

| Value | Meaning |
|-------|---------|
| `eol` | `now > eol`, or endoflife.date reports `eol: true` with no date. |
| `approaching_eol` | `now > (eol - LONGUE_VUE_EOL_APPROACHING_DAYS)` and `now <= eol`. Default window is 90 days. |
| `supported` | `now < (eol - approaching_days)`, or endoflife.date reports `eol: false`. |
| `unknown` | Product is not on endoflife.date, or the declared version cannot be parsed into a cycle. Only produced for VM applications — clusters and nodes simply skip unmatched fields. |

## Product coverage

The enricher derives `(product, cycle)` from typed entity fields. Versions that don't match any pattern are silently skipped for clusters and nodes, and produce an `unknown` stub for VM applications.

| Entity | Source field | Products | Cycle extraction |
|--------|--------------|----------|------------------|
| Cluster | `kubernetes_version` | `kubernetes` | `v?MAJOR.MINOR` |
| Node | `kubelet_version` | `kubernetes` | `v?MAJOR.MINOR` |
| Node | `container_runtime_version` | `containerd`, `cri-o`, `docker` (any `runtime://version` prefix) | scheme prefix + `MAJOR.MINOR` |
| Node | `os_image` | `ubuntu`, `debian`, `alpine`, `rhel`, `rocky-linux`, `alma-linux`, `amazon-linux`, `centos`, `fedora`, `oracle-linux`, `opensuse`, `sles`, `flatcar`, `cos` | distro-specific regex; first match wins |
| Node | `kernel_version` | `linux` | leading `MAJOR.MINOR` |
| Virtual machine | `applications[].product` + `applications[].version` | Any product on endoflife.date | strip leading `v`/`V`, strip `-`/`+`/space suffix; try `MAJOR.MINOR` then `MAJOR` |

VM product names are normalized at write **and** read time: trim, lowercase, runs of whitespace / `_` / `-` collapsed to a single `-`. See [VM Applications](vm-applications.md).

## MCP tool: `get_eol_summary`

Available when `mcp_enabled=true` in settings. Aggregates EOL status across all clusters, nodes, and non-terminated VMs.

**Input:** none.

**Output:**

```json
{
  "total_clusters": 3,
  "total_nodes": 27,
  "total_virtual_machines": 12,
  "eol": 4,
  "approaching_eol": 7,
  "supported": 28,
  "unknown": 3,
  "entries": [
    {
      "id": "5f1b…",
      "name": "prod-eu-west-1",
      "type": "cluster",
      "product": "kubernetes",
      "status": "approaching_eol",
      "eol_date": "2026-08-28"
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `total_clusters` / `total_nodes` / `total_virtual_machines` | integer | Counts of evaluated entities (terminated VMs excluded). |
| `eol` / `approaching_eol` / `supported` / `unknown` | integer | Entity counts by worst-status across that entity's annotations. |
| `entries[]` | array | One entry per evaluated entity. |
| `entries[].id` | string | UUID. |
| `entries[].name` | string | Display name. |
| `entries[].type` | enum | `cluster`, `node`, or `vm`. |
| `entries[].product` | string | The annotation product driving this entry's status. Omitted for `unknown`. |
| `entries[].status` | enum | Same vocabulary as `eol_status` above. |
| `entries[].eol_date` | string | `YYYY-MM-DD`. Omitted when no fixed EOL date. |

See [MCP Server](mcp-server.md) for auth, scope, and transport details.

## Prometheus metrics

Exposed on the unauthenticated `/metrics` endpoint.

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_eol_enrichments_total` | counter | `cluster`, `resource`, `status` | Annotations written per tick. `resource ∈ {cluster, node, vm}`. `status` is the resulting `eol_status`. |
| `longue_vue_eol_errors_total` | counter | `cluster`, `resource`, `phase` | Enrichment failures. `phase ∈ {list, resolve, update}`. |
| `longue_vue_eol_last_run_timestamp_seconds` | gauge | — | Unix timestamp of the last completed tick. |

Freshness alert:

```
time() - longue_vue_eol_last_run_timestamp_seconds > 600
```

## Configuration

Environment variables on the longue-vue Deployment. See [Configuration Reference](configuration.md) for the full table.

| Variable | Default | Description |
|----------|---------|-------------|
| `LONGUE_VUE_EOL_ENABLED` | — | Seeds the `eol_enabled` row on first boot. The runtime PATCH overrides it thereafter. |
| `LONGUE_VUE_EOL_INTERVAL` | `2m` | Time between ticks. Go duration syntax. |
| `LONGUE_VUE_EOL_APPROACHING_DAYS` | `90` | Window before EOL that flips status to `approaching_eol`. |
| `LONGUE_VUE_EOL_BASE_URL` | `https://endoflife.date` | Override for internal mirrors / air-gapped environments. |

## Errors and edge cases

- **Toggle off:** when `eol_enabled=false`, the goroutine still runs but every tick is a no-op — no outbound HTTP, no annotation writes. Existing annotations are left in place (they are not reaped on disable).
- **endoflife.date unreachable / non-200:** the tick logs at WARN and continues with the next entity; `longue_vue_eol_errors_total{phase="resolve"}` increments. No annotation is written; the previous value remains.
- **Product not on endoflife.date:** for clusters and nodes, the field is silently skipped. For VM applications, a stub annotation with `eol_status: unknown` is written so auditors can see the row was evaluated.
- **Version unparseable:** same as above — silent skip for cluster/node, `unknown` stub for VM.
- **Terminated VMs** (`terminated_at != null`) are skipped entirely. Their existing annotations are preserved.
- **Duplicate normalized products on one VM** (e.g. two `applications[]` entries that both normalize to `nginx`): last write wins in list order. Only one annotation will exist after the tick.
- **Direct PATCH on `annotations`:** clients may merge their own keys, but any `longue-vue.io/eol.*` key written by a client will be overwritten or reaped on the next tick.

## See also

- [EOL Enrichment](eol-enrichment.md) — concepts, UI, operational guidance.
- [VM Applications](vm-applications.md) — declaring the `applications[]` field that drives VM enrichment.
- [API Reference](api-reference.md) — auth, pagination, error format.
- [MCP Server](mcp-server.md) — agent integration.
- [ADR-0012](adr/adr-0012-eol-enrichment-via-endoflife-date.md) — design rationale.
- [ADR-0019](adr/adr-0019-vm-applications-and-eol-and-search.md) — VM applications and per-VM EOL.
