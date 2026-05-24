//nolint:gocritic // MCP SDK handler signature passes CallToolRequest by value.
package mcp

// get_application MCP tool (ADR-0029 §4). Read-only: fetches one
// Application by id OR name, returning the full entity including its DICT
// classification and member counts.
//
// EOL summary (ADR-0029 §5) is intentionally NOT wired here for v1: the
// MCP Store interface does not satisfy eolagg.ApplicationStore (which needs
// the paginated member-list walk), and the adapter would be more than the
// ~20-line budget. Operators can call GET /v1/applications/{id}/eol for the
// aggregated signal; surfacing it via MCP is a clean follow-up.

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

func (s *Server) handleGetApplication(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"id":   presence(request.GetString("id", "")),
		"name": presence(request.GetString("name", "")),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "get_application", args, err), nil
	}
	defer s.finishDeferred(ctx, "get_application", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("get_application", time.Since(start)) }()

	idStr := request.GetString("id", "")
	nameStr := request.GetString("name", "")
	if idStr == "" && nameStr == "" {
		return mcp.NewToolResultError("one of id or name is required"), nil
	}

	var app api.Application
	if idStr != "" {
		id, parseErr := uuid.Parse(idStr)
		if parseErr != nil {
			return mcp.NewToolResultError("invalid id"), nil
		}
		app, err = s.store.GetApplication(ctx, id)
	} else {
		app, err = s.store.GetApplicationByName(ctx, api.NormalizeApplicationName(nameStr))
	}
	if err != nil {
		return storeError("application", err)
	}
	return jsonResult(app)
}
