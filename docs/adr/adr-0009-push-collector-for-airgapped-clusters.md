---
title: "ADR-0009: Push-based collector for air-gapped clusters"
status: "Proposed"
date: "2026-04-21"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "collector", "airgap", "snc", "deployment"]
supersedes: ""
superseded_by: ""
---

# ADR-0009: Push-based collector for air-gapped clusters

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

ADR-0005 adopted a **central pull** topology: a single argosd process holds
N kubeconfigs and pulls every cluster from outside. This works when argosd
has network reachability to every target Kubernetes API server.

In SecNumCloud-qualified environments, a significant portion of the clusters
are **air-gapped** (or network-restricted to the point of equivalence):

- **Dedicated administration zones** (ZAD) whose API servers are
  unreachable from anything outside the zone boundary.
- **Production clusters behind strict egress firewalls** that block all
  inbound connections not in an explicit allow list — adding the central
  argosd to that list violates the principle of least privilege.
- **Multi-tenant environments** where the CMDB operator (who runs argosd)
  cannot obtain a kubeconfig for a tenant's cluster; only the tenant's own
  automation can push data out.

In all three cases the pull model fails: argosd cannot reach the Kubernetes
API, so it cannot poll. The data model, the store, and the REST API are
already multi-cluster-capable (every entity keys on `cluster_id`, every
`POST /v1/*` endpoint accepts the full resource payload). What is missing
is a **collector binary that runs inside the air-gapped cluster and pushes
observations to argosd over the REST API**.

ADR-0005 POS-004 explicitly anticipated this:

> *"The push model (agent-per-cluster) stays available as a future option
> via the existing REST API + scoped tokens."*

This ADR activates that option.

## Decision

Ship a **push-mode collector** (`argos-collector`) that runs inside
an air-gapped cluster, polls the local Kubernetes API, and pushes
observations to a remote argosd instance over HTTPS.

### Binary

Extract the existing `internal/collector` package into a standalone
binary under `cmd/argos-collector/`. The binary reuses the same
`KubeSource` implementation (in-cluster or kubeconfig) and the same
ingestion logic (`ingestNodes`, `ingestNamespaces`, …) but writes
through an **HTTP client targeting the argosd REST API** instead of
the direct `Store` interface.

The `argosd` binary keeps its embedded pull-mode collectors unchanged.
The two modes coexist: some clusters are pulled centrally, others push
from inside. The CMDB sees no difference — both paths write through the
same store (directly or via API).

### API client store

Introduce an `internal/collector/apiclient` package that implements
the `collector.cmdbStore` interface (the narrow subset the collector
actually uses: `GetClusterByName`, `UpdateCluster`, `UpsertNode`,
`UpsertNamespace`, …, `Delete*NotIn`) by calling the corresponding
argosd REST endpoints:

| Store method            | HTTP call                                          |
|-------------------------|----------------------------------------------------|
| `GetClusterByName`      | `GET /v1/clusters?name=<name>`                     |
| `UpdateCluster`         | `PATCH /v1/clusters/{id}`                           |
| `UpsertNode`            | `POST /v1/nodes` (idempotent on `cluster_id+name`) |
| `UpsertNamespace`       | `POST /v1/namespaces` (idem)                       |
| `UpsertPod`             | `POST /v1/pods`                                    |
| `UpsertWorkload`        | `POST /v1/workloads`                               |
| `UpsertService`         | `POST /v1/services`                                |
| `UpsertIngress`         | `POST /v1/ingresses`                               |
| `UpsertPersistentVolume`| `POST /v1/persistentvolumes`                       |
| `UpsertPersistentVolumeClaim` | `POST /v1/persistentvolumeclaims`            |
| `Delete*NotIn`          | `POST /v1/<resource>/reconcile` (new endpoint)     |

The existing POST endpoints already perform upsert semantics in the
store (INSERT … ON CONFLICT UPDATE). The push collector reuses them
as-is.

