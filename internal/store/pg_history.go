package store

// pg_history.go — Store implementation for time-travel history reads (ADR-0021 Phase 3).
// ListEntityHistory: paginated newest-first list of history rows.
// GetEntityAsOf:     point-in-time lookup from a history table.
// IsTimeTravelEnabled: reads the settings flag.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/timetravel"
)

// validHistoryKind returns true for the four kinds with history tables.
func validHistoryKind(kind string) bool {
	switch kind {
	case timetravel.KindCluster, timetravel.KindNamespace,
		timetravel.KindNode, timetravel.KindWorkload:
		return true
	}
	return false
}

// IsTimeTravelEnabled reads the time_travel_enabled settings flag.
func (s *PG) IsTimeTravelEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx,
		`SELECT time_travel_enabled FROM settings WHERE id = 1`,
	).Scan(&enabled)
	if err != nil {
		return false, fmt.Errorf("read time_travel_enabled: %w", err)
	}
	return enabled, nil
}

// historyCursorJSON is the opaque pagination token for ListEntityHistory.
// Encoded as base64(JSON{"vf": validFrom.UnixNano, "hid": historyID}).
type historyCursorJSON struct {
	VF  int64  `json:"vf"`
	HID string `json:"hid"`
}

func encodeHistoryCursor(validFrom time.Time, historyID uuid.UUID) string {
	b, _ := json.Marshal(historyCursorJSON{VF: validFrom.UnixNano(), HID: historyID.String()})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeHistoryCursor(s string) (validFrom time.Time, historyID uuid.UUID, err error) {
	b, decErr := base64.RawURLEncoding.DecodeString(s)
	if decErr != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("decode history cursor: %w", decErr)
	}
	var c historyCursorJSON
	if unmErr := json.Unmarshal(b, &c); unmErr != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("unmarshal history cursor: %w", unmErr)
	}
	validFrom = time.Unix(0, c.VF).UTC()
	hid, parseErr := uuid.Parse(c.HID)
	if parseErr != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("history cursor: parse UUID: %w", parseErr)
	}
	return validFrom, hid, nil
}

// buildHistoryCursorClause returns the SQL clause and additional args for
// cursor-based pagination (newest-first). Returns an empty clause when cursor
// is empty (first page).
func buildHistoryCursorClause(cursor string, args []any) (clause string, outArgs []any, err error) {
	if cursor == "" {
		return "", args, nil
	}
	vf, hid, err := decodeHistoryCursor(cursor)
	if err != nil {
		return "", nil, fmt.Errorf("invalid cursor: %w", err)
	}
	n := len(args) + 1
	c := fmt.Sprintf(
		`AND (valid_from < $%d OR (valid_from = $%d AND history_id < $%d))`,
		n, n, n+1,
	)
	args = append(args, vf, hid)
	return c, args, nil
}

// ListEntityHistory returns up to limit history rows for one entity, newest-first.
func (s *PG) ListEntityHistory(
	ctx context.Context,
	kind string,
	entityID uuid.UUID,
	limit int,
	cursor string,
) ([]api.HistoryRow, string, error) {
	if !validHistoryKind(kind) {
		return nil, "", fmt.Errorf("unknown kind %q for history", kind) //nolint:err113 // dynamic kind; static sentinel would lose context
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	args := []any{entityID, limit + 1} // +1 to detect next page
	cursorClause, args, err := buildHistoryCursorClause(cursor, args)
	if err != nil {
		return nil, "", err
	}

	q := fmt.Sprintf(`
		SELECT history_id, entity_id, valid_from, valid_to, change_type, actor_id, actor_kind
		FROM %s_history
		WHERE entity_id = $1
		%s
		ORDER BY valid_from DESC, history_id DESC
		LIMIT $2`, kind, cursorClause)

	dbRows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list %s history: %w", kind, err)
	}
	defer dbRows.Close()

	raw, err := scanHistoryRows(dbRows)
	if err != nil {
		return nil, "", fmt.Errorf("scan %s history: %w", kind, err)
	}

	hasMore := len(raw) > limit
	if hasMore {
		raw = raw[:limit]
	}

	result := make([]api.HistoryRow, 0, len(raw))
	for i := range raw {
		result = append(result, buildHistoryRow(ctx, s, kind, &raw[i]))
	}

	var nextCursor string
	if hasMore {
		last := raw[len(raw)-1]
		nextCursor = encodeHistoryCursor(last.validFrom, last.historyID)
	}

	return result, nextCursor, nil
}

