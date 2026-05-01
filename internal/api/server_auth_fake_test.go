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

	"github.com/sthalbert/longue-vue/internal/auth"
)

// Auth-substrate state, attached to memStore via an embedded field.
// Declared here so the additions don't bloat the big fake struct.
type memAuthState struct {
	users         map[uuid.UUID]User
	userHashes    map[uuid.UUID]string
	userByName    map[string]uuid.UUID // lowercase username -> id
	sessions      map[string]memSession
	tokens        map[uuid.UUID]memToken
	tokenByPrefix map[string]uuid.UUID
	// OIDC substrate
	identities     map[string]uuid.UUID // "<issuer>\x00<subject>" -> user id
	oidcAuthStates map[string]memOidcState
	// Audit substrate
	auditEvents []AuditEvent
}

type memOidcState struct {
	codeVerifier string
	nonce        string
	expires      time.Time
}

type memSession struct {
	id        string
	publicID  uuid.UUID
	userID    uuid.UUID
	created   time.Time
	lastUsed  time.Time
	expires   time.Time
	userAgent string
	sourceIP  string
}

type memToken struct {
	id                  uuid.UUID
	name                string
	prefix              string
	hash                string
	scopes              []string
	createdBy           uuid.UUID
	createdAt           time.Time
	lastUsedAt          *time.Time
	expiresAt           *time.Time
	revokedAt           *time.Time
	boundCloudAccountID *uuid.UUID
}

func newMemAuthState() memAuthState {
	return memAuthState{
		users:          make(map[uuid.UUID]User),
		userHashes:     make(map[uuid.UUID]string),
		userByName:     make(map[string]uuid.UUID),
		sessions:       make(map[string]memSession),
		tokens:         make(map[uuid.UUID]memToken),
		tokenByPrefix:  make(map[string]uuid.UUID),
		identities:     make(map[string]uuid.UUID),
		oidcAuthStates: make(map[string]memOidcState),
	}
}

func identityKey(issuer, subject string) string { return issuer + "\x00" + subject }

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
	return m.updateUserLocked(id, in)
}

// updateUserLocked is the inner body of UpdateUser, callable while m.mu
// is already held by a higher-level transactional method.
func (m *memStore) updateUserLocked(id uuid.UUID, in UserPatch) (User, error) {
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
			for sid, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
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
	for sid, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
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
	return m.deleteUserLocked(id)
}

// deleteUserLocked is the inner body of DeleteUser, callable while m.mu
// is already held by a higher-level transactional method.
func (m *memStore) deleteUserLocked(id uuid.UUID) error {
	u, ok := m.authState.users[id]
	if !ok {
		return ErrNotFound
	}
	// Restrict-style FK: reject if the user minted any still-present tokens.
	for _, t := range m.authState.tokens { //nolint:gocritic // acceptable copy in test code
		if t.createdBy == id {
			return fmt.Errorf("user owns api tokens: %w", ErrConflict)
		}
	}
	delete(m.authState.users, id)
	delete(m.authState.userHashes, id)
	delete(m.authState.userByName, strings.ToLower(u.Username))
	for sid, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
		if s.userID == id {
			delete(m.authState.sessions, sid)
		}
	}
	return nil
}

// UpdateUserGuarded mirrors the PG implementation's transactional
// semantics: count + check + update happen under one mutex acquisition
// so two concurrent demotions cannot both observe `n=2` and commit.
//
//nolint:gocyclo // guarded count + write under one lock is inherently branchy
func (m *memStore) UpdateUserGuarded(_ context.Context, id uuid.UUID, in UserPatch) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	target, ok := m.authState.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	demoting := in.Role != nil && *in.Role != auth.RoleAdmin
	disabling := in.Disabled != nil && *in.Disabled
	if (demoting || disabling) && target.Role == Role(auth.RoleAdmin) && target.DisabledAt == nil {
		others := 0
		for otherID, u := range m.authState.users {
			if otherID == id {
				continue
			}
			if u.Role == Role(auth.RoleAdmin) && u.DisabledAt == nil {
				others++
			}
		}
		if others == 0 {
			return User{}, ErrLastAdmin
		}
	}
	return m.updateUserLocked(id, in)
}

