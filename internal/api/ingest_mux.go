package api

// IngestMux — the strict-write-only HTTP router served on longue-vue's mTLS-only
// ingest listener (ADR-0016 §3). Registers exactly nineteen routes: the
// eighteen writes the K8s push collector uses (POST/PATCH only — no GETs),
// plus POST /v1/auth/verify which the DMZ ingest gateway calls to
// short-circuit invalid tokens before they cross the firewall.
//
// Anything else returns 404 from net/http's default ServeMux behaviour. A
// route that exists on longue-vue's public listener (e.g. /v1/admin/users,
// /v1/clusters via GET, /v1/auth/login) is physically not registered on
// this mux — defence in depth on top of the gateway's own allowlist.

import (
	"net/http"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// IngestRoutes is the canonical, hardcoded list of (method, path) pairs the
// ingest listener serves. Exposed for tests and security review — a single
// table of literals you can read out loud during an audit.
//
// Keep this list synchronised with internal/ingestgw's gateway-side
// allowlist (ADR-0016 §2). A route that longue-vue serves but the gateway
// blocks is fine (defence in depth); a route the gateway forwards but
// longue-vue does not register here is a configuration error and produces a
// 404 at the listener.
var IngestRoutes = []struct {
	Method  string
	Pattern string
}{
	// Token verification — the DMZ-side filter (ADR-0016 §5).
	{http.MethodPost, "/v1/auth/verify"},

	// Cluster bootstrap (idempotent on name per ADR-0016 §6).
	{http.MethodPost, "/v1/clusters"},
	{http.MethodPatch, "/v1/clusters/{id}"},

	// Per-resource upserts + reconciles. The eighteen write paths the K8s
	// push collector touches every tick — no other endpoint is needed.
	{http.MethodPost, "/v1/nodes"},
	{http.MethodPost, "/v1/nodes/reconcile"},
	{http.MethodPost, "/v1/namespaces"},
	{http.MethodPost, "/v1/namespaces/reconcile"},
	{http.MethodPost, "/v1/pods"},
	{http.MethodPost, "/v1/pods/reconcile"},
	{http.MethodPost, "/v1/workloads"},
	{http.MethodPost, "/v1/workloads/reconcile"},
	{http.MethodPost, "/v1/services"},
	{http.MethodPost, "/v1/services/reconcile"},
	{http.MethodPost, "/v1/ingresses"},
	{http.MethodPost, "/v1/ingresses/reconcile"},
	{http.MethodPost, "/v1/persistentvolumes"},
	{http.MethodPost, "/v1/persistentvolumes/reconcile"},
	{http.MethodPost, "/v1/persistentvolumeclaims"},
	{http.MethodPost, "/v1/persistentvolumeclaims/reconcile"},
}

// IngestMuxConfig wires the strict-server backend, the auth + audit
// middleware, and an optional 404 handler for the ingest listener. The
// auth middleware MUST be the same auth.Middleware longue-vue's public
// listener uses — longue-vue re-validates every forwarded token with the
// standard argon2id check; the gateway is never an auth authority.
type IngestMuxConfig struct {
	// Server is the StrictServerInterface implementation (typically
	// *Server from internal/api).
	Server ServerInterface

	// AuthMiddleware resolves cookie → bearer → 401 and attaches the
	// Caller to the request context. Same instance used on the public
	// listener, applied here too so forwarded writes go through the
	// same scope checks. The verify endpoint itself is public — its
	// auth is the listener-level mTLS handshake.
	AuthMiddleware MiddlewareFunc

	// AuditMiddleware records non-GET requests with source="ingest_gw"
	// (ADR-0016 §11). Configured by the caller (typically
	// AuditMiddleware(pg, "ingest_gw", trustedProxies)).
	AuditMiddleware MiddlewareFunc

	// Cookie policy is unused on this listener (no cookies traverse the
	// DMZ) but kept here so the wiring matches the public listener's
	// AuthMiddleware shape and tests can swap implementations.
	CookiePolicy auth.SecureCookiePolicy
}

// NewIngestMux builds the http.ServeMux for the ingest listener. Returns
// a fully wired handler — caller mounts it on its own *http.Server with
// the appropriate tls.Config (mTLS, RequireAndVerifyClientCert).
//
// The returned mux registers exactly len(IngestRoutes) handlers; a
// request to any other path/method returns 404 by net/http's default.
func NewIngestMux(cfg IngestMuxConfig) *http.ServeMux {
	wrapper := ServerInterfaceWrapper{
		Handler:            cfg.Server,
		HandlerMiddlewares: []MiddlewareFunc{cfg.AuditMiddleware, cfg.AuthMiddleware},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			// Same shape as the public listener's error handler — a 400
			// for parse errors with no internal detail leaked to the
			// caller. Keeps the ingest-listener surface area consistent.
			http.Error(w, err.Error(), http.StatusBadRequest)
		},
	}

	mux := http.NewServeMux()
	for _, route := range IngestRoutes {
		handler := dispatchByOperation(route.Method, route.Pattern, &wrapper)
		mux.HandleFunc(route.Method+" "+route.Pattern, handler)
	}
	return mux
}

// dispatchByOperation maps a (method, pattern) tuple to the matching
// generated wrapper method. Kept as a switch rather than reflection so
// the compiler catches a route added to IngestRoutes without a wrapper
// dispatch.
//
//nolint:gocyclo // a flat switch is the clearest reading of the route table
func dispatchByOperation(method, pattern string, wrapper *ServerInterfaceWrapper) http.HandlerFunc {
	key := method + " " + pattern
	switch key {
	case "POST /v1/auth/verify":
		return wrapper.VerifyToken
	case "POST /v1/clusters":
		return wrapper.CreateCluster
	case "PATCH /v1/clusters/{id}":
		return wrapper.UpdateCluster
	case "POST /v1/nodes":
		return wrapper.CreateNode
	case "POST /v1/nodes/reconcile":
		return wrapper.ReconcileNodes
	case "POST /v1/namespaces":
		return wrapper.CreateNamespace
	case "POST /v1/namespaces/reconcile":
		return wrapper.ReconcileNamespaces
	case "POST /v1/pods":
		return wrapper.CreatePod
	case "POST /v1/pods/reconcile":
		return wrapper.ReconcilePods
	case "POST /v1/workloads":
		return wrapper.CreateWorkload
	case "POST /v1/workloads/reconcile":
		return wrapper.ReconcileWorkloads
	case "POST /v1/services":
		return wrapper.CreateService
	case "POST /v1/services/reconcile":
		return wrapper.ReconcileServices
	case "POST /v1/ingresses":
		return wrapper.CreateIngress
	case "POST /v1/ingresses/reconcile":
		return wrapper.ReconcileIngresses
	case "POST /v1/persistentvolumes":
		return wrapper.CreatePersistentVolume
	case "POST /v1/persistentvolumes/reconcile":
		return wrapper.ReconcilePersistentVolumes
	case "POST /v1/persistentvolumeclaims":
		return wrapper.CreatePersistentVolumeClaim
	case "POST /v1/persistentvolumeclaims/reconcile":
		return wrapper.ReconcilePersistentVolumeClaims
	}
	// IngestRoutes carries an entry without a matching wrapper case —
	// produce a deliberate 500 so the test suite catches the gap rather
	// than silently 404ing forever.
	return func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "ingest mux misconfigured: no dispatch for "+key, http.StatusInternalServerError)
	}
}
