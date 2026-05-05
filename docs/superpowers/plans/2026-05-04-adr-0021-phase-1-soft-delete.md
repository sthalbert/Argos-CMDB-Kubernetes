# ADR-0021 Phase 1 — Soft-Delete Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `terminated_at TIMESTAMPTZ` soft-delete to `clusters`, `namespaces`, `nodes`, `workloads`; switch the collector reconcile path from hard `DELETE` to soft-delete `UPDATE`; add `?include_terminated=true` opt-in to the four list endpoints; soft-delete cascades to children in application code; re-appearing entities resurrect via upsert by clearing `terminated_at`.

**Architecture:** New goose migration adds the column + a partial index on each of the four parent tables. The three existing collector-driven `Delete<Kind>NotIn` methods on `*PG` switch their SQL from `DELETE` to `UPDATE … SET terminated_at = NOW() WHERE … AND terminated_at IS NULL`; their interface contract is unchanged so collector callsites keep working. List methods gain a `IncludeTerminated` filter parameter (default false) plumbed through the OpenAPI spec → regenerated stubs → handlers. The three Upsert paths (`UpsertNode`, `UpsertNamespace`, `UpsertWorkload`) explicitly write `terminated_at = NULL` so a re-appearing row resurrects. The two API delete handlers (`DeleteCluster`, `DeleteNamespace`) switch from hard-delete to soft-delete with application-code cascade to children. ADR-0021 §5 / §IMP-001 / §IMP-007.

**Tech Stack:** Go 1.23+, PostgreSQL via pgx/v5, goose migrations, oapi-codegen for OpenAPI → server stubs, integration tests against a Postgres service container gated on `PGX_TEST_DATABASE`.

**Spec:** `docs/adr/adr-0021-time-travel-snapshots.md` — focus on §5 (Soft-delete), §IMP-001 (migration order), §IMP-007 (cascade in application code), §6 (`?include_terminated` precedent from VMs).

**Out of scope (later phases):** History tables, `time_travel_*` settings, `?as_of=` query parameter, `/history` endpoint, reaper, history UI tab, MCP `as_of` parameters.

---

## File Structure

**Created:**
- `migrations/00031_add_terminated_at.sql` — single migration adds the column + partial index on the four tables.

**Modified:**
- `internal/store/pg.go` — `ListClusters`, `ListNodes`, `ListNamespaces`, `ListWorkloads`, `DeleteNodesNotIn`, `DeleteNamespacesNotIn`, `DeleteWorkloadsNotIn`, `UpsertNode`, `UpsertNamespace`, `UpsertWorkload`, `DeleteCluster`, `DeleteNamespace` (cascade), `scanCluster`/`scanNode`/`scanNamespace`/`scanWorkload` (carry `terminated_at` through if surfaced — note: ADR-0021 Phase 1 does **not** expose `terminated_at` in the API response payload; it stays an internal column. The list filter is the only outward-facing change).
- `internal/api/store.go` — interface signatures for the four `List*` methods gain a filter struct or a bool param (see Task 4 for the chosen shape).
- `internal/api/server.go` — four `List*` handlers parse `IncludeTerminated`, pass it through.
- `internal/api/cluster_handlers.go` (or wherever `DeleteCluster` lives — actually in `server.go`) — switch to soft-delete + cascade.
- `api/openapi/openapi.yaml` — shared `IncludeTerminated` parameter component, referenced from the four list paths.
- `internal/api/api.gen.go` — regenerated; do not hand-edit.
- `internal/store/pg_test.go` — new tests for soft-delete reconcile, resurrection, list filter, cascade.
- `internal/api/server_test.go` — new tests for the list `?include_terminated` query param, soft-delete handler.

**Migration numbering:** `00030_audit_events_source_mcp.sql` already exists. The ADR's `00030_add_terminated_at` slot is taken; this phase ships as `00031_add_terminated_at.sql`. Phase 2 history-table migrations renumber accordingly.

---

## Task 1: Add the migration

**Files:**
- Create: `migrations/00031_add_terminated_at.sql`

- [ ] **Step 1: Write the migration**

