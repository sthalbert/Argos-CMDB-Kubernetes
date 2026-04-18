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

// PodInfo is the subset of a Kubernetes Pod the collector consumes. Namespace
// is the K8s namespace name, which the collector resolves against the CMDB's
// namespace UUID before writing.
type PodInfo struct {
	Name      string
	Namespace string
	Phase     string
	NodeName  string
	PodIP     string
	Labels    map[string]string
}

// WorkloadInfo is the subset of a Kubernetes workload controller (Deployment /
// StatefulSet / DaemonSet) the collector consumes. Namespace is the K8s
// namespace name, resolved against the CMDB's namespace UUID before writing.
type WorkloadInfo struct {
	Name          string
	Namespace     string
	Kind          api.WorkloadKind
	Replicas      *int
	ReadyReplicas *int
	Labels        map[string]string
}

// ServiceInfo is the subset of a Kubernetes Service the collector consumes.
type ServiceInfo struct {
	Name      string
	Namespace string
	Type      string // K8s ServiceType as a string; passed through to api.ServiceType at upsert.
	ClusterIP string
	Selector  map[string]string
	Ports     []map[string]interface{}
	Labels    map[string]string
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

// PodLister returns every Pod visible to the configured kubeconfig, across
// all namespaces.
type PodLister interface {
	ListPods(ctx context.Context) ([]PodInfo, error)
}

// WorkloadLister returns every Deployment / StatefulSet / DaemonSet visible
// to the configured kubeconfig, folded into a single slice tagged by kind.
type WorkloadLister interface {
	ListWorkloads(ctx context.Context) ([]WorkloadInfo, error)
}

// ServiceLister returns every Service visible to the configured kubeconfig,
// across all namespaces.
type ServiceLister interface {
	ListServices(ctx context.Context) ([]ServiceInfo, error)
}

// KubeSource is the composite contract the Collector consumes.
type KubeSource interface {
	VersionFetcher
	NodeLister
	NamespaceLister
	PodLister
	WorkloadLister
	ServiceLister
}

// cmdbStore is the subset of api.Store the collector consumes.
type cmdbStore interface {
	GetClusterByName(ctx context.Context, name string) (api.Cluster, error)
	UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error)
	UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error)
	DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
	UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error)
	DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
	UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, error)
	DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
	UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error)
	DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error)
	UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, error)
	DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
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
	namespaceIDsByName := c.ingestNamespaces(ctx, *cluster.Id)
	if namespaceIDsByName != nil {
		c.ingestPods(ctx, namespaceIDsByName)
		c.ingestWorkloads(ctx, namespaceIDsByName)
		c.ingestServices(ctx, namespaceIDsByName)
	}
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
// deleted so stored state matches the cluster. Returns a name -> id map the
// pod-ingestion pass uses to resolve each pod's FK, or nil on list failure
// (signal to the caller to skip pod ingestion).
func (c *Collector) ingestNamespaces(ctx context.Context, clusterID uuid.UUID) map[string]uuid.UUID {
	namespaces, err := c.source.ListNamespaces(ctx)
	if err != nil {
		slog.Warn("collector: list namespaces failed", "error", err, "cluster_name", c.clusterName)
		return nil
	}

	var upserted, failed int
	keepNames := make([]string, 0, len(namespaces))
	idsByName := make(map[string]uuid.UUID, len(namespaces))
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
		stored, err := c.store.UpsertNamespace(ctx, in)
		if err != nil {
			slog.Warn("collector: upsert namespace failed", "error", err, "namespace", ns.Name, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepNames = append(keepNames, ns.Name)
		if stored.Id != nil {
			idsByName[ns.Name] = *stored.Id
		}
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
	return idsByName
}

// ingestPods lists pods from the kube source, resolves each pod's parent
// namespace via namespaceIDsByName, and upserts it. Pods whose namespace
// isn't in the live map (either the namespace disappeared this tick or the
// lookup never ran) are skipped rather than written against a guessed
// parent. When reconcile is enabled, every live namespace is reconciled
// independently so empty namespaces have their stale pods cleared too.
func (c *Collector) ingestPods(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	pods, err := c.source.ListPods(ctx)
	if err != nil {
		slog.Warn("collector: list pods failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	var upserted, failed, skipped int
	keepByNS := make(map[uuid.UUID][]string)
	for _, p := range pods {
		nsID, ok := namespaceIDsByName[p.Namespace]
		if !ok {
			slog.Warn("collector: pod in unknown namespace; skipping", "pod", p.Name, "namespace", p.Namespace, "cluster_name", c.clusterName)
			skipped++
			continue
		}
		in := api.PodCreate{
			NamespaceId: nsID,
			Name:        p.Name,
			Phase:       ptrIfNonEmpty(p.Phase),
			NodeName:    ptrIfNonEmpty(p.NodeName),
			PodIp:       ptrIfNonEmpty(p.PodIP),
		}
		if len(p.Labels) > 0 {
			labels := p.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertPod(ctx, in); err != nil {
			slog.Warn("collector: upsert pod failed", "error", err, "pod", p.Name, "namespace", p.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], p.Name)
	}

	var reconciled int64
	if c.reconcile {
		// Reconcile every live namespace, including ones with zero pods this
		// tick, so emptied namespaces see their stored pods removed.
		for _, nsID := range namespaceIDsByName {
			n, err := c.store.DeletePodsNotIn(ctx, nsID, keepByNS[nsID])
			if err != nil {
				slog.Error("collector: reconcile pods failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
	}
	slog.Info("collector: ingested pods", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

// ingestWorkloads lists Deployments, StatefulSets, and DaemonSets (folded
// into a single slice tagged by Kind), resolves each one's K8s namespace
// name to the CMDB namespace UUID, and upserts it. Reconcile operates
// per-namespace keyed on the (kind, name) tuple so a deleted Deployment
// 'web' doesn't wipe the still-live StatefulSet 'web' in the same namespace.
func (c *Collector) ingestWorkloads(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	workloads, err := c.source.ListWorkloads(ctx)
	if err != nil {
		slog.Warn("collector: list workloads failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	type wlKey struct{ kind, name string }
	keepByNS := make(map[uuid.UUID][]wlKey)

	var upserted, failed, skipped int
	for _, w := range workloads {
		nsID, ok := namespaceIDsByName[w.Namespace]
		if !ok {
			slog.Warn("collector: workload in unknown namespace; skipping", "workload", w.Name, "kind", w.Kind, "namespace", w.Namespace, "cluster_name", c.clusterName)
			skipped++
			continue
		}
		in := api.WorkloadCreate{
			NamespaceId:   nsID,
			Kind:          w.Kind,
			Name:          w.Name,
			Replicas:      w.Replicas,
			ReadyReplicas: w.ReadyReplicas,
		}
		if len(w.Labels) > 0 {
			labels := w.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertWorkload(ctx, in); err != nil {
			slog.Warn("collector: upsert workload failed", "error", err, "workload", w.Name, "kind", w.Kind, "namespace", w.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], wlKey{kind: string(w.Kind), name: w.Name})
	}

	var reconciled int64
	if c.reconcile {
		// Reconcile every live namespace, including ones with zero workloads
		// this tick, so emptied namespaces have their stored workloads cleared.
		for _, nsID := range namespaceIDsByName {
			keep := keepByNS[nsID]
			kinds := make([]string, 0, len(keep))
			names := make([]string, 0, len(keep))
			for _, k := range keep {
				kinds = append(kinds, k.kind)
				names = append(names, k.name)
			}
			n, err := c.store.DeleteWorkloadsNotIn(ctx, nsID, kinds, names)
			if err != nil {
				slog.Error("collector: reconcile workloads failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
	}
	slog.Info("collector: ingested workloads", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

// ingestServices lists services cluster-wide, resolves each one's K8s
// namespace name to the CMDB namespace UUID, and upserts it. Per-namespace
// reconcile mirrors ingestPods.
func (c *Collector) ingestServices(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	services, err := c.source.ListServices(ctx)
	if err != nil {
		slog.Warn("collector: list services failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	keepByNS := make(map[uuid.UUID][]string)

	var upserted, failed, skipped int
	for _, s := range services {
		nsID, ok := namespaceIDsByName[s.Namespace]
		if !ok {
			slog.Warn("collector: service in unknown namespace; skipping", "service", s.Name, "namespace", s.Namespace, "cluster_name", c.clusterName)
			skipped++
			continue
		}
		in := api.ServiceCreate{
			NamespaceId: nsID,
			Name:        s.Name,
			ClusterIp:   ptrIfNonEmpty(s.ClusterIP),
		}
		if s.Type != "" {
			t := api.ServiceType(s.Type)
			in.Type = &t
		}
		if len(s.Selector) > 0 {
			sel := s.Selector
			in.Selector = &sel
		}
		if len(s.Ports) > 0 {
			ports := s.Ports
			in.Ports = &ports
		}
		if len(s.Labels) > 0 {
			labels := s.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertService(ctx, in); err != nil {
			slog.Warn("collector: upsert service failed", "error", err, "service", s.Name, "namespace", s.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], s.Name)
	}

	var reconciled int64
	if c.reconcile {
		for _, nsID := range namespaceIDsByName {
			n, err := c.store.DeleteServicesNotIn(ctx, nsID, keepByNS[nsID])
			if err != nil {
				slog.Error("collector: reconcile services failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
	}
	slog.Info("collector: ingested services", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

