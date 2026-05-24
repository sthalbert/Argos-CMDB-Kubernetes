package timetravel

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Capture writes a history row for the given entity inside an already-open
// transaction tx. The caller must commit or roll back tx; Capture does not
// manage transaction boundaries.
//
// Flow:
//  1. Check the time_travel_enabled flag; return nil immediately if off.
//  2. For "update": diff watched fields; return nil if nothing changed (noop).
//  3. Close the prior open row (valid_to IS NULL) for this entity.
//  4. Insert the new history row.
//
// For "create" no prior-row close is needed.
//
// kind must be one of KindCluster, KindNamespace, KindNode, KindWorkload.
// prev may be nil for "create" (no diff is computed). next must always be set.
func Capture(
	ctx context.Context,
	tx pgx.Tx,
	kind string,
	entityID uuid.UUID,
	prev, next map[string]any,
	changeType string,
	actor Actor,
) error {
	// Feature-flag check: re-read each call for simplicity (v1 perf is not the
	// primary concern; the query hits a single-row table by PK).
	enabled, err := isTimeTravelEnabled(ctx, tx)
	if err != nil {
		// Non-fatal: if we can't read settings, skip the history write rather
		// than failing the parent transaction.
		return nil //nolint:nilerr // deliberate: setting read failure is non-fatal
	}
	if !enabled {
		return nil
	}

	watched := WatchedFields[kind]

	// For update operations, check whether any watched field actually changed.
	if changeType == "update" && prev != nil {
		ops := Diff(prev, next, watched)
		if len(ops) == 0 {
			// No watched field changed — record noop metric and skip write.
			RecordNoop(kind)
			return nil
		}
	}

	now := time.Now().UTC()
	historyID := uuid.New()

	// Close the prior open row (all change types except "create").
	if changeType != "create" {
		closeQ := fmt.Sprintf(
			`UPDATE %s_history SET valid_to = $1 WHERE entity_id = $2 AND valid_to IS NULL`,
			kind,
		)
		if _, err := tx.Exec(ctx, closeQ, now, entityID); err != nil {
			return fmt.Errorf("timetravel: close prior %s history row: %w", kind, err)
		}
	}

	// Insert the new history row using a kind-specific helper.
	if err := insertHistoryRow(ctx, tx, kind, historyID, entityID, now, changeType, actor, next); err != nil {
		return fmt.Errorf("timetravel: insert %s history row: %w", kind, err)
	}

	RecordWrite(kind, changeType)
	return nil
}

// isTimeTravelEnabled reads the time_travel_enabled flag from the settings row.
// Returns true on any error so that a missing/unrunning migration doesn't kill
// every write path — the migration adds the column with DEFAULT TRUE, so in
// steady state the error path should be unreachable.
func isTimeTravelEnabled(ctx context.Context, tx pgx.Tx) (bool, error) {
	var enabled bool
	err := tx.QueryRow(ctx,
		`SELECT time_travel_enabled FROM settings WHERE id = 1`,
	).Scan(&enabled)
	if err != nil {
		return true, fmt.Errorf("read time_travel_enabled: %w", err) // fail-open
	}
	return enabled, nil
}

// insertHistoryRow dispatches to the kind-specific INSERT.
func insertHistoryRow(
	ctx context.Context,
	tx pgx.Tx,
	kind string,
	historyID, entityID uuid.UUID,
	validFrom time.Time,
	changeType string,
	actor Actor,
	row map[string]any,
) error {
	switch kind {
	case KindCluster:
		return insertClusterHistory(ctx, tx, historyID, entityID, validFrom, changeType, actor, row)
	case KindNamespace:
		return insertNamespaceHistory(ctx, tx, historyID, entityID, validFrom, changeType, actor, row)
	case KindNode:
		return insertNodeHistory(ctx, tx, historyID, entityID, validFrom, changeType, actor, row)
	case KindWorkload:
		return insertWorkloadHistory(ctx, tx, historyID, entityID, validFrom, changeType, actor, row)
	default:
		return fmt.Errorf("unknown kind %q", kind) //nolint:err113 // kind is caller-supplied; static error would lose context
	}
}

