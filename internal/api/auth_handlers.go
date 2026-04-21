package api

// Handlers for the /v1/auth/* and /v1/admin/* endpoints declared in
// openapi.yaml per ADR-0007. The substrate (password hashing, token
// minting, session cookies, the resolve-caller middleware) lives in
// internal/auth; this file just wires the HTTP shape onto the store.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// ── request-in-context plumbing ─────────────────────────────────────
//
// Several strict handlers need the original *http.Request for cookies,
// User-Agent, or source-IP. The strict handler wrapper passes
// r.Context() as ctx but not r itself, so we inject it via a
// StrictMiddlewareFunc registered at startup.

type ctxKeyHTTPRequest struct{}

// InjectRequestMiddleware is a StrictMiddlewareFunc that stores the
// *http.Request in the context so strict handlers can retrieve it.
func InjectRequestMiddleware(
	f StrictHandlerFunc,
	_ string,
) StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request interface{}) (interface{}, error) {
		ctx = context.WithValue(ctx, ctxKeyHTTPRequest{}, r)
		return f(ctx, w, r, request)
	}
}

// httpRequestFromCtx retrieves the *http.Request previously stored by
// InjectRequestMiddleware. Returns nil when absent (should never happen
// if the middleware is wired).
func httpRequestFromCtx(ctx context.Context) *http.Request {
	r, _ := ctx.Value(ctxKeyHTTPRequest{}).(*http.Request)
	return r
}

// ── custom response types ────────────────────────────────────────────
//
// A few auth endpoints need to set a cookie alongside the generated
// response status (Logout 204, ChangePassword 204, OidcCallback 302)
// but the generated response types don't carry a Set-Cookie header.
// We define minimal custom visitors for those cases.

// logout204WithCookie clears the session cookie alongside the 204.
type logout204WithCookie struct {
	cookie *http.Cookie
}

func (r logout204WithCookie) VisitLogoutResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	w.WriteHeader(204)
	return nil
}

// changePassword204WithCookie clears the session cookie alongside the 204.
type changePassword204WithCookie struct {
	cookie *http.Cookie
}

func (r changePassword204WithCookie) VisitChangePasswordResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	w.WriteHeader(204)
	return nil
}

// oidcCallback302WithCookie sets a session cookie alongside the 302.
type oidcCallback302WithCookie struct {
	location string
	cookie   *http.Cookie
}

func (r oidcCallback302WithCookie) VisitOidcCallbackResponse(w http.ResponseWriter) error {
	http.SetCookie(w, r.cookie)
	w.Header().Set("Location", r.location)
	w.WriteHeader(302)
	return nil
}

// oidcCallbackErrorRedirect redirects to the login page with an error
// code — used for every non-happy path in the OIDC callback.
type oidcCallbackErrorRedirect struct {
	code string
}

func (r oidcCallbackErrorRedirect) VisitOidcCallbackResponse(w http.ResponseWriter) error {
	w.Header().Set("Location", "/ui/login?oidc_error="+url.QueryEscape(r.code))
	w.WriteHeader(302)
	return nil
}

// ── problem helpers for auth-specific status codes ───────────────────
//
// server.go already provides problemBadRequest, problemNotFound, and
// problemConflict which return Problem. The generated response types
// embed defined-type aliases (e.g. UnauthorizedApplicationProblemPlusJSONResponse)
// so we need conversion-compatible helpers for 401 and 403.

func problemUnauthorized(detail string) UnauthorizedApplicationProblemPlusJSONResponse {
	p := Problem{Type: "about:blank", Title: "Unauthorized", Status: 401}
	if detail != "" {
		p.Detail = &detail
	}
	return UnauthorizedApplicationProblemPlusJSONResponse(p)
}

func problemForbidden(detail string) ForbiddenApplicationProblemPlusJSONResponse {
	p := Problem{Type: "about:blank", Title: "Forbidden", Status: 403}
	if detail != "" {
		p.Detail = &detail
	}
	return ForbiddenApplicationProblemPlusJSONResponse(p)
}

