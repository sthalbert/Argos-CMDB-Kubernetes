package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/api/swagger"
	"github.com/sthalbert/longue-vue/internal/auth"
)

// swaggerTestRig holds the shared server and helpers for the auth-gating subtests.
type swaggerTestRig struct {
	srv        *httptest.Server
	noRedirect *http.Client
	env        *testEnv
}

func newSwaggerTestRig(t *testing.T) *swaggerTestRig {
	t.Helper()
	env := newTestEnv(t)

	swaggerMux := http.NewServeMux()

	swaggerUI := swagger.UIHandler()
	swaggerMux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	swaggerMux.Handle("GET /docs/", http.StripPrefix("/docs", swaggerUI))

	specAuth := auth.Middleware(env.store, auth.SecureNever, nil)
	requireRead := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:staticcheck // matches oapi-codegen context key convention
			ctx := context.WithValue(r.Context(), "BearerAuth.Scopes", []string{"read"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	swaggerMux.Handle("GET /openapi.yaml", requireRead(specAuth(swagger.OpenAPISpecHandler())))

	strict := api.NewStrictHandlerWithOptions(
		api.NewServer("test", env.store, auth.SecureNever, nil, api.NewLoginRateLimiter(), api.NewVerifyRateLimiter()),
		[]api.StrictMiddlewareFunc{api.InjectRequestMiddleware},
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter: swaggerMux,
		Middlewares: []api.MiddlewareFunc{
			api.AuditMiddleware(env.store, "api", nil),
			api.AuthMiddleware(env.store, auth.SecureNever, nil),
		},
	})

	srv := httptest.NewServer(swaggerMux)
	t.Cleanup(srv.Close)

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &swaggerTestRig{srv: srv, noRedirect: noRedirect, env: env}
}

// TestSwaggerDocs_authGating exercises the full auth chain on /docs/ and
// /openapi.yaml, asserting:
//
//	(a) /docs/ (shell) is reachable without credentials.
//	(b) /openapi.yaml returns 401 without credentials.
//	(c) /openapi.yaml returns 200 with a valid session cookie.
//	(d) /openapi.yaml returns 200 with a read-scope PAT Bearer token.
//	(e) GET /docs (no trailing slash) redirects 301 → /docs/
//	(f) POST /openapi.yaml returns 405 (Go 1.22+ method enforcement).
func TestSwaggerDocs_authGating(t *testing.T) {
	rig := newSwaggerTestRig(t)
	t.Run("docs shell is public", func(t *testing.T) { testSwaggerDocsShellIsPublic(t, rig) })
	t.Run("spec unauth returns 401", func(t *testing.T) { testSwaggerSpecUnauthReturns401(t, rig) })
	t.Run("spec with session cookie returns 200", func(t *testing.T) { testSwaggerSpecWithCookieReturns200(t, rig) })
	t.Run("spec with PAT returns 200", func(t *testing.T) { testSwaggerSpecWithPATReturns200(t, rig) })
	t.Run("docs without slash redirects 301", func(t *testing.T) { testSwaggerDocsRedirects(t, rig) })
	t.Run("POST openapi.yaml returns 405", func(t *testing.T) { testSwaggerSpecPostReturns405(t, rig) })
}

func testSwaggerDocsShellIsPublic(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rig.srv.URL+"/docs/", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /docs/: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/docs/ unauth: status = %d, want 200", resp.StatusCode)
	}
}

func testSwaggerSpecUnauthReturns401(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rig.srv.URL+"/openapi.yaml", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /openapi.yaml unauth: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/openapi.yaml unauth: status = %d, want 401", resp.StatusCode)
	}
}

func testSwaggerSpecWithCookieReturns200(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	loginBody := fmt.Sprintf(`{"username":%q,"password":%q}`, rig.env.adminUser, rig.env.adminPass)
	loginReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rig.srv.URL+"/v1/auth/login", strings.NewReader(loginBody))
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := rig.noRedirect.Do(loginReq)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusNoContent {
		t.Fatalf("login: status = %d, want 204", loginResp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie in login response")
	}
	cookieReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rig.srv.URL+"/openapi.yaml", http.NoBody)
	if err != nil {
		t.Fatalf("build cookie request: %v", err)
	}
	cookieReq.AddCookie(sessionCookie)
	resp, err := rig.noRedirect.Do(cookieReq)
	if err != nil {
		t.Fatalf("GET /openapi.yaml with cookie: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.yaml with cookie: status = %d, want 200", resp.StatusCode)
	}
}

func testSwaggerSpecWithPATReturns200(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	patReq, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rig.srv.URL+"/openapi.yaml", http.NoBody)
	if err != nil {
		t.Fatalf("build PAT request: %v", err)
	}
	patReq.Header.Set("Authorization", "Bearer "+rig.env.token)
	resp, err := http.DefaultClient.Do(patReq)
	if err != nil {
		t.Fatalf("GET /openapi.yaml with PAT: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.yaml with PAT: status = %d, want 200", resp.StatusCode)
	}
}

func testSwaggerDocsRedirects(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, rig.srv.URL+"/docs", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := rig.noRedirect.Do(req)
	if err != nil {
		t.Fatalf("GET /docs: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("/docs redirect: status = %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/docs/" {
		t.Errorf("/docs redirect: Location = %q, want /docs/", loc)
	}
}

func testSwaggerSpecPostReturns405(t *testing.T, rig *swaggerTestRig) {
	t.Helper()
	postReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, rig.srv.URL+"/openapi.yaml", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /openapi.yaml: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /openapi.yaml: status = %d, want 405", resp.StatusCode)
	}
}
