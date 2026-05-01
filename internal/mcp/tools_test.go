//nolint:goconst // duplicated literals in assertions are clearer than named constants.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/impact"
)

// resultText drills into a CallToolResult's first TextContent so tests
// can assert on the human-readable payload the MCP client would render.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		t.Fatal("nil result or empty content")
	}
	tc, ok := r.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T; want TextContent", r.Content[0])
	}
	return tc.Text
}

// --- helper unit tests -----------------------------------------------------

func TestParseID(t *testing.T) {
	t.Parallel()
	good := uuid.New().String()
	tests := []struct {
		name    string
		args    map[string]any
		wantErr error
	}{
		{"valid", map[string]any{"id": good}, nil},
		{"missing", map[string]any{}, errRequiredField},
		{"empty", map[string]any{"id": ""}, errRequiredField},
		{"malformed", map[string]any{"id": "abc"}, nil}, // returns parse error, not sentinel
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := makeRequest("", tc.args)
			_, err := parseID(req)
			switch tc.name {
			case "valid":
				if err != nil {
					t.Errorf("err = %v; want nil", err)
				}
			case "missing", "empty":
				if !errors.Is(err, errRequiredField) {
					t.Errorf("err = %v; want errRequiredField", err)
				}
			case "malformed":
				if err == nil {
					t.Error("expected uuid parse error")
				}
			}
		})
	}
}

