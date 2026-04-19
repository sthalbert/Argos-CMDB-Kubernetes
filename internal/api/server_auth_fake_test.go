package api

// memStore auth-substrate fakes (users / sessions / tokens). Kept in a
// separate _test.go file so server_test.go doesn't grow unbounded.
// Semantics mirror the PG implementation closely enough that the same
// handler tests exercise either backend equivalently.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// Auth-substrate state, attached to memStore via an embedded field.
// Declared here so the additions don't bloat the big fake struct.
type memAuthState struct {
	users      map[uuid.UUID]User
	userHashes map[uuid.UUID]string
	userByName map[string]uuid.UUID // lowercase username -> id
	sessions   map[string]memSession
	tokens     map[uuid.UUID]memToken
	tokenByPrefix map[string]uuid.UUID
}

type memSession struct {
	id         string
	userID     uuid.UUID
	created    time.Time
	lastUsed   time.Time
	expires    time.Time
	userAgent  string
	sourceIP   string
}

type memToken struct {
	id              uuid.UUID
	name            string
	prefix          string
	hash            string
	scopes          []string
	createdBy       uuid.UUID
	createdAt       time.Time
	lastUsedAt      *time.Time
	expiresAt       *time.Time
	revokedAt       *time.Time
}

func newMemAuthState() memAuthState {
	return memAuthState{
		users:         make(map[uuid.UUID]User),
		userHashes:    make(map[uuid.UUID]string),
		userByName:    make(map[string]uuid.UUID),
		sessions:      make(map[string]memSession),
		tokens:        make(map[uuid.UUID]memToken),
		tokenByPrefix: make(map[string]uuid.UUID),
	}
}

// --- Store methods on memStore ------------------------------------------

func (m *memStore) CountActiveAdmins(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, u := range m.authState.users {
		if u.Role == Role(auth.RoleAdmin) && u.DisabledAt == nil {
			n++
		}
	}
	return n, nil
}

func (m *memStore) CreateUser(_ context.Context, in UserInsert) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(in.Username)
	if _, dup := m.authState.userByName[key]; dup {
		return User{}, fmt.Errorf("username already exists: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	mustChange := in.MustChangePassword
	u := User{
		Id:                 &id,
		Username:           in.Username,
		Role:               Role(in.Role),
		MustChangePassword: &mustChange,
		CreatedAt:          &now,
		UpdatedAt:          &now,
	}
	m.authState.users[id] = u
	m.authState.userHashes[id] = in.PasswordHash
	m.authState.userByName[key] = id
	return u, nil
}

func (m *memStore) GetUser(_ context.Context, id uuid.UUID) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *memStore) GetUserByUsername(_ context.Context, username string) (UserWithSecret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.authState.userByName[strings.ToLower(username)]
	if !ok {
		return UserWithSecret{}, ErrNotFound
	}
	u := m.authState.users[id]
	if u.DisabledAt != nil {
		return UserWithSecret{}, ErrNotFound
	}
	return UserWithSecret{User: u, PasswordHash: m.authState.userHashes[id]}, nil
}

func (m *memStore) ListUsers(_ context.Context, limit int, _ string) ([]User, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]User, 0, len(m.authState.users))
	for _, u := range m.authState.users {
		out = append(out, u)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateUser(_ context.Context, id uuid.UUID, in UserPatch) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	if in.Role != nil {
		u.Role = Role(*in.Role)
	}
	if in.MustChangePassword != nil {
		mc := *in.MustChangePassword
		u.MustChangePassword = &mc
	}
	if in.Disabled != nil {
		if *in.Disabled {
			now := time.Now().UTC()
			u.DisabledAt = &now
			// revoke sessions
			for sid, s := range m.authState.sessions {
				if s.userID == id {
					delete(m.authState.sessions, sid)
				}
			}
		} else {
			u.DisabledAt = nil
		}
	}
	now := time.Now().UTC()
	u.UpdatedAt = &now
	m.authState.users[id] = u
	return u, nil
}

func (m *memStore) SetUserPassword(_ context.Context, id uuid.UUID, hash string, mustChange bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return ErrNotFound
	}
	m.authState.userHashes[id] = hash
	mc := mustChange
	u.MustChangePassword = &mc
	now := time.Now().UTC()
	u.UpdatedAt = &now
	m.authState.users[id] = u
	// clear sessions
	for sid, s := range m.authState.sessions {
		if s.userID == id {
			delete(m.authState.sessions, sid)
		}
	}
	return nil
}

func (m *memStore) TouchUserLogin(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return ErrNotFound
	}
	ll := now
	u.LastLoginAt = &ll
	m.authState.users[id] = u
	return nil
}

func (m *memStore) DeleteUser(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return ErrNotFound
	}
	// Restrict-style FK: reject if the user minted any still-present tokens.
	for _, t := range m.authState.tokens {
		if t.createdBy == id {
			return fmt.Errorf("user owns api tokens: %w", ErrConflict)
		}
	}
	delete(m.authState.users, id)
	delete(m.authState.userHashes, id)
	delete(m.authState.userByName, strings.ToLower(u.Username))
	for sid, s := range m.authState.sessions {
		if s.userID == id {
			delete(m.authState.sessions, sid)
		}
	}
	return nil
}

func (m *memStore) CreateSession(_ context.Context, in SessionInsert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authState.sessions[in.ID] = memSession{
		id:        in.ID,
		userID:    in.UserID,
		created:   in.CreatedAt,
		lastUsed:  in.CreatedAt,
		expires:   in.ExpiresAt,
		userAgent: in.UserAgent,
		sourceIP:  in.SourceIP,
	}
	return nil
}

