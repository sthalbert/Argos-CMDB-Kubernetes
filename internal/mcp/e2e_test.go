// Package mcp — end-to-end composition test (LOW-01).
//
// This file exercises the full security-middleware stack at the handler layer
// (no TCP listener needed). It wires a real AuthCache, real RateLimiter, real
// fakeRecorder, and a real fakeStore through a single Server and drives it via
// direct handleListClusters / handleGetCluster calls — the same surface used
// by the MCP SDK in production.
//
// Network-level TLS smoke test: see tls_test.go::TestSSE_StartsWithTLS.
package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// e2eFixture wires the shared values used across the security-stack subtests.
type e2eFixture struct {
	validFull   string
	validPrefix string
	validCaller *Caller
}

func newE2EFixture() *e2eFixture {
	tokenID := uuid.New()
	return &e2eFixture{
		validFull:   "longue_vue_pat_abcd1234_validsecret",
		validPrefix: "longue_v",
		validCaller: &Caller{
			TokenID: &tokenID,
			Name:    "e2e-test-token",
			Scopes:  []string{"read"},
		},
	}
}

// makeAuthFn simulates main.go's closure: cache hit first, then slow verify.
func (f *e2eFixture) makeAuthFn(cache *AuthCache, allowValid bool) AuthFunc {
	return func(_ context.Context, tok string) (*Caller, error) {
		prefix := ""
		if len(tok) >= 8 {
			prefix = tok[:8]
		}
		if caller, ok := cache.Get(prefix, tok); ok {
			return caller, nil
		}
		if allowValid && tok == f.validFull {
			cache.Put(prefix, tok, f.validCaller)
			return f.validCaller, nil
		}
		return nil, errors.New("authentication failed")
	}
}

// TestE2E_SecurityStackComposition verifies the security controls compose
// correctly across all key paths.
func TestE2E_SecurityStackComposition(t *testing.T) {
	t.Parallel()
	f := newE2EFixture()

	t.Run("missing_authorization", func(t *testing.T) { e2eMissingAuthorization(t, f) })
	t.Run("invalid_token", func(t *testing.T) { e2eInvalidToken(t, f) })
	t.Run("valid_token_succeeds", func(t *testing.T) { e2eValidTokenSucceeds(t, f) })
	t.Run("valid_token_burst_then_rate_limit", func(t *testing.T) { e2eBurstThenRateLimit(t, f) })
	t.Run("revoked_invalidates", func(t *testing.T) { e2eRevokedInvalidates(t, f) })
	t.Run("panic_records_500", func(t *testing.T) { e2ePanicRecords500(t) })
	t.Run("disabled_setting_records_401", func(t *testing.T) { e2eDisabledRecords401(t, f) })
}

func e2eMissingAuthorization(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	cache := NewAuthCache(8, time.Minute)
	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        f.makeAuthFn(cache, true),
		Recorder:    rec,
		RateLimiter: NewRateLimiter(10, 10),
	})

	req := makeRequest("", map[string]any{"name": ""})
	result, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("want tool error result for missing auth header")
	}

	assertSingleEventStatus(t, rec, 401)
	content := toolResultText(result)
	if strings.Contains(content, "db") || strings.Contains(content, "sql") {
		t.Errorf("error message leaks internals: %q", content)
	}
}

func e2eInvalidToken(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	cache := NewAuthCache(8, time.Minute)
	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        f.makeAuthFn(cache, false),
		Recorder:    rec,
		RateLimiter: NewRateLimiter(10, 10),
	})

	req := makeRequest("Bearer longue_vue_pat_badbadba_wrongsecret", map[string]any{"name": ""})
	result, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("want tool error result for invalid token")
	}
	assertSingleEventStatus(t, rec, 401)
}

func e2eValidTokenSucceeds(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	cache := NewAuthCache(8, time.Minute)
	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        f.makeAuthFn(cache, true),
		Recorder:    rec,
		RateLimiter: NewRateLimiter(10, 10),
	})

	req := makeRequest("Bearer "+f.validFull, map[string]any{"name": ""})
	result, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("want success result; result=%v", result)
	}
	assertSingleEventStatus(t, rec, 200)
}

func e2eBurstThenRateLimit(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	cache := NewAuthCache(8, time.Minute)
	limiter := NewRateLimiter(2, 2)
	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        f.makeAuthFn(cache, true),
		Recorder:    rec,
		RateLimiter: limiter,
	})

	req := makeRequest("Bearer "+f.validFull, map[string]any{"name": ""})
	ctx := context.Background()

	expectE2ESuccess(t, s, ctx, req, "1st")
	expectE2ESuccess(t, s, ctx, req, "2nd")
	expectE2EToolError(t, s, ctx, req, "3rd")

	assertEventStatuses(t, rec, []int{200, 200, 429})
}

