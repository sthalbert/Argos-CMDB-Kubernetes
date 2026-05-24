package eolagg

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// fakeAppStore is an in-memory ApplicationStore for the aggregator tests.
// Each slice is keyed implicitly to the single application under test;
// errs lets a test inject a per-source failure.
type fakeAppStore struct {
	workloads []WorkloadMember
	vms       []VMMember
	entries   []VMAppEntryMember
	wlErr     error
	vmErr     error
	entryErr  error
}

func (f *fakeAppStore) ApplicationWorkloadMembers(_ context.Context, _ uuid.UUID) ([]WorkloadMember, error) {
	return f.workloads, f.wlErr
}

func (f *fakeAppStore) ApplicationVMMembers(_ context.Context, _ uuid.UUID) ([]VMMember, error) {
	return f.vms, f.vmErr
}

func (f *fakeAppStore) ApplicationVMAppEntryMembers(_ context.Context, _ uuid.UUID) ([]VMAppEntryMember, error) {
	return f.entries, f.entryErr
}

// Shared product / status literals (goconst).
const (
	productVault   = "vault"
	statusOutdated = "outdated"
)

// eolAnn is a convenience to build a marshalled longue-vue.io/eol.* value.
func eolAnn(product, cycle, status, latestAvailable, checkedAt string) string {
	return `{"product":"` + product + `","cycle":"` + cycle + `","eol_status":"` + status +
		`","latest_available":"` + latestAvailable + `","checked_at":"` + checkedAt + `"}`
}

func TestAggregateForApplication_Empty(t *testing.T) {
	rows, err := AggregateForApplication(context.Background(), &fakeAppStore{}, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero rows, got %d: %#v", len(rows), rows)
	}
}

func TestAggregateForApplication_SingleWorkload(t *testing.T) {
	store := &fakeAppStore{
		workloads: []WorkloadMember{{
			ID:   "w1",
			Name: "kube-prod/vault",
			ContainersVersions: map[string]ContainerVersion{
				productVault: {LatestTag: "1.18.2", IsBehind: true, LastCheckedAt: "2026-05-15T00:00:00Z"},
			},
		}},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %#v", len(rows), rows)
	}
	r := rows[0]
	if r.Product != productVault {
		t.Errorf("product = %q want vault", r.Product)
	}
	if r.LatestAvailable != "1.18.2" {
		t.Errorf("latest_available = %q want 1.18.2", r.LatestAvailable)
	}
	if r.EOLStatus != statusOutdated {
		t.Errorf("eol_status = %q want outdated (is_behind)", r.EOLStatus)
	}
	if len(r.Sources) != 1 || r.Sources[0].Kind != "workload" || r.Sources[0].ID != "w1" {
		t.Errorf("sources = %#v want one workload source", r.Sources)
	}
}

func TestAggregateForApplication_WorkloadNotEnrichedSkipped(t *testing.T) {
	// A container with no latest_tag has no EOL signal yet → no row.
	store := &fakeAppStore{
		workloads: []WorkloadMember{{
			ID:   "w1",
			Name: "kube-prod/web",
			ContainersVersions: map[string]ContainerVersion{
				"web": {LatestTag: ""},
			},
		}},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero rows for un-enriched container, got %d", len(rows))
	}
}

