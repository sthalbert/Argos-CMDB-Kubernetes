package store

// pg_timetravel.go — helpers that fetch current DB rows as map[string]any
// snapshots for the timetravel.Capture function, plus conversion utilities.
//
// Since the generated api.* structs do not expose terminated_at (it's handled
// at the store layer only), we read directly from the DB into raw maps when we
// need a pre-update snapshot, and build the post-update snapshot from the same
// source after the UPDATE runs.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
)

// --- cluster ----------------------------------------------------------------

// clusterRowMap reads a clusters row by id inside tx (FOR UPDATE) and returns
// it as a map[string]any for timetravel.Capture.
func clusterRowMap(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       terminated_at, created_at, updated_at
		FROM clusters WHERE id = $1 FOR UPDATE`
	return clusterRowMapFromRow(tx.QueryRow(ctx, q, id))
}

// clusterRowMapNoLock reads a clusters row by id without a FOR UPDATE lock.
// Used for "create" snapshots where we already know the row is new.
func clusterRowMapNoLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, name, display_name, environment, provider, region,
		       kubernetes_version, api_endpoint, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       terminated_at, created_at, updated_at
		FROM clusters WHERE id = $1`
	return clusterRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func clusterRowMapFromRow(row pgx.Row) (map[string]any, error) {
	var (
		id           uuid.UUID
		name         string
		displayName  sql.NullString
		environment  sql.NullString
		provider     sql.NullString
		region       sql.NullString
		kubeVersion  sql.NullString
		apiEndpoint  sql.NullString
		labelsJSON   []byte
		owner        sql.NullString
		criticality  sql.NullString
		notes        sql.NullString
		runbookURL   sql.NullString
		annotJSON    []byte
		terminatedAt *time.Time
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&id, &name, &displayName, &environment, &provider, &region,
		&kubeVersion, &apiEndpoint, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotJSON,
		&terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan cluster row map: %w", err)
	}
	labels, annotations := unmarshalJSONB(labelsJSON), unmarshalJSONB(annotJSON)
	return map[string]any{
		"id":                 id.String(),
		"name":               name,
		"display_name":       ns(displayName),
		"environment":        ns(environment),
		"provider":           ns(provider),
		"region":             ns(region),
		"kubernetes_version": ns(kubeVersion),
		"api_endpoint":       ns(apiEndpoint),
		"labels":             labels,
		"owner":              ns(owner),
		"criticality":        ns(criticality),
		"notes":              ns(notes),
		"runbook_url":        ns(runbookURL),
		"annotations":        annotations,
		"terminated_at":      terminatedAt,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
	}, nil
}

// --- namespace --------------------------------------------------------------

func namespaceRowMap(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, cluster_id, name, display_name, phase, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       terminated_at, created_at, updated_at
		FROM namespaces WHERE id = $1 FOR UPDATE`
	return namespaceRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func namespaceRowMapNoLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, cluster_id, name, display_name, phase, labels,
		       owner, criticality, notes, runbook_url, annotations,
		       terminated_at, created_at, updated_at
		FROM namespaces WHERE id = $1`
	return namespaceRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func namespaceRowMapFromRow(row pgx.Row) (map[string]any, error) {
	var (
		id           uuid.UUID
		clusterID    uuid.UUID
		name         string
		displayName  sql.NullString
		phase        sql.NullString
		labelsJSON   []byte
		owner        sql.NullString
		criticality  sql.NullString
		notes        sql.NullString
		runbookURL   sql.NullString
		annotJSON    []byte
		terminatedAt *time.Time
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&id, &clusterID, &name, &displayName, &phase, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotJSON,
		&terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan namespace row map: %w", err)
	}
	labels, annotations := unmarshalJSONB(labelsJSON), unmarshalJSONB(annotJSON)
	return map[string]any{
		"id":            id.String(),
		"cluster_id":    clusterID,
		"name":          name,
		"display_name":  ns(displayName),
		"phase":         ns(phase),
		"labels":        labels,
		"owner":         ns(owner),
		"criticality":   ns(criticality),
		"notes":         ns(notes),
		"runbook_url":   ns(runbookURL),
		"annotations":   annotations,
		"terminated_at": terminatedAt,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
	}, nil
}

// --- node -------------------------------------------------------------------

func nodeRowMap(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	q := `SELECT ` + nodeColumns + `, terminated_at FROM nodes WHERE id = $1 FOR UPDATE`
	return nodeRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func nodeRowMapNoLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	q := `SELECT ` + nodeColumns + `, terminated_at FROM nodes WHERE id = $1`
	return nodeRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func nodeRowMapFromRow(row pgx.Row) (map[string]any, error) { //nolint:funlen // many node columns
	var (
		id                      uuid.UUID
		clusterID               uuid.UUID
		name                    string
		displayName             sql.NullString
		role                    sql.NullString
		kubeletVersion          sql.NullString
		kubeProxyVersion        sql.NullString
		containerRuntimeVersion sql.NullString
		osImage                 sql.NullString
		operatingSystem         sql.NullString
		kernelVersion           sql.NullString
		architecture            sql.NullString
		internalIP              sql.NullString
		externalIP              sql.NullString
		podCIDR                 sql.NullString
		providerID              sql.NullString
		instanceType            sql.NullString
		zone                    sql.NullString
		capacityCPU             sql.NullString
		capacityMemory          sql.NullString
		capacityPods            sql.NullString
		capacityEphemeral       sql.NullString
		allocatableCPU          sql.NullString
		allocatableMemory       sql.NullString
		allocatablePods         sql.NullString
		allocatableEphemeral    sql.NullString
		conditionsJSON          []byte
		taintsJSON              []byte
		unschedulable           bool
		ready                   bool
		labelsJSON              []byte
		owner                   sql.NullString
		criticality             sql.NullString
		notes                   sql.NullString
		runbookURL              sql.NullString
		annotationsJSON         []byte
		hardwareModel           sql.NullString
		createdAt               time.Time
		updatedAt               time.Time
		terminatedAt            *time.Time
	)
	if err := row.Scan(
		&id, &clusterID, &name, &displayName, &role,
		&kubeletVersion, &kubeProxyVersion, &containerRuntimeVersion,
		&osImage, &operatingSystem, &kernelVersion, &architecture,
		&internalIP, &externalIP, &podCIDR,
		&providerID, &instanceType, &zone,
		&capacityCPU, &capacityMemory, &capacityPods, &capacityEphemeral,
		&allocatableCPU, &allocatableMemory, &allocatablePods, &allocatableEphemeral,
		&conditionsJSON, &taintsJSON, &unschedulable, &ready,
		&labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON, &hardwareModel,
		&createdAt, &updatedAt,
		&terminatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan node row map: %w", err)
	}
	labels := unmarshalJSONB(labelsJSON)
	annotations := unmarshalJSONB(annotationsJSON)
	var conditions, taints any
	if len(conditionsJSON) > 0 {
		var v any
		_ = json.Unmarshal(conditionsJSON, &v)
		conditions = v
	}
	if len(taintsJSON) > 0 {
		var v any
		_ = json.Unmarshal(taintsJSON, &v)
		taints = v
	}
	return map[string]any{
		"id":                            id.String(),
		"cluster_id":                    clusterID,
		"name":                          name,
		"display_name":                  ns(displayName),
		"role":                          ns(role),
		"kubelet_version":               ns(kubeletVersion),
		"kube_proxy_version":            ns(kubeProxyVersion),
		"container_runtime_version":     ns(containerRuntimeVersion),
		"os_image":                      ns(osImage),
		"operating_system":              ns(operatingSystem),
		"kernel_version":                ns(kernelVersion),
		"architecture":                  ns(architecture),
		"internal_ip":                   ns(internalIP),
		"external_ip":                   ns(externalIP),
		"pod_cidr":                      ns(podCIDR),
		"provider_id":                   ns(providerID),
		"instance_type":                 ns(instanceType),
		"zone":                          ns(zone),
		"capacity_cpu":                  ns(capacityCPU),
		"capacity_memory":               ns(capacityMemory),
		"capacity_pods":                 ns(capacityPods),
		"capacity_ephemeral_storage":    ns(capacityEphemeral),
		"allocatable_cpu":               ns(allocatableCPU),
		"allocatable_memory":            ns(allocatableMemory),
		"allocatable_pods":              ns(allocatablePods),
		"allocatable_ephemeral_storage": ns(allocatableEphemeral),
		"conditions":                    conditions,
		"taints":                        taints,
		"unschedulable":                 unschedulable,
		"ready":                         ready,
		"labels":                        labels,
		"owner":                         ns(owner),
		"criticality":                   ns(criticality),
		"notes":                         ns(notes),
		"runbook_url":                   ns(runbookURL),
		"annotations":                   annotations,
		"hardware_model":                ns(hardwareModel),
		"terminated_at":                 terminatedAt,
		"created_at":                    createdAt,
		"updated_at":                    updatedAt,
	}, nil
}

// --- workload ---------------------------------------------------------------

func workloadRowMap(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, namespace_id, kind, name, replicas, ready_replicas,
		       containers, labels, spec, created_at, updated_at, terminated_at
		FROM workloads WHERE id = $1 FOR UPDATE`
	return workloadRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func workloadRowMapNoLock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (map[string]any, error) {
	const q = `
		SELECT id, namespace_id, kind, name, replicas, ready_replicas,
		       containers, labels, spec, created_at, updated_at, terminated_at
		FROM workloads WHERE id = $1`
	return workloadRowMapFromRow(tx.QueryRow(ctx, q, id))
}

func workloadRowMapFromRow(row pgx.Row) (map[string]any, error) {
	var (
		id             uuid.UUID
		namespaceID    uuid.UUID
		kind           string
		name           string
		replicas       sql.NullInt32
		readyReplicas  sql.NullInt32
		containersJSON []byte
		labelsJSON     []byte
		specJSON       []byte
		createdAt      time.Time
		updatedAt      time.Time
		terminatedAt   *time.Time
	)
	if err := row.Scan(
		&id, &namespaceID, &kind, &name,
		&replicas, &readyReplicas,
		&containersJSON, &labelsJSON, &specJSON,
		&createdAt, &updatedAt, &terminatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan workload row map: %w", err)
	}
	labels := unmarshalJSONB(labelsJSON)
	var spec any
	if len(specJSON) > 0 {
		var v any
		_ = json.Unmarshal(specJSON, &v)
		spec = v
	}
	var containers any
	if len(containersJSON) > 0 {
		var v any
		_ = json.Unmarshal(containersJSON, &v)
		containers = v
	}
	m := map[string]any{
		"id":            id.String(),
		"namespace_id":  namespaceID,
		"kind":          kind,
		"name":          name,
		"labels":        labels,
		"spec":          spec,
		"containers":    containers,
		"terminated_at": terminatedAt,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
	}
	if replicas.Valid {
		m["replicas"] = int(replicas.Int32)
	} else {
		m["replicas"] = nil
	}
	if readyReplicas.Valid {
		m["ready_replicas"] = int(readyReplicas.Int32)
	} else {
		m["ready_replicas"] = nil
	}
	return m, nil
}

// --- shared helpers ---------------------------------------------------------

// ns converts a sql.NullString to *string (nil when invalid).
func ns(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// unmarshalJSONB returns a Go value from a JSONB byte slice, or nil on empty input.
func unmarshalJSONB(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}
