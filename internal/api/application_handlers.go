package api

// Hand-written HTTP handlers for /v1/applications (ADR-0029 §2.3).
// First-class Application entity: operator-curated, two-level group above
// the curated annotations carried on workloads + VMs (the SNC ch.8 model).
//
// Scope matrix (mirrors the ADR table):
//   POST   /v1/applications                  → write
//   GET    /v1/applications                  → read
//   GET    /v1/applications/{id}             → read
//   GET    /v1/applications/by-name/{name}   → read
//   PATCH  /v1/applications/{id}             → write
//   DELETE /v1/applications/{id}             → admin
//   GET    /v1/applications/{id}/members     → read
//   GET    /v1/applications/{id}/eol         → read (501 stub until Phase 5)
//
// POST is idempotent on `name` (200 on hit, 201 on insert) — mirrors
// ApplicationBlock + EnsureCluster. DICT validation runs BEFORE the store
// call so a 400 takes precedence over a 409 on duplicate-with-bad-DICT.
//
// requireScope / parseLimit live in application_block_handlers.go (just
// landed in Task 1.8) — reused here to avoid duplicate declarations.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/eolagg"
)

// Static sentinels for the DICT-shape validators. Required by err113 —
// gives callers a stable error identity, and makes the per-axis message
// a single-line wrap rather than a dynamic fmt.Errorf format string.
var (
	errDICTAxisOutOfRange  = errors.New("must be between 0 and 4")
	errDICTSecNotesTooLong = errors.New("sec_notes too long (max 4096 chars)")
)

// validateDICTAxis enforces the SecNumCloud 0..4 range on a single DICT
// axis. nil means "leave alone" on patch / "unset" on create.
func validateDICTAxis(name string, v *int) error {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > 4 {
		return fmt.Errorf("%s %w", name, errDICTAxisOutOfRange)
	}
	return nil
}

// validateDICTOnCreate runs the five DICT-shape checks for a Create body.
// Returned as a single error so the handler can write one 400 problem.
func validateDICTOnCreate(in *ApplicationCreate) error {
	if err := validateDICTAxis("sec_disponibilite", in.SecDisponibilite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_integrite", in.SecIntegrite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_confidentialite", in.SecConfidentialite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_tracabilite", in.SecTracabilite); err != nil {
		return err
	}
	if in.SecNotes != nil && len(*in.SecNotes) > 4096 {
		return errDICTSecNotesTooLong
	}
	return nil
}

// validateDICTOnPatch mirrors validateDICTOnCreate against the patch
// shape. Same five rules — only the receiving struct differs.
func validateDICTOnPatch(in *ApplicationPatch) error {
	if err := validateDICTAxis("sec_disponibilite", in.SecDisponibilite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_integrite", in.SecIntegrite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_confidentialite", in.SecConfidentialite); err != nil {
		return err
	}
	if err := validateDICTAxis("sec_tracabilite", in.SecTracabilite); err != nil {
		return err
	}
	if in.SecNotes != nil && len(*in.SecNotes) > 4096 {
		return errDICTSecNotesTooLong
	}
	return nil
}

// HandleCreateApplication — write scope. POST /v1/applications.
// Idempotent on name: a duplicate returns 200 with the existing row;
// first insert returns 201. DICT validation runs BEFORE the store call
// so an invalid axis surfaces as 400, not a server-side 5xx.
func HandleCreateApplication(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		var in ApplicationCreate
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if NormalizeApplicationName(in.Name) == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		if err := validateDICTOnCreate(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
			return
		}
		app, err := store.CreateApplication(r.Context(), in)
		if err == nil {
			writeJSON(w, http.StatusCreated, app)
			return
		}
		if errors.Is(err, ErrConflict) {
			existing, getErr := store.GetApplicationByName(r.Context(), in.Name)
			if getErr == nil {
				writeJSON(w, http.StatusOK, existing)
				return
			}
			slog.Error("application conflict lookup",
				slog.Any("error", getErr), slog.String("name", in.Name))
			writeProblem(w, http.StatusConflict, "Conflict", err.Error())
			return
		}
		slog.Error("create application", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
	}
}