func TestAggregateForApplication_MergesWorkloadAndVM(t *testing.T) {
	// Same product on a workload (image-versions) AND a linked VM
	// (endoflife.date annotation) → ONE merged row with TWO sources.
	store := &fakeAppStore{
		workloads: []WorkloadMember{{
			ID:   "w1",
			Name: "kube-prod/vault",
			ContainersVersions: map[string]ContainerVersion{
				productVault: {LatestTag: "1.18.2"},
			},
		}},
		vms: []VMMember{{
			ID:   "v1",
			Name: "bastion-eu",
			Annotations: map[string]string{
				eolPrefix + productVault: eolAnn(productVault, "1.13", "eol", "1.18.2", "2026-05-15T00:00:00Z"),
			},
		}},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 merged row, got %d: %#v", len(rows), rows)
	}
	r := rows[0]
	if r.Product != productVault {
		t.Errorf("product = %q want vault", r.Product)
	}
	// The VM's real annotation (eol) should win over the workload's
	// sentinel "unknown".
	if r.EOLStatus != "eol" {
		t.Errorf("eol_status = %q want eol", r.EOLStatus)
	}
	if r.Cycle != "1.13" {
		t.Errorf("cycle = %q want 1.13", r.Cycle)
	}
	if len(r.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d: %#v", len(r.Sources), r.Sources)
	}
	// Sorted by kind: virtual_machine < workload.
	if r.Sources[0].Kind != "virtual_machine" || r.Sources[1].Kind != "workload" {
		t.Errorf("sources order = %#v want [virtual_machine, workload]", r.Sources)
	}
}

func TestAggregateForApplication_VMAppEntryUnlinkedParent(t *testing.T) {
	// The parent VM is NOT row-level linked (absent from vms), but one of
	// its applications[] entries carries application_id == this app. The
	// matching product's annotation contributes a row.
	store := &fakeAppStore{
		entries: []VMAppEntryMember{{
			VMID:    "v9",
			VMName:  "bastion-west",
			Product: productVault,
			Annotations: map[string]string{
				eolPrefix + productVault: eolAnn(productVault, "1.13", "eol", "1.18.2", "2026-05-15T00:00:00Z"),
				eolPrefix + "bind":       eolAnn("bind", "9.18", "supported", "9.18.24", "2026-05-15T00:00:00Z"),
			},
		}},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 row (only the matching product), got %d: %#v", len(rows), rows)
	}
	r := rows[0]
	if r.Product != productVault {
		t.Errorf("product = %q want vault (bind must not leak)", r.Product)
	}
	if len(r.Sources) != 1 || r.Sources[0].Kind != "vm_application" {
		t.Fatalf("sources = %#v want one vm_application source", r.Sources)
	}
	if r.Sources[0].ID != "v9#vault" {
		t.Errorf("source id = %q want v9#vault", r.Sources[0].ID)
	}
}

func TestAggregateForApplication_MalformedAndMissingAnnotationsSkipped(t *testing.T) {
	store := &fakeAppStore{
		vms: []VMMember{{
			ID:   "v1",
			Name: "bastion-eu",
			Annotations: map[string]string{
				eolPrefix + productVault: "{not json", // malformed → skipped
				"my-team.io/owner":       "alice",     // non-EOL → skipped
			},
		}},
		entries: []VMAppEntryMember{
			{VMID: "v2", VMName: "no-ann", Product: "redis", Annotations: map[string]string{}},                           // missing → skipped
			{VMID: "v3", VMName: "bad-ann", Product: "nginx", Annotations: map[string]string{eolPrefix + "nginx": "}{"}}, // malformed → skipped
			{VMID: "v4", VMName: "empty-product", Product: "", Annotations: map[string]string{}},                         // empty product → skipped
		},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error (must not panic / error on malformed): %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected zero rows, got %d: %#v", len(rows), rows)
	}
}

func TestAggregateForApplication_MultipleProductsSorted(t *testing.T) {
	store := &fakeAppStore{
		vms: []VMMember{{
			ID:   "v1",
			Name: "bastion",
			Annotations: map[string]string{
				eolPrefix + productVault: eolAnn(productVault, "1.13", "eol", "1.18.2", "t"),
				eolPrefix + "bind":       eolAnn("bind", "9.18", "supported", "9.18.24", "t"),
			},
		}},
	}
	rows, err := AggregateForApplication(context.Background(), store, uuid.New())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Product != "bind" || rows[1].Product != productVault {
		t.Errorf("rows not sorted by product: %q, %q", rows[0].Product, rows[1].Product)
	}
}
