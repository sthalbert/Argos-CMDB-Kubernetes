package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

var (
	errKyvernoTickFailed   = errors.New("kyverno tick: one or more operations failed")
	errKyvernoSweepFailed  = errors.New("kyverno sweep failed")
	errKyvernoUpsertFailed = errors.New("kyverno upsert failed")
	// errKyvernoListForbidden marks an RBAC-denied Kyverno list. Unlike a
	// missing CRD (empty success — Kyverno uninstalled, rows legitimately
	// disappear), a 403 means the policies still exist but are invisible:
	// the tick must skip BOTH collection and sweep, or the reconcile
	// would wipe the whole collector-managed inventory.
	errKyvernoListForbidden = errors.New("kyverno list forbidden by RBAC")
)

// jsonNullLiteral is the 4-byte payload json.Marshal produces for a
// missing value; a json.RawMessage holding it is valid JSON but carries
// no data.
const jsonNullLiteral = "null"

// SettingsGetter is the minimal settings interface the Kyverno collector
// needs to gate itself behind policies_enabled. Satisfied by *store.PG
// (in-process mode). The push-mode apiclient.Store does not implement it
// — push-mode Kyverno collection is deferred (ADR-0043 NEG-008).
type SettingsGetter interface {
	GetSettings(ctx context.Context) (api.Settings, error)
}

