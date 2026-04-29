// Package httputil provides shared HTTP request introspection helpers
// that gate the trust placed in attacker-controllable proxy headers.
//
// ADR-0017 establishes that argosd reads X-Forwarded-For and
// X-Forwarded-Proto only when the immediate peer is in an explicit
// trusted-proxy CIDR list. The empty-list default ignores both headers
// unconditionally — that is the secure default the pentest report
// (AUTH-VULN-04) demands.
package httputil

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// ParseTrustedProxies parses a comma-separated CIDR list into IP
// networks. Empty input returns an empty slice and nil error. Any
// invalid CIDR aborts the parse and the offending value appears in the
// returned error.
func ParseTrustedProxies(csv string) ([]*net.IPNet, error) {
	if strings.TrimSpace(csv) == "" {
		return nil, nil
	}
	out := make([]*net.IPNet, 0)
	for _, raw := range strings.Split(csv, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		out = append(out, n)
	}
	return out, nil
}

// ClientIP returns the source IP for rate-limiting and audit logging.
//
// X-Forwarded-For is honored only when the immediate peer (r.RemoteAddr)
// matches an entry in the trusted list. The XFF walk runs right-to-left
// through trusted hops and returns the first untrusted IP — the real
// client. Attackers can prepend arbitrary values to XFF but cannot
// append past trusted hops, so prepended values are unreachable.
//
// With an empty trust list, XFF is ignored entirely. Returns nil only
// if r.RemoteAddr is unparseable.
func ClientIP(r *http.Request, trusted []*net.IPNet) net.IP {
	peer := remoteIP(r.RemoteAddr)
	if !inAny(peer, trusted) {
		return peer
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return peer
	}
	parts := strings.Split(xff, ",")
	// Right-to-left walk.
	var leftmost net.IP
	for i := len(parts) - 1; i >= 0; i-- {
		ip := net.ParseIP(strings.TrimSpace(parts[i]))
		if ip == nil {
			continue
		}
		leftmost = ip
		if !inAny(ip, trusted) {
			return ip
		}
	}
	if leftmost != nil {
		return leftmost
	}
	return peer
}

// IsHTTPS reports whether the request actually arrived over TLS — either
// natively (r.TLS != nil) or via a TLS-terminating peer in the trusted
// list that set X-Forwarded-Proto: https. With an empty trust list,
// X-Forwarded-Proto is ignored entirely.
func IsHTTPS(r *http.Request, trusted []*net.IPNet) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	peer := remoteIP(r.RemoteAddr)
	return inAny(peer, trusted)
}

// remoteIP strips the optional :port suffix from RemoteAddr and parses
// the result. Returns nil on any parse failure.
func remoteIP(remoteAddr string) net.IP {
	if remoteAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr without a port — try the raw value.
		host = remoteAddr
	}
	return net.ParseIP(host)
}

func inAny(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
