package eol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/metrics"
)

const annotationPrefix = "argos.io/eol."

// EnricherStore is the narrow subset of api.Store the enricher needs.
type EnricherStore interface {
	GetSettings(ctx context.Context) (api.Settings, error)
	ListClusters(ctx context.Context, limit int, cursor string) ([]api.Cluster, string, error)
	GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error)
	UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error)
	ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Node, string, error)
	GetNode(ctx context.Context, id uuid.UUID) (api.Node, error)
	UpdateNode(ctx context.Context, id uuid.UUID, in api.NodeUpdate) (api.Node, error)
	ListVirtualMachines(ctx context.Context, filter api.VirtualMachineListFilter, limit int, cursor string) ([]api.VirtualMachine, string, error)
	GetVirtualMachine(ctx context.Context, id uuid.UUID) (api.VirtualMachine, error)
	UpdateVirtualMachine(ctx context.Context, id uuid.UUID, in api.VirtualMachinePatch) (api.VirtualMachine, error)
}

// Enricher periodically queries endoflife.date and annotates CMDB
// entities with lifecycle status. Architecture follows the same
// goroutine pattern as the collector (ADR-0012).
type Enricher struct {
	store           EnricherStore
	client          *Client
	interval        time.Duration
	approachingDays int
}

// NewEnricher creates an Enricher. approachingDays defines the window
// before EOL where status becomes "approaching_eol".
func NewEnricher(store EnricherStore, client *Client, interval time.Duration, approachingDays int) *Enricher {
	return &Enricher{
		store:           store,
		client:          client,
		interval:        interval,
		approachingDays: approachingDays,
	}
}

// Run starts the enrichment loop. It runs once immediately, then on
// each tick until ctx is cancelled. Errors are logged and swallowed;
// the ticker is unaffected.
func (e *Enricher) Run(ctx context.Context) error {
	slog.Info("eol enricher started", slog.String("interval", e.interval.String()))
	e.enrich(ctx)

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("eol enricher stopped")
			return fmt.Errorf("eol enricher: %w", ctx.Err())
		case <-ticker.C:
			e.enrich(ctx)
		}
	}
}

func (e *Enricher) enrich(ctx context.Context) {
	// Check the DB setting — skip when disabled by admin.
	settings, err := e.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("eol enricher: read settings failed, skipping tick", slog.Any("error", err))
		return
	}
	if !settings.EOLEnabled {
		slog.Debug("eol enricher: disabled via settings, skipping tick")
		return
	}

	slog.Debug("eol enricher: tick started")

	cursor := ""
	for {
		clusters, next, err := e.store.ListClusters(ctx, 100, cursor)
		if err != nil {
			slog.Error("eol enricher: list clusters", slog.Any("error", err))
			metrics.ObserveEOLError("_all_", "cluster", "list")
			return
		}
		for i := range clusters {
			e.enrichCluster(ctx, &clusters[i])
		}
		if next == "" {
			break
		}
		cursor = next
	}

	e.enrichVirtualMachines(ctx)

	metrics.MarkEOLRun()
	slog.Debug("eol enricher: tick completed")
}

// enrichVirtualMachines walks all non-terminated VMs and writes
// `argos.io/eol.<product>` annotations driven by the operator-declared
// applications list. Filter intentionally omits IncludeTerminated so
// soft-deleted rows don't pile up annotation churn.
func (e *Enricher) enrichVirtualMachines(ctx context.Context) {
	cursor := ""
	for {
		vms, next, err := e.store.ListVirtualMachines(ctx, api.VirtualMachineListFilter{}, 100, cursor)
		if err != nil {
			slog.Error("eol enricher: list virtual machines", slog.Any("error", err))
			metrics.ObserveEOLError("_all_", "vm", "list")
			return
		}
		for i := range vms {
			e.enrichVirtualMachine(ctx, &vms[i])
		}
		if next == "" {
			break
		}
		cursor = next
	}
}

func (e *Enricher) enrichCluster(ctx context.Context, cluster *api.Cluster) {
	if cluster.Id == nil {
		return
	}
	clusterID := *cluster.Id
	clusterName := cluster.Name

	e.enrichClusterVersion(ctx, clusterID, clusterName, cluster.KubernetesVersion)
	e.enrichClusterNodes(ctx, clusterID, clusterName)
}

