package eol

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

	metrics.MarkEOLRun()
	slog.Debug("eol enricher: tick completed")
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