// ── /v1/auth/config + OIDC ──────────────────────────────────────────

// GetAuthConfig surfaces what the login page needs pre-session.
func (s *Server) GetAuthConfig(_ context.Context, _ GetAuthConfigRequestObject) (GetAuthConfigResponseObject, error) {
	resp := AuthConfig{}
	enabled := s.oidc != nil
	resp.Oidc.Enabled = enabled
	if enabled {
		label := s.oidc.Config.Label
		resp.Oidc.Label = &label
	}
	return GetAuthConfig200JSONResponse(resp), nil
}

// OidcAuthorize mints state + PKCE + nonce, stores them, redirects to
// the IdP. 404 when OIDC is unconfigured.
func (s *Server) OidcAuthorize(ctx context.Context, _ OidcAuthorizeRequestObject) (OidcAuthorizeResponseObject, error) {
	if s.oidc == nil {
		return OidcAuthorize404Response{}, nil
	}
	state, err := auth.GenerateOIDCState()
	if err != nil {
		return nil, fmt.Errorf("oidcAuthorize state: %w", err)
	}
	verifier, challenge, err := auth.GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("oidcAuthorize pkce: %w", err)
	}
	nonce, err := auth.GenerateNonce()
	if err != nil {
		return nil, fmt.Errorf("oidcAuthorize nonce: %w", err)
	}
	now := time.Now().UTC()
	if err := s.store.CreateOidcAuthState(ctx, OidcAuthStateInsert{
		State:        state,
		CodeVerifier: verifier,
		Nonce:        nonce,
		CreatedAt:    now,
		ExpiresAt:    now.Add(oidcStateTTL),
	}); err != nil {
		return nil, fmt.Errorf("oidcAuthorize store: %w", err)
	}

	return OidcAuthorize302Response{
		Headers: OidcAuthorize302ResponseHeaders{
			Location: s.oidc.AuthorizeURL(state, challenge, nonce),
		},
	}, nil
}

// OidcCallback completes the flow, finds-or-creates the shadow user,
// mints a session, and redirects to the UI. On failure redirects to
// /ui/login?oidc_error=<reason> so the UI can surface what went wrong.
func (s *Server) OidcCallback(ctx context.Context, request OidcCallbackRequestObject) (OidcCallbackResponseObject, error) {
	if s.oidc == nil {
		return oidcCallbackErrorRedirect{code: "oidc_not_configured"}, nil
	}

	r := httpRequestFromCtx(ctx)

	verifier, nonce, err := s.store.ConsumeOidcAuthState(ctx, request.Params.State)
	if err != nil {
		return oidcCallbackErrorRedirect{code: "state_expired_or_unknown"}, nil
	}

	claims, err := s.oidc.Exchange(ctx, request.Params.Code, verifier, nonce)
	if err != nil {
		slog.Warn("oidc: exchange or id-token verify failed", "error", err)
		return oidcCallbackErrorRedirect{code: "exchange_failed"}, nil
	}

	user, err := s.findOrCreateOidcUser(ctx, claims)
	if err != nil {
		slog.Error("oidc: shadow-user resolution failed", "error", err)
		return oidcCallbackErrorRedirect{code: "user_lookup_failed"}, nil
	}

	// Mint session — same shape as the local-login path.
	sid, err := auth.RandomSecret(32)
	if err != nil {
		return oidcCallbackErrorRedirect{code: "session_mint_failed"}, nil
	}
	now := time.Now().UTC()
	expires := now.Add(auth.SessionDuration)
	if err := s.store.CreateSession(ctx, SessionInsert{
		ID:        sid,
		UserID:    *user.Id,
		CreatedAt: now,
		ExpiresAt: expires,
		UserAgent: r.UserAgent(),
		SourceIP:  clientIP(r),
	}); err != nil {
		return oidcCallbackErrorRedirect{code: "session_create_failed"}, nil
	}
	_ = s.store.TouchUserLogin(ctx, *user.Id, now)
	_ = s.store.TouchUserIdentity(ctx, *user.Id, claims.Issuer, claims.Sub, now)

	cookie := auth.SessionCookie(sid, expires, r, s.cookiePolicy)
	return oidcCallback302WithCookie{
		location: "/ui/",
		cookie:   cookie,
	}, nil
}