### Reconciliation endpoint

The pull collector calls `Delete*NotIn(scope, keepNames)` directly on
the store. The push collector needs an equivalent API surface. Add a
`POST /v1/<resource>/reconcile` endpoint per reconcilable resource:

```json
POST /v1/nodes/reconcile
{
  "cluster_id": "<uuid>",
  "keep_names": ["node-1", "node-2"]
}
```

Response: `{"deleted": 3}`.

Requires `write` scope. Scoped by `cluster_id` (cluster-scoped kinds)
or `namespace_id` (namespace-scoped kinds), exactly mirroring the
store's existing `Delete*NotIn` signatures.

### Authentication

The push collector authenticates to argosd with a **bearer token**
(PAT) issued from the admin panel per ADR-0007. The token needs the
`write` scope (role `editor` or `admin`). One token per air-gapped
cluster is the recommended pattern — revocation is per-cluster.

### Gateway / proxy support

In SNC environments, outbound traffic from an air-gapped cluster rarely
reaches the internet (or even the management network) directly. It
typically transits through an **API gateway** or **forward proxy** —
Envoy, HAProxy, Nginx, or a corporate HTTP proxy — that enforces TLS
termination, allowlisting, rate limiting, and audit logging.

The push collector must work transparently behind such intermediaries:

- **HTTP(S) proxy**: honour the standard `HTTPS_PROXY` / `HTTP_PROXY` /
  `NO_PROXY` environment variables. Go's `net/http` default transport
  already reads these — no custom code needed, just document it.
- **Envoy / API gateway as a reverse proxy in front of argosd**: the
  collector only sees the gateway's URL. `ARGOS_SERVER_URL` points to
  the gateway (e.g. `https://gateway.zad.internal:443/argos`). The
  gateway routes to argosd based on path prefix or SNI. The collector
  is unaware of the hop — it sends standard REST+Bearer requests.
- **Path prefix rewrite**: if the gateway exposes argosd under a
  sub-path (e.g. `/argos/v1/…` instead of `/v1/…`), the collector
  supports `ARGOS_SERVER_URL=https://gw:443/argos` and prepends the
  base path to every request. The `apiclient` strips or joins trailing
  slashes so operators don't have to worry about them.
- **Custom headers**: some gateways require extra headers for routing
  or tenant identification (e.g. `X-Tenant-Id`, `X-Route-Key`).
  `ARGOS_EXTRA_HEADERS` accepts a comma-separated `key=value` list
  injected into every outbound request:
  ```
  ARGOS_EXTRA_HEADERS=X-Tenant-Id=zad-prod,X-Route-Key=argos
  ```
- **mTLS to the gateway**: when the gateway requires client-certificate
  authentication (common in zero-trust ZAD architectures), the
  collector loads a client cert + key from:
  ```
  ARGOS_CLIENT_CERT=/etc/argos/tls/client.crt
  ARGOS_CLIENT_KEY=/etc/argos/tls/client.key
  ```
  Combined with `ARGOS_CA_CERT` for the server-side CA, this gives
  full mTLS between the collector and the gateway.

The design principle is: the collector speaks plain HTTPS with bearer
auth — any gateway that can forward HTTP requests transparently is
supported without collector-side code changes. The configuration knobs
above cover the edge cases where the gateway imposes constraints
(path rewrite, extra headers, client certs).

### Configuration

```
ARGOS_MODE=push                       # "pull" (default/argosd) or "push"
ARGOS_SERVER_URL=https://argos.internal:8080
ARGOS_API_TOKEN=argos_pat_xxxx_yyyy
ARGOS_CLUSTER_NAME=zad-prod
ARGOS_KUBECONFIG=""                   # empty = in-cluster
ARGOS_COLLECTOR_INTERVAL=5m
ARGOS_COLLECTOR_RECONCILE=true
ARGOS_CA_CERT=                        # optional: custom CA for server TLS
ARGOS_CLIENT_CERT=                    # optional: client cert for mTLS
ARGOS_CLIENT_KEY=                     # optional: client key for mTLS
ARGOS_EXTRA_HEADERS=                  # optional: extra HTTP headers (k=v,…)
# Standard HTTPS_PROXY / HTTP_PROXY / NO_PROXY honoured by Go's net/http
```

