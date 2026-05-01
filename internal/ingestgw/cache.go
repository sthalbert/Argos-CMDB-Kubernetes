package ingestgw

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// CachedToken holds a decoded verify result kept in the gateway's
// short-lived in-memory cache. Set Valid=false for a negative entry.
type CachedToken struct {
	Valid               bool
	CallerID            string
	TokenName           string
	Scopes              []string
	BoundCloudAccountID string
	// ExpiresAt is the cache-entry expiry, computed from
	// min(now+TTL, token's own exp). Zero on negative entries (they
	// use NegativeTTL fixed from CacheConfig).
	ExpiresAt time.Time
}

// HasScope returns true when the caller carries want. Mirrors auth.Caller's
// admin-implies-write convention so the gateway can short-circuit obvious
// scope mismatches without calling longue-vue. For the ingest gateway use case
// the only scope that matters in practice is "write" (the K8s collector's
// declared scope), so the implication rule is just "admin implies write".
//
// Importantly, admin does NOT imply vm-collector here — exactly mirrors
// the longue-vue-side auth.HasScope rule from ADR-0015 §5.
func (c *CachedToken) HasScope(want string) bool {
	for _, s := range c.Scopes {
		if s == want {
			return true
		}
		if s == "admin" && want != "vm-collector" {
			return true
		}
	}
	return false
}

// CacheConfig sets the gateway cache's TTLs and bounds.
type CacheConfig struct {
	// MaxEntries caps the LRU size. Reached → least-recently-used
	// entry is evicted before insert.
	MaxEntries int

	// PositiveTTL bounds how long a successful verify result is
	// trusted by the gateway. 60 s is the documented SLA from
	// ADR-0016 §5; revoke-then-wait-PositiveTTL = guaranteed effective
	// at the gateway.
	PositiveTTL time.Duration

	// NegativeTTL bounds how long an invalid-token verify result is
	// cached. 10 s by default — short enough that a freshly-issued
	// token is not denied by a stale negative entry, long enough to
	// absorb scanner / brute-force traffic.
	NegativeTTL time.Duration
}

// DefaultCacheConfig returns the cache settings recommended by ADR-0016.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		MaxEntries:  10000,
		PositiveTTL: 60 * time.Second,
		NegativeTTL: 10 * time.Second,
	}
}

// Cache is a bounded LRU mapping sha256(token) → CachedToken with
// per-entry TTLs. Safe for concurrent use.
//
// The cache is keyed on a SHA-256 digest of the full token bytes — not
// the 8-character prefix used elsewhere — which prevents any
// hypothetical attack that exploits a colliding prefix to poison
// another token's entry.
type Cache struct {
	cfg CacheConfig
	mu  sync.Mutex
	// entries maps the hex-encoded SHA-256 of the token to a list
	// element pointing at its (key, value, expiry) record. The list
	// is ordered most-recently-used to least-recently-used; eviction
	// pops from the back.
	entries map[string]*list.Element
	lru     *list.List
	// inflight de-dupes concurrent verify calls for the same token.
	// First caller acquires the channel, subsequent callers wait on
	// it — single longue-vue round-trip even under thundering-herd.
	inflight map[string]chan struct{}
	// now overrides time.Now() in tests. Production code leaves it nil.
	now func() time.Time
}

type cacheEntry struct {
	key string
	val CachedToken
}

// NewCache constructs a Cache with the given config. cfg.MaxEntries must
// be positive; the rest fall back to DefaultCacheConfig values when
// zero, so a partially-set CacheConfig still produces a usable cache.
func NewCache(cfg CacheConfig) *Cache {
	defaults := DefaultCacheConfig()
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaults.MaxEntries
	}
	if cfg.PositiveTTL <= 0 {
		cfg.PositiveTTL = defaults.PositiveTTL
	}
	if cfg.NegativeTTL <= 0 {
		cfg.NegativeTTL = defaults.NegativeTTL
	}
	return &Cache{
		cfg:      cfg,
		entries:  make(map[string]*list.Element, cfg.MaxEntries),
		lru:      list.New(),
		inflight: make(map[string]chan struct{}),
	}
}

