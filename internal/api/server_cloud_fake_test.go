package api

// Cloud-account / virtual-machine fake-store methods for memStore.
// Mirrors the pattern in server_auth_fake_test.go: an in-memory store
// good enough for unit tests of the cloud handlers without touching PG.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/secrets"
)

// memCloudState is the cloud-substrate slice of memStore. Embedded into
// the existing memStore via package-level globals so existing callers
// don't have to be touched.
type memCloudState struct {
	mu       sync.Mutex
	accounts map[uuid.UUID]CloudAccount
	creds    map[uuid.UUID]storedCreds
	vms      map[uuid.UUID]VirtualMachine
}

type storedCreds struct {
	AccessKey string
	Encrypted secrets.Ciphertext
}

// cloud is the singleton cloud state shared by every memStore in the
// test process. Tests instantiate fresh stores; for cloud unit tests
// that exercise multiple handlers in sequence we rely on the per-test
// reset helper resetCloudFake.
var cloudFake = &memCloudState{
	accounts: make(map[uuid.UUID]CloudAccount),
	creds:    make(map[uuid.UUID]storedCreds),
	vms:      make(map[uuid.UUID]VirtualMachine),
}

// resetCloudFake wipes the in-memory cloud state. Tests should call
// this in setup to avoid cross-test bleed.
func resetCloudFake() {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	cloudFake.accounts = make(map[uuid.UUID]CloudAccount)
	cloudFake.creds = make(map[uuid.UUID]storedCreds)
	cloudFake.vms = make(map[uuid.UUID]VirtualMachine)
}

func (m *memStore) UpsertCloudAccount(_ context.Context, in CloudAccountUpsert) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	for _, a := range cloudFake.accounts { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to avoid mutation
		if a.Provider == in.Provider && a.Name == in.Name {
			a.Region = in.Region
			a.UpdatedAt = time.Now().UTC()
			cloudFake.accounts[a.ID] = a
			return a, nil
		}
	}
	now := time.Now().UTC()
	a := CloudAccount{
		ID:        uuid.New(),
		Provider:  in.Provider,
		Name:      in.Name,
		Region:    in.Region,
		Status:    CloudAccountStatusPendingCredentials,
		CreatedAt: now,
		UpdatedAt: now,
	}
	cloudFake.accounts[a.ID] = a
	return a, nil
}

func (m *memStore) GetCloudAccount(_ context.Context, id uuid.UUID) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return CloudAccount{}, ErrNotFound
	}
	return a, nil
}

func (m *memStore) GetCloudAccountByName(_ context.Context, provider, name string) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	for _, a := range cloudFake.accounts { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to avoid mutation
		if a.Provider == provider && a.Name == name {
			return a, nil
		}
	}
	return CloudAccount{}, ErrNotFound
}

func (m *memStore) GetCloudAccountByNameAny(_ context.Context, name string) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	for _, a := range cloudFake.accounts { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to avoid mutation
		if a.Name == name {
			return a, nil
		}
	}
	return CloudAccount{}, ErrNotFound
}

func (m *memStore) ListCloudAccounts(_ context.Context, limit int, _ string) ([]CloudAccount, string, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]CloudAccount, 0, len(cloudFake.accounts))
	for _, a := range cloudFake.accounts { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to avoid mutation
		out = append(out, a)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

//nolint:gocyclo,gocritic // merge-patch checks; hugeParam for interface
func (m *memStore) UpdateCloudAccount(_ context.Context, id uuid.UUID, in CloudAccountPatch) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return CloudAccount{}, ErrNotFound
	}
	if in.Name != nil {
		a.Name = *in.Name
	}
	if in.Region != nil {
		a.Region = *in.Region
	}
	if in.Owner != nil {
		s := *in.Owner
		a.Owner = &s
	}
	if in.Criticality != nil {
		s := *in.Criticality
		a.Criticality = &s
	}
	if in.Notes != nil {
		s := *in.Notes
		a.Notes = &s
	}
	if in.RunbookURL != nil {
		s := *in.RunbookURL
		a.RunbookURL = &s
	}
	if in.Annotations != nil {
		a.Annotations = *in.Annotations
	}
	if in.Status != nil {
		switch *in.Status {
		case CloudAccountStatusActive, CloudAccountStatusError:
			a.Status = *in.Status
		default:
			return CloudAccount{}, ErrConflict
		}
	}
	if in.LastSeenAt != nil {
		v := *in.LastSeenAt
		a.LastSeenAt = &v
	}
	if in.LastError != nil {
		v := *in.LastError
		a.LastError = &v
	}
	if in.LastErrorAt != nil {
		v := *in.LastErrorAt
		a.LastErrorAt = &v
	}
	a.UpdatedAt = time.Now().UTC()
	cloudFake.accounts[id] = a
	return a, nil
}

