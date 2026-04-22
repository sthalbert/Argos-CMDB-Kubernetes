// Package collector polls a Kubernetes cluster on an interval and refreshes
// the matching CMDB records. v1 scope fetches the server version and all
// nodes, writing them back through the Store.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/metrics"
)

// NodeInfo is the subset of a Kubernetes Node the collector consumes. It
// lives in this package (not in api) so the Kubernetes-facing KubeSource
// interface stays decoupled from the CMDB wire types.
//
// Modelled on Mercator's logical-server entity plus Kubernetes-specific
// additions needed for incident response (role, taints, conditions,
// capacity/allocatable pairs). Everything here is observed state — the
// collector overwrites it every tick via UpsertNode.
type NodeInfo struct {
	Name                        string
	Role                        string
	KubeletVersion              string
	KubeProxyVersion            string
	ContainerRuntimeVersion     string
	OsImage                     string
	OperatingSystem             string
	KernelVersion               string
	Architecture                string
	InternalIP                  string
	ExternalIP                  string
	PodCIDR                     string
	ProviderID                  string
	InstanceType                string
	Zone                        string
	CapacityCPU                 string
	CapacityMemory              string
	CapacityPods                string
	CapacityEphemeralStorage    string
	AllocatableCPU              string
	AllocatableMemory           string
	AllocatablePods             string
	AllocatableEphemeralStorage string
	Conditions                  []map[string]interface{}
	Taints                      []map[string]interface{}
	Unschedulable               bool
	Ready                       bool
	Labels                      map[string]string
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
// LoadBalancer mirrors status.loadBalancer.ingress[] — empty for Services
// whose type doesn't take one, populated by the cloud controller or
// on-prem equivalents (MetalLB / Kube-VIP / hardware LB).
type ServiceInfo struct {
	Name         string
	Namespace    string
	Type         string // K8s ServiceType as a string; passed through to api.ServiceType at upsert.
	ClusterIP    string
	Selector     map[string]string
	Ports        []map[string]interface{}
	LoadBalancer []map[string]interface{}
	Labels       map[string]string
}

// IngressInfo is the subset of a Kubernetes Ingress the collector consumes.
// Rules, TLS, and LoadBalancer entries are flattened into generic maps so
// the store can persist them as JSONB without coupling to client-go types.
// LoadBalancer mirrors status.loadBalancer.ingress[] — populated by the
// ingress controller (or its underlying Service / cloud LB / MetalLB).
type IngressInfo struct {
	Name             string
	Namespace        string
	IngressClassName string
	Rules            []map[string]interface{}
	TLS              []map[string]interface{}
	LoadBalancer     []map[string]interface{}
	Labels           map[string]string
}

// PVInfo is the subset of a Kubernetes PersistentVolume the collector
// consumes. PVs are cluster-scoped so there's no Namespace field. Capacity /
// AccessModes / reclaim policy come verbatim from the Kubernetes API;
// ClaimRef fields mirror spec.claimRef for PV -> PVC reconstruction.
type PVInfo struct {
	Name              string
	Capacity          string
	AccessModes       []string
	ReclaimPolicy     string
	Phase             string
	StorageClassName  string
	CSIDriver         string
	VolumeHandle      string
	ClaimRefNamespace string
	ClaimRefName      string
	Labels            map[string]string
}

// PVCInfo is the subset of a Kubernetes PersistentVolumeClaim the collector
// consumes. Namespace is the K8s namespace name, resolved against the CMDB's
// namespace UUID before writing. VolumeName is the raw spec.volumeName (the
// PV name the claim binds to) — the collector resolves this against the PV
// map each tick to set bound_volume_id.
type PVCInfo struct {
	Name             string
	Namespace        string
	Phase            string
	StorageClassName string
	VolumeName       string
	AccessModes      []string
	RequestedStorage string
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

// PersistentVolumeLister returns every cluster-scoped PersistentVolume
// visible to the configured kubeconfig.
type PersistentVolumeLister interface {
	ListPersistentVolumes(ctx context.Context) ([]PVInfo, error)
}

// PersistentVolumeClaimLister returns every PersistentVolumeClaim visible to
// the configured kubeconfig, across all namespaces.
type PersistentVolumeClaimLister interface {
	ListPersistentVolumeClaims(ctx context.Context) ([]PVCInfo, error)
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
	PersistentVolumeLister
	PersistentVolumeClaimLister
}

// CmdbStore is the subset of api.Store the collector consumes. Exported so
// the apiclient package (push-mode HTTP store) can implement it.
type CmdbStore interface {
	CreateCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, error)
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
	UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error)
	DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error)
	UpsertPersistentVolumeClaim(ctx context.Context, in api.PersistentVolumeClaimCreate) (api.PersistentVolumeClaim, error)
	DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error)
}

