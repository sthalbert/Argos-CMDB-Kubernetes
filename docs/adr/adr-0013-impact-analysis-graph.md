---
title: "ADR-0013: Impact analysis graph"
status: "Proposed"
date: "2026-04-24"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "impact", "graph", "ui", "incident-response"]
supersedes: ""
superseded_by: ""
---

# ADR-0013: Impact analysis graph

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

Argos inventories 9 entity types across clusters — clusters, nodes, namespaces, pods, workloads, services, ingresses, persistent volumes, and persistent volume claims — linked by foreign-key relationships. When an operator needs to upgrade a node, decommission a PV, or change a workload, they must manually trace which other components are affected by navigating detail pages one by one.

SecNumCloud chapter 8 (asset management) and chapter 12 (incident management) expect the CMDB to answer "what is the blast radius of a change?" quickly and reliably. ANSSI auditors routinely ask for evidence that the organisation understands the dependency chain between infrastructure and application components.

Today, the relationships exist in the database (FK columns, `node_name` on pods, `selector` on services, `bound_volume_id` on PVCs, `workload_id` on pods) but are **not surfaced as a navigable graph** in the UI. An operator has no single view showing "if I touch component X, components A, B, C are impacted."

## Decision

**Add an impact analysis graph endpoint and an interactive dependency diagram in the UI** that lets operators select any entity and see its upstream and downstream dependencies as a human-readable schema.

### Graph model

The impact graph is a **server-side traversal** starting from a given entity, walking relationships in both directions (upstream = "depends on", downstream = "depended upon by"):

```
                          Cluster
                         /   |    \
                      Node  Namespace  PersistentVolume
                       |    / | | \  \         |
                       |  Pod WL Svc Ing     PVC
                       |   |   |
                (node_name) (workload_id)
```

| Relationship | From | To | Direction | Source |
|---|---|---|---|---|
| cluster → nodes | Cluster | Node | downstream | `nodes.cluster_id` FK |
| cluster → namespaces | Cluster | Namespace | downstream | `namespaces.cluster_id` FK |
| cluster → PVs | Cluster | PersistentVolume | downstream | `persistent_volumes.cluster_id` FK |
| namespace → pods | Namespace | Pod | downstream | `pods.namespace_id` FK |
| namespace → workloads | Namespace | Workload | downstream | `workloads.namespace_id` FK |
| namespace → services | Namespace | Service | downstream | `services.namespace_id` FK |
| namespace → ingresses | Namespace | Ingress | downstream | `ingresses.namespace_id` FK |
| namespace → PVCs | Namespace | PVC | downstream | `persistent_volume_claims.namespace_id` FK |
| workload → pods | Workload | Pod | downstream | `pods.workload_id` FK |
| node → pods | Node | Pod | downstream | `pods.node_name` = `nodes.name` (string match) |
| PV → PVC | PersistentVolume | PVC | downstream | `persistent_volume_claims.bound_volume_id` FK |

Each edge is bidirectional for navigation: a Pod shows its upstream Workload, Node, Namespace, and Cluster; a Node shows its downstream Pods.

### API surface

One new endpoint:

```
GET /v1/impact/{entity_type}/{id}?depth=2
```

- `entity_type`: one of `cluster`, `node`, `namespace`, `pod`, `workload`, `service`, `ingress`, `persistentvolume`, `persistentvolumeclaim`
- `id`: entity UUID
- `depth` (optional, default: `2`, max: `3`): how many relationship hops to traverse

Response:

```json
{
  "root": {
    "id": "...",
    "type": "node",
    "name": "worker-1",
    "status": "ready"
  },
  "nodes": [
    { "id": "...", "type": "node", "name": "worker-1", "status": "ready" },
    { "id": "...", "type": "pod", "name": "nginx-abc", "status": "Running" },
    { "id": "...", "type": "workload", "name": "nginx", "kind": "Deployment", "status": "3/3" },
    { "id": "...", "type": "namespace", "name": "production", "status": "Active" },
    { "id": "...", "type": "cluster", "name": "prod-eu-west", "status": "v1.30.2" }
  ],
  "edges": [
    { "from": "<node-id>", "to": "<pod-id>", "relation": "hosts" },
    { "from": "<workload-id>", "to": "<pod-id>", "relation": "owns" },
    { "from": "<namespace-id>", "to": "<pod-id>", "relation": "contains" },
    { "from": "<namespace-id>", "to": "<workload-id>", "relation": "contains" },
    { "from": "<cluster-id>", "to": "<namespace-id>", "relation": "contains" },
    { "from": "<cluster-id>", "to": "<node-id>", "relation": "contains" }
  ]
}
```

The response is a flat graph (nodes + edges) suitable for client-side rendering. The server does the traversal, not the client, to avoid N+1 API calls and to enforce the depth limit.

### Depth limit

