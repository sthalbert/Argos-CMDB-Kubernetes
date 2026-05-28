package api

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LoginRateLimiter provides per-IP rate limiting for the login endpoint.
// Implements ADR-0007 IMP-009: 5 requests/minute per source IP.
type LoginRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rlEntry
}

type rlEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// NewLoginRateLimiter creates a rate limiter allowing 5 login attempts
// per minute per source IP with a burst of 5.
func NewLoginRateLimiter() *LoginRateLimiter {
	rl := &LoginRateLimiter{
		limiters: make(map[string]*rlEntry),
	}
	go rl.cleanup()
	return rl
}

// Allow returns true if the IP is within the rate limit.
func (rl *LoginRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	e, ok := rl.limiters[ip]
	if !ok {
		// 5 tokens per minute = 1 every 12 seconds, burst of 5.
		e = &rlEntry{lim: rate.NewLimiter(rate.Every(12*time.Second), 5)}
		rl.limiters[ip] = e
	}
	e.lastSeen = time.Now()
	rl.mu.Unlock()
	return e.lim.Allow()
}

// cleanup runs in a background goroutine and evicts IPs that haven't
// been seen in 30 minutes, preventing unbounded map growth.
func (rl *LoginRateLimiter) cleanup() {
	for {
		time.Sleep(10 * time.Minute) //nolint:mnd // cleanup interval is not a magic number worth extracting
		rl.mu.Lock()
		for ip, e := range rl.limiters {
			if time.Since(e.lastSeen) > 30*time.Minute { //nolint:mnd // eviction window
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Default per-IP limits for the ingest /v1/auth/verify endpoint (ADR-0016 §5).
// Generous enough that a healthy gateway never hits it, strict enough to make
// a buggy build that bypassed the verify cache visible immediately. The
// "source IP" in practice is always the gateway pod's IP since this listener
// is only reachable across one mTLS hop — but with many push-collectors
// fanning through a single gateway IP, operators may need to raise these via
// LONGUE_VUE_VERIFY_RATE_LIMIT_{RPS,BURST}.
const (
	DefaultVerifyRateLimitRPS   = 100
	DefaultVerifyRateLimitBurst = 200
)

// VerifyRateLimiter provides per-IP rate limiting for the ingest-listener
// /v1/auth/verify endpoint (ADR-0016 §5).
type VerifyRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rlEntry
	rps      rate.Limit
	burst    int
}

// NewVerifyRateLimiter creates a rate limiter with the default 100 rps /
// burst 200 per source IP. Kept for tests and call sites that don't need
// to tune the limits.
func NewVerifyRateLimiter() *VerifyRateLimiter {
	return NewVerifyRateLimiterWithLimits(DefaultVerifyRateLimitRPS, DefaultVerifyRateLimitBurst)
}

// NewVerifyRateLimiterWithLimits creates a rate limiter with the given
// per-IP rps and burst. Values <= 0 fall back to the defaults so a typo'd
// env var can't accidentally disable the limiter.
func NewVerifyRateLimiterWithLimits(rps float64, burst int) *VerifyRateLimiter {
	if rps <= 0 {
		rps = DefaultVerifyRateLimitRPS
	}
	if burst <= 0 {
		burst = DefaultVerifyRateLimitBurst
	}
	rl := &VerifyRateLimiter{
		limiters: make(map[string]*rlEntry),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	go rl.cleanup()
	return rl
}

// Allow returns true if the IP is within the rate limit.
func (rl *VerifyRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	e, ok := rl.limiters[ip]
	if !ok {
		e = &rlEntry{lim: rate.NewLimiter(rl.rps, rl.burst)}
		rl.limiters[ip] = e
	}
	e.lastSeen = time.Now()
	rl.mu.Unlock()
	return e.lim.Allow()
}

// cleanup mirrors LoginRateLimiter.cleanup — eviction window long enough
// that a quiet collector still has its bucket on the next tick.
func (rl *VerifyRateLimiter) cleanup() {
	for {
		time.Sleep(10 * time.Minute) //nolint:mnd // cleanup cadence
		rl.mu.Lock()
		for ip, e := range rl.limiters {
			if time.Since(e.lastSeen) > 30*time.Minute { //nolint:mnd // eviction window
				delete(rl.limiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}
