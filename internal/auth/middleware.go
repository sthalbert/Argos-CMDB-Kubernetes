package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Scope values. These strings match exactly what `openapi.yaml` declares
// per-operation; changing them means changing the spec + regenerating.
const (
	ScopeRead        = "read"
	ScopeWrite       = "write"
	ScopeDelete      = "delete"
	ScopeAdmin       = "admin"
	ScopeAudit       = "audit"
	ScopeVMCollector = "vm-collector"
)

// Role values — enum over the fixed role set from ADR-0007.
const (
	RoleAdmin   = "admin"
	RoleEditor  = "editor"
	RoleAuditor = "auditor"
	RoleViewer  = "viewer"
)

// ValidRoles is the set accepted by the API surface for human users.
// Keep in sync with the CHECK constraint on the `users` table. The
// vm-collector scope (ADR-0015) is purely a token-issuance preset —
// it never appears as a user's role.
var ValidRoles = map[string]struct{}{
	RoleAdmin:   {},
	RoleEditor:  {},
	RoleAuditor: {},
	RoleViewer:  {},
}

// TokenScopePreset is a UI shorthand for picking scope sets when an
// admin issues a PAT. The four "role" presets mirror the user roles;
// the vm-collector preset is the only one specific to machine clients
// and requires binding to a cloud_account at issue time (ADR-0015).
const (
	TokenPresetAdmin       = "admin"
	TokenPresetEditor      = "editor"
	TokenPresetAuditor     = "auditor"
	TokenPresetViewer      = "viewer"
	TokenPresetVMCollector = "vm-collector"
)

// ScopesForRole returns the fixed scope set granted to a role. Admin
// always carries the admin scope too; it implicitly satisfies any
// scoped endpoint by the "admin implies all" convention the OpenAPI
// enforcer uses, **except** for ScopeVMCollector, which is reserved
// for collector tokens bound to a specific cloud_account (ADR-0015 §5).
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

// ScopesForTokenPreset returns the scope set associated with a token
// issuance preset. Mirrors ScopesForRole for the "human" presets;
// returns the single ScopeVMCollector for the collector preset.
func ScopesForTokenPreset(preset string) []string {
	switch preset {
	case TokenPresetVMCollector:
		return []string{ScopeVMCollector}
	default:
		return ScopesForRole(preset)
	}
}

// CallerKind distinguishes human-session callers from machine-token
// callers. Handlers can gate flows like "change password" on kind=User.
type CallerKind string

// CallerKind constants distinguish human from machine callers.
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
	// BoundCloudAccountID is set on tokens issued with the
	// vm-collector scope (ADR-0015). When non-nil, handlers on
	// per-account endpoints must verify the requested account
	// matches this binding via EnforceCloudAccountBinding.
	BoundCloudAccountID *uuid.UUID
}

// HasScope reports whether the caller carries want.
//
// Admin scope implies every other "regular" scope (read / write /
// delete / audit) — that's the simplifying convention from ADR-0007.
//
// Admin scope does **not** imply ScopeVMCollector: that scope is
// reserved for collector tokens bound to a specific cloud_account
// (ADR-0015 §5). Letting admin tokens fetch credentials would defeat
// the SK-is-write-only-from-admin-endpoints guarantee.
func (c *Caller) HasScope(want string) bool {
	for _, s := range c.Scopes {
		if s == want {
			return true
		}
		if s == ScopeAdmin && want != ScopeVMCollector {
			return true
		}
	}
	return false
}

