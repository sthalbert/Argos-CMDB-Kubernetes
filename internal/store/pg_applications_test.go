package store

// PostgreSQL integration tests for the applications CRUD methods + the
// members walk (ADR-0029, Task 1.7). Gated on PGX_TEST_DATABASE like the
// rest of internal/store; newTestPG TRUNCATEs applications +
// application_blocks between tests.

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// intPtr is a small helper for the *int DICT fields. Keeping it
// test-local avoids polluting api/* with a generic pointer helper.
func intPtr(i int) *int { return &i }

// appNameVault is the canonical fixture application name reused throughout
// this test file. Hoisted to a const so goconst stays happy as new tests
// across the package also seed "vault".
const appNameVault = "vault"

// TestPGApplicationCRUD walks the happy path: create with block FK +
// DICT axes → get-by-id → get-by-name → update criticality + sec_notes
// → list with name substring → list with DICTMin → delete → get returns
// ErrNotFound.
//
//nolint:gocyclo,gocognit // integration test exercises multiple CRUD branches
func TestPGApplicationCRUD(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	blk, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{Name: "management-apps"})
	if err != nil {
		t.Fatalf("create block: %v", err)
	}

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:               appNameVault,
		DisplayName:        strPtr("HashiCorp Vault"),
		ApplicationBlockID: &blk.ID,
		Owner:              strPtr("security-team"),
		Criticality:        strPtr("critical"),
		SecDisponibilite:   intPtr(3),
		SecIntegrite:       intPtr(2),
		SecConfidentialite: intPtr(4),
		SecTracabilite:     intPtr(1),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.Name != appNameVault {
		t.Fatalf("expected name vault, got %q", app.Name)
	}
	if app.ApplicationBlockID == nil || *app.ApplicationBlockID != blk.ID {
		t.Fatalf("expected block FK, got %v", app.ApplicationBlockID)
	}
	if app.ApplicationBlockName == nil || *app.ApplicationBlockName != "management-apps" {
		t.Fatalf("expected denorm block name, got %v", app.ApplicationBlockName)
	}
	if app.SecConfidentialite == nil || *app.SecConfidentialite != 4 {
		t.Fatalf("expected SecConfidentialite=4, got %v", app.SecConfidentialite)
	}
	if app.Annotations == nil {
		t.Fatalf("expected non-nil annotations map (json contract)")
	}
	if app.CreatedAt.IsZero() || app.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", app)
	}
	// MemberCounts on a brand new application should all be zero.
	if app.MemberCounts.Workloads != 0 ||
		app.MemberCounts.VirtualMachines != 0 ||
		app.MemberCounts.VMApplications != 0 {
		t.Fatalf("fresh app should have zero member counts, got %+v", app.MemberCounts)
	}

	got, err := pg.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ID != app.ID {
		t.Fatalf("get-by-id returned wrong row: %v", got.ID)
	}

	byName, err := pg.GetApplicationByName(ctx, appNameVault)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if byName.ID != app.ID {
		t.Fatalf("get-by-name returned wrong row: got %s, want %s", byName.ID, app.ID)
	}

	updated, err := pg.UpdateApplication(ctx, app.ID, api.ApplicationPatch{
		Criticality: strPtr("high"),
		SecNotes:    strPtr("classification reviewed 2026-05-24"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Criticality == nil || *updated.Criticality != "high" {
		t.Fatalf("expected criticality=high, got %v", updated.Criticality)
	}
	if updated.SecNotes == nil || *updated.SecNotes != "classification reviewed 2026-05-24" {
		t.Fatalf("expected sec_notes set, got %v", updated.SecNotes)
	}
	if !updated.UpdatedAt.After(app.UpdatedAt) {
		t.Fatalf("updated_at should advance: %v vs %v", updated.UpdatedAt, app.UpdatedAt)
	}

	items, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{Name: strPtr("vau")}, 50, "")
	if err != nil {
		t.Fatalf("list by name: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 row from name filter, got %d", len(items))
	}

	// dict_min=4 → matches (SecConfidentialite=4 wins GREATEST).
	items, _, err = pg.ListApplications(ctx, api.ApplicationListFilter{DICTMin: intPtr(4)}, 50, "")
	if err != nil {
		t.Fatalf("list dict_min=4: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 row from dict_min=4, got %d", len(items))
	}

	// dict_min=5 → no match (axis cap is 4).
	items, _, err = pg.ListApplications(ctx, api.ApplicationListFilter{DICTMin: intPtr(5)}, 50, "")
	if err != nil {
		t.Fatalf("list dict_min=5: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 rows from dict_min=5, got %d", len(items))
	}

	if err := pg.DeleteApplication(ctx, app.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pg.GetApplication(ctx, app.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("get after delete should be ErrNotFound, got %v", err)
	}
}

// TestPGApplicationCreateByBlockName proves the block-name path resolves
// to the same row as the block-id path, and the denormalised
// application_block_name is populated on the returned application.
func TestPGApplicationCreateByBlockName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	if _, err := pg.CreateApplicationBlock(ctx, api.ApplicationBlockCreate{Name: "office-apps"}); err != nil {
		t.Fatalf("create block: %v", err)
	}
	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:                 "outlook",
		ApplicationBlockName: strPtr("office-apps"),
	})
	if err != nil {
		t.Fatalf("create with block name: %v", err)
	}
	if app.ApplicationBlockName == nil || *app.ApplicationBlockName != "office-apps" {
		t.Fatalf("expected block name resolved, got %v", app.ApplicationBlockName)
	}
	if app.ApplicationBlockID == nil {
		t.Fatalf("expected block id set on resolved create")
	}
}

