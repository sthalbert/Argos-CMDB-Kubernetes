package httputil

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{name: "empty string", input: "", wantCount: 0},
		{name: "single IPv4 CIDR", input: "10.0.0.0/8", wantCount: 1},
		{name: "single IPv6 CIDR", input: "fd00::/8", wantCount: 1},
		{name: "two CIDRs", input: "10.0.0.0/8,192.168.0.0/16", wantCount: 2},
		{name: "spaces tolerated", input: " 10.0.0.0/8 , 192.168.0.0/16 ", wantCount: 2},
		{name: "trailing comma ignored", input: "10.0.0.0/8,", wantCount: 1},
		{name: "double comma ignored", input: "10.0.0.0/8,,192.168.0.0/16", wantCount: 2},
		{name: "loopback v4", input: "127.0.0.1/32", wantCount: 1},
		{name: "loopback v6", input: "::1/128", wantCount: 1},
		{name: "invalid CIDR errors", input: "not-a-cidr", wantErr: true},
		{name: "missing prefix length errors", input: "10.0.0.0", wantErr: true},
		{name: "valid then invalid errors", input: "10.0.0.0/8,not-a-cidr", wantErr: true},
		{name: "out of range prefix errors", input: "10.0.0.0/33", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTrustedProxies(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil; result=%v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantCount {
				t.Fatalf("got %d nets, want %d", len(got), tc.wantCount)
			}
		})
	}
}

func TestParseTrustedProxiesErrorNamesOffender(t *testing.T) {
	_, err := ParseTrustedProxies("10.0.0.0/8,banana")
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "banana"; !contains(err.Error(), want) {
		t.Fatalf("error %q should mention %q", err.Error(), want)
	}
}

func TestClientIP_NoXFFReturnsPeer(t *testing.T) {
	r := newRequest("203.0.113.5:54321", nil, false)
	got := ClientIP(r, mustParse(t, ""))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_XFFIgnoredWithEmptyTrustList(t *testing.T) {
	// Pentest reproducer at the function level: an attacker connecting
	// directly with cycled XFF must not get a different ClientIP than
	// their actual peer address. With an empty trust list, XFF is
	// ignored unconditionally — that is the secure default.
	r := newRequest("203.0.113.5:54321", http.Header{"X-Forwarded-For": []string{"10.0.0.99"}}, false)
	got := ClientIP(r, mustParse(t, ""))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v (XFF must be ignored with empty trust list)", got, want)
	}
}

func TestClientIP_XFFIgnoredWhenPeerNotTrusted(t *testing.T) {
	// Attacker connects directly from 203.0.113.5 with XFF cycling.
	// Trust list contains only legitimate proxies, not the attacker.
	r := newRequest("203.0.113.5:54321", http.Header{"X-Forwarded-For": []string{"10.0.0.99"}}, false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v (XFF must be ignored when peer is untrusted)", got, want)
	}
}

