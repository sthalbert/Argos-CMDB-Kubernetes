package store

import (
	"context"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// vmTestAccount seeds a cloud_accounts row so VirtualMachineUpsert can
// satisfy its FK. Each test gets its own (provider, name) pair.
func vmTestAccount(t *testing.T, pg *PG, name string) api.CloudAccount {
	t.Helper()
	ctx := context.Background()
	acct, err := pg.UpsertCloudAccount(ctx, api.CloudAccountUpsert{
		Provider: "outscale",
		Name:     name,
		Region:   "eu-west-2",
	})
	if err != nil {
		t.Fatalf("cloud account: %v", err)
	}
	return acct
}

func TestPG_UpsertVirtualMachine_OutcomeInserted(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acct := vmTestAccount(t, pg, "vm-outcome-inserted")

	_, outcome, err := pg.UpsertVirtualMachine(ctx, api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-0123456789abcdef",
		Name:           "vm-a",
		Region:         strPtr("eu-west-2"),
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if outcome != api.OutcomeInserted {
		t.Errorf("first upsert outcome = %v, want Inserted", outcome)
	}
}

func TestPG_UpsertVirtualMachine_OutcomeNoChange_ClockOnly(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acct := vmTestAccount(t, pg, "vm-outcome-nochange")

	payload := api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-0123456789abcdef",
		Name:           "vm-a",
		PrivateIP:      strPtr("10.0.0.10"),
		PublicIP:       strPtr("203.0.113.5"),
		InstanceType:   strPtr("m5.large"),
		Architecture:   strPtr("x86_64"),
		Zone:           strPtr("eu-west-2a"),
		Region:         strPtr("eu-west-2"),
		ImageID:        strPtr("ami-12345"),
		ImageName:      strPtr("ubuntu-22.04"),
		PowerState:     "running",
		Ready:          true,
		CapacityCPU:    strPtr("2"),
		CapacityMemory: strPtr("8Gi"),
		Tags:           map[string]string{"env": "prod"},
		Labels:         map[string]string{"team": "platform"},
	}

	if _, _, err := pg.UpsertVirtualMachine(ctx, payload); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	_, outcome, err := pg.UpsertVirtualMachine(ctx, payload)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if outcome != api.OutcomeNoChange {
		t.Errorf("identical second upsert outcome = %v, want NoChange (last_seen_at moved)", outcome)
	}
}

func TestPG_UpsertVirtualMachine_OutcomeBusinessChanged_PerField(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	acct := vmTestAccount(t, pg, "vm-outcome-changed")

	base := api.VirtualMachineUpsert{
		CloudAccountID: acct.ID,
		ProviderVMID:   "i-0123456789abcdef",
		Name:           "vm-a",
		PrivateIP:      strPtr("10.0.0.10"),
		PublicIP:       strPtr("203.0.113.5"),
		InstanceType:   strPtr("m5.large"),
		Architecture:   strPtr("x86_64"),
		Zone:           strPtr("eu-west-2a"),
		Region:         strPtr("eu-west-2"),
		ImageID:        strPtr("ami-12345"),
		ImageName:      strPtr("ubuntu-22.04"),
		PowerState:     "running",
		Ready:          true,
		CapacityCPU:    strPtr("2"),
		CapacityMemory: strPtr("8Gi"),
		Tags:           map[string]string{"env": "prod"},
		Labels:         map[string]string{"team": "platform"},
	}
	if _, _, err := pg.UpsertVirtualMachine(ctx, base); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	mutations := []struct {
		name string
		mut  func(p *api.VirtualMachineUpsert)
	}{
		{"name", func(p *api.VirtualMachineUpsert) { p.Name = "vm-a-renamed" }},
		{"private_ip", func(p *api.VirtualMachineUpsert) { p.PrivateIP = strPtr("10.0.0.11") }},
		{"public_ip", func(p *api.VirtualMachineUpsert) { p.PublicIP = strPtr("203.0.113.6") }},
		{"instance_type", func(p *api.VirtualMachineUpsert) { p.InstanceType = strPtr("m5.xlarge") }},
		{"power_state", func(p *api.VirtualMachineUpsert) { p.PowerState = "stopped" }},
		{"ready", func(p *api.VirtualMachineUpsert) { p.Ready = false }},
		{"capacity_cpu", func(p *api.VirtualMachineUpsert) { p.CapacityCPU = strPtr("4") }},
		{"capacity_memory", func(p *api.VirtualMachineUpsert) { p.CapacityMemory = strPtr("16Gi") }},
		{"image_id", func(p *api.VirtualMachineUpsert) { p.ImageID = strPtr("ami-99999") }},
		{"tags", func(p *api.VirtualMachineUpsert) { p.Tags = map[string]string{"env": "stage"} }},
		{"labels", func(p *api.VirtualMachineUpsert) { p.Labels = map[string]string{"team": "infra"} }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			pl := base
			m.mut(&pl)
			_, outcome, err := pg.UpsertVirtualMachine(ctx, pl)
			if err != nil {
				t.Fatalf("upsert: %v", err)
			}
			if outcome != api.OutcomeBusinessChanged {
				t.Errorf("touching %s gave outcome=%v, want BusinessChanged", m.name, outcome)
			}
			if _, _, err := pg.UpsertVirtualMachine(ctx, base); err != nil {
				t.Fatalf("reset: %v", err)
			}
		})
	}
}

func strPtr(s string) *string { return &s }
