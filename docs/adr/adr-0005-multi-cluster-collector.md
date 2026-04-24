---
title: "ADR-0005: Multi-cluster collector topology"
status: "Proposed"
date: "2026-04-18"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "collector", "deployment", "multi-cluster"]
supersedes: ""
superseded_by: ""
---

# ADR-0005: Multi-cluster collector topology

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

The argosd collector is currently bound to a single Kubernetes cluster: one kubeconfig, one cluster name, one set of per-tick goroutines (`ingestNodes` / `ingestNamespaces` / `ingestPods` / `ingestWorkloads` / `ingestServices` / `ingestIngresses`). This was the right v1 scope (ADR-0001 IMP-005(c) explicitly deferred multi-cluster topology), but it no longer fits any realistic SecNumCloud use case:

- A qualified environment typically runs **prod / staging / DR**, often in **multiple regions**, and almost always spanning **multiple control planes**.
- SNC evidence exports aggregate assets across every cluster in scope; a CMDB that catalogues just one leaves the rest invisible.
- The Argos data model already carries `cluster_id` on every entity, and the FK chain already scopes namespaces / pods / workloads / services / ingresses under their parent cluster. The *schema* is multi-cluster-ready. Only the *collector* isn't.

This ADR decides how one or more Kubernetes clusters are polled into a single Argos CMDB.

**Two axes force the decision:**

1. **Process topology** — one argosd polling N clusters from outside, or N argosd instances each polling one cluster from inside?
2. **Configuration shape** — how the list of target clusters is declared.

**Three existing constraints shape the answer:**

- The collector currently writes **directly to the store** (not through the REST API). ADR-0001 POS-003 already anticipates external push via the API as a supported pattern, but the current code path is internal.
- The CMDB API already accepts `POST /v1/clusters` etc. with scoped bearer tokens (ADR-0002 + scopes PR), so an agent-per-cluster push model is feasible today.
- argosd is a single Go binary with a static build (Dockerfile PR). It can run in-cluster with a mounted kubeconfig or a ServiceAccount with cluster-scoped RBAC.

## Decision

**Adopt the "one argosd, N internal collectors" (central pull) topology as the v1 multi-cluster shape**, with the push model explicitly kept as a future option the data model already supports.

**Process topology:** a single argosd process runs a `Collector` goroutine per target cluster. Each goroutine owns its own `KubeClient` (kubeconfig + clientset), its own ticker, its own per-tick `poll()` cycle. All write through the shared `Store`. All anchor on the root `signal.NotifyContext` so SIGINT / SIGTERM drains every collector before the HTTP server shuts down.

**Configuration:** a new env var `ARGOS_COLLECTOR_CLUSTERS` accepts a JSON array:

```json
[
  {"name": "prod-eu-west-1",    "kubeconfig": "/etc/kube/prod-eu.yaml"},
  {"name": "staging-eu-west-1", "kubeconfig": "/etc/kube/staging-eu.yaml"},
  {"name": "in-cluster",        "kubeconfig": ""}
]
```

