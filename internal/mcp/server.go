// Package mcp exposes CMDB data through the Model Context Protocol (MCP),
// enabling agents to query longue-vue inventory read-only. The server
// follows the same goroutine-with-context pattern as the EOL enricher and
// the collector.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/impact"
)

// errDisabled is returned to MCP clients when the administrator has
// toggled the MCP feature off via the settings table.
var (
	errDisabled         = errors.New("MCP server is disabled by administrator")
	errUnauthorized     = errors.New("authentication required — provide a valid bearer token")
	errUnknownTransport = errors.New("unknown MCP transport (want \"stdio\" or \"sse\")")
)

// maxTotalItems caps the total number of items collectAll returns to
// prevent memory exhaustion on large clusters.
const maxTotalItems = 1000

// maxPageSize caps the number of items fetched per store list call.
const maxPageSize = 500

// Store is the narrow subset of api.Store the MCP server needs.
// All methods are read-only.
// Store is the narrow subset of api.Store the MCP server needs.
// All methods are read-only.
type Store interface {
	// Settings
	GetSettings(ctx context.Context) (api.Settings, error)

	// Clusters
	ListClusters(ctx context.Context, limit int, cursor string) ([]api.Cluster, string, error)
	GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error)

	// Nodes
	ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Node, string, error)
	GetNode(ctx context.Context, id uuid.UUID) (api.Node, error)

	// Namespaces
	ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Namespace, string, error)
	GetNamespace(ctx context.Context, id uuid.UUID) (api.Namespace, error)

	// Workloads
	ListWorkloads(ctx context.Context, filter api.WorkloadListFilter, limit int, cursor string) ([]api.Workload, string, error)
	GetWorkload(ctx context.Context, id uuid.UUID) (api.Workload, error)

	// Pods
	ListPods(ctx context.Context, filter api.PodListFilter, limit int, cursor string) ([]api.Pod, string, error)
	GetPod(ctx context.Context, id uuid.UUID) (api.Pod, error)

	// Services
	ListServices(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Service, string, error)

	// Ingresses
	ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Ingress, string, error)

	// PersistentVolumes
	ListPersistentVolumes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolume, string, error)

	// PersistentVolumeClaims
	ListPersistentVolumeClaims(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolumeClaim, string, error)
}

// MCPCaller carries the resolved identity of a tool-call initiator.
// For SSE transport, it is populated from the verified bearer token.
// For stdio transport (no per-request auth), TokenID is nil and Name
// is "mcp-stdio".
type MCPCaller struct {
	TokenID *uuid.UUID // nil for stdio
	Name    string     // "mcp-stdio" for stdio, token name otherwise
	UserID  *uuid.UUID // creator of the token, if any
	Scopes  []string
}

type ctxKeyMCPCaller struct{}

// mcpCallerFromContext retrieves the resolved caller stored by checkAccess.
// Returns nil when no caller is present (e.g. disabled-before-auth path).
func mcpCallerFromContext(ctx context.Context) *MCPCaller {
	v, _ := ctx.Value(ctxKeyMCPCaller{}).(*MCPCaller)
	return v
}

// AuthFunc validates a bearer token and returns the resolved caller on
// success, or an error if the token is invalid. The MCP server calls this on
// every tool invocation for SSE transport.
type AuthFunc func(ctx context.Context, token string) (*MCPCaller, error)

// Config holds the MCP server configuration.
type Config struct {
	// Transport selects the MCP transport: "stdio" or "sse".
	Transport string
	// Addr is the listen address for the SSE transport (e.g. ":8090").
	Addr string
	// Token is the PAT used for stdio authentication (optional).
	Token string
	// Auth validates bearer tokens on SSE transport. Required for SSE.
	Auth AuthFunc
	// Recorder records one audit_events row per tool call. Optional; nil
	// disables MCP audit logging (tests / stdio without DB).
	Recorder api.AuditRecorder
}

