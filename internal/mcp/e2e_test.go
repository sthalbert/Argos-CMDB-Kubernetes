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

// TestE2E_SecurityStackComposition is a single integrated test that verifies
// the security controls compose correctly across all key paths.
func TestE2E_SecurityStackComposition(t *testing.T) {
	t.Parallel()

	// Shared token values used across subtests.
	// validPrefix is the first 8 chars of validFull (after "Bearer " stripping by checkAccess).
	const (
		validFull   = "longue_vue_pat_abcd1234_validsecret"
		validPrefix = "longue_v" // validFull[:8]
	)

	tokenID := uuid.New()
	validCaller := &MCPCaller{
		TokenID: &tokenID,
		Name:    "e2e-test-token",
		Scopes:  []string{"read"},
	}

	// authFn simulates what main.go's closure does: check the cache first,
	// then fall back to the slow verify path.
	makeAuthFn := func(cache *AuthCache, allowValid bool) AuthFunc {
		return func(_ context.Context, tok string) (*MCPCaller, error) {
			// Check the cache first (mirrors main.go pattern).
			prefix := ""
			if len(tok) >= 8 {
				prefix = tok[:8]
			}
			if caller, ok := cache.Get(prefix, tok); ok {
				return caller, nil
			}
			// Simulate slow verification.
			if allowValid && tok == validFull {
				cache.Put(prefix, tok, validCaller)
				return validCaller, nil
			}
			return nil, errors.New("authentication failed")
		}
	}

	t.Run("missing_authorization", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		cache := NewAuthCache(8, time.Minute)
		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        makeAuthFn(cache, true),
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

		events := rec.captured()
		if len(events) != 1 {
			t.Fatalf("want 1 audit event, got %d", len(events))
		}
		if events[0].HTTPStatus != 401 {
			t.Errorf("HTTPStatus = %d; want 401", events[0].HTTPStatus)
		}
		// Masked error message must not leak internal detail.
		content := toolResultText(result)
		if strings.Contains(content, "db") || strings.Contains(content, "sql") {
			t.Errorf("error message leaks internals: %q", content)
		}
	})

	t.Run("invalid_token", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		cache := NewAuthCache(8, time.Minute)
		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        makeAuthFn(cache, false), // no valid tokens
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

		events := rec.captured()
		if len(events) != 1 {
			t.Fatalf("want 1 audit event, got %d", len(events))
		}
		if events[0].HTTPStatus != 401 {
			t.Errorf("HTTPStatus = %d; want 401", events[0].HTTPStatus)
		}
	})

	t.Run("valid_token_succeeds", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		cache := NewAuthCache(8, time.Minute)
		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        makeAuthFn(cache, true),
			Recorder:    rec,
			RateLimiter: NewRateLimiter(10, 10),
		})

		req := makeRequest("Bearer "+validFull, map[string]any{"name": ""})
		result, err := s.handleListClusters(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if result == nil || result.IsError {
			t.Fatalf("want success result; IsError=%v", result.IsError)
		}

		events := rec.captured()
		if len(events) != 1 {
			t.Fatalf("want 1 audit event, got %d", len(events))
		}
		if events[0].HTTPStatus != 200 {
			t.Errorf("HTTPStatus = %d; want 200", events[0].HTTPStatus)
		}
	})

	t.Run("valid_token_burst_then_rate_limit", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		cache := NewAuthCache(8, time.Minute)
		// Tight limiter: burst=2 → 3rd call hits limit.
		limiter := NewRateLimiter(2, 2)
		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        makeAuthFn(cache, true),
			Recorder:    rec,
			RateLimiter: limiter,
		})

		req := makeRequest("Bearer "+validFull, map[string]any{"name": ""})
		ctx := context.Background()

		r1, err := s.handleListClusters(ctx, req)
		if err != nil || r1 == nil || r1.IsError {
			t.Fatalf("1st call: err=%v isError=%v", err, r1.IsError)
		}
		r2, err := s.handleListClusters(ctx, req)
		if err != nil || r2 == nil || r2.IsError {
			t.Fatalf("2nd call: err=%v isError=%v", err, r2.IsError)
		}
		r3, err := s.handleListClusters(ctx, req)
		if err != nil {
			t.Fatalf("3rd call unexpected Go error: %v", err)
		}
		if r3 == nil || !r3.IsError {
			t.Fatal("3rd call must be rate-limited (IsError=true)")
		}

		events := rec.captured()
		if len(events) != 3 {
			t.Fatalf("want 3 audit events, got %d", len(events))
		}
		if events[0].HTTPStatus != 200 {
			t.Errorf("event[0].HTTPStatus = %d; want 200", events[0].HTTPStatus)
		}
		if events[1].HTTPStatus != 200 {
			t.Errorf("event[1].HTTPStatus = %d; want 200", events[1].HTTPStatus)
		}
		if events[2].HTTPStatus != 429 {
			t.Errorf("event[2].HTTPStatus = %d; want 429", events[2].HTTPStatus)
		}
	})

	t.Run("revoked_invalidates", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		cache := NewAuthCache(8, time.Minute)

		// Prime the cache with the valid token upfront.
		cache.Put(validPrefix, validFull, validCaller)

		// authFn: only cache hits succeed; slow path always denies.
		authFn := func(_ context.Context, tok string) (*MCPCaller, error) {
			prefix := ""
			if len(tok) >= 8 {
				prefix = tok[:8]
			}
			if caller, ok := cache.Get(prefix, tok); ok {
				return caller, nil
			}
			return nil, errors.New("token revoked")
		}

		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        authFn,
			Recorder:    rec,
			RateLimiter: NewRateLimiter(10, 10),
		})

		req := makeRequest("Bearer "+validFull, map[string]any{"name": ""})
		ctx := context.Background()

		// First call: cache hit → succeeds.
		r1, err := s.handleListClusters(ctx, req)
		if err != nil || r1 == nil || r1.IsError {
			t.Fatalf("1st call before revocation: err=%v isError=%v", err, r1.IsError)
		}

		// Invalidate the cached entry (simulates admin revoking token).
		cache.Invalidate(validPrefix)

		// Second call: cache miss → slow path → denied.
		r2, err := s.handleListClusters(ctx, req)
		if err != nil {
			t.Fatalf("2nd call unexpected Go error: %v", err)
		}
		if r2 == nil || !r2.IsError {
			t.Fatal("2nd call after revocation must be denied (IsError=true)")
		}

		events := rec.captured()
		if len(events) != 2 {
			t.Fatalf("want 2 audit events, got %d", len(events))
		}
		if events[0].HTTPStatus != 200 {
			t.Errorf("event[0].HTTPStatus = %d; want 200", events[0].HTTPStatus)
		}
		if events[1].HTTPStatus != 401 {
			t.Errorf("event[1].HTTPStatus = %d; want 401", events[1].HTTPStatus)
		}
	})

	t.Run("panic_records_500", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = true
		store.panicOnGetCluster = true
		s := NewServer(store, nil, Config{
			Transport: "stdio", // stdio avoids per-call auth check
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
			//nolint:errcheck
			s.handleGetCluster(context.Background(), req) //nolint:errcheck
		}()

		if !panicked {
			t.Fatal("expected panic to propagate from panicOnGetCluster")
		}

		events := rec.captured()
		if len(events) != 1 {
			t.Fatalf("want 1 audit event after panic, got %d", len(events))
		}
		if events[0].HTTPStatus != 500 {
			t.Errorf("HTTPStatus = %d; want 500", events[0].HTTPStatus)
		}
	})

	t.Run("disabled_setting_records_401", func(t *testing.T) {
		t.Parallel()
		rec := &fakeRecorder{}
		store := newFakeStore()
		store.settings.MCPEnabled = false // admin toggled off
		cache := NewAuthCache(8, time.Minute)
		s := NewServer(store, nil, Config{
			Transport:   "sse",
			Auth:        makeAuthFn(cache, true),
			Recorder:    rec,
			RateLimiter: NewRateLimiter(10, 10),
		})

		req := makeRequest("Bearer "+validFull, map[string]any{"name": ""})
		result, err := s.handleListClusters(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if result == nil || !result.IsError {
			t.Fatal("want tool error result when MCP disabled")
		}

		events := rec.captured()
		if len(events) != 1 {
			t.Fatalf("want 1 audit event, got %d", len(events))
		}
		if events[0].HTTPStatus != 401 {
			t.Errorf("HTTPStatus = %d; want 401", events[0].HTTPStatus)
		}
	})
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
