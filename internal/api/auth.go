package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Scope names exactly match the strings declared per-operation in
// api/openapi/openapi.yaml and surfaced by oapi-codegen under BearerAuthScopes.
const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeDelete = "delete"
	ScopeAdmin  = "admin"
)

// ScopedToken is a bearer token with its granted scopes. Name is a
// human-friendly label used only in logs and error messages — never the token
// value itself.
type ScopedToken struct {
	Name   string   `json:"name"`
	Token  string   `json:"token"`
	Scopes []string `json:"scopes"`
}

// hasScope reports whether the token grants want. The admin scope implies
// every other scope.
func (t ScopedToken) hasScope(want string) bool {
	for _, s := range t.Scopes {
		if s == ScopeAdmin || s == want {
			return true
		}
	}
	return false
}

// TokenStore is an immutable lookup table from token value to scopes. Build
// once at startup via NewTokenStore and share across goroutines — it's
// read-only and therefore safe for concurrent use.
type TokenStore struct {
	tokens []ScopedToken
}

// NewTokenStore validates tokens and returns a ready-to-use store. It rejects
// empty token values, duplicate token values across entries, and unknown
// scope names.
func NewTokenStore(tokens []ScopedToken) (*TokenStore, error) {
	seen := make(map[string]struct{}, len(tokens))
	for i, t := range tokens {
		if t.Token == "" {
			return nil, fmt.Errorf("token[%d] %q has empty value", i, t.Name)
		}
		if _, dup := seen[t.Token]; dup {
			return nil, fmt.Errorf("token[%d] %q duplicates an earlier token value", i, t.Name)
		}
		seen[t.Token] = struct{}{}
		for _, s := range t.Scopes {
			switch s {
			case ScopeRead, ScopeWrite, ScopeDelete, ScopeAdmin:
			default:
				return nil, fmt.Errorf("token[%d] %q has unknown scope %q", i, t.Name, s)
			}
		}
	}
	return &TokenStore{tokens: tokens}, nil
}

// Len returns the number of tokens in the store.
func (s *TokenStore) Len() int { return len(s.tokens) }

// lookup finds the ScopedToken matching the presented bearer using a
// constant-time compare across every stored token, so elapsed time can't leak
// which entry matched (or that any matched at all).
func (s *TokenStore) lookup(presented string) (ScopedToken, bool) {
	pb := []byte(presented)
	var matched ScopedToken
	found := 0
	for _, t := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(t.Token), pb) == 1 {
			matched = t
			found = 1
		}
	}
	return matched, found == 1
}

// ParseTokensJSON decodes a JSON array of ScopedToken. An empty or whitespace
// input returns (nil, nil) so callers can feed it unset env vars directly.
func ParseTokensJSON(raw string) ([]ScopedToken, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []ScopedToken
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse tokens json: %w", err)
	}
	return out, nil
}

// BearerAuth returns a middleware suitable for the Middlewares slot of
// StdHTTPServerOptions. Behaviour:
//
//   - Operations whose generated wrapper does not set BearerAuthScopes in
//     context (i.e., those declared with `security: []` in the OpenAPI
//     spec, such as /healthz and /readyz) pass through untouched.
//   - Missing or malformed Authorization header: 401 with WWW-Authenticate.
//   - Token not in the store: 401.
//   - Valid token missing any of the operation's required scopes: 403.
func BearerAuth(store *TokenStore) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scopesIface := r.Context().Value(BearerAuthScopes)
			if scopesIface == nil {
				next.ServeHTTP(w, r)
				return
			}
			required, _ := scopesIface.([]string)

			presented, ok := extractBearer(r.Header.Get("Authorization"))
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
				writeProblem(w, http.StatusUnauthorized, "Unauthorized", "missing bearer token")
				return
			}
			token, found := store.lookup(presented)
			if !found {
				w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
				writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid bearer token")
				return
			}
			for _, want := range required {
				if !token.hasScope(want) {
					writeProblem(w, http.StatusForbidden, "Forbidden", fmt.Sprintf("token %q lacks scope %q", token.Name, want))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ErrNoTokensConfigured is returned when argosd starts without any configured
// tokens. Callers typically return it from main() with a user-facing message.
var ErrNoTokensConfigured = errors.New("no API tokens configured; set ARGOS_API_TOKEN or ARGOS_API_TOKENS")

func extractBearer(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", false
	}
	return token, true
}