// HandleListApplications — read scope. GET /v1/applications. Filter
// params: name (substring), application_block_id (UUID), application_block_name,
// criticality, has_dict (bool), dict_min (int 0..4), plus cursor + limit.
//
//nolint:gocyclo // five optional filters + cursor; each branch is trivial
func HandleListApplications(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		q := r.URL.Query()
		limit := parseLimit(q.Get("limit"), 50)
		cursor := q.Get("cursor")

		var filter ApplicationListFilter
		if v := q.Get("name"); v != "" {
			filter.Name = &v
		}
		if v := q.Get("application_block_id"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid application_block_id")
				return
			}
			filter.ApplicationBlockID = &id
		}
		if v := q.Get("application_block_name"); v != "" {
			filter.ApplicationBlockName = &v
		}
		if v := q.Get("criticality"); v != "" {
			filter.Criticality = &v
		}
		if v := q.Get("has_dict"); v != "" {
			switch v {
			case "true", "1":
				b := true
				filter.HasDICT = &b
			case "false", "0":
				b := false
				filter.HasDICT = &b
			default:
				writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid has_dict (use true/false/1/0)")
				return
			}
		}
		if v := q.Get("dict_min"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 4 {
				writeProblem(w, http.StatusBadRequest, "Bad Request", "dict_min must be an integer 0..4")
				return
			}
			filter.DICTMin = &n
		}

		items, next, err := store.ListApplications(r.Context(), filter, limit, cursor)
		if err != nil {
			slog.Error("list applications", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	}
}

// HandleGetApplication — read scope. GET /v1/applications/{id}.
func HandleGetApplication(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		app, err := store.GetApplication(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get application", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, app)
	}
}

// HandleGetApplicationByName — read scope. GET /v1/applications/by-name/{name}.
// The handler normalises the path value (NormalizeApplicationName ⇒
// kebab-case) so operator-typed "HashiCorp Vault" matches the stored
// "hashicorp-vault" row. The store applies the same normalisation, so
// the explicit step here is defence-in-depth.
func HandleGetApplicationByName(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		raw := r.PathValue("name")
		if raw == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "missing path parameter name")
			return
		}
		name := NormalizeApplicationName(raw)
		if name == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		app, err := store.GetApplicationByName(r.Context(), name)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get application by name", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, app)
	}
}

// HandlePatchApplication — write scope. PATCH /v1/applications/{id}.
// Merge-patch on the mutable fields (name intentionally NOT mutable here
// — renaming would orphan inbound links). DICT validation runs BEFORE
// the store call.
func HandlePatchApplication(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeWrite) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var in ApplicationPatch
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if err := validateDICTOnPatch(&in); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", err.Error())
			return
		}
		app, err := store.UpdateApplication(r.Context(), id, in)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("patch application", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, app)
	}
}

