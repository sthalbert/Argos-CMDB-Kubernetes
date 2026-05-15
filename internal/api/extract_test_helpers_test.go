package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
)

// newExtractTestServer wires the three extract routes onto a stub store
// and an injected viewer caller. Returns the server and the underlying stub.
func newExtractTestServer(t *testing.T) (*httptest.Server, *extractStubStore) {
	t.Helper()
	store := newExtractStubStore()
	const maxRows = 50000
	mux := http.NewServeMux()
	wrap := func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := &auth.Caller{
				Kind:   auth.CallerKindUser,
				Role:   "viewer",
				Scopes: []string{auth.ScopeRead},
			}
			ctx := auth.WithCaller(r.Context(), caller)
			r = r.WithContext(ctx)
			h(w, r)
		})
	}
	mux.Handle("GET /v1/search/extract", wrap(api.HandleSearchExtract(store, maxRows)))
	mux.Handle("GET /v1/search/extract.zip", wrap(api.HandleSearchExtractZip(store, maxRows)))
	mux.Handle("GET /v1/eol/extract", wrap(api.HandleEolExtract(store, maxRows)))
	return httptest.NewServer(mux), store
}

func getWithAuth(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b)
}

// extractStubStore implements api.ExtractStore in-memory.
type extractStubStore struct {
	clusters   []api.Cluster
	namespaces []api.Namespace
	nodes      []api.Node
	workloads  []api.Workload
	pods       []api.Pod
	vms        []api.VirtualMachine
	accounts   []api.CloudAccount
}

func newExtractStubStore() *extractStubStore {
	c1 := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	n1 := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	ns1 := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	wl1 := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	pod1 := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	vm1 := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	acc1 := uuid.MustParse("77777777-7777-7777-7777-777777777777")

	displayProd := "Production EU"
	region := "eu-west-2"
	imageID := "ami-12345"
	imageName := "longue-vue-base-2024"
	role := "bastion"

	clusterAnnotations := map[string]string{
		"longue-vue.io/eol.kubernetes": `{"product":"kubernetes","cycle":"1.28","eol_status":"eol","eol":"2024-10-28","latest":"1.28.15","latest_available":"1.32.3","checked_at":"2026-05-15T00:00:00Z"}`,
	}
	nodeAnnotations := map[string]string{
		"longue-vue.io/eol.ubuntu": `{"product":"ubuntu","cycle":"22.04","eol_status":"supported","latest":"22.04.4","checked_at":"2026-05-15T00:00:00Z"}`,
	}
	vmAnnotations := map[string]string{
		"longue-vue.io/eol.vault": `{"product":"vault","cycle":"1.13","eol_status":"eol","eol":"2024-12-09","checked_at":"2026-05-15T00:00:00Z"}`,
	}

	// ContainerList is []map[string]interface{}
	clContainers := api.ContainerList{
		{"name": "main", "image": "log4j:2.15"},
	}

	return &extractStubStore{
		clusters: []api.Cluster{
			{Id: &c1, Name: "prod-eu", DisplayName: &displayProd, Annotations: &clusterAnnotations},
		},
		namespaces: []api.Namespace{
			{Id: &ns1, ClusterId: c1, Name: "default"},
		},
		nodes: []api.Node{
			{Id: &n1, ClusterId: c1, Name: "worker-01", Annotations: &nodeAnnotations},
		},
		workloads: []api.Workload{
			{Id: &wl1, NamespaceId: ns1, Kind: api.Deployment, Name: "log4j-app",
				Containers: &clContainers,
			},
		},
		pods: []api.Pod{
			{Id: &pod1, NamespaceId: ns1, Name: "log4j-app-abc", WorkloadId: &wl1,
				Containers: &clContainers,
			},
		},
		vms: []api.VirtualMachine{
			{ID: vm1, CloudAccountID: acc1, Name: "bastion-prod", Region: &region,
				ImageID: &imageID, ImageName: &imageName, Role: &role, PowerState: "running",
				Annotations:  vmAnnotations,
				Applications: []api.VMApplication{{Product: "vault", Version: "1.13.4"}},
			},
		},
		accounts: []api.CloudAccount{{ID: acc1, Name: "acme-prod", Provider: "outscale"}},
	}
}

func (s *extractStubStore) ListClusters(_ context.Context, _ int, _ string, _ bool) ([]api.Cluster, string, error) {
	return s.clusters, "", nil
}
func (s *extractStubStore) ListNodes(_ context.Context, _ *uuid.UUID, _ int, _ string, _ bool) ([]api.Node, string, error) {
	return s.nodes, "", nil
}
func (s *extractStubStore) ListNamespaces(_ context.Context, _ *uuid.UUID, _ int, _ string, _ bool) ([]api.Namespace, string, error) {
	return s.namespaces, "", nil
}
func (s *extractStubStore) ListWorkloads(_ context.Context, f api.WorkloadListFilter, _ int, _ string) ([]api.Workload, string, error) {
	if f.ImageSubstring != nil {
		out := make([]api.Workload, 0)
		for _, w := range s.workloads {
			if w.Containers == nil {
				continue
			}
			for _, c := range *w.Containers {
				img, _ := c["image"].(string)
				if containsLower(img, *f.ImageSubstring) {
					out = append(out, w)
					break
				}
			}
		}
		return out, "", nil
	}
	return s.workloads, "", nil
}
func (s *extractStubStore) ListPods(_ context.Context, f api.PodListFilter, _ int, _ string) ([]api.Pod, string, error) {
	if f.ImageSubstring != nil {
		out := make([]api.Pod, 0)
		for _, p := range s.pods {
			if p.Containers == nil {
				continue
			}
			for _, c := range *p.Containers {
				img, _ := c["image"].(string)
				if containsLower(img, *f.ImageSubstring) {
					out = append(out, p)
					break
				}
			}
		}
		return out, "", nil
	}
	return s.pods, "", nil
}
func (s *extractStubStore) ListVirtualMachines(_ context.Context, f api.VirtualMachineListFilter, _ int, _ string) ([]api.VirtualMachine, string, error) {
	if f.Image != nil {
		out := make([]api.VirtualMachine, 0)
		for _, v := range s.vms {
			if v.ImageID != nil && containsLower(*v.ImageID, *f.Image) {
				out = append(out, v)
				continue
			}
			if v.ImageName != nil && containsLower(*v.ImageName, *f.Image) {
				out = append(out, v)
			}
		}
		return out, "", nil
	}
	if f.Application != nil {
		out := make([]api.VirtualMachine, 0)
		for _, v := range s.vms {
			for _, app := range v.Applications {
				if app.Product == *f.Application {
					out = append(out, v)
					break
				}
			}
		}
		return out, "", nil
	}
	return s.vms, "", nil
}
func (s *extractStubStore) ListCloudAccounts(_ context.Context, _ int, _ string) ([]api.CloudAccount, string, error) {
	return s.accounts, "", nil
}

func containsLower(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
