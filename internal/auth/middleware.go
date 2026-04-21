package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Scope values. These strings match exactly what `openapi.yaml` declares
// per-operation; changing them means changing the spec + regenerating.
const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeDelete = "delete"
	ScopeAdmin  = "admin"
	ScopeAudit  = "audit"
)

// Role values — enum over the fixed role set from ADR-0007.
const (
	RoleAdmin   = "admin"
	RoleEditor  = "editor"
	RoleAuditor = "auditor"
	RoleViewer  = "viewer"
)

// ValidRoles is the set accepted by the API surface. Keep in sync with
// the CHECK constraint on the `users` table.
var ValidRoles = map[string]struct{}{
	RoleAdmin:   {},
	RoleEditor:  {},
	RoleAuditor: {},
	RoleViewer:  {},
}

// scopesForRole returns the fixed scope set granted to a role. Admin
// always carries the admin scope too; it implicitly satisfies any
// scoped endpoint by the "admin implies all" convention the OpenAPI
// enforcer uses.
func ScopesForRole(role string) []string {
	switch role {
	case RoleAdmin:
		return []string{ScopeRead, ScopeWrite, ScopeDelete, ScopeAdmin, ScopeAudit}
	case RoleEditor:
		return []string{ScopeRead, ScopeWrite}
	case RoleAuditor:
		return []string{ScopeRead, ScopeAudit}
	case RoleViewer:
		return []string{ScopeRead}
	}
	return nil
}

// CallerKind distinguishes human-session callers from machine-token
// callers. Handlers can gate flows like "change password" on kind=User.
type CallerKind string

const (
	CallerKindUser  CallerKind = "user"
	CallerKindToken CallerKind = "token"
)

// Caller is what middleware attaches to the request context on
// successful auth. Handlers read it with CallerFromContext.
type Caller struct {
	Kind CallerKind
	// UserID is populated when Kind == CallerKindUser; for token
	// callers, it's the user who minted the token.
	UserID uuid.UUID
	// TokenID is populated when Kind == CallerKindToken.
	TokenID uuid.UUID
	// SessionID is populated when Kind == CallerKindUser — handy
	// for logout to know which row to delete without re-reading
	// the cookie.
	SessionID string
	// Username / TokenName are cosmetic — for log lines and audit
	// entries, never for auth decisions.
	Username  string
	TokenName string
	// Role is only meaningful for Kind == CallerKindUser.
	Role string
	// Scopes are already resolved: role→scopes for users, the
	// token's declared scopes for machines.
	Scopes []string
	// MustChangePassword signals the forced-rotation state. Only
	// /v1/auth/change-password and /v1/auth/me are reachable while
	// it's true; everything else gets a 403 steering the UI to the
	// rotation page.
	MustChangePassword bool
}

// HasScope reports whether the caller carries want. Admin implies all.
func (c Caller) HasScope(want string) bool {
	for _, s := range c.Scopes {
		if s == ScopeAdmin || s == want {
			return true
		}
	}
	return false
}

type callerCtxKey struct{}

// WithCaller returns a new context carrying c. Test helpers use it; the
// middleware uses it on every authenticated request.
func WithCaller(ctx context.Context, c *Caller) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, c)
}

// CallerFromContext returns the caller attached by the middleware, or
// nil for unauthenticated requests.
func CallerFromContext(ctx context.Context) *Caller {
	c, _ := ctx.Value(callerCtxKey{}).(*Caller)
	return c
}

// Session / Token / User shapes the middleware reads. Kept inside this
// package so the store implementation can depend on it without a cycle.

// Session is the post-lookup view of a row in the `sessions` table. The
// middleware doesn't care about CreatedAt / UserAgent / SourceIP; only
// UserID and expiry.
type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
}

// APIToken is the post-lookup view of a row in the `api_tokens` table
// after prefix + hash match. Expires and Revoked checks happen in the
// store.
type APIToken struct {
	ID              uuid.UUID
	Name            string
	Hash            string
	Scopes          []string
	CreatedByUserID uuid.UUID
}

// User is the post-lookup view for attaching role + must_change_password.
type User struct {
	ID                 uuid.UUID
	Username           string
	Role               string
	MustChangePassword bool
	Disabled           bool
}

// Store is the subset of the persistence layer the auth middleware
// consumes. Implementations must be safe for concurrent use.
type Store interface {
	// GetActiveSession returns the session by id if it exists and is
	// not expired. Returns ErrUnauthorized for expired / missing rows
	// so callers can treat both the same.
	GetActiveSession(ctx context.Context, id string) (Session, error)
	// TouchSession refreshes last_used_at and extends expires_at.
	TouchSession(ctx context.Context, id string, now time.Time, newExpiry time.Time) error
	// GetUserForAuth loads the fields the middleware needs.
	GetUserForAuth(ctx context.Context, id uuid.UUID) (User, error)
	// GetActiveTokenByPrefix returns the token row keyed on its
	// 8-char prefix, restricted to non-revoked + non-expired.
	GetActiveTokenByPrefix(ctx context.Context, prefix string) (APIToken, error)
	// TouchToken refreshes last_used_at.
	TouchToken(ctx context.Context, id uuid.UUID, now time.Time) error
}

// ErrUnauthorized is returned by Store implementations on missing /
// expired / revoked rows. The middleware maps it to a 401 without
// leaking which bit was wrong.
var ErrUnauthorized = errors.New("unauthorized")

