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
