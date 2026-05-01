package api

// Auth glue between the generated server and the internal/auth package.
//
// Historically this file carried the env-var-driven TokenStore + BearerAuth
// middleware. That path is removed per ADR-0007 — tokens are issued in the
// admin UI and stored in PostgreSQL, and humans log in with cookies. The
// only thing left here is a thin adapter that turns `auth.Middleware` into
// an `api.MiddlewareFunc` so the StdHTTPServerOptions wiring stays
// identical to the pre-ADR-0007 shape.

import (
	"net"
	"net/http"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// AuthMiddleware returns an api.MiddlewareFunc that resolves cookie →
// bearer → 401 and attaches the caller to the request context. Pass the
// same store the Server holds, the same cookie policy NewServer got, and
// the operator-supplied trusted-proxy CIDR list (ADR-0017) — the trust
// list gates whether X-Forwarded-Proto is honored when deciding the
// Secure cookie flag. Pass nil to ignore XFP unconditionally — the
// secure default.
func AuthMiddleware(store auth.Store, policy auth.SecureCookiePolicy, trustedProxies []*net.IPNet) MiddlewareFunc {
	m := auth.Middleware(store, policy, trustedProxies)
	return func(next http.Handler) http.Handler {
		return m(next)
	}
}
