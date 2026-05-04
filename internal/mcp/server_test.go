package mcp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/impact"
)

// makeRequest constructs a CallToolRequest with the given arguments and
// optional Authorization header — matches what the MCP SDK delivers to a
// tool handler in production.
func makeRequest(authz string, args map[string]any) mcp.CallToolRequest {
	r := mcp.CallToolRequest{
		Header: http.Header{},
		Params: mcp.CallToolParams{Arguments: args},
	}
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	return r
}

func TestNewServer_RegistersAllTools(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	tr := impact.NewTraverser(store)
	s := NewServer(store, tr, Config{Transport: "stdio"})

	if s == nil || s.mcp == nil {
		t.Fatal("NewServer returned nil server or nil mcp")
	}
	// We can't enumerate tools through the public mcp-go API, so the
	// best we can do here is trigger registerTools (called from
	// NewServer) without panicking. The handler tests below confirm
	// each tool resolves to its handler.
}

func TestRun_UnknownTransport(t *testing.T) {
	t.Parallel()
	s := NewServer(newFakeStore(), nil, Config{Transport: "carrier-pigeon"})

	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
	if !errors.Is(err, errUnknownTransport) {
		t.Errorf("err = %v; want wrapping errUnknownTransport", err)
	}
}

func TestCheckAccess_DisabledByAdmin(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.settings.MCPEnabled = false
	s := NewServer(store, nil, Config{Transport: "stdio"})

	_, err := s.checkAccess(context.Background(), makeRequest("", nil))
	if !errors.Is(err, errDisabled) {
		t.Errorf("err = %v; want errDisabled", err)
	}
}

func TestCheckAccess_StdioNoAuth(t *testing.T) {
	// No Auth callback configured (stdio transport) → access granted.
	t.Parallel()
	s := NewServer(newFakeStore(), nil, Config{Transport: "stdio"})
	if _, err := s.checkAccess(context.Background(), makeRequest("", nil)); err != nil {
		t.Errorf("stdio (Auth=nil) should permit access; got %v", err)
	}
}

func TestCheckAccess_SSE(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		authz     string
		authFunc  AuthFunc
		wantErrIs error
	}{
		{
			name:      "missing header",
			authz:     "",
			authFunc:  func(context.Context, string) (*MCPCaller, error) { return nil, errors.New("never called") },
			wantErrIs: errUnauthorized,
		},
		{
			name:      "valid token",
			authz:     "Bearer good-token",
			authFunc:  func(_ context.Context, tok string) (*MCPCaller, error) { return &MCPCaller{Name: "test"}, nil },
			wantErrIs: nil,
		},
		{
			name:  "invalid token",
			authz: "Bearer bad-token",
			authFunc: func(_ context.Context, _ string) (*MCPCaller, error) {
				return nil, errors.New("denied")
			},
			wantErrIs: nil, // wrapper error not exported; just check non-nil
		},
		{
			name:  "bare token without Bearer prefix",
			authz: "abc-not-bearer",
			authFunc: func(_ context.Context, tok string) (*MCPCaller, error) {
				if tok != "abc-not-bearer" {
					return nil, errors.New("token mismatch: expected raw token")
				}
				return &MCPCaller{Name: "test"}, nil
			},
			wantErrIs: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewServer(newFakeStore(), nil, Config{Transport: "sse", Auth: tc.authFunc})
			_, err := s.checkAccess(context.Background(), makeRequest(tc.authz, nil))

			switch tc.name {
			case "missing header":
				if !errors.Is(err, tc.wantErrIs) {
					t.Errorf("err = %v; want errUnauthorized", err)
				}
			case "valid token", "bare token without Bearer prefix":
				if err != nil {
					t.Errorf("err = %v; want nil", err)
				}
			case "invalid token":
				if err == nil {
					t.Error("expected non-nil error from auth callback")
				}
			}
		})
	}
}

func TestCheckAccess_SettingsReadFails(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.errOn["GetSettings"] = errors.New("db unreachable")
	s := NewServer(store, nil, Config{Transport: "stdio"})

	_, err := s.checkAccess(context.Background(), makeRequest("", nil))
	if err == nil || !strings.Contains(err.Error(), "read settings") {
		t.Errorf("err = %v; want wrapping read settings", err)
	}
}

// pagingFakeStore exercises collectAll's truncation cap by returning
// pages of items beyond maxTotalItems forever.
type pagingFakeStore struct {
	*fakeStore
	pageSize int
}

func (p *pagingFakeStore) ListClusters(_ context.Context, _ int, _ string) ([]api.Cluster, string, error) {
	out := make([]api.Cluster, p.pageSize)
	for i := range out {
		id := uuid.New()
		out[i] = api.Cluster{Id: &id, Name: "c"}
	}
	return out, "next", nil
}

func TestCollectAll_TruncatesAtMaxTotalItems(t *testing.T) {
	t.Parallel()
	inner := newFakeStore()
	p := &pagingFakeStore{fakeStore: inner, pageSize: 250} // 4 pages of 250 = 1000

	got, err := collectAll(context.Background(), func(ctx context.Context, cursor string) ([]api.Cluster, string, error) {
		return p.ListClusters(ctx, maxPageSize, cursor)
	})
	if err != nil {
		t.Fatalf("collectAll: %v", err)
	}
	if len(got) != maxTotalItems {
		t.Errorf("len = %d; want exactly maxTotalItems (%d)", len(got), maxTotalItems)
	}
}

func TestCollectAll_StopsOnEmptyCursor(t *testing.T) {
	t.Parallel()
	calls := 0
	got, err := collectAll(context.Background(), func(_ context.Context, _ string) ([]api.Cluster, string, error) {
		calls++
		return []api.Cluster{{Name: "only"}}, "", nil
	})
	if err != nil {
		t.Fatalf("collectAll: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d; want 1", calls)
	}
	if len(got) != 1 {
		t.Errorf("len = %d; want 1", len(got))
	}
}

func TestCollectAll_PropagatesError(t *testing.T) {
	t.Parallel()
	want := errors.New("boom")
	_, err := collectAll(context.Background(), func(_ context.Context, _ string) ([]api.Cluster, string, error) {
		return nil, "", want
	})
	if !errors.Is(err, want) {
		t.Errorf("err = %v; want %v", err, want)
	}
}
