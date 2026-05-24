package api

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// decorateWorkloadDICT sets EffectiveDict on a single workload (ADR-0029 §6).
//
// Workloads carry no DICT columns of their own (the Workload/Namespace DICT
// migration described in ADR-0008 §8.3 / ADR-0029 was never created), so the
// fallback axes are all-nil: a workload's effective DICT is either the linked
// application's or "none". The helper is written against the general
// ComputeEffectiveDICT signature so wiring in workload-level columns later is
// a one-line change.
//
// A store read failure is logged and the field is left as source="none"
// rather than failing the whole response — effective_dict is advisory
// metadata, not a correctness-critical field.
func decorateWorkloadDICT(ctx context.Context, store Store, wl Workload) Workload { //nolint:gocritic // value copy for immutable decoration
	var app *Application
	if wl.ApplicationId != nil {
		got, err := store.GetApplication(ctx, *wl.ApplicationId)
		if err == nil {
			app = &got
		} else {
			slog.Warn("effective_dict: fetch linked application failed",
				slog.String("application_id", wl.ApplicationId.String()), slog.Any("error", err))
		}
	}
	eff := ComputeEffectiveDICT(nil, nil, nil, nil, nil, DICTSourceWorkload, app)
	wl.EffectiveDict = &eff
	return wl
}

// decorateWorkloadsDICT sets EffectiveDict on a slice of workloads in place,
// bulk-fetching the distinct linked applications in a single query to avoid
// an N+1 (ADR-0029 §6). A bulk-fetch failure leaves every row at
// source="none" rather than failing the list.
func decorateWorkloadsDICT(ctx context.Context, store Store, items []Workload) {
	apps := bulkFetchLinkedApplications(ctx, store, collectWorkloadAppIDs(items))
	for i := range items {
		var app *Application
		if items[i].ApplicationId != nil {
			if got, ok := apps[*items[i].ApplicationId]; ok {
				app = &got
			}
		}
		eff := ComputeEffectiveDICT(nil, nil, nil, nil, nil, DICTSourceWorkload, app)
		items[i].EffectiveDict = &eff
	}
}

// decorateVMDICT sets EffectiveDict on a single VM (ADR-0029 §6). VMs have no
// DICT columns of their own, so the result is application-or-none.
func decorateVMDICT(ctx context.Context, store Store, vm *VirtualMachine) {
	var app *Application
	if vm.ApplicationID != nil {
		got, err := store.GetApplication(ctx, *vm.ApplicationID)
		if err == nil {
			app = &got
		} else {
			slog.Warn("effective_dict: fetch linked application failed",
				slog.String("application_id", vm.ApplicationID.String()), slog.Any("error", err))
		}
	}
	eff := ComputeEffectiveDICT(nil, nil, nil, nil, nil, DICTSourceWorkload, app)
	vm.EffectiveDict = &eff
}

// decorateVMsDICT sets EffectiveDict on a slice of VMs in place, bulk-fetching
// the distinct linked applications in a single query (ADR-0029 §6).
func decorateVMsDICT(ctx context.Context, store Store, items []VirtualMachine) {
	ids := make([]uuid.UUID, 0, len(items))
	seen := make(map[uuid.UUID]struct{}, len(items))
	for i := range items {
		if items[i].ApplicationID == nil {
			continue
		}
		if _, dup := seen[*items[i].ApplicationID]; dup {
			continue
		}
		seen[*items[i].ApplicationID] = struct{}{}
		ids = append(ids, *items[i].ApplicationID)
	}
	apps := bulkFetchLinkedApplications(ctx, store, ids)
	for i := range items {
		var app *Application
		if items[i].ApplicationID != nil {
			if got, ok := apps[*items[i].ApplicationID]; ok {
				app = &got
			}
		}
		eff := ComputeEffectiveDICT(nil, nil, nil, nil, nil, DICTSourceWorkload, app)
		items[i].EffectiveDict = &eff
	}
}

// collectWorkloadAppIDs returns the distinct non-nil application_id values
// across the workloads.
func collectWorkloadAppIDs(items []Workload) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(items))
	seen := make(map[uuid.UUID]struct{}, len(items))
	for i := range items {
		if items[i].ApplicationId == nil {
			continue
		}
		if _, dup := seen[*items[i].ApplicationId]; dup {
			continue
		}
		seen[*items[i].ApplicationId] = struct{}{}
		ids = append(ids, *items[i].ApplicationId)
	}
	return ids
}

// bulkFetchLinkedApplications wraps GetApplicationsByIDs, logging and
// swallowing errors so DICT decoration never fails a list response.
func bulkFetchLinkedApplications(ctx context.Context, store Store, ids []uuid.UUID) map[uuid.UUID]Application {
	if len(ids) == 0 {
		return nil
	}
	apps, err := store.GetApplicationsByIDs(ctx, ids)
	if err != nil {
		slog.Warn("effective_dict: bulk-fetch linked applications failed", slog.Any("error", err))
		return nil
	}
	return apps
}