```sql
-- +goose Up
-- ADR-0021 Phase 1: soft-delete foundation. Each of clusters, namespaces,
-- nodes, workloads gains a nullable terminated_at TIMESTAMPTZ. List
-- endpoints filter it out by default; the collector reconcile path
-- soft-deletes via UPDATE rather than DELETE so history of which
-- entities existed and when they were reaped survives a tick. Mirrors
-- the existing virtual_machines.terminated_at pattern from ADR-0015 §2.

ALTER TABLE clusters   ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE namespaces ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE nodes      ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;
ALTER TABLE workloads  ADD COLUMN IF NOT EXISTS terminated_at TIMESTAMPTZ;

-- Partial indexes — only terminated rows are indexed, keeping the
-- live-row query path index-free for terminated_at and the index
-- itself tiny in steady state. Mirror of virtual_machines_terminated_at_idx.
CREATE INDEX IF NOT EXISTS clusters_terminated_at_idx
    ON clusters (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS namespaces_terminated_at_idx
    ON namespaces (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS nodes_terminated_at_idx
    ON nodes (terminated_at) WHERE terminated_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS workloads_terminated_at_idx
    ON workloads (terminated_at) WHERE terminated_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS workloads_terminated_at_idx;
DROP INDEX IF EXISTS nodes_terminated_at_idx;
DROP INDEX IF EXISTS namespaces_terminated_at_idx;
DROP INDEX IF EXISTS clusters_terminated_at_idx;

ALTER TABLE workloads  DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE nodes      DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE namespaces DROP COLUMN IF EXISTS terminated_at;
ALTER TABLE clusters   DROP COLUMN IF EXISTS terminated_at;
```

- [ ] **Step 2: Verify the migration compiles into the embed**

The migrations package uses `//go:embed` (see `migrations/embed.go`). New `.sql` files are picked up automatically — just confirm:

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 3: Commit**

```bash
git add migrations/00031_add_terminated_at.sql
git commit -m "feat(migrations): add terminated_at to clusters/namespaces/nodes/workloads (ADR-0021 phase 1)"
```

---

## Task 2: Reconcile becomes soft-delete (PG store)

The three collector-driven `Delete<Kind>NotIn` methods change SQL semantics from hard `DELETE` to soft-delete `UPDATE`. Method names and signatures stay the same to keep callsites in `internal/collector/collector.go` working unchanged — the doc comment notes the semantics shift. The contract: returns the count of rows newly soft-deleted (those that were `terminated_at IS NULL` before and aren't in the keep list).

**Files:**
- Modify: `internal/store/pg.go:745-770` (`DeleteNodesNotIn`, `DeleteNamespacesNotIn`)
- Modify: `internal/store/pg.go:1634-1650` (`DeleteWorkloadsNotIn`)
- Test: `internal/store/pg_test.go` (extend the existing `TestPGDeleteNodesNotIn` family)

- [ ] **Step 1: Write the failing test for `DeleteNodesNotIn` soft-delete semantics**

Add to `internal/store/pg_test.go` (next to `TestPGDeleteNodesNotIn`):

```go
func TestPGDeleteNodesNotIn_SoftDeletesAndIdempotent(t *testing.T) {
    pg := openTestPG(t)
    ctx := context.Background()

    cluster := mustCreateCluster(t, pg, "c-soft")
    nA := mustCreateNode(t, pg, cluster.ID, "node-a")
    nB := mustCreateNode(t, pg, cluster.ID, "node-b")
    nC := mustCreateNode(t, pg, cluster.ID, "node-c")

    // Reconcile: keep only node-b. Expect a-and-c to be soft-deleted (count 2),
    // not hard-deleted. Both rows must still exist in the table with
    // terminated_at set.
    affected, err := pg.DeleteNodesNotIn(ctx, cluster.ID, []string{"node-b"})
    if err != nil { t.Fatal(err) }
    if affected != 2 { t.Fatalf("affected=%d want 2", affected) }

    var liveCount, termCount int
    if err := pg.pool.QueryRow(ctx,
        "SELECT COUNT(*) FROM nodes WHERE cluster_id=$1 AND terminated_at IS NULL",
        cluster.ID).Scan(&liveCount); err != nil { t.Fatal(err) }
    if err := pg.pool.QueryRow(ctx,
        "SELECT COUNT(*) FROM nodes WHERE cluster_id=$1 AND terminated_at IS NOT NULL",
        cluster.ID).Scan(&termCount); err != nil { t.Fatal(err) }
    if liveCount != 1 { t.Fatalf("liveCount=%d want 1", liveCount) }
    if termCount != 2 { t.Fatalf("termCount=%d want 2", termCount) }

    // Idempotency: a second reconcile with the same keep list flips no rows.
    affected2, err := pg.DeleteNodesNotIn(ctx, cluster.ID, []string{"node-b"})
    if err != nil { t.Fatal(err) }
    if affected2 != 0 { t.Fatalf("affected2=%d want 0", affected2) }

    _ = nA; _ = nB; _ = nC
}
```

(Helpers `openTestPG`, `mustCreateCluster`, `mustCreateNode` exist in `pg_test.go` — reuse the patterns near `TestPGDeleteNodesNotIn`.)

- [ ] **Step 2: Run the test, expect it to fail**

Run: `make test-one TEST=TestPGDeleteNodesNotIn_SoftDeletesAndIdempotent`
Expected: FAIL — `affected=2 want 2` may pass spuriously because DELETE also returns 2; but the row-count assertions will fail (`liveCount=1, termCount=0`) because rows are hard-deleted today.

- [ ] **Step 3: Switch `DeleteNodesNotIn` SQL to UPDATE**

Replace the body at `internal/store/pg.go:745-756`:

```go
// DeleteNodesNotIn soft-deletes every node of the given cluster whose name is
// not in keepNames AND that is not already terminated. Returns the number of
// rows newly soft-deleted. Despite the name, this is a soft-delete: per
// ADR-0021 §5 the row stays in the table with terminated_at = NOW() so list
// queries can opt back in via include_terminated, history (Phase 2) can
// reconstruct the lifecycle, and a re-appearing node resurrects via the
// upsert path. keepNames may be nil or empty (soft-deletes every live node
// for the cluster).
func (p *PG) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
    tag, err := p.pool.Exec(ctx,
        `UPDATE nodes
            SET terminated_at = NOW(), updated_at = NOW()
          WHERE cluster_id = $1
            AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
            AND terminated_at IS NULL`,
        clusterID, keepNames,
    )
    if err != nil {
        return 0, fmt.Errorf("soft-delete nodes not in: %w", err)
    }
    return tag.RowsAffected(), nil
}
```

- [ ] **Step 4: Switch `DeleteNamespacesNotIn` SQL to UPDATE**

Replace the body at `internal/store/pg.go:759-770`:

```go
// DeleteNamespacesNotIn mirrors DeleteNodesNotIn: soft-deletes namespaces
// of the given cluster not in keepNames and not already terminated.
func (p *PG) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
    tag, err := p.pool.Exec(ctx,
        `UPDATE namespaces
            SET terminated_at = NOW(), updated_at = NOW()
          WHERE cluster_id = $1
            AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))
            AND terminated_at IS NULL`,
        clusterID, keepNames,
    )
    if err != nil {
        return 0, fmt.Errorf("soft-delete namespaces not in: %w", err)
    }
    return tag.RowsAffected(), nil
}
```

