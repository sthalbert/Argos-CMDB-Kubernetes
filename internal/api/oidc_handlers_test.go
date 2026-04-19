package api

// Handler-level tests for OIDC endpoints. A full end-to-end callback
// would need an RSA-signing httptest IdP (discovery + JWKS + token)
// which is heavy and fragile; the pieces we own — config surface,
// authorize URL minting, state-bounce on miss, shadow-user derivation —
// are tested directly here. The exchange + verifier path is covered by
// internal/auth/oidc_test.go at the primitive level.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sthalbert/argos/internal/auth"
	"golang.org/x/oauth2"
)

// oidcStub builds an OIDCProvider with a stub oauth2 endpoint for
// URL-assembly tests. Never reaches the network because the authorize
// URL is only read from the redirect — not followed.
func oidcStub(label string) *auth.OIDCProvider {
	return auth.NewOIDCProviderFromTestParts(auth.OIDCConfig{
		ClientID:    "cid",
		RedirectURL: "https://argos.example.com/v1/auth/oidc/callback",
		Label:       label,
	}, &oauth2.Config{
		ClientID:    "cid",
		RedirectURL: "https://argos.example.com/v1/auth/oidc/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://idp.example.com/authorize",
			TokenURL: "https://idp.example.com/token",
		},
		Scopes: []string{"openid", "email", "profile"},
	})
}

// newTestHandlerWithOIDC mirrors newTestHandler but threads in an OIDC provider.
func newTestHandlerWithOIDC(t *testing.T, store Store, oidc *auth.OIDCProvider) http.Handler {
	t.Helper()
	return Handler(NewServer("test", store, auth.SecureNever, oidc))
}

func TestGetAuthConfig_Disabled(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	rr := do(h, http.MethodGet, "/v1/auth/config", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got AuthConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Oidc.Enabled {
		t.Errorf("expected oidc.enabled=false, got true")
	}
	if got.Oidc.Label != nil {
		t.Errorf("expected no label, got %q", *got.Oidc.Label)
	}
}

func TestGetAuthConfig_Enabled(t *testing.T) {
	t.Parallel()
	h := newTestHandlerWithOIDC(t, newMemStore(), oidcStub("Google"))
	rr := do(h, http.MethodGet, "/v1/auth/config", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	var got AuthConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Oidc.Enabled {
		t.Errorf("expected oidc.enabled=true")
	}
	if got.Oidc.Label == nil || *got.Oidc.Label != "Google" {
		t.Errorf("expected label=Google, got %v", got.Oidc.Label)
	}
}

func TestOidcAuthorize_DisabledReturns404(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	rr := do(h, http.MethodGet, "/v1/auth/oidc/authorize", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestOidcAuthorize_StoresStateAndRedirects(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	h := newTestHandlerWithOIDC(t, m, oidcStub("OIDC"))
	rr := do(h, http.MethodGet, "/v1/auth/oidc/authorize", "")
	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example.com/authorize?") {
		t.Fatalf("unexpected Location: %q", loc)
	}
	// authorize URL must carry state, code_challenge, code_challenge_method=S256, nonce.
	for _, needle := range []string{"state=", "code_challenge=", "code_challenge_method=S256", "nonce="} {
		if !strings.Contains(loc, needle) {
			t.Errorf("authorize URL missing %q: %s", needle, loc)
		}
	}
	// exactly one in-flight state row recorded.
	if n := len(m.authState.oidcAuthStates); n != 1 {
		t.Errorf("expected 1 stored auth state row, got %d", n)
	}
}

func TestOidcCallback_DisabledReturns404(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	rr := do(h, http.MethodGet, "/v1/auth/oidc/callback?code=x&state=y", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestOidcCallback_UnknownStateBouncesToLogin(t *testing.T) {
	t.Parallel()
	h := newTestHandlerWithOIDC(t, newMemStore(), oidcStub("OIDC"))
	rr := do(h, http.MethodGet, "/v1/auth/oidc/callback?code=x&state=never-seen", "")
	if rr.Code != http.StatusFound {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/ui/login?oidc_error=state_expired_or_unknown") {
		t.Errorf("unexpected Location: %q", loc)
	}
}

// --- findOrCreateOidcUser — shadow-user derivation -----------------------

func TestFindOrCreateOidcUser_CreatesShadowFromPreferredUsername(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	s := NewServer("test", m, auth.SecureNever, nil) // no provider needed for this helper
	u, err := s.findOrCreateOidcUser(context.Background(), &auth.OIDCClaims{
		Issuer:            "https://idp.example.com",
		Sub:               "abc123",
		Email:             "alice@example.com",
		PreferredUsername: "Alice",
	})
	if err != nil {
		t.Fatalf("findOrCreateOidcUser: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("expected lowercased preferred_username, got %q", u.Username)
	}
	if string(u.Role) != auth.RoleViewer {
		t.Errorf("expected first-login role=viewer, got %q", u.Role)
	}
}

func TestFindOrCreateOidcUser_FallsBackToEmailLocalPart(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	s := NewServer("test", m, auth.SecureNever, nil)
	u, err := s.findOrCreateOidcUser(context.Background(), &auth.OIDCClaims{
		Issuer: "https://idp.example.com",
		Sub:    "xyz",
		Email:  "bob.smith@corp.example.com",
	})
	if err != nil {
		t.Fatalf("findOrCreateOidcUser: %v", err)
	}
	if u.Username != "bob.smith" {
		t.Errorf("expected email local-part, got %q", u.Username)
	}
}

func TestFindOrCreateOidcUser_FallsBackToShortSub(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	s := NewServer("test", m, auth.SecureNever, nil)
	u, err := s.findOrCreateOidcUser(context.Background(), &auth.OIDCClaims{
		Issuer: "https://idp.example.com",
		Sub:    "01234567890abcdef",
	})
	if err != nil {
		t.Fatalf("findOrCreateOidcUser: %v", err)
	}
	if u.Username != "oidc-01234567" {
		t.Errorf("expected oidc-<shortSub>, got %q", u.Username)
	}
}

func TestFindOrCreateOidcUser_IsIdempotentOnRepeatLogin(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	s := NewServer("test", m, auth.SecureNever, nil)
	claims := &auth.OIDCClaims{
		Issuer:            "https://idp.example.com",
		Sub:               "abc123",
		PreferredUsername: "carol",
	}
	first, err := s.findOrCreateOidcUser(context.Background(), claims)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.findOrCreateOidcUser(context.Background(), claims)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if *first.Id != *second.Id {
		t.Errorf("expected same user id on repeat login, got %s vs %s", first.Id, second.Id)
	}
}

func TestFindOrCreateOidcUser_ResolvesUsernameCollision(t *testing.T) {
	t.Parallel()
	m := newMemStore()
	s := NewServer("test", m, auth.SecureNever, nil)
	// Plant a pre-existing local user with the same username.
	if _, err := m.CreateUser(context.Background(), UserInsert{
		Username:     "dave",
		PasswordHash: "$argon2id$placeholder",
		Role:         auth.RoleViewer,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	u, err := s.findOrCreateOidcUser(context.Background(), &auth.OIDCClaims{
		Issuer:            "https://idp.example.com",
		Sub:               "dave-sub",
		PreferredUsername: "Dave",
	})
	if err != nil {
		t.Fatalf("findOrCreateOidcUser: %v", err)
	}
	if u.Username == "dave" {
		t.Errorf("expected collision-suffixed username, got %q", u.Username)
	}
	if !strings.HasPrefix(u.Username, "dave-") {
		t.Errorf("expected dave-<suffix>, got %q", u.Username)
	}
}
