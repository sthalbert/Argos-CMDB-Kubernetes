package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// testOAuthConfig builds a minimal oauth2.Config for URL-assembly tests
// (no network — Endpoint URLs just have to be syntactically valid).
func testOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:    "cid",
		RedirectURL: "https://argos.example.com/cb",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://idp.example.com/authorize",
			TokenURL: "https://idp.example.com/token",
		},
		Scopes: []string{"openid", "email", "profile"},
	}
}

func TestGeneratePKCE_ChallengeIsSha256OfVerifier(t *testing.T) {
	t.Parallel()
	// Run multiple times so any randomness edge-case is exercised.
	for range 20 {
		verifier, challenge, err := GeneratePKCE()
		if err != nil {
			t.Fatalf("GeneratePKCE: %v", err)
		}
		if verifier == "" || challenge == "" {
			t.Fatal("empty verifier or challenge")
		}
		// RFC 7636: verifier must be 43–128 chars of URL-safe charset.
		if len(verifier) < 43 || len(verifier) > 128 {
			t.Errorf("verifier length out of RFC 7636 range: %d", len(verifier))
		}
		if strings.ContainsAny(verifier, "+/=") {
			t.Errorf("verifier contains non-urlsafe chars: %q", verifier)
		}
		if strings.ContainsAny(challenge, "+/=") {
			t.Errorf("challenge contains non-urlsafe chars: %q", challenge)
		}
		sum := sha256.Sum256([]byte(verifier))
		want := base64.RawURLEncoding.EncodeToString(sum[:])
		if challenge != want {
			t.Errorf("challenge mismatch:\n got: %q\nwant: %q", challenge, want)
		}
	}
}

func TestGenerateOIDCState_Uniqueness(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 100)
	for i := range 100 {
		s, err := GenerateOIDCState()
		if err != nil {
			t.Fatalf("GenerateOIDCState: %v", err)
		}
		if len(s) < 16 {
			t.Errorf("state suspiciously short: %q", s)
		}
		if _, dup := seen[s]; dup {
			t.Errorf("duplicate state after %d draws: %q", i, s)
		}
		seen[s] = struct{}{}
	}
}

func TestGenerateNonce_Uniqueness(t *testing.T) {
	t.Parallel()
	seen := make(map[string]struct{}, 100)
	for i := range 100 {
		n, err := GenerateNonce()
		if err != nil {
			t.Fatalf("GenerateNonce: %v", err)
		}
		if _, dup := seen[n]; dup {
			t.Errorf("duplicate nonce after %d draws: %q", i, n)
		}
		seen[n] = struct{}{}
	}
}

func TestNewOIDCProvider_DisabledWhenIssuerEmpty(t *testing.T) {
	t.Parallel()
	p, err := NewOIDCProvider(context.Background(), &OIDCConfig{})
	if !errors.Is(err, ErrOIDCDisabled) {
		t.Fatalf("expected ErrOIDCDisabled when issuer empty, got %v", err)
	}
	if p != nil {
		t.Fatalf("expected nil provider when issuer empty, got %+v", p)
	}
}

func TestNewOIDCProvider_RejectsIncompleteConfig(t *testing.T) {
	t.Parallel()
	// Issuer set but client id missing → should error before any network.
	_, err := NewOIDCProvider(context.Background(), &OIDCConfig{
		Issuer:      "https://example.invalid",
		RedirectURL: "https://argos.example.com/cb",
	})
	if err == nil {
		t.Fatal("expected error for missing client id")
	}
	if !strings.Contains(err.Error(), "client id") {
		t.Errorf("error doesn't mention missing client id: %v", err)
	}
}

// AuthorizeURL builds the IdP URL. We can't easily instantiate a real
// *OIDCProvider without a live issuer, but we can synthesise one with a
// stub endpoint to exercise the URL-building path.
func TestAuthorizeURL_CarriesStatePKCENonce(t *testing.T) {
	t.Parallel()
	p := &OIDCProvider{
		Config: OIDCConfig{ClientID: "cid", RedirectURL: "https://argos.example.com/cb"},
		oauth:  testOAuthConfig(),
	}
	raw := p.AuthorizeURL("state-XYZ", "challenge-ABC", "nonce-123")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}
	q := u.Query()
	if got := q.Get("state"); got != "state-XYZ" {
		t.Errorf("state = %q", got)
	}
	if got := q.Get("code_challenge"); got != "challenge-ABC" {
		t.Errorf("code_challenge = %q", got)
	}
	if got := q.Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q", got)
	}
	if got := q.Get("nonce"); got != "nonce-123" {
		t.Errorf("nonce = %q", got)
	}
	if got := q.Get("client_id"); got != "cid" {
		t.Errorf("client_id = %q", got)
	}
}