// KyvernoStore is the slice of the store interface the Kyverno collector
// uses. Satisfied by *store.PG (direct, in-process). The push-mode
// apiclient.Store does not implement it and the ingest listener exposes
// no Kyverno routes — push-mode collection is deferred (ADR-0043
// NEG-008); collector.New logs once at startup when the store lacks
// support. Defined here so a test fake can stub without dragging the
// full store. ADR-0043.
type KyvernoStore interface {
	UpsertClusterPolicy(ctx context.Context, cp api.ClusterPolicyRow) (uuid.UUID, error)
	DeleteClusterScopedPoliciesNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	DeleteClusterPoliciesByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	UpsertPolicyReport(ctx context.Context, pr api.PolicyReportRow) (uuid.UUID, error)
	DeleteClusterScopedPolicyReportsNotIn(ctx context.Context, clusterID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
	DeletePolicyReportsByNamespace(ctx context.Context, clusterID uuid.UUID, namespaceID uuid.UUID, keepIDs []uuid.UUID) (int64, error)
}

// CollectKyvernoPolicies runs one tick of Kyverno policy and policy-report
// reconciliation for the local cluster. Issues FOUR list calls (two
// cluster-scoped, two namespace-wide) via the dynamic client, groups
// results, upserts each row, and sweeps stale entries per-namespace.
// ADR-0043 §5.
//
// The sweep is per-namespace (following the netpol pattern from
// ADR-0038): only known namespaces are swept, so policies in unknown
// namespaces survive the reconcile tick. Cluster-scoped policies are
// swept separately with DeleteClusterScopedPoliciesNotIn.
//
// MUST only be called after the kube list calls succeed — transient API
// errors must never wipe the store (reconcile contract per CLAUDE.md).
//
//nolint:gocyclo // forbidden-vs-failed classification for two resource families
func CollectKyvernoPolicies(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) error {
	var policyCollectErrs, policySweepErrs int
	var reportCollectErrs, reportSweepErrs int

	cpResult, cpErr := collectClusterPolicies(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	cpForbidden := errors.Is(cpErr, errKyvernoListForbidden)
	switch {
	case cpForbidden:
		// Expected on installs whose collector credentials predate the
		// Kyverno clusterrole rules: skip collection AND sweep without
		// failing the tick — sweeping would wipe the inventory.
		slog.Warn("collector: kyverno cluster-policies list forbidden by RBAC; skipping collection and sweep this tick",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", cpErr))
	case cpErr != nil:
		slog.Warn("collector: kyverno cluster-policies tick partially failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", cpErr))
		policyCollectErrs++
	}
	if cpResult == nil {
		if !cpForbidden {
			slog.Warn("collector: skipping kyverno cluster-policies sweep due to list failure",
				slog.String("cluster", clusterID.String()))
			policySweepErrs++
		}
	} else {
		deleted, err := sweepClusterPolicies(ctx, st, clusterID, clusterName, namespaceIDsByName, cpResult)
		if err != nil {
			slog.Warn("collector: sweep kyverno cluster-policies failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "reconcile")
			policySweepErrs++
		} else {
			metrics.ObserveReconciled(clusterName, "cluster_policies", deleted)
			metrics.MarkPoll(clusterName, "cluster_policies")
		}
	}

	prResult, prErr := collectPolicyReports(ctx, src, st, clusterID, clusterName, namespaceIDsByName)
	prForbidden := errors.Is(prErr, errKyvernoListForbidden)
	switch {
	case prForbidden:
		slog.Warn("collector: kyverno policy-reports list forbidden by RBAC; skipping collection and sweep this tick",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", prErr))
	case prErr != nil:
		slog.Warn("collector: kyverno policy-reports tick partially failed",
			slog.String("cluster", clusterID.String()),
			slog.Any("err", prErr))
		reportCollectErrs++
	}
	if prResult == nil {
		if !prForbidden {
			slog.Warn("collector: skipping kyverno policy-reports sweep due to list failure",
				slog.String("cluster", clusterID.String()))
			reportSweepErrs++
		}
	} else {
		deleted, err := sweepPolicyReports(ctx, st, clusterID, clusterName, namespaceIDsByName, prResult)
		if err != nil {
			slog.Warn("collector: sweep kyverno policy-reports failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "reconcile")
			reportSweepErrs++
		} else {
			metrics.ObserveReconciled(clusterName, "policy_reports", deleted)
			metrics.MarkPoll(clusterName, "policy_reports")
		}
	}

	policyTotal := policyCollectErrs + policySweepErrs
	reportTotal := reportCollectErrs + reportSweepErrs
	if policyTotal+reportTotal > 0 {
		return fmt.Errorf("%w (policy: %d collect/%d sweep, report: %d collect/%d sweep)",
			errKyvernoTickFailed, policyCollectErrs, policySweepErrs, reportCollectErrs, reportSweepErrs)
	}
	return nil
}

type kyvernoSweepResult struct {
	clusterScoped      []uuid.UUID
	byNamespace        map[uuid.UUID][]uuid.UUID
	clusterScopedDirty bool
	dirtyNamespaces    map[uuid.UUID]struct{}
}

func newKyvernoSweepResult() *kyvernoSweepResult {
	return &kyvernoSweepResult{
		clusterScoped:   make([]uuid.UUID, 0),
		byNamespace:     make(map[uuid.UUID][]uuid.UUID),
		dirtyNamespaces: make(map[uuid.UUID]struct{}),
	}
}

func (r *kyvernoSweepResult) addClusterScoped(id uuid.UUID) {
	r.clusterScoped = append(r.clusterScoped, id)
}

func (r *kyvernoSweepResult) markClusterScopedDirty() {
	r.clusterScopedDirty = true
}

func (r *kyvernoSweepResult) addNamespaced(nsID, id uuid.UUID) {
	r.byNamespace[nsID] = append(r.byNamespace[nsID], id)
}

func (r *kyvernoSweepResult) markNamespaceDirty(nsID uuid.UUID) {
	r.dirtyNamespaces[nsID] = struct{}{}
}

func sweepClusterPolicies(
	ctx context.Context,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
	result *kyvernoSweepResult,
) (int64, error) {
	var total int64
	var sweepErrors int

	if !result.clusterScopedDirty {
		n, err := st.DeleteClusterScopedPoliciesNotIn(ctx, clusterID, result.clusterScoped)
		if err != nil {
			slog.Error("collector: sweep cluster-scoped policies failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("error", err))
			sweepErrors++
		} else {
			total += n
		}
	} else {
		slog.Warn("collector: skipping cluster-scoped policy sweep due to upsert errors",
			slog.String("cluster", clusterID.String()))
	}

	for _, nsID := range namespaceIDsByName {
		if _, dirty := result.dirtyNamespaces[nsID]; dirty {
			slog.Warn("collector: skipping namespace policy sweep due to upsert errors",
				slog.String("cluster", clusterID.String()),
				slog.String("namespace_id", nsID.String()))
			continue
		}
		keep := result.byNamespace[nsID]
		n, err := st.DeleteClusterPoliciesByNamespace(ctx, clusterID, nsID, keep)
		if err != nil {
			slog.Error("collector: sweep cluster_policies by namespace failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster", clusterName))
			sweepErrors++
			continue
		}
		total += n
	}

	if sweepErrors > 0 {
		return total, fmt.Errorf("%w: %d sweep errors", errKyvernoSweepFailed, sweepErrors)
	}
	return total, nil
}

func sweepPolicyReports(
	ctx context.Context,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
	result *kyvernoSweepResult,
) (int64, error) {
	var total int64
	var sweepErrors int

	if !result.clusterScopedDirty {
		n, err := st.DeleteClusterScopedPolicyReportsNotIn(ctx, clusterID, result.clusterScoped)
		if err != nil {
			slog.Error("collector: sweep cluster-scoped policy_reports failed",
				slog.String("cluster", clusterID.String()),
				slog.Any("error", err))
			sweepErrors++
		} else {
			total += n
		}
	} else {
		slog.Warn("collector: skipping cluster-scoped policy_report sweep due to upsert errors",
			slog.String("cluster", clusterID.String()))
	}

	for _, nsID := range namespaceIDsByName {
		if _, dirty := result.dirtyNamespaces[nsID]; dirty {
			slog.Warn("collector: skipping namespace policy_report sweep due to upsert errors",
				slog.String("cluster", clusterID.String()),
				slog.String("namespace_id", nsID.String()))
			continue
		}
		keep := result.byNamespace[nsID]
		n, err := st.DeletePolicyReportsByNamespace(ctx, clusterID, nsID, keep)
		if err != nil {
			slog.Error("collector: sweep policy_reports by namespace failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster", clusterName))
			sweepErrors++
			continue
		}
		total += n
	}

	if sweepErrors > 0 {
		return total, fmt.Errorf("%w: %d sweep errors", errKyvernoSweepFailed, sweepErrors)
	}
	return total, nil
}

// collectClusterPolicies upserts all Kyverno ClusterPolicy + Policy rows
// and returns the sweep result (cluster-scoped IDs + IDs grouped by
// namespace) for per-namespace reconcile.
//
//nolint:gocyclo // two list+upsert loops with per-row dirty tracking
func collectClusterPolicies(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) (*kyvernoSweepResult, error) {
	result := newKyvernoSweepResult()
	var listErrors int

	clusterPol, err := src.ListKyvernoClusterPolicies(ctx)
	if err != nil {
		if !errors.Is(err, errKyvernoListForbidden) {
			metrics.ObserveError(clusterName, "cluster_policies", "list")
		}
		return nil, fmt.Errorf("list kyverno clusterpolicies: %w", err)
	}
	for i := range clusterPol {
		cp := &clusterPol[i]
		row := kyvernoPolicyToRow(cp, clusterID, nil)
		id, err := st.UpsertClusterPolicy(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno clusterpolicy failed",
				slog.String("policy", cp.Name),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "upsert")
			result.markClusterScopedDirty()
			listErrors++
			continue
		}
		result.addClusterScoped(id)
	}

	namespacedPol, err := src.ListKyvernoPolicies(ctx)
	if err != nil {
		if !errors.Is(err, errKyvernoListForbidden) {
			metrics.ObserveError(clusterName, "cluster_policies", "list")
		}
		return nil, fmt.Errorf("list kyverno policies: %w", err)
	}
	for i := range namespacedPol {
		p := &namespacedPol[i]
		nsID, ok := namespaceIDsByName[p.Namespace]
		if !ok {
			slog.Warn("collector: kyverno policy in unknown namespace; skipping",
				slog.String("policy", p.Name),
				slog.String("namespace", p.Namespace),
				slog.String("cluster", clusterID.String()))
			metrics.ObserveError(clusterName, "cluster_policies", "namespace_unknown")
			continue
		}
		row := kyvernoPolicyToRow(p, clusterID, &nsID)
		id, err := st.UpsertClusterPolicy(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno policy failed",
				slog.String("policy", p.Name),
				slog.String("namespace", p.Namespace),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "cluster_policies", "upsert")
			result.markNamespaceDirty(nsID)
			listErrors++
			continue
		}
		result.addNamespaced(nsID, id)
	}

	totalUpserted := len(result.clusterScoped)
	for _, ids := range result.byNamespace {
		totalUpserted += len(ids)
	}
	metrics.ObserveUpserts(clusterName, "cluster_policies", totalUpserted)
	if listErrors > 0 {
		return result, fmt.Errorf("%w: %d cluster-policy upsert errors", errKyvernoUpsertFailed, listErrors)
	}
	return result, nil
}

// collectPolicyReports upserts all PolicyReport + ClusterPolicyReport rows
// and returns the sweep result (cluster-scoped IDs + IDs grouped by
// namespace) for per-namespace reconcile.
//
//nolint:gocyclo // two list+upsert loops with per-row dirty tracking
func collectPolicyReports(
	ctx context.Context,
	src KubeSource,
	st KyvernoStore,
	clusterID uuid.UUID,
	clusterName string,
	namespaceIDsByName map[string]uuid.UUID,
) (*kyvernoSweepResult, error) {
	result := newKyvernoSweepResult()
	var listErrors int

	clusterReports, err := src.ListKyvernoClusterPolicyReports(ctx)
	if err != nil {
		if !errors.Is(err, errKyvernoListForbidden) {
			metrics.ObserveError(clusterName, "policy_reports", "list")
		}
		return nil, fmt.Errorf("list kyverno clusterpolicyreports: %w", err)
	}
	for i := range clusterReports {
		r := &clusterReports[i]
		row := kyvernoReportToRow(r, clusterID, nil)
		id, err := st.UpsertPolicyReport(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno clusterpolicyreport failed",
				slog.String("report", r.Name),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "upsert")
			result.markClusterScopedDirty()
			listErrors++
			continue
		}
		result.addClusterScoped(id)
	}

	namespacedReports, err := src.ListKyvernoPolicyReports(ctx)
	if err != nil {
		if !errors.Is(err, errKyvernoListForbidden) {
			metrics.ObserveError(clusterName, "policy_reports", "list")
		}
		return nil, fmt.Errorf("list kyverno policyreports: %w", err)
	}
	for i := range namespacedReports {
		r := &namespacedReports[i]
		nsID, ok := namespaceIDsByName[r.Namespace]
		if !ok {
			slog.Warn("collector: kyverno policyreport in unknown namespace; skipping",
				slog.String("report", r.Name),
				slog.String("namespace", r.Namespace),
				slog.String("cluster", clusterID.String()))
			metrics.ObserveError(clusterName, "policy_reports", "namespace_unknown")
			continue
		}
		row := kyvernoReportToRow(r, clusterID, &nsID)
		id, err := st.UpsertPolicyReport(ctx, row)
		if err != nil {
			slog.Warn("collector: upsert kyverno policyreport failed",
				slog.String("report", r.Name),
				slog.String("namespace", r.Namespace),
				slog.String("cluster", clusterID.String()),
				slog.Any("err", err))
			metrics.ObserveError(clusterName, "policy_reports", "upsert")
			result.markNamespaceDirty(nsID)
			listErrors++
			continue
		}
		result.addNamespaced(nsID, id)
	}

	totalUpserted := len(result.clusterScoped)
	for _, ids := range result.byNamespace {
		totalUpserted += len(ids)
	}
	metrics.ObserveUpserts(clusterName, "policy_reports", totalUpserted)
	if listErrors > 0 {
		return result, fmt.Errorf("%w: %d policy-report upsert errors", errKyvernoUpsertFailed, listErrors)
	}
	return result, nil
}

// kyvernoPolicyToRow converts a KyvernoClusterPolicyInfo to an
// api.ClusterPolicyRow for upsert.
func kyvernoPolicyToRow(info *KyvernoClusterPolicyInfo, clusterID uuid.UUID, namespaceID *uuid.UUID) api.ClusterPolicyRow {
	now := time.Now().UTC()
	// json.Marshal of a missing field yields the literal "null", which is
	// valid JSON — treat it like absent data: SQL NULL for the nullable
	// annotations column, {} for the NOT NULL spec_raw column.
	annotations := json.RawMessage(info.Annotations)
	specRaw := json.RawMessage(info.SpecRaw)
	if len(annotations) == 0 || !json.Valid(annotations) || string(annotations) == jsonNullLiteral {
		annotations = nil
	}
	if len(specRaw) == 0 || !json.Valid(specRaw) || string(specRaw) == jsonNullLiteral {
		specRaw = json.RawMessage(`{}`)
	}
	row := api.ClusterPolicyRow{
		ClusterID:       clusterID,
		NamespaceID:     namespaceID,
		Name:            info.Name,
		ResourceType:    info.ResourceType,
		Scope:           info.Scope,
		Description:     info.Description,
		Category:        info.Category,
		Severity:        info.Severity,
		Action:          info.Action,
		FailurePolicy:   info.FailurePolicy,
		Background:      info.Background,
		RuleTypes:       info.RuleTypes,
		TargetResources: info.TargetResources,
		KeyExclusions:   info.KeyExclusions,
		Ready:           info.Ready,
		Annotations:     annotations,
		SpecRaw:         specRaw,
		Source:          api.SourceCollector,
		ReconcileSeenAt: now,
	}
	if info.RulesCount > 0 {
		rc := info.RulesCount
		row.RulesCount = &rc
	}
	return row
}

// kyvernoReportToRow converts a KyvernoPolicyReportInfo to an
// api.PolicyReportRow for upsert.
func kyvernoReportToRow(info *KyvernoPolicyReportInfo, clusterID uuid.UUID, namespaceID *uuid.UUID) api.PolicyReportRow {
	// A report with no results field marshals to the literal "null" —
	// normalise to nil so the store's nil→[] default applies instead of
	// persisting jsonb null where the schema promises an array.
	resultsRaw := json.RawMessage(info.ResultsRaw)
	if len(resultsRaw) == 0 || !json.Valid(resultsRaw) || string(resultsRaw) == jsonNullLiteral {
		resultsRaw = nil
	}
	return api.PolicyReportRow{
		ClusterID:       clusterID,
		NamespaceID:     namespaceID,
		Name:            info.Name,
		ScopeKind:       info.ScopeKind,
		ScopeName:       info.ScopeName,
		SummaryPass:     info.SummaryPass,
		SummaryFail:     info.SummaryFail,
		SummaryWarn:     info.SummaryWarn,
		SummaryError:    info.SummaryError,
		SummarySkip:     info.SummarySkip,
		ResultsRaw:      resultsRaw,
		Source:          api.SourceCollector,
		ReconcileSeenAt: time.Now().UTC(),
	}
}
