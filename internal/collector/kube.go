package collector

import (
	"context"
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubeVersionFetcher fetches the Kubernetes API server version using
// client-go. The target cluster is whatever the loaded kubeconfig points
// at; only one cluster per KubeVersionFetcher instance is supported in v1.
type KubeVersionFetcher struct {
	discovery discovery.DiscoveryInterface
}

// NewKubeVersionFetcher constructs a fetcher. Resolution order:
//   - explicit kubeconfigPath when non-empty;
//   - in-cluster config when running inside a pod;
//   - the default kubectl loading rules (KUBECONFIG env var, then ~/.kube/config).
func NewKubeVersionFetcher(kubeconfigPath string) (*KubeVersionFetcher, error) {
	cfg, err := loadKubeConfig(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes.NewForConfig: %w", err)
	}
	return &KubeVersionFetcher{discovery: cs.Discovery()}, nil
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
func (k *KubeVersionFetcher) ServerVersion(_ context.Context) (string, error) {
	info, err := k.discovery.ServerVersion()
	if err != nil {
		return "", err
	}
	return info.GitVersion, nil
}
