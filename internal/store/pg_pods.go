// Pod CRUD + upsert + reconcile. Split out of pg.go.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

// CreatePod inserts a new pod.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) CreatePod(ctx context.Context, in api.PodCreate) (api.Pod, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Pod{}, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Pod{}, err
	}

	const q = `
		INSERT INTO pods (
			id, namespace_id, name, phase, node_name, pod_ip,
			workload_id, containers, labels, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
	`
	_, err = p.pool.Exec(ctx, q,
		id, in.NamespaceId, in.Name, in.Phase, in.NodeName, in.PodIp,
		in.WorkloadId, containersJSON, labelsJSON, now,
	)
	if err != nil {
		if pErr := classifyPodFKError(err, in.NamespaceId, in.WorkloadId); pErr != nil {
			return api.Pod{}, pErr
		}
		if isUniqueViolation(err) {
			return api.Pod{}, fmt.Errorf("pod %q in namespace %s already exists: %w", in.Name, in.NamespaceId, api.ErrConflict)
		}
		return api.Pod{}, fmt.Errorf("insert pod: %w", err)
	}

	return api.Pod{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Phase:       in.Phase,
		NodeName:    in.NodeName,
		PodIp:       in.PodIp,
		WorkloadId:  in.WorkloadId,
		Containers:  in.Containers,
		Labels:      in.Labels,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}, nil
}

// classifyPodFKError disambiguates 23503 foreign-key violations on the pods
// table into namespace vs workload misses, so the handler can return an
// accurate 404 message. PG auto-names FK constraints <table>_<column>_fkey;
// we match on the column name in pgErr.ConstraintName.
func classifyPodFKError(err error, namespaceID uuid.UUID, workloadID *uuid.UUID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return nil
	}
	if strings.Contains(pgErr.ConstraintName, "workload_id") {
		target := nilUUIDDisplay
		if workloadID != nil {
			target = workloadID.String()
		}
		return fmt.Errorf("workload %s does not exist: %w", target, api.ErrNotFound)
	}
	return fmt.Errorf("namespace %s does not exist: %w", namespaceID, api.ErrNotFound)
}

// GetPod fetches a pod by id.
// Pod read-side projection with parent-name JOINs (ADR-0027). Pods also
// LEFT JOIN workloads to expose `workload_name`.
const podSelectColumns = `p.id, p.namespace_id, p.name, p.phase, p.node_name, p.pod_ip,
	p.workload_id, p.containers, p.labels, p.created_at, p.updated_at,
	n.name AS namespace_name, n.cluster_id AS namespace_cluster_id, c.name AS cluster_name,
	w.name AS workload_name`

const podFromJoined = `FROM pods p
	LEFT JOIN namespaces n ON n.id = p.namespace_id
	LEFT JOIN clusters   c ON c.id = n.cluster_id
	LEFT JOIN workloads  w ON w.id = p.workload_id`