// findOrCreateOidcUser looks up by (issuer, sub); on miss, creates a
// shadow user. First-login role is viewer per ADR-0007 — admins promote.
func (s *Server) findOrCreateOidcUser(ctx context.Context, claims *auth.OIDCClaims) (User, error) {
	u, err := s.store.GetUserByIdentity(ctx, claims.Issuer, claims.Sub)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return User{}, err
	}

	// First login → create. Username: prefer preferred_username, then
	// email's local part, then a sub-derived fallback. Resolve collisions
	// by appending a short random suffix — case-insensitive username
	// uniqueness is enforced at the DB.
	base := deriveUsername(claims)
	username := base
	for attempt := 0; attempt < 5; attempt++ {
		// Shadow users never use the password path; fill the hash slot
		// with a deliberately unusable sentinel so VerifyPassword always
		// fails if someone tries the local-login flow with this name.
		// argon2id verify against a junk PHC string returns
		// ErrInvalidHashFormat, which the login handler surfaces as 401.
		out, err := s.store.CreateUserWithIdentity(ctx,
			UserInsert{
				Username:           username,
				PasswordHash:       "$argon2id$shadow$oidc", // never verifies
				Role:               auth.RoleViewer,
				MustChangePassword: false,
			},
			UserIdentityInsert{
				Issuer:  claims.Issuer,
				Subject: claims.Sub,
				Email:   claims.Email,
			},
		)
		if err == nil {
			return out, nil
		}
		// Two distinct conflict cases:
		//   - username taken → retry with a suffix
		//   - identity taken (race with a parallel first-login) → re-read
		if !errors.Is(err, ErrConflict) {
			return User{}, err
		}
		// Was it the identity? Re-reading resolves the race.
		if u, rerr := s.store.GetUserByIdentity(ctx, claims.Issuer, claims.Sub); rerr == nil {
			return u, nil
		}
		// Must've been the username — suffix and retry.
		suffix, sErr := auth.RandomSecret(3)
		if sErr != nil {
			return User{}, sErr
		}
		username = base + "-" + suffix
	}
	return User{}, fmt.Errorf("could not pick a free username after retries")
}

// deriveUsername picks a friendly local username from the OIDC claims.
// Output is lowercased and stripped down to characters the `users.username`
// constraint (`^[a-zA-Z0-9._-]+$` in the OpenAPI schema) accepts.
func deriveUsername(c *auth.OIDCClaims) string {
	candidates := []string{c.PreferredUsername, emailLocalPart(c.Email), "oidc-" + shortSub(c.Sub)}
	for _, raw := range candidates {
		if u := sanitiseUsername(raw); u != "" {
			return u
		}
	}
	return "oidc-user"
}

func emailLocalPart(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return ""
}

func shortSub(sub string) string {
	const n = 8
	if len(sub) <= n {
		return sub
	}
	return sub[:n]
}

func sanitiseUsername(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			out = append(out, c)
		default:
			// drop
		}
	}
	if len(out) == 0 {
		return ""
	}
	return string(out)
}

// oidcStateTTL bounds how long a user can take to complete the IdP's
// login page before the pending authorization expires. 5 minutes is
// plenty and matches common OAuth2 implementations.
const oidcStateTTL = 5 * time.Minute

// ── /v1/auth ─────────────────────────────────────────────────────────

