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
	"github.com/sthalbert/argos/internal/metrics"
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
//
// OwnerKind/OwnerName carry the controlling ownerReference (controller: true)
// when present, so the collector can resolve the top-level Workload the pod
// belongs to. Direct kinds (StatefulSet, DaemonSet) point straight at the
// workload; ReplicaSet needs a second hop via ReplicaSetOwnerLister.
type PodInfo struct {
	Name       string
	Namespace  string
	Phase      string
	NodeName   string
	PodIP      string
	Containers []map[string]interface{}
	Labels     map[string]string
	OwnerKind  string
	OwnerName  string
}

// ReplicaSetOwner carries the controlling ownerReference of a ReplicaSet so
// the collector can walk the Pod -> ReplicaSet -> Deployment chain without
// re-listing ReplicaSets per pod. Namespace/Name identify the ReplicaSet;
// OwnerKind/OwnerName point at its parent (typically Deployment).
type ReplicaSetOwner struct {
	Namespace string
	Name      string
	OwnerKind string
	OwnerName string
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
	Containers    []map[string]interface{}
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

// IngressInfo is the subset of a Kubernetes Ingress the collector consumes.
// Rules and TLS entries are flattened into generic maps so the store can
// persist them as JSONB without coupling to client-go types.
type IngressInfo struct {
	Name             string
	Namespace        string
	IngressClassName string
	Rules            []map[string]interface{}
	TLS              []map[string]interface{}
	Labels           map[string]string
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

// IngressLister returns every Ingress visible to the configured kubeconfig,
// across all namespaces.
type IngressLister interface {
	ListIngresses(ctx context.Context) ([]IngressInfo, error)
}

// ReplicaSetOwnerLister returns the (namespace, name, owner) tuple for every
// ReplicaSet visible to the configured kubeconfig. Used to bridge Pod ->
// ReplicaSet -> Deployment when resolving a pod's top-level workload.
type ReplicaSetOwnerLister interface {
	ListReplicaSetOwners(ctx context.Context) ([]ReplicaSetOwner, error)
}

// KubeSource is the composite contract the Collector consumes.
type KubeSource interface {
	VersionFetcher
	NodeLister
	NamespaceLister
	PodLister
	WorkloadLister
	ServiceLister
	IngressLister
	ReplicaSetOwnerLister
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
	UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error)
	DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
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
		metrics.ObserveError(c.clusterName, "version", "list")
		slog.Warn("collector: fetch server version failed", "error", err, "cluster_name", c.clusterName)
		return
	}
	metrics.MarkPoll(c.clusterName, "version")

	cluster, err := c.store.GetClusterByName(ctx, c.clusterName)
	if err != nil {
		metrics.ObserveError(c.clusterName, "cluster", "lookup")
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
			metrics.ObserveError(c.clusterName, "cluster", "upsert")
			slog.Error("collector: update cluster failed", "error", err, "cluster_name", c.clusterName)
			return
		}
		metrics.ObserveUpserts(c.clusterName, "cluster", 1)
		slog.Info("collector: refreshed cluster version", "cluster_name", c.clusterName, "version", version)
	}

	c.ingestNodes(ctx, *cluster.Id)
	namespaceIDsByName := c.ingestNamespaces(ctx, *cluster.Id)
	if namespaceIDsByName != nil {
		// Workloads go first so ingestPods can resolve each pod's top-level
		// controller into a workload_id FK. ingestWorkloads returns a
		// per-namespace (kind, name) -> id map that stays nil on list error,
		// signalling ingestPods to write pods with workload_id unset.
		workloadIDs := c.ingestWorkloads(ctx, namespaceIDsByName)
		c.ingestPods(ctx, namespaceIDsByName, workloadIDs)
		c.ingestServices(ctx, namespaceIDsByName)
		c.ingestIngresses(ctx, namespaceIDsByName)
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
		metrics.ObserveError(c.clusterName, "nodes", "list")
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
			metrics.ObserveError(c.clusterName, "nodes", "upsert")
			slog.Warn("collector: upsert node failed", "error", err, "node", n.Name, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepNames = append(keepNames, n.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "nodes", upserted)

	var reconciled int64
	if c.reconcile {
		n, err := c.store.DeleteNodesNotIn(ctx, clusterID, keepNames)
		if err != nil {
			metrics.ObserveError(c.clusterName, "nodes", "reconcile")
			slog.Error("collector: reconcile nodes failed", "error", err, "cluster_name", c.clusterName)
		}
		reconciled = n
		metrics.ObserveReconciled(c.clusterName, "nodes", n)
	}
	metrics.MarkPoll(c.clusterName, "nodes")
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
		metrics.ObserveError(c.clusterName, "namespaces", "list")
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
			metrics.ObserveError(c.clusterName, "namespaces", "upsert")
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
	metrics.ObserveUpserts(c.clusterName, "namespaces", upserted)

	var reconciled int64
	if c.reconcile {
		n, err := c.store.DeleteNamespacesNotIn(ctx, clusterID, keepNames)
		if err != nil {
			metrics.ObserveError(c.clusterName, "namespaces", "reconcile")
			slog.Error("collector: reconcile namespaces failed", "error", err, "cluster_name", c.clusterName)
		}
		reconciled = n
		metrics.ObserveReconciled(c.clusterName, "namespaces", n)
	}
	metrics.MarkPoll(c.clusterName, "namespaces")
	slog.Info("collector: ingested namespaces", "upserted", upserted, "failed", failed, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
	return idsByName
}

// wlKey uniquely identifies a workload within a namespace — the (kind, name)
// tuple mirrors the store's natural key. Used by ingestWorkloads to surface
// each upserted workload's UUID for pod-owner resolution, and by reconcile
// to track which workloads are still live.
type wlKey struct{ kind, name string }

// resolveWorkloadID walks a pod's ownerReference chain to its top-level
// Workload (Deployment / StatefulSet / DaemonSet) and returns the stored
// UUID. Returns nil when the pod has no controller, its controller kind
// isn't modelled (Job, CronJob, bare Pod), or the target workload wasn't
// upserted this tick — in all those cases the pod's workload_id stays null
// and gets revisited on the next poll.
func resolveWorkloadID(
	namespaceID uuid.UUID,
	ownerKind, ownerName string,
	workloadIDs map[uuid.UUID]map[wlKey]uuid.UUID,
	rsOwners map[string]ReplicaSetOwner,
) *uuid.UUID {
	if ownerKind == "" || ownerName == "" {
		return nil
	}
	// ReplicaSets are an intermediate layer: walk one hop up to the Deployment
	// (or whatever owns the RS) before looking up the workload.
	if ownerKind == "ReplicaSet" {
		rs, ok := rsOwners[namespaceID.String()+"/"+ownerName]
		if !ok {
			return nil
		}
		ownerKind, ownerName = rs.OwnerKind, rs.OwnerName
		if ownerKind == "" || ownerName == "" {
			return nil
		}
	}
	nsWorkloads, ok := workloadIDs[namespaceID]
	if !ok {
		return nil
	}
	id, ok := nsWorkloads[wlKey{kind: ownerKind, name: ownerName}]
	if !ok {
		return nil
	}
	return &id
}

// ingestPods lists pods from the kube source, resolves each pod's parent
// namespace via namespaceIDsByName, and upserts it. Pods whose namespace
// isn't in the live map (either the namespace disappeared this tick or the
// lookup never ran) are skipped rather than written against a guessed
// parent. When reconcile is enabled, every live namespace is reconciled
// independently so empty namespaces have their stale pods cleared too.
//
// workloadIDs may be nil (list-workloads failure) in which case pods are
// still upserted, just without workload_id set — the next successful poll
// will backfill the FK.
func (c *Collector) ingestPods(ctx context.Context, namespaceIDsByName map[string]uuid.UUID, workloadIDs map[uuid.UUID]map[wlKey]uuid.UUID) {
	pods, err := c.source.ListPods(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "pods", "list")
		slog.Warn("collector: list pods failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	// Fetch ReplicaSet owners only if we have workloads to resolve into. A
	// list failure here degrades gracefully: pods owned by ReplicaSets end
	// up with workload_id null, to be backfilled on the next tick.
	rsOwners := map[string]ReplicaSetOwner{}
	if workloadIDs != nil {
		rss, err := c.source.ListReplicaSetOwners(ctx)
		if err != nil {
			metrics.ObserveError(c.clusterName, "replicasets", "list")
			slog.Warn("collector: list replicasets failed", "error", err, "cluster_name", c.clusterName)
		} else {
			for _, rs := range rss {
				nsID, ok := namespaceIDsByName[rs.Namespace]
				if !ok {
					continue
				}
				rsOwners[nsID.String()+"/"+rs.Name] = rs
			}
		}
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
		if len(p.Containers) > 0 {
			cs := api.ContainerList(p.Containers)
			in.Containers = &cs
		}
		if len(p.Labels) > 0 {
			labels := p.Labels
			in.Labels = &labels
		}
		if wid := resolveWorkloadID(nsID, p.OwnerKind, p.OwnerName, workloadIDs, rsOwners); wid != nil {
			in.WorkloadId = wid
		}
		if _, err := c.store.UpsertPod(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "pods", "upsert")
			slog.Warn("collector: upsert pod failed", "error", err, "pod", p.Name, "namespace", p.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], p.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "pods", upserted)

	var reconciled int64
	if c.reconcile {
		// Reconcile every live namespace, including ones with zero pods this
		// tick, so emptied namespaces see their stored pods removed.
		for _, nsID := range namespaceIDsByName {
			n, err := c.store.DeletePodsNotIn(ctx, nsID, keepByNS[nsID])
			if err != nil {
				metrics.ObserveError(c.clusterName, "pods", "reconcile")
				slog.Error("collector: reconcile pods failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
		metrics.ObserveReconciled(c.clusterName, "pods", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "pods")
	slog.Info("collector: ingested pods", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

// ingestWorkloads lists Deployments, StatefulSets, and DaemonSets (folded
// into a single slice tagged by Kind), resolves each one's K8s namespace
// name to the CMDB namespace UUID, and upserts it. Reconcile operates
// per-namespace keyed on the (kind, name) tuple so a deleted Deployment
// 'web' doesn't wipe the still-live StatefulSet 'web' in the same namespace.
//
// Returns a (namespace_id -> (kind, name) -> workload_id) map so ingestPods
// can resolve each pod's controller into a workload_id FK. Returns nil on
// ListWorkloads failure — ingestPods treats that as "skip owner resolution,
// write pods without workload_id, let the next tick backfill".
func (c *Collector) ingestWorkloads(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) map[uuid.UUID]map[wlKey]uuid.UUID {
	workloads, err := c.source.ListWorkloads(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "workloads", "list")
		slog.Warn("collector: list workloads failed", "error", err, "cluster_name", c.clusterName)
		return nil
	}

	keepByNS := make(map[uuid.UUID][]wlKey)
	idsByNS := make(map[uuid.UUID]map[wlKey]uuid.UUID)

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
		if len(w.Containers) > 0 {
			cs := api.ContainerList(w.Containers)
			in.Containers = &cs
		}
		if len(w.Labels) > 0 {
			labels := w.Labels
			in.Labels = &labels
		}
		stored, err := c.store.UpsertWorkload(ctx, in)
		if err != nil {
			metrics.ObserveError(c.clusterName, "workloads", "upsert")
			slog.Warn("collector: upsert workload failed", "error", err, "workload", w.Name, "kind", w.Kind, "namespace", w.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		key := wlKey{kind: string(w.Kind), name: w.Name}
		keepByNS[nsID] = append(keepByNS[nsID], key)
		if stored.Id != nil {
			if idsByNS[nsID] == nil {
				idsByNS[nsID] = make(map[wlKey]uuid.UUID)
			}
			idsByNS[nsID][key] = *stored.Id
		}
	}
	metrics.ObserveUpserts(c.clusterName, "workloads", upserted)

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
				metrics.ObserveError(c.clusterName, "workloads", "reconcile")
				slog.Error("collector: reconcile workloads failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
		metrics.ObserveReconciled(c.clusterName, "workloads", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "workloads")
	slog.Info("collector: ingested workloads", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
	return idsByNS
}

// ingestServices lists services cluster-wide, resolves each one's K8s
// namespace name to the CMDB namespace UUID, and upserts it. Per-namespace
// reconcile mirrors ingestPods.
func (c *Collector) ingestServices(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	services, err := c.source.ListServices(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "services", "list")
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
			metrics.ObserveError(c.clusterName, "services", "upsert")
			slog.Warn("collector: upsert service failed", "error", err, "service", s.Name, "namespace", s.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], s.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "services", upserted)

	var reconciled int64
	if c.reconcile {
		for _, nsID := range namespaceIDsByName {
			n, err := c.store.DeleteServicesNotIn(ctx, nsID, keepByNS[nsID])
			if err != nil {
				metrics.ObserveError(c.clusterName, "services", "reconcile")
				slog.Error("collector: reconcile services failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
		metrics.ObserveReconciled(c.clusterName, "services", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "services")
	slog.Info("collector: ingested services", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

// ingestIngresses lists ingresses cluster-wide, resolves each one's K8s
// namespace name to the CMDB namespace UUID, and upserts it. Per-namespace
// reconcile mirrors ingestServices.
func (c *Collector) ingestIngresses(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	ingresses, err := c.source.ListIngresses(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "ingresses", "list")
		slog.Warn("collector: list ingresses failed", "error", err, "cluster_name", c.clusterName)
		return
	}

	keepByNS := make(map[uuid.UUID][]string)

	var upserted, failed, skipped int
	for _, ing := range ingresses {
		nsID, ok := namespaceIDsByName[ing.Namespace]
		if !ok {
			slog.Warn("collector: ingress in unknown namespace; skipping", "ingress", ing.Name, "namespace", ing.Namespace, "cluster_name", c.clusterName)
			skipped++
			continue
		}
		in := api.IngressCreate{
			NamespaceId:      nsID,
			Name:             ing.Name,
			IngressClassName: ptrIfNonEmpty(ing.IngressClassName),
		}
		if len(ing.Rules) > 0 {
			rules := ing.Rules
			in.Rules = &rules
		}
		if len(ing.TLS) > 0 {
			tls := ing.TLS
			in.Tls = &tls
		}
		if len(ing.Labels) > 0 {
			labels := ing.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertIngress(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "ingresses", "upsert")
			slog.Warn("collector: upsert ingress failed", "error", err, "ingress", ing.Name, "namespace", ing.Namespace, "cluster_name", c.clusterName)
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], ing.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "ingresses", upserted)

	var reconciled int64
	if c.reconcile {
		for _, nsID := range namespaceIDsByName {
			n, err := c.store.DeleteIngressesNotIn(ctx, nsID, keepByNS[nsID])
			if err != nil {
				metrics.ObserveError(c.clusterName, "ingresses", "reconcile")
				slog.Error("collector: reconcile ingresses failed", "error", err, "namespace_id", nsID, "cluster_name", c.clusterName)
				continue
			}
			reconciled += n
		}
		metrics.ObserveReconciled(c.clusterName, "ingresses", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "ingresses")
	slog.Info("collector: ingested ingresses", "upserted", upserted, "failed", failed, "skipped", skipped, "reconciled_deleted", reconciled, "cluster_name", c.clusterName)
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

