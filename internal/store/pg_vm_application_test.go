package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// vmAppBilling is the seed application name reused across the VM and
// workload application-linkage tests. Hoisted to a const so goconst stays
// happy; the value matches the workload-side fixture so the two tests
// share intuition.
const vmAppBilling = "billing"

// appLinkageNoSuchApp is the bogus application name reused across the
// linkage and substring tests to assert non-matching look-ups return zero
// rows. Hoisted to a const so goconst stays happy.
const appLinkageNoSuchApp = "no-such-app"

// TestPGVMApplicationLinkage exercises the ADR-0029 wiring on the
// virtual_machines side end-to-end at the store layer:
//
//   - PATCH VM with application_id → row carries the link.
//   - ListVirtualMachines filter by application_id / application_name /
//     unlinked.
//   - Per-entry application_id on the applications JSONB array is
//     persisted on PATCH and rejected when the id is unknown.
//   - Collector invariant: UpsertVirtualMachine (reconcile tick) must
//     NOT touch the curator's row-level link nor the per-entry id.
//
// The handler-level "id wins on conflict" precedence is exercised by
// TestResolveApplicationID in internal/api; the store-side filter helper
// honours the same rule when both fields are non-nil and is verified
// here too.
//
//nolint:gocyclo,gocognit // integration test exercises several distinct paths
func TestPGVMApplicationLinkage(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// --- seed cloud account so VM upserts satisfy the FK -----------------
	acct := vmTestAccount(t, pg, "vm-app-linkage")

	// --- seed two applications so we can exercise both id and name paths -
	billing, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: vmAppBilling})
	if err != nil {
		t.Fatalf("create application billing: %v", err)
	}
	checkout, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "checkout"})
	if err != nil {
		t.Fatalf("create application checkout: %v", err)
	}

	// --- seed two VMs (one to link, one to keep unlinked) ----------------
	vmLinked, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-vm-linked-0000",
		Name:           "vm-linked",
		PowerState:     "running",
		Ready:          true,
		Region:         strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("upsert vmLinked: %v", err)
	}
	vmUnlinked, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-vm-unlinked-0000",
		Name:           "vm-unlinked",
		PowerState:     "running",
		Ready:          true,
		Region:         strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("upsert vmUnlinked: %v", err)
	}

	// --- 1. PATCH with application_id → row picks up the link ------------
	updated, err := pg.UpdateVirtualMachine(ctx, vmLinked.ID, api.VirtualMachinePatch{
		ApplicationID: &billing.ID,
	})
	if err != nil {
		t.Fatalf("update vm: %v", err)
	}
	if updated.ApplicationID == nil || *updated.ApplicationID != billing.ID {
		t.Fatalf("expected application_id=%v, got %v", billing.ID, updated.ApplicationID)
	}

	// Re-fetch to confirm scanVirtualMachine reads the link back too.
	got, err := pg.GetVirtualMachine(ctx, vmLinked.ID)
	if err != nil {
		t.Fatalf("get vm: %v", err)
	}
	if got.ApplicationID == nil || *got.ApplicationID != billing.ID {
		t.Fatalf("GET: expected application_id=%v, got %v", billing.ID, got.ApplicationID)
	}

	// --- 2. ListVirtualMachines filter by application_id → only linked ---
	items, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID: &acct.ID,
		ApplicationID:  &billing.ID,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_id: %v", err)
	}
	if len(items) != 1 || items[0].ID != vmLinked.ID {
		t.Fatalf("list by application_id: got %d items, want 1 (vmLinked); items=%+v", len(items), items)
	}

	// --- 3. ListVirtualMachines filter by application_name → same, resolved
	billingName := vmAppBilling
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID:  &acct.ID,
		ApplicationName: &billingName,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_name: %v", err)
	}
	if len(items) != 1 || items[0].ID != vmLinked.ID {
		t.Fatalf("list by application_name: got %d items, want 1 (vmLinked)", len(items))
	}

	// --- 3a. application_name input is normalised (kebab-case) -----------
	mixedCase := "Billing"
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID:  &acct.ID,
		ApplicationName: &mixedCase,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by application_name (mixed case): %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("normalisation broke the lookup: got %d items, want 1", len(items))
	}

	// --- 3b. id wins on conflict (store-side precedence) ------------------
	otherName := "checkout"
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID:  &acct.ID,
		ApplicationID:   &billing.ID,
		ApplicationName: &otherName,
	}, 50, "")
	if err != nil {
		t.Fatalf("list with id+name: %v", err)
	}
	if len(items) != 1 || items[0].ID != vmLinked.ID {
		t.Fatalf("id should win on conflict: got %+v", items)
	}

	// --- 4. unlinked=true → returns only the unlinked row ----------------
	yes := true
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID: &acct.ID,
		Unlinked:       &yes,
	}, 50, "")
	if err != nil {
		t.Fatalf("list unlinked: %v", err)
	}
	if len(items) != 1 || items[0].ID != vmUnlinked.ID {
		t.Fatalf("list unlinked: got %d items, want 1 (vmUnlinked); items=%+v", len(items), items)
	}

	// --- 5. ListVirtualMachines with unknown application_name → 0 rows ---
	bogus := appLinkageNoSuchApp
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		CloudAccountID:  &acct.ID,
		ApplicationName: &bogus,
	}, 50, "")
	if err != nil {
		t.Fatalf("list bogus app name: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("unknown application_name should yield 0 rows, got %d", len(items))
	}

	// --- 6. Per-entry application_id on the JSONB array: invalid id ------
	bogusID := uuid.New()
	_, err = pg.UpdateVirtualMachine(ctx, vmLinked.ID, api.VirtualMachinePatch{
		Applications: &[]api.VMApplication{
			{Product: "vault", Version: "1.15.4", ApplicationID: &bogusID},
		},
	})
	if err == nil {
		t.Fatal("expected error for unknown per-entry application_id; got nil")
	}
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for invalid per-entry application_id, got %v", err)
	}

	// --- 7. Per-entry application_id with valid id → entry persists ------
	updated, err = pg.UpdateVirtualMachine(ctx, vmLinked.ID, api.VirtualMachinePatch{
		Applications: &[]api.VMApplication{
			{Product: "vault", Version: "1.15.4", ApplicationID: &checkout.ID},
			{Product: "bind", Version: "9.18", ApplicationID: &billing.ID},
		},
	})
	if err != nil {
		t.Fatalf("update with valid per-entry application_ids: %v", err)
	}
	if len(updated.Applications) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(updated.Applications))
	}
	// Order is replace-not-merge → preserved as supplied.
	if updated.Applications[0].ApplicationID == nil || *updated.Applications[0].ApplicationID != checkout.ID {
		t.Fatalf("vault entry: expected application_id=%v, got %v", checkout.ID, updated.Applications[0].ApplicationID)
	}
	if updated.Applications[1].ApplicationID == nil || *updated.Applications[1].ApplicationID != billing.ID {
		t.Fatalf("bind entry: expected application_id=%v, got %v", billing.ID, updated.Applications[1].ApplicationID)
	}

	// --- 8. COLLECTOR INVARIANT (ADR-0029 §7): UpsertVirtualMachine must
	// NOT touch row-level application_id NOR the per-entry application_id
	// in the JSONB column.
	_, _, err = pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-vm-linked-0000",
		Name:           "vm-linked",
		PowerState:     "stopped", // changed business field so the upsert isn't a no-op
		Ready:          false,
		Region:         strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("collector tick upsert: %v", err)
	}
	got, err = pg.GetVirtualMachine(ctx, vmLinked.ID)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if got.ApplicationID == nil || *got.ApplicationID != billing.ID {
		t.Fatalf("collector invariant violated: row-level application_id=%v after upsert, want %v",
			got.ApplicationID, billing.ID)
	}
	if got.PowerState != "stopped" || got.Ready {
		t.Fatalf("upsert should still update collector fields: power_state=%q ready=%v",
			got.PowerState, got.Ready)
	}
	// Per-entry application_id survives the collector tick too.
	if len(got.Applications) != 2 {
		t.Fatalf("collector tick changed applications length: got %d, want 2", len(got.Applications))
	}
	if got.Applications[0].ApplicationID == nil || *got.Applications[0].ApplicationID != checkout.ID {
		t.Fatalf("collector invariant violated (per-entry vault): application_id=%v want %v",
			got.Applications[0].ApplicationID, checkout.ID)
	}
	if got.Applications[1].ApplicationID == nil || *got.Applications[1].ApplicationID != billing.ID {
		t.Fatalf("collector invariant violated (per-entry bind): application_id=%v want %v",
			got.Applications[1].ApplicationID, billing.ID)
	}

	// --- 9. Re-link the VM to a different application -------------------
	updated, err = pg.UpdateVirtualMachine(ctx, vmLinked.ID, api.VirtualMachinePatch{
		ApplicationID: &checkout.ID,
	})
	if err != nil {
		t.Fatalf("re-link: %v", err)
	}
	if updated.ApplicationID == nil || *updated.ApplicationID != checkout.ID {
		t.Fatalf("expected application_id=%v after re-link, got %v", checkout.ID, updated.ApplicationID)
	}

	// --- 10. Deleting the application sets the FK to NULL ----------------
	if err := pg.DeleteApplication(ctx, checkout.ID); err != nil {
		t.Fatalf("delete application: %v", err)
	}
	got, err = pg.GetVirtualMachine(ctx, vmLinked.ID)
	if err != nil {
		t.Fatalf("get after delete app: %v", err)
	}
	if got.ApplicationID != nil {
		t.Fatalf("ON DELETE SET NULL violated: application_id=%v", *got.ApplicationID)
	}
}