- [ ] **Step 5: Switch `DeleteWorkloadsNotIn` SQL to UPDATE**

Replace the body at `internal/store/pg.go:1634-1650`:

```go
// DeleteWorkloadsNotIn soft-deletes workloads in the namespace whose
// (kind, name) tuple is not in the parallel keep arrays and that are not
// already terminated. Per ADR-0021 §5; same semantics as DeleteNodesNotIn.
func (p *PG) DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
    tag, err := p.pool.Exec(ctx,
        `UPDATE workloads
            SET terminated_at = NOW(), updated_at = NOW()
          WHERE namespace_id = $1
            AND (kind, name) NOT IN (
                SELECT k, n FROM UNNEST(
                    COALESCE($2::text[], ARRAY[]::text[]),
                    COALESCE($3::text[], ARRAY[]::text[])
                ) AS t(k, n)
            )
            AND terminated_at IS NULL`,
        namespaceID, keepKinds, keepNames,
    )
    if err != nil {
        return 0, fmt.Errorf("soft-delete workloads not in: %w", err)
    }
    return tag.RowsAffected(), nil
}
```

- [ ] **Step 6: Run nodes test, expect pass**

Run: `make test-one TEST=TestPGDeleteNodesNotIn_SoftDeletesAndIdempotent`
Expected: PASS.

- [ ] **Step 7: Run all store tests to confirm no regression**

Run: `go test ./internal/store/... -race`
Expected: existing tests that asserted hard-delete will fail. Update them: `TestPGDeleteNodesNotIn` and any `TestPG*DeleteNamespacesNotIn` / `TestPG*DeleteWorkloadsNotIn` that read `SELECT COUNT(*) FROM <table>` to verify deletion must add `WHERE terminated_at IS NULL` (the rows are now soft-deleted).

- [ ] **Step 8: Commit**

```bash
git add internal/store/pg.go internal/store/pg_test.go
git commit -m "refactor(store): soft-delete reconcile via UPDATE terminated_at (ADR-0021 phase 1)"
```

---

## Task 3: Upsert paths resurrect terminated rows