// TestPGApplicationCreateUnknownBlockName proves the create-by-name path
// surfaces an error rather than silently inserting an unlinked row.
func TestPGApplicationCreateUnknownBlockName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	_, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:                 "outlook",
		ApplicationBlockName: strPtr("does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected error for unknown block name")
	}
}

// TestPGApplicationDuplicateName proves the UNIQUE (name) index surfaces
// as api.ErrConflict.
func TestPGApplicationDuplicateName(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: appNameVault}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: appNameVault})
	if !errors.Is(err, api.ErrConflict) {
		t.Errorf("duplicate name create should be ErrConflict, got %v", err)
	}
	// Normalisation: "Vault" should also collide.
	_, err = pg.CreateApplication(ctx, api.ApplicationCreate{Name: "Vault"})
	if !errors.Is(err, api.ErrConflict) {
		t.Errorf("normalised duplicate create should be ErrConflict, got %v", err)
	}
}

// TestPGApplicationUpdatePreservesUntouchedFields proves the merge-patch
// only touches fields whose patch pointer is non-nil.
//
//nolint:gocyclo // walking four "untouched" assertions across one update
func TestPGApplicationUpdatePreservesUntouchedFields(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:             "billing-api",
		Owner:            strPtr("team-billing"),
		Notes:            strPtr("Handles invoices"),
		SecDisponibilite: intPtr(3),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	patched, err := pg.UpdateApplication(ctx, app.ID, api.ApplicationPatch{
		Criticality: strPtr("medium"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if patched.Criticality == nil || *patched.Criticality != "medium" {
		t.Fatalf("expected criticality patched, got %v", patched.Criticality)
	}
	if patched.Owner == nil || *patched.Owner != "team-billing" {
		t.Errorf("owner should be preserved across patch: %v", patched.Owner)
	}
	if patched.Notes == nil || *patched.Notes != "Handles invoices" {
		t.Errorf("notes should be preserved across patch: %v", patched.Notes)
	}
	if patched.SecDisponibilite == nil || *patched.SecDisponibilite != 3 {
		t.Errorf("sec_disponibilite should be preserved across patch: %v", patched.SecDisponibilite)
	}
}

// TestPGApplicationDeleteSweepsVMAppReferences proves the DeleteApplication
// transaction strips per-entry application_id from virtual_machines.applications
// JSONB so dangling references don't linger.
//
//nolint:gocyclo // multi-step VM + JSONB assertion
func TestPGApplicationDeleteSweepsVMAppReferences(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: appNameVault})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	acct := vmTestAccount(t, pg, "vm-app-sweep")
	vm, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-sweep-0001",
		Name:           "vm-sweep",
		PowerState:     "running",
	})
	if err != nil {
		t.Fatalf("upsert vm: %v", err)
	}

	// Inject an applications JSONB row directly to avoid pulling in the
	// curator handler in a store-layer test. The handler path lands in
	// later phases; this is the canonical "patch the table" shortcut.
	apps := []map[string]any{
		{
			"product":        appNameVault,
			"version":        "1.15.4",
			"added_at":       "2026-05-24T10:00:00Z",
			"added_by":       "test",
			"application_id": app.ID.String(),
		},
	}
	appsJSON, err := json.Marshal(apps)
	if err != nil {
		t.Fatalf("marshal apps: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`UPDATE virtual_machines SET applications = $1::jsonb WHERE id = $2`,
		appsJSON, vm.ID); err != nil {
		t.Fatalf("seed vm.applications: %v", err)
	}

	if err := pg.DeleteApplication(ctx, app.ID); err != nil {
		t.Fatalf("delete app: %v", err)
	}

	// VM row still exists; the entry inside applications survives without
	// its application_id key.
	var raw []byte
	if err := pg.pool.QueryRow(ctx,
		`SELECT applications FROM virtual_machines WHERE id = $1`, vm.ID,
	).Scan(&raw); err != nil {
		t.Fatalf("re-read vm: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode vm.applications: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 vm-app entry after sweep, got %d", len(out))
	}
	if out[0]["product"] != appNameVault {
		t.Errorf("product should survive sweep, got %v", out[0]["product"])
	}
	if _, present := out[0]["application_id"]; present {
		t.Errorf("application_id key should have been stripped, got %v", out[0])
	}
}

