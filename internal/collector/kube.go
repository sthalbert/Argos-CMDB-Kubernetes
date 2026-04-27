package collector

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sthalbert/argos/internal/api"
)

// podContainers flattens the runtime view of a Pod's containers. It prefers
// containerStatuses (post-schedule, carries image_id digest) and falls back
// to spec.containers when status isn't populated yet (Pending pods). Init
// containers are included with init=true.
func podContainers(p *corev1.Pod) []map[string]interface{} {
	out := make([]map[string]interface{}, 0,
		len(p.Status.ContainerStatuses)+len(p.Status.InitContainerStatuses))

	statusIdx := make(map[string]corev1.ContainerStatus, len(p.Status.ContainerStatuses))
	for i := range p.Status.ContainerStatuses {
		cs := &p.Status.ContainerStatuses[i]
		statusIdx[cs.Name] = *cs
	}
	initStatusIdx := make(map[string]corev1.ContainerStatus, len(p.Status.InitContainerStatuses))
	for i := range p.Status.InitContainerStatuses {
		cs := &p.Status.InitContainerStatuses[i]
		initStatusIdx[cs.Name] = *cs
	}

	emit := func(name, specImage string, isInit bool, cs corev1.ContainerStatus, hasStatus bool) {
		image := specImage
		var imageID string
		if hasStatus {
			if cs.Image != "" {
				image = cs.Image
			}
			imageID = cs.ImageID
		}
		entry := map[string]interface{}{
			"name":  name,
			"image": image,
			"init":  isInit,
		}
		if imageID != "" {
			entry["image_id"] = imageID
		}
		out = append(out, entry)
	}

	for i := range p.Spec.Containers {
		c := &p.Spec.Containers[i]
		cs, ok := statusIdx[c.Name]
		emit(c.Name, c.Image, false, cs, ok)
	}
	for i := range p.Spec.InitContainers {
		c := &p.Spec.InitContainers[i]
		cs, ok := initStatusIdx[c.Name]
		emit(c.Name, c.Image, true, cs, ok)
	}
	return out
}

// podTemplateContainers flattens a pod template's declared containers.
// Used by Workload ingestion, where status-resolved image IDs aren't
// available (those live on the owned Pods).
func podTemplateContainers(tpl *corev1.PodSpec) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tpl.Containers)+len(tpl.InitContainers))
	for i := range tpl.Containers {
		c := &tpl.Containers[i]
		out = append(out, map[string]interface{}{"name": c.Name, "image": c.Image, "init": false})
	}
	for i := range tpl.InitContainers {
		c := &tpl.InitContainers[i]
		out = append(out, map[string]interface{}{"name": c.Name, "image": c.Image, "init": true})
	}
	return out
}

// netv1Backend flattens a NetworkingV1 IngressBackend into the generic map
// shape the CMDB persists as JSONB. Returns nil when the backend isn't a
// Service-backed one (a resource backend, for instance).
func netv1Backend(b *networkingv1.IngressBackend) map[string]interface{} {
	if b == nil || b.Service == nil {
		return nil
	}
	out := map[string]interface{}{"service_name": b.Service.Name}
	if b.Service.Port.Name != "" {
		out["service_port_name"] = b.Service.Port.Name
	}
	if b.Service.Port.Number != 0 {
		out["service_port_number"] = int(b.Service.Port.Number)
	}
	return out
}

// KubeClient talks to a single Kubernetes cluster via client-go. It
// satisfies KubeSource (ServerVersion + ListNodes). The target cluster is
// whatever the loaded kubeconfig points at.
type KubeClient struct {
	clientset *kubernetes.Clientset
}

// NewKubeClient constructs a client. Resolution order:
//   - explicit kubeconfigPath when non-empty;
//   - in-cluster config when running inside a pod;
//   - the default kubectl loading rules (KUBECONFIG env var, then ~/.kube/config).
func NewKubeClient(kubeconfigPath string) (*KubeClient, error) {
	cfg, err := loadKubeConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes.NewForConfig: %w", err)
	}
	return &KubeClient{clientset: cs}, nil
}

func loadKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	if kubeconfigPath != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("build config from %q: %w", kubeconfigPath, err)
		}
		return cfg, nil
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load default kubeconfig: %w", err)
	}
	return cfg, nil
}

// ServerVersion returns the cluster's reported git version (e.g., "v1.29.5").
// client-go's discovery.ServerVersion() does not accept a context, so the
// parameter is retained for interface compatibility but unused.
func (k *KubeClient) ServerVersion(_ context.Context) (string, error) {
	info, err := k.clientset.Discovery().ServerVersion()
	if err != nil {
		return "", fmt.Errorf("discovery server version: %w", err)
	}
	return info.GitVersion, nil
}

// ListNodes returns every Node visible through the configured kubeconfig.
// A single List call is used; paginating via Continue is unnecessary at
// the cluster-wide node counts this project targets.
//
// Most fields on NodeInfo come straight from the Node's status subtree.
// Role is derived from the `node-role.kubernetes.io/*` labels (the
// well-known convention since K8s 1.16); instance_type and zone come from
// the matching well-known labels so cloud identity surfaces without
// parsing spec.providerID. Conditions and taints are flattened into
// generic maps so the store persists them as JSONB without coupling to
// client-go types.
func (k *KubeClient) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	list, err := k.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeInfo, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		internal, external := nodeAddresses(n)
		// KubeProxyVersion was deprecated in K8s 1.31 because kube-proxy is
		// optional (Cilium, kube-router with proxy-less mode). The field
		// still exists in the API and remains populated by conventional
		// distributions — keep reading it; empty string is fine when the
		// cluster omits it. staticcheck flags the access, so suppress it
		// locally rather than drop a field that classical distributions still surface.
		//nolint:staticcheck // SA1019 — deprecated but still relevant for classic clusters.
		kubeProxyVersion := n.Status.NodeInfo.KubeProxyVersion

		info := NodeInfo{
			Name:                        n.Name,
			Role:                        nodeRole(n),
			KubeletVersion:              n.Status.NodeInfo.KubeletVersion,
			KubeProxyVersion:            kubeProxyVersion,
			ContainerRuntimeVersion:     n.Status.NodeInfo.ContainerRuntimeVersion,
			OsImage:                     n.Status.NodeInfo.OSImage,
			OperatingSystem:             n.Status.NodeInfo.OperatingSystem,
			KernelVersion:               n.Status.NodeInfo.KernelVersion,
			Architecture:                n.Status.NodeInfo.Architecture,
			InternalIP:                  internal,
			ExternalIP:                  external,
			PodCIDR:                     n.Spec.PodCIDR,
			ProviderID:                  n.Spec.ProviderID,
			InstanceType:                n.Labels["node.kubernetes.io/instance-type"],
			Zone:                        n.Labels["topology.kubernetes.io/zone"],
			CapacityCPU:                 quantityString(n.Status.Capacity, corev1.ResourceCPU),
			CapacityMemory:              quantityString(n.Status.Capacity, corev1.ResourceMemory),
			CapacityPods:                quantityString(n.Status.Capacity, corev1.ResourcePods),
			CapacityEphemeralStorage:    quantityString(n.Status.Capacity, corev1.ResourceEphemeralStorage),
			AllocatableCPU:              quantityString(n.Status.Allocatable, corev1.ResourceCPU),
			AllocatableMemory:           quantityString(n.Status.Allocatable, corev1.ResourceMemory),
			AllocatablePods:             quantityString(n.Status.Allocatable, corev1.ResourcePods),
			AllocatableEphemeralStorage: quantityString(n.Status.Allocatable, corev1.ResourceEphemeralStorage),
			Conditions:                  nodeConditions(n),
			Taints:                      nodeTaints(n),
			Unschedulable:               n.Spec.Unschedulable,
			Ready:                       nodeReady(n),
			Labels:                      n.Labels,
		}
		out = append(out, info)
	}
	return out, nil
}