func (m *memStore) SetCloudAccountCredentials(_ context.Context, id uuid.UUID, accessKey string, encSK secrets.Ciphertext) (CloudAccount, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return CloudAccount{}, ErrNotFound
	}
	ak := accessKey
	a.AccessKey = &ak
	a.Status = CloudAccountStatusActive
	a.UpdatedAt = time.Now().UTC()
	cloudFake.accounts[id] = a
	cloudFake.creds[id] = storedCreds{AccessKey: accessKey, Encrypted: encSK}
	return a, nil
}

func (m *memStore) GetCloudAccountCredentials(_ context.Context, id uuid.UUID) (string, secrets.Ciphertext, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return "", secrets.Ciphertext{}, ErrNotFound
	}
	if a.Status == CloudAccountStatusDisabled {
		return "", secrets.Ciphertext{}, ErrConflict
	}
	if a.Status == CloudAccountStatusPendingCredentials {
		return "", secrets.Ciphertext{}, ErrNotFound
	}
	creds, ok := cloudFake.creds[id]
	if !ok {
		return "", secrets.Ciphertext{}, ErrNotFound
	}
	return creds.AccessKey, creds.Encrypted, nil
}

func (m *memStore) UpdateCloudAccountStatus(_ context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastError *string) error {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.Status == CloudAccountStatusDisabled || a.Status == CloudAccountStatusPendingCredentials {
		return ErrConflict
	}
	switch status {
	case "":
	case CloudAccountStatusActive, CloudAccountStatusError:
		a.Status = status
	default:
		return ErrConflict
	}
	if lastSeenAt != nil {
		v := *lastSeenAt
		a.LastSeenAt = &v
	}
	if lastError != nil {
		v := *lastError
		a.LastError = &v
		now := time.Now().UTC()
		a.LastErrorAt = &now
	}
	a.UpdatedAt = time.Now().UTC()
	cloudFake.accounts[id] = a
	return nil
}

func (m *memStore) DisableCloudAccount(_ context.Context, id uuid.UUID) error {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return ErrNotFound
	}
	a.Status = CloudAccountStatusDisabled
	now := time.Now().UTC()
	a.DisabledAt = &now
	a.UpdatedAt = now
	cloudFake.accounts[id] = a
	return nil
}

func (m *memStore) EnableCloudAccount(_ context.Context, id uuid.UUID) error {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	a, ok := cloudFake.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if a.AccessKey == nil {
		a.Status = CloudAccountStatusPendingCredentials
	} else {
		a.Status = CloudAccountStatusActive
	}
	a.DisabledAt = nil
	a.UpdatedAt = time.Now().UTC()
	cloudFake.accounts[id] = a
	return nil
}

func (m *memStore) DeleteCloudAccount(_ context.Context, id uuid.UUID) error {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	if _, ok := cloudFake.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(cloudFake.accounts, id)
	delete(cloudFake.creds, id)
	for vmID, vm := range cloudFake.vms { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to avoid mutation
		if vm.CloudAccountID == id {
			delete(cloudFake.vms, vmID)
		}
	}
	return nil
}

func (m *memStore) CountCloudAccountsWithSecrets(_ context.Context) (int, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	return len(cloudFake.creds), nil
}

