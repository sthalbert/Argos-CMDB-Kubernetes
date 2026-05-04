package mcp

import (
	"context"
	"sync"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// fakeRecorder captures InsertAuditEvent calls for test assertions.
type fakeRecorder struct {
	mu     sync.Mutex
	events []api.AuditEventInsert
	errOn  error // if non-nil, InsertAuditEvent returns this error
}

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
	return NewServer(store, nil, Config{
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
// row with HTTPStatus=400 (tool error).
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
	s := NewServer(newFakeStore(), nil, Config{
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
// the denial is recorded (status=400, since it is a tool-level error result).
func TestAudit_DisabledMCP_DenialRecorded(t *testing.T) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = false
	s := NewServer(store, nil, Config{
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
		t.Fatalf("want 1 audit event, got %d", len(events))
	}
	// Denial before auth → status 400 (IsError=true result path).
	if events[0].HTTPStatus != 400 {
		t.Errorf("HTTPStatus = %d; want 400", events[0].HTTPStatus)
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