// nodeRole prefers the standard "control-plane" label, falls back to the
// pre-1.24 "master" alias, and finally returns "worker" for nodes without
// any role label — matching how kubectl get nodes renders the column.
func nodeRole(n *corev1.Node) string {
	for label := range n.Labels {
		const prefix = "node-role.kubernetes.io/"
		if len(label) > len(prefix) && label[:len(prefix)] == prefix {
			role := label[len(prefix):]
			if role == "master" {
				return "control-plane"
			}
			return role
		}
	}
	return "worker"
}

// nodeAddresses pulls the first InternalIP and first ExternalIP out of
// status.addresses. Nodes may advertise several of each; we surface the
// first for the detail view.
func nodeAddresses(n *corev1.Node) (internal, external string) {
	for _, a := range n.Status.Addresses {
		switch a.Type {
		case corev1.NodeInternalIP:
			if internal == "" {
				internal = a.Address
			}
		case corev1.NodeExternalIP:
			if external == "" {
				external = a.Address
			}
		}
	}
	return internal, external
}

// quantityString renders resource.Quantity via its canonical String() so
// storage stays in the unit Kubernetes emitted (Gi/Mi/m/etc.), which is
// what auditors expect to see.
func quantityString(list corev1.ResourceList, name corev1.ResourceName) string {
	if q, ok := list[name]; ok {
		return q.String()
	}
	return ""
}

func nodeConditions(n *corev1.Node) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(n.Status.Conditions))
	for _, c := range n.Status.Conditions {
		out = append(out, map[string]interface{}{
			"type":                 string(c.Type),
			"status":               string(c.Status),
			"reason":               c.Reason,
			"message":              c.Message,
			"last_transition_time": c.LastTransitionTime.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return out
}

func nodeTaints(n *corev1.Node) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(n.Spec.Taints))
	for _, t := range n.Spec.Taints {
		out = append(out, map[string]interface{}{
			"key":    t.Key,
			"value":  t.Value,
			"effect": string(t.Effect),
		})
	}
	return out
}

func nodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// ListNamespaces returns every Namespace visible through the configured kubeconfig.
func (k *KubeClient) ListNamespaces(ctx context.Context) ([]NamespaceInfo, error) {
	list, err := k.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	out := make([]NamespaceInfo, 0, len(list.Items))
	for i := range list.Items {
		n := &list.Items[i]
		out = append(out, NamespaceInfo{
			Name:   n.Name,
			Phase:  string(n.Status.Phase),
			Labels: n.Labels,
		})
	}
	return out, nil
}

// ListPods returns every Pod visible through the configured kubeconfig,
// across all namespaces. Uses the empty-string namespace selector to query
// pods cluster-wide. Extracts the controlling ownerReference (controller:
// true) into OwnerKind/OwnerName so the collector can resolve each pod's
// top-level Workload.
func (k *KubeClient) ListPods(ctx context.Context) ([]PodInfo, error) {
	list, err := k.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	out := make([]PodInfo, 0, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		ownerKind, ownerName := controllerOwner(p.OwnerReferences)
		out = append(out, PodInfo{
			Name:       p.Name,
			Namespace:  p.Namespace,
			Phase:      string(p.Status.Phase),
			NodeName:   p.Spec.NodeName,
			PodIP:      p.Status.PodIP,
			Containers: podContainers(p),
			Labels:     p.Labels,
			OwnerKind:  ownerKind,
			OwnerName:  ownerName,
		})
	}
	return out, nil
}

// controllerOwner returns the (kind, name) of the ownerReference marked
// controller: true, if any. Pods with no controller — bare pods, or ones
// whose controller was deleted mid-reconcile — return ("", "").
func controllerOwner(refs []metav1.OwnerReference) (kind, name string) {
	for _, r := range refs {
		if r.Controller != nil && *r.Controller {
			return r.Kind, r.Name
		}
	}
	return "", ""
}

