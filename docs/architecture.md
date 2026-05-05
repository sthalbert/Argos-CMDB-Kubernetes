<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# Architecture

This document explains how longue-vue works internally -- its components, data flow, storage model, and design decisions.

## High-level diagram

```
   Kubernetes cluster A               Kubernetes cluster B (air-gapped)
          |                                       |
          | client-go (list)                      | client-go (list)
          v                                       v
   ┌──────────────┐                      ┌──────────────────┐
   │ pull collector│                      │ longue-vue-collector   │
   │ (goroutine    │                      │ (push binary)     │
   │  in longue-vue)   │                      └────────┬─────────┘
   └──────┬───────┘                               |
          │ direct store calls                     | HTTPS + Bearer token
          │                                        | POST /v1/*, /reconcile
          v                                        v
   ┌─────────────────────────────────────────────────┐
   │                    longue-vue                        │
   │  ┌───────────┐  ┌──────────┐  ┌──────────────┐  │
   │  │ REST API  │  │ Auth     │  │ Audit        │  │
   │  │ (OpenAPI) │  │ (ADR-07) │  │ Middleware   │  │
   │  └─────┬─────┘  └──────────┘  └──────────────┘  │
   │        │                                          │
   │        v                                          │
   │  ┌──────────────────┐   ┌─────────────────────┐  │
   │  │ PostgreSQL Store │   │ Prometheus /metrics  │  │
   │  │ (pgx/v5, goose)  │   └─────────────────────┘  │
   │  └────────┬─────────┘                             │
   │           │                                       │
   └───────────┼───────────────────────────────────────┘
               v
        ┌────────────┐         ┌────────────────────┐
        │ PostgreSQL │         │ Web UI (embedded    │
        │ (JSONB +   │         │  React SPA at /ui/) │
        │  goose     │         └────────────────────┘
        │  migrations│
        └────────────┘
```

## Components

### longue-vue

The main daemon, built from `cmd/longue-vue/main.go`. It combines several subsystems into a single binary:

