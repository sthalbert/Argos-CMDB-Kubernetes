package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestSettingsClusterStaleAfterDays(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	s, err := pg.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if s.ClusterStaleAfterDays != 7 {
		t.Fatalf("default cluster_stale_after_days = %d, want 7", s.ClusterStaleAfterDays)
	}

	thirty := 30
	s2, err := pg.UpdateSettings(ctx, api.SettingsPatch{ClusterStaleAfterDays: &thirty})
	if err != nil {
		t.Fatalf("patch to 30: %v", err)
	}
	if s2.ClusterStaleAfterDays != 30 {
		t.Fatalf("after patch = %d, want 30", s2.ClusterStaleAfterDays)
	}

	zero := 0
	s3, err := pg.UpdateSettings(ctx, api.SettingsPatch{ClusterStaleAfterDays: &zero})
	if err != nil {
		t.Fatalf("patch to 0: %v", err)
	}
	if s3.ClusterStaleAfterDays != 0 {
		t.Fatalf("after patch = %d, want 0 (disabled)", s3.ClusterStaleAfterDays)
	}
}