The push collector does not need `ARGOS_DATABASE_URL` — it never talks
to PostgreSQL. It does not serve HTTP. It is a pure client.

### Deployment

The push collector is deployed as a `Deployment` (replicas: 1) inside
the air-gapped cluster, with:

- A `ServiceAccount` + `ClusterRole` granting read-only list access to
  the Kubernetes API (same RBAC as the pull collector).
- A `Secret` carrying `ARGOS_API_TOKEN`.
- Egress network policy allowing HTTPS to the argosd endpoint only.

The push collector auto-creates the cluster record on first contact if
it doesn't exist (ADR-0011). Pre-registering via `POST /v1/clusters` is
optional but recommended to populate curated metadata upfront.

### Bulk push (future optimisation)

The v1 push collector sends one HTTP request per upsert (same
granularity as the pull collector's per-row store calls). If
per-request overhead becomes a bottleneck at scale, a bulk endpoint
(`POST /v1/collect` accepting the full tick payload in one request)
can be added without changing the collector's internal structure —
the `apiclient` store would batch rows into a single call instead of
N calls. Out of scope for v1.

## Consequences

### Positive

- **POS-001**: Air-gapped and network-restricted clusters become
  cataloguable without punching holes in their firewall — only the
  collector inside the cluster initiates outbound HTTPS.
- **POS-002**: Each push collector only holds credentials for its own
  cluster (in-cluster ServiceAccount) + one argosd PAT. A compromised
  collector exposes one cluster's read-only Kubernetes data and one
  write-scoped token — strictly less than the central pull model where
  one pod holds N kubeconfigs (ADR-0005 NEG-001).
- **POS-003**: The existing REST API, data model, and store are reused
  unchanged. No schema migration, no new tables, no breaking API
  changes.
- **POS-004**: Pull and push collectors coexist. An environment can mix
  both modes: centrally-pulled clusters where reachability exists,
  push-based collectors where it doesn't. The CMDB is topology-agnostic.
- **POS-005**: The `argos-collector` binary is a minimal, static Go
  binary with no database dependency. It can run in constrained
  environments (small footprint, no PG client needed).
- **POS-006**: Gateway-transparent by design. The collector speaks
  plain HTTPS — it works unchanged behind Envoy, HAProxy, Nginx, or
  any HTTP-aware gateway. mTLS, path-prefix rewrite, custom headers,
  and forward proxies are supported via env vars without code changes.

### Negative

- **NEG-001**: Second binary to build, release, and version. Mitigated
  by sharing `internal/collector` — the ingestion logic is compiled
  once, consumed by both `argosd` and `argos-collector`.
- **NEG-002**: Per-upsert HTTP round-trips add latency compared to
  direct store writes. For a 500-pod cluster at 5-minute intervals this
  is acceptable (one tick completes in seconds over LAN). The bulk
  endpoint escape hatch exists if needed.
- **NEG-003**: Reconciliation via API requires new endpoints
  (`/reconcile`). These endpoints are destructive (they delete rows)
  and must be protected by `write` scope + per-cluster scoping to
  prevent cross-cluster data loss.
- **NEG-004**: The push collector must trust argosd's (or the gateway's)
  TLS certificate. In air-gapped environments, the CA chain may need to
  be explicitly mounted. Configuration: `ARGOS_CA_CERT=/path/to/ca.pem`.
- **NEG-005**: When a gateway sits between the collector and argosd,
  error diagnostics become harder — a 503 may come from the gateway,
  not argosd. The `apiclient` must log the full response status and
  body to distinguish gateway errors from application errors.

## Alternatives Considered

### File-based export / import

