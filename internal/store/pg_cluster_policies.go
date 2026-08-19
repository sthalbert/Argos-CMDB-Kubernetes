package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

// ClusterPolicy aliases the shared row type so store methods read
// naturally (matches the NetworkPolicy alias pattern).
type ClusterPolicy = api.ClusterPolicyRow

const cpSelect = `
	id, cluster_id, namespace_id, name,
	resource_type, scope, description, category, severity,
	action, failure_policy, background,
	rule_types, rules_count, target_resources, key_exclusions,
	ready, annotations, spec_raw, source, reconcile_seen_at`

// GetClusterPolicy returns one policy row by id; api.ErrNotFound when
// absent.
func (p *PG) GetClusterPolicy(ctx context.Context, id uuid.UUID) (api.ClusterPolicyRow, error) {
	const q = `SELECT ` + cpSelect + ` FROM cluster_policies WHERE id = $1`
	var cp api.ClusterPolicyRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&cp.ID, &cp.ClusterID, &cp.NamespaceID, &cp.Name,
		&cp.ResourceType, &cp.Scope, &cp.Description, &cp.Category, &cp.Severity,
		&cp.Action, &cp.FailurePolicy, &cp.Background,
		&cp.RuleTypes, &cp.RulesCount, &cp.TargetResources, &cp.KeyExclusions,
		&cp.Ready, &cp.Annotations, &cp.SpecRaw,
		&cp.Source,
		&cp.ReconcileSeenAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ClusterPolicyRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.ClusterPolicyRow{}, fmt.Errorf("get cluster_policy: %w", err)
	}
	return cp, nil
}

// UpsertClusterPolicy inserts or updates a policy keyed on
// (cluster_id, namespace_id, name) and returns the stable row UUID.
// The guarded ON CONFLICT means an API write never overwrites a
// collector-managed row (api.ErrConflict instead); the collector
// overwrites anything.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertClusterPolicy(ctx context.Context, cp ClusterPolicy) (uuid.UUID, error) {
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, `
		INSERT INTO cluster_policies
			(cluster_id, namespace_id, name,
			 resource_type, scope, description, category, severity,
			 action, failure_policy, background,
			 rule_types, rules_count, target_resources, key_exclusions,
			 ready, annotations, spec_raw, source, reconcile_seen_at)
		VALUES ($1, $2, $3,
		        $4, $5, $6, $7, $8,
		        $9, $10, $11,
		        $12, $13, $14, $15,
		        $16, $17, $18, $19, NOW())
		ON CONFLICT (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name) DO UPDATE SET
			resource_type      = EXCLUDED.resource_type,
			scope              = EXCLUDED.scope,
			description        = EXCLUDED.description,
			category           = EXCLUDED.category,
			severity           = EXCLUDED.severity,
			action             = EXCLUDED.action,
			failure_policy     = EXCLUDED.failure_policy,
			background         = EXCLUDED.background,
			rule_types         = EXCLUDED.rule_types,
			rules_count        = EXCLUDED.rules_count,
			target_resources   = EXCLUDED.target_resources,
			key_exclusions     = EXCLUDED.key_exclusions,
			ready              = EXCLUDED.ready,
			annotations        = EXCLUDED.annotations,
			spec_raw           = EXCLUDED.spec_raw,
			source             = EXCLUDED.source,
			reconcile_seen_at  = NOW()
		WHERE EXCLUDED.source = 'collector' OR cluster_policies.source = 'api'
		RETURNING id`,
		cp.ClusterID, cp.NamespaceID, cp.Name,
		cp.ResourceType, cp.Scope, cp.Description, cp.Category, cp.Severity,
		cp.Action, cp.FailurePolicy, cp.Background,
		cp.RuleTypes, cp.RulesCount, cp.TargetResources, cp.KeyExclusions,
		cp.Ready, cp.Annotations, cp.SpecRaw,
		cp.Source,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, api.ErrConflict
		}
		if fkErr := classifyClusterPolicyFKError(err, cp.ClusterID, cp.NamespaceID); fkErr != nil {
			return uuid.Nil, fkErr
		}
		return uuid.Nil, fmt.Errorf("upsert cluster_policy: %w", err)
	}
	return id, nil
}