// keyOf hashes a raw token to its cache key. Exposed at package level so
// tests can assert key derivation without reaching into Cache internals.
func keyOf(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Get looks up a cached entry by token. Returns the entry and ok=true
// when present and unexpired; ok=false otherwise. A returned entry with
// Valid=false is a cached negative result — callers should reject
// without calling the verify endpoint.
func (c *Cache) Get(token string) (CachedToken, bool) {
	key := keyOf(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		observeCache("miss")
		return CachedToken{}, false
	}
	entry := elem.Value.(*cacheEntry) //nolint:errcheck // type stable by construction
	if c.clock().After(entry.val.ExpiresAt) {
		// Lazy expiry: drop on read.
		c.lru.Remove(elem)
		delete(c.entries, key)
		setCacheSize(len(c.entries))
		observeCache("miss")
		return CachedToken{}, false
	}
	c.lru.MoveToFront(elem)
	if entry.val.Valid {
		observeCache("hit")
	} else {
		observeCache("negative_hit")
	}
	return entry.val, true
}

// PutValid caches a successful verify result. tokenExp is the token's
// own expiry (zero when the token does not expire); the cache entry is
// kept until min(now+PositiveTTL, tokenExp), so a soon-to-expire token
// never gets a longer cache life than its remaining validity.
//
//nolint:gocritic // hugeParam: CachedToken is 104 bytes; pass-by-value is intentional — callers construct inline literals
func (c *Cache) PutValid(
	token string,
	val CachedToken,
	tokenExp time.Time,
) {
	now := c.clock()
	exp := now.Add(c.cfg.PositiveTTL)
	if !tokenExp.IsZero() && tokenExp.Before(exp) {
		exp = tokenExp
	}
	val.Valid = true
	val.ExpiresAt = exp
	c.put(token, val)
}

// PutNegative caches a verify-denied result. The entry expires after
// NegativeTTL — short by design so a freshly-issued token isn't blocked
// by a stale negative.
func (c *Cache) PutNegative(token string) {
	c.put(token, CachedToken{
		Valid:     false,
		ExpiresAt: c.clock().Add(c.cfg.NegativeTTL),
	})
}

// Invalidate drops the entry for token (no-op if absent). Called when an
// upstream forwarded request returns 401 — longue-vue has revoked between
// the cache hit and now, so the next request must re-verify.
func (c *Cache) Invalidate(token string) {
	key := keyOf(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.lru.Remove(elem)
		delete(c.entries, key)
		setCacheSize(len(c.entries))
	}
}

// Len returns the current number of entries (positive + negative).
// Exposed for tests; production code reads the gauge metric instead.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// SingleflightGet wraps Get with thundering-herd protection: at most one
// in-flight verify call per (sha256(token)) is allowed; subsequent
// callers wait on the first one's completion and re-read the cache.
//
// loadFn is called when the cache is cold and no in-flight call is
// already underway. It receives the same context the caller passed; on
// completion the result is stored in the cache (positive or negative)
// before notifying waiters.
//
// When a predecessor's loadFn errors without caching, waiting goroutines
// loop and try to claim the in-flight slot themselves. The mutex acquire
// and release are factored into tiny helpers so the body never juggles
// "do I currently hold the lock?" — a previous version got that wrong
// and could double-unlock under upstream-failure fall-through.
func (c *Cache) SingleflightGet(
	ctx context.Context,
	token string,
	loadFn func(context.Context) (CachedToken, time.Time, error),
) (CachedToken, error) {
	key := keyOf(token)
	for {
		if entry, ok := c.Get(token); ok {
			return entry, nil
		}
		ch, owner := c.acquireOrJoinInflight(key)
		if owner {
			return c.runLoader(ctx, token, key, ch, loadFn)
		}
		observeCache("inflight_dedupe")
		select {
		case <-ctx.Done():
			return CachedToken{}, fmt.Errorf("singleflight wait: %w", ctx.Err())
		case <-ch:
		}
		// Predecessor released the slot. Loop and either find the
		// cached result on the next Get or claim the slot ourselves.
	}
}

// acquireOrJoinInflight returns the in-flight channel for key. owner=true
// means the caller has just claimed the slot and is responsible for
// running the loader and releasing the slot via releaseInflight; owner=false
// means another goroutine is already running the loader and the caller
// should wait on the returned channel.
func (c *Cache) acquireOrJoinInflight(key string) (ch chan struct{}, owner bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, busy := c.inflight[key]; busy {
		return existing, false
	}
	ch = make(chan struct{})
	c.inflight[key] = ch
	return ch, true
}

// releaseInflight removes the in-flight slot for key and notifies any
// waiters. Called from the deferred cleanup of runLoader.
func (c *Cache) releaseInflight(key string, ch chan struct{}) {
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(ch)
}

// runLoader executes loadFn and stores the result in the cache. The
// caller MUST already hold the in-flight slot for key (via
// acquireOrJoinInflight returning owner=true). On any return path the
// in-flight slot is released and waiters are notified.
func (c *Cache) runLoader(
	ctx context.Context,
	token, key string,
	ch chan struct{},
	loadFn func(context.Context) (CachedToken, time.Time, error),
) (CachedToken, error) {
	defer c.releaseInflight(key, ch)
	val, tokenExp, err := loadFn(ctx)
	if err != nil {
		// Don't cache transport errors — let the next request retry.
		// Waiters wake up and loop back into the cache lookup.
		return CachedToken{}, err
	}
	if val.Valid {
		c.PutValid(token, val, tokenExp)
	} else {
		c.PutNegative(token)
	}
	return val, nil
}

// clock returns the current time, or a test-injected override when set.
func (c *Cache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// put inserts a cache entry under the LRU contract. Must NOT be called
// with c.mu held (it acquires it itself).
//
//nolint:gocritic // hugeParam: CachedToken is 104 bytes; pass-by-value is intentional — called from PutValid/PutNegative with inline literals
func (c *Cache) put(token string, val CachedToken) {
	key := keyOf(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		// Update in place + bump to MRU.
		entry := elem.Value.(*cacheEntry) //nolint:errcheck // type stable by construction
		entry.val = val
		c.lru.MoveToFront(elem)
		return
	}
	// Evict if at capacity.
	for len(c.entries) >= c.cfg.MaxEntries {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		evicted := oldest.Value.(*cacheEntry) //nolint:errcheck // type stable
		c.lru.Remove(oldest)
		delete(c.entries, evicted.key)
		observeCache("evict")
	}
	elem := c.lru.PushFront(&cacheEntry{key: key, val: val})
	c.entries[key] = elem
	setCacheSize(len(c.entries))
}
