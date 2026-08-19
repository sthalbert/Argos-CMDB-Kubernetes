package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

// PolicyReport aliases the shared row type so store methods read
// naturally (matches the NetworkPolicy alias pattern).
type PolicyReport = api.PolicyReportRow

const prSelect = `
	id, cluster_id, namespace_id, name,
	scope_kind, scope_name,
	summary_pass, summary_fail, summary_warn, summary_error, summary_skip,
	results_raw, source, reconcile_seen_at`

const prListSelect = `
	id, cluster_id, namespace_id, name,
	scope_kind, scope_name,
	summary_pass, summary_fail, summary_warn, summary_error, summary_skip,
	source, reconcile_seen_at`

// GetPolicyReport returns one report row by id; api.ErrNotFound when
// absent.
func (p *PG) GetPolicyReport(ctx context.Context, id uuid.UUID) (api.PolicyReportRow, error) {
	const q = `SELECT ` + prSelect + ` FROM policy_reports WHERE id = $1`
	var pr api.PolicyReportRow
	err := p.pool.QueryRow(ctx, q, id).Scan(
		&pr.ID, &pr.ClusterID, &pr.NamespaceID, &pr.Name,
		&pr.ScopeKind, &pr.ScopeName,
		&pr.SummaryPass, &pr.SummaryFail, &pr.SummaryWarn, &pr.SummaryError, &pr.SummarySkip,
		&pr.ResultsRaw,
		&pr.Source,
		&pr.ReconcileSeenAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return api.PolicyReportRow{}, api.ErrNotFound
	}
	if err != nil {
		return api.PolicyReportRow{}, fmt.Errorf("get policy_report: %w", err)
	}
	return pr, nil
}

// UpsertPolicyReport inserts or updates a report keyed on
// (cluster_id, namespace_id, name) and returns the stable row UUID.
// The guarded ON CONFLICT means an API write never overwrites a
// collector-managed row (api.ErrConflict instead); the collector
// overwrites anything.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) UpsertPolicyReport(ctx context.Context, pr PolicyReport) (uuid.UUID, error) {
	// A nil RawMessage would be encoded as SQL NULL (bypassing the
	// column's NOT NULL DEFAULT '[]' because the INSERT lists the column
	// explicitly), and the JSON literal "null" would be stored as jsonb
	// null — both would break consumers expecting an array. Normalise
	// here so every caller (handler, collector) gets the same contract.
	if len(pr.ResultsRaw) == 0 || string(pr.ResultsRaw) == "null" {
		pr.ResultsRaw = json.RawMessage(`[]`)
	}
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, `
		INSERT INTO policy_reports
			(cluster_id, namespace_id, name,
			 scope_kind, scope_name,
			 summary_pass, summary_fail, summary_warn, summary_error, summary_skip,
			 results_raw, source, reconcile_seen_at)
		VALUES ($1, $2, $3,
		       $4, $5,
		       $6, $7, $8, $9, $10,
		       $11, $12, NOW())
		ON CONFLICT (cluster_id, COALESCE(namespace_id, '00000000-0000-0000-0000-000000000000'), name) DO UPDATE SET
			scope_kind        = EXCLUDED.scope_kind,
			scope_name        = EXCLUDED.scope_name,
			summary_pass      = EXCLUDED.summary_pass,
			summary_fail      = EXCLUDED.summary_fail,
			summary_warn      = EXCLUDED.summary_warn,
			summary_error     = EXCLUDED.summary_error,
			summary_skip      = EXCLUDED.summary_skip,
			results_raw       = EXCLUDED.results_raw,
			source            = EXCLUDED.source,
			reconcile_seen_at = NOW()
		WHERE EXCLUDED.source = 'collector' OR policy_reports.source = 'api'
		RETURNING id`,
		pr.ClusterID, pr.NamespaceID, pr.Name,
		pr.ScopeKind, pr.ScopeName,
		pr.SummaryPass, pr.SummaryFail, pr.SummaryWarn, pr.SummaryError, pr.SummarySkip,
		pr.ResultsRaw,
		pr.Source,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, api.ErrConflict
		}
		if fkErr := classifyPolicyReportFKError(err, pr.ClusterID, pr.NamespaceID); fkErr != nil {
			return uuid.Nil, fkErr
		}
		return uuid.Nil, fmt.Errorf("upsert policy_report: %w", err)
	}
	return id, nil
}