// DeleteClusterPoliciesByNamespace sweeps collector-managed policy rows
// of one namespace, keeping keepIDs (reconcile per ADR-0043 §5).
func (p *PG) DeleteClusterPoliciesByNamespace(ctx context.Context, clusterID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1 AND namespace_id=$2 AND source=$3`,
			clusterID, namespaceID, api.SourceCollector)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1 AND namespace_id=$2 AND source=$3 AND id <> ALL($4)`,
			clusterID, namespaceID, api.SourceCollector, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep cluster_policies by namespace: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeleteClusterScopedPoliciesNotIn sweeps collector-managed
// cluster-scoped policy rows, keeping keepIDs (reconcile per ADR-0043 §5).
func (p *PG) DeleteClusterScopedPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1 AND namespace_id IS NULL AND source=$2`, clusterID, api.SourceCollector)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM cluster_policies WHERE cluster_id=$1 AND namespace_id IS NULL AND source=$2 AND id <> ALL($3)`,
			clusterID, api.SourceCollector, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep cluster-scoped policies: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeleteClusterPolicy deletes one API-authored row (source='api');
// api.ErrNotFound when the row is absent or collector-managed.
func (p *PG) DeleteClusterPolicy(ctx context.Context, id uuid.UUID) error {
	ct, err := p.pool.Exec(ctx,
		`DELETE FROM cluster_policies WHERE id = $1 AND source = $2`,
		id, api.SourceAPI)
	if err != nil {
		return fmt.Errorf("delete cluster_policy: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

var clusterPolicySortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName: {expr: "LOWER(name)", kind: sortText},
		sortKeyAction: {
			expr:     "LOWER(action)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyBackground: {
			expr:     "background::int",
			kind:     sortInt,
			nullable: true,
		},
		sortKeySeverity: {
			// SQL CASE returns -1 for NULL or unknown severity values,
			// so the expression is effectively non-nullable.
			expr: `CASE LOWER(severity) ` +
				`WHEN 'critical' THEN 4 ` +
				`WHEN 'high' THEN 3 ` +
				`WHEN 'medium' THEN 2 ` +
				`WHEN 'low' THEN 1 ` +
				`WHEN 'info' THEN 0 ` +
				`ELSE -1 END`,
			kind:     sortInt,
			nullable: false,
		},
		sortKeyRulesCount: {
			expr:     "rules_count",
			kind:     sortInt,
			nullable: true,
		},
		sortKeyFailurePolicy: {
			expr:     "LOWER(failure_policy)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyCategory: {
			expr:     "LOWER(category)",
			kind:     sortText,
			nullable: true,
		},
		sortKeyReady: {
			expr:     "ready::int",
			kind:     sortInt,
			nullable: true,
		},
		sortKeyResourceType:    {expr: "LOWER(resource_type)", kind: sortText},
		sortKeyScope:           {expr: "LOWER(scope)", kind: sortText},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyName,
	defaultDir: dirAsc,
}

//nolint:gocyclo // per-sort-key dispatch; each case is trivial
func clusterPolicySortVal(r *api.ClusterPolicyRow, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.Name)
	case sortKeyResourceType:
		return sortValText(&r.ResourceType)
	case sortKeyScope:
		return sortValText(&r.Scope)
	case sortKeyAction:
		return sortValText(r.Action)
	case sortKeySeverity:
		return sortValInt(severityRank(r.Severity))
	case sortKeyFailurePolicy:
		return sortValText(r.FailurePolicy)
	case sortKeyCategory:
		return sortValText(r.Category)
	case sortKeyBackground:
		return sortValInt(boolToIntPtr(r.Background))
	case sortKeyReady:
		return sortValInt(boolToIntPtr(r.Ready))
	case sortKeyRulesCount:
		return sortValInt(r.RulesCount)
	default:
		return sortValTime(&r.ReconcileSeenAt)
	}
}

func severityRank(s *string) *int {
	if s == nil {
		return intPtr(-1)
	}
	switch strings.ToLower(*s) {
	case "critical":
		return intPtr(4)
	case "high":
		return intPtr(3)
	case "medium":
		return intPtr(2)
	case "low":
		return intPtr(1)
	case "info":
		return intPtr(0)
	default:
		return intPtr(-1)
	}
}

// ListClusterPolicies returns a cursor-paginated page of policy rows
// matching the filter (ADR-0042 sort/cursor semantics).
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListClusterPolicies(
	ctx context.Context,
	filter api.ClusterPolicyListFilter,
	page api.ListPage,
) ([]api.ClusterPolicyRow, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := clusterPolicySortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(cpSelect)
	sb.WriteString(` FROM cluster_policies`)

	conds := make([]string, 0, 8)
	args := make([]any, 0, 9)

	if filter.ClusterID != nil {
		args = append(args, *filter.ClusterID)
		conds = append(conds, fmt.Sprintf("cluster_id = $%d", len(args)))
	}

	if filter.NamespaceID != nil {
		args = append(args, *filter.NamespaceID)
		conds = append(conds, fmt.Sprintf("namespace_id = $%d", len(args)))
	}

	if filter.Name != nil {
		args = append(args, namePattern(*filter.Name))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE $%d ESCAPE '\\'", len(args)))
	}

	if filter.ResourceType != nil {
		args = append(args, *filter.ResourceType)
		conds = append(conds, fmt.Sprintf("LOWER(resource_type) = LOWER($%d)", len(args)))
	}

	if filter.Action != nil {
		args = append(args, strings.ToLower(*filter.Action))
		conds = append(conds, fmt.Sprintf("LOWER(action) = $%d", len(args)))
	}

	if filter.Severity != nil {
		args = append(args, strings.ToLower(*filter.Severity))
		conds = append(conds, fmt.Sprintf("LOWER(severity) = $%d", len(args)))
	}

	if filter.FailurePolicy != nil {
		args = append(args, strings.ToLower(*filter.FailurePolicy))
		conds = append(conds, fmt.Sprintf("LOWER(failure_policy) = $%d", len(args)))
	}

	if filter.Category != nil {
		args = append(args, namePattern(*filter.Category))
		conds = append(conds, fmt.Sprintf("LOWER(category) LIKE $%d ESCAPE '\\'", len(args)))
	}

	if page.Cursor != "" {
		val, cid, curErr := decodeListCursor(page.Cursor, key, dir)
		if curErr != nil {
			return nil, "", curErr
		}
		if curErr := keysetCond(col, "id", dir, val, cid, &conds, &args); curErr != nil {
			return nil, "", curErr
		}
	}

	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " %s LIMIT $%d", orderBy(col, "id", dir), len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query cluster_policies: %w", err)
	}
	defer rows.Close()

	raw := make([]api.ClusterPolicyRow, 0, limit+1)
	for rows.Next() {
		var r api.ClusterPolicyRow
		if err := rows.Scan(
			&r.ID, &r.ClusterID, &r.NamespaceID, &r.Name,
			&r.ResourceType, &r.Scope, &r.Description, &r.Category, &r.Severity,
			&r.Action, &r.FailurePolicy, &r.Background,
			&r.RuleTypes, &r.RulesCount, &r.TargetResources, &r.KeyExclusions,
			&r.Ready, &r.Annotations, &r.SpecRaw,
			&r.Source,
			&r.ReconcileSeenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan cluster_policy: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate cluster_policies: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := &raw[limit-1]
		next = encodeListCursor(key, clusterPolicySortVal(last, key), last.ID, dir)
		raw = raw[:limit]
	}
	return raw, next, nil
}

// classifyClusterPolicyFKError disambiguates 23503 foreign-key violations on
// the cluster_policies table into cluster vs namespace misses, so the POST
// handler can return an accurate 4xx.
func classifyClusterPolicyFKError(err error, clusterID uuid.UUID, namespaceID *uuid.UUID) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		return nil
	}
	if strings.Contains(pgErr.ConstraintName, "namespace_id") {
		target := nilUUIDDisplay
		if namespaceID != nil {
			target = namespaceID.String()
		}
		return fmt.Errorf("namespace %s does not exist: %w", target, api.ErrNotFound)
	}
	return fmt.Errorf("cluster %s does not exist: %w", clusterID, api.ErrNotFound)
}