// Middleware resolves cookie → bearer → 401 and attaches the Caller to
// the request context. Public endpoints (no scope list in context)
// pass through untouched.
//
// `policy` governs the Secure flag on Set-Cookie rewrites (on expiry
// refresh + logout). Pass it to the Clear helpers; this middleware
// itself only refreshes, via a fresh Set-Cookie when it touches a
// session, to keep the browser's Max-Age in step with the DB's
// expires_at.
func Middleware(store Store, policy SecureCookiePolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			required, authed := requiredScopes(r.Context())
			if !authed {
				// No security declared → public endpoint, let it through.
				next.ServeHTTP(w, r)
				return
			}

			caller, err := resolve(r, store, policy, w)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="argos"`)
				writeProblemJSON(w, http.StatusUnauthorized, "Unauthorized", "missing or invalid credentials")
				return
			}

			// Forced password-change state: admins in bootstrap mode hit
			// the API before they can rotate. Let /v1/auth/me and
			// /v1/auth/change-password through so the UI can guide the
			// rotation; everything else gets a 403.
			if caller.MustChangePassword && !isPasswordChangeAllowed(r.URL.Path) {
				writeProblemJSON(w, http.StatusForbidden, "Password change required",
					"rotate your password at /v1/auth/change-password before calling other endpoints")
				return
			}

			for _, want := range required {
				if !caller.HasScope(want) {
					writeProblemJSON(w, http.StatusForbidden, "Forbidden",
						fmt.Sprintf("caller lacks scope %q", want))
					return
				}
			}

			r = r.WithContext(WithCaller(r.Context(), caller))
			next.ServeHTTP(w, r)
		})
	}
}

// requiredScopes returns the union of BearerAuth and SessionCookie
// scope lists declared for the current operation. Returns (nil, false)
// only when neither key is present — i.e., the endpoint is public.
func requiredScopes(ctx context.Context) ([]string, bool) {
	// Match the strings oapi-codegen emits in api.gen.go; we avoid an
	// import cycle by referencing them as plain strings here. If the
	// generator ever renames these, a compile-time integration test
	// catches it.
	bearer, _ := ctx.Value("BearerAuth.Scopes").([]string)
	cookie, _ := ctx.Value("SessionCookie.Scopes").([]string)
	if bearer == nil && cookie == nil {
		return nil, false
	}
	// Both lists should carry the same required scopes (openapi
	// declares them identically for endpoints accepting either auth).
	// Preferring bearer if both are set is arbitrary but stable.
	if bearer != nil {
		return bearer, true
	}
	return cookie, true
}

func resolve(r *http.Request, store Store, policy SecureCookiePolicy, w http.ResponseWriter) (*Caller, error) {
	if caller, err := tryCookie(r, store, policy, w); err == nil {
		return caller, nil
	} else if !errors.Is(err, http.ErrNoCookie) {
		return nil, err
	}
	return tryBearer(r, store)
}

func tryCookie(r *http.Request, store Store, policy SecureCookiePolicy, w http.ResponseWriter) (*Caller, error) {
	ck, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, err
	}
	if ck.Value == "" {
		return nil, http.ErrNoCookie
	}

	sess, err := store.GetActiveSession(r.Context(), ck.Value)
	if err != nil {
		// Revoked / expired / unknown session → clear the cookie so the
		// browser stops sending it every request.
		ClearSessionCookie(w, r, policy)
		return nil, err
	}

	user, err := store.GetUserForAuth(r.Context(), sess.UserID)
	if err != nil {
		ClearSessionCookie(w, r, policy)
		return nil, err
	}
	if user.Disabled {
		ClearSessionCookie(w, r, policy)
		return nil, ErrUnauthorized
	}

	now := time.Now().UTC()
	newExpiry := now.Add(SessionDuration)
	// Best-effort refresh — if the UPDATE fails, the request still
	// succeeds; we just won't have slid the expiry this tick.
	_ = store.TouchSession(r.Context(), sess.ID, now, newExpiry)
	SetSessionCookie(w, r, sess.ID, newExpiry, policy)

	return &Caller{
		Kind:               CallerKindUser,
		UserID:             user.ID,
		Username:           user.Username,
		Role:               user.Role,
		Scopes:             ScopesForRole(user.Role),
		SessionID:          sess.ID,
		MustChangePassword: user.MustChangePassword,
	}, nil
}

func tryBearer(r *http.Request, store Store) (*Caller, error) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return nil, ErrUnauthorized
	}
	presented := strings.TrimPrefix(h, prefix)
	if presented == "" {
		return nil, ErrUnauthorized
	}

	tokPrefix, _, err := ParseToken(presented)
	if err != nil {
		return nil, ErrUnauthorized
	}

	tok, err := store.GetActiveTokenByPrefix(r.Context(), tokPrefix)
	if err != nil {
		return nil, err
	}
	if err := VerifyPassword(presented, tok.Hash); err != nil {
		return nil, ErrUnauthorized
	}

	// Best-effort last-used refresh. Don't fail the request if this errors.
	_ = store.TouchToken(r.Context(), tok.ID, time.Now().UTC())

	return &Caller{
		Kind:      CallerKindToken,
		TokenID:   tok.ID,
		TokenName: tok.Name,
		UserID:    tok.CreatedByUserID,
		Scopes:    tok.Scopes,
	}, nil
}

// isPasswordChangeAllowed whitelists the endpoints a forced-password
// user can reach before rotating. Matches the prefix rather than an
// exact path so `/v1/auth/me` and `/v1/auth/logout` (benign) also
// work.
func isPasswordChangeAllowed(path string) bool {
	switch path {
	case "/v1/auth/me", "/v1/auth/change-password", "/v1/auth/logout":
		return true
	}
	return false
}

// writeProblemJSON emits a minimal RFC 7807 body. The api package has a
// richer helper; duplicated here to avoid an import cycle.
func writeProblemJSON(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
