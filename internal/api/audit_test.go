package api

// Tests for the audit capture middleware and the list handler. PG-level
// integration lives in internal/store/pg_test.go — these cover the
// derive-resource / derive-action / scrub-secrets logic and the
// round-trip through the handler.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

func TestShouldAudit(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method, path string
		want         bool
	}{
		// writes are always audited
		{"POST", "/v1/clusters", true},
		{"PATCH", "/v1/clusters/x", true},
		{"DELETE", "/v1/nodes/x", true},
		// reads of /v1/admin/* are audited (who-looked-at-whom)
		{"GET", "/v1/admin/users", true},
		{"GET", "/v1/admin/audit", true},
		// reads of /v1/* are NOT audited — drowns the CMDB browsing use case
		{"GET", "/v1/clusters", false},
		{"GET", "/v1/pods/x", false},
		{"GET", "/healthz", false},
		{"GET", "/metrics", false},
	}
	for _, c := range cases {
		r, _ := http.NewRequestWithContext(context.Background(), c.method, c.path, http.NoBody)
		if got := shouldAudit(r); got != c.want {
			t.Errorf("shouldAudit(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestDeriveResource(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path     string
		wantType string
		wantID   string
	}{
		{"/v1/clusters", "cluster", ""},
		{"/v1/clusters/deadbeef", "cluster", "deadbeef"},
		{"/v1/admin/users", "admin_user", ""},
		{"/v1/admin/users/11111111-1111-1111-1111-111111111111", "admin_user", "11111111-1111-1111-1111-111111111111"},
		{"/v1/admin/audit", "admin_audit", ""},
		{"/v1/auth/login", "auth", "login"},
		{"/healthz", "", ""},
	}
	for _, c := range cases {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, c.path, http.NoBody)
		gotType, gotID := deriveResource(r)
		if gotType != c.wantType || gotID != c.wantID {
			t.Errorf("deriveResource(%s) = (%q, %q), want (%q, %q)",
				c.path, gotType, gotID, c.wantType, c.wantID)
		}
	}
}

func TestDeriveAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		method, path string
		status       int
		want         string
	}{
		{"POST", "/v1/auth/login", 204, "auth.login.success"},
		{"POST", "/v1/auth/login", 401, "auth.login.failure"},
		{"POST", "/v1/auth/logout", 204, "auth.logout"},
		{"POST", "/v1/auth/change-password", 204, "auth.change_password"},
		{"GET", "/v1/auth/oidc/authorize", 302, "auth.oidc.authorize"},
		{"GET", "/v1/auth/oidc/callback", 302, "auth.oidc.login"},
		{"GET", "/v1/auth/oidc/callback", 302, "auth.oidc.login"}, // redirect to /ui/ is success
		{"POST", "/v1/admin/users", 201, "admin_user.create"},
		{"PATCH", "/v1/admin/users/x", 200, "admin_user.update"},
		{"DELETE", "/v1/admin/tokens/x", 204, "admin_token.delete"},
		{"GET", "/v1/admin/audit", 200, "admin_audit.read"},
	}
	for _, c := range cases {
		r, _ := http.NewRequestWithContext(context.Background(), c.method, c.path, http.NoBody)
		if got := deriveAction(r, c.status); got != c.want {
			t.Errorf("deriveAction(%s %s %d) = %q, want %q", c.method, c.path, c.status, got, c.want)
		}
	}
}

const redactedValue = "[redacted]"

func TestScrubSecrets(t *testing.T) {
	t.Parallel()
	body := []byte(`{"username":"alice","password":"hunter2","new_password":"xyz","keep":"ok"}`)
	out := scrubSecrets(body)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("scrubSecrets returned %T, want map", out)
	}
	if m["password"] != redactedValue {
		t.Errorf("password not redacted: %v", m["password"])
	}
	if m["new_password"] != redactedValue {
		t.Errorf("new_password not redacted: %v", m["new_password"])
	}
	if m["username"] != "alice" {
		t.Errorf("username mangled: %v", m["username"])
	}
	if m["keep"] != "ok" {
		t.Errorf("keep mangled: %v", m["keep"])
	}
}

// End-to-end: wrap a no-op handler with the middleware and confirm a
// row lands in the fake store with the caller resolved from context.
func TestAuditMiddlewareRecordsWriteWithCaller(t *testing.T) { //nolint:gocyclo // end-to-end test asserting multiple audit-event fields
	t.Parallel()
	m := newMemStore()
	userID := uuid.New()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	mw := AuditMiddleware(m, "api", nil)(inner)

	body := []byte(`{"name":"prod","password":"hunter2"}`)
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/admin/users", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	ctx := auth.WithCaller(r.Context(), &auth.Caller{
		Kind:     auth.CallerKindUser,
		UserID:   userID,
		Username: "admin",
		Role:     auth.RoleAdmin,
	})
	r = r.WithContext(ctx)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, r)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d", rr.Code)
	}
	evs, _, err := m.ListAuditEvents(context.Background(), AuditEventFilter{}, 10, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Action != "admin_user.create" {
		t.Errorf("action = %q", ev.Action)
	}
	if ev.HttpStatus != http.StatusCreated {
		t.Errorf("status = %d", ev.HttpStatus)
	}
	if ev.ActorId == nil || *ev.ActorId != userID {
		t.Errorf("actor_id = %v, want %v", ev.ActorId, userID)
	}
	if ev.ActorUsername == nil || *ev.ActorUsername != "admin" {
		t.Errorf("actor_username = %v", ev.ActorUsername)
	}
	if ev.Details == nil {
		t.Fatalf("expected details with body, got nil")
	}
	b, err := json.Marshal(*ev.Details)
	if err != nil {
		t.Fatalf("marshal details: %v", err)
	}
	if bytes.Contains(b, []byte("hunter2")) {
		t.Errorf("details contains secret: %s", b)
	}
}

// GET reads of /v1/* (non-admin) should NOT produce an audit row.
func TestAuditMiddlewareSkipsNonAdminReads(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mw := AuditMiddleware(m, "api", nil)(inner)
	r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/clusters", http.NoBody)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	if len(m.authState.auditEvents) != 0 {
		t.Errorf("expected 0 audit events, got %d", len(m.authState.auditEvents))
	}
}

func TestListAuditEventsHandler(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	ctx := context.Background()
	for range 3 {
		if err := m.InsertAuditEvent(ctx, AuditEventInsert{
			ID:         uuid.New(),
			ActorKind:  "user",
			Action:     "cluster.create",
			HTTPMethod: "POST",
			HTTPPath:   "/v1/clusters",
			HTTPStatus: 201,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	h := newTestHandler(t, m)
	rr := do(h, http.MethodGet, "/v1/admin/audit", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got AuditEventList
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 3 {
		t.Errorf("expected 3 items, got %d", len(got.Items))
	}
}