Maximum depth is capped at 3 to keep response sizes bounded. At depth 3, starting from a cluster with 50 nodes and 20 namespaces, the worst case is a few hundred graph nodes — still a small JSON payload. The default (depth 2) covers the most common impact questions ("what pods run on this node?", "what workloads use this PVC?").

### UI surface

A new **Impact** tab on each entity detail page (`/ui/clusters/:id`, `/ui/nodes/:id`, etc.) renders the graph as an interactive diagram:

- **Layout**: Hierarchical top-down layout (cluster at top, pods at bottom) using a lightweight client-side graph library.
- **Root highlight**: The selected entity is visually emphasised (border/glow).
- **Node labels**: Entity type icon + name + status indicator.
- **Edge labels**: Relationship type (`hosts`, `owns`, `contains`, `binds`).
- **Click navigation**: Clicking a graph node navigates to that entity's detail page.
- **Depth selector**: Toggle between depth 1, 2, 3.

No external graph database is needed — the traversal runs against the existing PostgreSQL store using the existing FK relationships and indexed columns.

### Relation types

| Relation | Meaning | Example |
|---|---|---|
| `contains` | Parent scope contains child | Cluster → Namespace, Namespace → Pod |
| `owns` | Controller owns managed resource | Workload → Pod |
| `hosts` | Infrastructure hosts workload | Node → Pod |
| `binds` | Storage binding | PV ↔ PVC |

### Security

The endpoint requires the `read` scope (same as listing entities). The traversal only returns entities the caller can already see via the existing list endpoints. No new authorization model is needed.

## Consequences

### Positive

- **POS-001**: Operators can assess blast radius of a change in seconds — "upgrading this node impacts these 12 pods across 3 namespaces."
- **POS-002**: SNC auditors get a visual dependency map exportable for compliance evidence.
- **POS-003**: Incident responders can trace from a failing pod upward to the node and cluster in one view.
- **POS-004**: No new infrastructure — the graph is computed on-the-fly from existing FK relationships.
- **POS-005**: The graph endpoint is reusable by external tooling (CI/CD pipelines, change management systems) via the REST API.

### Negative

- **NEG-001**: The traversal query may be slow for very large clusters at depth 3. Mitigated by the depth cap and the fact that each hop is a simple indexed FK lookup, not a recursive CTE.
- **NEG-002**: The UI needs a graph rendering library, adding a frontend dependency. Mitigated by choosing a lightweight library with no server-side requirements.
- **NEG-003**: The `node_name` relationship (pod → node) is a string match, not a FK. A renamed or missing node produces a broken edge. Mitigated by omitting edges where the target is not found.

## Alternatives Considered

### Store a pre-computed adjacency table

- **Description**: Maintain a `relationships` table updated on every collector tick.
- **Rejection reason**: The relationships are already encoded in the FK columns. A separate table duplicates data, adds write amplification, and can drift. On-the-fly traversal from the source of truth is simpler and always consistent.

### Use a graph database (Neo4j, Dgraph)

- **Description**: Mirror the CMDB into a graph database for traversal.
- **Rejection reason**: Introduces a new infrastructure dependency, requires synchronisation logic, and adds operational burden. The CMDB has ~10 entity types with simple FK relationships — PostgreSQL handles this trivially. Graph databases make sense at thousands of entity types; Argos has 9.

### Client-side traversal via multiple API calls

- **Description**: The UI fetches each entity type independently and assembles the graph in JavaScript.
- **Rejection reason**: N+1 API calls for each hop, poor performance, and the client needs to understand the full relationship model. Server-side traversal keeps the relationship logic in one place and returns a single response.

## Implementation Notes

- **IMP-001**: Add `internal/impact/` package. `Traverser` struct with `Traverse(ctx, entityType, entityID, depth)` method returning `Graph{Nodes, Edges}`. Uses the existing `api.Store` interface — may need a few new query methods (e.g. `ListPodsByNodeName`).
- **IMP-002**: Add `GET /v1/impact/{entity_type}/{id}` to the OpenAPI spec with query param `depth`. Hand-written handler in `internal/api/impact_handlers.go` (same pattern as settings handlers).
- **IMP-003**: UI: add `ImpactGraph` component using a lightweight graph layout library (e.g. `elkjs` for layout + SVG rendering, or `reactflow`). Mount as a tab on each entity detail page.
- **IMP-004**: Tests: unit tests for the traverser with a fake store, verifying correct graph output for each entity type as root. Integration test against Postgres with seeded data.
- **IMP-005**: Add Prometheus metrics: `argos_impact_queries_total{entity_type}`, `argos_impact_query_duration_seconds{entity_type}`.

## References

- **REF-001**: ADR-0001 — CMDB for SNC (foundational architecture)
- **REF-002**: ADR-0008 — SecNumCloud chapter 8 asset management
- **REF-003**: ANSSI SecNumCloud v3.2 — incident management and change impact requirements
- **REF-004**: ITIL Change Management — impact analysis best practices
