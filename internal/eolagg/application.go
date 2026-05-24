package eolagg

// Per-application EOL aggregation (ADR-0029 §5). Read-time only — no
// enricher pass, no background job, no extra DB writes. The aggregator
// walks an application's three member sources and rolls their EOL signal
// up into one row per product, each row carrying the list of member
// assets that contribute it.
//
// The three sources, per ADR-0029 §5:
//
//  1. Linked workloads — workloads carry NO `longue-vue.io/eol.*`
//     annotations (the EOL enricher only writes clusters / nodes / VMs).
//     Their EOL signal is the per-container image-versions enrichment
//     (ADR-0022): a `containers_versions` map of container name →
//     {latest_tag, is_behind}. We surface one row per container that has
//     been enriched, product = the container name, eol_status derived
//     from is_behind ("outdated" when behind, "unknown" otherwise).
//  2. Linked VMs — their `annotations` JSONB carries one
//     `longue-vue.io/eol.<product>` entry per VM-application (ADR-0019).
//  3. VM-application entries whose per-entry application_id matches THIS
//     application, even when the parent VM as a whole is not linked. The
//     matching product's annotation is read transitively from the parent
//     VM's annotations.
//
// This package stays free of an internal/api dependency (internal/api
// imports eolagg for the extract path; the reverse would cycle). The
// member input shapes below are eolagg-local; the api layer adapts its
// Store to ApplicationStore.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// ApplicationEOLSource names one member asset that contributes an EOL row.
type ApplicationEOLSource struct {
	Kind string `json:"kind"` // workload | virtual_machine | vm_application
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ApplicationEOLRow is one (product) tuple rolled up across every member
// that exposes EOL signal for it. Sources lists the contributing assets.
type ApplicationEOLRow struct {
	Product         string                 `json:"product"`
	Cycle           string                 `json:"cycle"`
	EOLStatus       string                 `json:"eol_status"`
	LatestAvailable string                 `json:"latest_available,omitempty"`
	EvaluatedAt     string                 `json:"evaluated_at,omitempty"`
	Sources         []ApplicationEOLSource `json:"sources"`
}

// WorkloadMember is a workload linked to the application, carrying its
// per-container image-versions enrichment (ADR-0022).
type WorkloadMember struct {
	ID                 string
	Name               string
	ContainersVersions map[string]ContainerVersion
}

// ContainerVersion mirrors the relevant subset of api.ContainerVersionInfo
// (the image-versions enrichment shape). Duplicated to keep eolagg free of
// an internal/api dependency.
type ContainerVersion struct {
	LatestTag     string
	IsBehind      bool
	LastCheckedAt string
}

// VMMember is a VM whose row-level application_id links it to the
// application. Annotations carries the EOL enricher's output.
type VMMember struct {
	ID          string
	Name        string
	Annotations map[string]string
}

// VMAppEntryMember is a single VM-application JSONB entry whose per-entry
// application_id matches the application (independent of the parent VM's
// row-level link). Product is the normalized product key; Annotations is
// the parent VM's full annotation map (we look up
// longue-vue.io/eol.<product> within it).
type VMAppEntryMember struct {
	VMID        string
	VMName      string
	Product     string
	Annotations map[string]string
}

// ApplicationStore is the narrow read surface the aggregator needs. The
// api.Store does not satisfy it directly (it speaks api types); the api
// layer wires a thin adapter. Each method returns the members of one
// source for a single application. Implementations paginate internally —
// the aggregator consumes the full slice.
type ApplicationStore interface {
	ApplicationWorkloadMembers(ctx context.Context, appID uuid.UUID) ([]WorkloadMember, error)
	ApplicationVMMembers(ctx context.Context, appID uuid.UUID) ([]VMMember, error)
	ApplicationVMAppEntryMembers(ctx context.Context, appID uuid.UUID) ([]VMAppEntryMember, error)
}

// productAccumulator collects sources + the best-known annotation fields
// for a single product key as we walk the three sources.
type productAccumulator struct {
	row     ApplicationEOLRow
	sources []ApplicationEOLSource
	// seen dedups identical (kind,id) source tuples so the same asset
	// listed twice (e.g. two containers of the same product on one
	// workload) contributes one chip.
	seen map[string]struct{}
}

// AggregateForApplication walks the application's members across the three
// sources and returns one row per product, sorted by product name. The
// query cost is three paginated member lists (one per source); no N+1
// beyond that. Read-time only.
func AggregateForApplication(ctx context.Context, store ApplicationStore, appID uuid.UUID) ([]ApplicationEOLRow, error) {
	acc := make(map[string]*productAccumulator)

	workloads, err := store.ApplicationWorkloadMembers(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("application workload members: %w", err)
	}
	for i := range workloads {
		addWorkload(acc, &workloads[i])
	}

	vms, err := store.ApplicationVMMembers(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("application vm members: %w", err)
	}
	for i := range vms {
		addVM(acc, &vms[i])
	}

	entries, err := store.ApplicationVMAppEntryMembers(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("application vm-app entry members: %w", err)
	}
	for i := range entries {
		addVMAppEntry(acc, &entries[i])
	}

	return finalize(acc), nil
}

// addWorkload folds a workload's per-container image-versions enrichment
// into the accumulator. One product per enriched container; the container
// name is the product key (workloads carry no endoflife.date cycle data).
func addWorkload(acc map[string]*productAccumulator, w *WorkloadMember) {
	for container, cv := range w.ContainersVersions {
		// Only surface containers that were actually enriched. An empty
		// latest_tag means "not yet processed / non-parseable tag / not
		// allowlisted" — no signal to show.
		if cv.LatestTag == "" {
			continue
		}
		product := container
		a := annotation{
			Product:         product,
			EOLStatus:       "unknown",
			LatestAvailable: cv.LatestTag,
			CheckedAt:       cv.LastCheckedAt,
		}
		if cv.IsBehind {
			a.EOLStatus = "outdated"
		}
		upsert(acc, product, &a, ApplicationEOLSource{
			Kind: "workload",
			ID:   w.ID,
			Name: w.Name,
		})
	}
}

// addVM folds a linked VM's longue-vue.io/eol.* annotations into the
// accumulator. One product per annotation key.
func addVM(acc map[string]*productAccumulator, vm *VMMember) {
	parsed := parseEOLAnnotations(vm.Annotations)
	for product := range parsed {
		a := parsed[product]
		upsert(acc, product, &a, ApplicationEOLSource{
			Kind: "virtual_machine",
			ID:   vm.ID,
			Name: vm.Name,
		})
	}
}

// addVMAppEntry folds the matching product's annotation from a VM-app
// entry into the accumulator. The annotation is read from the parent VM's
// annotation map under longue-vue.io/eol.<product>; a missing or malformed
// annotation is skipped gracefully.
func addVMAppEntry(acc map[string]*productAccumulator, e *VMAppEntryMember) {
	product := e.Product
	if product == "" {
		return
	}
	raw, ok := e.Annotations[eolPrefix+product]
	if !ok {
		return
	}
	var a annotation
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return
	}
	upsert(acc, product, &a, ApplicationEOLSource{
		Kind: "vm_application",
		ID:   e.VMID + "#" + product,
		Name: e.VMName + " / " + product,
	})
}