// Login validates username + password and issues a session cookie.
// 401 on any failure (no distinction between unknown user and bad
// password) to avoid username enumeration.
func (s *Server) Login(ctx context.Context, request LoginRequestObject) (LoginResponseObject, error) {
	body := request.Body
	if body.Username == "" || body.Password == "" {
		return Login400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "username and password are required")),
		}, nil
	}

	r := httpRequestFromCtx(ctx)

	user, err := s.store.GetUserByUsername(ctx, body.Username)
	if err != nil {
		// Still run an argon2 verify against a dummy hash so timing
		// doesn't leak whether the username exists.
		_ = auth.VerifyPassword(body.Password, dummyHash)
		return Login401ApplicationProblemPlusJSONResponse{
			problemUnauthorized("invalid credentials"),
		}, nil
	}
	if err := auth.VerifyPassword(body.Password, user.PasswordHash); err != nil {
		return Login401ApplicationProblemPlusJSONResponse{
			problemUnauthorized("invalid credentials"),
		}, nil
	}

	// Mint session id + persist row + set cookie.
	sid, err := auth.RandomSecret(32)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	now := time.Now().UTC()
	expires := now.Add(auth.SessionDuration)
	if err := s.store.CreateSession(ctx, SessionInsert{
		ID:        sid,
		UserID:    *user.Id,
		CreatedAt: now,
		ExpiresAt: expires,
		UserAgent: r.UserAgent(),
		SourceIP:  clientIP(r),
	}); err != nil {
		return nil, fmt.Errorf("login create session: %w", err)
	}
	_ = s.store.TouchUserLogin(ctx, *user.Id, now)

	cookie := auth.SessionCookie(sid, expires, r, s.cookiePolicy)
	return Login204Response{
		Headers: Login204ResponseHeaders{
			SetCookie: cookie.String(),
		},
	}, nil
}

// Logout deletes the session row (if any) and clears the cookie.
// Idempotent for bearer callers — nothing to revoke.
func (s *Server) Logout(ctx context.Context, _ LogoutRequestObject) (LogoutResponseObject, error) {
	caller := auth.CallerFromContext(ctx)
	if caller != nil && caller.Kind == auth.CallerKindUser && caller.SessionID != "" {
		if err := s.store.DeleteSession(ctx, caller.SessionID); err != nil &&
			!errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("logout: %w", err)
		}
	}
	r := httpRequestFromCtx(ctx)
	cookie := auth.ClearSessionCookieValue(r, s.cookiePolicy)
	return logout204WithCookie{cookie: cookie}, nil
}

// GetMe returns identity + role + effective scopes for the current caller.
// The UI polls this on load to decide which nav items to render and
// whether to redirect to the forced-change page.
func (s *Server) GetMe(ctx context.Context, _ GetMeRequestObject) (GetMeResponseObject, error) {
	caller := auth.CallerFromContext(ctx)
	if caller == nil {
		return GetMe401ApplicationProblemPlusJSONResponse{
			problemUnauthorized(""),
		}, nil
	}
	out := Me{Scopes: caller.Scopes}
	if caller.Kind == auth.CallerKindUser {
		k := MeKind("user")
		out.Kind = k
		id := caller.UserID
		out.Id = &id
		username := caller.Username
		out.Username = &username
		role := Role(caller.Role)
		out.Role = &role
		mcp := caller.MustChangePassword
		out.MustChangePassword = &mcp
	} else {
		k := MeKind("token")
		out.Kind = k
		id := caller.TokenID
		out.Id = &id
		name := caller.TokenName
		out.TokenName = &name
	}
	return GetMe200JSONResponse(out), nil
}

