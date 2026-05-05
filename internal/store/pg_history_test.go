package store

// pg_history_test.go — Integration tests for the Phase 3 history read API.
// Tests require PGX_TEST_DATABASE to be set.
//
// Scenario: create a cluster → update a watched field → soft-delete.
// Asserts:
//   - /history returns 3 rows (create, update, soft_delete) in newest-first order.
//   - as_of at a timestamp between create and update returns the create snapshot.
//   - as_of at a timestamp between update and soft_delete returns the update snapshot.
//   - as_of before the cluster existed returns ErrNotFound.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestListEntityHistory_Cluster(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// ── 1. Create ──────────────────────────────────────────────────────────
	name := "hist-api-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)
	id := *cluster.Id

	// Small sleep so valid_from values are distinct (postgres TIMESTAMPTZ
	// resolution is 1 µs; sub-millisecond writes may collide in CI).
	time.Sleep(10 * time.Millisecond)
	afterCreate := time.Now().UTC()

	// ── 2. Update a watched field ──────────────────────────────────────────
	time.Sleep(10 * time.Millisecond)
	ver := "1.29.5"
	_, err = pg.UpdateCluster(ctx, id, api.ClusterUpdate{KubernetesVersion: &ver})
	require.NoError(t, err)

	time.Sleep(10 * time.Millisecond)
	afterUpdate := time.Now().UTC()

	// ── 3. Soft-delete ────────────────────────────────────────────────────
	time.Sleep(10 * time.Millisecond)
	err = pg.SoftDeleteCluster(ctx, id)
	require.NoError(t, err)

	// ── 4. ListEntityHistory should return 3 rows, newest first ───────────
	rows, nextCursor, err := pg.ListEntityHistory(ctx, "clusters", id, 50, "")
	require.NoError(t, err)
	assert.Empty(t, nextCursor, "only 3 rows; no next page")
	require.Len(t, rows, 3, "expected create + update + soft_delete rows")

	assert.Equal(t, "soft_delete", rows[0].ChangeType)
	assert.Equal(t, "update", rows[1].ChangeType)
	assert.Equal(t, "create", rows[2].ChangeType)

	// The update row should have a non-empty diff.
	assert.NotEmpty(t, rows[1].Diff, "update row should carry a non-empty diff")

	// ── 5. as_of before the cluster existed → ErrNotFound ─────────────────
	farPast := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = pg.GetEntityAsOf(ctx, "clusters", id, farPast)
	assert.ErrorIs(t, err, api.ErrNotFound, "as_of before creation should be not found")

	// ── 6. as_of between create and update → returns create snapshot ───────
	snapCreate, err := pg.GetEntityAsOf(ctx, "clusters", id, afterCreate)
	require.NoError(t, err)
	assert.Nil(t, snapCreate["kubernetes_version"], "snapshot at create time should have no kube version")

	// ── 7. as_of between update and soft_delete → returns update snapshot ──
	snapUpdate, err := pg.GetEntityAsOf(ctx, "clusters", id, afterUpdate)
	require.NoError(t, err)
	kv, _ := snapUpdate["kubernetes_version"].(*string)
	require.NotNil(t, kv)
	assert.Equal(t, "1.29.5", *kv)
}

func TestListEntityHistory_Pagination(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	name := "hist-page-" + uuid.New().String()[:8]
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: name})
	require.NoError(t, err)
	id := *cluster.Id

	// Write 3 watched-field updates to get 4 total rows (1 create + 3 updates).
	for _, v := range []string{"1.28.0", "1.29.0", "1.30.0"} {
		time.Sleep(5 * time.Millisecond)
		ver := v
		_, err = pg.UpdateCluster(ctx, id, api.ClusterUpdate{KubernetesVersion: &ver})
		require.NoError(t, err)
	}

	// First page: limit=2 → should return 2 rows and a non-empty next_cursor.
	page1, cursor, err := pg.ListEntityHistory(ctx, "clusters", id, 2, "")
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.NotEmpty(t, cursor)

	// Second page: should return remaining rows.
	page2, cursor2, err := pg.ListEntityHistory(ctx, "clusters", id, 2, cursor)
	require.NoError(t, err)
	assert.Empty(t, cursor2, "second page should exhaust history")
	assert.Equal(t, 2, len(page2))

	// All 4 rows together, newest first.
	all := append(page1, page2...) //nolint:gocritic // appendAssign: deliberate for test assertion
	assert.Equal(t, "update", all[0].ChangeType)
	assert.Equal(t, "create", all[3].ChangeType)
}