// TestPGApplicationDeleteSetsNullOnWorkloadFK proves the PG FK
// `ON DELETE SET NULL` chain leaves the workload row intact with
// application_id IS NULL after the parent application is deleted.
func TestPGApplicationDeleteSetsNullOnWorkloadFK(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "fk-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "fk-ns",
	})
	if err != nil {
		t.Fatalf("upsert ns: %v", err)
	}
	wl, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
		NamespaceId: *ns.Id,
		Kind:        api.Deployment,
		Name:        "fk-wl",
	})
	if err != nil {
		t.Fatalf("upsert wl: %v", err)
	}

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "fk-app"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`UPDATE workloads SET application_id = $1 WHERE id = $2`,
		app.ID, *wl.Id); err != nil {
		t.Fatalf("link workload: %v", err)
	}

	if err := pg.DeleteApplication(ctx, app.ID); err != nil {
		t.Fatalf("delete app: %v", err)
	}

	var appID *uuid.UUID
	if err := pg.pool.QueryRow(ctx,
		`SELECT application_id FROM workloads WHERE id = $1`, *wl.Id,
	).Scan(&appID); err != nil {
		t.Fatalf("re-read workload: %v", err)
	}
	if appID != nil {
		t.Fatalf("expected workload.application_id IS NULL after delete, got %v", *appID)
	}
}

// TestPGApplicationListFilterHasDICT verifies the HasDICT branch on
// both true (any axis set) and false (all-nil) shapes.
func TestPGApplicationListFilterHasDICT(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name:             "with-dict",
		SecDisponibilite: intPtr(2),
	}); err != nil {
		t.Fatalf("create with-dict: %v", err)
	}
	if _, err := pg.CreateApplication(ctx, api.ApplicationCreate{
		Name: "without-dict",
	}); err != nil {
		t.Fatalf("create without-dict: %v", err)
	}

	yes := true
	items, _, err := pg.ListApplications(ctx, api.ApplicationListFilter{HasDICT: &yes}, 50, "")
	if err != nil {
		t.Fatalf("list HasDICT=true: %v", err)
	}
	if len(items) != 1 || items[0].Name != "with-dict" {
		t.Fatalf("HasDICT=true returned %+v", names(items))
	}

	no := false
	items, _, err = pg.ListApplications(ctx, api.ApplicationListFilter{HasDICT: &no}, 50, "")
	if err != nil {
		t.Fatalf("list HasDICT=false: %v", err)
	}
	if len(items) != 1 || items[0].Name != "without-dict" {
		t.Fatalf("HasDICT=false returned %+v", names(items))
	}
}

// TestPGApplicationMemberCountsEmpty asserts the three subqueries return
// zero on a brand-new application with no linked rows.
func TestPGApplicationMemberCountsEmpty(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "lonely-app"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if app.MemberCounts.Workloads != 0 ||
		app.MemberCounts.VirtualMachines != 0 ||
		app.MemberCounts.VMApplications != 0 {
		t.Fatalf("expected zero counts, got %+v", app.MemberCounts)
	}
}

