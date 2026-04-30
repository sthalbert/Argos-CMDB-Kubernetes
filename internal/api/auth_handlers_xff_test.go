package api

import (
	"net"
	"net/http"
	"testing"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// TestServerClientIP_PentestReproducer exercises the AUTH-VULN-04
// scenario at the unit level: an attacker connecting directly with cycled
// X-Forwarded-For values must NOT get a different ClientIP per request.
//
// With no trusted proxies configured (the secure default), the peer
// address (r.RemoteAddr) wins regardless of what XFF contains.
func TestServerClientIP_PentestReproducer(t *testing.T) {
	srv := NewServer("test", nil, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	// No trusted proxies set; this is the secure default — and what
	// every existing deployment ships as until LONGUE_VUE_TRUSTED_PROXIES is
	// configured.

	for _, fakeXFF := range []string{"10.0.0.99", "10.0.0.100", "203.0.113.7"} {
		r := &http.Request{
			RemoteAddr: "203.0.113.5:54321",
			Header:     http.Header{"X-Forwarded-For": []string{fakeXFF}},
		}
		got := srv.clientIP(r)
		if got != "203.0.113.5" {
			t.Fatalf("attacker XFF=%q: got %q, want 203.0.113.5 (peer)", fakeXFF, got)
		}
	}
}

func TestServerClientIP_TrustedPeerHonorsXFF(t *testing.T) {
	srv := NewServer("test", nil, auth.SecureNever, nil, NewLoginRateLimiter(), NewVerifyRateLimiter())
	_, cidr, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	srv.SetTrustedProxies([]*net.IPNet{cidr})

	r := &http.Request{
		RemoteAddr: "10.0.0.1:443",
		Header:     http.Header{"X-Forwarded-For": []string{"203.0.113.5"}},
	}
	if got := srv.clientIP(r); got != "203.0.113.5" {
		t.Fatalf("trusted peer XFF passthrough: got %q, want 203.0.113.5", got)
	}
}
