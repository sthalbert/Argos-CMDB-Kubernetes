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
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// AuditRecorder is the narrow slice of Store the middleware needs.
// Exposed as its own interface so tests don't need a full Store fake
// just to assert on audit rows.
type AuditRecorder interface {
	InsertAuditEvent(ctx context.Context, in AuditEventInsert) error
}

// AuditMiddleware wraps the generated router and records every call
// that looks state-changing. Recording happens after the downstream
// handler returns so we capture the response status alongside the
// caller. Insertion failures are logged at ERROR but never surface
// to the client: losing the CMDB because the audit table is briefly
// unreachable would be a worse outcome than a gap in the log.
func AuditMiddleware(recorder AuditRecorder) MiddlewareFunc {
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

			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)

			ev := buildAuditEvent(r, rw.status, bodySnap)
			if err := recorder.InsertAuditEvent(r.Context(), ev); err != nil {
				slog.Error("audit: insert failed",
					"error", err,
					"method", r.Method,
					"path", r.URL.Path,
					"status", rw.status,
				)
			}
		})
	}
}

// shouldAudit is the allow-list. Writes of any kind are audited; reads
// are audited only when they hit /v1/admin/*. /healthz, /readyz,
// /metrics, /ui/*, and /v1/auth/config are chatty reads not worth
// logging.
func shouldAudit(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/v1/admin/")
}

func buildAuditEvent(r *http.Request, status int, bodySnap []byte) AuditEventInsert {
	caller := auth.CallerFromContext(r.Context())
	ev := AuditEventInsert{
		ID:         uuid.New(),
		OccurredAt: time.Now().UTC(),
		ActorKind:  "anonymous",
		Action:     deriveAction(r, status),
		HTTPMethod: r.Method,
		HTTPPath:   r.URL.Path,
		HTTPStatus: status,
		SourceIP:   clientIP(r),
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
func deriveResource(r *http.Request) (string, string) {
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

// singular naively de-pluralises collection names for audit display.
// Only the kinds argosd actually serves are listed — anything else
// passes through unchanged (best-effort display only).
func singular(plural string) string {
	switch plural {
	case "users":
		return "user"
	case "tokens":
		return "token"
	case "sessions":
		return "session"
	case "audit":
		return "audit"
	case "clusters":
		return "cluster"
	case "namespaces":
		return "namespace"
	case "nodes":
		return "node"
	case "pods":
		return "pod"
	case "workloads":
		return "workload"
	case "services":
		return "service"
	case "ingresses":
		return "ingress"
	case "persistent-volumes":
		return "persistent_volume"
	case "persistent-volume-claims":
		return "persistent_volume_claim"
	case "auth":
		return "auth"
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
	return r.ResponseWriter.Write(p)
}

// --- handler -------------------------------------------------------------

// ListAuditEvents serves GET /v1/admin/audit. Any field left nil on the
// filter struct is ignored downstream.
func (s *Server) ListAuditEvents(w http.ResponseWriter, r *http.Request, params ListAuditEventsParams) {
	limit, cursor := paging(params.Limit, params.Cursor)
	filter := AuditEventFilter{
		ActorID:      params.ActorId,
		ResourceType: params.ResourceType,
		ResourceID:   params.ResourceId,
		Action:       params.Action,
		Since:        params.Since,
		Until:        params.Until,
	}
	items, next, err := s.store.ListAuditEvents(r.Context(), filter, limit, cursor)
	if err != nil {
		s.writeStoreError(w, "listAuditEvents", err)
		return
	}
	resp := AuditEventList{Items: items}
	if next != "" {
		resp.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, resp)
}
