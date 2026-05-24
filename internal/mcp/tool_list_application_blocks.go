//nolint:gocritic // MCP SDK handler signature passes CallToolRequest by value.
package mcp

// list_application_blocks MCP tool (ADR-0029 §4). Read-only: lists the
// SNC two-level grouping above Application, each carrying a denormalised
// application_count.

import (
	"context"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

func (s *Server) handleListApplicationBlocks(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"name":  presence(request.GetString("name", "")),
		"owner": presence(request.GetString("owner", "")),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_application_blocks", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_application_blocks", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_application_blocks", time.Since(start)) }()

	var filter api.ApplicationBlockListFilter
	if v := request.GetString("name", ""); v != "" {
		filter.Name = &v
	}
	if v := request.GetString("owner", ""); v != "" {
		filter.Owner = &v
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.ApplicationBlock, string, error) {
		return s.store.ListApplicationBlocks(ctx, filter, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list application blocks: %w", err)
	}
	return jsonResult(map[string]any{"items": items})
}
