package api

// ADR-0029 §2.3 — honor an explicit `"application_id": null` on the
// workload PATCH as an unlink, while an absent key leaves the link
// unchanged (RFC 7396 merge-patch).
//
// The workload PATCH body is decoded by the oapi-codegen strict server into
// WorkloadUpdate, whose `application_id` is a single *uuid.UUID. JSON `null`
// and an absent key both decode to a nil pointer, so the handler alone cannot
// tell "unlink" from "leave alone". This standard MiddlewareFunc runs before
// the codegen decode: for PATCH /v1/workloads/{id} it peeks the raw body,
// records whether `application_id` is present-and-null in the request context,
// and restores r.Body so the downstream decode is unaffected. The strict
// handler reads the flag via workloadClearApplicationFromCtx.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type ctxKeyClearWorkloadApplication struct{}

// workloadPatchBodyMaxPeek caps how much of the body we buffer for null
// detection. Workload PATCH bodies are small curated merge-patches; the
// public listener already wraps everything in a 1 MiB MaxBytesHandler.
const workloadPatchBodyMaxPeek = 1 << 20

// DetectWorkloadUnlinkMiddleware records, for PATCH /v1/workloads/{id}, whether
// the body carries an explicit `"application_id": null`. It restores r.Body so
// the codegen decode still sees the full payload.
func DetectWorkloadUnlinkMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isWorkloadPatch(r) || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		buf, err := io.ReadAll(io.LimitReader(r.Body, workloadPatchBodyMaxPeek))
		if err != nil {
			// Let the downstream decoder surface the read error consistently.
			r.Body = io.NopCloser(bytes.NewReader(buf))
			next.ServeHTTP(w, r)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(buf))
		if explicitNullJSONField(buf, "application_id") {
			r = r.WithContext(context.WithValue(r.Context(), ctxKeyClearWorkloadApplication{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

// isWorkloadPatch matches PATCH /v1/workloads/{id} (a single path segment after
// the collection) and excludes the sub-resources (.../reconcile, .../history).
func isWorkloadPatch(r *http.Request) bool {
	if r.Method != http.MethodPatch {
		return false
	}
	const prefix = "/v1/workloads/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	rest = strings.TrimSuffix(rest, "/")
	return rest != "" && !strings.Contains(rest, "/")
}

// explicitNullJSONField reports whether the JSON object in body has the given
// key with a value that is literally null. Absent key → false; any non-null
// value → false; non-object body → false. Decoding into json.RawMessage
// preserves the present-and-null vs absent distinction that a typed pointer
// field loses. Shared by the workload-unlink middleware and the hand-written
// VM PATCH handler (ADR-0029 §2.3).
func explicitNullJSONField(body []byte, key string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return false
	}
	v, ok := raw[key]
	if !ok {
		return false
	}
	return string(bytes.TrimSpace(v)) == jsonNullLiteral
}

// workloadClearApplicationFromCtx reports whether DetectWorkloadUnlinkMiddleware
// saw an explicit `"application_id": null` on this request.
func workloadClearApplicationFromCtx(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyClearWorkloadApplication{}).(bool)
	return v
}
