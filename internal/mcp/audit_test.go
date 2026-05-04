package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
)

// fakeRecorder captures InsertAuditEvent calls for test assertions.
type fakeRecorder struct {
	mu     sync.Mutex
	events []api.AuditEventInsert
	errOn  error // if non-nil, InsertAuditEvent returns this error
}

//nolint:gocritic // hugeParam: api.AuditRecorder interface requires by-value AuditEventInsert.
func (f *fakeRecorder) InsertAuditEvent(_ context.Context, in api.AuditEventInsert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOn != nil {
		return f.errOn
	}
	f.events = append(f.events, in)
	return nil
}

func (f *fakeRecorder) captured() []api.AuditEventInsert {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.AuditEventInsert, len(f.events))
	copy(out, f.events)
	return out
}

// makeServerWithRecorder builds a stdio-transport Server with a fakeRecorder
// and an enabled fake store.
func makeServerWithRecorder(rec *fakeRecorder) *Server {
	store := newFakeStore()
	store.settings.MCPEnabled = true
	return NewServer(store, nil, &Config{
		Transport: "stdio",
		Recorder:  rec,
	})
}

// TestAudit_ListClusters_Success verifies a successful list_clusters call
// emits exactly one audit row with Action="mcp.list_clusters", Source="mcp",
// and HTTPStatus=200.
func TestAudit_ListClusters_Success(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	s := makeServerWithRecorder(rec)

	req := makeRequest("", map[string]any{"name": ""})
	_, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListClusters: %v", err)
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(events))
	}
	ev := events[0]
	if ev.Action != "mcp.list_clusters" {
		t.Errorf("Action = %q; want mcp.list_clusters", ev.Action)
	}
	if ev.Source != "mcp" {
		t.Errorf("Source = %q; want mcp", ev.Source)
	}
	if ev.HTTPStatus != 200 {
		t.Errorf("HTTPStatus = %d; want 200", ev.HTTPStatus)
	}
	if ev.ResourceType != "mcp_tool" {
		t.Errorf("ResourceType = %q; want mcp_tool", ev.ResourceType)
	}
}

// TestAudit_GetCluster_BadUUID verifies a bad-UUID get_cluster call emits a
// row with HTTPStatus=400 (tool error / bad input).
func TestAudit_GetCluster_BadUUID(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	s := makeServerWithRecorder(rec)

	req := makeRequest("", map[string]any{"id": "not-a-uuid"})
	result, err := s.handleGetCluster(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetCluster returned error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected tool error result")
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(events))
	}
	if events[0].HTTPStatus != 400 {
		t.Errorf("HTTPStatus = %d; want 400", events[0].HTTPStatus)
	}
}

// TestAudit_NilRecorder_NoPanic verifies that a nil Recorder causes no panic
// and no error.
func TestAudit_NilRecorder_NoPanic(t *testing.T) {
	t.Parallel()
	s := NewServer(newFakeStore(), nil, &Config{
		Transport: "stdio",
		Recorder:  nil, // explicitly nil
	})

	req := makeRequest("", map[string]any{})
	_, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// If we got here without panic, the test passes.
}

// TestAudit_SensitiveArgsFiltered verifies that image substrings are replaced
// with "<set>" or "<unset>" and never logged verbatim.
func TestAudit_SensitiveArgsFiltered(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	s := makeServerWithRecorder(rec)

	req := makeRequest("", map[string]any{
		"namespace_id": "",
		"kind":         "",
		"image":        "secret-string",
	})
	_, err := s.handleListWorkloads(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListWorkloads: %v", err)
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	img, ok := events[0].Details["image"]
	if !ok {
		t.Fatal("image key missing from Details")
	}
	if img == "secret-string" {
		t.Error("raw image substring must not appear in audit Details")
	}
	if img != "<set>" {
		t.Errorf("image = %q; want <set>", img)
	}
}

// TestAudit_DisabledMCP_DenialRecorded verifies that when MCPEnabled=false
// the denial is recorded with HTTPStatus=401 (auth denial, not bad input).
func TestAudit_DisabledMCP_DenialRecorded(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = false
	s := NewServer(store, nil, &Config{
		Transport: "stdio",
		Recorder:  rec,
	})

	req := makeRequest("", map[string]any{})
	result, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListClusters: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("expected tool error result for disabled MCP")
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want exactly 1 audit event, got %d", len(events))
	}
	// Denial → status 401, not 400.
	if events[0].HTTPStatus != 401 {
		t.Errorf("HTTPStatus = %d; want 401", events[0].HTTPStatus)
	}
}

