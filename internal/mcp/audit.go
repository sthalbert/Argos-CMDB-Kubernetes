package mcp

// recordToolCall writes one audit_events row for an MCP tool invocation.
// Errors from the recorder are logged at ERROR and never returned —
// losing the CMDB because the audit table is briefly unreachable would
// be a worse outcome than a gap in the log (mirrors internal/api/audit.go).

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
)

// sensitiveArgKeys lists args whose raw values must NOT appear in audit
// logs because they may contain PII or secret-like substrings.
var sensitiveArgKeys = map[string]struct{}{
	"image":     {},
	"query":     {},
	"name":      {},
	"node_name": {}, // may embed cloud instance IDs / internal hostnames
}

// presence returns "<set>" when s is non-empty, "<unset>" otherwise.
// Used for sensitive string args that should not be logged verbatim.
func presence(s string) string {
	if s != "" {
		return "<set>"
	}
	return "<unset>"
}

// filterArgs returns a copy of args safe for audit-log insertion.
// Safe keys are included verbatim; sensitive keys are replaced with
// presence("<set>"|"<unset>").
func filterArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if _, sensitive := sensitiveArgKeys[k]; sensitive {
			// Coerce to string for presence check.
			s, _ := v.(string)
			out[k] = presence(s)
		} else {
			out[k] = v
		}
	}
	return out
}

// recordToolCall writes one audit_events row for the given tool call.
// status is an HTTP-like status code: 200 (success), 400 (tool error /
// bad input), 401 (auth denied), 500 (internal error / panic).
func (s *Server) recordToolCall(ctx context.Context, tool string, args map[string]any, status int) {
	if s.cfg.Recorder == nil {
		return
	}

	in := api.AuditEventInsert{
		ID:           uuid.New(),
		OccurredAt:   time.Now().UTC(),
		Action:       "mcp." + tool,
		ResourceType: "mcp_tool",
		ResourceID:   tool,
		HTTPStatus:   status,
		Source:       "mcp",
		Details:      filterArgs(args),
	}

	// Resolve caller from context.
	caller := mcpCallerFromContext(ctx)
	if caller != nil {
		if caller.TokenID != nil {
			in.ActorKind = "token"
			in.ActorID = caller.TokenID
			in.ActorUsername = caller.Name
		} else {
			// stdio path
			in.ActorKind = "system"
			in.ActorUsername = caller.Name
		}
	} else {
		in.ActorKind = "anonymous"
	}

	if err := s.cfg.Recorder.InsertAuditEvent(ctx, in); err != nil {
		slog.Error("mcp: failed to insert audit event",
			slog.String("tool", tool),
			slog.Any("error", err),
		)
	}
}

// recordDenial emits a 401 audit row for an auth/enable failure. It is
// the only call site that should produce status 401. Handlers must call
// this BEFORE installing the deferred finish so there is no double-record.
func (s *Server) recordDenial(ctx context.Context, tool string, args map[string]any) {
	s.recordToolCall(ctx, tool, args, 401)
}

// finishDeferred is intended for use as a named-return defer:
//
//	func (s *Server) handleFoo(...) (resp *mcp.CallToolResult, retErr error) {
//	    args := map[string]any{...}
//	    // checkAccess / recordDenial / early-return first …
//	    defer s.finishDeferred(ctx, "foo", args, &resp, &retErr)
//	    ...
//	}
//
// It recovers panics from the handler body, records a 500 row, then
// re-raises the panic so the MCP SDK can handle/log it. For normal
// returns it maps retErr→500, result.IsError→400, else 200.
func (s *Server) finishDeferred(ctx context.Context, tool string, args map[string]any, result **mcp.CallToolResult, retErr *error) {
	if r := recover(); r != nil {
		s.recordToolCall(ctx, tool, args, 500)
		panic(r) // re-raise — MCP SDK will handle/log
	}

	status := 200
	switch {
	case retErr != nil && *retErr != nil:
		status = 500
	case *result != nil && (*result).IsError:
		status = 400
	}
	s.recordToolCall(ctx, tool, args, status)
}