// rawHistoryRow holds the envelope columns from a history table SELECT.
type rawHistoryRow struct {
	historyID  uuid.UUID
	entityID   uuid.UUID
	validFrom  time.Time
	validTo    *time.Time
	changeType string
	actorID    *uuid.UUID
	actorKind  *string
}

func scanHistoryRows(rows pgx.Rows) ([]rawHistoryRow, error) {
	var out []rawHistoryRow
	for rows.Next() {
		var r rawHistoryRow
		if err := rows.Scan(
			&r.historyID, &r.entityID, &r.validFrom, &r.validTo,
			&r.changeType, &r.actorID, &r.actorKind,
		); err != nil {
			return nil, fmt.Errorf("scan history envelope row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate history rows: %w", err)
	}
	return out, nil
}

// buildHistoryRow converts a rawHistoryRow to the api.HistoryRow shape,
// computing the diff for "update" rows by reading the current and prior
// history rows from the DB.
func buildHistoryRow(ctx context.Context, s *PG, kind string, r *rawHistoryRow) api.HistoryRow {
	hr := api.HistoryRow{
		HistoryID:  r.historyID.String(),
		EntityID:   r.entityID.String(),
		ValidFrom:  r.validFrom,
		ValidTo:    r.validTo,
		ChangeType: r.changeType,
		Diff:       []timetravel.PatchOp{},
	}
	if r.actorID != nil {
		s := r.actorID.String()
		hr.ActorID = &s
	}
	hr.ActorKind = r.actorKind

	if r.changeType == "update" {
		if ops, err := computeHistoryDiff(ctx, s, kind, r.entityID, r.validFrom); err == nil {
			hr.Diff = ops
		}
		// On error, leave Diff as empty slice — non-fatal for the overall listing.
	}
	return hr
}

// computeHistoryDiff fetches the current and prior history rows and diffs
// their watched fields.
func computeHistoryDiff(
	ctx context.Context,
	s *PG,
	kind string,
	entityID uuid.UUID,
	validFrom time.Time,
) ([]timetravel.PatchOp, error) {
	cur, err := historyRowMap(ctx, s, kind, entityID, validFrom)
	if err != nil {
		return nil, err
	}
	prev, err := historyRowMapBefore(ctx, s, kind, entityID, validFrom)
	if err != nil {
		return nil, err
	}
	return timetravel.Diff(prev, cur, timetravel.WatchedFields[kind]), nil
}

// historyRowMap reads one history row identified by (entity_id, valid_from).
func historyRowMap(
	ctx context.Context,
	s *PG,
	kind string,
	entityID uuid.UUID,
	validFrom time.Time,
) (map[string]any, error) {
	cols, scanFn := historyColumnsAndScanner(kind)
	q := fmt.Sprintf(
		`SELECT %s FROM %s_history WHERE entity_id = $1 AND valid_from = $2`,
		cols, kind,
	)
	return scanFn(s.pool.QueryRow(ctx, q, entityID, validFrom))
}

// historyRowMapBefore reads the history row immediately preceding validFrom.
func historyRowMapBefore(
	ctx context.Context,
	s *PG,
	kind string,
	entityID uuid.UUID,
	validFrom time.Time,
) (map[string]any, error) {
	cols, scanFn := historyColumnsAndScanner(kind)
	q := fmt.Sprintf(
		`SELECT %s FROM %s_history
		 WHERE entity_id = $1 AND valid_from < $2
		 ORDER BY valid_from DESC LIMIT 1`,
		cols, kind,
	)
	return scanFn(s.pool.QueryRow(ctx, q, entityID, validFrom))
}

// GetEntityAsOf returns the history row valid at time t for the given entity.
func (s *PG) GetEntityAsOf(
	ctx context.Context,
	kind string,
	entityID uuid.UUID,
	t time.Time,
) (map[string]any, error) {
	if !validHistoryKind(kind) {
		return nil, fmt.Errorf("unknown kind %q for history", kind) //nolint:err113 // dynamic kind; static sentinel would lose context
	}
	cols, scanFn := historyColumnsAndScanner(kind)
	q := fmt.Sprintf(`
		SELECT %s FROM %s_history
		WHERE entity_id = $1
		  AND valid_from <= $2
		  AND (valid_to IS NULL OR valid_to > $2)
		LIMIT 1`, cols, kind)
	m, err := scanFn(s.pool.QueryRow(ctx, q, entityID, t))
	if errors.Is(err, api.ErrNotFound) {
		return nil, api.ErrNotFound
	}
	return m, err
}

// historyColumnsAndScanner returns the SELECT column list and a scan function
// for the given kind's history table. Named results for clarity.
func historyColumnsAndScanner(kind string) (cols string, scanner func(pgx.Row) (map[string]any, error)) {
	switch kind {
	case timetravel.KindCluster:
		return clusterHistoryColumns, scanClusterHistoryRow
	case timetravel.KindNamespace:
		return namespaceHistoryColumns, scanNamespaceHistoryRow
	case timetravel.KindNode:
		return nodeHistoryColumns, scanNodeHistoryRow
	case timetravel.KindWorkload:
		return workloadHistoryColumns, scanWorkloadHistoryRow
	default:
		return "", nil
	}
}

// --- cluster history row scanner --------------------------------------------

const clusterHistoryColumns = `
	name, display_name, environment, provider, region,
	kubernetes_version, api_endpoint, labels,
	owner, criticality, notes, runbook_url, annotations,
	terminated_at, created_at, updated_at`

func scanClusterHistoryRow(row pgx.Row) (map[string]any, error) {
	var (
		name         string
		displayName  *string
		environment  *string
		provider     *string
		region       *string
		kubeVersion  *string
		apiEndpoint  *string
		labelsJSON   []byte
		owner        *string
		criticality  *string
		notes        *string
		runbookURL   *string
		annotJSON    []byte
		terminatedAt *time.Time
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&name, &displayName, &environment, &provider, &region,
		&kubeVersion, &apiEndpoint, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotJSON,
		&terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan cluster history row: %w", err)
	}
	return map[string]any{
		"name":               name,
		"display_name":       displayName,
		"environment":        environment,
		"provider":           provider,
		"region":             region,
		"kubernetes_version": kubeVersion,
		"api_endpoint":       apiEndpoint,
		"labels":             unmarshalJSONB(labelsJSON),
		"owner":              owner,
		"criticality":        criticality,
		"notes":              notes,
		"runbook_url":        runbookURL,
		"annotations":        unmarshalJSONB(annotJSON),
		"terminated_at":      terminatedAt,
		"created_at":         createdAt,
		"updated_at":         updatedAt,
	}, nil
}

// --- namespace history row scanner ------------------------------------------

const namespaceHistoryColumns = `
	cluster_id, name, display_name, phase, labels,
	owner, criticality, notes, runbook_url, annotations,
	terminated_at, created_at, updated_at`

func scanNamespaceHistoryRow(row pgx.Row) (map[string]any, error) {
	var (
		clusterID    uuid.UUID
		name         string
		displayName  *string
		phase        *string
		labelsJSON   []byte
		owner        *string
		criticality  *string
		notes        *string
		runbookURL   *string
		annotJSON    []byte
		terminatedAt *time.Time
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&clusterID, &name, &displayName, &phase, &labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotJSON,
		&terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan namespace history row: %w", err)
	}
	return map[string]any{
		"cluster_id":    clusterID.String(),
		"name":          name,
		"display_name":  displayName,
		"phase":         phase,
		"labels":        unmarshalJSONB(labelsJSON),
		"owner":         owner,
		"criticality":   criticality,
		"notes":         notes,
		"runbook_url":   runbookURL,
		"annotations":   unmarshalJSONB(annotJSON),
		"terminated_at": terminatedAt,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
	}, nil
}

// --- node history row scanner -----------------------------------------------

const nodeHistoryColumns = `
	cluster_id, name, display_name, role,
	kubelet_version, kube_proxy_version, container_runtime_version,
	os_image, operating_system, kernel_version, architecture,
	internal_ip, external_ip, pod_cidr,
	provider_id, instance_type, zone,
	capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
	allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
	conditions, taints, unschedulable, ready,
	labels, owner, criticality, notes, runbook_url, annotations,
	hardware_model, terminated_at, created_at, updated_at`

func scanNodeHistoryRow(row pgx.Row) (map[string]any, error) { //nolint:funlen // many node columns; mechanical field list
	var (
		clusterID               uuid.UUID
		name                    string
		displayName             *string
		role                    *string
		kubeletVersion          *string
		kubeProxyVersion        *string
		containerRuntimeVersion *string
		osImage                 *string
		operatingSystem         *string
		kernelVersion           *string
		architecture            *string
		internalIP              *string
		externalIP              *string
		podCIDR                 *string
		providerID              *string
		instanceType            *string
		zone                    *string
		capacityCPU             *string
		capacityMemory          *string
		capacityPods            *string
		capacityEphemeral       *string
		allocatableCPU          *string
		allocatableMemory       *string
		allocatablePods         *string
		allocatableEphemeral    *string
		conditionsJSON          []byte
		taintsJSON              []byte
		unschedulable           bool
		ready                   bool
		labelsJSON              []byte
		owner                   *string
		criticality             *string
		notes                   *string
		runbookURL              *string
		annotationsJSON         []byte
		hardwareModel           *string
		terminatedAt            *time.Time
		createdAt               time.Time
		updatedAt               time.Time
	)
	if err := row.Scan(
		&clusterID, &name, &displayName, &role,
		&kubeletVersion, &kubeProxyVersion, &containerRuntimeVersion,
		&osImage, &operatingSystem, &kernelVersion, &architecture,
		&internalIP, &externalIP, &podCIDR,
		&providerID, &instanceType, &zone,
		&capacityCPU, &capacityMemory, &capacityPods, &capacityEphemeral,
		&allocatableCPU, &allocatableMemory, &allocatablePods, &allocatableEphemeral,
		&conditionsJSON, &taintsJSON, &unschedulable, &ready,
		&labelsJSON,
		&owner, &criticality, &notes, &runbookURL, &annotationsJSON,
		&hardwareModel, &terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan node history row: %w", err)
	}
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
		"cluster_id":                    clusterID.String(),
		"name":                          name,
		"display_name":                  displayName,
		"role":                          role,
		"kubelet_version":               kubeletVersion,
		"kube_proxy_version":            kubeProxyVersion,
		"container_runtime_version":     containerRuntimeVersion,
		"os_image":                      osImage,
		"operating_system":              operatingSystem,
		"kernel_version":                kernelVersion,
		"architecture":                  architecture,
		"internal_ip":                   internalIP,
		"external_ip":                   externalIP,
		"pod_cidr":                      podCIDR,
		"provider_id":                   providerID,
		"instance_type":                 instanceType,
		"zone":                          zone,
		"capacity_cpu":                  capacityCPU,
		"capacity_memory":               capacityMemory,
		"capacity_pods":                 capacityPods,
		"capacity_ephemeral_storage":    capacityEphemeral,
		"allocatable_cpu":               allocatableCPU,
		"allocatable_memory":            allocatableMemory,
		"allocatable_pods":              allocatablePods,
		"allocatable_ephemeral_storage": allocatableEphemeral,
		"conditions":                    conditions,
		"taints":                        taints,
		"unschedulable":                 unschedulable,
		"ready":                         ready,
		"labels":                        unmarshalJSONB(labelsJSON),
		"owner":                         owner,
		"criticality":                   criticality,
		"notes":                         notes,
		"runbook_url":                   runbookURL,
		"annotations":                   unmarshalJSONB(annotationsJSON),
		"hardware_model":                hardwareModel,
		"terminated_at":                 terminatedAt,
		"created_at":                    createdAt,
		"updated_at":                    updatedAt,
	}, nil
}