- **REST API** -- generated from `api/openapi/openapi.yaml` via `oapi-codegen`. Handlers live in `internal/api/`. Errors follow RFC 7807.
- **Auth middleware** -- resolves requests as either session-cookie (humans) or bearer-token (machines), attaching a `Caller{id, kind, role, scopes}` to the context. Implemented in `internal/auth/`.
- **Audit middleware** -- records every state-changing request and every admin-panel read. Sensitive fields are scrubbed. Implemented in `internal/api/audit.go`.
- **Pull collector** -- one goroutine per configured cluster, polling the Kubernetes API at a configurable interval. Implemented in `internal/collector/`.
- **PostgreSQL store** -- cursor-paginated CRUD with merge-patch updates. Implemented in `internal/store/`.
- **EOL enricher** -- background goroutine that annotates clusters, nodes, and platform VMs with lifecycle status from [endoflife.date](https://endoflife.date). Implemented in `internal/eol/`. Toggled at runtime via the `eol_enabled` setting. See [ADR-0012](adr/adr-0012-eol-enrichment-via-endoflife-date.md) and [EOL Enrichment](eol-enrichment.md).
- **Impact analysis** -- on-the-fly FK traversal producing a dependency graph for any CMDB entity. Serves `GET /v1/impact/{entity_type}/{id}`. Implemented in `internal/impact/`. See [ADR-0013](adr/adr-0013-impact-analysis-graph.md) and [Impact Analysis](impact-analysis.md).
- **MCP server** -- Model Context Protocol server exposing 22 read-only CMDB tools for AI agents, over SSE or stdio transports. Implemented in `internal/mcp/`. Toggled at runtime via the `mcp_enabled` setting. See [ADR-0014](adr/adr-0014-mcp-server.md) and [MCP Server](mcp-server.md).
- **Ingest listener** -- optional second mTLS-only listener (`:8443`) for push-mode collectors transiting through a DMZ ingest gateway. Registered via `api.NewIngestMux`. Disabled unless `LONGUE_VUE_INGEST_LISTEN_ADDR` is set. See [ADR-0016](adr/adr-0016-dmz-ingest-gateway.md).
- **Metrics** -- Prometheus counters and gauges at `/metrics`. Implemented in `internal/metrics/`.
- **Embedded UI** -- the React SPA built into the binary via `//go:embed` and served at `/ui/*`.

### longue-vue-collector

A standalone push-mode collector binary, built from `cmd/longue-vue-collector/`. It shares the same `internal/collector` package as the pull collector but writes through an HTTP client (`internal/collector/apiclient`) instead of the direct store interface.

Key differences from the pull collector:

- No database dependency.
- No HTTP server -- it is a pure client.
- Authenticates to longue-vue with a bearer token (PAT).
- Supports gateway/proxy traversal: custom CA, mTLS, path prefix rewrite, extra headers.

See [ADR-0009](adr/adr-0009-push-collector-for-airgapped-clusters.md) for the design rationale.

### longue-vue-vm-collector

A standalone push-mode binary (`cmd/longue-vue-vm-collector/`) that catalogues non-Kubernetes platform VMs (VPN gateways, DNS servers, bastions, Vault clusters). It polls a cloud provider's API (Outscale v1), deduplicates against the `nodes` table server-side, and pushes remaining VMs to `POST /v1/virtual-machines`. Credentials (AK/SK) live in longue-vue's `cloud_accounts` table and are fetched at runtime — never in env vars. One instance per cloud account. See [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) and [vm-collector](vm-collector.md).

### longue-vue-ingest-gw

A stateless DMZ reverse-proxy binary (`cmd/longue-vue-ingest-gw/`) deployed between remote collectors and longue-vue's trusted zone. It enforces a hardcoded 18-route write-only allowlist, verifies bearer PATs against longue-vue via `POST /v1/auth/verify` (with an LRU cache), and forwards approved requests over mTLS to longue-vue's ingest listener. No database, no replay buffer. See [ADR-0016](adr/adr-0016-dmz-ingest-gateway.md) and [How to deploy the DMZ ingest gateway](how-to-deploy-dmz-ingest-gateway.md).

## Pull collector

The pull collector runs as goroutines inside longue-vue. Each goroutine handles one cluster.

### Polling cycle

On each tick (default: 60 seconds), the collector:

1. Fetches the Kubernetes API server version, updating the cluster record's `kubernetes_version`.
2. Lists **nodes** cluster-wide. Upserts by `(cluster_id, name)`. Reads the full field set: role, cloud identity, networking, OS stack, capacity, allocatable, conditions, taints.
3. Lists **PersistentVolumes** cluster-wide. Upserts by `(cluster_id, name)`. Builds a name-to-ID map for PVC linking.
4. Lists **namespaces**. Upserts by `(cluster_id, name)`. Builds a name-to-ID map.
5. For each namespace:
   - Lists **workloads** (Deployments, StatefulSets, DaemonSets via three `AppsV1` calls). Upserts by `(namespace_id, kind, name)`.
   - Lists **pods**. Upserts by `(namespace_id, name)`. Resolves the controlling workload via the ownerReference chain (ReplicaSet -> Deployment, or direct for StatefulSet/DaemonSet).
   - Lists **services**. Upserts by `(namespace_id, name)`. Flattens `status.loadBalancer.ingress[]` into the `load_balancer` JSONB.
   - Lists **ingresses**. Upserts by `(namespace_id, name)`. Flattens rules, TLS, and load-balancer into JSONB.
   - Lists **PersistentVolumeClaims**. Upserts by `(namespace_id, name)`. Resolves `spec.volumeName` against the PV map to set `bound_volume_id`.

### Reconciliation

When `LONGUE_VUE_COLLECTOR_RECONCILE=true` (default), after each successful listing the collector deletes rows that disappeared from the live Kubernetes listing:

- **Cluster-scoped**: nodes and PVs reconcile against `(cluster_id, name)`.
- **Namespace-scoped**: pods, services, ingresses, PVCs reconcile per namespace against `(namespace_id, name)`.
- **Workloads**: reconcile keys on `(namespace_id, kind, name)` so deleting a Deployment named "web" does not affect a StatefulSet also named "web".

Reconciliation only runs after a successful list. A transient Kubernetes API error never wipes the store.

### Multi-cluster

Configured via `LONGUE_VUE_COLLECTOR_CLUSTERS` (JSON array of `{name, kubeconfig}` tuples). One goroutine per entry, all sharing the store. The legacy `LONGUE_VUE_CLUSTER_NAME` + `LONGUE_VUE_KUBECONFIG` works as a single-cluster shortcut.

## Push collector

The push collector (`longue-vue-collector`) runs inside an air-gapped cluster. It performs the same polling cycle as the pull collector but writes observations to longue-vue over HTTPS using the REST API:

- **Upserts** map to `POST /v1/<resource>` (idempotent on the natural key).
- **Reconciliation** maps to `POST /v1/<resource>/reconcile`.

The HTTP client retries transient 5xx errors with exponential backoff (3 attempts max). On 401/403, the collector stops (token revoked or misconfigured).

## Data model

### Entity hierarchy

```
Cluster
├── Node
├── Namespace
│   ├── Workload (Deployment | StatefulSet | DaemonSet)
│   ├── Pod  ──→ Workload (workload_id FK, ON DELETE SET NULL)
│   ├── Service
│   ├── Ingress
│   └── PersistentVolumeClaim  ──→ PersistentVolume (bound_volume_id FK, ON DELETE SET NULL)
└── PersistentVolume
```

The FK chain from `clusters` cascades on delete: deleting a cluster removes all its children. The `workload_id` and `bound_volume_id` FKs use `ON DELETE SET NULL` -- deleting a workload or PV leaves the referencing row to be cleaned up by reconciliation.

### Polymorphic workloads

Workloads use a single `workloads` table with a `kind` discriminator (`Deployment`, `StatefulSet`, `DaemonSet`). Kind-specific details live in the `spec` JSONB column. See [ADR-0003](adr/adr-0003-workload-polymorphism.md).

### JSONB columns

Several columns use PostgreSQL JSONB for semi-structured data:

- `containers` (pods, workloads): `[{name, image, image_id, init}]`
- `load_balancer` (services, ingresses): `[{ip, hostname, ports}]`
- `conditions` (nodes): `[{type, status, reason, message}]`
- `taints` (nodes): `[{key, value, effect}]`
- `spec` (workloads): kind-specific configuration
- `annotations` (clusters): operator-defined key-value pairs

### Curated metadata

Clusters carry operator-editable columns (`owner`, `criticality`, `notes`, `runbook_url`, `annotations`) that the collector never touches. The merge-patch update semantics in `UpdateCluster` leave unset fields alone, so a collector tick cannot overwrite operator annotations.

### Cloud accounts and virtual machines (ADR-0015)

Two top-level tables outside the Kubernetes entity hierarchy:

- `cloud_accounts` — operator-editable records of cloud-provider accounts. Each row holds the AK (plaintext) and SK (AES-256-GCM encrypted, AAD-bound to the row UUID) for one account, plus curated metadata and a lifecycle status (`pending_credentials`, `active`, `error`, `disabled`). FK source for `virtual_machines`.
- `virtual_machines` — non-Kubernetes platform VMs catalogued by the vm-collector. Top-level FK to `cloud_accounts(id)` `ON DELETE CASCADE`. Carries cloud-provider metadata (AMI, instance type, networking, security groups) plus an operator-curated `applications` JSONB array for platform software inventory. Soft-deleted via `terminated_at`; never hard-deleted by reconciliation.

### Audit events

`audit_events` — append-only table written by `AuditMiddleware`. Records every non-GET request and every `/v1/admin/*` read. Sensitive fields are scrubbed. Includes a `source` discriminator (`api`, `ingest_gw`, `system`). Accessible via `GET /v1/admin/audit` (requires `audit` scope). See [Audit log](audit-log.md).

### Settings table

`settings` — single-row table (`id=1 CHECK`) with runtime feature toggles: `eol_enabled`, `mcp_enabled`. Modified by `PATCH /v1/admin/settings` (admin scope). Env vars seed these values on first boot; the admin can override at runtime without restarting.

## ANSSI cartography layers

Each Kubernetes kind maps to an ANSSI SecNumCloud cartography layer (per [ADR-0002](adr/adr-0002-kubernetes-to-anssi-cartography-layers.md)):

| Kind | ANSSI Layer |
|------|-------------|
| Node | `infrastructure_physical` |
| PersistentVolume | `infrastructure_physical` |
| Namespace | `infrastructure_logical` |
| Service | `infrastructure_logical` |
| Workload (Deployment, StatefulSet, DaemonSet) | `applicative` |
| Pod | `applicative` |
| Ingress | `applicative` |
| PersistentVolumeClaim | `applicative` |
| Cluster | `ecosystem` |

## Auth model

Dual-path authentication per [ADR-0007](adr/adr-0007-auth-and-rbac.md):

- **Humans**: session cookie (HttpOnly, SameSite=Strict, 8h sliding expiry) obtained via local login or OIDC.
- **Machines**: bearer token (`longue_vue_pat_<prefix>_<secret>`, argon2id-hashed at rest).
- **RBAC**: four fixed roles (admin, editor, auditor, viewer) mapping to scope sets. Scope checks happen at the operation level.

See [Authentication](authentication.md) for operational details.

## Storage

### PostgreSQL

longue-vue uses PostgreSQL 14+ with `pgx/v5` for connection pooling. JSONB columns store heterogeneous Kubernetes specs without schema sprawl. All queries use parameterized statements.

### Migrations

SQL migrations are embedded in the binary via `migrations/embed.go` and run by `goose` on startup (when `LONGUE_VUE_AUTO_MIGRATE=true`, the default). Migrations are timestamped and forward-only.

### Pagination

All list endpoints use cursor-based pagination. The cursor is an opaque base64-encoded token containing the last-seen ID. This avoids the offset-skip performance degradation of traditional pagination.

## Build and embed

The longue-vue binary embeds the React UI bundle:

1. `make ui-build` produces `ui/dist/` (Vite build).
2. `go build` picks it up via `//go:embed all:dist` in `ui/embed.go`.
3. The embedded files are served at `/ui/*`; root `/` redirects to `/ui/`.

The build tag `noui` disables the embed (`make build-noui`), making `/ui/` return 404. Useful for backend-only development or headless deployments.

### Container image

The `Dockerfile` uses a three-stage build:

1. `node:22-alpine` builds the UI bundle.
2. `golang` compiles longue-vue with the embedded UI (`CGO_ENABLED=0` for a static binary).
3. `gcr.io/distroless/static-debian12:nonroot` runs the binary as UID 65532.

The push collector has its own `Dockerfile.collector` following the same pattern without the UI stage.

## Architectural Decision Records

Detailed design rationale is recorded in ADRs under `docs/adr/`:

| ADR | Topic |
|-----|-------|
| [0001](adr/adr-0001-cmdb-for-snc-using-kube.md) | Build a CMDB for SNC against the Kubernetes API. |
| [0002](adr/adr-0002-kubernetes-to-anssi-cartography-layers.md) | Map Kubernetes kinds to ANSSI cartography layers. |
| [0003](adr/adr-0003-workload-polymorphism.md) | Single workloads table polymorphic on kind. |
| [0004](adr/adr-0004-ingress-layer-classification.md) | Classify Ingress in the applicative layer. |
| [0005](adr/adr-0005-multi-cluster-collector.md) | Central-pull multi-cluster topology. |
| [0006](adr/adr-0006-ui-for-audit-and-curated-metadata.md) | Web UI for audit views and curated metadata. |
| [0007](adr/adr-0007-auth-and-rbac.md) | Dual-path auth and RBAC. |
| [0008](adr/adr-0008-secnumcloud-chapter-8-asset-management.md) | SecNumCloud chapter 8 asset management. |
| [0009](adr/adr-0009-push-collector-for-airgapped-clusters.md) | Push-based collector for air-gapped clusters. |
| [0012](adr/adr-0012-eol-enrichment-via-endoflife-date.md) | End-of-life enrichment from endoflife.date. |
| [0013](adr/adr-0013-impact-analysis-graph.md) | On-the-fly FK traversal for dependency impact graphs. |
| [0014](adr/adr-0014-mcp-server.md) | Model Context Protocol server for AI agent integration. |
| [0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) | Cloud-account model, VM collector, AES-256-GCM SK encryption. |
| [0016](adr/adr-0016-dmz-ingest-gateway.md) | DMZ ingest gateway for push collectors in restricted networks. |
| [0017](adr/adr-0017-public-listener-tls-posture-and-proxy-trust.md) | Public-listener TLS posture, proxy trust, last-admin guard. |
| [0018](adr/adr-0018-helm-chart-per-deployable-binary.md) | Helm charts as the supported production deployment surface. |
| [0019](adr/adr-0019-vm-applications-and-eol-and-search.md) | Operator-curated applications on platform VMs for EOL enrichment. |
