package api

import (
	"net"
	"net/http"

	"github.com/sthalbert/longue-vue/internal/httputil"
)

// SecurityHeadersMiddleware sets security-related HTTP headers on every
// response.
//
// HSTS gating (ADR-0017): the header is emitted when the request actually
// arrived over TLS — either natively (r.TLS != nil) or via a TLS-terminating
// proxy whose peer address is in `trustedProxies`. With an empty trust list,
// X-Forwarded-Proto is ignored entirely so an attacker connecting directly
// can never spoof "https" and steer the trust posture of the response.
//
// `forceHSTS` reflects the operator's LONGUE_VUE_REQUIRE_HTTPS=true declaration
// that the deployment is HTTPS-only. When set, HSTS is emitted on every
// response regardless of the per-request shape — a browser that ever lands
// on the public hostname is told never to downgrade, even if a stray plain
// HTTP request slipped through.
func SecurityHeadersMiddleware(trustedProxies []*net.IPNet, forceHSTS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'")

			if forceHSTS || httputil.IsHTTPS(r, trustedProxies) {
				w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}