// DeleteUserGuarded mirrors UpdateUserGuarded for the delete path.
func (m *memStore) DeleteUserGuarded(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	target, ok := m.authState.users[id]
	if !ok {
		return ErrNotFound
	}
	if target.Role == Role(auth.RoleAdmin) && target.DisabledAt == nil {
		others := 0
		for otherID, u := range m.authState.users {
			if otherID == id {
				continue
			}
			if u.Role == Role(auth.RoleAdmin) && u.DisabledAt == nil {
				others++
			}
		}
		if others == 0 {
			return ErrLastAdmin
		}
	}
	return m.deleteUserLocked(id)
}

func (m *memStore) CreateSession(_ context.Context, in SessionInsert) error { //nolint:gocritic // interface-mandated signature
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authState.sessions[in.ID] = memSession{
		id:        in.ID,
		publicID:  uuid.New(),
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

func (m *memStore) DeleteSessionByPublicID(_ context.Context, publicID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
		if s.publicID == publicID {
			delete(m.authState.sessions, id)
			return nil
		}
	}
	return ErrNotFound
}

func (m *memStore) DeleteSessionsForUser(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sid, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
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
	for _, s := range m.authState.sessions { //nolint:gocritic // acceptable copy in test code
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
		out = append(out, Session{
			Id:         s.publicID.String(),
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

func (m *memStore) CreateAPIToken(_ context.Context, in APITokenInsert) (ApiToken, error) { //nolint:gocritic // interface-mandated signature
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.authState.tokenByPrefix[in.Prefix]; dup {
		return ApiToken{}, fmt.Errorf("token prefix collision: %w", ErrConflict)
	}
	now := time.Now().UTC()
	t := memToken{
		id:                  in.ID,
		name:                in.Name,
		prefix:              in.Prefix,
		hash:                in.Hash,
		scopes:              append([]string(nil), in.Scopes...),
		createdBy:           in.CreatedByUserID,
		createdAt:           now,
		expiresAt:           in.ExpiresAt,
		boundCloudAccountID: in.BoundCloudAccountID,
	}
	m.authState.tokens[in.ID] = t
	m.authState.tokenByPrefix[in.Prefix] = in.ID

	return m.tokenToApi(t), nil
}

func (m *memStore) tokenToApi(t memToken) ApiToken { //nolint:gocritic // value semantics intentional for test helper
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
		ID:                  t.id,
		Name:                t.name,
		Hash:                t.hash,
		Scopes:              append([]string(nil), t.scopes...),
		CreatedByUserID:     t.createdBy,
		BoundCloudAccountID: t.boundCloudAccountID,
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
	for _, t := range m.authState.tokens { //nolint:gocritic // acceptable copy in test code
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

func (m *memStore) GetUserByIdentity(_ context.Context, issuer, subject string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.authState.identities[identityKey(issuer, subject)]
	if !ok {
		return User{}, ErrNotFound
	}
	u := m.authState.users[id]
	if u.DisabledAt != nil {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *memStore) CreateUserWithIdentity(_ context.Context, in UserInsert, ident UserIdentityInsert) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToLower(in.Username)
	if _, dup := m.authState.userByName[key]; dup {
		return User{}, fmt.Errorf("username already exists: %w", ErrConflict)
	}
	ik := identityKey(ident.Issuer, ident.Subject)
	if _, dup := m.authState.identities[ik]; dup {
		return User{}, fmt.Errorf("identity already exists: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	mc := in.MustChangePassword
	u := User{
		Id:                 &id,
		Username:           in.Username,
		Role:               Role(in.Role),
		MustChangePassword: &mc,
		CreatedAt:          &now,
		UpdatedAt:          &now,
	}
	m.authState.users[id] = u
	m.authState.userHashes[id] = in.PasswordHash
	m.authState.userByName[key] = id
	m.authState.identities[ik] = id
	return u, nil
}

func (m *memStore) TouchUserIdentity(_ context.Context, _ uuid.UUID, _, _ string, _ time.Time) error {
	// No-op in the fake; the test surface doesn't assert on last_seen_at.
	return nil
}

func (m *memStore) CreateOidcAuthState(_ context.Context, in OidcAuthStateInsert) error { //nolint:gocritic // interface-mandated signature
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authState.oidcAuthStates[in.State] = memOidcState{
		codeVerifier: in.CodeVerifier,
		nonce:        in.Nonce,
		expires:      in.ExpiresAt,
	}
	return nil
}

func (m *memStore) ConsumeOidcAuthState(_ context.Context, state string) (codeVerifier, nonce string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.authState.oidcAuthStates[state]
	if !ok || time.Now().After(s.expires) {
		delete(m.authState.oidcAuthStates, state)
		return "", "", ErrNotFound
	}
	delete(m.authState.oidcAuthStates, state)
	return s.codeVerifier, s.nonce, nil
}

func (m *memStore) InsertAuditEvent(_ context.Context, in AuditEventInsert) error { //nolint:gocritic // interface-mandated signature
	m.mu.Lock()
	defer m.mu.Unlock()
	ev := AuditEvent{
		Id:         in.ID,
		OccurredAt: in.OccurredAt,
		ActorId:    in.ActorID,
		ActorKind:  AuditEventActorKind(in.ActorKind),
		Action:     in.Action,
		HttpMethod: in.HTTPMethod,
		HttpPath:   in.HTTPPath,
		HttpStatus: in.HTTPStatus,
	}
	if in.ActorUsername != "" {
		v := in.ActorUsername
		ev.ActorUsername = &v
	}
	if in.ActorRole != "" {
		v := in.ActorRole
		ev.ActorRole = &v
	}
	if in.ResourceType != "" {
		v := in.ResourceType
		ev.ResourceType = &v
	}
	if in.ResourceID != "" {
		v := in.ResourceID
		ev.ResourceId = &v
	}
	if in.SourceIP != "" {
		v := in.SourceIP
		ev.SourceIp = &v
	}
	if in.UserAgent != "" {
		v := in.UserAgent
		ev.UserAgent = &v
	}
	if in.Details != nil {
		d := in.Details
		ev.Details = &d
	}
	m.authState.auditEvents = append(m.authState.auditEvents, ev)
	return nil
}

func (m *memStore) GetSettings(_ context.Context) (Settings, error) {
	return Settings{EOLEnabled: false}, nil
}

func (m *memStore) UpdateSettings(_ context.Context, _ SettingsPatch) (Settings, error) {
	return Settings{EOLEnabled: false}, nil
}

//nolint:gocyclo // multi-filter test fake
func (m *memStore) ListAuditEvents(
	_ context.Context, filter AuditEventFilter, limit int, _ string,
) ([]AuditEvent, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]AuditEvent, 0, len(m.authState.auditEvents))
	for i := len(m.authState.auditEvents) - 1; i >= 0; i-- {
		ev := m.authState.auditEvents[i]
		if filter.ActorID != nil && (ev.ActorId == nil || *ev.ActorId != *filter.ActorID) {
			continue
		}
		if filter.ResourceType != nil && (ev.ResourceType == nil || *ev.ResourceType != *filter.ResourceType) {
			continue
		}
		if filter.ResourceID != nil && (ev.ResourceId == nil || *ev.ResourceId != *filter.ResourceID) {
			continue
		}
		if filter.Action != nil && ev.Action != *filter.Action {
			continue
		}
		if filter.Since != nil && ev.OccurredAt.Before(*filter.Since) {
			continue
		}
		if filter.Until != nil && !ev.OccurredAt.Before(*filter.Until) {
			continue
		}
		out = append(out, ev)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}