// GetPod fetches a pod by id, including the denormalized namespace_name,
// cluster_id, cluster_name, and workload_name (ADR-0027).
func (p *PG) GetPod(ctx context.Context, id uuid.UUID) (api.Pod, error) {
	q := `SELECT ` + podSelectColumns + ` ` + podFromJoined + ` WHERE p.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	pod, err := scanPod(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Pod{}, api.ErrNotFound
		}
		return api.Pod{}, fmt.Errorf("select pod: %w", err)
	}
	return pod, nil
}

// podSortSpec is the sort=<key> allowlist for GET /v1/pods.
var podSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:      {expr: "LOWER(p.name)", kind: sortText},
		sortKeyPhase:     {expr: "LOWER(p.phase)", kind: sortText, nullable: true},
		"node_name":      {expr: "LOWER(p.node_name)", kind: sortText, nullable: true},
		"pod_ip":         {expr: "LOWER(p.pod_ip)", kind: sortText, nullable: true},
		sortKeyCreatedAt: {expr: "p.created_at", kind: sortTime},
		sortKeyUpdatedAt: {expr: "p.updated_at", kind: sortTime},
	},
	defaultKey: sortKeyCreatedAt,
}

// podSortVal extracts the serialized sort value for cursor minting.
func podSortVal(pod *api.Pod, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&pod.Name)
	case sortKeyPhase:
		return sortValText(pod.Phase)
	case "node_name":
		return sortValText(pod.NodeName)
	case "pod_ip":
		return sortValText(pod.PodIp)
	case sortKeyUpdatedAt:
		return sortValTime(pod.UpdatedAt)
	default: // created_at
		return sortValTime(pod.CreatedAt)
	}
}

// ListPods returns a cursor-paginated page of pods, optionally filtered.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPods(ctx context.Context, filter api.PodListFilter, page api.ListPage) ([]api.Pod, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := podSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(podSelectColumns)
	sb.WriteString(` `)
	sb.WriteString(podFromJoined)
	args := make([]any, 0, 8)
	conds := make([]string, 0, 6)

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("p.namespace_id = $%d", len(args)))
	}
	if filter.NodeName != nil {
		args = append(args, *filter.NodeName)
		conds = append(conds, fmt.Sprintf("p.node_name = $%d", len(args)))
	}
	if filter.WorkloadID != nil {
		args = append(args, *filter.WorkloadID)
		conds = append(conds, fmt.Sprintf("p.workload_id = $%d", len(args)))
	}
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(p.name) LIKE $%d ESCAPE '\\'", len(args)))
	}
	if filter.ImageSubstring != nil && *filter.ImageSubstring != "" {
		// ILIKE handles case-insensitivity; escapeLike + ESCAPE makes
		// operator-pasted % / _ literal (was unescaped before ADR-0042).
		args = append(args, "%"+escapeLike(*filter.ImageSubstring)+"%")
		conds = append(conds, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM jsonb_array_elements(p.containers) elem WHERE elem->>'image' ILIKE $%d ESCAPE '\\')",
			len(args),
		))
	}
	if page.Cursor != "" {
		val, cid, err := decodeListCursor(page.Cursor, key, dir)
		if err != nil {
			return nil, "", err
		}
		if err := keysetCond(col, "p.id", dir, val, cid, &conds, &args); err != nil {
			return nil, "", err
		}
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "p.id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query pods: %w", err)
	}
	defer rows.Close()

	items := make([]api.Pod, 0, limit)
	for rows.Next() {
		pod, err := scanPod(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan pod: %w", err)
		}
		items = append(items, pod)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate pods: %w", err)
	}

	var next string
	if len(items) > limit {
		last := &items[limit-1]
		if last.Id != nil {
			next = encodeListCursor(key, podSortVal(last, key), *last.Id, dir)
		}
		items = items[:limit]
	}
	return items, next, nil
}

// UpdatePod applies merge-patch semantics on mutable fields.
//
//nolint:gocyclo // merge-patch nil checks are inherently repetitive
func (p *PG) UpdatePod(ctx context.Context, id uuid.UUID, in api.PodUpdate) (api.Pod, error) {
	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	idx := 1
	appendSet := func(column string, value any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", column, idx))
		args = append(args, value)
		idx++
	}

	if in.Phase != nil {
		appendSet("phase", *in.Phase)
	}
	if in.NodeName != nil {
		appendSet("node_name", *in.NodeName)
	}
	if in.PodIp != nil {
		appendSet("pod_ip", *in.PodIp)
	}
	if in.WorkloadId != nil {
		appendSet("workload_id", *in.WorkloadId)
	}
	if in.Containers != nil {
		b, err := marshalPorts(in.Containers)
		if err != nil {
			return api.Pod{}, err
		}
		appendSet("containers", b)
	}
	if in.Labels != nil {
		b, err := marshalLabels(in.Labels)
		if err != nil {
			return api.Pod{}, err
		}
		appendSet("labels", b)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)

	q := fmt.Sprintf("UPDATE pods SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		if pErr := classifyPodFKError(err, uuid.Nil, in.WorkloadId); pErr != nil {
			return api.Pod{}, pErr
		}
		return api.Pod{}, fmt.Errorf("update pod: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.Pod{}, api.ErrNotFound
	}
	return p.GetPod(ctx, id)
}

// DeletePod removes a pod by id.
func (p *PG) DeletePod(ctx context.Context, id uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, "DELETE FROM pods WHERE id=$1", id)
	if err != nil {
		return fmt.Errorf("delete pod: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// UpsertPod inserts-or-updates a pod keyed by (namespace_id, name).
//
//nolint:gocritic,gocyclo // hugeParam: Store interface requires value param; CTE drives branch count
func (p *PG) UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, api.UpsertOutcome, error) {
	id := uuid.New()
	now := time.Now().UTC()

	labelsJSON, err := marshalLabels(in.Labels)
	if err != nil {
		return api.Pod{}, api.OutcomeNoChange, err
	}
	containersJSON, err := marshalPorts(in.Containers)
	if err != nil {
		return api.Pod{}, api.OutcomeNoChange, err
	}

	// AUDIT_BUSINESS_FIELDS: phase, node_name, pod_ip, workload_id,
	// containers, labels. updated_at is a clock field — excluded from
	// business_changed detection so reconcile-only ticks turn into NoChange.
	const q = `
		WITH old AS (
		  SELECT phase, node_name, pod_ip, workload_id, containers, labels
		    FROM pods WHERE namespace_id = $2 AND name = $3
		),
		upserted AS (
		  INSERT INTO pods (
		      id, namespace_id, name, phase, node_name, pod_ip,
		      workload_id, containers, labels, created_at, updated_at
		  ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		  ON CONFLICT (namespace_id, name) DO UPDATE SET
		      phase       = EXCLUDED.phase,
		      node_name   = EXCLUDED.node_name,
		      pod_ip      = EXCLUDED.pod_ip,
		      workload_id = EXCLUDED.workload_id,
		      containers  = EXCLUDED.containers,
		      labels      = EXCLUDED.labels,
		      updated_at  = EXCLUDED.updated_at
		  RETURNING id, namespace_id, name, phase, node_name, pod_ip,
		            workload_id, containers, labels, created_at, updated_at,
		            xmax
		)
		SELECT u.id, u.namespace_id, u.name, u.phase, u.node_name, u.pod_ip,
		       u.workload_id, u.containers, u.labels, u.created_at, u.updated_at,
		       (u.xmax = 0) AS inserted,
		       (u.xmax <> 0 AND (
		           o.phase       IS DISTINCT FROM u.phase       OR
		           o.node_name   IS DISTINCT FROM u.node_name   OR
		           o.pod_ip      IS DISTINCT FROM u.pod_ip      OR
		           o.workload_id IS DISTINCT FROM u.workload_id OR
		           o.containers  IS DISTINCT FROM u.containers  OR
		           o.labels      IS DISTINCT FROM u.labels
		       )) AS business_changed
		  FROM upserted u LEFT JOIN old o ON true
	`
	row := p.pool.QueryRow(ctx, q,
		id, in.NamespaceId, in.Name, in.Phase, in.NodeName, in.PodIp,
		in.WorkloadId, containersJSON, labelsJSON, now,
	)

	var (
		pod             api.Pod
		podID           uuid.UUID
		namespaceID     uuid.UUID
		createdAt       time.Time
		updatedAt       time.Time
		phase           sql.NullString
		nodeName        sql.NullString
		podIP           sql.NullString
		workloadID      *uuid.UUID
		containersOut   []byte
		labelsOut       []byte
		inserted        bool
		businessChanged bool
	)
	if err := row.Scan(
		&podID, &namespaceID, &pod.Name,
		&phase, &nodeName, &podIP,
		&workloadID, &containersOut, &labelsOut,
		&createdAt, &updatedAt,
		&inserted, &businessChanged,
	); err != nil {
		if pErr := classifyPodFKError(err, in.NamespaceId, in.WorkloadId); pErr != nil {
			return api.Pod{}, api.OutcomeNoChange, pErr
		}
		return api.Pod{}, api.OutcomeNoChange, fmt.Errorf("upsert pod: %w", err)
	}
	pod.Id = &podID
	pod.NamespaceId = namespaceID
	pod.CreatedAt = &createdAt
	pod.UpdatedAt = &updatedAt
	pod.Phase = nullableString(phase)
	pod.NodeName = nullableString(nodeName)
	pod.PodIp = nullableString(podIP)
	if workloadID != nil {
		pod.WorkloadId = workloadID
	}
	if cs, err := unmarshalContainers(containersOut); err != nil {
		return api.Pod{}, api.OutcomeNoChange, fmt.Errorf("unmarshal pod containers: %w", err)
	} else if cs != nil {
		pod.Containers = cs
	}
	if len(labelsOut) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsOut, &labels); err != nil {
			return api.Pod{}, api.OutcomeNoChange, fmt.Errorf("unmarshal pod labels: %w", err)
		}
		if len(labels) > 0 {
			pod.Labels = &labels
		}
	}
	return pod, classifyOutcome(inserted, businessChanged), nil
}

// DeletePodsNotIn removes every pod in the given namespace whose name is not
// in keepNames. Same COALESCE safety against pgx encoding a nil slice as NULL.
func (p *PG) DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM pods
		 WHERE namespace_id = $1
		   AND name <> ALL(COALESCE($2::text[], ARRAY[]::text[]))`,
		namespaceID, keepNames,
	)
	if err != nil {
		return 0, fmt.Errorf("delete pods not in: %w", err)
	}
	return tag.RowsAffected(), nil
}