When a soft-deleted entity reappears in a cluster (transient API outage on the previous tick, then recovery), the next upsert tick must clear `terminated_at` so it goes back to live. The three Upsert methods need an explicit `terminated_at = NULL` in the `ON CONFLICT … DO UPDATE SET …` clause.

**Files:**
- Modify: `internal/store/pg.go` — `UpsertNode`, `UpsertNamespace`, `UpsertWorkload`
- Test: `internal/store/pg_test.go`

- [ ] **Step 1: Locate the three upsert methods**

Run: `grep -n "func (p \*PG) Upsert" internal/store/pg.go`
Expected: three matches, one per kind. Note their line numbers.

- [ ] **Step 2: Write the failing resurrection test**

Add to `internal/store/pg_test.go`:

```go
func TestPGUpsertNode_ResurrectsTerminated(t *testing.T) {
    pg := openTestPG(t)
    ctx := context.Background()
    cluster := mustCreateCluster(t, pg, "c-resurrect")

    // Create + reconcile-out + verify it's terminated.
    n := mustCreateNode(t, pg, cluster.ID, "node-x")
    if _, err := pg.DeleteNodesNotIn(ctx, cluster.ID, nil); err != nil { t.Fatal(err) }

    var termAt sql.NullTime
    if err := pg.pool.QueryRow(ctx,
        "SELECT terminated_at FROM nodes WHERE id=$1", n.ID).Scan(&termAt); err != nil { t.Fatal(err) }
    if !termAt.Valid { t.Fatal("expected node terminated") }

    // Now upsert the same (cluster_id, name) again — it should resurrect.
    if _, err := pg.UpsertNode(ctx, cluster.ID, /* fill the same NodeInfo as mustCreateNode */); err != nil {
        t.Fatal(err)
    }
    if err := pg.pool.QueryRow(ctx,
        "SELECT terminated_at FROM nodes WHERE id=$1", n.ID).Scan(&termAt); err != nil { t.Fatal(err) }
    if termAt.Valid { t.Fatalf("expected resurrected; got terminated_at=%v", termAt.Time) }
}
```

(The exact `UpsertNode` signature is whatever exists in `pg.go` today — match it. Use `mustCreateNode`'s implementation as the template for the upsert call.)

- [ ] **Step 3: Run the test, expect failure**

Run: `make test-one TEST=TestPGUpsertNode_ResurrectsTerminated`
Expected: FAIL — `terminated_at` is still set after upsert.

- [ ] **Step 4: Add `terminated_at = NULL` to each upsert's ON CONFLICT clause**

For each of `UpsertNode`, `UpsertNamespace`, `UpsertWorkload` in `internal/store/pg.go`, find the `ON CONFLICT … DO UPDATE SET` clause and add `terminated_at = NULL` to the SET list. Example pattern:

```go
// existing
ON CONFLICT (cluster_id, name) DO UPDATE SET
    role = EXCLUDED.role,
    updated_at = NOW()
// becomes
ON CONFLICT (cluster_id, name) DO UPDATE SET
    role = EXCLUDED.role,
    terminated_at = NULL,
    updated_at = NOW()
```

- [ ] **Step 5: Run the test, expect pass**

Run: `make test-one TEST=TestPGUpsertNode_ResurrectsTerminated`
Expected: PASS.

- [ ] **Step 6: Add equivalent tests for namespace and workload resurrection**

Mirror the node test for `UpsertNamespace` (key on `(cluster_id, name)`) and `UpsertWorkload` (key on `(namespace_id, kind, name)`). Run them; expect PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/pg.go internal/store/pg_test.go
git commit -m "feat(store): resurrect soft-deleted rows on upsert (ADR-0021 phase 1)"
```

---

## Task 4: List methods filter `terminated_at IS NULL` by default

`ListClusters`, `ListNodes`, `ListNamespaces`, `ListWorkloads` must filter out terminated rows by default and accept an opt-in `IncludeTerminated bool`. Choice: extend each method's signature with an `includeTerminated bool` argument. The interface change is small (four methods) and avoids inventing new filter structs for the three kinds that don't have one.

**Files:**
- Modify: `internal/api/store.go` — interface declarations.
- Modify: `internal/store/pg.go` — `ListClusters`, `ListNodes`, `ListNamespaces`, `ListWorkloads` impls.
- Modify: `internal/api/server.go` — four list handlers, pass through.
- Modify: any `Store`-implementer fakes used in handler tests (mcp/impact fakes).

- [ ] **Step 1: Update the Store interface**

In `internal/api/store.go`:

```go
// before
ListClusters(ctx context.Context, limit int, cursor string) ([]Cluster, string, error)
ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]Node, string, error)
ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]Namespace, string, error)
ListWorkloads(ctx context.Context, filter WorkloadListFilter, limit int, cursor string) ([]Workload, string, error)