// ListPersistentVolumes returns every cluster-scoped PersistentVolume
// visible through the configured kubeconfig. Capacity is rendered via the
// resource.Quantity canonical string (e.g. "10Gi"). CSI-backed volumes
// surface driver + handle; in-tree drivers leave those empty.
func (k *KubeClient) ListPersistentVolumes(ctx context.Context) ([]PVInfo, error) {
	list, err := k.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list persistent volumes: %w", err)
	}
	out := make([]PVInfo, 0, len(list.Items))
	for i := range list.Items {
		pv := &list.Items[i]
		info := PVInfo{
			Name:             pv.Name,
			Phase:            string(pv.Status.Phase),
			ReclaimPolicy:    string(pv.Spec.PersistentVolumeReclaimPolicy),
			StorageClassName: pv.Spec.StorageClassName,
			Labels:           pv.Labels,
		}
		if capacity, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
			info.Capacity = capacity.String()
		}
		if modes := pv.Spec.AccessModes; len(modes) > 0 {
			m := make([]string, 0, len(modes))
			for _, mode := range modes {
				m = append(m, string(mode))
			}
			info.AccessModes = m
		}
		if pv.Spec.CSI != nil {
			info.CSIDriver = pv.Spec.CSI.Driver
			info.VolumeHandle = pv.Spec.CSI.VolumeHandle
		}
		if ref := pv.Spec.ClaimRef; ref != nil {
			info.ClaimRefNamespace = ref.Namespace
			info.ClaimRefName = ref.Name
		}
		out = append(out, info)
	}
	return out, nil
}

// ListPersistentVolumeClaims returns every PVC visible through the
// configured kubeconfig, across all namespaces. VolumeName carries raw
// spec.volumeName (the bound PV name) so the collector can resolve it
// against the PV map each tick.
func (k *KubeClient) ListPersistentVolumeClaims(ctx context.Context) ([]PVCInfo, error) {
	list, err := k.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list persistent volume claims: %w", err)
	}
	out := make([]PVCInfo, 0, len(list.Items))
	for i := range list.Items {
		pvc := &list.Items[i]
		info := PVCInfo{
			Name:       pvc.Name,
			Namespace:  pvc.Namespace,
			Phase:      string(pvc.Status.Phase),
			VolumeName: pvc.Spec.VolumeName,
			Labels:     pvc.Labels,
		}
		if pvc.Spec.StorageClassName != nil {
			info.StorageClassName = *pvc.Spec.StorageClassName
		}
		if modes := pvc.Spec.AccessModes; len(modes) > 0 {
			m := make([]string, 0, len(modes))
			for _, mode := range modes {
				m = append(m, string(mode))
			}
			info.AccessModes = m
		}
		if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			info.RequestedStorage = req.String()
		}
		out = append(out, info)
	}
	return out, nil
}

// ListReplicaSetOwners returns one entry per ReplicaSet visible through the
// configured kubeconfig, carrying its controlling ownerReference (typically
// a Deployment). Used by the collector to walk Pod -> RS -> Deployment
// without per-pod API calls.
func (k *KubeClient) ListReplicaSetOwners(ctx context.Context) ([]ReplicaSetOwner, error) {
	list, err := k.clientset.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list replicasets: %w", err)
	}
	out := make([]ReplicaSetOwner, 0, len(list.Items))
	for i := range list.Items {
		rs := &list.Items[i]
		kind, name := controllerOwner(rs.OwnerReferences)
		out = append(out, ReplicaSetOwner{
			Namespace: rs.Namespace,
			Name:      rs.Name,
			OwnerKind: kind,
			OwnerName: name,
		})
	}
	return out, nil
}

// ListIngresses returns every Ingress visible through the configured
// kubeconfig. Rules and TLS entries are flattened to generic maps so the
// store persists them as JSONB without coupling to client-go types.
func (k *KubeClient) ListIngresses(ctx context.Context) ([]IngressInfo, error) {
	list, err := k.clientset.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list ingresses: %w", err)
	}
	out := make([]IngressInfo, 0, len(list.Items))
	for i := range list.Items {
		ing := &list.Items[i]
		var className string
		if ing.Spec.IngressClassName != nil {
			className = *ing.Spec.IngressClassName
		}
		out = append(out, IngressInfo{
			Name:             ing.Name,
			Namespace:        ing.Namespace,
			IngressClassName: className,
			Rules:            flattenIngressRules(ing.Spec.Rules),
			TLS:              flattenIngressTLS(ing.Spec.TLS),
			LoadBalancer:     netv1LoadBalancerIngress(ing.Status.LoadBalancer.Ingress),
			Labels:           ing.Labels,
		})
	}
	return out, nil
}

