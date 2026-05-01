# Impact Analysis

longue-vue provides an interactive dependency graph that lets you assess the blast radius of a change before you make it. Select any entity in the CMDB and see what depends on it — and what it depends on — in a single view.

## How it works

The impact graph traverses the existing FK relationships in the CMDB (cluster → namespace → workload → pod, node → pod, PV → PVC, etc.) starting from a selected entity. It walks both upstream (what does this depend on?) and downstream (what depends on this?) up to a configurable depth.

No pre-computed adjacency table or graph database is needed — the traversal runs on-the-fly against PostgreSQL using existing indexed columns.

## Use the impact graph

Every entity detail page includes an **Impact graph** section at the bottom.

1. Navigate to any entity detail page (cluster, node, namespace, workload, pod, or ingress).
2. Scroll to the **Impact graph** section.
3. The graph renders automatically at depth 2.
4. Use the **depth buttons** (1, 2, 3) to expand or collapse the traversal range.
5. **Click any node** in the graph to navigate to that entity's detail page.

The root entity (the one you're viewing) is highlighted with a coloured border matching its entity type.

## Relationship types

The graph displays four types of relationships:

| Relation | Meaning | Example |
|----------|---------|---------|
| `contains` | Parent scope contains child | Cluster → Namespace, Namespace → Pod |
| `owns` | Controller owns managed resource | Workload → Pod |
| `hosts` | Infrastructure hosts workload | Node → Pod |
| `binds` | Storage binding | PV ↔ PVC |

## Depth levels

| Depth | What you see | Use case |
|-------|-------------|----------|
| **1** | Direct neighbours only | Quick check: what's immediately connected? |
| **2** (default) | Two hops | Most common: "what pods run on this node and what workloads own them?" |
| **3** | Three hops | Full picture: cluster → namespace → workload → pod chain |

Depth is capped at 3 to keep response sizes bounded.

## API endpoint

The graph is also available via the REST API for external tooling (CI/CD, change management):

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/v1/impact/node/<uuid>?depth=2' | jq .
```

See the [API Reference](api-reference.md#impact-analysis) for the full response schema.

## Supported entity types

All 9 CMDB entity types can be used as the graph root:

- Cluster, Node, Namespace
- Pod, Workload (Deployment / StatefulSet / DaemonSet)
- Service, Ingress
- PersistentVolume, PersistentVolumeClaim

## Monitoring

The impact endpoint exports Prometheus metrics:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_impact_queries_total` | counter | `entity_type` | Number of impact graph queries. |
| `longue_vue_impact_query_duration_seconds` | histogram | `entity_type` | Query duration in seconds. |