func (e *Enricher) enrichClusterVersion(ctx context.Context, clusterID uuid.UUID, clusterName string, version *string) {
	if version == nil || *version == "" {
		return
	}
	matcher := KubernetesMatcher{}
	mr, ok := matcher.Match(*version)
	if !ok {
		return
	}
	ann, err := e.resolveAnnotation(ctx, mr)
	if err != nil {
		slog.Warn("eol enricher: resolve cluster version",
			slog.Any("error", err), slog.String("cluster", clusterName))
		metrics.ObserveEOLError(clusterName, "cluster", "resolve")
		return
	}
	if ann == nil {
		return
	}
	if err := e.mergeClusterAnnotation(ctx, clusterID, mr.Product, ann); err != nil {
		slog.Warn("eol enricher: update cluster annotations",
			slog.Any("error", err), slog.String("cluster", clusterName))
		metrics.ObserveEOLError(clusterName, "cluster", "update")
		return
	}
	metrics.ObserveEOLEnrichment(clusterName, "cluster", string(ann.EOLStatus))
}

func (e *Enricher) enrichClusterNodes(ctx context.Context, clusterID uuid.UUID, clusterName string) {
	nodeCursor := ""
	for {
		nodes, next, err := e.store.ListNodes(ctx, &clusterID, 100, nodeCursor)
		if err != nil {
			slog.Error("eol enricher: list nodes",
				slog.Any("error", err), slog.String("cluster", clusterName))
			metrics.ObserveEOLError(clusterName, "node", "list")
			return
		}
		for i := range nodes {
			e.enrichNode(ctx, clusterName, &nodes[i])
		}
		if next == "" {
			break
		}
		nodeCursor = next
	}
}

// nodeField pairs a node's version field with its matcher.
type nodeField struct {
	value   *string
	matcher Matcher
}

func nodeFields(n *api.Node) []nodeField {
	return []nodeField{
		{n.KubeletVersion, KubernetesMatcher{}},
		{n.ContainerRuntimeVersion, ContainerRuntimeMatcher{}},
		{n.OsImage, OSImageMatcher{}},
		{n.KernelVersion, KernelMatcher{}},
	}
}

func (e *Enricher) enrichNode(ctx context.Context, clusterName string, node *api.Node) {
	if node.Id == nil {
		return
	}
	nodeID := *node.Id
	nodeName := node.Name

	for _, f := range nodeFields(node) {
		if f.value == nil || *f.value == "" {
			continue
		}
		mr, ok := f.matcher.Match(*f.value)
		if !ok {
			continue
		}
		ann, err := e.resolveAnnotation(ctx, mr)
		if err != nil {
			slog.Warn("eol enricher: resolve node version",
				slog.Any("error", err),
				slog.String("cluster", clusterName),
				slog.String("node", nodeName),
				slog.String("product", mr.Product))
			metrics.ObserveEOLError(clusterName, "node", "resolve")
			continue
		}
		if ann == nil {
			continue
		}
		if err := e.mergeNodeAnnotation(ctx, nodeID, mr.Product, ann); err != nil {
			slog.Warn("eol enricher: update node annotations",
				slog.Any("error", err),
				slog.String("cluster", clusterName),
				slog.String("node", nodeName),
				slog.String("product", mr.Product))
			metrics.ObserveEOLError(clusterName, "node", "update")
		} else {
			metrics.ObserveEOLEnrichment(clusterName, "node", string(ann.EOLStatus))
		}
	}
}