func e2eRevokedInvalidates(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = true
	cache := NewAuthCache(8, time.Minute)
	cache.Put(f.validPrefix, f.validFull, f.validCaller)

	authFn := func(_ context.Context, tok string) (*Caller, error) {
		prefix := ""
		if len(tok) >= 8 {
			prefix = tok[:8]
		}
		if caller, ok := cache.Get(prefix, tok); ok {
			return caller, nil
		}
		return nil, errors.New("token revoked")
	}

	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        authFn,
		Recorder:    rec,
		RateLimiter: NewRateLimiter(10, 10),
	})

	req := makeRequest("Bearer "+f.validFull, map[string]any{"name": ""})
	ctx := context.Background()

	expectE2ESuccess(t, s, ctx, req, "before_revoke")
	cache.Invalidate(f.validPrefix)
	expectE2EToolError(t, s, ctx, req, "after_revoke")

	assertEventStatuses(t, rec, []int{200, 401})
}

func e2ePanicRecords500(t *testing.T) {
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
		t.Fatal("expected panic to propagate from panicOnGetCluster")
	}
	assertSingleEventStatus(t, rec, 500)
}

func e2eDisabledRecords401(t *testing.T, f *e2eFixture) {
	t.Parallel()
	rec := &fakeRecorder{}
	store := newFakeStore()
	store.settings.MCPEnabled = false
	cache := NewAuthCache(8, time.Minute)
	s := NewServer(store, nil, &Config{
		Transport:   "sse",
		Auth:        f.makeAuthFn(cache, true),
		Recorder:    rec,
		RateLimiter: NewRateLimiter(10, 10),
	})

	req := makeRequest("Bearer "+f.validFull, map[string]any{"name": ""})
	result, err := s.handleListClusters(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatal("want tool error result when MCP disabled")
	}
	assertSingleEventStatus(t, rec, 401)
}

// expectE2ESuccess invokes handleListClusters and asserts a non-error tool
// result.
//
//nolint:gocritic // hugeParam: CallToolRequest is heavy but matches the MCP SDK handler signature.
func expectE2ESuccess(t *testing.T, s *Server, ctx context.Context, req mcplib.CallToolRequest, label string) {
	t.Helper()
	r, err := s.handleListClusters(ctx, req)
	if err != nil || r == nil || r.IsError {
		t.Fatalf("%s call: err=%v result=%v", label, err, r)
	}
}

// expectE2EToolError invokes handleListClusters and asserts an MCP tool
// error result with no Go error.
//
//nolint:gocritic // hugeParam: CallToolRequest is heavy but matches the MCP SDK handler signature.
func expectE2EToolError(t *testing.T, s *Server, ctx context.Context, req mcplib.CallToolRequest, label string) {
	t.Helper()
	r, err := s.handleListClusters(ctx, req)
	if err != nil {
		t.Fatalf("%s call unexpected Go error: %v", label, err)
	}
	if r == nil || !r.IsError {
		t.Fatalf("%s call must produce a tool error result", label)
	}
}

// assertSingleEventStatus asserts the recorder captured exactly one event
// with the expected HTTPStatus.
func assertSingleEventStatus(t *testing.T, rec *fakeRecorder, want int) {
	t.Helper()
	events := rec.captured()
	if len(events) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(events))
	}
	if events[0].HTTPStatus != want {
		t.Errorf("HTTPStatus = %d; want %d", events[0].HTTPStatus, want)
	}
}

// assertEventStatuses asserts the recorder captured exactly len(want) events
// with the matching HTTPStatus per index.
func assertEventStatuses(t *testing.T, rec *fakeRecorder, want []int) {
	t.Helper()
	events := rec.captured()
	if len(events) != len(want) {
		t.Fatalf("want %d audit events, got %d", len(want), len(events))
	}
	for i, w := range want {
		if events[i].HTTPStatus != w {
			t.Errorf("event[%d].HTTPStatus = %d; want %d", i, events[i].HTTPStatus, w)
		}
	}
}

// toolResultText extracts the first text string from a CallToolResult's Content
// slice for error-message assertions.
func toolResultText(result *mcplib.CallToolResult) string {
	for _, c := range result.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}