// after — extra trailing bool keeps callsite churn minimal
ListClusters(ctx context.Context, limit int, cursor string, includeTerminated bool) ([]Cluster, string, error)
ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string, includeTerminated bool) ([]Node, string, error)
ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string, includeTerminated bool) ([]Namespace, string, error)
// Workloads already has a filter struct — extend it:
type WorkloadListFilter struct {
    NamespaceID    *uuid.UUID
    Kind           *string
    ImageSubstring *string
    NodeName       *string
    IncludeTerminated bool // ADR-0021 phase 1
}
```

- [ ] **Step 2: Update the four PG implementations**

In `internal/store/pg.go`, for each List method, add the `includeTerminated` parameter and an early `if !includeTerminated { conds = append(conds, "terminated_at IS NULL") }` (mirror the VM pattern at `internal/store/pg_virtual_machines.go:217-219`). For Workloads, read the flag from `filter.IncludeTerminated` instead of a separate parameter.

The List methods that build SQL via string concat already have a `WHERE` builder pattern in some cases; for those that don't (e.g. `ListClusters` likely uses a static query), add the conditional inline.

- [ ] **Step 3: Run the existing list tests; fix breakage**

Run: `go test ./internal/store/... -race`
Expected: every callsite of the four List methods now mismatches the signature and fails to compile. Fix each callsite by passing `false` (the safe default) for the new parameter.

- [ ] **Step 4: Add a list-filter test**

```go
func TestPGListNodes_ExcludesTerminatedByDefault(t *testing.T) {
    pg := openTestPG(t)
    ctx := context.Background()
    cluster := mustCreateCluster(t, pg, "c-list")
    mustCreateNode(t, pg, cluster.ID, "live-1")
    mustCreateNode(t, pg, cluster.ID, "live-2")
    mustCreateNode(t, pg, cluster.ID, "soon-dead")
    if _, err := pg.DeleteNodesNotIn(ctx, cluster.ID, []string{"live-1", "live-2"}); err != nil {
        t.Fatal(err)
    }

    cid := cluster.ID
    items, _, err := pg.ListNodes(ctx, &cid, 50, "", false)
    if err != nil { t.Fatal(err) }
    if len(items) != 2 { t.Fatalf("default list returned %d, want 2", len(items)) }

    items, _, err = pg.ListNodes(ctx, &cid, 50, "", true)
    if err != nil { t.Fatal(err) }
    if len(items) != 3 { t.Fatalf("include_terminated list returned %d, want 3", len(items)) }
}
```

Run: `make test-one TEST=TestPGListNodes_ExcludesTerminatedByDefault`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/store.go internal/store/pg.go internal/store/pg_test.go
git commit -m "feat(store): include_terminated list filter on clusters/nodes/namespaces/workloads (ADR-0021 phase 1)"
```

---

## Task 5: OpenAPI parameter + regenerate stubs

**Files:**
- Modify: `api/openapi/openapi.yaml` — add `IncludeTerminated` parameter component, reference it from the four list endpoints.
- Regenerate: `internal/api/api.gen.go`

- [ ] **Step 1: Add the shared parameter**

Insert after the `Cursor:` definition at `api/openapi/openapi.yaml:2404-2411`, inside the `parameters:` block:

```yaml
    IncludeTerminated:
      name: include_terminated
      in: query
      required: false
      description: |
        When true, include rows whose `terminated_at` is set (soft-deleted by
        the collector reconcile pass). Defaults to false: only live entities
        are returned. ADR-0021 §5 / §6.
      schema:
        type: boolean
        default: false
```

- [ ] **Step 2: Reference it from the four list paths**

Add `- $ref: '#/components/parameters/IncludeTerminated'` to the `parameters:` array of:
- `/v1/clusters` GET (around line 113)
- `/v1/nodes` GET (around line 264)
- `/v1/namespaces` GET (around line 394)
- `/v1/workloads` GET (around line 659)

- [ ] **Step 3: Regenerate the server stubs**

Run: `make generate`
Expected: `internal/api/api.gen.go` updated; the four `List<Kind>Params` structs each grow an `IncludeTerminated *bool` field.

- [ ] **Step 4: Confirm the build is broken in handlers (good signal)**

