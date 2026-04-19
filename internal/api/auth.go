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
	"net/http"

	"github.com/sthalbert/argos/internal/auth"
)

// AuthMiddleware returns an api.MiddlewareFunc that resolves cookie →
// bearer → 401 and attaches the caller to the request context. Pass the
// same store the Server holds and the same cookie policy NewServer got.
func AuthMiddleware(store auth.Store, policy auth.SecureCookiePolicy) MiddlewareFunc {
	m := auth.Middleware(store, policy)
	return func(next http.Handler) http.Handler {
		return m(next)
	}
}
