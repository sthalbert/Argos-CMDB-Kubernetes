package api

// Handlers for the /v1/auth/* and /v1/admin/* endpoints declared in
// openapi.yaml per ADR-0007. The substrate (password hashing, token
// minting, session cookies, the resolve-caller middleware) lives in
// internal/auth; this file just wires the HTTP shape onto the store.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// --- /v1/auth ------------------------------------------------------------

// Login validates username + password and issues a session cookie.
// 401 on any failure (no distinction between unknown user and bad
// password) to avoid username enumeration.
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var body LoginRequest
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Username == "" || body.Password == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "username and password are required")
		return
	}

	user, err := s.store.GetUserByUsername(r.Context(), body.Username)
	if err != nil {
		// Still run an argon2 verify against a dummy hash so timing
		// doesn't leak whether the username exists.
		_ = auth.VerifyPassword(body.Password, dummyHash)
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid credentials")
		return
	}
	if err := auth.VerifyPassword(body.Password, user.PasswordHash); err != nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "invalid credentials")
		return
	}

	// Mint session id + persist row + set cookie.
	sid, err := auth.RandomSecret(32)
	if err != nil {
		s.writeStoreError(w, "login", err)
		return
	}
	now := time.Now().UTC()
	expires := now.Add(auth.SessionDuration)
	if err := s.store.CreateSession(r.Context(), SessionInsert{
		ID:        sid,
		UserID:    *user.Id,
		CreatedAt: now,
		ExpiresAt: expires,
		UserAgent: r.UserAgent(),
		SourceIP:  clientIP(r),
	}); err != nil {
		s.writeStoreError(w, "login create session", err)
		return
	}
	_ = s.store.TouchUserLogin(r.Context(), *user.Id, now)

	auth.SetSessionCookie(w, r, sid, expires, s.cookiePolicy)
	w.WriteHeader(http.StatusNoContent)
}

// Logout deletes the session row (if any) and clears the cookie.
// Idempotent for bearer callers — nothing to revoke.
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller != nil && caller.Kind == auth.CallerKindUser && caller.SessionID != "" {
		if err := s.store.DeleteSession(r.Context(), caller.SessionID); err != nil &&
			!errors.Is(err, ErrNotFound) {
			s.writeStoreError(w, "logout", err)
			return
		}
	}
	auth.ClearSessionCookie(w, r, s.cookiePolicy)
	w.WriteHeader(http.StatusNoContent)
}

// GetMe returns identity + role + effective scopes for the current caller.
// The UI polls this on load to decide which nav items to render and
// whether to redirect to the forced-change page.
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "")
		return
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
	writeJSON(w, http.StatusOK, out)
}

// ChangePassword rotates the caller's password. Session-only — bearer
// tokens can't change a user's password.
func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || caller.Kind != auth.CallerKindUser {
		writeProblem(w, http.StatusForbidden, "Forbidden", "password change requires a human session")
		return
	}
	var body ChangePasswordRequest
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if len(body.NewPassword) < minPasswordLength {
		writeProblem(w, http.StatusBadRequest, "Invalid password",
			"new password must be at least 12 characters")
		return
	}

	// Re-fetch the user with its hash so we can verify the current password.
	// GetUserByUsername does the disabled check we want.
	withSecret, err := s.store.GetUserByUsername(r.Context(), caller.Username)
	if err != nil {
		s.writeStoreError(w, "change-password lookup", err)
		return
	}
	if err := auth.VerifyPassword(body.CurrentPassword, withSecret.PasswordHash); err != nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "current password does not match")
		return
	}

	hash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		s.writeStoreError(w, "change-password hash", err)
		return
	}
	if err := s.store.SetUserPassword(r.Context(), caller.UserID, hash, false); err != nil {
		s.writeStoreError(w, "change-password store", err)
		return
	}
	// SetUserPassword deletes every session for this user — including
	// ours. Clear the cookie so the browser drops it and the UI
	// redirects back to login.
	auth.ClearSessionCookie(w, r, s.cookiePolicy)
	w.WriteHeader(http.StatusNoContent)
}

// --- /v1/admin/users -----------------------------------------------------

