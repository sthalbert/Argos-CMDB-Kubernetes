package main

// Route-registration smoke test. buildHTTPServer mixes hand-written
// mux.Handle patterns with the oapi-codegen router on the same
// http.ServeMux; a pattern registered by both sides panics at
// registration time (Go 1.22+ ServeMux conflict detection) and nothing
// short of booting the daemon would catch it. This test boots the full
// route table so CI does.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sthalbert/longue-vue/internal/store"
)

func newTestPGForRoutes(t *testing.T) *store.PG {
	t.Helper()
	dsn := os.Getenv("PGX_TEST_DATABASE")
	if dsn == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping route-registration integration test")
	}
	ctx := context.Background()
	pg, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(pg.Close)
	return pg
}

func TestBuildHTTPServerRegistersAllRoutes(t *testing.T) {
	pg := newTestPGForRoutes(t)
	cfg := &runConfig{
		extractMaxRows:       100,
		verifyRateLimitRPS:   1,
		verifyRateLimitBurst: 1,
	}
	srv, err := buildHTTPServer(cfg, pg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildHTTPServer: %v", err)
	}
	// Exercise the mux dispatcher once so a lazily-detected routing
	// problem would also surface here, not just registration panics.
	// /metrics is registered unauthenticated in buildHTTPServer.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody) //nolint:noctx // in-process handler test
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics through the built mux: got %d, want 200", rec.Code)
	}
}
