//nolint:gocritic // MCP SDK handler signature passes CallToolRequest by value.
package mcp

// list_applications MCP tool (ADR-0029 §4). Read-only: lists curated
// first-class Application entities with their DICT classification and
// member counts. Mirrors the GET /v1/applications filter surface.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/metrics"
)

func (s *Server) handleListApplications(ctx context.Context, request mcp.CallToolRequest) (resp *mcp.CallToolResult, retErr error) {
	args := map[string]any{
		"name":                   presence(request.GetString("name", "")),
		"application_block_name": presence(request.GetString("application_block_name", "")),
		"criticality":            presence(request.GetString("criticality", "")),
		"has_dict":               presence(request.GetString("has_dict", "")),
		"dict_min":               presence(request.GetString("dict_min", "")),
	}
	var err error
	if ctx, err = s.checkAccess(ctx, request); err != nil {
		return s.recordCheckAccessFailure(ctx, "list_applications", args, err), nil
	}
	defer s.finishDeferred(ctx, "list_applications", args, &resp, &retErr)

	start := time.Now()
	defer func() { metrics.ObserveMCPToolCall("list_applications", time.Since(start)) }()

	filter, problem := buildApplicationListFilter(request)
	if problem != "" {
		return mcp.NewToolResultError(problem), nil
	}

	items, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Application, string, error) {
		return s.store.ListApplications(ctx, filter, maxPageSize, cursor)
	})
	if err != nil {
		return nil, fmt.Errorf("list applications: %w", err)
	}
	return jsonResult(map[string]any{"items": items})
}

// buildApplicationListFilter parses the optional list_applications args into
// an api.ApplicationListFilter. Returns a user-facing problem string on the
// first invalid value (so the handler surfaces it via NewToolResultError).
//
//nolint:gocyclo // five optional filters; each branch is trivial
func buildApplicationListFilter(request mcp.CallToolRequest) (api.ApplicationListFilter, string) {
	var f api.ApplicationListFilter
	if v := request.GetString("name", ""); v != "" {
		f.Name = &v
	}
	if v := request.GetString("application_block_name", ""); v != "" {
		f.ApplicationBlockName = &v
	}
	if v := request.GetString("criticality", ""); v != "" {
		f.Criticality = &v
	}
	if v := request.GetString("has_dict", ""); v != "" {
		switch v {
		case "true", "1":
			b := true
			f.HasDICT = &b
		case "false", "0":
			b := false
			f.HasDICT = &b
		default:
			return f, "invalid has_dict (use true/false)"
		}
	}
	if v := request.GetString("dict_min", ""); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 4 {
			return f, "dict_min must be an integer 0..4"
		}
		f.DICTMin = &n
	}
	return f, ""
}
