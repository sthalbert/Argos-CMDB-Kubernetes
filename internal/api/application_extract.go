package api

// Applications bulk-extract endpoints (ADR-0029 §2.1). Mirrors the
// Search + EOL extract pattern (extract_handlers.go): read scope, the
// same row cap (LONGUE_VUE_EXTRACT_MAX_ROWS), the X-Longue-Vue-Truncated
// header, audit-logging via the shouldAudit allowlist, and the shared
// CSV / JSON writer helpers.
//
// The CSV shape flattens the DICT axes + member counts into columns so
// the file is a self-contained SecNumCloud evidence artefact. The JSON
// shape emits the full Application objects (mirrors how the EOL extract
// emits whole rows rather than a separately-defined export struct).

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

// ApplicationExtractStore is the narrow slice of Store the application
// extract handlers consume. Kept separate from ExtractStore because the
// applications extract only needs the one paginated list call.
type ApplicationExtractStore interface {
	ListApplications(ctx context.Context, filter ApplicationListFilter, limit int, cursor string) ([]Application, string, error)
}

// HandleExtractApplicationsCSV — read scope.
// GET /v1/applications/extract.csv. Accepts the same filters as
// GET /v1/applications.
func HandleExtractApplicationsCSV(store ApplicationExtractStore, maxRows int) http.HandlerFunc {
	return handleApplicationsExtract(store, maxRows, extractFormatCSV)
}

// HandleExtractApplicationsJSON — read scope.
// GET /v1/applications/extract.json. Accepts the same filters as
// GET /v1/applications.
func HandleExtractApplicationsJSON(store ApplicationExtractStore, maxRows int) http.HandlerFunc {
	return handleApplicationsExtract(store, maxRows, extractFormatJSON)
}

// handleApplicationsExtract is the shared body of the CSV + JSON extract
// handlers — the only difference between the two routes is the emit
// format, fixed at registration time so the path itself (extract.csv vs
// extract.json) is the format selector (no ?format= param needed).
func handleApplicationsExtract(store ApplicationExtractStore, maxRows int, format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeRead) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "read scope required")
			return
		}

		filter, err := parseApplicationListFilter(r.URL.Query())
		if err != nil {
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "applications", "format": format, "outcome": "denied",
			})
			metrics.ObserveExtract("applications", format, "denied", 0)
			writeProblem(w, http.StatusBadRequest, "Bad Request", filterErrMessage(err))
			return
		}

		apps, err := collectAllApplications(r.Context(), store, filter)
		if err != nil {
			slog.Error("extract: list applications", slog.Any("error", err))
			SetAuditDetails(r.Context(), map[string]any{
				"action": "extract", "page": "applications", "format": format, "outcome": "error",
			})
			metrics.ObserveExtract("applications", format, "error", 0)
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		truncated := false
		if len(apps) > maxRows {
			apps = apps[:maxRows]
			truncated = true
		}

		var buf bytes.Buffer
		if err := emitApplicationsExtract(&buf, format, apps); err != nil {
			slog.Error("extract: applications emit", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		outcome := "ok"
		if truncated {
			outcome = extractOutcomeTruncated
			w.Header().Set("X-Longue-Vue-Truncated", "true")
		}
		filename := fmt.Sprintf("longue-vue-applications-%s.%s", extractTimestamp(time.Now()), format)
		w.Header().Set("Content-Type", extractContentType(format))
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())

		SetAuditDetails(r.Context(), map[string]any{
			"action":    "extract",
			"page":      "applications",
			"format":    format,
			"filters":   applicationFilterAudit(filter),
			"row_count": len(apps),
			"truncated": truncated,
			"outcome":   outcome,
		})
		metrics.ObserveExtract("applications", format, outcome, len(apps))
	}
}

// emitApplicationsExtract writes the buffered CSV or JSON body. CSV
// flattens the DICT axes + member counts into columns; JSON emits the
// full Application objects.
func emitApplicationsExtract(buf *bytes.Buffer, format string, apps []Application) error {
	switch format {
	case extractFormatCSV:
		cw := newExtractCSVWriter(buf, applicationCSVHeader())
		for i := range apps {
			if err := cw.WriteRow(applicationRowToCSV(&apps[i])); err != nil {
				return err
			}
		}
		return cw.Close()
	case extractFormatJSON:
		jw := newExtractJSONWriter(buf)
		for i := range apps {
			if err := jw.WriteRow(apps[i]); err != nil {
				return err
			}
		}
		return jw.Close()
	}
	return nil
}

// collectAllApplications paginates ListApplications with the given filter
// and returns the full result set (page size shared with the other
// extracts).
func collectAllApplications(
	ctx context.Context,
	store ApplicationExtractStore,
	filter ApplicationListFilter,
) ([]Application, error) {
	var out []Application
	cursor := ""
	for {
		items, next, err := store.ListApplications(ctx, filter, extractPageSize, cursor)
		if err != nil {
			return nil, fmt.Errorf("listApplications: %w", err)
		}
		out = append(out, items...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

func applicationCSVHeader() []string {
	return []string{
		"id", "name", "display_name", "application_block", "owner", "criticality",
		"sec_disponibilite", "sec_integrite", "sec_confidentialite", "sec_tracabilite", "sec_notes",
		"workloads", "virtual_machines", "vm_applications", "created_at", "updated_at",
	}
}

func applicationRowToCSV(a *Application) []string {
	return []string{
		a.ID.String(),
		a.Name,
		derefStr(a.DisplayName),
		derefStr(a.ApplicationBlockName),
		derefStr(a.Owner),
		derefStr(a.Criticality),
		derefIntStr(a.SecDisponibilite),
		derefIntStr(a.SecIntegrite),
		derefIntStr(a.SecConfidentialite),
		derefIntStr(a.SecTracabilite),
		derefStr(a.SecNotes),
		fmt.Sprintf("%d", a.MemberCounts.Workloads),
		fmt.Sprintf("%d", a.MemberCounts.VirtualMachines),
		fmt.Sprintf("%d", a.MemberCounts.VMApplications),
		a.CreatedAt.UTC().Format(time.RFC3339),
		a.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// applicationFilterAudit renders the active filters as a compact map for
// the audit row's details.filters field — only set keys are included so
// the row records what the operator actually narrowed by.
func applicationFilterAudit(f ApplicationListFilter) map[string]any {
	out := map[string]any{}
	if f.Name != nil {
		out["name"] = *f.Name
	}
	if f.ApplicationBlockID != nil {
		out["application_block_id"] = f.ApplicationBlockID.String()
	}
	if f.ApplicationBlockName != nil {
		out["application_block_name"] = *f.ApplicationBlockName
	}
	if f.Criticality != nil {
		out["criticality"] = *f.Criticality
	}
	if f.HasDICT != nil {
		out["has_dict"] = *f.HasDICT
	}
	if f.DICTMin != nil {
		out["dict_min"] = *f.DICTMin
	}
	return out
}
