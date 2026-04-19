package auth

import (
	"net/http"
	"time"
)

// SessionCookieName is the fixed cookie key both the API sets and the
// SPA (same-origin, browser-managed) sends back. Keep stable — changing
// it logs everyone out.
const SessionCookieName = "argos_session"

// SessionDuration is the sliding expiry window per ADR-0007 (8 hours).
// Every authenticated request refreshes `last_used_at` and bumps
// `expires_at = last_used_at + SessionDuration`. Logout / revocation
// deletes the row server-side so the session stops working immediately.
const SessionDuration = 8 * time.Hour

// SecureCookiePolicy controls whether Set-Cookie carries the `Secure`
// flag. `SecureAuto` defers to the request: HTTPS → secure, HTTP → not
// (so dev against :8080 still works); `SecureAlways` / `SecureNever`
// override either way.
type SecureCookiePolicy int

const (
	SecureAuto SecureCookiePolicy = iota
	SecureAlways
	SecureNever
)

// SetSessionCookie writes the session cookie on the response with the
// flags required by ADR-0007: HttpOnly, SameSite=Strict, Path=/.
// The Secure flag depends on `policy`.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, id string, expires time.Time, policy SecureCookiePolicy) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secureFlag(r, policy),
	})
}

// ClearSessionCookie overwrites the cookie with an empty value and
// MaxAge=-1 so the browser drops it. Use on logout and whenever the
// session row is gone server-side.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request, policy SecureCookiePolicy) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secureFlag(r, policy),
	})
}

func secureFlag(r *http.Request, policy SecureCookiePolicy) bool {
	switch policy {
	case SecureAlways:
		return true
	case SecureNever:
		return false
	default:
		// Auto: if the incoming request arrived over TLS (or a proxy
		// claims it did via X-Forwarded-Proto), set Secure.
		if r.TLS != nil {
			return true
		}
		return r.Header.Get("X-Forwarded-Proto") == "https"
	}
}
