// Package collector polls a Kubernetes cluster on an interval and refreshes
// the matching CMDB records. v1 scope fetches the server version and all
// nodes, writing them back through the Store.
package collector

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

// NodeInfo is the subset of a Kubernetes Node the collector consumes. It
// lives in this package (not in api) so the Kubernetes-facing KubeSource
// interface stays decoupled from the CMDB wire types.
type NodeInfo struct {
	Name           string
	KubeletVersion string
	OsImage        string
	Architecture   string
	Labels         map[string]string
}

// NamespaceInfo is the subset of a Kubernetes Namespace the collector consumes.
type NamespaceInfo struct {
	Name   string
	Phase  string
	Labels map[string]string
}

// VersionFetcher returns the Kubernetes API server version for the cluster
// it was configured against (typically via kubeconfig or in-cluster config).
type VersionFetcher interface {
	ServerVersion(ctx context.Context) (string, error)
}

// NodeLister returns every Node visible to the configured kubeconfig.
type NodeLister interface {
	ListNodes(ctx context.Context) ([]NodeInfo, error)
}

// NamespaceLister returns every Namespace visible to the configured kubeconfig.
type NamespaceLister interface {
	ListNamespaces(ctx context.Context) ([]NamespaceInfo, error)
}

// KubeSource is the composite contract the Collector consumes.
type KubeSource interface {
	VersionFetcher
	NodeLister
	NamespaceLister
}

// cmdbStore is the subset of api.Store the collector consumes.
type cmdbStore interface {
	GetClusterByName(ctx context.Context, name string) (api.Cluster, error)
	UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error)
	UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error)
	DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
	UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error)
	DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
}

// Collector polls a KubeSource and reconciles the results into the CMDB
// store against a cluster record matched by name. Errors encountered during
// a single tick are logged and the loop continues to the next tick.
type Collector struct {
	store        cmdbStore
	source       KubeSource
	clusterName  string
	interval     time.Duration
	fetchTimeout time.Duration
	reconcile    bool
}

// New returns a Collector. fetchTimeout bounds each poll; interval is the
// delay between polls. When reconcile is true, nodes and namespaces that
// vanish from the Kubernetes listing are deleted from the CMDB so the stored
// state always matches the live cluster — required for ANSSI cartography
// fidelity.
func New(store cmdbStore, source KubeSource, clusterName string, interval, fetchTimeout time.Duration, reconcile bool) *Collector {
	return &Collector{
		store:        store,
		source:       source,
		clusterName:  clusterName,
		interval:     interval,
		fetchTimeout: fetchTimeout,
		reconcile:    reconcile,
	}
}

// Run polls until ctx is cancelled. A poll happens immediately on start and
// every interval thereafter.
func (c *Collector) Run(ctx context.Context) error {
	slog.Info("collector starting", "cluster_name", c.clusterName, "interval", c.interval)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("collector stopping", "reason", ctx.Err())
			return ctx.Err()
		case <-ticker.C:
			c.poll(ctx)
		}
	}
}

// poll performs one polling cycle: refresh cluster version, then ingest nodes.
// Errors are logged and swallowed; the caller's ticker is unaffected.
func (c *Collector) poll(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, c.fetchTimeout)
	defer cancel()

	version, err := c.source.ServerVersion(ctx)
	if err != nil {
		slog.Warn("collector: fetch server version failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	cluster, err := c.store.GetClusterByName(ctx, c.clusterName)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			slog.Warn("collector: cluster not registered; POST /v1/clusters first", "cluster_name", c.clusterName)
			return
		}
		slog.Error("collector: lookup cluster failed", "error", err, "cluster_name", c.clusterName)
		return
	}
	if cluster.Id == nil {
		slog.Error("collector: stored cluster has nil id", "cluster_name", c.clusterName)
		return
	}

	if cluster.KubernetesVersion == nil || *cluster.KubernetesVersion != version {
		if _, err := c.store.UpdateCluster(ctx, *cluster.Id, api.ClusterUpdate{KubernetesVersion: &version}); err != nil {
			slog.Error("collector: update cluster failed", "error", err, "cluster_name", c.clusterName)
			return
		}
		slog.Info("collector: refreshed cluster version", "cluster_name", c.clusterName, "version", version)
	}

	c.ingestNodes(ctx, *cluster.Id)
	c.ingestNamespaces(ctx, *cluster.Id)
}

// ingestNodes lists nodes from the kube source and upserts each into the
// store under the given cluster. Individual node failures are logged and
// skipped; the loop continues so one bad node doesn't block the rest. When
// reconcile is enabled, nodes in the CMDB that no longer appear in the live
// listing are deleted so stored state matches the cluster.
func (c *Collector) ingestNodes(ctx context.Context, clusterID uuid.UUID) {
	nodes, err := c.source.ListNodes(ctx)
	if err != nil {
		slog.Warn("collector: list nodes failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	var upserted, failed int
	keepNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		in := api.NodeCreate{
			ClusterId:      clusterID,
			Name:           n.Name,
			KubeletVersion: ptrIfNonEmpty(n.KubeletVersion),
			OsImage:        ptrIfNonEmpty(n.OsImage),
			Architecture:   ptrIfNonEmpty(n.Architecture),
		}
		if len(n.Labels) > 0 {
			labels := n.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertNode(ctx, in); err != nil {
			slog.Warn("collector: upsert node failed", "error", err, "node", n.Name, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepNames = append(keepNames, n.Name)
	}

	var reconciled int64
	if c.reconcile {
		n, err := c.store.DeleteNodesNotIn(ctx, clusterID, keepNames)
		if err != nil {
			slog.Error("collector: reconcile nodes failed", "error", err, "cluster_name", c.clusterName)
		}
		reconciled = n
	}
	slog.Info("collector: ingested nodes", "upserted", upserted, "failed", failed, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

// ingestNamespaces lists namespaces from the kube source and upserts each
// into the store under the given cluster. When reconcile is enabled,
// namespaces in the CMDB that no longer appear in the live listing are
// deleted so stored state matches the cluster.
func (c *Collector) ingestNamespaces(ctx context.Context, clusterID uuid.UUID) {
	namespaces, err := c.source.ListNamespaces(ctx)
	if err != nil {
		slog.Warn("collector: list namespaces failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	var upserted, failed int
	keepNames := make([]string, 0, len(namespaces))
	for _, ns := range namespaces {
		in := api.NamespaceCreate{
			ClusterId: clusterID,
			Name:      ns.Name,
			Phase:     ptrIfNonEmpty(ns.Phase),
		}
		if len(ns.Labels) > 0 {
			labels := ns.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertNamespace(ctx, in); err != nil {
			slog.Warn("collector: upsert namespace failed", "error", err, "namespace", ns.Name, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepNames = append(keepNames, ns.Name)
	}

	var reconciled int64
	if c.reconcile {
		n, err := c.store.DeleteNamespacesNotIn(ctx, clusterID, keepNames)
		if err != nil {
			slog.Error("collector: reconcile namespaces failed", "error", err, "cluster_name", c.clusterName)
		}
		reconciled = n
	}
	slog.Info("collector: ingested namespaces", "upserted", upserted, "failed", failed, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

