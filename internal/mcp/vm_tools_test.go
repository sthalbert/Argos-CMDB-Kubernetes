//nolint:goconst // duplicated literals in assertions are clearer than named constants.
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func newTerminatedVM(id, accountID uuid.UUID, name string) api.VirtualMachine {
	now := time.Now().UTC()
	term := now.Add(-time.Hour)
	return api.VirtualMachine{
		ID: id, CloudAccountID: accountID, ProviderVMID: "i-" + name, Name: name,
		PowerState: "terminated", TerminatedAt: &term,
		Applications: []api.VMApplication{}, CreatedAt: now, UpdatedAt: now, LastSeenAt: now,
	}
}

func newVM(id, accountID uuid.UUID, name string, apps []api.VMApplication) api.VirtualMachine {
	now := time.Now().UTC()
	return api.VirtualMachine{
		ID: id, CloudAccountID: accountID, ProviderVMID: "i-" + name, Name: name,
		PowerState: "running", Ready: true, Applications: apps,
		CreatedAt: now, UpdatedAt: now, LastSeenAt: now,
	}
}

func TestHandleListVirtualMachines_NoFilters(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	acct := uuid.New()
	store.vms = []api.VirtualMachine{
		newVM(uuid.New(), acct, "vault-1", nil),
		newVM(uuid.New(), acct, "dns-1", nil),
		newTerminatedVM(uuid.New(), acct, "old-1"),
	}
	s := newServer(t, store)

	r, err := s.handleListVirtualMachines(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got []api.VirtualMachine
	if err := json.Unmarshal([]byte(resultText(t, r)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d; want 2 (terminated excluded by default)", len(got))
	}
	if store.lastVMFilter.IncludeTerminated {
		t.Error("default IncludeTerminated should be false")
	}
}

func TestHandleListVirtualMachines_FilterByCloudAccount(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	a, b := uuid.New(), uuid.New()
	store.vms = []api.VirtualMachine{
		newVM(uuid.New(), a, "vm-a", nil),
		newVM(uuid.New(), b, "vm-b", nil),
	}
	s := newServer(t, store)

	r, _ := s.handleListVirtualMachines(context.Background(), makeRequest("", map[string]any{"cloud_account_id": a.String()}))
	var got []api.VirtualMachine
	_ = json.Unmarshal([]byte(resultText(t, r)), &got)
	if len(got) != 1 || got[0].Name != "vm-a" {
		t.Errorf("got = %+v; want only vm-a", got)
	}
	if store.lastVMFilter.CloudAccountID == nil || *store.lastVMFilter.CloudAccountID != a {
		t.Errorf("filter.CloudAccountID = %v; want %v", store.lastVMFilter.CloudAccountID, a)
	}
}

func TestHandleListVirtualMachines_ApplicationFilter(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	acct := uuid.New()
	store.vms = []api.VirtualMachine{
		newVM(uuid.New(), acct, "vault-1", []api.VMApplication{{Product: "vault", Version: "1.15.4"}}),
		newVM(uuid.New(), acct, "vault-2", []api.VMApplication{{Product: "vault", Version: "1.16.0"}}),
		newVM(uuid.New(), acct, "dns-1", []api.VMApplication{{Product: "bind", Version: "9.18"}}),
	}
	s := newServer(t, store)

	r, _ := s.handleListVirtualMachines(context.Background(), makeRequest("", map[string]any{
		"application":         "Vault",
		"application_version": "1.15.4",
	}))
	var got []api.VirtualMachine
	_ = json.Unmarshal([]byte(resultText(t, r)), &got)
	if len(got) != 1 || got[0].Name != "vault-1" {
		t.Errorf("got = %+v; want only vault-1", got)
	}
	if store.lastVMFilter.Application == nil || *store.lastVMFilter.Application != "Vault" {
		t.Errorf("filter.Application = %v; want 'Vault'", store.lastVMFilter.Application)
	}
	if store.lastVMFilter.ApplicationVersion == nil || *store.lastVMFilter.ApplicationVersion != "1.15.4" {
		t.Errorf("filter.ApplicationVersion = %v", store.lastVMFilter.ApplicationVersion)
	}
}

func TestHandleListVirtualMachines_IncludeTerminated(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	acct := uuid.New()
	store.vms = []api.VirtualMachine{
		newVM(uuid.New(), acct, "live", nil),
		newTerminatedVM(uuid.New(), acct, "dead"),
	}
	s := newServer(t, store)

	r, _ := s.handleListVirtualMachines(context.Background(), makeRequest("", map[string]any{"include_terminated": true}))
	var got []api.VirtualMachine
	_ = json.Unmarshal([]byte(resultText(t, r)), &got)
	if len(got) != 2 {
		t.Errorf("len = %d; want 2 (terminated included)", len(got))
	}
	if !store.lastVMFilter.IncludeTerminated {
		t.Error("filter.IncludeTerminated = false; want true")
	}
}

func TestHandleListVirtualMachines_RejectsOversizedName(t *testing.T) {
	t.Parallel()
	s := newServer(t, newFakeStore())
	r, err := s.handleListVirtualMachines(context.Background(), makeRequest("", map[string]any{
		"name": strings.Repeat("a", 200),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !r.IsError {
		t.Error("oversized name should produce IsError")
	}
}

func TestHandleGetCloudAccount_HappyPathRedacts(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	id := uuid.New()
	ak := "AKIASECRETPUBLICID"
	now := time.Now().UTC()
	store.accounts = []api.CloudAccount{
		{ID: id, Provider: "outscale", Name: "prod", Region: "eu-west-2", Status: api.CloudAccountStatusActive, AccessKey: &ak, CreatedAt: now, UpdatedAt: now},
	}
	s := newServer(t, store)

	r, err := s.handleGetCloudAccount(context.Background(), makeRequest("", map[string]any{"id": id.String()}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := resultText(t, r)
	if strings.Contains(text, "access_key") {
		t.Errorf("response contains access_key: %s", text)
	}
	if strings.Contains(text, ak) {
		t.Errorf("response leaks AK value")
	}
}

func TestHandleGetCloudAccount_NotFound(t *testing.T) {
	t.Parallel()
	s := newServer(t, newFakeStore())
	r, err := s.handleGetCloudAccount(context.Background(), makeRequest("", map[string]any{"id": uuid.New().String()}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !r.IsError {
		t.Error("missing account should produce IsError")
	}
	if !strings.Contains(resultText(t, r), "cloud account not found") {
		t.Errorf("text = %q; want 'cloud account not found'", resultText(t, r))
	}
}

func TestHandleListCloudAccounts_RedactsAccessKey(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	ak1, ak2 := "AKIA0000PUBLIC0001", "AKIA0000PUBLIC0002"
	now := time.Now().UTC()
	store.accounts = []api.CloudAccount{
		{ID: uuid.New(), Provider: "outscale", Name: "prod-eu", Region: "eu-west-2", Status: api.CloudAccountStatusActive, AccessKey: &ak1, CreatedAt: now, UpdatedAt: now},
		{ID: uuid.New(), Provider: "outscale", Name: "prod-us", Region: "us-east-2", Status: api.CloudAccountStatusActive, AccessKey: &ak2, CreatedAt: now, UpdatedAt: now},
	}
	s := newServer(t, store)

	r, err := s.handleListCloudAccounts(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	text := resultText(t, r)
	if strings.Contains(text, "access_key") {
		t.Errorf("response contains access_key field: %s", text)
	}
	if strings.Contains(text, ak1) || strings.Contains(text, ak2) {
		t.Errorf("response leaks access key value")
	}
	var got []api.CloudAccount
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d; want 2", len(got))
	}
	for i := range got {
		if got[i].AccessKey != nil {
			t.Errorf("account %d AccessKey not redacted", i)
		}
	}
}
