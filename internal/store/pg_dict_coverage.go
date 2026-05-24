package store

import (
	"context"
	"fmt"
)

// DICTCoverageCounts returns the number of workloads in each effective-DICT
// source bucket (ADR-0029 §6), for the longue_vue_dict_coverage gauge.
//
// Workloads carry no DICT columns of their own (the Workload/Namespace DICT
// migration described in ADR-0008 §8.3 / ADR-0029 was never created), so a
// workload's effective DICT is either inherited from its linked application
// or "none". The `workload` bucket therefore exists in the metric's label
// space for forward-compatibility but is always 0 today; if workload-level
// DICT columns land later, extend the query's middle FILTER to count them.
func (p *PG) DICTCoverageCounts(ctx context.Context) (application, workload, none int, err error) {
	const q = `
SELECT
  COUNT(*) FILTER (WHERE a.id IS NOT NULL
    AND (a.sec_disponibilite IS NOT NULL OR a.sec_integrite IS NOT NULL
         OR a.sec_confidentialite IS NOT NULL OR a.sec_tracabilite IS NOT NULL)) AS application,
  0 AS workload,
  COUNT(*) FILTER (WHERE a.id IS NULL
    OR (a.sec_disponibilite IS NULL AND a.sec_integrite IS NULL
        AND a.sec_confidentialite IS NULL AND a.sec_tracabilite IS NULL)) AS none
FROM workloads w LEFT JOIN applications a ON a.id = w.application_id`
	if err := p.pool.QueryRow(ctx, q).Scan(&application, &workload, &none); err != nil {
		return 0, 0, 0, fmt.Errorf("dict coverage counts: %w", err)
	}
	return application, workload, none, nil
}