// ChangePassword rotates the caller's password. Session-only — bearer
// tokens can't change a user's password.
func (s *Server) ChangePassword(ctx context.Context, request ChangePasswordRequestObject) (ChangePasswordResponseObject, error) {
	caller := auth.CallerFromContext(ctx)
	if caller == nil || caller.Kind != auth.CallerKindUser {
		return ChangePassword403ApplicationProblemPlusJSONResponse{
			problemForbidden("password change requires a human session"),
		}, nil
	}
	body := request.Body
	if len(body.NewPassword) < minPasswordLength {
		return ChangePassword400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid password", "new password must be at least 12 characters")),
		}, nil
	}

	// Re-fetch the user with its hash so we can verify the current password.
	// GetUserByUsername does the disabled check we want.
	withSecret, err := s.store.GetUserByUsername(ctx, caller.Username)
	if err != nil {
		return nil, fmt.Errorf("change-password lookup: %w", err)
	}
	if err := auth.VerifyPassword(body.CurrentPassword, withSecret.PasswordHash); err != nil {
		return ChangePassword401ApplicationProblemPlusJSONResponse{
			problemUnauthorized("current password does not match"),
		}, nil
	}

	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		return nil, fmt.Errorf("change-password hash: %w", err)
	}
	if err := s.store.SetUserPassword(ctx, caller.UserID, hash, false); err != nil {
		return nil, fmt.Errorf("change-password store: %w", err)
	}
	// SetUserPassword deletes every session for this user — including
	// ours. Clear the cookie so the browser drops it and the UI
	// redirects back to login.
	r := httpRequestFromCtx(ctx)
	cookie := auth.ClearSessionCookieValue(r, s.cookiePolicy)
	return changePassword204WithCookie{cookie: cookie}, nil
}

// ── /v1/admin/users ─────────────────────────────────────────────────

func (s *Server) ListUsers(ctx context.Context, request ListUsersRequestObject) (ListUsersResponseObject, error) {
	limit, cursor := paging(request.Params.Limit, request.Params.Cursor)
	items, next, err := s.store.ListUsers(ctx, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("listUsers: %w", err)
	}
	resp := UserList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListUsers200JSONResponse(resp), nil
}

func (s *Server) CreateUser(ctx context.Context, request CreateUserRequestObject) (CreateUserResponseObject, error) {
	body := request.Body
	if body.Username == "" {
		return CreateUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "username is required")),
		}, nil
	}
	if len(body.Password) < minPasswordLength {
		return CreateUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid password", "password must be at least 12 characters")),
		}, nil
	}
	if _, ok := auth.ValidRoles[string(body.Role)]; !ok {
		return CreateUser400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid role", "role must be one of admin, editor, auditor, viewer")),
		}, nil
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		return nil, fmt.Errorf("createUser hash: %w", err)
	}
	mustChange := true
	if body.MustChangePassword != nil {
		mustChange = *body.MustChangePassword
	}
	created, err := s.store.CreateUser(ctx, UserInsert{
		Username:           body.Username,
		PasswordHash:       hash,
		Role:               string(body.Role),
		MustChangePassword: mustChange,
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return CreateUser409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse(problemConflict(err)),
			}, nil
		}
		return nil, fmt.Errorf("createUser: %w", err)
	}
	return CreateUser201JSONResponse(created), nil
}

func (s *Server) GetUser(ctx context.Context, request GetUserRequestObject) (GetUserResponseObject, error) {
	u, err := s.store.GetUser(ctx, request.Id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return GetUser404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("getUser: %w", err)
	}
	return GetUser200JSONResponse(u), nil
}

func (s *Server) UpdateUser(ctx context.Context, request UpdateUserRequestObject) (UpdateUserResponseObject, error) {
	body := request.Body

	patch := UserPatch{
		MustChangePassword: body.MustChangePassword,
		Disabled:           body.Disabled,
	}
	if body.Role != nil {
		r := string(*body.Role)
		if _, ok := auth.ValidRoles[r]; !ok {
			return UpdateUser400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid role", "role must be one of admin, editor, auditor, viewer")),
			}, nil
		}
		patch.Role = &r
	}
	// Password updates go through SetUserPassword so the hash stays out
	// of UserPatch (and so we can force must_change_password atomically).
	if body.Password != nil {
		if len(*body.Password) < minPasswordLength {
			return UpdateUser400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid password", "password must be at least 12 characters")),
			}, nil
		}
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			return nil, fmt.Errorf("updateUser hash: %w", err)
		}
		if err := s.store.SetUserPassword(ctx, request.Id, hash, true); err != nil {
			if errors.Is(err, ErrNotFound) {
				return UpdateUser404ApplicationProblemPlusJSONResponse{
					NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
				}, nil
			}
			return nil, fmt.Errorf("updateUser password: %w", err)
		}
	}

	u, err := s.store.UpdateUser(ctx, request.Id, patch)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return UpdateUser404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("updateUser: %w", err)
	}
	return UpdateUser200JSONResponse(u), nil
}

