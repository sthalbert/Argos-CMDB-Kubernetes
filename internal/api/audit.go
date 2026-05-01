package api

// Audit-event capture middleware + the listAuditEvents handler. The
// middleware observes every API request that completes through the
// generated router (non-GET requests, plus every /v1/admin/* call) and
// appends one row to audit_events. Read traffic is deliberately not
// logged — it would drown the CMDB browsing use case — but admin-panel
// reads are, because who-looked-at-whom matters for SNC auditors.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/httputil"
)

// AuditRecorder is the narrow slice of Store the middleware needs.
// Exposed as its own interface so tests don't need a full Store fake
// just to assert on audit rows.
type AuditRecorder interface {
	InsertAuditEvent(ctx context.Context, in AuditEventInsert) error
}

// auditBag is a mutable holder placed in the context before the handler
// runs. Handlers call SetAuditDetails to attach extra fields; the
// middleware reads them after the handler returns. A pointer-in-context
// pattern is necessary because the strict-server handler receives a
// derived context that cannot propagate values back to the middleware's
// original request context.
type auditBag struct {
	details map[string]any
}

type ctxKeyAuditBag struct{}

// withAuditBag returns a child context carrying a mutable audit bag.
func withAuditBag(ctx context.Context) (context.Context, *auditBag) {
	bag := &auditBag{}
	return context.WithValue(ctx, ctxKeyAuditBag{}, bag), bag
}

// SetAuditDetails stores extra detail fields in the audit bag carried
// by ctx. Handlers call this to enrich the audit event with domain-
// specific data (e.g., a pre-deletion cascade snapshot per ADR-0010).
// Safe to call when no bag is present (no-op).
func SetAuditDetails(ctx context.Context, details map[string]any) {
	if bag, ok := ctx.Value(ctxKeyAuditBag{}).(*auditBag); ok && bag != nil {
		bag.details = details
	}
}

// AuditMiddleware wraps the generated router and records every call
// that looks state-changing. Recording happens after the downstream
// handler returns so we capture the response status alongside the
// caller. Insertion failures are logged at ERROR but never surface
// to the client: losing the CMDB because the audit table is briefly
// unreachable would be a worse outcome than a gap in the log.
//
// `source` distinguishes which listener served the request — "api" for
// longue-vue's public listener, "ingest_gw" for the mTLS-only listener
// fronted by the DMZ gateway (ADR-0016). The label is passed through to
// audit_events.source so operators can answer "what came through the
// DMZ" with a single WHERE clause.
//
// `trustedProxies` is the operator-supplied CIDR list (ADR-0017 §2)
// whose X-Forwarded-For headers we honor when resolving the SourceIP for
// the audit row. Pass nil to ignore XFF unconditionally — the secure
// default and what tests should use unless they're specifically
// exercising proxy-trust behavior.
func AuditMiddleware(recorder AuditRecorder, source string, trustedProxies []*net.IPNet) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldAudit(r) {
				next.ServeHTTP(w, r)
				return
			}
			// Snapshot the request body for write calls with JSON payloads —
			// keeps a small diff in `details` without reading-then-failing
			// on the handler's second pass. Capped at 4 KiB.
			var bodySnap []byte
			if r.Body != nil && r.ContentLength >= 0 && r.ContentLength <= 4096 &&
				strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				buf, _ := io.ReadAll(r.Body)
				bodySnap = buf
				r.Body = io.NopCloser(bytes.NewReader(buf))
			}

			// Inject a mutable audit bag so handlers can enrich
			// the event via SetAuditDetails (ADR-0010).
			ctx, bag := withAuditBag(r.Context())
			r = r.WithContext(ctx)

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			ev := buildAuditEvent(r, rw.status, bodySnap, trustedProxies)
			ev.Source = source
			// Merge handler-injected details (e.g., cascade snapshot).
			if bag.details != nil {
				if ev.Details == nil {
					ev.Details = make(map[string]any)
				}
				for k, v := range bag.details {
					ev.Details[k] = v
				}
			}
			if err := recorder.InsertAuditEvent(ctx, ev); err != nil {
				slog.Error("audit: insert failed",
					slog.Any("error", err),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", rw.status),
				)
			}
		})
	}
}

// shouldAudit is the allow-list. Writes of any kind are audited; reads
// are audited only when they hit /v1/admin/* OR a credentials-fetch
// endpoint under /v1/cloud-accounts/.../credentials (ADR-0015 §5: every
// plaintext SK read must produce an audit row, even though the endpoint
// is reached by collector PATs not admins). /healthz, /readyz,
// /metrics, /ui/*, and /v1/auth/config are chatty reads not worth
// logging.
func shouldAudit(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/v1/admin/") {
		return true
	}
	// /v1/cloud-accounts/<id-or-by-name/...>/credentials
	return strings.HasPrefix(r.URL.Path, "/v1/cloud-accounts/") &&
		strings.HasSuffix(r.URL.Path, "/credentials")
}

