package api

// Hand-written HTTP handlers for the virtual_machines endpoints (ADR-0015).
// Mounted on the main mux next to the cloud-accounts handlers.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

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
type vmPatchReq struct {
	DisplayName *string            `json:"display_name,omitempty"`
	Role        *string            `json:"role,omitempty"`
	Owner       *string            `json:"owner,omitempty"`
	Criticality *string            `json:"criticality,omitempty"`
	Notes       *string            `json:"notes,omitempty"`
	RunbookURL  *string            `json:"runbook_url,omitempty"`
	Annotations *map[string]string `json:"annotations,omitempty"`
}

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

// HandleListVirtualMachines — read scope. GET /v1/virtual-machines.
//
//nolint:gocyclo // filter parsing is repetitive but flat
func HandleListVirtualMachines(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}
		filter := VirtualMachineListFilter{}
		if v := r.URL.Query().Get("cloud_account_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid cloud_account_id")
				return
			}
			filter.CloudAccountID = &id
		}
		if v := r.URL.Query().Get("region"); v != "" {
			filter.Region = &v
		}
		if v := r.URL.Query().Get("role"); v != "" {
			filter.Role = &v
		}
		if v := r.URL.Query().Get("power_state"); v != "" {
			filter.PowerState = &v
		}
		if v := r.URL.Query().Get("include_terminated"); v != "" {
			b, _ := strconv.ParseBool(v)
			filter.IncludeTerminated = b
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
		vm, err := store.UpdateVirtualMachine(r.Context(), id, VirtualMachinePatch{
			DisplayName: req.DisplayName,
			Role:        req.Role,
			Owner:       req.Owner,
			Criticality: req.Criticality,
			Notes:       req.Notes,
			RunbookURL:  req.RunbookURL,
			Annotations: req.Annotations,
		})
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
