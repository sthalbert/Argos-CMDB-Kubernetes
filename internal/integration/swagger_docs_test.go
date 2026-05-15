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

// TestSwaggerDocs_authGating exercises the full auth chain on /docs/ and
// /openapi.yaml, asserting:
//
//   (a) /docs/ (shell) is reachable without credentials.
//   (b) /openapi.yaml returns 401 without credentials.
//   (c) /openapi.yaml returns 200 with a valid session cookie.
//   (d) /openapi.yaml returns 200 with a read-scope PAT Bearer token.
//   (e) GET /docs (no trailing slash) redirects 301 → /docs/
//   (f) POST /openapi.yaml returns 405 (Go 1.22+ method enforcement).
func TestSwaggerDocs_authGating(t *testing.T) {
	// Reuse newTestEnv for DB setup, admin credentials, and the pre-minted
	// all-scope PAT (env.token). We build a second httptest.Server with the
	// same store so we can mount the swagger routes that main.go adds but
	// newTestEnv's mux does not.
	env := newTestEnv(t)

	// Build a mux that mirrors the swagger-relevant portion of main.go,
	// using the same store as the base env so login sessions are shared.
	swaggerMux := http.NewServeMux()

	// /docs/ shell — public, no auth.
	swaggerUI := swagger.SwaggerUIHandler()
	swaggerMux.Handle("GET /docs", http.RedirectHandler("/docs/", http.StatusMovedPermanently))
	swaggerMux.Handle("GET /docs/", http.StripPrefix("/docs", swaggerUI))

	// /openapi.yaml — requires read scope (mirrors requireReadScope in main.go).
	specAuth := auth.Middleware(env.store, auth.SecureNever, nil)
	requireRead := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			//nolint:staticcheck // matches oapi-codegen context key convention
			ctx := context.WithValue(r.Context(), "BearerAuth.Scopes", []string{"read"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	swaggerMux.Handle("GET /openapi.yaml", requireRead(specAuth(swagger.OpenAPISpecHandler())))

	// Also wire the /v1/auth/login endpoint so loginAndGetCookie works.
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
	defer srv.Close()

	// (a) The shell is public.
	resp, err := http.Get(srv.URL + "/docs/")
	if err != nil {
		t.Fatalf("GET /docs/: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/docs/ unauth: status = %d, want 200", resp.StatusCode)
	}

	// (b) The spec without auth returns 401.
	resp, err = http.Get(srv.URL + "/openapi.yaml")
	if err != nil {
		t.Fatalf("GET /openapi.yaml unauth: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/openapi.yaml unauth: status = %d, want 401", resp.StatusCode)
	}

	// (c) The spec with a valid session cookie returns 200.
	// Log in against the swagger server so the cookie is valid for its sessions.
	loginBody := fmt.Sprintf(`{"username":%q,"password":%q}`, env.adminUser, env.adminPass)
	loginReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/v1/auth/login", strings.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	loginResp, err := noRedirect.Do(loginReq)
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

	cookieReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/openapi.yaml", http.NoBody)
	cookieReq.AddCookie(sessionCookie)
	resp, err = noRedirect.Do(cookieReq)
	if err != nil {
		t.Fatalf("GET /openapi.yaml with cookie: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.yaml with cookie: status = %d, want 200", resp.StatusCode)
	}

	// (d) The spec with a Bearer PAT (read scope via env.token, which carries
	// all scopes including read) returns 200.
	patReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/openapi.yaml", http.NoBody)
	patReq.Header.Set("Authorization", "Bearer "+env.token)
	resp, err = http.DefaultClient.Do(patReq)
	if err != nil {
		t.Fatalf("GET /openapi.yaml with PAT: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/openapi.yaml with PAT: status = %d, want 200", resp.StatusCode)
	}

	// (e) GET /docs (no trailing slash) → 301 → Location: /docs/
	noRedirectClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedirectClient.Get(srv.URL + "/docs")
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

	// (f) POST /openapi.yaml → 405 (Go 1.22+ method-not-allowed enforcement).
	postReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/openapi.yaml", http.NoBody)
	resp, err = http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatalf("POST /openapi.yaml: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /openapi.yaml: status = %d, want 405", resp.StatusCode)
	}
}
