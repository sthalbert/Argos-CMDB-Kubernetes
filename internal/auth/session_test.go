package auth

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
	"time"
)

// AUTH-VULN-04 reproducer at the cookie layer: an attacker connecting
// directly with X-Forwarded-Proto: https must NOT cause SecureAuto to
// emit Secure=true. Only TLS or a TLS-terminating peer in the trust
// list should flip the bit.
func TestSecureFlag_AutoIgnoresXFPWithoutTrust(t *testing.T) {
	t.Parallel()

	r := &http.Request{
		RemoteAddr: "203.0.113.5:54321",
		Header:     http.Header{"X-Forwarded-Proto": []string{"https"}},
	}
	if got := secureFlag(r, SecureAuto, nil); got {
		t.Errorf("secureFlag(SecureAuto, nil trust) = true; want false (XFP must be ignored)")
	}
}

func TestSecureFlag_AutoHonorsXFPFromTrustedPeer(t *testing.T) {
	t.Parallel()

	_, cidr, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{
		RemoteAddr: "10.0.0.1:443",
		Header:     http.Header{"X-Forwarded-Proto": []string{"https"}},
	}
	if got := secureFlag(r, SecureAuto, []*net.IPNet{cidr}); !got {
		t.Errorf("secureFlag(SecureAuto, trusted peer) = false; want true")
	}
}

func TestSecureFlag_AutoIgnoresXFPFromUntrustedPeer(t *testing.T) {
	t.Parallel()

	_, cidr, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{
		RemoteAddr: "203.0.113.5:443",
		Header:     http.Header{"X-Forwarded-Proto": []string{"https"}},
	}
	if got := secureFlag(r, SecureAuto, []*net.IPNet{cidr}); got {
		t.Errorf("secureFlag(SecureAuto, untrusted peer) = true; want false")
	}
}

func TestSecureFlag_AutoHonorsNativeTLS(t *testing.T) {
	t.Parallel()

	r := &http.Request{
		RemoteAddr: "203.0.113.5:54321",
		TLS:        &tls.ConnectionState{},
	}
	if got := secureFlag(r, SecureAuto, nil); !got {
		t.Errorf("secureFlag(SecureAuto, native TLS) = false; want true")
	}
}

func TestSecureFlag_AlwaysOverrides(t *testing.T) {
	t.Parallel()

	r := &http.Request{RemoteAddr: "203.0.113.5:54321"}
	if got := secureFlag(r, SecureAlways, nil); !got {
		t.Errorf("secureFlag(SecureAlways) = false; want true")
	}
}

func TestSecureFlag_NeverOverrides(t *testing.T) {
	t.Parallel()

	r := &http.Request{
		RemoteAddr: "10.0.0.1:443",
		TLS:        &tls.ConnectionState{},
	}
	if got := secureFlag(r, SecureNever, nil); got {
		t.Errorf("secureFlag(SecureNever, native TLS) = true; want false")
	}
}

// SessionCookie wires policy → secureFlag and must propagate the flag.
func TestSessionCookie_SecureFlagFromTrustList(t *testing.T) {
	t.Parallel()

	_, cidr, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{
		RemoteAddr: "10.0.0.1:443",
		Header:     http.Header{"X-Forwarded-Proto": []string{"https"}},
	}
	c := SessionCookie("sid", time.Now().Add(time.Hour), r, SecureAuto, []*net.IPNet{cidr})
	if !c.Secure {
		t.Error("SessionCookie did not propagate Secure=true from trusted-peer XFP")
	}
}