// seedApplicationWithMembers builds a small fixture: an application
// linked to 2 workloads (in one namespace), 1 VM (via FK), and 1 VM-app
// entry on a different VM (via JSONB). Returns the application + IDs so
// follow-up assertions stay readable.
//
//nolint:gocyclo // multi-step fixture setup
func seedApplicationWithMembers(t *testing.T, pg *PG) api.Application {
	t.Helper()
	ctx := context.Background()

	app, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "memberful-app"})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	// 2 workloads under the same namespace.
	cluster, _, err := pg.EnsureCluster(ctx, api.ClusterCreate{Name: "members-cluster"})
	if err != nil {
		t.Fatalf("ensure cluster: %v", err)
	}
	ns, _, err := pg.UpsertNamespace(ctx, api.NamespaceCreate{
		ClusterId: *cluster.Id,
		Name:      "members-ns",
	})
	if err != nil {
		t.Fatalf("upsert ns: %v", err)
	}
	for _, name := range []string{"wl-a", "wl-b"} {
		wl, _, err := pg.UpsertWorkload(ctx, api.WorkloadCreate{
			NamespaceId: *ns.Id,
			Kind:        api.Deployment,
			Name:        name,
		})
		if err != nil {
			t.Fatalf("upsert wl %s: %v", name, err)
		}
		if _, err := pg.pool.Exec(ctx,
			`UPDATE workloads SET application_id = $1 WHERE id = $2`,
			app.ID, *wl.Id); err != nil {
			t.Fatalf("link wl %s: %v", name, err)
		}
	}

	// 1 VM linked via FK.
	acct := vmTestAccount(t, pg, "members-acct")
	vmFK, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-fk-vm",
		Name:           "vm-fk",
		PowerState:     "running",
	})
	if err != nil {
		t.Fatalf("upsert vm-fk: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`UPDATE virtual_machines SET application_id = $1 WHERE id = $2`,
		app.ID, vmFK.ID); err != nil {
		t.Fatalf("link vm-fk: %v", err)
	}

	// 1 VM with a vm-app entry pointing at the application (JSONB linkage).
	vmJSON, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-jsonb-vm",
		Name:           "vm-jsonb",
		PowerState:     "running",
	})
	if err != nil {
		t.Fatalf("upsert vm-jsonb: %v", err)
	}
	apps := []map[string]any{
		{
			"product":        "memberful",
			"version":        "1.0.0",
			"added_at":       "2026-05-24T10:00:00Z",
			"added_by":       "test",
			"application_id": app.ID.String(),
		},
	}
	appsJSON, err := json.Marshal(apps)
	if err != nil {
		t.Fatalf("marshal apps: %v", err)
	}
	if _, err := pg.pool.Exec(ctx,
		`UPDATE virtual_machines SET applications = $1::jsonb WHERE id = $2`,
		appsJSON, vmJSON.ID); err != nil {
		t.Fatalf("seed vm-jsonb.applications: %v", err)
	}

	// Re-read so MemberCounts reflect the writes.
	got, err := pg.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("re-read app: %v", err)
	}
	return got
}

// TestPGApplicationMemberCountsAfterLinking checks the three subqueries
// against a known fixture of 2 workloads + 1 VM + 1 VM-app entry.
func TestPGApplicationMemberCountsAfterLinking(t *testing.T) {
	pg := newTestPG(t)
	app := seedApplicationWithMembers(t, pg)
	if app.MemberCounts.Workloads != 2 {
		t.Errorf("expected 2 workloads, got %d", app.MemberCounts.Workloads)
	}
	if app.MemberCounts.VirtualMachines != 1 {
		t.Errorf("expected 1 VM, got %d", app.MemberCounts.VirtualMachines)
	}
	if app.MemberCounts.VMApplications != 1 {
		t.Errorf("expected 1 vm-application entry, got %d", app.MemberCounts.VMApplications)
	}
}

// TestPGApplicationListMembers walks the three sources in order and
// asserts every linked row surfaces with the right Kind.
func TestPGApplicationListMembers(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	app := seedApplicationWithMembers(t, pg)

	members, next, err := pg.ListApplicationMembers(ctx, app.ID, 100, "")
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if next != "" {
		t.Errorf("expected empty next cursor on small fixture, got %q", next)
	}
	if len(members) != 4 {
		t.Fatalf("expected 4 members (2 workloads + 1 vm + 1 vm-app), got %d: %+v",
			len(members), members)
	}

	byKind := map[api.ApplicationMemberKind]int{}
	for _, m := range members {
		byKind[m.Kind]++
	}
	if byKind[api.ApplicationMemberKindWorkload] != 2 {
		t.Errorf("expected 2 workload members, got %d", byKind[api.ApplicationMemberKindWorkload])
	}
	if byKind[api.ApplicationMemberKindVirtualMachine] != 1 {
		t.Errorf("expected 1 virtual_machine member, got %d", byKind[api.ApplicationMemberKindVirtualMachine])
	}
	if byKind[api.ApplicationMemberKindVMApplication] != 1 {
		t.Errorf("expected 1 vm_application member, got %d", byKind[api.ApplicationMemberKindVMApplication])
	}

	// Order is workload → virtual_machine → vm_application.
	wantOrder := []api.ApplicationMemberKind{
		api.ApplicationMemberKindWorkload,
		api.ApplicationMemberKindWorkload,
		api.ApplicationMemberKindVirtualMachine,
		api.ApplicationMemberKindVMApplication,
	}
	for i, m := range members {
		if m.Kind != wantOrder[i] {
			t.Errorf("members[%d].Kind = %q, want %q", i, m.Kind, wantOrder[i])
		}
	}
}

// names is a tiny helper to keep ListApplications assertions readable.
// Iterates by index because api.Application is a wide value (the lint
// catches a 216-byte copy on each range iteration).
func names(items []api.Application) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].Name
	}
	sort.Strings(out)
	return out
}