func TestClientIP_TrustedPeerSingleHopXFF(t *testing.T) {
	// nginx (10.0.0.1) forwarded a request from real client 203.0.113.5.
	r := newRequest("10.0.0.1:443", http.Header{"X-Forwarded-For": []string{"203.0.113.5"}}, false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_TrustedPeerMultiHopXFF(t *testing.T) {
	// CDN -> proxyA -> proxyB -> argosd; both proxies trusted.
	// XFF: client, proxyA-internal-IP. Peer: proxyB.
	r := newRequest("10.0.0.2:443", http.Header{"X-Forwarded-For": []string{"203.0.113.5, 10.0.0.1"}}, false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_AttackerPrefixedXFFReturnsRealClient(t *testing.T) {
	// Attacker prepends arbitrary values to XFF before reaching nginx.
	// nginx (10.0.0.1, trusted) sees the attacker's bogus XFF, appends
	// the attacker's actual IP (203.0.113.5), and forwards to argosd.
	//
	// Argosd's right-to-left walk peels off 10.0.0.1 (trusted) and
	// returns 203.0.113.5 (the first untrusted hop from the right) —
	// the real client. Attacker's prepended "evil-fake" is unreachable.
	r := newRequest("10.0.0.1:443",
		http.Header{"X-Forwarded-For": []string{"evil-fake, 198.51.100.99, 203.0.113.5"}},
		false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v (right-to-left walk must return first untrusted hop)", got, want)
	}
}

func TestClientIP_AllXFFEntriesTrustedFallsBackToLeftmost(t *testing.T) {
	// Every hop in the chain is trusted infrastructure (e.g. an
	// internal-only test). Return the leftmost as a best-effort.
	r := newRequest("10.0.0.2:443", http.Header{"X-Forwarded-For": []string{"10.0.0.5, 10.0.0.1"}}, false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("10.0.0.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_MalformedXFFEntrySkipped(t *testing.T) {
	// nginx forwards a malformed XFF entry; we walk past it as if it
	// were trusted and return the first parseable untrusted hop.
	r := newRequest("10.0.0.1:443",
		http.Header{"X-Forwarded-For": []string{"203.0.113.5, not-an-ip"}},
		false)
	got := ClientIP(r, mustParse(t, "10.0.0.0/8"))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_IPv6Peer(t *testing.T) {
	r := newRequest("[fd00::1]:443", http.Header{"X-Forwarded-For": []string{"2001:db8::1"}}, false)
	got := ClientIP(r, mustParse(t, "fd00::/8"))
	if want := net.ParseIP("2001:db8::1"); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestClientIP_PortStripping(t *testing.T) {
	r := newRequest("203.0.113.5:54321", nil, false)
	got := ClientIP(r, mustParse(t, ""))
	if want := net.ParseIP("203.0.113.5"); !got.Equal(want) {
		t.Fatalf("got %v, want %v (port must be stripped)", got, want)
	}
}

func TestClientIP_LoopbackNotImplicitlyTrusted(t *testing.T) {
	// A request from loopback with XFF must NOT have its XFF honored
	// unless 127.0.0.1 is explicitly listed. ADR-0017 §2.
	r := newRequest("127.0.0.1:54321", http.Header{"X-Forwarded-For": []string{"203.0.113.5"}}, false)
	got := ClientIP(r, mustParse(t, ""))
	if want := net.ParseIP("127.0.0.1"); !got.Equal(want) {
		t.Fatalf("got %v, want %v (loopback must not be implicitly trusted)", got, want)
	}
}

func TestIsHTTPS_NativeTLSAlwaysTrue(t *testing.T) {
	r := newRequest("203.0.113.5:54321", nil, true)
	if !IsHTTPS(r, mustParse(t, "")) {
		t.Fatal("native TLS request must be HTTPS regardless of trust list")
	}
}

func TestIsHTTPS_PlainHTTPNoXFP(t *testing.T) {
	r := newRequest("203.0.113.5:54321", nil, false)
	if IsHTTPS(r, mustParse(t, "10.0.0.0/8")) {
		t.Fatal("plain HTTP without XFP must not be HTTPS")
	}
}

func TestIsHTTPS_UntrustedPeerXFPIgnored(t *testing.T) {
	// Attacker on the public internet sets XFP: https. Without trust,
	// IsHTTPS must return false — otherwise HSTS would emit on a
	// plain-HTTP response and the cookie's Secure flag would be set
	// over a non-TLS connection (browser drops the cookie => DoS).
	r := newRequest("203.0.113.5:54321",
		http.Header{"X-Forwarded-Proto": []string{"https"}}, false)
	if IsHTTPS(r, mustParse(t, "10.0.0.0/8")) {
		t.Fatal("XFP from untrusted peer must be ignored")
	}
}

func TestIsHTTPS_TrustedPeerXFPHttps(t *testing.T) {
	r := newRequest("10.0.0.1:443",
		http.Header{"X-Forwarded-Proto": []string{"https"}}, false)
	if !IsHTTPS(r, mustParse(t, "10.0.0.0/8")) {
		t.Fatal("trusted peer with XFP=https must be HTTPS")
	}
}

func TestIsHTTPS_TrustedPeerXFPHttp(t *testing.T) {
	r := newRequest("10.0.0.1:443",
		http.Header{"X-Forwarded-Proto": []string{"http"}}, false)
	if IsHTTPS(r, mustParse(t, "10.0.0.0/8")) {
		t.Fatal("trusted peer with XFP=http must not be HTTPS")
	}
}

func TestIsHTTPS_EmptyTrustListXFPIgnored(t *testing.T) {
	r := newRequest("10.0.0.1:443",
		http.Header{"X-Forwarded-Proto": []string{"https"}}, false)
	if IsHTTPS(r, mustParse(t, "")) {
		t.Fatal("XFP must be ignored with empty trust list")
	}
}

// ----- helpers -----

func newRequest(remote string, h http.Header, withTLS bool) *http.Request {
	r := &http.Request{
		RemoteAddr: remote,
		Header:     http.Header{},
	}
	for k, v := range h {
		r.Header[k] = v
	}
	if withTLS {
		r.TLS = &tls.ConnectionState{}
	}
	return r
}

func mustParse(t *testing.T, csv string) []*net.IPNet {
	t.Helper()
	got, err := ParseTrustedProxies(csv)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", csv, err)
	}
	return got
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