// flattenIngressRules converts Kubernetes IngressRules into generic maps.
func flattenIngressRules(rules []networkingv1.IngressRule) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(rules))
	for _, r := range rules {
		paths := []map[string]interface{}{}
		if r.HTTP != nil {
			for j := range r.HTTP.Paths {
				p := &r.HTTP.Paths[j]
				path := map[string]interface{}{
					"path": p.Path,
				}
				if p.PathType != nil {
					path["path_type"] = string(*p.PathType)
				}
				if backend := netv1Backend(&p.Backend); backend != nil {
					path["backend"] = backend
				}
				paths = append(paths, path)
			}
		}
		out = append(out, map[string]interface{}{"host": r.Host, "paths": paths})
	}
	return out
}

// flattenIngressTLS converts Kubernetes IngressTLS entries into generic maps.
func flattenIngressTLS(tls []networkingv1.IngressTLS) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tls))
	for _, t := range tls {
		out = append(out, map[string]interface{}{
			"hosts":       t.Hosts,
			"secret_name": t.SecretName,
		})
	}
	return out
}

// netv1LoadBalancerIngress flattens NetworkingV1 LB entries into the
// generic JSONB shape the store persists. Mirrors what controllers
// write to status.loadBalancer.ingress[] — a mix of IP, hostname, and
// optional per-port details. On-prem setups typically land with one
// entry carrying the VIP from MetalLB / Kube-VIP / the hardware LB.
func netv1LoadBalancerIngress(ingress []networkingv1.IngressLoadBalancerIngress) []map[string]interface{} {
	if len(ingress) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(ingress))
	for _, entry := range ingress {
		item := map[string]interface{}{}
		if entry.IP != "" {
			item["ip"] = entry.IP
		}
		if entry.Hostname != "" {
			item["hostname"] = entry.Hostname
		}
		if len(entry.Ports) > 0 {
			ports := make([]map[string]interface{}, 0, len(entry.Ports))
			for _, p := range entry.Ports {
				port := map[string]interface{}{"port": int(p.Port), "protocol": string(p.Protocol)}
				if p.Error != nil && *p.Error != "" {
					port["error"] = *p.Error
				}
				ports = append(ports, port)
			}
			item["ports"] = ports
		}
		out = append(out, item)
	}
	return out
}

// corev1LoadBalancerIngress flattens CoreV1 LB entries — same shape as
// the NetworkingV1 helper, but a different type. Services carry LB
// entries when type=LoadBalancer and something has fulfilled them.
func corev1LoadBalancerIngress(ingress []corev1.LoadBalancerIngress) []map[string]interface{} {
	if len(ingress) == 0 {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(ingress))
	for _, entry := range ingress {
		item := map[string]interface{}{}
		if entry.IP != "" {
			item["ip"] = entry.IP
		}
		if entry.Hostname != "" {
			item["hostname"] = entry.Hostname
		}
		if len(entry.Ports) > 0 {
			ports := make([]map[string]interface{}, 0, len(entry.Ports))
			for _, p := range entry.Ports {
				port := map[string]interface{}{"port": int(p.Port), "protocol": string(p.Protocol)}
				if p.Error != nil && *p.Error != "" {
					port["error"] = *p.Error
				}
				ports = append(ports, port)
			}
			item["ports"] = ports
		}
		out = append(out, item)
	}
	return out
}

