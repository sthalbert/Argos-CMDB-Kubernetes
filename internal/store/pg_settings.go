// Single-row settings table (id=1) reads + merge-patch. Split out of pg.go.
package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
)

// GetSettings returns the current runtime settings from the single-row
// settings table.
func (p *PG) GetSettings(ctx context.Context) (api.Settings, error) {
	const q = `SELECT eol_enabled, mcp_enabled,
		time_travel_enabled, time_travel_retention_days, time_travel_reaper_enabled,
		image_versions_enabled,
		flow_matrix_enabled,
		cluster_stale_after_days,
		policies_enabled,
		updated_at FROM settings WHERE id = 1`
	var s api.Settings
	if err := p.pool.QueryRow(ctx, q).Scan(
		&s.EOLEnabled, &s.MCPEnabled,
		&s.TimeTravelEnabled, &s.TimeTravelRetentionDays, &s.TimeTravelReaperEnabled,
		&s.ImageVersionsEnabled,
		&s.FlowMatrixEnabled,
		&s.ClusterStaleAfterDays,
		&s.PoliciesEnabled,
		&s.UpdatedAt,
	); err != nil {
		return api.Settings{}, fmt.Errorf("get settings: %w", err)
	}
	return s, nil
}

// UpdateSettings applies the merge-patch on the settings row.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdateSettings(ctx context.Context, in api.SettingsPatch) (api.Settings, error) {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 6)
	idx := 1

	if in.EOLEnabled != nil {
		sets = append(sets, fmt.Sprintf("eol_enabled=$%d", idx))
		args = append(args, *in.EOLEnabled)
		idx++
	}
	if in.MCPEnabled != nil {
		sets = append(sets, fmt.Sprintf("mcp_enabled=$%d", idx))
		args = append(args, *in.MCPEnabled)
		idx++
	}
	if in.TimeTravelEnabled != nil {
		sets = append(sets, fmt.Sprintf("time_travel_enabled=$%d", idx))
		args = append(args, *in.TimeTravelEnabled)
		idx++
	}
	if in.TimeTravelRetentionDays != nil {
		sets = append(sets, fmt.Sprintf("time_travel_retention_days=$%d", idx))
		args = append(args, *in.TimeTravelRetentionDays)
		idx++
	}
	if in.TimeTravelReaperEnabled != nil {
		sets = append(sets, fmt.Sprintf("time_travel_reaper_enabled=$%d", idx))
		args = append(args, *in.TimeTravelReaperEnabled)
		idx++
	}
	if in.ImageVersionsEnabled != nil {
		sets = append(sets, fmt.Sprintf("image_versions_enabled=$%d", idx))
		args = append(args, *in.ImageVersionsEnabled)
		idx++
	}
	if in.FlowMatrixEnabled != nil {
		sets = append(sets, fmt.Sprintf("flow_matrix_enabled=$%d", idx))
		args = append(args, *in.FlowMatrixEnabled)
		idx++
	}
	if in.ClusterStaleAfterDays != nil {
		sets = append(sets, fmt.Sprintf("cluster_stale_after_days=$%d", idx))
		args = append(args, *in.ClusterStaleAfterDays)
		idx++
	}
	if in.PoliciesEnabled != nil {
		sets = append(sets, fmt.Sprintf("policies_enabled=$%d", idx))
		args = append(args, *in.PoliciesEnabled)
		idx++
	}
	if len(sets) == 0 {
		return p.GetSettings(ctx)
	}

	sets = append(sets, fmt.Sprintf("updated_at=$%d", idx))
	args = append(args, time.Now().UTC())

	q := fmt.Sprintf("UPDATE settings SET %s WHERE id=1", strings.Join(sets, ", "))
	if _, err := p.pool.Exec(ctx, q, args...); err != nil {
		return api.Settings{}, fmt.Errorf("update settings: %w", err)
	}
	return p.GetSettings(ctx)
}
