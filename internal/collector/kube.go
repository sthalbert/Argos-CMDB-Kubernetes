package collector

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/sthalbert/argos/internal/api"
)

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
		return "", err
	}
	return info.GitVersion, nil
}

// ListNodes returns every Node visible through the configured kubeconfig.
// A single List call is used; paginating via Continue is unnecessary at
// the cluster-wide node counts this project targets.
func (k *KubeClient) ListNodes(ctx context.Context) ([]NodeInfo, error) {
	list, err := k.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	out := make([]NodeInfo, 0, len(list.Items))
	for _, n := range list.Items {
		out = append(out, NodeInfo{
			Name:           n.Name,
			KubeletVersion: n.Status.NodeInfo.KubeletVersion,
			OsImage:        n.Status.NodeInfo.OSImage,
			Architecture:   n.Status.NodeInfo.Architecture,
			Labels:         n.Labels,
		})
	}
	return out, nil
}

// ListNamespaces returns every Namespace visible through the configured kubeconfig.
func (k *KubeClient) ListNamespaces(ctx context.Context) ([]NamespaceInfo, error) {
	list, err := k.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list namespaces: %w", err)
	}
	out := make([]NamespaceInfo, 0, len(list.Items))
	for _, n := range list.Items {
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
// pods cluster-wide.
func (k *KubeClient) ListPods(ctx context.Context) ([]PodInfo, error) {
	list, err := k.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	out := make([]PodInfo, 0, len(list.Items))
	for _, p := range list.Items {
		out = append(out, PodInfo{
			Name:      p.Name,
			Namespace: p.Namespace,
			Phase:     string(p.Status.Phase),
			NodeName:  p.Spec.NodeName,
			PodIP:     p.Status.PodIP,
			Labels:    p.Labels,
		})
	}
	return out, nil
}

// ListServices returns every Service visible through the configured kubeconfig,
// across all namespaces. Ports are serialised into generic maps so the store
// can persist them as JSONB without coupling to client-go types.
func (k *KubeClient) ListServices(ctx context.Context) ([]ServiceInfo, error) {
	list, err := k.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list services: %w", err)
	}
	out := make([]ServiceInfo, 0, len(list.Items))
	for _, s := range list.Items {
		ports := make([]map[string]interface{}, 0, len(s.Spec.Ports))
		for _, p := range s.Spec.Ports {
			entry := map[string]interface{}{
				"name":        p.Name,
				"port":        int(p.Port),
				"protocol":    string(p.Protocol),
				"target_port": p.TargetPort.String(),
			}
			if p.NodePort != 0 {
				entry["node_port"] = int(p.NodePort)
			}
			ports = append(ports, entry)
		}
		out = append(out, ServiceInfo{
			Name:      s.Name,
			Namespace: s.Namespace,
			Type:      string(s.Spec.Type),
			ClusterIP: s.Spec.ClusterIP,
			Selector:  s.Spec.Selector,
			Ports:     ports,
			Labels:    s.Labels,
		})
	}
	return out, nil
}

// ListWorkloads fans out three AppsV1 list calls (Deployments, StatefulSets,
// DaemonSets) and folds the results into a single slice tagged by kind. Any
// list failure aborts the whole call — partial ingestion risks wiping
// kinds that weren't listed when the collector reconciles.
func (k *KubeClient) ListWorkloads(ctx context.Context) ([]WorkloadInfo, error) {
	out := make([]WorkloadInfo, 0)

	deps, err := k.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range deps.Items {
		replicas := int(d.Status.Replicas)
		readyReplicas := int(d.Status.ReadyReplicas)
		out = append(out, WorkloadInfo{
			Name:          d.Name,
			Namespace:     d.Namespace,
			Kind:          api.Deployment,
			Replicas:      &replicas,
			ReadyReplicas: &readyReplicas,
			Labels:        d.Labels,
		})
	}

	sfs, err := k.clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list statefulsets: %w", err)
	}
	for _, s := range sfs.Items {
		replicas := int(s.Status.Replicas)
		readyReplicas := int(s.Status.ReadyReplicas)
		out = append(out, WorkloadInfo{
			Name:          s.Name,
			Namespace:     s.Namespace,
			Kind:          api.StatefulSet,
			Replicas:      &replicas,
			ReadyReplicas: &readyReplicas,
			Labels:        s.Labels,
		})
	}

	dss, err := k.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list daemonsets: %w", err)
	}
	for _, d := range dss.Items {
		// DaemonSet has no scalar desired count; expose ReadyReplicas only
		// via Status.NumberReady so the CMDB surfaces observable fleet size.
		readyReplicas := int(d.Status.NumberReady)
		out = append(out, WorkloadInfo{
			Name:          d.Name,
			Namespace:     d.Namespace,
			Kind:          api.DaemonSet,
			ReadyReplicas: &readyReplicas,
			Labels:        d.Labels,
		})
	}

	return out, nil
}
