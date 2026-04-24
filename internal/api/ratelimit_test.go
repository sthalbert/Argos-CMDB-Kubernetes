package api

import "testing"

func TestLoginRateLimiter(t *testing.T) {
	t.Parallel()
	lim := NewLoginRateLimiter()

	ip := "192.168.1.1"
	// First 5 should succeed (burst=5).
	for i := range 5 {
		if !lim.Allow(ip) {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	// 6th should be blocked.
	if lim.Allow(ip) {
		t.Fatal("6th attempt should be blocked")
	}
	// Different IP should still work.
	if !lim.Allow("10.0.0.1") {
		t.Fatal("different IP should be allowed")
	}
}