func insertClusterHistory(
	ctx context.Context,
	tx pgx.Tx,
	historyID, entityID uuid.UUID,
	validFrom time.Time,
	changeType string,
	actor Actor,
	r map[string]any,
) error {
	const q = `
		INSERT INTO clusters_history (
			history_id, entity_id, valid_from, valid_to, change_type,
			actor_id, actor_kind,
			name, display_name, environment, provider, region,
			kubernetes_version, api_endpoint, labels,
			owner, criticality, notes, runbook_url, annotations,
			terminated_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,NULL,$4,
			$5,$6,
			$7,$8,$9,$10,$11,
			$12,$13,$14,
			$15,$16,$17,$18,$19,
			$20,$21,$22
		)`
	_, err := tx.Exec(ctx, q,
		historyID, entityID, validFrom, changeType,
		actor.ID, actor.Kind,
		strField(r, "name"), strField(r, "display_name"), strField(r, "environment"), strField(r, "provider"), strField(r, "region"),
		strField(r, "kubernetes_version"), strField(r, "api_endpoint"), jsonField(r, "labels"),
		strField(r, "owner"), strField(r, "criticality"), strField(r, "notes"), strField(r, "runbook_url"), jsonField(r, "annotations"),
		timeField(r, "terminated_at"), timeField(r, "created_at"), timeField(r, "updated_at"),
	)
	if err != nil {
		return fmt.Errorf("insert cluster history: %w", err)
	}
	return nil
}

func insertNamespaceHistory(
	ctx context.Context,
	tx pgx.Tx,
	historyID, entityID uuid.UUID,
	validFrom time.Time,
	changeType string,
	actor Actor,
	r map[string]any,
) error {
	const q = `
		INSERT INTO namespaces_history (
			history_id, entity_id, valid_from, valid_to, change_type,
			actor_id, actor_kind,
			cluster_id, name, display_name, phase, labels,
			owner, criticality, notes, runbook_url, annotations,
			terminated_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,NULL,$4,
			$5,$6,
			$7,$8,$9,$10,$11,
			$12,$13,$14,$15,$16,
			$17,$18,$19
		)`
	_, err := tx.Exec(ctx, q,
		historyID, entityID, validFrom, changeType,
		actor.ID, actor.Kind,
		uuidField(r, "cluster_id"), strField(r, "name"), strField(r, "display_name"), strField(r, "phase"), jsonField(r, "labels"),
		strField(r, "owner"), strField(r, "criticality"), strField(r, "notes"), strField(r, "runbook_url"), jsonField(r, "annotations"),
		timeField(r, "terminated_at"), timeField(r, "created_at"), timeField(r, "updated_at"),
	)
	if err != nil {
		return fmt.Errorf("insert namespace history: %w", err)
	}
	return nil
}

func insertNodeHistory(
	ctx context.Context,
	tx pgx.Tx,
	historyID, entityID uuid.UUID,
	validFrom time.Time,
	changeType string,
	actor Actor,
	r map[string]any,
) error { //nolint:funlen // long but mechanical field list
	const q = `
		INSERT INTO nodes_history (
			history_id, entity_id, valid_from, valid_to, change_type,
			actor_id, actor_kind,
			cluster_id, name, display_name, role,
			kubelet_version, kube_proxy_version, container_runtime_version,
			os_image, operating_system, kernel_version, architecture,
			internal_ip, external_ip, pod_cidr,
			provider_id, instance_type, zone,
			capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
			allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
			conditions, taints, unschedulable, ready,
			labels, owner, criticality, notes, runbook_url, annotations,
			hardware_model, terminated_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,NULL,$4,
			$5,$6,
			$7,$8,$9,$10,
			$11,$12,$13,
			$14,$15,$16,$17,
			$18,$19,$20,
			$21,$22,$23,
			$24,$25,$26,$27,
			$28,$29,$30,$31,
			$32,$33,$34,$35,
			$36,$37,$38,$39,$40,$41,
			$42,$43,$44,$45
		)`
	_, err := tx.Exec(
		ctx,
		q,
		historyID,
		entityID,
		validFrom,
		changeType,
		actor.ID,
		actor.Kind,
		uuidField(r, "cluster_id"),
		strField(r, "name"),
		strField(r, "display_name"),
		strField(r, "role"),
		strField(r, "kubelet_version"),
		strField(r, "kube_proxy_version"),
		strField(r, "container_runtime_version"),
		strField(r, "os_image"),
		strField(r, "operating_system"),
		strField(r, "kernel_version"),
		strField(r, "architecture"),
		strField(r, "internal_ip"),
		strField(r, "external_ip"),
		strField(r, "pod_cidr"),
		strField(r, "provider_id"),
		strField(r, "instance_type"),
		strField(r, "zone"),
		strField(r, "capacity_cpu"),
		strField(r, "capacity_memory"),
		strField(r, "capacity_pods"),
		strField(r, "capacity_ephemeral_storage"),
		strField(
			r,
			"allocatable_cpu",
		),
		strField(r, "allocatable_memory"),
		strField(r, "allocatable_pods"),
		strField(r, "allocatable_ephemeral_storage"),
		jsonField(r, "conditions"),
		jsonField(r, "taints"),
		boolField(r, "unschedulable"),
		boolField(r, "ready"),
		jsonField(
			r,
			"labels",
		),
		strField(r, "owner"),
		strField(r, "criticality"),
		strField(r, "notes"),
		strField(r, "runbook_url"),
		jsonField(r, "annotations"),
		strField(r, "hardware_model"),
		timeField(r, "terminated_at"),
		timeField(r, "created_at"),
		timeField(r, "updated_at"),
	)
	if err != nil {
		return fmt.Errorf("insert node history: %w", err)
	}
	return nil
}