// parseEOLAnnotations extracts every longue-vue.io/eol.<product> entry from
// an annotation map, keyed by the product suffix. Malformed values are
// skipped (mirrors Flatten / the UI dashboard).
func parseEOLAnnotations(annotations map[string]string) map[string]annotation {
	out := make(map[string]annotation, len(annotations))
	for k, v := range annotations {
		if !strings.HasPrefix(k, eolPrefix) {
			continue
		}
		var a annotation
		if err := json.Unmarshal([]byte(v), &a); err != nil {
			continue
		}
		out[strings.TrimPrefix(k, eolPrefix)] = a
	}
	return out
}

// upsert merges one (product, annotation, source) contribution into the
// accumulator. The first non-empty annotation field wins for the row's
// scalar fields (cycle / status / latest / evaluated_at) — every source
// for a given product carries the same enricher output in practice, so
// first-wins is stable; the source is always appended (deduped).
func upsert(acc map[string]*productAccumulator, product string, a *annotation, src ApplicationEOLSource) {
	pa, ok := acc[product]
	if !ok {
		pa = &productAccumulator{
			row:  ApplicationEOLRow{Product: product},
			seen: make(map[string]struct{}),
		}
		acc[product] = pa
	}
	if pa.row.Cycle == "" {
		pa.row.Cycle = a.Cycle
	}
	// Status precedence: a stronger signal wins regardless of source
	// order. endoflife.date verdicts (eol / approaching_eol / supported)
	// from VM annotations outrank the workload image-versions signal
	// (outdated), which outranks the unknown sentinel.
	if statusRank(a.EOLStatus) > statusRank(pa.row.EOLStatus) {
		pa.row.EOLStatus = a.EOLStatus
	}
	if pa.row.LatestAvailable == "" {
		pa.row.LatestAvailable = a.LatestAvailable
	}
	if pa.row.EvaluatedAt == "" {
		pa.row.EvaluatedAt = a.CheckedAt
	}
	dedup := src.Kind + "\x00" + src.ID
	if _, dup := pa.seen[dedup]; !dup {
		pa.seen[dedup] = struct{}{}
		pa.sources = append(pa.sources, src)
	}
}

// statusRank orders EOL statuses by signal strength so the strongest
// verdict wins when a product is contributed by multiple sources,
// independent of walk order. endoflife.date verdicts (from VM
// annotations) outrank the workload image-versions signal, which outranks
// the unknown sentinel / unset.
func statusRank(status string) int {
	switch status {
	case "eol":
		return 5
	case "approaching_eol":
		return 4
	case "supported":
		return 3
	case "outdated":
		return 2
	case "unknown":
		return 1
	default: // "" (unset) or any future value below the known floor
		return 0
	}
}

// finalize sorts the accumulated products and their sources into the
// stable output order: rows by product, sources by (kind, name, id).
func finalize(acc map[string]*productAccumulator) []ApplicationEOLRow {
	out := make([]ApplicationEOLRow, 0, len(acc))
	for _, pa := range acc {
		if pa.row.EOLStatus == "" {
			pa.row.EOLStatus = "unknown"
		}
		sources := pa.sources
		sort.Slice(sources, func(i, j int) bool {
			if sources[i].Kind != sources[j].Kind {
				return sources[i].Kind < sources[j].Kind
			}
			if sources[i].Name != sources[j].Name {
				return sources[i].Name < sources[j].Name
			}
			return sources[i].ID < sources[j].ID
		})
		pa.row.Sources = sources
		out = append(out, pa.row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Product < out[j].Product })
	return out
}
