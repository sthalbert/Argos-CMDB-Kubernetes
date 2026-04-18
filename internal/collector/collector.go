// Package collector polls a Kubernetes cluster on an interval and refreshes
// the matching cluster record in the CMDB. v1 scope fetches the Kubernetes
// server version and writes it back via the Store.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

// VersionFetcher returns the Kubernetes API server version for the cluster
// it was configured against (typically via kubeconfig or in-cluster config).
type VersionFetcher interface {
	ServerVersion(ctx context.Context) (string, error)
}

// clusterStore is the subset of api.Store the collector consumes.
type clusterStore interface {
	ListClusters(ctx context.Context, limit int, cursor string) ([]api.Cluster, string, error)
	UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error)
}

// Collector polls a VersionFetcher and updates a named cluster record in the
// store. Errors encountered during a single tick are logged and the loop
// continues to the next tick.
type Collector struct {
	store        clusterStore
	fetcher      VersionFetcher
	clusterName  string
	interval     time.Duration
	fetchTimeout time.Duration
}

// New returns a Collector. fetchTimeout bounds each poll; interval is the
// delay between polls.
func New(store clusterStore, fetcher VersionFetcher, clusterName string, interval, fetchTimeout time.Duration) *Collector {
	return &Collector{
		store:        store,
		fetcher:      fetcher,
		clusterName:  clusterName,
		interval:     interval,
		fetchTimeout: fetchTimeout,
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

// poll performs one polling cycle. Errors are logged and swallowed; the
// caller's ticker is unaffected.
func (c *Collector) poll(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, c.fetchTimeout)
	defer cancel()

	version, err := c.fetcher.ServerVersion(ctx)
	if err != nil {
		slog.Warn("collector: fetch server version failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	cluster, err := c.findClusterByName(ctx, c.clusterName)
	if err != nil {
		if errors.Is(err, errClusterNotFound) {
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

	if cluster.KubernetesVersion != nil && *cluster.KubernetesVersion == version {
		slog.Debug("collector: version unchanged", "cluster_name", c.clusterName, "version", version)
		return
	}

	if _, err := c.store.UpdateCluster(ctx, *cluster.Id, api.ClusterUpdate{KubernetesVersion: &version}); err != nil {
		slog.Error("collector: update cluster failed", "error", err, "cluster_name", c.clusterName)
		return
	}
	slog.Info("collector: refreshed cluster version", "cluster_name", c.clusterName, "version", version)
}

var errClusterNotFound = errors.New("cluster not found by name")

// findClusterByName scans paginated store output and returns the first match.
// For v1 scope (a handful of clusters) a linear scan is adequate; a dedicated
// GetByName store method is a follow-up.
func (c *Collector) findClusterByName(ctx context.Context, name string) (api.Cluster, error) {
	const pageSize = 200
	cursor := ""
	for {
		items, next, err := c.store.ListClusters(ctx, pageSize, cursor)
		if err != nil {
			return api.Cluster{}, fmt.Errorf("list clusters: %w", err)
		}
		for _, cl := range items {
			if cl.Name == name {
				return cl, nil
			}
		}
		if next == "" {
			return api.Cluster{}, errClusterNotFound
		}
		cursor = next
	}
}
