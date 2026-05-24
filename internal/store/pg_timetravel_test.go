package store

// pg_timetravel_test.go — integration tests for history capture wired into
// the PG store. All tests require PGX_TEST_DATABASE to be set. Tests verify
// that:
//   - EnsureCluster writes a "create" history row.
//   - UpdateCluster with a non-watched field writes nothing; with a watched
//     field closes the prior row and writes a new one.
//   - SoftDeleteCluster writes "soft_delete" rows for the cluster, namespaces,
//     nodes, and workloads.
//   - UpsertNamespace with terminated_at cleared writes "restore".

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestTimeTravelCapture_CreateCluster(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "tt-create-" + uuid.New().String()[:8]
	cluster, created, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)
	assert.True(t, created)

	var count int
	err = pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM clusters_history WHERE entity_id=$1 AND change_type='create'`,
		*cluster.Id,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "expected one create history row")
}

func TestTimeTravelCapture_NoopUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "tt-noop-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)

	// Update with only a non-watched field (display_name) — should not write history.
	displayName := "irrelevant display"
	_, err = pg.UpdateCluster(ctx, *cluster.Id, api.ClusterUpdate{DisplayName: &displayName})
	require.NoError(t, err)

	// Should still be only 1 history row (the create).
	var count int
	err = pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM clusters_history WHERE entity_id=$1`,
		*cluster.Id,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "noop update should not write a history row")
}

func TestTimeTravelCapture_WatchedFieldUpdate(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "tt-watched-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)

	// Update a watched field (kubernetes_version).
	ver := "1.29.5"
	_, err = pg.UpdateCluster(ctx, *cluster.Id, api.ClusterUpdate{KubernetesVersion: &ver})
	require.NoError(t, err)

	// Should have 2 rows: the create + the update.
	var count int
	err = pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM clusters_history WHERE entity_id=$1`,
		*cluster.Id,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 2, count, "watched field update should write a history row")

	// The create row should now have valid_to set (closed).
	var closedCount int
	err = pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM clusters_history WHERE entity_id=$1 AND change_type='create' AND valid_to IS NOT NULL`,
		*cluster.Id,
	).Scan(&closedCount)
	require.NoError(t, err)
	assert.Equal(t, 1, closedCount, "prior create row should be closed (valid_to != NULL)")
}

func TestTimeTravelCapture_SoftDeleteCascade(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Create a cluster with a namespace, node, and workload.
	name := "tt-softdel-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)
	clusterID := *cluster.Id

	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: clusterID, Name: "ns-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)

	node, _, err := pg.UpsertNode(ctx, api.NodeCreate{
		ClusterId: clusterID, Name: "node-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)

	wl, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.Deployment,
		Name:        "wl-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)

	// Soft-delete the cluster (cascades to namespace, node, workload).
	err = pg.SoftDeleteCluster(ctx, clusterID)
	require.NoError(t, err)

	// Each entity should have a soft_delete history row.
	checkSDRow := func(table, label string, entityID uuid.UUID) {
		t.Helper()
		var count int
		q := `SELECT COUNT(*) FROM ` + table + ` WHERE entity_id=$1 AND change_type='soft_delete'`
		require.NoError(t, pg.pool.QueryRow(ctx, q, entityID).Scan(&count), label)
		assert.Equal(t, 1, count, label+" should have a soft_delete history row")
	}
	checkSDRow("clusters_history", "cluster", clusterID)
	checkSDRow("namespaces_history", "namespace", *ns.Id)
	checkSDRow("nodes_history", "node", *node.Id)
	checkSDRow("workloads_history", "workload", *wl.Id)
}

// TestTimeTravelCapture_WorkloadApplicationID pins ADR-0029 / migration
// 00048: linking a workload to an application is a watched change and the
// resulting history row carries the snapshotted application_id column.
// Without the migration + insertWorkloadHistory wiring, the column would
// either be absent (failed migrate) or always NULL (capture didn't write
// it), so this test catches both regressions.
func TestTimeTravelCapture_WorkloadApplicationID(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "tt-wlapp-" + uuid.New().String()[:8]})
	require.NoError(t, err)
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id, Name: "ns-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "tt-app-" + uuid.New().String()[:8]})
	require.NoError(t, err)

	wl, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id, Kind: api.Deployment, Name: "wl-" + uuid.New().String()[:8],
	})
	require.NoError(t, err)

	// Link the workload — this is a watched change, so a new history row
	// is written and the prior (create) row is closed.
	_, err = pg.UpdateWorkload(ctx, *wl.Id, api.WorkloadUpdate{ApplicationId: &app.ID}, false)
	require.NoError(t, err)

	// Inspect the *open* history row (valid_to IS NULL) — its application_id
	// column must equal the link target.
	var got *uuid.UUID
	err = pg.pool.QueryRow(ctx,
		`SELECT application_id FROM workloads_history
		  WHERE entity_id=$1 AND valid_to IS NULL`,
		*wl.Id,
	).Scan(&got)
	require.NoError(t, err)
	require.NotNil(t, got, "open history row should have application_id populated")
	assert.Equal(t, app.ID, *got)

	// The prior create row should carry NULL (the workload was unlinked when
	// it was created) so we know the column is preserved per-snapshot,
	// not retroactively rewritten.
	var prior *uuid.UUID
	err = pg.pool.QueryRow(ctx,
		`SELECT application_id FROM workloads_history
		  WHERE entity_id=$1 AND change_type='create'`,
		*wl.Id,
	).Scan(&prior)
	require.NoError(t, err)
	assert.Nil(t, prior, "create snapshot should have NULL application_id (workload was unlinked at create time)")
}

func TestTimeTravelCapture_Resurrection(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "tt-resurrect-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)
	clusterID := *cluster.Id

	nsName := "resurrect-ns-" + uuid.New().String()[:8]
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: clusterID, Name: nsName,
	})
	require.NoError(t, err)

	// Soft-delete the namespace.
	err = pg.SoftDeleteNamespace(ctx, *ns.Id)
	require.NoError(t, err)

	// Resurrect via UpsertNamespace.
	ns2, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: clusterID, Name: nsName,
	})
	require.NoError(t, err)
	assert.Equal(t, *ns.Id, *ns2.Id, "resurrected namespace should have same id")

	var count int
	err = pg.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM namespaces_history WHERE entity_id=$1 AND change_type='restore'`,
		*ns.Id,
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "resurrection should write a restore history row")
}