func (s *Server) DeleteUser(ctx context.Context, request DeleteUserRequestObject) (DeleteUserResponseObject, error) {
	// Guard against an admin deleting themselves mid-session —
	// re-admitting the break-glass admin path gets awkward. Requires
	// the caller to disable the target via UpdateUser first, then
	// delete from a separate admin session if desired.
	if caller := auth.CallerFromContext(ctx); caller != nil &&
		caller.Kind == auth.CallerKindUser && caller.UserID == request.Id {
		detail := "ask another admin to delete your account, or disable yourself via PATCH first"
		return DeleteUser409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse(Problem{
				Type: "about:blank", Title: "Cannot delete self", Status: 409, Detail: &detail,
			}),
		}, nil
	}
	if err := s.store.DeleteUser(ctx, request.Id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return DeleteUser404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("deleteUser: %w", err)
	}
	return DeleteUser204Response{}, nil
}

// ── /v1/admin/tokens ─────────────────────────────────────────────────

func (s *Server) ListApiTokens(ctx context.Context, request ListApiTokensRequestObject) (ListApiTokensResponseObject, error) {
	limit, cursor := paging(request.Params.Limit, request.Params.Cursor)
	items, next, err := s.store.ListAPITokens(ctx, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("listApiTokens: %w", err)
	}
	resp := ApiTokenList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListApiTokens200JSONResponse(resp), nil
}

func (s *Server) CreateApiToken(ctx context.Context, request CreateApiTokenRequestObject) (CreateApiTokenResponseObject, error) {
	caller := auth.CallerFromContext(ctx)
	if caller == nil {
		return CreateApiToken401ApplicationProblemPlusJSONResponse{
			problemUnauthorized(""),
		}, nil
	}

	body := request.Body
	if strings.TrimSpace(body.Name) == "" {
		return CreateApiToken400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Missing field", "name is required")),
		}, nil
	}
	if len(body.Scopes) == 0 {
		return CreateApiToken400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid scopes", "at least one scope required")),
		}, nil
	}
	scopes := make([]string, 0, len(body.Scopes))
	for _, sc := range body.Scopes {
		sv := string(sc)
		switch sv {
		case auth.ScopeRead, auth.ScopeWrite, auth.ScopeDelete:
			scopes = append(scopes, sv)
		default:
			return CreateApiToken400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse(problemBadRequest("Invalid scope", "only read/write/delete may be granted to tokens")),
			}, nil
		}
	}

	minted, err := auth.MintToken()
	if err != nil {
		return nil, fmt.Errorf("mint token: %w", err)
	}
	id := uuid.New()
	// The caller here is always a User (admin scope is session-only per
	// this ADR); attribute the token to them.
	stored, err := s.store.CreateAPIToken(ctx, APITokenInsert{
		ID:              id,
		Name:            body.Name,
		Prefix:          minted.Prefix,
		Hash:            minted.Hash,
		Scopes:          scopes,
		CreatedByUserID: caller.UserID,
		ExpiresAt:       body.ExpiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("createApiToken: %w", err)
	}

	// ApiTokenMint = ApiToken + `token` plaintext field. Returned once.
	resp := ApiTokenMint{
		Id:              stored.Id,
		Name:            stored.Name,
		Prefix:          stored.Prefix,
		Scopes:          stored.Scopes,
		CreatedByUserId: stored.CreatedByUserId,
		CreatedAt:       stored.CreatedAt,
		LastUsedAt:      stored.LastUsedAt,
		ExpiresAt:       stored.ExpiresAt,
		RevokedAt:       stored.RevokedAt,
		Token:           minted.Plaintext,
	}
	return CreateApiToken201JSONResponse(resp), nil
}

