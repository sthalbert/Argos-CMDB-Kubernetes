//nolint:gocritic,golines // test fixture; rangeValCopy/golines are noise on a fake store.
package impact

import (
	"context"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// fakeStore is an in-memory TraverserStore fixture. Each slice holds the
// rows returned by the matching List* method; Get* methods scan the same
// slices by id.
//
// The lists are intentionally tiny so individual tests can wire the exact
// graph shape they need without dragging in a full memStore.
//
//nolint:gocritic // rangeValCopy across this fixture is intentional — clarity over micro-perf in tests.
type fakeStore struct {
	clusters []api.Cluster
	nodes    []api.Node
	nss      []api.Namespace
	pods     []api.Pod
	wls      []api.Workload
	svcs     []api.Service
	ings     []api.Ingress
	pvs      []api.PersistentVolume
	pvcs     []api.PersistentVolumeClaim

	// Forced errors per method, keyed for quick toggling in tests.
	errOn map[string]error

	// listCalls counts ListXxx invocations so pagination tests can
	// assert collectAll loops correctly.
	listCalls map[string]int
}

func newFakeStore() *fakeStore {
	return &fakeStore{errOn: map[string]error{}, listCalls: map[string]int{}}
}

// --- Get* ----

func (f *fakeStore) GetCluster(_ context.Context, id uuid.UUID) (api.Cluster, error) {
	if err := f.errOn["GetCluster"]; err != nil {
		return api.Cluster{}, err
	}
	for i := range f.clusters {
		if f.clusters[i].Id != nil && *f.clusters[i].Id == id {
			return f.clusters[i], nil
		}
	}
	return api.Cluster{}, api.ErrNotFound
}

func (f *fakeStore) GetNode(_ context.Context, id uuid.UUID) (api.Node, error) {
	if err := f.errOn["GetNode"]; err != nil {
		return api.Node{}, err
	}
	for i := range f.nodes {
		if f.nodes[i].Id != nil && *f.nodes[i].Id == id {
			return f.nodes[i], nil
		}
	}
	return api.Node{}, api.ErrNotFound
}

func (f *fakeStore) GetNamespace(_ context.Context, id uuid.UUID) (api.Namespace, error) {
	if err := f.errOn["GetNamespace"]; err != nil {
		return api.Namespace{}, err
	}
	for i := range f.nss {
		if f.nss[i].Id != nil && *f.nss[i].Id == id {
			return f.nss[i], nil
		}
	}
	return api.Namespace{}, api.ErrNotFound
}

func (f *fakeStore) GetPod(_ context.Context, id uuid.UUID) (api.Pod, error) {
	for i := range f.pods {
		if f.pods[i].Id != nil && *f.pods[i].Id == id {
			return f.pods[i], nil
		}
	}
	return api.Pod{}, api.ErrNotFound
}

func (f *fakeStore) GetWorkload(_ context.Context, id uuid.UUID) (api.Workload, error) {
	for i := range f.wls {
		if f.wls[i].Id != nil && *f.wls[i].Id == id {
			return f.wls[i], nil
		}
	}
	return api.Workload{}, api.ErrNotFound
}

func (f *fakeStore) GetService(_ context.Context, id uuid.UUID) (api.Service, error) {
	for i := range f.svcs {
		if f.svcs[i].Id != nil && *f.svcs[i].Id == id {
			return f.svcs[i], nil
		}
	}
	return api.Service{}, api.ErrNotFound
}

func (f *fakeStore) GetIngress(_ context.Context, id uuid.UUID) (api.Ingress, error) {
	for i := range f.ings {
		if f.ings[i].Id != nil && *f.ings[i].Id == id {
			return f.ings[i], nil
		}
	}
	return api.Ingress{}, api.ErrNotFound
}

func (f *fakeStore) GetPersistentVolume(_ context.Context, id uuid.UUID) (api.PersistentVolume, error) {
	for i := range f.pvs {
		if f.pvs[i].Id != nil && *f.pvs[i].Id == id {
			return f.pvs[i], nil
		}
	}
	return api.PersistentVolume{}, api.ErrNotFound
}

func (f *fakeStore) GetPersistentVolumeClaim(_ context.Context, id uuid.UUID) (api.PersistentVolumeClaim, error) {
	for i := range f.pvcs {
		if f.pvcs[i].Id != nil && *f.pvcs[i].Id == id {
			return f.pvcs[i], nil
		}
	}
	return api.PersistentVolumeClaim{}, api.ErrNotFound
}

// --- List* ----
//
// The fake ignores cursors; all list methods return everything matching
// the filter in a single page (cursor "" on return). The pagination test
// uses a dedicated pager (see pagingFakeStore in traverser_test.go).

func (f *fakeStore) ListNodes(_ context.Context, clusterID *uuid.UUID, _ int, _ string) ([]api.Node, string, error) {
	f.bump("ListNodes")
	if err := f.errOn["ListNodes"]; err != nil {
		return nil, "", err
	}
	out := make([]api.Node, 0, len(f.nodes))
	for _, n := range f.nodes {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	return out, "", nil
}

func (f *fakeStore) ListNamespaces(_ context.Context, clusterID *uuid.UUID, _ int, _ string) ([]api.Namespace, string, error) {
	f.bump("ListNamespaces")
	out := make([]api.Namespace, 0, len(f.nss))
	for _, n := range f.nss {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	return out, "", nil
}

func (f *fakeStore) ListPods(_ context.Context, filter api.PodListFilter, _ int, _ string) ([]api.Pod, string, error) {
	f.bump("ListPods")
	out := make([]api.Pod, 0, len(f.pods))
	for _, p := range f.pods {
		if filter.NamespaceID != nil && p.NamespaceId != *filter.NamespaceID {
			continue
		}
		if filter.NodeName != nil && (p.NodeName == nil || *p.NodeName != *filter.NodeName) {
			continue
		}
		out = append(out, p)
	}
	return out, "", nil
}

func (f *fakeStore) ListWorkloads(_ context.Context, filter api.WorkloadListFilter, _ int, _ string) ([]api.Workload, string, error) {
	f.bump("ListWorkloads")
	out := make([]api.Workload, 0, len(f.wls))
	for _, w := range f.wls {
		if filter.NamespaceID != nil && w.NamespaceId != *filter.NamespaceID {
			continue
		}
		out = append(out, w)
	}
	return out, "", nil
}

func (f *fakeStore) ListServices(_ context.Context, namespaceID *uuid.UUID, _ int, _ string) ([]api.Service, string, error) {
	f.bump("ListServices")
	out := make([]api.Service, 0, len(f.svcs))
	for _, s := range f.svcs {
		if namespaceID != nil && s.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, s)
	}
	return out, "", nil
}

func (f *fakeStore) ListIngresses(_ context.Context, namespaceID *uuid.UUID, _ int, _ string) ([]api.Ingress, string, error) {
	f.bump("ListIngresses")
	out := make([]api.Ingress, 0, len(f.ings))
	for _, ig := range f.ings {
		if namespaceID != nil && ig.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, ig)
	}
	return out, "", nil
}

func (f *fakeStore) ListPersistentVolumes(_ context.Context, clusterID *uuid.UUID, _ int, _ string) ([]api.PersistentVolume, string, error) {
	f.bump("ListPersistentVolumes")
	out := make([]api.PersistentVolume, 0, len(f.pvs))
	for _, pv := range f.pvs {
		if clusterID != nil && pv.ClusterId != *clusterID {
			continue
		}
		out = append(out, pv)
	}
	return out, "", nil
}

func (f *fakeStore) ListPersistentVolumeClaims(_ context.Context, namespaceID *uuid.UUID, _ int, _ string) ([]api.PersistentVolumeClaim, string, error) {
	f.bump("ListPersistentVolumeClaims")
	out := make([]api.PersistentVolumeClaim, 0, len(f.pvcs))
	for _, pvc := range f.pvcs {
		if namespaceID != nil && pvc.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, pvc)
	}
	return out, "", nil
}

// --- builders ----

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }
func ptrStrLit(s string) *string     { return &s }
func ptrBoolLit(b bool) *bool        { return &b }
func ptrIntLit(i int) *int           { return &i }

func (f *fakeStore) bump(name string) { f.listCalls[name]++ }