// --- workload history row scanner -------------------------------------------

// workloadHistoryColumns lists the parent-table columns snapshotted into
// workloads_history. application_id (ADR-0029 / migration 00048) is part
// of the snapshot so /v1/workloads/{id}/history can show the curator's
// link as of any point in time.
const workloadHistoryColumns = `
	namespace_id, kind, name, replicas, ready_replicas,
	labels, spec, containers, application_id,
	terminated_at, created_at, updated_at`

func scanWorkloadHistoryRow(row pgx.Row) (map[string]any, error) {
	var (
		namespaceID    uuid.UUID
		kind           string
		name           string
		replicas       *int32
		readyReplicas  *int32
		labelsJSON     []byte
		specJSON       []byte
		containersJSON []byte
		applicationID  uuid.NullUUID
		terminatedAt   *time.Time
		createdAt      time.Time
		updatedAt      time.Time
	)
	if err := row.Scan(
		&namespaceID, &kind, &name, &replicas, &readyReplicas,
		&labelsJSON, &specJSON, &containersJSON, &applicationID,
		&terminatedAt, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, api.ErrNotFound
		}
		return nil, fmt.Errorf("scan workload history row: %w", err)
	}
	m := map[string]any{
		"namespace_id":  namespaceID.String(),
		"kind":          kind,
		"name":          name,
		"labels":        unmarshalJSONB(labelsJSON),
		"spec":          unmarshalJSONBVal(specJSON),
		"containers":    unmarshalJSONBVal(containersJSON),
		"terminated_at": terminatedAt,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
	}
	if replicas != nil {
		m["replicas"] = int(*replicas)
	}
	if readyReplicas != nil {
		m["ready_replicas"] = int(*readyReplicas)
	}
	if applicationID.Valid {
		m["application_id"] = applicationID.UUID.String()
	}
	return m, nil
}

func unmarshalJSONBVal(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(b, &v)
	return v
}
