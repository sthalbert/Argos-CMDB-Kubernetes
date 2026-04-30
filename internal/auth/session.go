package auth

import (
	"net"
	"net/http"
	"time"

	"github.com/sthalbert/longue-vue/internal/httputil"
)

// SessionCookieName is the fixed cookie key both the API sets and the
// SPA (same-origin, browser-managed) sends back. Keep stable — changing
// it logs everyone out.
const SessionCookieName = "longue_vue_session"

// SessionDuration is the sliding expiry window per ADR-0007 (8 hours).
// Every authenticated request refreshes `last_used_at` and bumps
// `expires_at = last_used_at + SessionDuration`. Logout / revocation
// deletes the row server-side so the session stops working immediately.
const SessionDuration = 8 * time.Hour

// SecureCookiePolicy controls whether Set-Cookie carries the `Secure`
// flag. `SecureAuto` defers to the request shape gated through the
// trusted-proxy list (ADR-0017 §3): native TLS or X-Forwarded-Proto from
// a trusted peer flips Secure on; an attacker connecting directly with
// a spoofed XFP cannot. `SecureAlways` / `SecureNever` override either
// way — operators running fully behind TLS-terminating ingress should
// pin to `SecureAlways`.
type SecureCookiePolicy int

// SecureCookiePolicy values control the Secure flag on session cookies.
const (
	SecureAuto SecureCookiePolicy = iota
	SecureAlways
	SecureNever
)

// SessionCookie builds the session cookie value with the flags required
// by ADR-0007: HttpOnly, SameSite=Strict, Path=/. The Secure flag is
// derived from `policy` and — for SecureAuto — the request's TLS posture
// gated through `trustedProxies` (see ADR-0017). The caller is
// responsible for writing it to the response.
func SessionCookie(id string, expires time.Time, r *http.Request, policy SecureCookiePolicy, trustedProxies []*net.IPNet) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secureFlag(r, policy, trustedProxies),
	}
}

// SetSessionCookie writes the session cookie on the response with the
// flags required by ADR-0007: HttpOnly, SameSite=Strict, Path=/.
// The Secure flag is derived from `policy` and `trustedProxies`.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, id string, expires time.Time, policy SecureCookiePolicy, trustedProxies []*net.IPNet) {
	http.SetCookie(w, SessionCookie(id, expires, r, policy, trustedProxies))
}

// ClearSessionCookieValue builds a cookie that clears the session —
// empty value, MaxAge=-1 so the browser drops it. The caller writes
// it to the response.
func ClearSessionCookieValue(r *http.Request, policy SecureCookiePolicy, trustedProxies []*net.IPNet) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secureFlag(r, policy, trustedProxies),
	}
}

// ClearSessionCookie overwrites the cookie with an empty value and
// MaxAge=-1 so the browser drops it. Use on logout and whenever the
// session row is gone server-side.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, policy SecureCookiePolicy, trustedProxies []*net.IPNet) {
	http.SetCookie(w, ClearSessionCookieValue(r, policy, trustedProxies))
}

func secureFlag(r *http.Request, policy SecureCookiePolicy, trustedProxies []*net.IPNet) bool {
	switch policy {
	case SecureAlways:
		return true
	case SecureNever:
		return false
	default:
		// Auto: native TLS, or a TLS-terminating peer in the trust list
		// that set X-Forwarded-Proto: https. With an empty trust list,
		// X-Forwarded-Proto is ignored entirely (ADR-0017).
		return httputil.IsHTTPS(r, trustedProxies)
	}
}
