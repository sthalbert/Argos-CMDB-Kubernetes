package api

// Hand-written HTTP handlers for the virtual_machines endpoints (ADR-0015).
// Mounted on the main mux next to the cloud-accounts handlers.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// timeNow is overridable in tests so the diff logic in diffVMApplications
// produces deterministic added_at stamps. Real callers use time.Now.
var timeNow = time.Now

// vmUpsertReq is the body for POST /v1/virtual-machines (collector).
type vmUpsertReq struct {
	CloudAccountID       uuid.UUID         `json:"cloud_account_id"`
	ProviderVMID         string            `json:"provider_vm_id"`
	Name                 string            `json:"name"`
	Role                 *string           `json:"role,omitempty"`
	PrivateIP            *string           `json:"private_ip,omitempty"`
	PublicIP             *string           `json:"public_ip,omitempty"`
	PrivateDNSName       *string           `json:"private_dns_name,omitempty"`
	VPCID                *string           `json:"vpc_id,omitempty"`
	SubnetID             *string           `json:"subnet_id,omitempty"`
	NICs                 json.RawMessage   `json:"nics,omitempty"`
	SecurityGroups       json.RawMessage   `json:"security_groups,omitempty"`
	InstanceType         *string           `json:"instance_type,omitempty"`
	Architecture         *string           `json:"architecture,omitempty"`
	Zone                 *string           `json:"zone,omitempty"`
	Region               *string           `json:"region,omitempty"`
	ImageID              *string           `json:"image_id,omitempty"`
	ImageName            *string           `json:"image_name,omitempty"`
	KeypairName          *string           `json:"keypair_name,omitempty"`
	BootMode             *string           `json:"boot_mode,omitempty"`
	ProviderAccountID    *string           `json:"provider_account_id,omitempty"`
	ProviderCreationDate *jsonTime         `json:"provider_creation_date,omitempty"`
	PowerState           string            `json:"power_state"`
	StateReason          *string           `json:"state_reason,omitempty"`
	Ready                bool              `json:"ready"`
	DeletionProtection   bool              `json:"deletion_protection"`
	KernelVersion        *string           `json:"kernel_version,omitempty"`
	OperatingSystem      *string           `json:"operating_system,omitempty"`
	CapacityCPU          *string           `json:"capacity_cpu,omitempty"`
	CapacityMemory       *string           `json:"capacity_memory,omitempty"`
	BlockDevices         json.RawMessage   `json:"block_devices,omitempty"`
	RootDeviceType       *string           `json:"root_device_type,omitempty"`
	RootDeviceName       *string           `json:"root_device_name,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
}

// vmPatchReq is the body for PATCH /v1/virtual-machines/{id}.
//
// Applications has replace-not-merge semantics (ADR-0019 §4): a non-nil
// pointer replaces the entire list. The handler diffs the input against
// the stored list to preserve `added_at` / `added_by` on entries whose
// (product, version, name) key is unchanged, and stamps fresh values
// on new entries. Input values for added_at / added_by are ignored.
type vmPatchReq struct {
	DisplayName  *string            `json:"display_name,omitempty"`
	Role         *string            `json:"role,omitempty"`
	Owner        *string            `json:"owner,omitempty"`
	Criticality  *string            `json:"criticality,omitempty"`
	Notes        *string            `json:"notes,omitempty"`
	RunbookURL   *string            `json:"runbook_url,omitempty"`
	Annotations  *map[string]string `json:"annotations,omitempty"`
	Applications *[]VMApplication   `json:"applications,omitempty"`
}

// VM applications input bounds — bounded to keep the JSONB column small,
// the audit log readable, and the GIN index efficient.
const (
	vmAppsMaxEntries     = 100
	vmAppProductMaxLen   = 64
	vmAppVersionMaxLen   = 64
	vmAppNameMaxLen      = 200
	vmAppNotesMaxLen     = 4096
	vmListNameMaxLen     = 100
	vmListImageMaxLen    = 100
	vmListAccountMaxLen  = 200
	vmListAppFilterMaxLn = 64
	// vmListEnumMaxLen caps region / role / power_state filter values.
	// These are exact-match enum-ish fields; an oversized value is never
	// legitimate and would only inflate the audit log on the hot path.
	vmListEnumMaxLen = 64
)

// vmReconcileReq is the body for POST /v1/virtual-machines/reconcile.
type vmReconcileReq struct {
	CloudAccountID    uuid.UUID `json:"cloud_account_id"`
	KeepProviderVMIDs []string  `json:"keep_provider_vm_ids"`
}

// HandleUpsertVirtualMachine — vm-collector scope. POST /v1/virtual-machines.
//
//nolint:gocyclo // body-to-upsert mapping is intentionally flat
func HandleUpsertVirtualMachine(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		var req vmUpsertReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if req.CloudAccountID == uuid.Nil || req.ProviderVMID == "" || req.Name == "" || req.PowerState == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request",
				"cloud_account_id, provider_vm_id, name, power_state required")
			return
		}
		if err := caller.EnforceCloudAccountBinding(req.CloudAccountID); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token not bound to this cloud account")
			return
		}
		in := VirtualMachineUpsert{
			CloudAccountID:       req.CloudAccountID,
			ProviderVMID:         req.ProviderVMID,
			Name:                 req.Name,
			Role:                 req.Role,
			PrivateIP:            req.PrivateIP,
			PublicIP:             req.PublicIP,
			PrivateDNSName:       req.PrivateDNSName,
			VPCID:                req.VPCID,
			SubnetID:             req.SubnetID,
			NICs:                 req.NICs,
			SecurityGroups:       req.SecurityGroups,
			InstanceType:         req.InstanceType,
			Architecture:         req.Architecture,
			Zone:                 req.Zone,
			Region:               req.Region,
			ImageID:              req.ImageID,
			ImageName:            req.ImageName,
			KeypairName:          req.KeypairName,
			BootMode:             req.BootMode,
			ProviderAccountID:    req.ProviderAccountID,
			ProviderCreationDate: parseOptTime(req.ProviderCreationDate),
			PowerState:           req.PowerState,
			StateReason:          req.StateReason,
			Ready:                req.Ready,
			DeletionProtection:   req.DeletionProtection,
			KernelVersion:        req.KernelVersion,
			OperatingSystem:      req.OperatingSystem,
			CapacityCPU:          req.CapacityCPU,
			CapacityMemory:       req.CapacityMemory,
			BlockDevices:         req.BlockDevices,
			RootDeviceType:       req.RootDeviceType,
			RootDeviceName:       req.RootDeviceName,
			Tags:                 req.Tags,
			Labels:               req.Labels,
		}
		vm, err := store.UpsertVirtualMachine(r.Context(), in)
		if err != nil {
			if errors.Is(err, ErrConflict) {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":          "already_inventoried_as_kubernetes_node",
					"provider_vm_id": req.ProviderVMID,
				})
				return
			}
			slog.Error("upsert virtual machine", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, vm)
	}
}

// parseVMListFilter builds a VirtualMachineListFilter from the request query
// string. Returns ("", filter) on success; returns (problem, zero) on the
// first validation error so the caller can surface a 400.
//
//nolint:gocyclo // each query param is an independent branch; flat and intentional
func parseVMListFilter(r *http.Request) (string, VirtualMachineListFilter) {
	q := r.URL.Query()
	var f VirtualMachineListFilter
	if v := q.Get("cloud_account_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return "invalid cloud_account_id", f
		}
		f.CloudAccountID = &id
	}
	if v := q.Get("cloud_account_name"); v != "" {
		if len(v) > vmListAccountMaxLen {
			return "cloud_account_name too long", f
		}
		f.CloudAccountName = &v
	}
	if v := q.Get("region"); v != "" {
		if len(v) > vmListEnumMaxLen {
			return "region too long", f
		}
		f.Region = &v
	}
	if v := q.Get("role"); v != "" {
		if len(v) > vmListEnumMaxLen {
			return "role too long", f
		}
		f.Role = &v
	}
	if v := q.Get("power_state"); v != "" {
		if len(v) > vmListEnumMaxLen {
			return "power_state too long", f
		}
		f.PowerState = &v
	}
	if v := q.Get("name"); v != "" {
		if len(v) > vmListNameMaxLen {
			return "name too long", f
		}
		f.Name = &v
	}
	if v := q.Get("image"); v != "" {
		if len(v) > vmListImageMaxLen {
			return "image too long", f
		}
		f.Image = &v
	}
	if v := q.Get("application"); v != "" {
		if len(v) > vmListAppFilterMaxLn {
			return "application too long", f
		}
		f.Application = &v
	}
	if v := q.Get("application_version"); v != "" {
		if len(v) > vmAppVersionMaxLen {
			return "application_version too long", f
		}
		f.ApplicationVersion = &v
	}
	if v := q.Get("include_terminated"); v != "" {
		b, _ := strconv.ParseBool(v)
		f.IncludeTerminated = b
	}
	return "", f
}

// HandleListVirtualMachines — read scope. GET /v1/virtual-machines.
func HandleListVirtualMachines(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		problem, filter := parseVMListFilter(r)
		if problem != "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", problem)
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		cursor := r.URL.Query().Get("cursor")
		items, next, err := store.ListVirtualMachines(r.Context(), filter, limit, cursor)
		if err != nil {
			slog.Error("list virtual machines", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	}
}

// HandleGetVirtualMachine — read scope. GET /v1/virtual-machines/{id}.
func HandleGetVirtualMachine(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		vm, err := store.GetVirtualMachine(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get virtual machine", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, vm)
	}
}

// HandlePatchVirtualMachine — write scope. PATCH /v1/virtual-machines/{id}.
func HandlePatchVirtualMachine(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeWrite) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "write scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var req vmPatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		patch := VirtualMachinePatch{
			DisplayName: req.DisplayName,
			Role:        req.Role,
			Owner:       req.Owner,
			Criticality: req.Criticality,
			Notes:       req.Notes,
			RunbookURL:  req.RunbookURL,
			Annotations: req.Annotations,
		}
		if req.Applications != nil {
			diffed, problem := diffVMApplications(r.Context(), store, id, *req.Applications, caller)
			if problem != "" {
				writeProblem(w, http.StatusBadRequest, "Bad Request", problem)
				return
			}
			patch.Applications = &diffed
		}
		vm, err := store.UpdateVirtualMachine(r.Context(), id, patch)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("patch virtual machine", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, vm)
	}
}

// diffVMApplications validates the input list, normalizes product names,
// reads the existing list from the store, and preserves added_at / added_by
// for entries whose (product, version, name) key matches existing rows.
// New entries are stamped with the current time and the caller's identifier.
//
// Returns (final list, "") on success, or ([], problem-detail) on validation
// failure. Caller may be nil (defense in depth — the handler already gated
// on caller != nil).
//
//nolint:gocyclo // per-entry validation branches are flat and intentional
func diffVMApplications(
	ctx context.Context,
	store Store,
	vmID uuid.UUID,
	input []VMApplication,
	caller *auth.Caller,
) (apps []VMApplication, problem string) {
	if len(input) > vmAppsMaxEntries {
		return nil, "applications: too many entries (max 100)"
	}
	// Operate on a copy so the caller's request struct stays clean.
	work := make([]VMApplication, len(input))
	copy(work, input)
	for i := range work {
		work[i].Product = NormalizeProductName(work[i].Product)
		work[i].Version = strings.TrimSpace(work[i].Version)
		if work[i].Product == "" {
			return nil, "applications: empty product"
		}
		if work[i].Version == "" {
			return nil, "applications: empty version"
		}
		if len(work[i].Product) > vmAppProductMaxLen {
			return nil, "applications: product too long"
		}
		if len(work[i].Version) > vmAppVersionMaxLen {
			return nil, "applications: version too long"
		}
		if work[i].Name != nil && len(*work[i].Name) > vmAppNameMaxLen {
			return nil, "applications: name too long"
		}
		if work[i].Notes != nil && len(*work[i].Notes) > vmAppNotesMaxLen {
			return nil, "applications: notes too long"
		}
	}

	existing, err := store.GetVirtualMachine(ctx, vmID)
	if err != nil {
		// Surface ErrNotFound up to the handler so it returns 404; for
		// any other store error, we treat it as a missing baseline and
		// stamp every input as new — the surrounding UpdateVirtualMachine
		// call will surface the underlying error.
		if !errors.Is(err, ErrNotFound) {
			existing = VirtualMachine{}
		}
	}
	existingByKey := make(map[string]VMApplication, len(existing.Applications))
	for i := range existing.Applications {
		existingByKey[VMApplicationKey(&existing.Applications[i])] = existing.Applications[i]
	}

	now := timeNow().UTC()
	addedBy := callerIdentifier(caller)
	out := make([]VMApplication, 0, len(work))
	for i := range work {
		entry := work[i]
		if prev, ok := existingByKey[VMApplicationKey(&entry)]; ok {
			entry.AddedAt = prev.AddedAt
			entry.AddedBy = prev.AddedBy
		} else {
			entry.AddedAt = now
			entry.AddedBy = addedBy
		}
		out = append(out, entry)
	}
	return out, ""
}

// callerIdentifier returns the audit-friendly identifier for the caller.
// Mirrors the audit middleware's actor logic (audit.go).
func callerIdentifier(caller *auth.Caller) string {
	if caller == nil {
		return ""
	}
	switch caller.Kind {
	case auth.CallerKindUser:
		return caller.Username
	case auth.CallerKindToken:
		if caller.TokenName != "" {
			return "token:" + caller.TokenName
		}
		return "token"
	default:
		return ""
	}
}

// HandleListDistinctVMApplications — read scope. GET
// /v1/virtual-machines/applications/distinct. Used by the UI to populate
// the application-filter autocomplete (ADR-0019 §3).
func HandleListDistinctVMApplications(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		products, err := store.ListDistinctVMApplications(r.Context())
		if err != nil {
			slog.Error("list distinct vm applications", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"products": products})
	}
}

// HandleDeleteVirtualMachine — delete scope. DELETE /v1/virtual-machines/{id}.
func HandleDeleteVirtualMachine(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeDelete) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "delete scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeleteVirtualMachine(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("delete virtual machine", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleReconcileVirtualMachines — vm-collector scope.
// POST /v1/virtual-machines/reconcile.
func HandleReconcileVirtualMachines(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		var req vmReconcileReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if req.CloudAccountID == uuid.Nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "cloud_account_id required")
			return
		}
		if err := caller.EnforceCloudAccountBinding(req.CloudAccountID); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token not bound to this cloud account")
			return
		}
		n, err := store.ReconcileVirtualMachines(r.Context(), req.CloudAccountID, req.KeepProviderVMIDs)
		if err != nil {
			slog.Error("reconcile virtual machines", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tombstoned": n})
	}
}