//nolint:gocritic // hugeParam: Store interface requires value param
func (m *memStore) UpsertVirtualMachine(_ context.Context, in VirtualMachineUpsert) (VirtualMachine, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	// Check provider_vm_id conflict — simulate the nodes.provider_id dedup
	// by also checking for a conflict marker tag.
	if v, ok := in.Tags["argos.test.is_kube"]; ok && v == "true" {
		return VirtualMachine{}, ErrConflict
	}
	for vmID, vm := range cloudFake.vms {
		if vm.CloudAccountID == in.CloudAccountID && vm.ProviderVMID == in.ProviderVMID {
			vm.Name = in.Name
			vm.PowerState = in.PowerState
			vm.Ready = in.Ready
			vm.UpdatedAt = time.Now().UTC()
			vm.LastSeenAt = vm.UpdatedAt
			vm.TerminatedAt = nil
			cloudFake.vms[vmID] = vm
			return vm, nil
		}
	}
	now := time.Now().UTC()
	vm := VirtualMachine{
		ID:                 uuid.New(),
		CloudAccountID:     in.CloudAccountID,
		ProviderVMID:       in.ProviderVMID,
		Name:               in.Name,
		Role:               in.Role,
		PowerState:         in.PowerState,
		Ready:              in.Ready,
		DeletionProtection: in.DeletionProtection,
		Tags:               in.Tags,
		Labels:             in.Labels,
		CreatedAt:          now,
		UpdatedAt:          now,
		LastSeenAt:         now,
	}
	cloudFake.vms[vm.ID] = vm
	return vm, nil
}

func (m *memStore) GetVirtualMachine(_ context.Context, id uuid.UUID) (VirtualMachine, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	vm, ok := cloudFake.vms[id]
	if !ok {
		return VirtualMachine{}, ErrNotFound
	}
	return vm, nil
}

// vmMatchesFilter reports whether vm passes the given filter.
// Called from ListVirtualMachines; extracted to keep cognitive complexity low.
//
//nolint:gocyclo,gocritic // filter evaluation is inherently branchy; hugeParam ok in test fake
func vmMatchesFilter(vm VirtualMachine, filter VirtualMachineListFilter) bool {
	if !filter.IncludeTerminated && vm.TerminatedAt != nil {
		return false
	}
	if filter.CloudAccountID != nil && vm.CloudAccountID != *filter.CloudAccountID {
		return false
	}
	if filter.CloudAccountName != nil {
		a, ok := cloudFake.accounts[vm.CloudAccountID]
		if !ok || a.Name != *filter.CloudAccountName {
			return false
		}
	}
	if filter.Region != nil && (vm.Region == nil || *vm.Region != *filter.Region) {
		return false
	}
	if filter.Role != nil && (vm.Role == nil || *vm.Role != *filter.Role) {
		return false
	}
	if filter.PowerState != nil && vm.PowerState != *filter.PowerState {
		return false
	}
	if filter.Name != nil && !strings.Contains(strings.ToLower(vm.Name), strings.ToLower(*filter.Name)) {
		return false
	}
	if !vmImageMatches(vm, filter.Image) {
		return false
	}
	if !vmApplicationMatches(vm, filter.Application, filter.ApplicationVersion) {
		return false
	}
	return true
}

// vmImageMatches checks whether vm.ImageID or vm.ImageName contain needle
// (case-insensitive). Returns true when needle is nil.
//
//nolint:gocritic // hugeParam: test fake; value copy is fine
func vmImageMatches(vm VirtualMachine, needle *string) bool {
	if needle == nil {
		return true
	}
	n := strings.ToLower(*needle)
	imageID, imageName := "", ""
	if vm.ImageID != nil {
		imageID = strings.ToLower(*vm.ImageID)
	}
	if vm.ImageName != nil {
		imageName = strings.ToLower(*vm.ImageName)
	}
	return strings.Contains(imageID, n) || strings.Contains(imageName, n)
}

// vmApplicationMatches checks whether any of vm.Applications has the given
// product (normalized). When wantVersion is non-nil, also requires the
// matching entry to have that version. Returns true when want is nil.
//
//nolint:gocritic // hugeParam: test fake; value copy is fine
func vmApplicationMatches(vm VirtualMachine, want, wantVersion *string) bool {
	if want == nil {
		return true
	}
	normalized := NormalizeProductName(*want)
	var version string
	if wantVersion != nil {
		version = strings.TrimSpace(*wantVersion)
	}
	for _, app := range vm.Applications {
		if app.Product != normalized {
			continue
		}
		if version != "" && app.Version != version {
			continue
		}
		return true
	}
	return false
}