- **Description**: The air-gapped collector writes a JSON snapshot to a
  file. An operator (or an automated relay) transfers the file to the
  argosd side, which imports it via a `POST /v1/import` endpoint.
- **Rejection reason**: Adds operational burden (file transfer, ordering,
  idempotency guarantees). The push model over HTTPS is simpler and
  near-real-time. File-based import remains viable as a last-resort for
  fully offline environments (no network at all), but that is not the
  case described here — the clusters have outbound HTTPS.

### gRPC streaming

- **Description**: The push collector opens a gRPC stream to argosd and
  sends observations as a stream of messages.
- **Rejection reason**: Introduces a second wire protocol (the REST API
  already exists and is tested). gRPC adds protobuf codegen, a second
  listening port, and mTLS configuration. The marginal performance gain
  over HTTP/1.1 JSON does not justify the complexity at current scale.
  If bulk throughput becomes critical, HTTP/2 + the bulk endpoint is a
  simpler step.

### VPN / bastion tunnel to enable pull mode

- **Description**: Instead of a push collector, establish a VPN or SSH
  tunnel from argosd to the air-gapped cluster's API server.
- **Rejection reason**: Violates the air-gap boundary by design. The
  whole point is that inbound connectivity to the cluster is forbidden.
  Outbound-only HTTPS from the push collector respects the network
  security posture.

## Implementation Notes

- **IMP-001**: Create `cmd/argos-collector/main.go`. Parse env vars,
  build a `KubeClient`, build an `apiclient.Store`, build a
  `collector.Collector`, call `Run(ctx)`. Signal handling identical to
  argosd (SIGINT/SIGTERM → context cancel → graceful drain).
- **IMP-002**: Implement `internal/collector/apiclient/store.go` — the
  HTTP-backed `cmdbStore`. Use `net/http` with `Authorization: Bearer`
  header. Retry transient 5xx with exponential backoff (3 attempts max).
  On 401/403, log and stop (token revoked or misconfigured). Build the
  `http.Transport` with: custom CA pool (`ARGOS_CA_CERT`), client
  certificate (`ARGOS_CLIENT_CERT` + `ARGOS_CLIENT_KEY`) for mTLS,
  and standard proxy env var support (Go default). Inject extra headers
  from `ARGOS_EXTRA_HEADERS` into every request. Prepend the base path
  from `ARGOS_SERVER_URL` to every endpoint path.
- **IMP-003**: Add `POST /v1/<resource>/reconcile` endpoints to
  `api/openapi/openapi.yaml` and implement handlers in
  `internal/api/server.go`. Each handler calls the existing
  `Delete*NotIn` store method. Requires `write` scope.
- **IMP-004**: Add a `Dockerfile.collector` (or a build stage in the
  existing Dockerfile) producing the `argos-collector` static binary.
  Same distroless base image, same UID 65532.
- **IMP-005**: Add `deploy/collector/` with reference Kustomize
  manifests for deploying the push collector in a target cluster
  (ServiceAccount, ClusterRole, Deployment, Secret template).
- **IMP-006**: Integration test: start argosd with a test PG, start
  `argos-collector` pointed at argosd's HTTP listener with a
  `fakeSource`, verify nodes/pods/workloads appear in the store via
  the API.
- **IMP-007**: Update project documentation to describe the dual-mode
  topology.
- **IMP-008**: Add an integration test with a test HTTP proxy between
  the collector and argosd to validate proxy/gateway transparency
  (path rewrite, custom headers, mTLS).

## References

- **REF-001**: ADR-0005 — Multi-cluster collector topology (central
  pull model, POS-004 anticipates push)
- **REF-002**: ADR-0007 — Auth and RBAC (bearer tokens for machine
  clients)
- **REF-003**: ADR-0001 — CMDB for SNC (POS-003: external push via
  API as a supported pattern)
- **REF-004**: SecNumCloud v3.2 — network segmentation requirements
  for administration zones (ZAD)
