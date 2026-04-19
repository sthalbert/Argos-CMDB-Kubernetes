package auth

// OIDC authorization-code flow with PKCE + nonce + state, per ADR-0007.
// Configured via env vars in cmd/argosd/main.go; this package owns the
// provider wiring and the flow primitives. Storage of in-flight state
// (code_verifier + nonce tied to `state`) is the caller's job — pass
// them in and back out at the right points.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig is the env-derived configuration for an OIDC provider.
// Zero-value Issuer means "OIDC disabled"; handlers use that signal to
// 404 the authorize/callback endpoints and hide the "Sign in with X"
// button in the UI.
type OIDCConfig struct {
	Issuer       string   // e.g. https://accounts.example.com
	ClientID     string
	ClientSecret string
	RedirectURL  string   // e.g. https://argos.example.com/v1/auth/oidc/callback
	Scopes       []string // default: ["openid", "email", "profile"]
	Label        string   // button label on the login page; default "OIDC"
}

// OIDCProvider bundles a discovered *oidc.Provider + *oauth2.Config +
// ID-token verifier. Immutable after NewOIDCProvider returns.
type OIDCProvider struct {
	Config   OIDCConfig
	oauth    *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewOIDCProvider resolves the issuer's discovery document and builds
// the flow primitives. Returns a nil provider + nil error when OIDC is
// disabled (no Issuer configured) so callers can branch on that.
//
// A network failure here is fatal for argosd: OIDC configured but
// unreachable at start means "operator misconfigured", not "IdP has a
// transient outage" — the operator should see a clear error.
func NewOIDCProvider(ctx context.Context, cfg OIDCConfig) (*OIDCProvider, error) {
	if cfg.Issuer == "" {
		return nil, nil
	}
	if cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("OIDC issuer set but client id or redirect URL missing")
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile"}
	}
	if cfg.Label == "" {
		cfg.Label = "OIDC"
	}

	prov, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("resolve OIDC issuer %q: %w", cfg.Issuer, err)
	}

	oauthConf := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     prov.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       scopes,
	}
	verifier := prov.Verifier(&oidc.Config{ClientID: cfg.ClientID})

	return &OIDCProvider{
		Config:   cfg,
		oauth:    oauthConf,
		verifier: verifier,
	}, nil
}

// AuthorizeURL returns the IdP authorize URL carrying state, PKCE
// challenge, and nonce. Caller is expected to have already stashed the
// code_verifier + nonce keyed on state.
func (p *OIDCProvider) AuthorizeURL(state, codeChallenge, nonce string) string {
	return p.oauth.AuthCodeURL(state,
		oauth2.AccessTypeOnline,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("nonce", nonce),
	)
}

// OIDCClaims captures the identity fields we pull off the ID token.
// `Sub` is the stable external identifier; `Email` and
// `PreferredUsername` are preferred for username generation when
// present.
type OIDCClaims struct {
	Issuer            string `json:"iss"`
	Sub               string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     bool   `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Nonce             string `json:"nonce"`
}

// Exchange swaps the `code` + `codeVerifier` for tokens and validates
// the returned ID token. Asserts the `nonce` claim matches what we
// issued. Returns the ID-token claims on success.
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier, expectedNonce string) (*OIDCClaims, error) {
	token, err := p.oauth.Exchange(ctx, code,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return nil, fmt.Errorf("oauth2 exchange: %w", err)
	}

	rawID, ok := token.Extra("id_token").(string)
	if !ok || rawID == "" {
		return nil, errors.New("oidc: no id_token in response")
	}

	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("oidc id-token verify: %w", err)
	}

	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc parse claims: %w", err)
	}
	if claims.Sub == "" {
		return nil, errors.New("oidc: empty sub claim")
	}
	if claims.Nonce != expectedNonce {
		return nil, errors.New("oidc: nonce mismatch")
	}
	claims.Issuer = idToken.Issuer // trust the verified value, not the body
	return &claims, nil
}

// NewOIDCProviderFromTestParts builds a provider from pre-constructed
// oauth parts. Exposed only for tests in other packages (the verifier
// is left nil — Exchange will panic if called, so use this only for
// AuthorizeURL / config-surface tests).
func NewOIDCProviderFromTestParts(cfg OIDCConfig, oauthConf *oauth2.Config) *OIDCProvider {
	return &OIDCProvider{Config: cfg, oauth: oauthConf}
}

// GenerateOIDCState returns a short, URL-safe random identifier.
// Callers use it for both the OAuth2 `state` and (reused) the row key
// in oidc_auth_states — the row is short-lived and keyed on `state`
// so reusing saves a column.
func GenerateOIDCState() (string, error) {
	return RandomSecret(24)
}

// GeneratePKCE returns a fresh (code_verifier, code_challenge) pair
// per RFC 7636. The verifier is 32 bytes of base64-url; the challenge
// is SHA-256(verifier), base64-url.
func GeneratePKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("pkce verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// GenerateNonce returns a 16-byte URL-safe random string suitable for
// the `nonce` claim in the authorize URL and ID-token.
func GenerateNonce() (string, error) {
	return RandomSecret(16)
}