//nolint:gocritic // hugeParam: signature matches Store interface
func (m *memStore) ListVirtualMachines(_ context.Context, filter VirtualMachineListFilter, limit int, _ string) ([]VirtualMachine, string, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]VirtualMachine, 0, len(cloudFake.vms))
	for _, vm := range cloudFake.vms { //nolint:gocritic // rangeValCopy: test fake; copy is intentional
		if vmMatchesFilter(vm, filter) {
			out = append(out, vm)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

//nolint:gocyclo // aggregates versions per product; complexity is inherent in the in-memory grouping
func (m *memStore) ListDistinctVMApplications(_ context.Context) ([]VMApplicationDistinct, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	versionsByProduct := make(map[string]map[string]struct{})
	for _, vm := range cloudFake.vms { //nolint:gocritic // rangeValCopy: test fake; copy is intentional
		if vm.TerminatedAt != nil {
			continue
		}
		for _, app := range vm.Applications {
			if app.Product == "" || app.Version == "" {
				continue
			}
			if _, ok := versionsByProduct[app.Product]; !ok {
				versionsByProduct[app.Product] = make(map[string]struct{})
			}
			versionsByProduct[app.Product][app.Version] = struct{}{}
		}
	}
	out := make([]VMApplicationDistinct, 0, len(versionsByProduct))
	for product, versionSet := range versionsByProduct {
		versions := make([]string, 0, len(versionSet))
		for v := range versionSet {
			versions = append(versions, v)
		}
		// Sort versions to mirror PG behaviour.
		for i := 1; i < len(versions); i++ {
			for j := i; j > 0 && versions[j-1] > versions[j]; j-- {
				versions[j-1], versions[j] = versions[j], versions[j-1]
			}
		}
		out = append(out, VMApplicationDistinct{Product: product, Versions: versions})
	}
	// Sort by product to mirror PG behaviour.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Product > out[j].Product; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

func (m *memStore) UpdateVirtualMachine(_ context.Context, id uuid.UUID, in VirtualMachinePatch) (VirtualMachine, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	vm, ok := cloudFake.vms[id]
	if !ok {
		return VirtualMachine{}, ErrNotFound
	}
	if in.DisplayName != nil {
		s := *in.DisplayName
		vm.DisplayName = &s
	}
	if in.Role != nil {
		s := *in.Role
		vm.Role = &s
	}
	if in.Owner != nil {
		s := *in.Owner
		vm.Owner = &s
	}
	if in.Criticality != nil {
		s := *in.Criticality
		vm.Criticality = &s
	}
	if in.Notes != nil {
		s := *in.Notes
		vm.Notes = &s
	}
	if in.RunbookURL != nil {
		s := *in.RunbookURL
		vm.RunbookURL = &s
	}
	if in.Annotations != nil {
		vm.Annotations = *in.Annotations
	}
	if in.Applications != nil {
		// Replace-not-merge: handler has already done the diff.
		copyApps := make([]VMApplication, len(*in.Applications))
		copy(copyApps, *in.Applications)
		vm.Applications = copyApps
	}
	vm.UpdatedAt = time.Now().UTC()
	cloudFake.vms[id] = vm
	return vm, nil
}

func (m *memStore) DeleteVirtualMachine(_ context.Context, id uuid.UUID) error {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	vm, ok := cloudFake.vms[id]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	vm.TerminatedAt = &now
	vm.PowerState = "terminated"
	vm.Ready = false
	cloudFake.vms[id] = vm
	return nil
}

func (m *memStore) ReconcileVirtualMachines(_ context.Context, accountID uuid.UUID, keepProviderVMIDs []string) (int64, error) {
	cloudFake.mu.Lock()
	defer cloudFake.mu.Unlock()
	keep := make(map[string]struct{}, len(keepProviderVMIDs))
	for _, k := range keepProviderVMIDs {
		keep[k] = struct{}{}
	}
	var n int64
	now := time.Now().UTC()
	for id, vm := range cloudFake.vms { //nolint:gocritic // rangeValCopy: test fake; copy is intentional to write back modified value
		if vm.CloudAccountID != accountID || vm.TerminatedAt != nil {
			continue
		}
		if _, ok := keep[vm.ProviderVMID]; ok {
			continue
		}
		vm.TerminatedAt = &now
		vm.PowerState = "terminated"
		vm.Ready = false
		cloudFake.vms[id] = vm
		n++
	}
	return n, nil
}

// silence the unused-import warning for `strings` which we keep for
// future filter implementations.
var _ = strings.HasPrefix