// HandleDeleteApplication — admin scope. DELETE /v1/applications/{id}.
// Migration 00047 declares ON DELETE SET NULL on
// workloads.application_id and virtual_machines.application_id, so
// dependent rows survive (curated linkage is cleared). The store
// additionally sweeps VM-app JSONB entries inside the same transaction.
func HandleDeleteApplication(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeleteApplication(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("delete application", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleListApplicationMembers — read scope. GET /v1/applications/{id}/members.
// Returns the three-source walk (workloads + VMs + VM-app JSONB entries)
// in stable order. Pagination is opaque-cursor (mirrors ListApplications).
func HandleListApplicationMembers(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		q := r.URL.Query()
		limit := parseLimit(q.Get("limit"), 100)
		cursor := q.Get("cursor")

		members, next, err := store.ListApplicationMembers(r.Context(), id, limit, cursor)
		if err != nil {
			slog.Error("list application members", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       members,
			"next_cursor": next,
		})
	}
}

// HandleGetApplicationEOL — read scope. GET /v1/applications/{id}/eol.
// Confirms the application exists (404 if not), then read-time aggregates
// the EOL signal across its three member sources (workloads' image-versions
// enrichment, linked VMs' annotations, and VM-application JSONB entries
// whose per-entry application_id matches — even when the parent VM is not
// itself linked). No enricher pass, no DB writes (ADR-0029 §5).
func HandleGetApplicationEOL(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireScope(w, r, auth.ScopeRead) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if _, err := store.GetApplication(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get application (eol)", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		rows, err := eolagg.AggregateForApplication(r.Context(), eolAppStore{store}, id)
		if err != nil {
			slog.Error("aggregate application eol", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows})
	}
}

// eolAppStore adapts api.Store to eolagg.ApplicationStore. The eolagg
// package is intentionally free of an internal/api dependency (api imports
// eolagg for the extract path; the reverse would cycle), so the adaptation
// — paginating the member lists and mapping api types to eolagg's local
// input shapes — lives here.
type eolAppStore struct{ s Store }

// eolMemberPageLimit bounds each member-list page. ADR-0029 §5/NEG-006:
// expected fleet sizes are low hundreds of members per application; we
// page at 200 and walk the cursor to completion.
const eolMemberPageLimit = 200

// ApplicationWorkloadMembers walks every workload linked to the
// application and maps each one's container image-versions enrichment
// (ADR-0022) into eolagg's WorkloadMember shape. Workloads carry no
// longue-vue.io/eol.* annotations — image-versions is their only EOL
// signal.
func (e eolAppStore) ApplicationWorkloadMembers(ctx context.Context, appID uuid.UUID) ([]eolagg.WorkloadMember, error) {
	out := make([]eolagg.WorkloadMember, 0, eolMemberPageLimit)
	filter := WorkloadListFilter{ApplicationID: &appID}
	cursor := ""
	for {
		items, next, err := e.s.ListWorkloads(ctx, filter, eolMemberPageLimit, cursor)
		if err != nil {
			return nil, fmt.Errorf("list workload members: %w", err)
		}
		for i := range items {
			out = append(out, toWorkloadMember(&items[i]))
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

// ApplicationVMMembers walks every VM whose row-level application_id links
// it to the application and maps its annotations into eolagg's VMMember.
func (e eolAppStore) ApplicationVMMembers(ctx context.Context, appID uuid.UUID) ([]eolagg.VMMember, error) {
	out := make([]eolagg.VMMember, 0, eolMemberPageLimit)
	filter := VirtualMachineListFilter{ApplicationID: &appID}
	cursor := ""
	for {
		items, next, err := e.s.ListVirtualMachines(ctx, filter, eolMemberPageLimit, cursor)
		if err != nil {
			return nil, fmt.Errorf("list vm members: %w", err)
		}
		for i := range items {
			out = append(out, eolagg.VMMember{
				ID:          items[i].ID.String(),
				Name:        vmDisplayName(&items[i]),
				Annotations: items[i].Annotations,
			})
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return out, nil
}

// ApplicationVMAppEntryMembers returns one eolagg.VMAppEntryMember per
// applications[] JSONB entry whose per-entry application_id matches the
// application — independent of the parent VM's row-level link. A single VM
// hosting two products linked to this app yields two entries.
func (e eolAppStore) ApplicationVMAppEntryMembers(ctx context.Context, appID uuid.UUID) ([]eolagg.VMAppEntryMember, error) {
	vms, err := e.s.ListVMsWithApplicationEntry(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("list vms with application entry: %w", err)
	}
	out := make([]eolagg.VMAppEntryMember, 0, len(vms))
	for i := range vms {
		vm := &vms[i]
		name := vmDisplayName(vm)
		for j := range vm.Applications {
			entry := &vm.Applications[j]
			if entry.ApplicationID == nil || *entry.ApplicationID != appID {
				continue
			}
			out = append(out, eolagg.VMAppEntryMember{
				VMID:        vm.ID.String(),
				VMName:      name,
				Product:     NormalizeProductName(entry.Product),
				Annotations: vm.Annotations,
			})
		}
	}
	return out, nil
}

// toWorkloadMember maps an api.Workload's containers_versions map into the
// eolagg shape, keeping only the fields the aggregator reads.
func toWorkloadMember(w *Workload) eolagg.WorkloadMember {
	name := w.Name
	if w.NamespaceName != nil && *w.NamespaceName != "" {
		name = *w.NamespaceName + "/" + w.Name
	}
	m := eolagg.WorkloadMember{Name: name}
	if w.Id != nil {
		m.ID = w.Id.String()
	}
	if w.ContainersVersions != nil {
		m.ContainersVersions = make(map[string]eolagg.ContainerVersion, len(*w.ContainersVersions))
		for container, cv := range *w.ContainersVersions {
			ec := eolagg.ContainerVersion{}
			if cv.LatestTag != nil {
				ec.LatestTag = *cv.LatestTag
			}
			if cv.IsBehind != nil {
				ec.IsBehind = *cv.IsBehind
			}
			if cv.LastCheckedAt != nil {
				ec.LastCheckedAt = cv.LastCheckedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
			}
			m.ContainersVersions[container] = ec
		}
	}
	return m
}

// vmDisplayName prefers the operator-set display name, falling back to the
// provider name — mirrors the eolagg.displayOrName convention.
func vmDisplayName(vm *VirtualMachine) string {
	if vm.DisplayName != nil && *vm.DisplayName != "" {
		return *vm.DisplayName
	}
	return vm.Name
}