Run: `go build ./...`
Expected: handler call sites of `s.store.List<Kind>` no longer pass enough args, OR pass `false` literally; we'll fix them in the next task. If the build still passes (because we already pass `false` from Task 4 step 3), proceed — Task 6 just upgrades the literal to the parsed param.

- [ ] **Step 5: Commit**

```bash
git add api/openapi/openapi.yaml internal/api/api.gen.go
git commit -m "feat(api): add include_terminated query param to list endpoints (ADR-0021 phase 1)"
```

---

## Task 6: Wire the query param through the four list handlers

**Files:**
- Modify: `internal/api/server.go` — `ListClusters`, `ListNodes`, `ListNamespaces`, `ListWorkloads`.
- Test: `internal/api/server_test.go`

- [ ] **Step 1: Locate the four handlers**

`ListClusters` near `internal/api/server.go:144`, `ListNodes` near `:329`, `ListNamespaces` near `:448`, `ListWorkloads` near `:684`.

- [ ] **Step 2: Plumb `IncludeTerminated` from `req.Params` to the store call**

For each handler, add:

```go
includeTerminated := false
if req.Params.IncludeTerminated != nil {
    includeTerminated = *req.Params.IncludeTerminated
}
```

…then pass `includeTerminated` as the new trailing argument to `s.store.List<Kind>(...)`. For `ListWorkloads`, set `filter.IncludeTerminated = includeTerminated` on the existing `WorkloadListFilter`.

- [ ] **Step 3: Run the build**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Add a handler-level test**

In `internal/api/server_test.go` (follow the patterns in `cluster_curated_test.go`):

```go
func TestListNodes_IncludeTerminated(t *testing.T) {
    srv, store := newTestServer(t) // existing helper pattern
    cluster := mustCreateClusterAPI(t, srv, "c-handler")
    mustCreateNodeAPI(t, srv, cluster.ID, "live-only")
    mustCreateNodeAPI(t, srv, cluster.ID, "to-be-killed")
    // Soft-delete via the store directly (collector path)
    if _, err := store.DeleteNodesNotIn(context.Background(), cluster.ID, []string{"live-only"}); err != nil {
        t.Fatal(err)
    }

    // Default: terminated excluded.
    body := doGET(t, srv, "/v1/nodes?cluster_id="+cluster.ID.String())
    if got := gjsonLen(body, "items"); got != 1 {
        t.Fatalf("default len=%d want 1", got)
    }
    // Opt-in.
    body = doGET(t, srv, "/v1/nodes?cluster_id="+cluster.ID.String()+"&include_terminated=true")
    if got := gjsonLen(body, "items"); got != 2 {
        t.Fatalf("opt-in len=%d want 2", got)
    }
}
```

(Use whatever helpers `server_test.go` already provides; if no `gjsonLen`, decode with `encoding/json` and `len(resp.Items)`.)

- [ ] **Step 5: Run the new test**

Run: `make test-one TEST=TestListNodes_IncludeTerminated`
Expected: PASS.

- [ ] **Step 6: Run the full API test suite**