func insertWorkloadHistory(
	ctx context.Context,
	tx pgx.Tx,
	historyID, entityID uuid.UUID,
	validFrom time.Time,
	changeType string,
	actor Actor,
	r map[string]any,
) error {
	// ADR-0029 §7 / migration 00048: application_id is captured verbatim from
	// the parent row. The history column is a bare UUID (no FK) so rows
	// outlive the parent application — that lets "what was this workload
	// linked to last Tuesday?" survive an admin DELETE on the application.
	const q = `
		INSERT INTO workloads_history (
			history_id, entity_id, valid_from, valid_to, change_type,
			actor_id, actor_kind,
			namespace_id, kind, name, replicas, ready_replicas,
			labels, spec, containers, application_id,
			terminated_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,NULL,$4,
			$5,$6,
			$7,$8,$9,$10,$11,
			$12,$13,$14,$15,
			$16,$17,$18
		)`
	_, err := tx.Exec(ctx, q,
		historyID, entityID, validFrom, changeType,
		actor.ID, actor.Kind,
		uuidField(r, "namespace_id"), strField(r, "kind"), strField(r, "name"), intField(r, "replicas"), intField(r, "ready_replicas"),
		jsonField(r, "labels"), jsonField(r, "spec"), jsonField(r, "containers"), uuidField(r, "application_id"),
		timeField(r, "terminated_at"), timeField(r, "created_at"), timeField(r, "updated_at"),
	)
	if err != nil {
		return fmt.Errorf("insert workload history: %w", err)
	}
	return nil
}

// -- field helpers ------------------------------------------------------------

func strField(r map[string]any, key string) *string {
	v, ok := r[key]
	if !ok || v == nil {
		return nil
	}
	switch s := v.(type) {
	case string:
		return &s
	case *string:
		return s
	}
	return nil
}

func uuidField(r map[string]any, key string) *uuid.UUID {
	v, ok := r[key]
	if !ok || v == nil {
		return nil
	}
	switch u := v.(type) {
	case uuid.UUID:
		return &u
	case *uuid.UUID:
		return u
	}
	return nil
}

func timeField(r map[string]any, key string) *time.Time {
	v, ok := r[key]
	if !ok || v == nil {
		return nil
	}
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		return t
	}
	return nil
}

func jsonField(r map[string]any, key string) any {
	v, ok := r[key]
	if !ok {
		return nil
	}
	return v
}

func boolField(r map[string]any, key string) *bool {
	v, ok := r[key]
	if !ok || v == nil {
		return nil
	}
	switch b := v.(type) {
	case bool:
		return &b
	case *bool:
		return b
	}
	return nil
}

func intField(r map[string]any, key string) *int {
	v, ok := r[key]
	if !ok || v == nil {
		return nil
	}
	switch i := v.(type) {
	case int:
		return &i
	case *int:
		return i
	case int32:
		n := int(i)
		return &n
	case int64:
		n := int(i)
		return &n
	}
	return nil
}