- `name` is the cluster's CMDB name (same value the Cluster row carries). The collector auto-creates the cluster record on first contact if it doesn't exist (ADR-0011); pre-registering via `POST /v1/clusters` is optional but recommended to populate curated metadata upfront.
- `kubeconfig` is a filesystem path. An empty string means "use in-cluster config" (argosd's own ServiceAccount) — useful when argosd is deployed inside one of the target clusters.
- Names must be unique within the list; `loadCollectorClusters` validates.

**Backward compatibility:** the existing single-cluster vars (`ARGOS_CLUSTER_NAME`, `ARGOS_KUBECONFIG`) remain supported as a shortcut. Precedence when both sets are set: `ARGOS_COLLECTOR_CLUSTERS` wins; operators explicitly migrating to multi-cluster stop setting the single-cluster vars.

**Concurrency:** each cluster's `Collector.Run(ctx)` is its own goroutine. Failures in one cluster (unreachable API, transient timeout) log and continue; other clusters' ticks are unaffected. Per-tick operations inside a single cluster remain sequential (as today).

**Cluster record lifecycle:** the collector auto-creates a minimal cluster record (name only) on first contact if the cluster doesn't exist in the CMDB (ADR-0011, reversing the original ALT-007/ALT-008 rejection below). Pre-registration via `POST /v1/clusters` is optional but recommended to populate curated metadata (display name, environment, owner, criticality) before the first tick.

**Per-cluster reconciliation isolation:** already correct by construction. Every `Delete*NotIn` call is scoped to a `cluster_id` (for cluster-scoped kinds) or `namespace_id` (for namespace-scoped kinds). Two collectors writing in parallel never delete each other's rows.

**Interval / fetchTimeout:** continue to be single global values (`ARGOS_COLLECTOR_INTERVAL`, `ARGOS_COLLECTOR_FETCH_TIMEOUT`), applied identically to every per-cluster collector. Per-cluster overrides can be added later if experience shows big clusters need slower ticks — out of scope here.

## Consequences

### Positive

- **POS-001**: Single operational footprint (one Deployment, one Pod replica, one PG connection pool). Matches where the project is today: solo-maintained, easy to reason about.
- **POS-002**: No data-model churn — every table is already keyed by cluster. Only the collector grows N-way.
- **POS-003**: Failure isolation is natural: each goroutine's poll is independent; a dead control plane in one cluster doesn't stall the others.
- **POS-004**: The push model (agent-per-cluster) stays available as a future option via the existing REST API + scoped tokens. When Argos needs horizontal scaling, or when network policy forbids outbound connections from a central argosd to every cluster, that path opens without redesigning data storage.
- **POS-005**: `ARGOS_COLLECTOR_CLUSTERS` JSON mirrors `ARGOS_API_TOKENS` in shape; operators already know this pattern.
- **POS-006**: Single-cluster env vars keep working. No migration pressure on the current dev setup.

### Negative

- **NEG-001**: All target-cluster credentials live in one Pod (mounted Secret with N kubeconfigs). A compromise of that Pod exposes every catalogued cluster. The push model would avoid this — each cluster's agent only sees its own cluster. Accepted as a v1 trade-off; mitigations: read-only RBAC per kubeconfig, Secret encryption at rest in the backing etcd.
- **NEG-002**: One process, one Go runtime, one PG pool. At tens-of-clusters scale the single-process ceiling is real. Mitigation path is clear (agent model), but we commit to raising the ceiling only when observed.
- **NEG-003**: Per-cluster interval / timeout is global. A Raspberry-Pi-sized staging cluster and a large prod cluster are polled on the same cadence. Not a blocker, but noted as a known limitation.
- **NEG-004**: `ARGOS_COLLECTOR_CLUSTERS` JSON gets unwieldy past ~5 entries. A config-file form (YAML / TOML) is a natural follow-up when that threshold is hit.

## Alternatives Considered

### Agent-per-cluster (push model)

- **ALT-001**: **Description**: Run an argosd instance (or a slimmer collector-only binary) inside each cluster, using its in-cluster ServiceAccount. Each agent POSTs observations to a central argosd via the REST API with a scoped bearer token.
- **ALT-002**: **Rejection Reason for v1**: Requires a second deployment target (the agent), a token-distribution story, and network reachability *to* the central argosd from each cluster. The central-pull model is a smaller step with the same data model. The push model is explicitly kept on the roadmap — ADR-0001 already committed to API-based pushes being supported, and this ADR doesn't foreclose it.

### Sidecar / separate collector binary

- **ALT-003**: **Description**: Split the collector out of `argosd` into its own binary (e.g., `argos-collector`), deploy it independently.
- **ALT-004**: **Rejection Reason**: Adds a second binary, a second Dockerfile, a second release cadence, before there's a demonstrated need. The `internal/collector` package can already run on its own inside argosd; if a future deployment requires a standalone binary, extracting the package is a small refactor.

### Filesystem-based config directory

- **ALT-005**: **Description**: Point at `/etc/argos/clusters/` and treat every `*.yaml` file as one kubeconfig; the filename (or the kubeconfig's `current-context`) becomes the cluster name.
- **ALT-006**: **Rejection Reason**: Less explicit than declaring `{name, kubeconfig}` tuples — filename-to-cluster-name is implicit convention that breaks for clusters whose CMDB name doesn't match their kubeconfig filename. JSON env var is heavier to edit but unambiguous. A `ARGOS_COLLECTOR_CONFIG=/path/to/file.yaml` option can be added later without breaking the env-var shape.

### Collector auto-registers the Cluster row

- **ALT-007**: **Description**: If the collector can't find the cluster row at startup, it `POST`s one itself.
- **ALT-008**: **Originally rejected** — explicit registration was preferred for data quality. **Reversed by ADR-0011**: operational friction (boot-ordering, air-gap round-trips) outweighed the data-quality benefit. The collector now auto-creates a minimal record; operators can enrich metadata afterwards.

### Per-cluster interval / timeout overrides in v1

- **ALT-009**: **Description**: Let each entry in `ARGOS_COLLECTOR_CLUSTERS` override `interval` / `fetch_timeout` individually.
- **ALT-010**: **Rejection Reason**: Premature. The operational shape we have (one global interval) hasn't yet produced a real pain point. Adding override fields costs nothing at the JSON level later when we need them.

## Implementation Notes

- **IMP-001**: Add `ARGOS_COLLECTOR_CLUSTERS` parsing in `cmd/argosd/main.go`: accept JSON, validate non-empty `name`, unique names, non-negative count, fall through to `ARGOS_CLUSTER_NAME` / `ARGOS_KUBECONFIG` when the new var is absent.
- **IMP-002**: Replace the single `maybeStartCollector(ctx, store)` with a `startCollectors(ctx, store, clusters)` helper that loops, builds one `KubeClient` + `Collector` per entry, and launches `coll.Run(ctx)` in its own goroutine. Use a `sync.WaitGroup` so `main.run` can wait for every collector to exit before returning — the HTTP server's graceful shutdown already has a bounded window; the WG lets shutdown block on collector drain too.
- **IMP-003**: `collector.Collector` is unchanged structurally — the struct already owns one cluster's identity. The only change is N instances of it instead of one.
- **IMP-004**: Logging: every `slog.*` call inside the collector already tags `cluster_name`, so multi-cluster logs are already greppable. Double-check no new call site drops the tag.
- **IMP-005**: Tests: extend `cmd/argosd` / `internal/collector` coverage with:
  - A unit test for the new config parser (JSON valid / invalid / empty-name / duplicate-name / fallback to single-cluster vars).
  - A collector test that runs two `Collector`s in parallel against two `fakeSource`s + a shared `fakeStore`, verifies reconciliation remains isolated per `cluster_id`.
- **IMP-006**: Deployment example (separate PR): update the `deploy/` manifests (from the Dockerfile follow-up) to show a Secret with multiple kubeconfigs and the matching env var, plus an explanatory README covering RBAC minimums per kubeconfig.
- **IMP-007**: `CLAUDE.md` architecture note: update the collector paragraph from "single cluster via kubeconfig" to "one collector goroutine per entry in `ARGOS_COLLECTOR_CLUSTERS` (JSON); shared store, independent ticks".
- **IMP-008**: Keep the JSON schema minimal: `{name, kubeconfig}` only. Additional per-cluster fields (custom interval, custom auth mode) are additive — leave the struct open for extension.

## References

- **REF-001**: ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md` (IMP-005(c) deferred multi-cluster topology)
- **REF-002**: ADR-0002 — Kubernetes-to-ANSSI cartography layer mapping — `docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md`
- **REF-003**: Kubernetes client-go kubeconfig loading — https://pkg.go.dev/k8s.io/client-go/tools/clientcmd
- **REF-004**: Kubernetes in-cluster config (ServiceAccount-based) — https://kubernetes.io/docs/tasks/run-application/access-api-from-pod/
