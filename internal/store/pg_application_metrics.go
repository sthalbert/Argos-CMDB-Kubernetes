package store

import (
	"context"
	"fmt"

	"github.com/sthalbert/longue-vue/internal/metrics"
)

// ApplicationMetricCounts returns the data backing the ADR-0029 §9 gauges:
//   - buckets: applications grouped by (has_block, has_dict), one row per
//     combination that actually occurs (combinations with zero rows are
//     omitted; the metrics setter resets all series each refresh so missing
//     combinations correctly read as 0),
//   - blocks: the total number of application_blocks rows.
//
// "has_dict" is true when ANY of the four DICT axes (sec_disponibilite,
// sec_integrite, sec_confidentialite, sec_tracabilite) is set. "has_block" is
// true when application_block_id is non-NULL.
func (p *PG) ApplicationMetricCounts(ctx context.Context) (buckets []metrics.ApplicationCount, blocks int, err error) {
	const appQ = `
SELECT
  (application_block_id IS NOT NULL) AS has_block,
  (sec_disponibilite IS NOT NULL OR sec_integrite IS NOT NULL
   OR sec_confidentialite IS NOT NULL OR sec_tracabilite IS NOT NULL) AS has_dict,
  COUNT(*) AS n
FROM applications
GROUP BY 1, 2`
	rows, err := p.pool.Query(ctx, appQ)
	if err != nil {
		return nil, 0, fmt.Errorf("application metric counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b metrics.ApplicationCount
		if err := rows.Scan(&b.HasBlock, &b.HasDict, &b.Count); err != nil {
			return nil, 0, fmt.Errorf("scan application metric counts: %w", err)
		}
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate application metric counts: %w", err)
	}

	if err := p.pool.QueryRow(ctx, `SELECT COUNT(*) FROM application_blocks`).Scan(&blocks); err != nil {
		return nil, 0, fmt.Errorf("application blocks count: %w", err)
	}
	return buckets, blocks, nil
}