// enrichVirtualMachine builds a fresh `argos.io/eol.<product>` set from
// the VM's declared applications and writes the result, dropping every
// previous `argos.io/eol.*` key in the process so stale annotations are
// reaped automatically (operators removing an entry from the list see
// the matching annotation disappear on the next tick).
//
// Operator metadata under any other key (owner team, custom tags) is
// preserved untouched. No-op writes are skipped to keep audit log
// volume bounded.
func (e *Enricher) enrichVirtualMachine(ctx context.Context, vm *api.VirtualMachine) { //nolint:gocyclo // per-product loop with error branches
	if vm.TerminatedAt != nil {
		return
	}
	vmName := vm.Name

	// Build the new EOL annotation set. Each application yields one entry
	// — either a real lifecycle annotation when endoflife.date knows the
	// product, or a stub with eol_status="unknown" so auditors can see
	// the row was evaluated.
	newEOL := make(map[string]string)
	for i := range vm.Applications {
		app := &vm.Applications[i]
		product := api.NormalizeProductName(app.Product)
		if product == "" {
			continue
		}
		ann, err := e.resolveVMApplicationAnnotation(ctx, product, app.Version)
		if err != nil {
			slog.Warn("eol enricher: resolve vm application",
				slog.Any("error", err),
				slog.String("vm", vmName),
				slog.String("product", product))
			metrics.ObserveEOLError(vmName, "vm", "resolve")
			continue
		}
		key := annotationPrefix + product
		b, err := json.Marshal(ann) //nolint:errchkjson // *Annotation contains only string fields; always serialisable
		if err != nil {
			slog.Warn("eol enricher: marshal vm annotation",
				slog.Any("error", err),
				slog.String("vm", vmName),
				slog.String("product", product))
			continue
		}
		// Last write wins on duplicate normalized products — matches the
		// "two apps share a product key, only one annotation makes sense"
		// edge case (the latter wins, which is the operator's most recent
		// declaration in list order).
		newEOL[key] = string(b)
		metrics.ObserveEOLEnrichment(vmName, "vm", string(ann.EOLStatus))
	}

	// Compute the merged annotations: keep every non-EOL annotation,
	// drop every existing `argos.io/eol.*`, then layer in newEOL.
	merged := make(map[string]string)
	for k, v := range vm.Annotations {
		if !strings.HasPrefix(k, annotationPrefix) {
			merged[k] = v
		}
	}
	for k, v := range newEOL {
		merged[k] = v
	}

	if vmAnnotationsEqual(vm.Annotations, merged) {
		return
	}

	if _, err := e.store.UpdateVirtualMachine(ctx, vm.ID, api.VirtualMachinePatch{Annotations: &merged}); err != nil {
		slog.Warn("eol enricher: update vm annotations",
			slog.Any("error", err),
			slog.String("vm", vmName))
		metrics.ObserveEOLError(vmName, "vm", "update")
	}
}

