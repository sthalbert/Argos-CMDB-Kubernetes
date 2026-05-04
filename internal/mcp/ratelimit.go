package mcp

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter is a per-key token-bucket limiter. Keys are typically
// caller token IDs; pre-auth (when a key isn't yet known) the limiter
// can be skipped or keyed on source IP.
//
// The limiter map grows by one entry per distinct key seen — bound it
// at the call site by capping how many tokens you mint, or add a
// future LRU. For longue-vue's threat model (a few dozen long-lived
// PATs), unbounded growth is acceptable.
type RateLimiter struct {
	mu    sync.Mutex
	rps   rate.Limit
	burst int
	lim   map[string]*rate.Limiter
}

// NewRateLimiter returns a limiter with the given steady-state rate
// and burst.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		rps:   rate.Limit(rps),
		burst: burst,
		lim:   make(map[string]*rate.Limiter),
	}
}

// Allow returns true if the call should proceed. Returns true and
// records consumption when allowed; returns false (call denied) when
// the bucket is empty.
//
// Empty key is treated as a single shared bucket (rare — the auth
// path should always populate a token id).
func (r *RateLimiter) Allow(_ context.Context, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	lim, ok := r.lim[key]
	if !ok {
		lim = rate.NewLimiter(r.rps, r.burst)
		r.lim[key] = lim
	}

	return lim.Allow()
}