func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request, params ListUsersParams) {
	limit, cursor := paging(params.Limit, params.Cursor)
	items, next, err := s.store.ListUsers(r.Context(), limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listUsers", err)
		return
	}
	resp := UserList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	var body UserCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Username == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "username is required")
		return
	}
	if len(body.Password) < minPasswordLength {
		writeProblem(w, http.StatusBadRequest, "Invalid password",
			"password must be at least 12 characters")
		return
	}
	if _, ok := auth.ValidRoles[string(body.Role)]; !ok {
		writeProblem(w, http.StatusBadRequest, "Invalid role",
			"role must be one of admin, editor, auditor, viewer")
		return
	}

	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.writeStoreError(w, "createUser hash", err)
		return
	}
	mustChange := true
	if body.MustChangePassword != nil {
		mustChange = *body.MustChangePassword
	}
	created, err := s.store.CreateUser(r.Context(), UserInsert{
		Username:           body.Username,
		PasswordHash:       hash,
		Role:               string(body.Role),
		MustChangePassword: mustChange,
	})
	if err != nil {
		s.writeStoreError(w, "createUser", err)
		return
	}
	if created.Id != nil {
		w.Header().Set("Location", "/v1/admin/users/"+created.Id.String())
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) GetUser(w http.ResponseWriter, r *http.Request, id UserId) {
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "getUser", err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) UpdateUser(w http.ResponseWriter, r *http.Request, id UserId) {
	var body UserUpdate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	patch := UserPatch{
		MustChangePassword: body.MustChangePassword,
		Disabled:           body.Disabled,
	}
	if body.Role != nil {
		r := string(*body.Role)
		if _, ok := auth.ValidRoles[r]; !ok {
			writeProblem(w, http.StatusBadRequest, "Invalid role",
				"role must be one of admin, editor, auditor, viewer")
			return
		}
		patch.Role = &r
	}
	// Password updates go through SetUserPassword so the hash stays out
	// of UserPatch (and so we can force must_change_password atomically).
	if body.Password != nil {
		if len(*body.Password) < minPasswordLength {
			writeProblem(w, http.StatusBadRequest, "Invalid password",
				"password must be at least 12 characters")
			return
		}
		hash, err := auth.HashPassword(*body.Password)
		if err != nil {
			s.writeStoreError(w, "updateUser hash", err)
			return
		}
		if err := s.store.SetUserPassword(r.Context(), id, hash, true); err != nil {
			s.writeStoreError(w, "updateUser password", err)
			return
		}
	}

	u, err := s.store.UpdateUser(r.Context(), id, patch)
	if err != nil {
		s.writeStoreError(w, "updateUser", err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) DeleteUser(w http.ResponseWriter, r *http.Request, id UserId) {
	// Guard against an admin deleting themselves mid-session —
	// re-admitting the break-glass admin path gets awkward. Requires
	// the caller to disable the target via UpdateUser first, then
	// delete from a separate admin session if desired.
	if caller := auth.CallerFromContext(r.Context()); caller != nil &&
		caller.Kind == auth.CallerKindUser && caller.UserID == id {
		writeProblem(w, http.StatusConflict, "Cannot delete self",
			"ask another admin to delete your account, or disable yourself via PATCH first")
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		s.writeStoreError(w, "deleteUser", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- /v1/admin/tokens ----------------------------------------------------

func (s *Server) ListApiTokens(w http.ResponseWriter, r *http.Request, params ListApiTokensParams) {
	limit, cursor := paging(params.Limit, params.Cursor)
	items, next, err := s.store.ListAPITokens(r.Context(), limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listApiTokens", err)
		return
	}
	resp := ApiTokenList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) CreateApiToken(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeProblem(w, http.StatusUnauthorized, "Unauthorized", "")
		return
	}

	var body ApiTokenCreate
	if err := decodeJSONBody(r, &body); err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeProblem(w, http.StatusBadRequest, "Missing field", "name is required")
		return
	}
	if len(body.Scopes) == 0 {
		writeProblem(w, http.StatusBadRequest, "Invalid scopes", "at least one scope required")
		return
	}
	scopes := make([]string, 0, len(body.Scopes))
	for _, sc := range body.Scopes {
		s := string(sc)
		switch s {
		case auth.ScopeRead, auth.ScopeWrite, auth.ScopeDelete:
			scopes = append(scopes, s)
		default:
			writeProblem(w, http.StatusBadRequest, "Invalid scope",
				"only read/write/delete may be granted to tokens")
			return
		}
	}

	minted, err := auth.MintToken()
	if err != nil {
		s.writeStoreError(w, "mint token", err)
		return
	}
	id := uuid.New()
	// The caller here is always a User (admin scope is session-only per
	// this ADR); attribute the token to them.
	stored, err := s.store.CreateAPIToken(r.Context(), APITokenInsert{
		ID:              id,
		Name:            body.Name,
		Prefix:          minted.Prefix,
		Hash:            minted.Hash,
		Scopes:          scopes,
		CreatedByUserID: caller.UserID,
		ExpiresAt:       body.ExpiresAt,
	})
	if err != nil {
		s.writeStoreError(w, "createApiToken", err)
		return
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
	if stored.Id != nil {
		w.Header().Set("Location", "/v1/admin/tokens/"+stored.Id.String())
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) RevokeApiToken(w http.ResponseWriter, r *http.Request, id TokenId) {
	if err := s.store.RevokeAPIToken(r.Context(), id, time.Now().UTC()); err != nil {
		s.writeStoreError(w, "revokeApiToken", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- /v1/admin/sessions --------------------------------------------------

func (s *Server) ListSessions(w http.ResponseWriter, r *http.Request, params ListSessionsParams) {
	limit, cursor := paging(params.Limit, params.Cursor)
	items, next, err := s.store.ListSessions(r.Context(), limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listSessions", err)
		return
	}
	resp := SessionList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) RevokeSession(w http.ResponseWriter, r *http.Request, id SessionId) {
	if err := s.store.DeleteSession(r.Context(), id); err != nil {
		s.writeStoreError(w, "revokeSession", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers -------------------------------------------------------------

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