func (m *memStore) GetActiveSession(_ context.Context, id string) (auth.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.authState.sessions[id]
	if !ok {
		return auth.Session{}, auth.ErrUnauthorized
	}
	if time.Now().After(s.expires) {
		return auth.Session{}, auth.ErrUnauthorized
	}
	return auth.Session{ID: s.id, UserID: s.userID, ExpiresAt: s.expires}, nil
}

func (m *memStore) TouchSession(_ context.Context, id string, now, newExpiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.authState.sessions[id]
	if !ok {
		return ErrNotFound
	}
	s.lastUsed = now
	s.expires = newExpiry
	m.authState.sessions[id] = s
	return nil
}

func (m *memStore) GetUserForAuth(_ context.Context, id uuid.UUID) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.authState.users[id]
	if !ok {
		return auth.User{}, auth.ErrUnauthorized
	}
	mc := false
	if u.MustChangePassword != nil {
		mc = *u.MustChangePassword
	}
	return auth.User{
		ID:                 *u.Id,
		Username:           u.Username,
		Role:               string(u.Role),
		MustChangePassword: mc,
		Disabled:           u.DisabledAt != nil,
	}, nil
}

func (m *memStore) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.authState.sessions[id]; !ok {
		return ErrNotFound
	}
	delete(m.authState.sessions, id)
	return nil
}

func (m *memStore) DeleteSessionsForUser(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, s := range m.authState.sessions {
		if s.userID == userID {
			delete(m.authState.sessions, sid)
		}
	}
	return nil
}

func (m *memStore) ListSessions(_ context.Context, limit int, _ string) ([]Session, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Session, 0, len(m.authState.sessions))
	for _, s := range m.authState.sessions {
		if time.Now().After(s.expires) {
			continue
		}
		uaPtr := (*string)(nil)
		if s.userAgent != "" {
			ua := s.userAgent
			uaPtr = &ua
		}
		ipPtr := (*string)(nil)
		if s.sourceIP != "" {
			ip := s.sourceIP
			ipPtr = &ip
		}
		uid := s.userID
		u := m.authState.users[s.userID]
		masked := s.id
		if len(masked) > 8 {
			masked = masked[:8] + "…"
		}
		out = append(out, Session{
			Id:         masked,
			UserId:     uid,
			Username:   &u.Username,
			CreatedAt:  s.created,
			LastUsedAt: s.lastUsed,
			ExpiresAt:  s.expires,
			UserAgent:  uaPtr,
			SourceIp:   ipPtr,
		})
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) CreateAPIToken(_ context.Context, in APITokenInsert) (ApiToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.authState.tokenByPrefix[in.Prefix]; dup {
		return ApiToken{}, fmt.Errorf("token prefix collision: %w", ErrConflict)
	}
	now := time.Now().UTC()
	t := memToken{
		id:        in.ID,
		name:      in.Name,
		prefix:    in.Prefix,
		hash:      in.Hash,
		scopes:    append([]string(nil), in.Scopes...),
		createdBy: in.CreatedByUserID,
		createdAt: now,
		expiresAt: in.ExpiresAt,
	}
	m.authState.tokens[in.ID] = t
	m.authState.tokenByPrefix[in.Prefix] = in.ID

	return m.tokenToApi(t), nil
}

func (m *memStore) tokenToApi(t memToken) ApiToken {
	id := t.id
	createdBy := t.createdBy
	createdAt := t.createdAt
	prefix := t.prefix
	return ApiToken{
		Id:              &id,
		Name:            t.name,
		Prefix:          &prefix,
		Scopes:          append([]string(nil), t.scopes...),
		CreatedByUserId: &createdBy,
		CreatedAt:       &createdAt,
		LastUsedAt:      t.lastUsedAt,
		ExpiresAt:       t.expiresAt,
		RevokedAt:       t.revokedAt,
	}
}

func (m *memStore) GetActiveTokenByPrefix(_ context.Context, prefix string) (auth.APIToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.authState.tokenByPrefix[prefix]
	if !ok {
		return auth.APIToken{}, auth.ErrUnauthorized
	}
	t := m.authState.tokens[id]
	if t.revokedAt != nil {
		return auth.APIToken{}, auth.ErrUnauthorized
	}
	if t.expiresAt != nil && time.Now().After(*t.expiresAt) {
		return auth.APIToken{}, auth.ErrUnauthorized
	}
	return auth.APIToken{
		ID:              t.id,
		Name:            t.name,
		Hash:            t.hash,
		Scopes:          append([]string(nil), t.scopes...),
		CreatedByUserID: t.createdBy,
	}, nil
}

func (m *memStore) TouchToken(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.authState.tokens[id]
	if !ok {
		return ErrNotFound
	}
	ts := now
	t.lastUsedAt = &ts
	m.authState.tokens[id] = t
	return nil
}

func (m *memStore) ListAPITokens(_ context.Context, limit int, _ string) ([]ApiToken, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]ApiToken, 0, len(m.authState.tokens))
	for _, t := range m.authState.tokens {
		out = append(out, m.tokenToApi(t))
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) RevokeAPIToken(_ context.Context, id uuid.UUID, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.authState.tokens[id]
	if !ok {
		return ErrNotFound
	}
	if t.revokedAt == nil {
		ts := now
		t.revokedAt = &ts
		m.authState.tokens[id] = t
	}
	return nil
}
