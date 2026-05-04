package mcp

import (
	"container/list"
	"crypto/subtle"
	"sync"
	"time"
)

// AuthCache is a bounded LRU cache of verified bearer tokens, keyed on
// the 8-char prefix. It exists to amortise argon2id verification cost
// (~100-500ms, 64 MiB) across a typical AI tool-call burst.
//
// All methods are safe for concurrent use.
type AuthCache struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	items map[string]*authCacheEntry
	lru   *list.List
}

type authCacheEntry struct {
	prefix     string
	fullToken  string
	caller     *MCPCaller
	validUntil time.Time
	elem       *list.Element
}

// NewAuthCache creates a new bounded LRU auth cache.
func NewAuthCache(capacity int, ttl time.Duration) *AuthCache {
	return &AuthCache{
		cap:   capacity,
		ttl:   ttl,
		items: make(map[string]*authCacheEntry, capacity),
		lru:   list.New(),
	}
}

// Get returns (caller, true) when the prefix is cached, the full token
// matches in constant time, AND the entry is not expired. Hits are
// promoted to the front of the LRU.
func (c *AuthCache) Get(prefix, full string) (*MCPCaller, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[prefix]
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.validUntil) {
		// Expired — evict eagerly.
		c.lru.Remove(entry.elem)
		delete(c.items, prefix)
		return nil, false
	}
	// Lengths can differ — return false immediately; prefix already
	// disambiguates, so constant-time comparison on different lengths is
	// unnecessary.
	if len(entry.fullToken) != len(full) {
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(entry.fullToken), []byte(full)) != 1 {
		return nil, false
	}
	// Promote to front (most recently used).
	c.lru.MoveToFront(entry.elem)
	return entry.caller, true
}

// Put inserts or refreshes an entry. Evicts the oldest entry when the
// cap is exceeded.
func (c *AuthCache) Put(prefix, full string, caller *MCPCaller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[prefix]; ok {
		// Refresh existing entry.
		entry.fullToken = full
		entry.caller = caller
		entry.validUntil = time.Now().Add(c.ttl)
		c.lru.MoveToFront(entry.elem)
		return
	}
	// Evict LRU if at capacity.
	if len(c.items) >= c.cap {
		oldest := c.lru.Back()
		if oldest != nil {
			e := oldest.Value.(*authCacheEntry)
			c.lru.Remove(oldest)
			delete(c.items, e.prefix)
		}
	}
	entry := &authCacheEntry{
		prefix:     prefix,
		fullToken:  full,
		caller:     caller,
		validUntil: time.Now().Add(c.ttl),
	}
	entry.elem = c.lru.PushFront(entry)
	c.items[prefix] = entry
}

// Invalidate removes the entry for a given prefix. No-op if absent.
func (c *AuthCache) Invalidate(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.items[prefix]; ok {
		c.lru.Remove(entry.elem)
		delete(c.items, prefix)
	}
}

// Len reports the current entry count (test-only).
func (c *AuthCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}