// TestPGVMApplicationNameSubstring exercises ADR-0029 §2.4 — the
// substring filter on the linked application's name on the VM side of
// the cross-entity Search endpoint. Covers: hit, case-insensitivity,
// non-matching exclusion, and LIKE-metacharacter safety.
//
//nolint:gocyclo // covers several distinct cases; complexity is inherent.
func TestPGVMApplicationNameSubstring(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acct := vmTestAccount(t, pg, "vm-app-substring")

	vaultPlat, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "vault-platform"})
	if err != nil {
		t.Fatalf("create vault-platform: %v", err)
	}
	billing, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: vmAppBilling})
	if err != nil {
		t.Fatalf("create billing: %v", err)
	}

	vmVault, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID, ProviderVMID: "i-vm-vault", Name: "vm-vault",
		PowerState: "running", Ready: true, Region: strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("upsert vmVault: %v", err)
	}
	vmBilling, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID, ProviderVMID: "i-vm-billing", Name: "vm-billing",
		PowerState: "running", Ready: true, Region: strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("upsert vmBilling: %v", err)
	}

	if _, err := pg.UpdateVirtualMachine(ctx, vmVault.ID, api.VirtualMachinePatch{
		ApplicationID: &vaultPlat.ID,
	}); err != nil {
		t.Fatalf("link vmVault: %v", err)
	}
	if _, err := pg.UpdateVirtualMachine(ctx, vmBilling.ID, api.VirtualMachinePatch{
		ApplicationID: &billing.ID,
	}); err != nil {
		t.Fatalf("link vmBilling: %v", err)
	}

	// --- substring "vault" → only the vault-platform-linked VM ----------
	vault := "vault"
	items, _, err := pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		ApplicationNameSubstring: &vault,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by substring 'vault': %v", err)
	}
	if len(items) != 1 || items[0].ID != vmVault.ID {
		t.Fatalf("substring 'vault': want 1 row (vmVault), got %d (%+v)", len(items), items)
	}

	// --- case-insensitive ----------------------------------------------
	vaultUpper := "VaUlT"
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		ApplicationNameSubstring: &vaultUpper,
	}, 50, "")
	if err != nil {
		t.Fatalf("list by substring 'VaUlT': %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("case-insensitive lookup broken: got %d, want 1", len(items))
	}

	// --- non-matching → 0 rows -----------------------------------------
	miss := appLinkageNoSuchApp
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		ApplicationNameSubstring: &miss,
	}, 50, "")
	if err != nil {
		t.Fatalf("list miss: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("non-matching substring: want 0 rows, got %d", len(items))
	}

	// --- LIKE metacharacter safety: underscore must be a literal -------
	underscore := "vault_"
	items, _, err = pg.ListVirtualMachines(ctx, api.VirtualMachineListFilter{
		ApplicationNameSubstring: &underscore,
	}, 50, "")
	if err != nil {
		t.Fatalf("list 'vault_': %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("LIKE underscore must be escaped: got %d rows, want 0", len(items))
	}
}