// Server wraps an MCP server backed by the longue-vue CMDB store.
type Server struct {
	store     Store
	traverser *impact.Traverser
	cfg       Config
	mcp       *server.MCPServer
}

// NewServer creates a Server. The traverser is used for the
// get_impact_graph tool; pass nil if impact analysis is not needed.
func NewServer(store Store, traverser *impact.Traverser, cfg Config) *Server {
	mcpSrv := server.NewMCPServer(
		"longue-vue CMDB",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	s := &Server{
		store:     store,
		traverser: traverser,
		cfg:       cfg,
		mcp:       mcpSrv,
	}
	s.registerTools()
	return s
}

// Run starts the configured transport and blocks until ctx is cancelled
// or the server encounters a fatal error. On context cancellation the
// transport is shut down gracefully.
func (s *Server) Run(ctx context.Context) error {
	switch s.cfg.Transport {
	case "stdio":
		return s.runStdio(ctx)
	case "sse":
		return s.runSSE(ctx)
	default:
		return fmt.Errorf("unknown MCP transport %q: %w", s.cfg.Transport, errUnknownTransport)
	}
}

func (s *Server) runStdio(ctx context.Context) error {
	slog.Info("mcp server starting", slog.String("transport", "stdio"))

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ServeStdio(s.mcp)
	}()

	select {
	case <-ctx.Done():
		slog.Info("mcp server stopped (stdio)")
		return fmt.Errorf("mcp server: %w", ctx.Err())
	case err := <-errCh:
		return fmt.Errorf("mcp stdio: %w", err)
	}
}

func (s *Server) runSSE(ctx context.Context) error {
	slog.Info("mcp server starting", slog.String("transport", "sse"), slog.String("addr", s.cfg.Addr))

	sseSrv := server.NewSSEServer(s.mcp)

	errCh := make(chan error, 1)
	go func() {
		if serveErr := sseSrv.Start(s.cfg.Addr); serveErr != nil {
			errCh <- serveErr
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("mcp server shutting down (sse)")
		if shutErr := sseSrv.Shutdown(ctx); shutErr != nil {
			slog.Warn("mcp sse shutdown error", slog.Any("error", shutErr))
		}
		return fmt.Errorf("mcp server: %w", ctx.Err())
	case err := <-errCh:
		return fmt.Errorf("mcp sse serve: %w", err)
	}
}

// checkEnabled reads the runtime settings and returns errDisabled when
// the MCP feature toggle is off.
func (s *Server) checkEnabled(ctx context.Context) error {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	if !settings.MCPEnabled {
		return errDisabled
	}
	return nil
}

// checkAccess validates that the MCP server is enabled and the caller
// is authenticated. On success it stores the resolved mcpCaller in ctx
// and returns the updated context. Called at the top of every tool handler.
//
//nolint:gocritic // hugeParam: CallToolRequest passed by value per MCP SDK handler signature.
func (s *Server) checkAccess(ctx context.Context, request mcp.CallToolRequest) (context.Context, error) {
	if err := s.checkEnabled(ctx); err != nil {
		return ctx, err
	}
	if s.cfg.Auth == nil {
		// stdio transport — no per-request auth; use a synthetic caller.
		caller := &MCPCaller{Name: "mcp-stdio"}
		return context.WithValue(ctx, ctxKeyMCPCaller{}, caller), nil
	}
	token := request.Header.Get("Authorization")
	if token == "" {
		return ctx, errUnauthorized
	}
	// Strip "Bearer " prefix if present.
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	caller, err := s.cfg.Auth(ctx, token)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, ctxKeyMCPCaller{}, caller), nil
}

// collectAll paginates through results up to maxTotalItems to prevent
// memory exhaustion on large clusters. Silently truncates beyond the cap.
func collectAll[T any](ctx context.Context, fn func(ctx context.Context, cursor string) ([]T, string, error)) ([]T, error) {
	var all []T
	cursor := ""
	for {
		items, next, err := fn(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(all) >= maxTotalItems {
			all = all[:maxTotalItems]
			break
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}
