package timetravel

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWatchedFields_AllColumnsClassified introspects the live parent tables and
// asserts that every column on each parent table appears in either WatchedFields
// or ExcludedFields for its kind. A column not in either set fails the test,
// ensuring that adding a column to a parent table without classifying it for
// history is a CI failure.
//
// Skipped when PGX_TEST_DATABASE is not set (non-integration environments).
func TestWatchedFields_AllColumnsClassified(t *testing.T) {
	dsn := os.Getenv("PGX_TEST_DATABASE")
	if dsn == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping integration test")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	// Map each kind to the actual parent table name (same for all four here).
	tableForKind := map[string]string{
		KindCluster:   "clusters",
		KindNamespace: "namespaces",
		KindNode:      "nodes",
		KindWorkload:  "workloads",
	}

	for kind, table := range tableForKind {
		t.Run(kind, func(t *testing.T) {
			rows, err := conn.Query(ctx,
				`SELECT column_name FROM information_schema.columns
				  WHERE table_schema = 'public' AND table_name = $1
				  ORDER BY ordinal_position`,
				table,
			)
			require.NoError(t, err)
			defer rows.Close()

			watched := make(map[string]bool, len(WatchedFields[kind]))
			for _, f := range WatchedFields[kind] {
				watched[f] = true
			}
			excluded := make(map[string]bool, len(ExcludedFields[kind]))
			for _, f := range ExcludedFields[kind] {
				excluded[f] = true
			}

			var unclassified []string
			for rows.Next() {
				var col string
				require.NoError(t, rows.Scan(&col))
				if !watched[col] && !excluded[col] {
					unclassified = append(unclassified, col)
				}
			}
			require.NoError(t, rows.Err())

			assert.Empty(t, unclassified,
				"columns on %q not classified in WatchedFields or ExcludedFields for kind %q: %v",
				table, kind, unclassified)
		})
	}
}