// ListServices returns every Service visible through the configured kubeconfig,
// across all namespaces.
func (k *KubeClient) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	list, err := k.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	out := make([]ServiceInfo, 0, len(list.Items))
	for i := range list.Items {
		s := &list.Items[i]
		out = append(out, ServiceInfo{
			Name:         s.Name,
			Namespace:    s.Namespace,
			Type:         string(s.Spec.Type),
			ClusterIP:    s.Spec.ClusterIP,
			Selector:     s.Spec.Selector,
			Ports:        flattenServicePorts(s.Spec.Ports),
			LoadBalancer: corev1LoadBalancerIngress(s.Status.LoadBalancer.Ingress),
			Labels:       s.Labels,
		})
	}
	return out, nil
}

// flattenServicePorts converts Kubernetes ServicePorts into generic maps.
func flattenServicePorts(ports []corev1.ServicePort) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(ports))
	for i := range ports {
		p := &ports[i]
		entry := map[string]interface{}{
			"name":        p.Name,
			"port":        int(p.Port),
			"protocol":    string(p.Protocol),
			"target_port": p.TargetPort.String(),
		}
		if p.NodePort != 0 {
			entry["node_port"] = int(p.NodePort)
		}
		out = append(out, entry)
	}
	return out
}

// ListWorkloads fans out three AppsV1 list calls (Deployments, StatefulSets,
// DaemonSets) and folds the results into a single slice tagged by kind. Any
// list failure aborts the whole call — partial ingestion risks wiping
// kinds that weren't listed when the collector reconciles.
func (k *KubeClient) ListWorkloads(ctx context.Context) ([]WorkloadInfo, error) {
	out := make([]WorkloadInfo, 0)

	deployments, err := k.listDeployments(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, deployments...)

	statefulSets, err := k.listStatefulSets(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, statefulSets...)

	daemonSets, err := k.listDaemonSets(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, daemonSets...)

	return out, nil
}

func (k *KubeClient) listDeployments(ctx context.Context) ([]WorkloadInfo, error) {
	deps, err := k.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	out := make([]WorkloadInfo, 0, len(deps.Items))
	for i := range deps.Items {
		d := &deps.Items[i]
		replicas := int(d.Status.Replicas)
		readyReplicas := int(d.Status.ReadyReplicas)
		out = append(out, WorkloadInfo{
			Name:          d.Name,
			Namespace:     d.Namespace,
			Kind:          api.Deployment,
			Replicas:      &replicas,
			ReadyReplicas: &readyReplicas,
			Containers:    podTemplateContainers(&d.Spec.Template.Spec),
			Labels:        d.Labels,
		})
	}
	return out, nil
}

func (k *KubeClient) listStatefulSets(ctx context.Context) ([]WorkloadInfo, error) {
	sfs, err := k.clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}
	out := make([]WorkloadInfo, 0, len(sfs.Items))
	for i := range sfs.Items {
		s := &sfs.Items[i]
		replicas := int(s.Status.Replicas)
		readyReplicas := int(s.Status.ReadyReplicas)
		out = append(out, WorkloadInfo{
			Name:          s.Name,
			Namespace:     s.Namespace,
			Kind:          api.StatefulSet,
			Replicas:      &replicas,
			ReadyReplicas: &readyReplicas,
			Containers:    podTemplateContainers(&s.Spec.Template.Spec),
			Labels:        s.Labels,
		})
	}
	return out, nil
}

func (k *KubeClient) listDaemonSets(ctx context.Context) ([]WorkloadInfo, error) {
	dss, err := k.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list daemonsets: %w", err)
	}
	out := make([]WorkloadInfo, 0, len(dss.Items))
	for i := range dss.Items {
		d := &dss.Items[i]
		// DaemonSet has no scalar desired count; expose ReadyReplicas only
		// via Status.NumberReady so the CMDB surfaces observable fleet size.
		readyReplicas := int(d.Status.NumberReady)
		out = append(out, WorkloadInfo{
			Name:          d.Name,
			Namespace:     d.Namespace,
			Kind:          api.DaemonSet,
			ReadyReplicas: &readyReplicas,
			Containers:    podTemplateContainers(&d.Spec.Template.Spec),
			Labels:        d.Labels,
		})
	}
	return out, nil
}