func buildAuditEvent(r *http.Request, status int, bodySnap []byte, trustedProxies []*net.IPNet) AuditEventInsert {
	caller := auth.CallerFromContext(r.Context())
	sourceIP := ""
	if ip := httputil.ClientIP(r, trustedProxies); ip != nil {
		sourceIP = ip.String()
	}
	ev := AuditEventInsert{
		ID:         uuid.New(),
		OccurredAt: time.Now().UTC(),
		ActorKind:  "anonymous",
		Action:     deriveAction(r, status),
		HTTPMethod: r.Method,
		HTTPPath:   r.URL.Path,
		HTTPStatus: status,
		SourceIP:   sourceIP,
		UserAgent:  r.UserAgent(),
	}
	if caller != nil {
		switch caller.Kind {
		case auth.CallerKindUser:
			ev.ActorKind = "user"
			id := caller.UserID
			ev.ActorID = &id
			ev.ActorUsername = caller.Username
			ev.ActorRole = caller.Role
		case auth.CallerKindToken:
			ev.ActorKind = "token"
			ev.ActorUsername = caller.TokenName
		}
	}
	resType, resID := deriveResource(r)
	ev.ResourceType = resType
	ev.ResourceID = resID
	if len(bodySnap) > 0 {
		// Pass through whatever was sent; scrubSecrets below redacts
		// common password / token fields so secrets don't live in the
		// long-lived audit table.
		ev.Details = map[string]any{"body": scrubSecrets(bodySnap)}
	}
	return ev
}

// deriveAction picks a human-friendly verb. For admin endpoints we use
// the domain verb (user.create, token.revoke, …) so the UI can group
// on it. For everything else we fall back to "<resource>.<verb>" based
// on the URL + method; unknown shapes get a generic http.<method>.
func deriveAction(r *http.Request, status int) string {
	method := strings.ToLower(r.Method)
	path := r.URL.Path

	// Login / logout / password get their own explicit verbs so admins
	// can grep for authentication activity without parsing URLs.
	switch path {
	case "/v1/auth/login":
		if status < 400 {
			return "auth.login.success"
		}
		return "auth.login.failure"
	case "/v1/auth/logout":
		return "auth.logout"
	case "/v1/auth/change-password":
		return "auth.change_password"
	case "/v1/auth/oidc/authorize":
		return "auth.oidc.authorize"
	case "/v1/auth/oidc/callback":
		if status >= 300 && status < 400 {
			return "auth.oidc.login"
		}
		return "auth.oidc.failure"
	}

	resType, _ := deriveResource(r)
	if resType != "" {
		return resType + "." + methodVerb(method)
	}
	return "http." + method
}

// deriveResource maps an admin / domain URL to (resource_type,
// optional_id). Returns "" when the URL doesn't match a known shape;
// the PG layer translates empty strings into SQL NULL.
func deriveResource(r *http.Request) (resType, resID string) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "v1" {
		return "", ""
	}
	// /v1/admin/<kind>[/<id>]
	if parts[1] == "admin" && len(parts) >= 3 {
		kind := singular(parts[2])
		if kind == "" {
			return "", ""
		}
		id := ""
		if len(parts) >= 4 {
			id = parts[3]
		}
		return "admin_" + kind, id
	}
	// /v1/<kind>[/<id>/...]
	kind := singular(parts[1])
	if kind == "" {
		return "", ""
	}
	id := ""
	if len(parts) >= 3 {
		id = parts[2]
	}
	return kind, id
}

// singularNames maps collection (URL-segment) names to their singular
// form for audit display. Only the kinds longue-vue actually serves are
// listed — anything else passes through unchanged (best-effort display
// only).
var singularNames = map[string]string{ //nolint:gochecknoglobals // read-only lookup table
	"users":                    "user",
	"tokens":                   "token",
	"sessions":                 "session",
	"audit":                    "audit",
	"clusters":                 "cluster",
	"namespaces":               "namespace",
	"nodes":                    "node",
	"pods":                     "pod",
	"workloads":                "workload",
	"services":                 "service",
	"ingresses":                "ingress",
	"persistent-volumes":       "persistent_volume",
	"persistent-volume-claims": "persistent_volume_claim",
	"cloud-accounts":           "cloud_account",
	"virtual-machines":         "virtual_machine",
	"auth":                     "auth",
}

// singular naively de-pluralises collection names for audit display.
func singular(plural string) string {
	if s, ok := singularNames[plural]; ok {
		return s
	}
	return plural
}

func methodVerb(lower string) string {
	switch lower {
	case "post":
		return "create"
	case "put", "patch":
		return "update"
	case "delete":
		return "delete"
	case "get":
		return "read"
	}
	return lower
}

// scrubSecrets redacts password / token / secret-like fields from a
// captured JSON body. The goal is "don't write the admin's password to
// a long-lived table"; it's not a security boundary — anything the
// handler sees is already trusted.
func scrubSecrets(raw []byte) any {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil // malformed — drop rather than risk leaking a prefix
	}
	for _, k := range []string{
		"password", "new_password", "current_password",
		"client_secret", "token", "code_verifier",
		// ADR-0015: cloud-provider credentials. The credentials-fetch
		// endpoints log only request metadata (method/path/caller); the
		// response body — which contains plaintext SK — is intentionally
		// NOT logged anywhere. The middleware here only ever sees request
		// bodies, but we redact request-side `secret_key` / `access_key`
		// defensively too because admins POST them on /credentials.
		"secret_key", "access_key",
	} {
		if _, ok := obj[k]; ok {
			obj[k] = "[redacted]"
		}
	}
	return obj
}

// statusRecorder is the tiniest http.ResponseWriter wrapper that
// remembers the status code the handler wrote. Defaulting to 200
// matches net/http's behaviour for handlers that never call WriteHeader.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

// WriteHeader captures the status code before forwarding to the wrapped writer.
func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if !r.written {
		r.status = http.StatusOK
		r.written = true
	}
	n, err := r.ResponseWriter.Write(p)
	if err != nil {
		return n, fmt.Errorf("write response: %w", err)
	}
	return n, nil
}

// ListAuditEvents handler lives in auth_handlers.go alongside the
// other admin endpoints to keep the strict-server pattern consistent.