func scanPod(row pgx.Row) (api.Pod, error) {
	var (
		p                  api.Pod
		id                 uuid.UUID
		namespaceID        uuid.UUID
		createdAt          time.Time
		updatedAt          time.Time
		phase              sql.NullString
		nodeName           sql.NullString
		podIP              sql.NullString
		workloadID         *uuid.UUID
		containersJSON     []byte
		labelsJSON         []byte
		namespaceName      sql.NullString
		namespaceClusterID uuid.NullUUID
		clusterName        sql.NullString
		workloadName       sql.NullString
	)
	if err := row.Scan(
		&id, &namespaceID, &p.Name,
		&phase, &nodeName, &podIP,
		&workloadID, &containersJSON, &labelsJSON,
		&createdAt, &updatedAt,
		&namespaceName, &namespaceClusterID, &clusterName,
		&workloadName,
	); err != nil {
		return api.Pod{}, fmt.Errorf("scan pod: %w", err)
	}
	p.Id = &id
	p.NamespaceId = namespaceID
	p.CreatedAt = &createdAt
	p.UpdatedAt = &updatedAt
	p.Phase = nullableString(phase)
	p.NodeName = nullableString(nodeName)
	p.PodIp = nullableString(podIP)
	if workloadID != nil {
		p.WorkloadId = workloadID
	}
	p.NamespaceName = nullableString(namespaceName)
	if namespaceClusterID.Valid {
		v := namespaceClusterID.UUID
		p.ClusterId = &v
	}
	p.ClusterName = nullableString(clusterName)
	p.WorkloadName = nullableString(workloadName)
	if cs, err := unmarshalContainers(containersJSON); err != nil {
		return api.Pod{}, fmt.Errorf("unmarshal pod containers: %w", err)
	} else if cs != nil {
		p.Containers = cs
	}
	if len(labelsJSON) > 0 {
		var labels map[string]string
		if err := json.Unmarshal(labelsJSON, &labels); err != nil {
			return api.Pod{}, fmt.Errorf("unmarshal pod labels: %w", err)
		}
		if len(labels) > 0 {
			p.Labels = &labels
		}
	}
	return p, nil
}