// EnforceCloudAccountBinding verifies that a token caller carrying the
// vm-collector scope is bound to the cloud_account identified by id.
// Tokens without the vm-collector scope pass through unchecked — the
// regular scope check is the only gate for those callers.
//
// Returns ErrUnauthorized when:
//   - the caller is a vm-collector token with no binding (must never happen
//     in production but defended against here);
//   - the caller's binding does not equal id.
func (c *Caller) EnforceCloudAccountBinding(id uuid.UUID) error {
	hasVM := false
	for _, s := range c.Scopes {
		if s == ScopeVMCollector {
			hasVM = true
			break
		}
	}
	if !hasVM {
		return nil
	}
	if c.BoundCloudAccountID == nil {
		return ErrUnauthorized
	}
	if *c.BoundCloudAccountID != id {
		return ErrUnauthorized
	}
	return nil
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
	// BoundCloudAccountID is set on tokens issued with the
	// vm-collector scope (ADR-0015). Nullable — every other token
	// kind leaves it empty.
	BoundCloudAccountID *uuid.UUID
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
// refresh + logout). `trustedProxies` is consulted only when the policy
// is SecureAuto: it gates whether X-Forwarded-Proto is honored when
// deciding the Secure flag, per ADR-0017. Pass nil to ignore XFP
// unconditionally — the secure default.
func Middleware(store Store, policy SecureCookiePolicy, trustedProxies []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			required, authed := requiredScopes(r.Context())
			if !authed {
				// No security declared → public endpoint, let it through.
				next.ServeHTTP(w, r)
				return
			}

			caller, err := resolve(r, store, policy, trustedProxies, w)
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

func resolve(r *http.Request, store Store, policy SecureCookiePolicy, trustedProxies []*net.IPNet, w http.ResponseWriter) (*Caller, error) {
	if caller, err := tryCookie(r, store, policy, trustedProxies, w); err == nil {
		return caller, nil
	} else if !errors.Is(err, http.ErrNoCookie) {
		return nil, err
	}
	return tryBearer(r, store)
}

func tryCookie(r *http.Request, store Store, policy SecureCookiePolicy, trustedProxies []*net.IPNet, w http.ResponseWriter) (*Caller, error) {
	ck, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("read session cookie: %w", err)
	}
	if ck.Value == "" {
		return nil, http.ErrNoCookie
	}

	sess, err := store.GetActiveSession(r.Context(), ck.Value)
	if err != nil {
		// Revoked / expired / unknown session → clear the cookie so the
		// browser stops sending it every request.
		ClearSessionCookie(w, r, policy, trustedProxies)
		return nil, fmt.Errorf("get active session: %w", err)
	}

	user, err := store.GetUserForAuth(r.Context(), sess.UserID)
	if err != nil {
		ClearSessionCookie(w, r, policy, trustedProxies)
		return nil, fmt.Errorf("get user for auth: %w", err)
	}
	if user.Disabled {
		ClearSessionCookie(w, r, policy, trustedProxies)
		return nil, ErrUnauthorized
	}

	now := time.Now().UTC()
	newExpiry := now.Add(SessionDuration)
	// Best-effort refresh — if the UPDATE fails, the request still
	// succeeds; we just won't have slid the expiry this tick.
	_ = store.TouchSession(r.Context(), sess.ID, now, newExpiry)
	SetSessionCookie(w, r, sess.ID, newExpiry, policy, trustedProxies)

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
	caller, err := VerifyBearerToken(r.Context(), store, presented)
	if err != nil {
		return nil, err
	}
	return caller, nil
}

// VerifyBearerToken validates an opaque PAT against the token store and
// returns the resulting Caller without modifying any HTTP state. This is
// the same argon2id verification path the bearer middleware runs per
// request; exposed as its own function so the DMZ ingest gateway's
// /v1/auth/verify endpoint (ADR-0016 §5) can run the check without
// having to fabricate an http.Request.
//
// Returns ErrUnauthorized for malformed, unknown, expired, revoked, or
// hash-mismatched tokens so callers can return 401 without leaking which
// bit was wrong. Best-effort touches the token's last_used_at on success.
func VerifyBearerToken(ctx context.Context, store Store, presented string) (*Caller, error) {
	tokPrefix, _, err := ParseToken(presented)
	if err != nil {
		return nil, ErrUnauthorized
	}

	tok, err := store.GetActiveTokenByPrefix(ctx, tokPrefix)
	if err != nil {
		return nil, fmt.Errorf("get active token by prefix: %w", err)
	}
	if err := VerifyPassword(presented, tok.Hash); err != nil {
		return nil, ErrUnauthorized
	}

	// Best-effort last-used refresh. Don't fail the request if this errors.
	_ = store.TouchToken(ctx, tok.ID, time.Now().UTC())

	return &Caller{
		Kind:                CallerKindToken,
		TokenID:             tok.ID,
		TokenName:           tok.Name,
		UserID:              tok.CreatedByUserID,
		Scopes:              tok.Scopes,
		BoundCloudAccountID: tok.BoundCloudAccountID,
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
	//nolint:errchkjson // best-effort write; struct literal is always serialisable.
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
