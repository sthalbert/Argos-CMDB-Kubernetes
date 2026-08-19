package store

// PR #250 review fixes: results_raw defaulting, case-insensitive
// scope_kind filtering, and ascending default sort for the two
// name-keyed Kyverno list endpoints. Gated on PGX_TEST_DATABASE like
// the rest of the store suite.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

func TestKyverno_UpsertPolicyReport_NilResultsRawDefaultsToEmptyArray(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	pr := makePR(cid, nsID, "nil-results")
	pr.ResultsRaw = nil

	id, err := pg.UpsertPolicyReport(ctx, pr)
	if err != nil {
		t.Fatalf("upsert with nil results_raw: %v", err)
	}
	got, err := pg.GetPolicyReport(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.ResultsRaw) != "[]" {
		t.Errorf("results_raw: got %q, want []", got.ResultsRaw)
	}
}

func TestKyverno_UpsertPolicyReport_JSONNullResultsRawDefaultsToEmptyArray(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	pr := makePR(cid, nsID, "json-null-results")
	pr.ResultsRaw = json.RawMessage("null")

	id, err := pg.UpsertPolicyReport(ctx, pr)
	if err != nil {
		t.Fatalf("upsert with JSON-null results_raw: %v", err)
	}
	got, err := pg.GetPolicyReport(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.ResultsRaw) != "[]" {
		t.Errorf("results_raw: got %q, want []", got.ResultsRaw)
	}
}

func TestKyverno_ListPolicyReports_ScopeKindFilterCaseInsensitive(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	pr := makePR(cid, nsID, "rs-report")
	sk := "ReplicaSet"
	pr.ScopeKind = &sk
	if _, err := pg.UpsertPolicyReport(ctx, pr); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, q := range []string{"ReplicaSet", "replicaset", "REPLICASET"} {
		items, _, err := pg.ListPolicyReports(ctx,
			api.PolicyReportListFilter{ClusterID: &cid, ScopeKind: &q},
			api.ListPage{Limit: 10},
		)
		if err != nil {
			t.Fatalf("list scope_kind=%s: %v", q, err)
		}
		if len(items) != 1 {
			t.Errorf("scope_kind=%s: got %d items, want 1", q, len(items))
		}
	}
}

func TestKyverno_ListClusterPolicies_ResourceTypeFilterCaseInsensitive(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	if _, err := pg.UpsertClusterPolicy(ctx, makeCP(cid, nsID, "ns-policy")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, q := range []string{"Policy", "policy", "POLICY"} {
		items, _, err := pg.ListClusterPolicies(ctx,
			api.ClusterPolicyListFilter{ClusterID: &cid, ResourceType: &q},
			api.ListPage{Limit: 10},
		)
		if err != nil {
			t.Fatalf("list resource_type=%s: %v", q, err)
		}
		if len(items) != 1 {
			t.Errorf("resource_type=%s: got %d items, want 1", q, len(items))
		}
	}
}

func TestKyverno_ListClusterPolicies_DefaultSortNameAscending(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	for _, name := range []string{"zeta-policy", "alpha-policy"} {
		if _, err := pg.UpsertClusterPolicy(ctx, makeCP(cid, nsID, name)); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	items, _, err := pg.ListClusterPolicies(ctx,
		api.ClusterPolicyListFilter{ClusterID: &cid},
		api.ListPage{Limit: 10},
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Name != "alpha-policy" || items[1].Name != "zeta-policy" {
		t.Errorf("default order: got [%s, %s], want [alpha-policy, zeta-policy] (A→Z)",
			items[0].Name, items[1].Name)
	}
}

func TestKyverno_ListPolicyReports_DefaultSortNameAscending(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	cid, nsID := seedClusterForKyverno(t, pg)

	for _, name := range []string{"zeta-report", "alpha-report"} {
		if _, err := pg.UpsertPolicyReport(ctx, makePR(cid, nsID, name)); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}

	items, _, err := pg.ListPolicyReports(ctx,
		api.PolicyReportListFilter{ClusterID: &cid},
		api.ListPage{Limit: 10},
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].Name != "alpha-report" || items[1].Name != "zeta-report" {
		t.Errorf("default order: got [%s, %s], want [alpha-report, zeta-report] (A→Z)",
			items[0].Name, items[1].Name)
	}
}