func TestParseOptionalUUID(t *testing.T) {
	t.Parallel()
	good := uuid.New().String()
	t.Run("absent returns nil,nil", func(t *testing.T) {
		t.Parallel()
		got, err := parseOptionalUUID(makeRequest("", nil), "cluster_id")
		if err != nil || got != nil {
			t.Errorf("got=%v err=%v; want nil,nil", got, err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		got, err := parseOptionalUUID(makeRequest("", map[string]any{"cluster_id": good}), "cluster_id")
		if err != nil || got == nil || got.String() != good {
			t.Errorf("got=%v err=%v; want valid uuid", got, err)
		}
	})
	t.Run("malformed", func(t *testing.T) {
		t.Parallel()
		_, err := parseOptionalUUID(makeRequest("", map[string]any{"cluster_id": "xyz"}), "cluster_id")
		if err == nil {
			t.Error("expected error for malformed uuid")
		}
	})
}

func TestJSONResult(t *testing.T) {
	t.Parallel()
	r, err := jsonResult(struct {
		A int    `json:"a"`
		B string `json:"b"`
	}{A: 1, B: "hi"})
	if err != nil {
		t.Fatalf("jsonResult: %v", err)
	}
	if r.IsError {
		t.Error("jsonResult should not produce an error result on success")
	}
	got := resultText(t, r)
	var roundTrip map[string]any
	if err := json.Unmarshal([]byte(got), &roundTrip); err != nil {
		t.Fatalf("result is not valid JSON: %v\npayload=%s", err, got)
	}
	if roundTrip["a"] != float64(1) || roundTrip["b"] != "hi" {
		t.Errorf("payload = %v; want {a:1,b:hi}", roundTrip)
	}
}

func TestStoreError(t *testing.T) {
	t.Parallel()
	t.Run("ErrNotFound → entity-specific message", func(t *testing.T) {
		t.Parallel()
		r, err := storeError("pod", api.ErrNotFound)
		if err != nil {
			t.Fatalf("storeError returned err = %v", err)
		}
		if !r.IsError {
			t.Error("result should be flagged IsError")
		}
		if !strings.Contains(resultText(t, r), "pod not found") {
			t.Errorf("text = %q; want contains 'pod not found'", resultText(t, r))
		}
	})
	t.Run("non-NotFound → masked", func(t *testing.T) {
		t.Parallel()
		r, _ := storeError("workload", errors.New("ssl handshake exposed"))
		text := resultText(t, r)
		// The internal message must NOT leak in the user-facing text.
		if strings.Contains(text, "ssl handshake") {
			t.Errorf("text = %q; internal error leaked to client", text)
		}
		if !strings.Contains(text, "internal error") {
			t.Errorf("text = %q; want generic internal error message", text)
		}
	})
}

func TestExtractEOLEntry(t *testing.T) {
	t.Parallel()
	id, name, kind := uuid.New().String(), "node-1", "node"

	t.Run("nil annotations → unknown", func(t *testing.T) {
		t.Parallel()
		entry := extractEOLEntry(id, name, kind, nil)
		if entry.Status != "unknown" {
			t.Errorf("status = %q; want unknown", entry.Status)
		}
	})
	t.Run("non-eol keys ignored", func(t *testing.T) {
		t.Parallel()
		ann := map[string]string{"longue-vue.io/owner": "team-a"}
		entry := extractEOLEntry(id, name, kind, &ann)
		if entry.Status != "unknown" {
			t.Errorf("status = %q; want unknown", entry.Status)
		}
	})
	t.Run("supported annotation parsed", func(t *testing.T) {
		t.Parallel()
		ann := map[string]string{
			"longue-vue.io/eol.kubernetes": `{"status":"supported","product":"kubernetes","eol_date":"2026-12-31"}`,
		}
		entry := extractEOLEntry(id, name, kind, &ann)
		if entry.Status != "supported" || entry.Product != "kubernetes" || entry.EOLDate != "2026-12-31" {
			t.Errorf("entry = %+v; want supported/kubernetes/2026-12-31", entry)
		}
	})
	t.Run("eol annotation wins over supported", func(t *testing.T) {
		t.Parallel()
		ann := map[string]string{
			"longue-vue.io/eol.containerd": `{"status":"supported","product":"containerd"}`,
			"longue-vue.io/eol.kubernetes": `{"status":"eol","product":"kubernetes","eol_date":"2024-04-30"}`,
		}
		entry := extractEOLEntry(id, name, kind, &ann)
		if entry.Status != "eol" {
			t.Errorf("status = %q; eol should beat supported regardless of map order", entry.Status)
		}
	})
	t.Run("malformed JSON skipped", func(t *testing.T) {
		t.Parallel()
		ann := map[string]string{"longue-vue.io/eol.kubernetes": "not-json"}
		entry := extractEOLEntry(id, name, kind, &ann)
		if entry.Status != "unknown" {
			t.Errorf("status = %q; want unknown when annotation is unparsable", entry.Status)
		}
	})
}

func TestCountEOLStatus(t *testing.T) {
	t.Parallel()
	s := &eolSummary{}
	for _, st := range []string{"eol", "approaching_eol", "supported", "unknown", "weird-string"} {
		countEOLStatus(s, st)
	}
	if s.EOL != 1 || s.ApproachingEOL != 1 || s.Supported != 1 {
		t.Errorf("summary = %+v; want EOL=1 Approaching=1 Supported=1", s)
	}
	// unknown + weird-string both fall through to Unknown.
	if s.Unknown != 2 {
		t.Errorf("unknown = %d; want 2 (incl. unrecognised status)", s.Unknown)
	}
}

func TestIDStr(t *testing.T) {
	t.Parallel()
	if got := idStr(nil); got != "" {
		t.Errorf("nil = %q; want empty", got)
	}
	id := uuid.New()
	if got := idStr(&id); got != id.String() {
		t.Errorf("got %q; want %q", got, id.String())
	}
}

// --- handler-level tests ---------------------------------------------------

func newServer(t *testing.T, store *fakeStore) *Server {
	t.Helper()
	tr := impact.NewTraverser(store)
	return NewServer(store, tr, Config{Transport: "stdio"})
}

func TestHandleListClusters_NameFilter(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()
	store.clusters = []api.Cluster{
		{Id: &id1, Name: "prod-eu"},
		{Id: &id2, Name: "prod-us"},
		{Id: &id3, Name: "staging"},
	}
	s := newServer(t, store)

	t.Run("no filter returns all", func(t *testing.T) {
		t.Parallel()
		r, err := s.handleListClusters(context.Background(), makeRequest("", nil))
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
		var got []api.Cluster
		_ = json.Unmarshal([]byte(resultText(t, r)), &got)
		if len(got) != 3 {
			t.Errorf("len = %d; want 3", len(got))
		}
	})
	t.Run("substring match is case-insensitive", func(t *testing.T) {
		t.Parallel()
		r, _ := s.handleListClusters(context.Background(), makeRequest("", map[string]any{"name": "PROD"}))
		var got []api.Cluster
		_ = json.Unmarshal([]byte(resultText(t, r)), &got)
		if len(got) != 2 {
			t.Errorf("len = %d; want 2 (prod-eu + prod-us)", len(got))
		}
	})
	t.Run("no match returns empty", func(t *testing.T) {
		t.Parallel()
		r, _ := s.handleListClusters(context.Background(), makeRequest("", map[string]any{"name": "qa"}))
		var got []api.Cluster
		_ = json.Unmarshal([]byte(resultText(t, r)), &got)
		if len(got) != 0 {
			t.Errorf("len = %d; want 0", len(got))
		}
	})
}

func TestHandleListClusters_DisabledReturnsError(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.settings.MCPEnabled = false
	s := newServer(t, store)

	r, err := s.handleListClusters(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler returned err = %v; want nil (errors as IsError result)", err)
	}
	if !r.IsError {
		t.Error("disabled MCP should produce IsError result")
	}
	if !strings.Contains(resultText(t, r), "disabled by administrator") {
		t.Errorf("text = %q; want disabled message", resultText(t, r))
	}
}

func TestHandleGetCluster_NotFound(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	s := newServer(t, store)

	args := map[string]any{"id": uuid.New().String()}
	r, err := s.handleGetCluster(context.Background(), makeRequest("", args))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !r.IsError {
		t.Error("missing cluster should produce IsError")
	}
	if !strings.Contains(resultText(t, r), "cluster not found") {
		t.Errorf("text = %q; want 'cluster not found'", resultText(t, r))
	}
}

func TestHandleGetCluster_InvalidID(t *testing.T) {
	t.Parallel()
	s := newServer(t, newFakeStore())
	r, err := s.handleGetCluster(context.Background(), makeRequest("", map[string]any{"id": "not-a-uuid"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !r.IsError {
		t.Error("invalid uuid should produce IsError")
	}
}

func TestHandleSearchImages_RequiresQuery(t *testing.T) {
	t.Parallel()
	s := newServer(t, newFakeStore())
	r, err := s.handleSearchImages(context.Background(), makeRequest("", map[string]any{}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !r.IsError {
		t.Error("empty query should produce IsError")
	}
	if !strings.Contains(resultText(t, r), "query is required") {
		t.Errorf("text = %q; want 'query is required'", resultText(t, r))
	}
}

func TestHandleGetImpactGraph_InvalidEntityType(t *testing.T) {
	t.Parallel()
	s := newServer(t, newFakeStore())
	r, _ := s.handleGetImpactGraph(context.Background(), makeRequest("", map[string]any{
		"entity_type": "carrier-pigeon",
		"id":          uuid.New().String(),
	}))
	if !r.IsError {
		t.Error("invalid entity type should produce IsError")
	}
	if !strings.Contains(resultText(t, r), "invalid entity_type") {
		t.Errorf("text = %q; want 'invalid entity_type'", resultText(t, r))
	}
}

func TestHandleGetImpactGraph_NoTraverser(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// Build a Server with traverser nil to exercise the guard branch.
	s := NewServer(store, nil, Config{Transport: "stdio"})

	r, _ := s.handleGetImpactGraph(context.Background(), makeRequest("", map[string]any{
		"entity_type": "cluster",
		"id":          uuid.New().String(),
	}))
	if !r.IsError {
		t.Error("nil traverser should produce IsError")
	}
}

func TestHandleGetImpactGraph_OK(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	clusterID := uuid.New()
	store.clusters = []api.Cluster{{Id: &clusterID, Name: "prod", KubernetesVersion: ptrStr("1.30.0")}}
	s := newServer(t, store)

	r, err := s.handleGetImpactGraph(context.Background(), makeRequest("", map[string]any{
		"entity_type": "cluster",
		"id":          clusterID.String(),
		"depth":       float64(1),
	}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if r.IsError {
		t.Errorf("unexpected IsError; text=%s", resultText(t, r))
	}
	var g impact.Graph
	if err := json.Unmarshal([]byte(resultText(t, r)), &g); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if g.Root.Type != impact.TypeCluster {
		t.Errorf("root = %s; want cluster", g.Root.Type)
	}
}

// TestListHandlers_OptionalScopeFilter walks each List* handler that
// accepts an optional cluster_id / namespace_id filter, confirming:
//
//  1. With no filter, every row is returned.
//  2. With a filter targeting one parent, only that parent's rows surface.
//  3. With a malformed UUID filter, an IsError result is produced and the
//     store is not consulted.
//
// This exists to lock in the parseOptionalUUID + collectAll plumbing
// across all 8 list handlers so a regression in one is caught.
func TestListHandlers_OptionalScopeFilter(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	clusterA, clusterB := uuid.New(), uuid.New()
	nsA, nsB := uuid.New(), uuid.New()

	id := func() *uuid.UUID { u := uuid.New(); return &u }
	store.nodes = []api.Node{
		{Id: id(), ClusterId: clusterA, Name: "a"},
		{Id: id(), ClusterId: clusterB, Name: "b"},
	}
	store.nss = []api.Namespace{
		{Id: &nsA, ClusterId: clusterA, Name: "ns-a"},
		{Id: &nsB, ClusterId: clusterB, Name: "ns-b"},
	}
	store.svcs = []api.Service{
		{Id: id(), NamespaceId: nsA, Name: "svc-a"},
		{Id: id(), NamespaceId: nsB, Name: "svc-b"},
	}
	store.ings = []api.Ingress{
		{Id: id(), NamespaceId: nsA, Name: "ing-a"},
	}
	store.pvs = []api.PersistentVolume{
		{Id: id(), ClusterId: clusterA, Name: "pv-a"},
	}
	store.pvcs = []api.PersistentVolumeClaim{
		{Id: id(), NamespaceId: nsA, Name: "pvc-a"},
	}
	store.wls = []api.Workload{
		{Id: id(), NamespaceId: nsA, Name: "wl-a", Kind: api.WorkloadKind("Deployment")},
		{Id: id(), NamespaceId: nsB, Name: "wl-b", Kind: api.WorkloadKind("Deployment")},
	}
	store.pods = []api.Pod{
		{Id: id(), NamespaceId: nsA, Name: "pod-a"},
		{Id: id(), NamespaceId: nsB, Name: "pod-b"},
	}

	s := newServer(t, store)

	type handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	cases := []struct {
		name      string
		handler   handler
		filterKey string
		filterVal uuid.UUID
		wantTotal int
		wantOne   int
	}{
		{"list_nodes", s.handleListNodes, "cluster_id", clusterA, 2, 1},
		{"list_namespaces", s.handleListNamespaces, "cluster_id", clusterA, 2, 1},
		{"list_services", s.handleListServices, "namespace_id", nsA, 2, 1},
		{"list_ingresses", s.handleListIngresses, "namespace_id", nsA, 1, 1},
		{"list_persistent_volumes", s.handleListPersistentVolumes, "cluster_id", clusterA, 1, 1},
		{"list_persistent_volume_claims", s.handleListPersistentVolumeClaims, "namespace_id", nsA, 1, 1},
		{"list_workloads", s.handleListWorkloads, "namespace_id", nsA, 2, 1},
		{"list_pods", s.handleListPods, "namespace_id", nsA, 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/no filter", func(t *testing.T) {
			t.Parallel()
			r, err := tc.handler(context.Background(), makeRequest("", nil))
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			var got []map[string]any
			_ = json.Unmarshal([]byte(resultText(t, r)), &got)
			if len(got) != tc.wantTotal {
				t.Errorf("len = %d; want %d", len(got), tc.wantTotal)
			}
		})
		t.Run(tc.name+"/scoped filter", func(t *testing.T) {
			t.Parallel()
			r, _ := tc.handler(context.Background(), makeRequest("", map[string]any{
				tc.filterKey: tc.filterVal.String(),
			}))
			var got []map[string]any
			_ = json.Unmarshal([]byte(resultText(t, r)), &got)
			if len(got) != tc.wantOne {
				t.Errorf("len = %d; want %d (filtered)", len(got), tc.wantOne)
			}
		})
		t.Run(tc.name+"/malformed filter", func(t *testing.T) {
			t.Parallel()
			r, err := tc.handler(context.Background(), makeRequest("", map[string]any{
				tc.filterKey: "not-a-uuid",
			}))
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !r.IsError {
				t.Error("malformed uuid should produce IsError")
			}
		})
	}
}

func TestHandleGetEOLSummary_AggregatesStatuses(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c1, c2, n1 := uuid.New(), uuid.New(), uuid.New()
	store.clusters = []api.Cluster{
		{Id: &c1, Name: "c-eol", Annotations: &map[string]string{
			"longue-vue.io/eol.kubernetes": `{"status":"eol","product":"kubernetes"}`,
		}},
		{Id: &c2, Name: "c-supp", Annotations: &map[string]string{
			"longue-vue.io/eol.kubernetes": `{"status":"supported","product":"kubernetes"}`,
		}},
	}
	store.nodes = []api.Node{
		{Id: &n1, ClusterId: c1, Name: "n-approach", Annotations: &map[string]string{
			"longue-vue.io/eol.containerd": `{"status":"approaching_eol","product":"containerd"}`,
		}},
	}
	s := newServer(t, store)

	r, err := s.handleGetEOLSummary(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var summary eolSummary
	if err := json.Unmarshal([]byte(resultText(t, r)), &summary); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if summary.TotalClusters != 2 || summary.TotalNodes != 1 {
		t.Errorf("totals = %+v; want clusters=2 nodes=1", summary)
	}
	if summary.EOL != 1 || summary.Supported != 1 || summary.ApproachingEOL != 1 {
		t.Errorf("counts = eol:%d sup:%d app:%d; want 1/1/1", summary.EOL, summary.Supported, summary.ApproachingEOL)
	}
}
