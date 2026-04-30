package ingestgw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// stripIngressHeaders is the set of headers stripped on inbound requests
// before the gateway forwards them. Two reasons:
//
//  1. RFC 7230 hop-by-hop headers must not be forwarded.
//  2. X-Longue-Vue-Verified-* are reserved for argosd's own use; a malicious
//     collector that sneaks one of these in could trick a downstream
//     listener into trusting a forged caller identity. The ingest
//     listener doesn't actually trust those headers (it runs full
//     argon2id verification against the original Authorization
//     header), but defence in depth — strip them at the boundary.
//
// Outbound (gateway → argosd): only the original Authorization,
// X-Forwarded-For, and any custom collector headers below are forwarded.
//
//nolint:gochecknoglobals // immutable lookup tables
var (
	hopByHopHeaders = map[string]struct{}{
		"Connection":          {},
		"Keep-Alive":          {},
		"Proxy-Connection":    {},
		"Proxy-Authenticate":  {},
		"Proxy-Authorization": {},
		"Te":                  {},
		"Trailer":             {},
		"Transfer-Encoding":   {},
		"Upgrade":             {},
	}
	stripIngressHeaders = map[string]struct{}{
		"X-Real-Ip":               {},
		"X-Longue-Vue-Verified-Caller": {},
		"X-Longue-Vue-Verified-Scope":  {},
		"X-Longue-Vue-Verified-User":   {},
		// X-Forwarded-For is also stripped on ingress and replaced with
		// the gateway's connection peer below. argosd's clientIP() trusts
		// the leftmost XFF entry and writes it into audit_events.source_ip;
		// honouring an attacker-controlled XFF would forge the audit trail
		// (ANSSI SecNumCloud chapter-8 requires audit-log integrity, see
		// ADR-0008). Operators who need the real public-internet client IP
		// should read it from Envoy's access log — that's its job.
		"X-Forwarded-For": {},
	}
)

// proxyRequest forwards an allowed request to argosd over the configured
// mTLS upstream client. handles header strip, body forward, response
// streaming. errors map to:
//   - ctx.Err() — collector-cancelled; return 499 (or the connection
//     reset, since we can't write 499 reliably from this point).
//   - timeout — caller maps to 504, OutcomeUpstreamTimeout.
//   - any other err — caller maps to 503, OutcomeUpstreamError.
func (s *Server) proxyRequest( //nolint:gocyclo // central proxy dispatcher; flat is better than nested helpers
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	route string,
	bodyBuf []byte,
) (status int, err error) {
	upstreamURL, parseErr := buildUpstreamURL(s.upstreamBaseURL, r.URL.RequestURI())
	if parseErr != nil {
		return http.StatusBadGateway, fmt.Errorf("build upstream URL: %w", parseErr)
	}

	upReq, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, strings.NewReader(string(bodyBuf)))
	if err != nil {
		return http.StatusBadGateway, fmt.Errorf("build upstream request: %w", err)
	}
	copyForwardableHeaders(upReq.Header, r.Header)
	if s.upstreamHost != "" {
		upReq.Host = s.upstreamHost
	}
	// Set X-Forwarded-For to the gateway's connection peer ONLY — never
	// honour an inbound XFF (it's already stripped from the copied
	// headers above). The connection peer is Envoy in production or the
	// direct caller in dev. argosd's audit log records this trusted hop;
	// the chain back to the public-internet client lives in Envoy's
	// access log where it belongs. See H-1 in the ADR-0016 security
	// audit + ADR-0008 (audit-log integrity).
	if peer := remotePeerIP(r); peer != "" {
		upReq.Header.Set("X-Forwarded-For", peer)
	}

	upStart := time.Now()
	resp, err := s.upstream.Do(upReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return http.StatusGatewayTimeout, fmt.Errorf("upstream timeout: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return http.StatusServiceUnavailable, fmt.Errorf("upstream canceled: %w", err)
		}
		return http.StatusBadGateway, fmt.Errorf("upstream do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	observeUpstream(route, time.Since(upStart))

	// Mirror argosd's response back to the collector verbatim — same
	// status, same headers (minus hop-by-hop), same body. If the
	// collector sees a 401 it knows to refresh / fail; the gateway
	// also invalidates its cached entry for this token so the next
	// request re-verifies.
	for k, vv := range resp.Header {
		if _, hop := hopByHopHeaders[k]; hop {
			continue
		}
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		return resp.StatusCode, fmt.Errorf("stream response: %w", err)
	}
	return resp.StatusCode, nil
}

// copyForwardableHeaders copies inbound headers to the outbound request,
// stripping the hop-by-hop and gateway-internal sets above.
func copyForwardableHeaders(dst, src http.Header) {
	for k, vv := range src {
		canonical := http.CanonicalHeaderKey(k)
		if _, hop := hopByHopHeaders[canonical]; hop {
			continue
		}
		if _, strip := stripIngressHeaders[canonical]; strip {
			continue
		}
		// Strip any header listed in Connection per RFC 7230 §6.1.
		// Common case: "Connection: close" is already in hopByHop above.
		dst[canonical] = append(dst[canonical], vv...)
	}
}

// buildUpstreamURL composes the upstream URL by joining the configured
// base + the inbound request URI. The base must include scheme + host
// (and may include a non-empty path prefix); the inbound URI already
// includes any query string. Returned URL is fully formed.
func buildUpstreamURL(base, requestURI string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base: %w", err)
	}
	target, err := url.Parse(requestURI)
	if err != nil {
		return "", fmt.Errorf("parse request URI: %w", err)
	}
	// Merge: scheme/host from base, path prefix joined, query from
	// the inbound request. The inbound request URI is always
	// absolute-path-form (per RFC 7230 §5.3.1) so target.Scheme /
	// target.Host are empty here.
	u.Path = strings.TrimSuffix(u.Path, "/") + target.Path
	u.RawQuery = target.RawQuery
	return u.String(), nil
}

// clientIP returns a best-effort client IP for the structured request
// log. It prefers an inbound X-Forwarded-For (set by Envoy in production)
// for log readability, falling back to X-Real-Ip and finally the
// connection peer.
//
// IMPORTANT: this value is for LOGS ONLY. It is never used to build the
// outbound X-Forwarded-For header (proxyRequest uses remotePeerIP for
// that, which ignores attacker-controlled headers). See H-1 in the
// ADR-0016 security audit.
func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		// X-Forwarded-For may be a comma-separated list; the leftmost
		// entry is the original client.
		if comma := strings.IndexByte(h, ','); comma > 0 {
			return strings.TrimSpace(h[:comma])
		}
		return strings.TrimSpace(h)
	}
	if h := r.Header.Get("X-Real-Ip"); h != "" {
		return strings.TrimSpace(h)
	}
	return remotePeerIP(r)
}

// remotePeerIP returns the IP address of the connection peer, stripped
// of any port. Unlike clientIP, this IGNORES X-Forwarded-For and
// X-Real-Ip — it's the only function the proxy uses when forwarding
// requests so an attacker cannot forge the upstream-side source IP.
func remotePeerIP(r *http.Request) string {
	if r.RemoteAddr == "" {
		return ""
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