func (s *Server) RevokeApiToken(ctx context.Context, request RevokeApiTokenRequestObject) (RevokeApiTokenResponseObject, error) {
	if err := s.store.RevokeAPIToken(ctx, request.Id, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return RevokeApiToken404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("revokeApiToken: %w", err)
	}
	return RevokeApiToken204Response{}, nil
}

// ── /v1/admin/sessions ───────────────────────────────────────────────

func (s *Server) ListSessions(ctx context.Context, request ListSessionsRequestObject) (ListSessionsResponseObject, error) {
	limit, cursor := paging(request.Params.Limit, request.Params.Cursor)
	items, next, err := s.store.ListSessions(ctx, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("listSessions: %w", err)
	}
	resp := SessionList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListSessions200JSONResponse(resp), nil
}

func (s *Server) RevokeSession(ctx context.Context, request RevokeSessionRequestObject) (RevokeSessionResponseObject, error) {
	// Path parameter is OpenAPI-typed as `string, maxLength: 64` for
	// backwards-compat; parse as UUID here (that's the public_id form
	// ListSessions surfaces). Anything that doesn't parse cleanly
	// is a 400 — don't dignify cookie-value guesses with 404.
	publicID, err := uuid.Parse(request.Id)
	if err != nil {
		return RevokeSession404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
		}, nil
	}
	if err := s.store.DeleteSessionByPublicID(ctx, publicID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return RevokeSession404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse(problemNotFound()),
			}, nil
		}
		return nil, fmt.Errorf("revokeSession: %w", err)
	}
	return RevokeSession204Response{}, nil
}

// ── /v1/admin/audit ──────────────────────────────────────────────────

func (s *Server) ListAuditEvents(ctx context.Context, request ListAuditEventsRequestObject) (ListAuditEventsResponseObject, error) {
	limit, cursor := paging(request.Params.Limit, request.Params.Cursor)
	filter := AuditEventFilter{
		ActorID:      request.Params.ActorId,
		ResourceType: request.Params.ResourceType,
		ResourceID:   request.Params.ResourceId,
		Action:       request.Params.Action,
		Since:        request.Params.Since,
		Until:        request.Params.Until,
	}
	items, next, err := s.store.ListAuditEvents(ctx, filter, limit, cursor)
	if err != nil {
		return nil, fmt.Errorf("listAuditEvents: %w", err)
	}
	resp := AuditEventList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	return ListAuditEvents200JSONResponse(resp), nil
}

// ── helpers ──────────────────────────────────────────────────────────

const minPasswordLength = 12

// dummyHash is a pre-computed argon2id hash used by the login path to
// keep elapsed time constant when the username doesn't exist. Value
// chosen at package-init time so the parameters travel with it.
var dummyHash = mustHashDummy()

func mustHashDummy() string {
	h, err := auth.HashPassword("this-is-not-a-real-password-0123456789abcdef")
	if err != nil {
		// init-time failure means the crypto/rand source is broken;
		// argosd can't serve auth anyway, fail loudly on first login.
		return ""
	}
	return h
}

func paging(limit *Limit, cursor *Cursor) (int, string) {
	var l int
	var c string
	if limit != nil {
		l = *limit
	}
	if cursor != nil {
		c = *cursor
	}
	return l, c
}

// clientIP picks a best-effort source IP: X-Forwarded-For's first hop
// if present (reverse-proxy deployments), else r.RemoteAddr. Stored
// for admin session review; not used in any authz decision.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	// Strip port.
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i >= 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
