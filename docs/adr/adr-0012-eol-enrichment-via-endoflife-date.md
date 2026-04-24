---
title: "ADR-0012: End-of-life enrichment via endoflife.date"
status: "Proposed"
date: "2026-04-24"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "enrichment", "eol", "lifecycle", "security"]
supersedes: ""
superseded_by: ""
---

# ADR-0012: End-of-life enrichment via endoflife.date

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

Argos catalogues software versions across every entity in the CMDB: `kubernetes_version` on clusters, `kubelet_version` / `kube_proxy_version` / `container_runtime_version` / `kernel_version` / `os_image` on nodes, and container `image` tags on pods and workloads. Today none of these carry lifecycle information — an operator looking at the inventory cannot tell which components are end-of-life (EOL), approaching EOL, or still under active support without checking each version manually.

SecNumCloud chapter 8 (asset management) and ANSSI best practices expect the CMDB to surface obsolescence risk. Running EOL software is a known vector for unpatched CVEs and a recurring finding in SNC audits.

The [endoflife.date](https://endoflife.date) project maintains a community-curated, machine-readable database of lifecycle dates for 450+ products — including Kubernetes, Ubuntu, Debian, Alpine Linux, RHEL, containerd, Docker, PostgreSQL, and most software Argos already inventories. It exposes a free, unauthenticated JSON API:

- `GET /api/all.json` — list of all tracked product identifiers.
- `GET /api/{product}.json` — all release cycles with `eol`, `support`, `lts`, `latest`, `releaseDate` per cycle.
- `GET /api/{product}/{cycle}.json` — single cycle detail.

No API key, no rate-limit header (community-funded, fair-use expected), responses are small JSON arrays cached at the CDN edge.

## Decision

**Add an EOL enrichment subsystem to argosd** that periodically queries the endoflife.date API, matches inventoried versions against known product cycles, and writes structured EOL annotations on the corresponding CMDB entities.

### Matchable products (v1 scope)

| CMDB entity | Field(s)                   | endoflife.date product(s) | Version extraction |
|-------------|----------------------------|---------------------------|--------------------|
| Cluster     | `kubernetes_version`       | `kubernetes`              | strip leading `v`, match major.minor cycle |
| Node        | `kubelet_version`          | `kubernetes`              | same as above (kubelet tracks k8s release) |
| Node        | `container_runtime_version`| `containerd`, `cri-o`    | parse `containerd://1.7.x` → product `containerd`, cycle `1.7` |
| Node        | `os_image`                 | `ubuntu`, `debian`, `alpine`, `rhel`, `rocky-linux`, `amazon-linux`, … | parse distro name + version from the free-text `os_image` string |
| Node        | `kernel_version`           | `linux`                   | match major.minor cycle |

Container images (pods/workloads) are **out of v1 scope**. Image tags are unstructured (`nginx:1.25-alpine`, `myapp:latest`, `sha256:…`) and matching them to endoflife.date products requires a registry-aware parser and a heuristic mapping layer. This is a natural v2 extension.

### Enrichment model

The enrichment writes **annotations** on the entity, using a reserved `argos.io/eol.*` key namespace:

```json
{
  "argos.io/eol.kubernetes": {
    "product": "kubernetes",
    "cycle": "1.28",
    "eol": "2025-06-28",
    "eol_status": "eol",
    "support": "2025-04-28",
    "latest": "1.28.15",
    "latest_available": "1.32.3",
    "checked_at": "2026-04-24T10:00:00Z"
  },
  "argos.io/eol.containerd": {
    "product": "containerd",
    "cycle": "1.6",
    "eol": "2024-02-16",
    "eol_status": "eol",
    "latest": "1.6.36",
    "latest_available": "2.0.4",
    "checked_at": "2026-04-24T10:00:00Z"
  }
}
```

`eol_status` is a derived enum for UI/query convenience:

| Value | Meaning |
|-------|---------|
| `eol` | Current date is past `eol` date. |
| `approaching_eol` | Current date is within a configurable window before `eol` (default: 90 days). |
| `supported` | Still under active support. |
| `unknown` | Product or cycle not found in endoflife.date. |

The annotations key is namespaced per product so a single node can carry multiple EOL signals (e.g. OS and container runtime simultaneously).

### Architecture

```
┌──────────────────────────────────┐
│           argosd                 │
│                                  │
│  ┌───────────┐  ┌─────────────┐ │
│  │ Collector  │  │ EOL Enricher│ │
│  │ (per-tick) │  │ (periodic)  │ │
│  └─────┬─────┘  └──────┬──────┘ │
│        │               │        │
│        ▼               ▼        │
│  ┌──────────────────────────┐   │
│  │         Store (PG)       │   │
│  └──────────────────────────┘   │
│                                  │
└──────────────────────────────────┘
                │
    EOL Enricher fetches from:
                │
                ▼
      https://endoflife.date/api/
```

The enricher is a **separate goroutine** in argosd, independent of the collector. It runs on its own interval (default: 24h — lifecycle data changes slowly). Each tick:

1. Queries the store for all distinct `(product, version)` tuples currently in the CMDB.
2. Fetches the matching endoflife.date cycles (with an in-memory cache per product, TTL = enricher interval, so repeated cycles don't re-fetch).
3. Computes `eol_status` for each tuple.
4. Writes the `argos.io/eol.*` annotations via `UpdateCluster` / `UpdateNode` merge-patch (existing annotations are preserved; only the `argos.io/eol.*` keys are overwritten).

The enricher never deletes CMDB entities — it only annotates. A failed endoflife.date fetch logs a warning and skips that product; stale annotations carry `checked_at` so the UI can show freshness.

### Configuration

```
ARGOS_EOL_ENABLED=true                    # default: false
ARGOS_EOL_INTERVAL=24h                    # default: 24h
ARGOS_EOL_APPROACHING_DAYS=90             # default: 90
ARGOS_EOL_BASE_URL=https://endoflife.date # default; override for mirror/proxy
```

Disabled by default so the feature is opt-in. When `ARGOS_EOL_ENABLED=false`, no goroutine is started, no outbound HTTP calls are made.

### API surface

No new endpoints. EOL data is exposed through the existing entity responses — the `annotations` field already ships in `GET /v1/clusters`, `GET /v1/nodes`, etc. Consumers filter on the `argos.io/eol.*` keys.

A future enhancement can add query parameters (`?eol_status=eol`, `?eol_before=2026-06-01`) as convenience filters, but v1 relies on client-side filtering or the UI's annotation display.

### UI surface

The UI exposes an **End-of-Life Inventory** page (`/ui/eol`) that reads the `argos.io/eol.*` annotations and renders:

- **Summary cards** at the top: red (End of Life), orange (Approaching EOL), green (Supported) — clickable to filter the table.
- A **sortable table** with two column groups separated by a visual border:
  - *What we run*: Status (badge), Product, Version (major.minor cycle), Patch (latest patch for the cycle), Entity (link), Cluster.
  - *What's available*: Latest Available (product-wide latest from endoflife.date), EOL Date, Checked.
- **Row-level highlighting**: red background for EOL rows, orange for approaching EOL.
- **Status badges** on entity detail pages (cluster, node).

### Push collector

The push collector (`argos-collector`) does **not** run the enricher. Enrichment is centralised in argosd — it reads from the store and writes back. Push-collected clusters are enriched the same way as pull-collected ones; no change to the push collector binary.

## Consequences

### Positive

- **POS-001**: Operators see EOL risk at a glance in the CMDB, without leaving the tool or cross-referencing external sites.
- **POS-002**: SNC auditors can query annotations to produce an obsolescence report — a direct input to chapter 8 compliance evidence.
- **POS-003**: The annotation model is extensible — future enrichers (CVE databases, compliance tags) can use the same `argos.io/*` namespace pattern.
- **POS-004**: No schema migration — annotations are already a JSONB column on clusters and nodes.
- **POS-005**: Opt-in by default — environments with no outbound internet (air-gap) are unaffected.

### Negative

- **NEG-001**: argosd makes outbound HTTPS calls to `endoflife.date`. In strict-egress environments, operators must allowlist the domain or configure `ARGOS_EOL_BASE_URL` to point to an internal mirror.
- **NEG-002**: endoflife.date is community-maintained. Data accuracy depends on upstream contributors. Mitigated by `checked_at` timestamps and the ability to override annotations manually via `PATCH`.
- **NEG-003**: Version extraction from free-text fields (`os_image`, `container_runtime_version`) is heuristic. Edge cases (custom OS images, non-standard version strings) may produce `unknown` status. The parser must be conservative — `unknown` is better than a wrong match.
- **NEG-004**: The annotation payload increases the JSON size of entity responses. For nodes with 3-4 matched products, this adds ~1 KB per node — negligible at expected scale.

## Alternatives Considered

### Store EOL data in dedicated columns

- **Description**: Add `eol_date`, `eol_status` columns to `clusters`, `nodes`, etc.
- **Rejection reason**: Rigid — each new matchable product requires a migration. The annotations JSONB is already designed for this kind of extensible metadata. Dedicated columns may make sense later for high-cardinality query patterns (e.g. `WHERE eol_status = 'eol'`), but v1 can use JSONB operators.

### Run enrichment as a separate sidecar / CronJob

- **Description**: A standalone binary or Kubernetes CronJob that reads the CMDB via the API, fetches endoflife.date, and patches entities back.
- **Rejection reason**: Adds a second deployment target, a second token to manage, and network round-trips through the API for what is a store-to-store operation. The in-process goroutine is simpler and consistent with the collector model. If isolation is needed later (e.g. for a plugin system), the enricher package can be extracted — same trade-off as ADR-0005 ALT-003/ALT-004 for the collector.

### Use the NIST NVD or another CVE database instead

- **Description**: Enrich with CVE counts per version rather than EOL dates.
- **Rejection reason**: Complementary, not a substitute. EOL status is a lifecycle signal (is this version still receiving patches?); CVE count is a vulnerability signal (how many known issues exist?). endoflife.date is simpler to integrate (no API key, small payloads, stable schema). CVE enrichment is a natural follow-up that can reuse the same annotation model.

## Amendment — Latest Available Version (2026-04-24)

### Context

The current annotation stores `latest` — the most recent patch for the entity's **own cycle** (e.g. `1.28.15` for cycle `1.28`). This tells operators whether they're on the newest patch, but not how far behind the current product release they are. An operator running Kubernetes 1.28 has no way to see that 1.32 exists without leaving the tool.

SecNumCloud auditors frequently ask "what is the latest available version and why hasn't the platform upgraded?" — answering this requires cross-referencing endoflife.date manually today.

### Decision

Add a `latest_available` field to the EOL annotation (see updated example in the Enrichment model section above). This field contains the latest patch version of the **newest cycle** published on endoflife.date for that product (e.g. `1.32.3` when the entity runs cycle `1.28`).

The enricher already fetches the full product cycles list; extracting the first element's `latest` field adds zero API calls.

### UI changes

The EOL Inventory table gains a **Latest Available** column showing the product-wide latest version. The table columns are grouped into two visual sections separated by a border:
- **What we run** (Status, Product, Version, Patch, Entity, Cluster): the deployed software and its lifecycle state. Rows are highlighted red for EOL, orange for approaching EOL.
- **What's available** (Latest Available, EOL Date, Checked): upgrade targets and lifecycle dates.

Column renames for clarity: "Cycle" → **Version**, "Cycle Latest" → **Patch**.

### Consequences

- **POS**: Operators and auditors can instantly identify upgrade opportunities without leaving Argos.
- **NEG**: None — the data is already fetched, this only stores one additional string per annotation (~10 bytes).

## Implementation Notes

- **IMP-001**: Create `internal/eol/` package. Core types: `Product`, `Cycle`, `Status` enum, `Annotation` struct. `Enricher` struct with `Run(ctx)` method (same pattern as `collector.Collector`).
- **IMP-002**: Create `internal/eol/endoflife/` sub-package: HTTP client for endoflife.date API with in-memory product cache, configurable base URL, timeout, and `http.Client` injection for testing.
- **IMP-003**: Create `internal/eol/matcher/` sub-package: version extraction and product-matching logic. Start with `KubernetesVersionMatcher`, `ContainerRuntimeMatcher`, `OSImageMatcher`, `KernelMatcher`. Each implements a `Match(fieldValue string) (product, cycle string, ok bool)` interface. Unit-test with real-world `os_image` and `container_runtime_version` strings from diverse clusters.
- **IMP-004**: Wire the enricher in `cmd/argosd/main.go` behind `ARGOS_EOL_ENABLED`. Start after the store is ready, stop on context cancellation (same as collector goroutines).
- **IMP-005**: Add Prometheus metrics: `argos_eol_enrichments_total{product, status}`, `argos_eol_errors_total{product, phase}`, `argos_eol_last_run_timestamp_seconds`.
- **IMP-006**: UI: add EOL badges to cluster detail and node detail pages. Read from `annotations["argos.io/eol.*"]`.
- **IMP-007**: Tests: unit tests for matchers, integration test that starts the enricher against a fake HTTP server returning canned endoflife.date responses and verifies annotations land in the store.

## References

- **REF-001**: endoflife.date API — `https://endoflife.date/docs/api`
- **REF-002**: endoflife.date product list — `https://endoflife.date/api/all.json` (450+ products)
- **REF-003**: ADR-0008 — SecNumCloud chapter 8 asset management
- **REF-004**: ADR-0005 — Multi-cluster collector topology (goroutine model)
- **REF-005**: ANSSI SecNumCloud v3.2 — obsolescence management requirements