Run: `go test ./internal/api/... -race`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go
git commit -m "feat(api): list handlers honor include_terminated (ADR-0021 phase 1)"
```

---

## Task 7: Soft-delete cascade — `DeleteCluster` and `DeleteNamespace` API handlers

The existing `DeleteCluster` / `DeleteNamespace` API handlers hard-delete via the FK CASCADE chain. Per ADR-0021 §IMP-007, they switch to soft-delete + application-code cascade.

**Files:**
- Modify: `internal/store/pg.go` — add `SoftDeleteCluster`, `SoftDeleteNamespace` (transactional, cascading).
- Modify: `internal/api/store.go` — interface.
- Modify: `internal/api/server.go` — `DeleteCluster`, `DeleteNamespace` handlers call the soft-delete instead of `DeleteCluster`/`DeleteNamespace`.
- Modify: `internal/store/pg_test.go` — cascade test.

- [ ] **Step 1: Add `SoftDeleteCluster` to the PG store**

Add to `internal/store/pg.go` (next to `DeleteCluster`):

```go
// SoftDeleteCluster marks the cluster and all its live children
// (namespaces, nodes, workloads) as terminated in a single transaction.
// Mirrors ADR-0021 §IMP-007. Children that are already terminated are
// skipped via the AND terminated_at IS NULL guard. Pods, services,
// ingresses, PVs, and PVCs are unaffected here — they continue to be
// reaped by the FK ON DELETE CASCADE chain only when the cluster is
// hard-deleted; under soft-delete they remain attached to the (now
// soft-deleted) parent. Phase 2 reconsiders pod-level reconciliation.
func (p *PG) SoftDeleteCluster(ctx context.Context, id uuid.UUID) error {
    tx, err := p.pool.Begin(ctx)
    if err != nil { return fmt.Errorf("begin: %w", err) }
    defer tx.Rollback(ctx)

    if _, err := tx.Exec(ctx,
        `UPDATE workloads SET terminated_at = NOW(), updated_at = NOW()
           WHERE namespace_id IN (SELECT id FROM namespaces WHERE cluster_id = $1)
             AND terminated_at IS NULL`, id); err != nil {
        return fmt.Errorf("soft-delete workloads: %w", err)
    }
    if _, err := tx.Exec(ctx,
        `UPDATE namespaces SET terminated_at = NOW(), updated_at = NOW()
           WHERE cluster_id = $1 AND terminated_at IS NULL`, id); err != nil {
        return fmt.Errorf("soft-delete namespaces: %w", err)
    }
    if _, err := tx.Exec(ctx,
        `UPDATE nodes SET terminated_at = NOW(), updated_at = NOW()
           WHERE cluster_id = $1 AND terminated_at IS NULL`, id); err != nil {
        return fmt.Errorf("soft-delete nodes: %w", err)
    }
    tag, err := tx.Exec(ctx,
        `UPDATE clusters SET terminated_at = NOW(), updated_at = NOW()
           WHERE id = $1 AND terminated_at IS NULL`, id)
    if err != nil { return fmt.Errorf("soft-delete cluster: %w", err) }
    if tag.RowsAffected() == 0 {
        // Either the cluster doesn't exist or it's already terminated —
        // distinguish via a follow-up SELECT for the not-found case.
        var exists bool
        if err := tx.QueryRow(ctx,
            `SELECT EXISTS(SELECT 1 FROM clusters WHERE id = $1)`, id).Scan(&exists); err != nil {
            return err
        }
        if !exists { return api.ErrNotFound }
        // Already terminated — idempotent success.
    }
    return tx.Commit(ctx)
}
```

- [ ] **Step 2: Add `SoftDeleteNamespace`**

```go
// SoftDeleteNamespace soft-deletes the namespace and its workloads.
// Pods/services/ingresses/PVCs are not touched (Phase 2 follow-up).
func (p *PG) SoftDeleteNamespace(ctx context.Context, id uuid.UUID) error {
    tx, err := p.pool.Begin(ctx)
    if err != nil { return fmt.Errorf("begin: %w", err) }
    defer tx.Rollback(ctx)

    if _, err := tx.Exec(ctx,
        `UPDATE workloads SET terminated_at = NOW(), updated_at = NOW()
           WHERE namespace_id = $1 AND terminated_at IS NULL`, id); err != nil {
        return fmt.Errorf("soft-delete workloads: %w", err)
    }
    tag, err := tx.Exec(ctx,
        `UPDATE namespaces SET terminated_at = NOW(), updated_at = NOW()
           WHERE id = $1 AND terminated_at IS NULL`, id)
    if err != nil { return fmt.Errorf("soft-delete namespace: %w", err) }
    if tag.RowsAffected() == 0 {
        var exists bool
        if err := tx.QueryRow(ctx,
            `SELECT EXISTS(SELECT 1 FROM namespaces WHERE id = $1)`, id).Scan(&exists); err != nil {
            return err
        }
        if !exists { return api.ErrNotFound }
    }
    return tx.Commit(ctx)
}
```

- [ ] **Step 3: Add the two methods to the `Store` interface**

In `internal/api/store.go`:

```go
SoftDeleteCluster(ctx context.Context, id uuid.UUID) error
SoftDeleteNamespace(ctx context.Context, id uuid.UUID) error
```

- [ ] **Step 4: Switch the two API handlers**

In `internal/api/server.go`, find `DeleteCluster` and `DeleteNamespace` handlers. Change the call from `s.store.DeleteCluster(ctx, id)` (or whatever the current call is) to `s.store.SoftDeleteCluster(ctx, id)`; same for namespace. Response shape (204 No Content on success, 404 on not found) is unchanged.

- [ ] **Step 5: Write the cascade test**

```go
func TestPGSoftDeleteCluster_CascadesToChildren(t *testing.T) {
    pg := openTestPG(t)
    ctx := context.Background()
    cluster := mustCreateCluster(t, pg, "c-cascade")
    ns := mustCreateNamespace(t, pg, cluster.ID, "ns-1")
    _ = mustCreateNode(t, pg, cluster.ID, "node-1")
    _ = mustCreateWorkload(t, pg, ns.ID, "Deployment", "wl-1")

    if err := pg.SoftDeleteCluster(ctx, cluster.ID); err != nil { t.Fatal(err) }

    // All four entities are now terminated.
    for _, q := range []string{
        `SELECT terminated_at IS NOT NULL FROM clusters WHERE id=$1`,
    } {
        var ok bool
        if err := pg.pool.QueryRow(ctx, q, cluster.ID).Scan(&ok); err != nil { t.Fatal(err) }
        if !ok { t.Fatalf("cluster not terminated: %s", q) }
    }
    var nsTerm, nodeTerm, wlTerm bool
    pg.pool.QueryRow(ctx, `SELECT terminated_at IS NOT NULL FROM namespaces WHERE cluster_id=$1`, cluster.ID).Scan(&nsTerm)
    pg.pool.QueryRow(ctx, `SELECT terminated_at IS NOT NULL FROM nodes WHERE cluster_id=$1`, cluster.ID).Scan(&nodeTerm)
    pg.pool.QueryRow(ctx, `SELECT terminated_at IS NOT NULL FROM workloads WHERE namespace_id=$1`, ns.ID).Scan(&wlTerm)
    if !nsTerm || !nodeTerm || !wlTerm {
        t.Fatalf("cascade missed: ns=%v node=%v wl=%v", nsTerm, nodeTerm, wlTerm)
    }
}
```

- [ ] **Step 6: Run the cascade test**

Run: `make test-one TEST=TestPGSoftDeleteCluster_CascadesToChildren`
Expected: PASS.

- [ ] **Step 7: Update existing `TestPG*Delete*` tests**

Any existing test that asserts hard-delete semantics on `DeleteCluster` / `DeleteNamespace` (counts after delete) needs updating to soft-delete semantics. Run `go test ./... -race` and fix breakage.

- [ ] **Step 8: Commit**

```bash
git add internal/api/store.go internal/api/server.go internal/store/pg.go internal/store/pg_test.go
git commit -m "feat(api): soft-delete cluster/namespace cascade to children (ADR-0021 phase 1)"
```

---

## Task 8: Full check + ADR backreference

- [ ] **Step 1: Run the full check**

Run: `make check`
Expected: fmt + vet + lint + test all clean.

- [ ] **Step 2: Run the OpenAPI validation tests**

Run: `go test ./internal/api/... -run OpenAPIValidation -race`
Expected: PASS — confirms the new `include_terminated` parameter validates against the spec.

- [ ] **Step 3: Update the ADR's status note (optional, only if PR touches the ADR)**

Leave the ADR file alone — Phase 1 doesn't change the decision text. The migration `00031_add_terminated_at` references ADR-0021 in its leading comment, which is enough provenance.

- [ ] **Step 4: Final commit if anything else changed**

```bash
git status
# if anything stray:
git add -p
git commit -m "chore: post-phase-1 polish (ADR-0021 phase 1)"
```

---

## Self-Review

- **Spec coverage** — §5 (terminated_at on the four kinds + reconcile UPDATE + list filter + resurrect on upsert) ✔ Tasks 1-4. §IMP-001 migration shape ✔ Task 1 (renumbered 30→31 because 00030 is taken). §IMP-007 cascade in app code ✔ Task 7. §6 `?include_terminated` precedent ✔ Tasks 5-6. Phase-1 explicit non-goals (history tables, settings flag, `?as_of=`, `/history`, reaper, UI tab, MCP) ✔ none in scope, all belong to Phase 2-4.
- **Placeholders** — none.
- **Type consistency** — `includeTerminated bool` parameter is consistent across the three non-Workload List methods; `WorkloadListFilter.IncludeTerminated` is the field name on the struct (matches `VirtualMachineListFilter.IncludeTerminated` precedent). `SoftDeleteCluster` / `SoftDeleteNamespace` are the new method names; existing `DeleteCluster` / `DeleteNamespace` stay (the latter currently hard-delete via API, but Task 7 step 4 redirects the handler to call SoftDelete*; the underlying `DeleteCluster` / `DeleteNamespace` PG methods remain available for tests / migrations / future hard-delete tooling).
- **Risks** — Migration is additive and idempotent (`IF NOT EXISTS`); rollback is clean. Existing tests asserting hard-delete row counts will break — Task 2 step 7 and Task 7 step 7 are the catch-all updates. The four list handler tests in `internal/api/server_test.go` may have hard-delete assumptions baked in; the `make check` in Task 8 step 1 surfaces any remaining issues.