// Collector polls a KubeSource and reconciles the results into the CMDB
// store against a cluster record matched by name. Errors encountered during
// a single tick are logged and the loop continues to the next tick.
type Collector struct {
	store        CmdbStore
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
func New(store CmdbStore, source KubeSource, clusterName string, interval, fetchTimeout time.Duration, reconcile bool) *Collector {
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
	slog.Info("collector starting", slog.String("cluster_name", c.clusterName), slog.Duration("interval", c.interval))

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	c.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("collector stopping", slog.Any("reason", ctx.Err()))
			return fmt.Errorf("collector stopped: %w", ctx.Err())
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
		slog.Warn("collector: fetch server version failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return
	}
	metrics.MarkPoll(c.clusterName, "version")

	cluster, err := c.store.GetClusterByName(ctx, c.clusterName)
	if err != nil {
		if errors.Is(err, api.ErrNotFound) {
			// Auto-create the cluster on first contact.
			cluster, err = c.store.CreateCluster(ctx, api.ClusterCreate{Name: c.clusterName})
			if err != nil {
				metrics.ObserveError(c.clusterName, "cluster", "create")
				slog.Error("collector: auto-create cluster failed",
					slog.Any("error", err), slog.String("cluster_name", c.clusterName))
				return
			}
			slog.Info("collector: auto-created cluster", slog.String("cluster_name", c.clusterName))
		} else {
			metrics.ObserveError(c.clusterName, "cluster", "lookup")
			slog.Error("collector: lookup cluster failed",
				slog.Any("error", err), slog.String("cluster_name", c.clusterName))
			return
		}
	}
	if cluster.Id == nil {
		slog.Error("collector: stored cluster has nil id", slog.String("cluster_name", c.clusterName))
		return
	}

	if cluster.KubernetesVersion == nil || *cluster.KubernetesVersion != version {
		if _, err := c.store.UpdateCluster(ctx, *cluster.Id, api.ClusterUpdate{KubernetesVersion: &version}); err != nil {
			metrics.ObserveError(c.clusterName, "cluster", "upsert")
			slog.Error("collector: update cluster failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
			return
		}
		metrics.ObserveUpserts(c.clusterName, "cluster", 1)
		slog.Info("collector: refreshed cluster version", slog.String("cluster_name", c.clusterName), slog.String("version", version))
	}

	c.ingestNodes(ctx, *cluster.Id)
	// PVs are cluster-scoped — they don't depend on namespaces so we ingest
	// them before namespace-scoped resources. The returned (pv-name -> id)
	// map is used by ingestPersistentVolumeClaims to resolve bound_volume_id.
	pvIDsByName := c.ingestPersistentVolumes(ctx, *cluster.Id)
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
		c.ingestPersistentVolumeClaims(ctx, namespaceIDsByName, pvIDsByName)
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
		slog.Warn("collector: list nodes failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return
	}

	var upserted, failed int
	keepNames := make([]string, 0, len(nodes))
	for i := range nodes {
		n := &nodes[i]
		in := api.NodeCreate{
			ClusterId:                   clusterID,
			Name:                        n.Name,
			Role:                        ptrIfNonEmpty(n.Role),
			KubeletVersion:              ptrIfNonEmpty(n.KubeletVersion),
			KubeProxyVersion:            ptrIfNonEmpty(n.KubeProxyVersion),
			ContainerRuntimeVersion:     ptrIfNonEmpty(n.ContainerRuntimeVersion),
			OsImage:                     ptrIfNonEmpty(n.OsImage),
			OperatingSystem:             ptrIfNonEmpty(n.OperatingSystem),
			KernelVersion:               ptrIfNonEmpty(n.KernelVersion),
			Architecture:                ptrIfNonEmpty(n.Architecture),
			InternalIp:                  ptrIfNonEmpty(n.InternalIP),
			ExternalIp:                  ptrIfNonEmpty(n.ExternalIP),
			PodCidr:                     ptrIfNonEmpty(n.PodCIDR),
			ProviderId:                  ptrIfNonEmpty(n.ProviderID),
			InstanceType:                ptrIfNonEmpty(n.InstanceType),
			Zone:                        ptrIfNonEmpty(n.Zone),
			CapacityCpu:                 ptrIfNonEmpty(n.CapacityCPU),
			CapacityMemory:              ptrIfNonEmpty(n.CapacityMemory),
			CapacityPods:                ptrIfNonEmpty(n.CapacityPods),
			CapacityEphemeralStorage:    ptrIfNonEmpty(n.CapacityEphemeralStorage),
			AllocatableCpu:              ptrIfNonEmpty(n.AllocatableCPU),
			AllocatableMemory:           ptrIfNonEmpty(n.AllocatableMemory),
			AllocatablePods:             ptrIfNonEmpty(n.AllocatablePods),
			AllocatableEphemeralStorage: ptrIfNonEmpty(n.AllocatableEphemeralStorage),
			Unschedulable:               &n.Unschedulable,
			Ready:                       &n.Ready,
		}
		if len(n.Conditions) > 0 {
			conds := n.Conditions
			in.Conditions = &conds
		}
		if len(n.Taints) > 0 {
			taints := n.Taints
			in.Taints = &taints
		}
		if len(n.Labels) > 0 {
			labels := n.Labels
			in.Labels = &labels
		}
		if _, err := c.store.UpsertNode(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "nodes", "upsert")
			slog.Warn("collector: upsert node failed",
				slog.Any("error", err), slog.String("node", n.Name), slog.String("cluster_name", c.clusterName))
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
			slog.Error("collector: reconcile nodes failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		}
		reconciled = n
		metrics.ObserveReconciled(c.clusterName, "nodes", n)
	}
	metrics.MarkPoll(c.clusterName, "nodes")
	slog.Info("collector: ingested nodes",
		slog.Int("upserted", upserted), slog.Int("failed", failed),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
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
		slog.Warn("collector: list namespaces failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
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
			slog.Warn("collector: upsert namespace failed",
				slog.Any("error", err), slog.String("namespace", ns.Name), slog.String("cluster_name", c.clusterName))
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
			slog.Error("collector: reconcile namespaces failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		}
		reconciled = n
		metrics.ObserveReconciled(c.clusterName, "namespaces", n)
	}
	metrics.MarkPoll(c.clusterName, "namespaces")
	slog.Info("collector: ingested namespaces",
		slog.Int("upserted", upserted), slog.Int("failed", failed),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
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
		slog.Warn("collector: list pods failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
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
			slog.Warn("collector: list replicasets failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
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
	for i := range pods {
		p := &pods[i]
		nsID, ok := namespaceIDsByName[p.Namespace]
		if !ok {
			slog.Warn("collector: pod in unknown namespace; skipping",
				slog.String("pod", p.Name), slog.String("namespace", p.Namespace), slog.String("cluster_name", c.clusterName))
			skipped++
			continue
		}
		in := buildPodCreate(p, nsID, workloadIDs, rsOwners)
		if _, err := c.store.UpsertPod(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "pods", "upsert")
			slog.Warn("collector: upsert pod failed",
				slog.Any("error", err), slog.String("pod", p.Name), slog.String("namespace", p.Namespace), slog.String("cluster_name", c.clusterName))
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], p.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "pods", upserted)

	var reconciled int64
	if c.reconcile {
		reconciled = c.reconcilePerNamespace(ctx, "pods", namespaceIDsByName, keepByNS, c.store.DeletePodsNotIn)
		metrics.ObserveReconciled(c.clusterName, "pods", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "pods")
	slog.Info("collector: ingested pods",
		slog.Int("upserted", upserted), slog.Int("failed", failed), slog.Int("skipped", skipped),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
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
		slog.Warn("collector: list workloads failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return nil
	}

	keepByNS := make(map[uuid.UUID][]wlKey)
	idsByNS := make(map[uuid.UUID]map[wlKey]uuid.UUID)

	var upserted, failed, skipped int
	for _, w := range workloads {
		nsID, ok := namespaceIDsByName[w.Namespace]
		if !ok {
			slog.Warn("collector: workload in unknown namespace; skipping",
				slog.String("workload", w.Name), slog.Any("kind", w.Kind),
				slog.String("namespace", w.Namespace), slog.String("cluster_name", c.clusterName))
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
			cs := w.Containers
			in.Containers = &cs
		}
		if len(w.Labels) > 0 {
			labels := w.Labels
			in.Labels = &labels
		}
		stored, err := c.store.UpsertWorkload(ctx, in)
		if err != nil {
			metrics.ObserveError(c.clusterName, "workloads", "upsert")
			slog.Warn("collector: upsert workload failed",
				slog.Any("error", err), slog.String("workload", w.Name), slog.Any("kind", w.Kind),
				slog.String("namespace", w.Namespace), slog.String("cluster_name", c.clusterName))
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
		reconciled = c.reconcileWorkloads(ctx, namespaceIDsByName, keepByNS)
		metrics.ObserveReconciled(c.clusterName, "workloads", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "workloads")
	slog.Info("collector: ingested workloads",
		slog.Int("upserted", upserted), slog.Int("failed", failed), slog.Int("skipped", skipped),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
	return idsByNS
}

// ingestServices lists services cluster-wide, resolves each one's K8s
// namespace name to the CMDB namespace UUID, and upserts it. Per-namespace
// reconcile mirrors ingestPods.
func (c *Collector) ingestServices(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	services, err := c.source.ListServices(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "services", "list")
		slog.Warn("collector: list services failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return
	}

	keepByNS := make(map[uuid.UUID][]string)

	var upserted, failed, skipped int
	for i := range services {
		s := &services[i]
		nsID, ok := namespaceIDsByName[s.Namespace]
		if !ok {
			slog.Warn("collector: service in unknown namespace; skipping",
				slog.String("service", s.Name), slog.String("namespace", s.Namespace), slog.String("cluster_name", c.clusterName))
			skipped++
			continue
		}
		in := buildServiceCreate(s, nsID)
		if _, err := c.store.UpsertService(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "services", "upsert")
			slog.Warn("collector: upsert service failed",
				slog.Any("error", err), slog.String("service", s.Name),
				slog.String("namespace", s.Namespace), slog.String("cluster_name", c.clusterName))
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], s.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "services", upserted)

	var reconciled int64
	if c.reconcile {
		reconciled = c.reconcilePerNamespace(ctx, "services", namespaceIDsByName, keepByNS, c.store.DeleteServicesNotIn)
		metrics.ObserveReconciled(c.clusterName, "services", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "services")
	slog.Info("collector: ingested services",
		slog.Int("upserted", upserted), slog.Int("failed", failed), slog.Int("skipped", skipped),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
}

// ingestIngresses lists ingresses cluster-wide, resolves each one's K8s
// namespace name to the CMDB namespace UUID, and upserts it. Per-namespace
// reconcile mirrors ingestServices.
func (c *Collector) ingestIngresses(ctx context.Context, namespaceIDsByName map[string]uuid.UUID) {
	ingresses, err := c.source.ListIngresses(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "ingresses", "list")
		slog.Warn("collector: list ingresses failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return
	}

	keepByNS := make(map[uuid.UUID][]string)

	var upserted, failed, skipped int
	for i := range ingresses {
		ing := &ingresses[i]
		nsID, ok := namespaceIDsByName[ing.Namespace]
		if !ok {
			slog.Warn("collector: ingress in unknown namespace; skipping",
				slog.String("ingress", ing.Name), slog.String("namespace", ing.Namespace), slog.String("cluster_name", c.clusterName))
			skipped++
			continue
		}
		in := buildIngressCreate(ing, nsID)
		if _, err := c.store.UpsertIngress(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "ingresses", "upsert")
			slog.Warn("collector: upsert ingress failed",
				slog.Any("error", err), slog.String("ingress", ing.Name),
				slog.String("namespace", ing.Namespace), slog.String("cluster_name", c.clusterName))
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], ing.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "ingresses", upserted)

	var reconciled int64
	if c.reconcile {
		reconciled = c.reconcilePerNamespace(ctx, "ingresses", namespaceIDsByName, keepByNS, c.store.DeleteIngressesNotIn)
		metrics.ObserveReconciled(c.clusterName, "ingresses", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "ingresses")
	slog.Info("collector: ingested ingresses",
		slog.Int("upserted", upserted), slog.Int("failed", failed), slog.Int("skipped", skipped),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrIfNonEmptySlice(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	return &s
}

// deleteFunc is a namespace-scoped reconcile callback.
type deleteFunc func(ctx context.Context, nsID uuid.UUID, keepNames []string) (int64, error)

// reconcilePerNamespace drives the reconcile-per-namespace pattern shared by
// pods, services, ingresses and PVCs. It returns the total number of deleted
// rows across all namespaces.
func (c *Collector) reconcilePerNamespace(
	ctx context.Context,
	resource string,
	namespaceIDsByName map[string]uuid.UUID,
	keepByNS map[uuid.UUID][]string,
	deleteFn deleteFunc,
) int64 {
	var reconciled int64
	for _, nsID := range namespaceIDsByName {
		n, err := deleteFn(ctx, nsID, keepByNS[nsID])
		if err != nil {
			metrics.ObserveError(c.clusterName, resource, "reconcile")
			slog.Error("collector: reconcile "+resource+" failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster_name", c.clusterName))
			continue
		}
		reconciled += n
	}
	return reconciled
}

// reconcileWorkloads reconciles workloads per-namespace keyed on the (kind, name)
// tuple so a deleted Deployment 'web' doesn't wipe the still-live StatefulSet 'web'.
func (c *Collector) reconcileWorkloads(
	ctx context.Context,
	namespaceIDsByName map[string]uuid.UUID,
	keepByNS map[uuid.UUID][]wlKey,
) int64 {
	var reconciled int64
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
			slog.Error("collector: reconcile workloads failed",
				slog.Any("error", err), slog.String("namespace_id", nsID.String()), slog.String("cluster_name", c.clusterName))
			continue
		}
		reconciled += n
	}
	return reconciled
}

// buildPodCreate converts a PodInfo to an api.PodCreate, resolving namespace
// and workload IDs.
func buildPodCreate(
	p *PodInfo,
	nsID uuid.UUID,
	workloadIDs map[uuid.UUID]map[wlKey]uuid.UUID,
	rsOwners map[string]ReplicaSetOwner,
) api.PodCreate {
	in := api.PodCreate{
		NamespaceId: nsID,
		Name:        p.Name,
		Phase:       ptrIfNonEmpty(p.Phase),
		NodeName:    ptrIfNonEmpty(p.NodeName),
		PodIp:       ptrIfNonEmpty(p.PodIP),
	}
	if len(p.Containers) > 0 {
		cs := p.Containers
		in.Containers = &cs
	}
	if len(p.Labels) > 0 {
		labels := p.Labels
		in.Labels = &labels
	}
	if wid := resolveWorkloadID(nsID, p.OwnerKind, p.OwnerName, workloadIDs, rsOwners); wid != nil {
		in.WorkloadId = wid
	}
	return in
}

// buildServiceCreate converts a ServiceInfo to an api.ServiceCreate.
func buildServiceCreate(s *ServiceInfo, nsID uuid.UUID) api.ServiceCreate {
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
	if len(s.LoadBalancer) > 0 {
		lb := s.LoadBalancer
		in.LoadBalancer = &lb
	}
	if len(s.Labels) > 0 {
		labels := s.Labels
		in.Labels = &labels
	}
	return in
}

// buildIngressCreate converts an IngressInfo to an api.IngressCreate.
func buildIngressCreate(ing *IngressInfo, nsID uuid.UUID) api.IngressCreate {
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
	if len(ing.LoadBalancer) > 0 {
		lb := ing.LoadBalancer
		in.LoadBalancer = &lb
	}
	if len(ing.Labels) > 0 {
		labels := ing.Labels
		in.Labels = &labels
	}
	return in
}

// buildPVCCreate converts a PVCInfo to an api.PersistentVolumeClaimCreate.
func buildPVCCreate(pvc *PVCInfo, nsID uuid.UUID, pvIDsByName map[string]uuid.UUID) api.PersistentVolumeClaimCreate {
	in := api.PersistentVolumeClaimCreate{
		NamespaceId:      nsID,
		Name:             pvc.Name,
		Phase:            ptrIfNonEmpty(pvc.Phase),
		StorageClassName: ptrIfNonEmpty(pvc.StorageClassName),
		VolumeName:       ptrIfNonEmpty(pvc.VolumeName),
		AccessModes:      ptrIfNonEmptySlice(pvc.AccessModes),
		RequestedStorage: ptrIfNonEmpty(pvc.RequestedStorage),
	}
	if len(pvc.Labels) > 0 {
		labels := pvc.Labels
		in.Labels = &labels
	}
	if pvc.VolumeName != "" && pvIDsByName != nil {
		if pvID, found := pvIDsByName[pvc.VolumeName]; found {
			in.BoundVolumeId = &pvID
		}
	}
	return in
}

// ingestPersistentVolumes lists cluster-scoped PVs and upserts each one.
// Returns a (pv-name -> pv-id) map the PVC ingestion pass uses to resolve
// each PVC's bound_volume_id FK, or nil on list failure (signal to skip
// FK resolution). Reconcile is cluster-scoped since PVs aren't namespaced.
func (c *Collector) ingestPersistentVolumes(ctx context.Context, clusterID uuid.UUID) map[string]uuid.UUID {
	pvs, err := c.source.ListPersistentVolumes(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "persistentvolumes", "list")
		slog.Warn("collector: list persistent volumes failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return nil
	}

	var upserted, failed int
	keepNames := make([]string, 0, len(pvs))
	idsByName := make(map[string]uuid.UUID, len(pvs))
	for i := range pvs {
		pv := &pvs[i]
		in := api.PersistentVolumeCreate{
			ClusterId:         clusterID,
			Name:              pv.Name,
			Capacity:          ptrIfNonEmpty(pv.Capacity),
			AccessModes:       ptrIfNonEmptySlice(pv.AccessModes),
			ReclaimPolicy:     ptrIfNonEmpty(pv.ReclaimPolicy),
			Phase:             ptrIfNonEmpty(pv.Phase),
			StorageClassName:  ptrIfNonEmpty(pv.StorageClassName),
			CsiDriver:         ptrIfNonEmpty(pv.CSIDriver),
			VolumeHandle:      ptrIfNonEmpty(pv.VolumeHandle),
			ClaimRefNamespace: ptrIfNonEmpty(pv.ClaimRefNamespace),
			ClaimRefName:      ptrIfNonEmpty(pv.ClaimRefName),
		}
		if len(pv.Labels) > 0 {
			labels := pv.Labels
			in.Labels = &labels
		}
		stored, err := c.store.UpsertPersistentVolume(ctx, in)
		if err != nil {
			metrics.ObserveError(c.clusterName, "persistentvolumes", "upsert")
			slog.Warn("collector: upsert persistent volume failed",
				slog.Any("error", err), slog.String("pv", pv.Name), slog.String("cluster_name", c.clusterName))
			failed++
			continue
		}
		upserted++
		keepNames = append(keepNames, pv.Name)
		if stored.Id != nil {
			idsByName[pv.Name] = *stored.Id
		}
	}
	metrics.ObserveUpserts(c.clusterName, "persistentvolumes", upserted)

	var reconciled int64
	if c.reconcile {
		n, err := c.store.DeletePersistentVolumesNotIn(ctx, clusterID, keepNames)
		if err != nil {
			metrics.ObserveError(c.clusterName, "persistentvolumes", "reconcile")
			slog.Error("collector: reconcile persistent volumes failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		}
		reconciled = n
		metrics.ObserveReconciled(c.clusterName, "persistentvolumes", n)
	}
	metrics.MarkPoll(c.clusterName, "persistentvolumes")
	slog.Info("collector: ingested persistent volumes",
		slog.Int("upserted", upserted), slog.Int("failed", failed),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
	return idsByName
}

// ingestPersistentVolumeClaims lists PVCs cluster-wide, resolves each one's
// K8s namespace name to the CMDB namespace UUID, and upserts it. When the
// PVC's spec.volumeName matches a PV upserted this tick, the FK is set;
// otherwise bound_volume_id stays null (pending, or PV not yet ingested).
//
// pvIDsByName may be nil (PV listing failed) — PVCs are still upserted,
// just without bound_volume_id set.
func (c *Collector) ingestPersistentVolumeClaims(ctx context.Context, namespaceIDsByName, pvIDsByName map[string]uuid.UUID) {
	pvcs, err := c.source.ListPersistentVolumeClaims(ctx)
	if err != nil {
		metrics.ObserveError(c.clusterName, "persistentvolumeclaims", "list")
		slog.Warn("collector: list pvcs failed", slog.Any("error", err), slog.String("cluster_name", c.clusterName))
		return
	}

	var upserted, failed, skipped int
	keepByNS := make(map[uuid.UUID][]string)
	for i := range pvcs {
		pvc := &pvcs[i]
		nsID, ok := namespaceIDsByName[pvc.Namespace]
		if !ok {
			slog.Warn("collector: pvc in unknown namespace; skipping",
				slog.String("pvc", pvc.Name), slog.String("namespace", pvc.Namespace), slog.String("cluster_name", c.clusterName))
			skipped++
			continue
		}
		in := buildPVCCreate(pvc, nsID, pvIDsByName)
		if _, err := c.store.UpsertPersistentVolumeClaim(ctx, in); err != nil {
			metrics.ObserveError(c.clusterName, "persistentvolumeclaims", "upsert")
			slog.Warn("collector: upsert pvc failed",
				slog.Any("error", err), slog.String("pvc", pvc.Name),
				slog.String("namespace", pvc.Namespace), slog.String("cluster_name", c.clusterName))
			failed++
			continue
		}
		upserted++
		keepByNS[nsID] = append(keepByNS[nsID], pvc.Name)
	}
	metrics.ObserveUpserts(c.clusterName, "persistentvolumeclaims", upserted)

	var reconciled int64
	if c.reconcile {
		reconciled = c.reconcilePerNamespace(
			ctx, "persistentvolumeclaims", namespaceIDsByName, keepByNS, c.store.DeletePersistentVolumeClaimsNotIn)
		metrics.ObserveReconciled(c.clusterName, "persistentvolumeclaims", reconciled)
	}
	metrics.MarkPoll(c.clusterName, "persistentvolumeclaims")
	slog.Info("collector: ingested pvcs",
		slog.Int("upserted", upserted), slog.Int("failed", failed), slog.Int("skipped", skipped),
		slog.Int64("reconciled_deleted", reconciled), slog.String("cluster_name", c.clusterName))
}
