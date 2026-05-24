package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// TestPGListVMsWithApplicationEntry verifies the JSONB-entry walk that the
// per-application EOL aggregator's third source relies on (ADR-0029 §5):
// every non-terminated VM with at least one applications[] entry whose
// per-entry application_id matches is returned, regardless of the VM's
// row-level application_id link. Terminated VMs and VMs whose entries point
// at a different application are excluded.
//
//nolint:gocyclo // integration test exercises several distinct match paths
func TestPGListVMsWithApplicationEntry(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acct := vmTestAccount(t, pg, "vm-app-entry")

	vault, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "vault-platform"})
	if err != nil {
		t.Fatalf("create application vault: %v", err)
	}
	other, err := pg.CreateApplication(ctx, api.ApplicationCreate{Name: "other-app"})
	if err != nil {
		t.Fatalf("create application other: %v", err)
	}

	// VM A: NOT row-level linked, but its vault entry points at vault.
	vmA, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-entry-a-0000",
		Name:           "bastion-a",
		PowerState:     "running",
		Ready:          true,
	})
	if err != nil {
		t.Fatalf("upsert vmA: %v", err)
	}
	if _, err := pg.UpdateVirtualMachine(ctx, vmA.ID, api.VirtualMachinePatch{
		Applications: &[]api.VMApplication{
			{Product: "vault", Version: "1.13", ApplicationID: &vault.ID},
			{Product: "bind", Version: "9.18", ApplicationID: &other.ID},
		},
	}); err != nil {
		t.Fatalf("patch vmA applications: %v", err)
	}

	// VM B: only an entry pointing at `other` → must NOT match vault.
	vmB, _, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-entry-b-0000",
		Name:           "bastion-b",
		PowerState:     "running",
		Ready:          true,
	})
	if err != nil {
		t.Fatalf("upsert vmB: %v", err)
	}
	if _, err := pg.UpdateVirtualMachine(ctx, vmB.ID, api.VirtualMachinePatch{
		Applications: &[]api.VMApplication{
			{Product: "redis", Version: "7", ApplicationID: &other.ID},
		},
	}); err != nil {
		t.Fatalf("patch vmB applications: %v", err)
	}

	got, err := pg.ListVMsWithApplicationEntry(ctx, vault.ID)
	if err != nil {
		t.Fatalf("ListVMsWithApplicationEntry: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 VM for vault, got %d: %+v", len(got), got)
	}
	if got[0].ID != vmA.ID {
		t.Fatalf("expected vmA (%v), got %v", vmA.ID, got[0].ID)
	}
	// The full VM is returned (annotations + applications) so the aggregator
	// can read the matching entry and its annotation.
	if len(got[0].Applications) != 2 {
		t.Errorf("expected vmA's 2 applications entries returned, got %d", len(got[0].Applications))
	}

	// An application with no matching entries → zero rows (no error).
	none, err := pg.ListVMsWithApplicationEntry(ctx, vmB.ID) // vmB.ID is not an application id
	if err != nil {
		t.Fatalf("ListVMsWithApplicationEntry (no match): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0 VMs for a non-application id, got %d", len(none))
	}

	// Soft-delete vmA → it must drop out of the result.
	if err := pg.DeleteVirtualMachine(ctx, vmA.ID); err != nil {
		t.Fatalf("soft-delete vmA: %v", err)
	}
	afterDelete, err := pg.ListVMsWithApplicationEntry(ctx, vault.ID)
	if err != nil {
		t.Fatalf("ListVMsWithApplicationEntry after delete: %v", err)
	}
	if len(afterDelete) != 0 {
		t.Fatalf("terminated VM must be excluded, got %d", len(afterDelete))
	}
}