// DeletePolicyReportsByNamespace sweeps collector-managed report rows
// of one namespace, keeping keepIDs (reconcile per ADR-0043 §5).
func (p *PG) DeletePolicyReportsByNamespace(ctx context.Context, clusterID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1 AND namespace_id=$2 AND source=$3`,
			clusterID, namespaceID, api.SourceCollector)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1 AND namespace_id=$2 AND source=$3 AND id <> ALL($4)`,
			clusterID, namespaceID, api.SourceCollector, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep policy_reports by namespace: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeleteClusterScopedPolicyReportsNotIn sweeps collector-managed
// cluster-scoped report rows, keeping keepIDs (reconcile per ADR-0043 §5).
func (p *PG) DeleteClusterScopedPolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error) {
	var ct pgconn.CommandTag
	var err error
	if len(keepIDs) == 0 {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1 AND namespace_id IS NULL AND source=$2`, clusterID, api.SourceCollector)
	} else {
		ct, err = p.pool.Exec(ctx,
			`DELETE FROM policy_reports WHERE cluster_id=$1 AND namespace_id IS NULL AND source=$2 AND id <> ALL($3)`,
			clusterID, api.SourceCollector, keepIDs)
	}
	if err != nil {
		return 0, fmt.Errorf("sweep cluster-scoped policy_reports: %w", err)
	}
	return ct.RowsAffected(), nil
}

// DeletePolicyReport deletes one API-authored row (source='api');
// api.ErrNotFound when the row is absent or collector-managed.
func (p *PG) DeletePolicyReport(ctx context.Context, id uuid.UUID) error {
	ct, err := p.pool.Exec(ctx,
		`DELETE FROM policy_reports WHERE id = $1 AND source = $2`,
		id, api.SourceAPI)
	if err != nil {
		return fmt.Errorf("delete policy_report: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

var policyReportSortSpec = sortSpec{
	columns: map[string]sortColumn{
		sortKeyName:            {expr: "LOWER(name)", kind: sortText},
		sortKeyScopeKind:       {expr: "LOWER(scope_kind)", kind: sortText, nullable: true},
		sortKeyScopeName:       {expr: "LOWER(scope_name)", kind: sortText, nullable: true},
		sortKeySummaryPass:     {expr: "summary_pass", kind: sortInt},
		sortKeySummaryFail:     {expr: "summary_fail", kind: sortInt},
		sortKeySummaryWarn:     {expr: "summary_warn", kind: sortInt},
		sortKeySummaryError:    {expr: "summary_error", kind: sortInt},
		sortKeySummarySkip:     {expr: "summary_skip", kind: sortInt},
		sortKeyReconcileSeenAt: {expr: "reconcile_seen_at", kind: sortTime},
	},
	defaultKey: sortKeyName,
	defaultDir: dirAsc,
}

func policyReportSortVal(r *api.PolicyReportRow, key string) *string {
	switch key {
	case sortKeyName:
		return sortValText(&r.Name)
	case sortKeyScopeKind:
		return sortValText(r.ScopeKind)
	case sortKeyScopeName:
		return sortValText(r.ScopeName)
	case sortKeySummaryPass:
		return sortValInt(intPtr(r.SummaryPass))
	case sortKeySummaryFail:
		return sortValInt(intPtr(r.SummaryFail))
	case sortKeySummaryWarn:
		return sortValInt(intPtr(r.SummaryWarn))
	case sortKeySummaryError:
		return sortValInt(intPtr(r.SummaryError))
	case sortKeySummarySkip:
		return sortValInt(intPtr(r.SummarySkip))
	default:
		return sortValTime(&r.ReconcileSeenAt)
	}
}

// ListPolicyReports returns a cursor-paginated page of report rows
// matching the filter (ADR-0042 sort/cursor semantics).
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListPolicyReports(
	ctx context.Context,
	filter api.PolicyReportListFilter,
	page api.ListPage,
) ([]api.PolicyReportRow, string, error) {
	limit := clampLimit(page.Limit, 200)
	key, col, dir, err := policyReportSortSpec.resolve(page)
	if err != nil {
		return nil, "", err
	}

	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(prListSelect)
	sb.WriteString(` FROM policy_reports`)

	conds := make([]string, 0, 4)
	args := make([]any, 0, 5)

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

	if filter.ScopeKind != nil {
		args = append(args, *filter.ScopeKind)
		conds = append(conds, fmt.Sprintf("LOWER(scope_kind) = LOWER($%d)", len(args)))
	}

	if filter.ScopeName != nil {
		args = append(args, namePattern(*filter.ScopeName))
		conds = append(conds, fmt.Sprintf("LOWER(scope_name) LIKE $%d ESCAPE '\\'", len(args)))
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
		return nil, "", fmt.Errorf("query policy_reports: %w", err)
	}
	defer rows.Close()

	raw := make([]api.PolicyReportRow, 0, limit+1)
	for rows.Next() {
		var r api.PolicyReportRow
		if err := rows.Scan(
			&r.ID, &r.ClusterID, &r.NamespaceID, &r.Name,
			&r.ScopeKind, &r.ScopeName,
			&r.SummaryPass, &r.SummaryFail, &r.SummaryWarn, &r.SummaryError, &r.SummarySkip,
			&r.Source,
			&r.ReconcileSeenAt,
		); err != nil {
			return nil, "", fmt.Errorf("scan policy_report: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate policy_reports: %w", err)
	}

	var next string
	if len(raw) > limit {
		last := &raw[limit-1]
		next = encodeListCursor(key, policyReportSortVal(last, key), last.ID, dir)
		raw = raw[:limit]
	}
	return raw, next, nil
}

// classifyPolicyReportFKError disambiguates 23503 foreign-key violations on
// the policy_reports table into cluster vs namespace misses.
func classifyPolicyReportFKError(err error, clusterID uuid.UUID, namespaceID *uuid.UUID) error {
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