// TestAudit_RecorderError_NoClientError verifies that a recorder failure is
// swallowed and never propagated to the MCP client.
func TestAudit_RecorderError_NoClientError(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{errOn: errDisabled} // reuse any sentinel error
	s := makeServerWithRecorder(rec)

	req := makeRequest("", map[string]any{})
	result, err := s.handleListClusters(context.Background(), req)
	// Neither result.IsError nor err should reflect the recorder failure.
	if err != nil {
		t.Errorf("recorder error must not propagate as Go error: %v", err)
	}
	if result != nil && result.IsError {
		t.Error("recorder error must not surface as MCP tool error")
	}
}

// TestAudit_NodeName_NotLoggedVerbatim verifies that node_name is replaced
// with presence("<set>"|"<unset>") in the audit log (it may embed cloud
// instance IDs / internal hostnames, making it PII).
func TestAudit_NodeName_NotLoggedVerbatim(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	s := makeServerWithRecorder(rec)

	req := makeRequest("", map[string]any{
		"node_name": "ip-10-0-1-42.eu-west-1.compute.internal",
	})
	_, err := s.handleListPods(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListPods: %v", err)
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	nn, ok := events[0].Details["node_name"]
	if !ok {
		t.Fatal("node_name key missing from Details")
	}
	if nn == "ip-10-0-1-42.eu-west-1.compute.internal" {
		t.Error("raw node_name must not appear verbatim in audit Details")
	}
	if nn != "<set>" {
		t.Errorf("node_name = %q; want <set>", nn)
	}
}

// TestAudit_Panic_Records500 verifies that a panic inside a handler body
// causes a 500 audit row to be recorded before the panic is re-raised.
func TestAudit_Panic_Records500(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	store.panicOnGetCluster = true
	s := NewServer(store, nil, &Config{
		Transport: "stdio",
		Recorder:  rec,
	})

	req := makeRequest("", map[string]any{"id": "00000000-0000-0000-0000-000000000001"})

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		s.handleGetCluster(context.Background(), req) //nolint:errcheck // test asserts side effect (audit row on panic), not handler return
	}()

	if !panicked {
		t.Fatal("expected panic to propagate")
	}

	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 audit event after panic, got %d", len(events))
	}
	if events[0].HTTPStatus != 500 {
		t.Errorf("HTTPStatus = %d; want 500", events[0].HTTPStatus)
	}
}

// TestAudit_RateLimit_Records429 verifies that a rate-limited call emits a
// 429 audit row (distinct from 401 auth denial).
func TestAudit_RateLimit_Records429(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true

	// Tight limiter: 2 rps, burst 2.
	limiter := NewRateLimiter(2, 2)

	s := NewServer(store, nil, &Config{
		Transport:   "stdio",
		Recorder:    rec,
		RateLimiter: limiter,
	})

	req := makeRequest("", map[string]any{"name": ""})
	ctx := context.Background()

	callListClustersExpectSuccess(t, s, ctx, req, "1st")
	callListClustersExpectSuccess(t, s, ctx, req, "2nd")
	callListClustersExpectRateLimited(t, s, ctx, req, "3rd")

	events := rec.captured()
	if len(events) != 3 {
		t.Fatalf("want 3 audit events, got %d", len(events))
	}
	wantStatuses := []int{200, 200, 429}
	for i, want := range wantStatuses {
		if events[i].HTTPStatus != want {
			t.Errorf("event[%d].HTTPStatus = %d; want %d", i, events[i].HTTPStatus, want)
		}
	}
}

// callListClustersExpectSuccess invokes handleListClusters and asserts the
// call returned a non-error tool result.
//
//nolint:gocritic // hugeParam: CallToolRequest is heavy but matches the MCP SDK handler signature.
func callListClustersExpectSuccess(t *testing.T, s *Server, ctx context.Context, req mcp.CallToolRequest, label string) {
	t.Helper()
	result, err := s.handleListClusters(ctx, req)
	if err != nil {
		t.Fatalf("%s call: %v", label, err)
	}
	if result == nil || result.IsError {
		t.Fatalf("%s call should succeed", label)
	}
}

// callListClustersExpectRateLimited invokes handleListClusters and asserts
// the call returned an MCP tool error (rate-limited).
//
//nolint:gocritic // hugeParam: CallToolRequest is heavy but matches the MCP SDK handler signature.
func callListClustersExpectRateLimited(t *testing.T, s *Server, ctx context.Context, req mcp.CallToolRequest, label string) {
	t.Helper()
	result, err := s.handleListClusters(ctx, req)
	if err != nil {
		t.Fatalf("%s call: %v", label, err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("%s call should be rate-limited", label)
	}
}