// resolveVMApplicationAnnotation looks up `product` on endoflife.date
// and returns an annotation describing the entity's `version`. Returns
// a stub annotation with eol_status="unknown" when the product is not
// on endoflife.date or the version cannot be parsed into a major.minor
// cycle — operators see the row was evaluated rather than silently
// dropped.
func (e *Enricher) resolveVMApplicationAnnotation(ctx context.Context, product, version string) (*Annotation, error) {
	cycles, err := e.client.GetProduct(ctx, product)
	if err != nil {
		if errorsIsProductNotFound(err) {
			return &Annotation{
				Product:   product,
				Cycle:     strings.TrimSpace(version),
				EOLStatus: StatusUnknown,
				CheckedAt: time.Now().UTC().Format(time.RFC3339),
			}, nil
		}
		return nil, fmt.Errorf("get product %s: %w", product, err)
	}

	cycleKey := extractMajorMinor(version)
	if cycleKey == "" {
		return &Annotation{
			Product:   product,
			Cycle:     strings.TrimSpace(version),
			EOLStatus: StatusUnknown,
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	cycle := FindCycle(cycles, cycleKey)
	if cycle == nil {
		return &Annotation{
			Product:   product,
			Cycle:     cycleKey,
			EOLStatus: StatusUnknown,
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		}, nil
	}

	return e.buildAnnotation(MatchResult{Product: product, Cycle: cycleKey}, cycle, cycles), nil
}

// extractMajorMinor pulls a "X.Y" cycle string out of a free-form
// operator-typed version. Strips a leading "v" and any "-suffix".
// Returns "" when the input has no recognisable numeric major.minor.
func extractMajorMinor(version string) string {
	v := strings.TrimSpace(version)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return ""
	}
	if !isNumeric(parts[0]) || !isNumeric(parts[1]) {
		return ""
	}
	return parts[0] + "." + parts[1]
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// errorsIsProductNotFound returns true when the underlying error is the
// well-known "product not on endoflife.date" sentinel. Wrapping is
// honored via errors.Is.
func errorsIsProductNotFound(err error) bool {
	return errors.Is(err, ErrProductNotFound)
}

// vmAnnotationsEqual compares the existing annotations map with the
// merged one. Both come from this package so a string-equality compare
// per key is enough — JSON serialisation is stable for our annotation
// shape (no maps, only string fields).
func vmAnnotationsEqual(existing, merged map[string]string) bool {
	if len(existing) != len(merged) {
		return false
	}
	for k, v := range existing {
		if merged[k] != v {
			return false
		}
	}
	return true
}

// resolveAnnotation fetches the product cycles and builds the annotation.
// Returns nil (without error) when the product has no matching cycle.
func (e *Enricher) resolveAnnotation(ctx context.Context, mr MatchResult) (*Annotation, error) {
	cycles, err := e.client.GetProduct(ctx, mr.Product)
	if err != nil {
		return nil, fmt.Errorf("get product %s: %w", mr.Product, err)
	}

	cycle := FindCycle(cycles, mr.Cycle)
	if cycle == nil {
		return nil, nil //nolint:nilnil // nil annotation signals "skip" to caller
	}

	return e.buildAnnotation(mr, cycle, cycles), nil
}

// buildAnnotation constructs an EOL annotation from the matched cycle and
// the full product cycle list. allCycles is ordered newest-first (as returned
// by endoflife.date); the first element's Latest field becomes LatestAvailable
// — the product-wide latest version — so operators can see how far behind
// they are, not just whether their current cycle is patched.
func (e *Enricher) buildAnnotation(mr MatchResult, cycle *Cycle, allCycles []Cycle) *Annotation {
	ann := &Annotation{
		Product:   mr.Product,
		Cycle:     mr.Cycle,
		Latest:    cycle.Latest,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}

	// The endoflife.date API returns cycles newest-first;
	// the first element's Latest field is the product-wide latest version.
	if len(allCycles) > 0 && allCycles[0].Latest != "" {
		ann.LatestAvailable = allCycles[0].Latest
	}

	now := time.Now().UTC()
	eolDate, hasEOL := cycle.EOLDate()

	switch {
	case !hasEOL:
		// EOL field is false or absent. Check if true (already EOL, date unknown).
		if b, ok := cycle.EOL.(bool); ok && b {
			ann.EOLStatus = StatusEOL
		} else {
			ann.EOLStatus = StatusSupported
		}
	case now.After(eolDate):
		ann.EOL = eolDate.Format("2006-01-02")
		ann.EOLStatus = StatusEOL
	case now.After(eolDate.AddDate(0, 0, -e.approachingDays)):
		ann.EOL = eolDate.Format("2006-01-02")
		ann.EOLStatus = StatusApproachingEOL
	default:
		ann.EOL = eolDate.Format("2006-01-02")
		ann.EOLStatus = StatusSupported
	}

	if supportDate, ok := cycle.SupportDate(); ok {
		ann.Support = supportDate.Format("2006-01-02")
	}

	return ann
}

func (e *Enricher) mergeClusterAnnotation(ctx context.Context, id uuid.UUID, product string, ann *Annotation) error {
	cluster, err := e.store.GetCluster(ctx, id)
	if err != nil {
		return fmt.Errorf("get cluster %s: %w", id, err)
	}

	merged := mergeAnnotation(cluster.Annotations, product, ann)
	if _, err := e.store.UpdateCluster(ctx, id, api.ClusterUpdate{Annotations: &merged}); err != nil {
		return fmt.Errorf("update cluster %s annotations: %w", id, err)
	}
	return nil
}

func (e *Enricher) mergeNodeAnnotation(ctx context.Context, id uuid.UUID, product string, ann *Annotation) error {
	node, err := e.store.GetNode(ctx, id)
	if err != nil {
		return fmt.Errorf("get node %s: %w", id, err)
	}

	merged := mergeAnnotation(node.Annotations, product, ann)
	if _, err := e.store.UpdateNode(ctx, id, api.NodeUpdate{Annotations: &merged}); err != nil {
		return fmt.Errorf("update node %s annotations: %w", id, err)
	}
	return nil
}

// mergeAnnotation takes existing annotations and merges in the new EOL
// annotation under "argos.io/eol.<product>". Non-EOL annotations are
// preserved; only EOL keys are overwritten.
//
//nolint:gocritic // ptrToRefParam: matches the api.Cluster.Annotations type (*map[string]string).
func mergeAnnotation(existing *map[string]string, product string, ann *Annotation) map[string]string {
	merged := make(map[string]string)
	if existing != nil {
		for k, v := range *existing {
			merged[k] = v
		}
	}

	key := annotationPrefix + product
	b, _ := json.Marshal(ann)
	merged[key] = string(b)
	return merged
}
