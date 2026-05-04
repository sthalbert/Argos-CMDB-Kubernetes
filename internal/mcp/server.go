// Package mcp exposes CMDB data through the Model Context Protocol (MCP),
// enabling agents to query longue-vue inventory read-only. The server
// follows the same goroutine-with-context pattern as the EOL enricher and
// the collector.
package mcp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/impact"
)

// errDisabled is returned to MCP clients when the administrator has
// toggled the MCP feature off via the settings table.
var (
	errDisabled            = errors.New("MCP server is disabled by administrator")
	errUnauthorized        = errors.New("authentication required — provide a valid bearer token")
	errUnknownTransport    = errors.New("unknown MCP transport (want \"stdio\" or \"sse\")")
	errRateLimited         = errors.New("rate limit exceeded — slow down or split into smaller batches")
	errMCPPlaintextRefused = errors.New("mcp sse: TLS not configured and AllowPlaintext=false — refusing to start (CRIT-01)")
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

// Caller carries the resolved identity of a tool-call initiator.
// For SSE transport, it is populated from the verified bearer token.
// For stdio transport (no per-request auth), TokenID is nil and Name
// is "mcp-stdio".
type Caller struct {
	TokenID *uuid.UUID // nil for stdio
	Name    string     // "mcp-stdio" for stdio, token name otherwise
	UserID  *uuid.UUID // creator of the token, if any
	Scopes  []string
}

type ctxKeyCaller struct{}

// mcpCallerFromContext retrieves the resolved caller stored by checkAccess.
// Returns nil when no caller is present (e.g. disabled-before-auth path).
func mcpCallerFromContext(ctx context.Context) *Caller {
	v, _ := ctx.Value(ctxKeyCaller{}).(*Caller)
	return v
}

// AuthFunc validates a bearer token and returns the resolved caller on
// success, or an error if the token is invalid. The MCP server calls this on
// every tool invocation for SSE transport.
type AuthFunc func(ctx context.Context, token string) (*Caller, error)

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
	// TLSGetCertificate enables TLS 1.3 on the SSE listener when non-nil.
	// The hook is the standard tls.Config.GetCertificate callback,
	// produced by the parent's cert-reloader so cert rotation is
	// transparent (no listener restart).
	TLSGetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// AllowPlaintext, when true, lets the SSE transport start without
	// TLS. Required for tests, dev loops, and environments that
	// terminate TLS upstream (e.g. an in-cluster service mesh sidecar).
	// Refused unless explicitly opted in — bearer tokens flow over the
	// wire and must not be exposed in plaintext on a network interface.
	AllowPlaintext bool
	// RateLimiter enforces per-token rate limiting on tool calls.
	// Optional; nil disables rate limiting.
	RateLimiter *RateLimiter
}

// Server wraps an MCP server backed by the longue-vue CMDB store.
type Server struct {
	store       Store
	traverser   *impact.Traverser
	cfg         Config
	mcp         *server.MCPServer
	stdioCaller *Caller // set after successful stdio token verification
	isStdio     bool    // true when transport=="stdio"; skips per-call bearer auth
}

// NewServer creates a Server. The traverser is used for the
// get_impact_graph tool; pass nil if impact analysis is not needed.
func NewServer(store Store, traverser *impact.Traverser, cfg *Config) *Server {
	mcpSrv := server.NewMCPServer(
		"longue-vue CMDB",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	s := &Server{
		store:     store,
		traverser: traverser,
		cfg:       *cfg,
		mcp:       mcpSrv,
		isStdio:   cfg.Transport == "stdio",
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

// verifyStdioToken validates cfg.Token at stdio startup (MED-01).
// If Token is non-empty and Auth is configured, the token is verified once;
// the resolved caller is stored on s.stdioCaller for subsequent tool-call
// attribution. If Token is empty, a loud warning is emitted and the server
// falls back to process-level trust (audit rows tagged "mcp-stdio").
func (s *Server) verifyStdioToken(ctx context.Context) error {
	if s.cfg.Token == "" {
		slog.Warn("mcp stdio: LONGUE_VUE_MCP_TOKEN not set — caller inherits process-level trust; audit rows will be tagged as 'mcp-stdio'")
		return nil
	}
	if s.cfg.Auth == nil {
		// Auth function not wired for stdio — no verification possible.
		return nil
	}
	caller, err := s.cfg.Auth(ctx, s.cfg.Token)
	if err != nil {
		return fmt.Errorf("mcp stdio: token verification failed: %w", err)
	}
	s.stdioCaller = caller
	slog.Info("mcp stdio: token verified", slog.String("caller", caller.Name))
	return nil
}

func (s *Server) runStdio(ctx context.Context) error {
	slog.Info("mcp server starting", slog.String("transport", "stdio"))

	if err := s.verifyStdioToken(ctx); err != nil {
		return err
	}

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
	if s.cfg.TLSGetCertificate == nil && !s.cfg.AllowPlaintext {
		return errMCPPlaintextRefused
	}

	useTLS := s.cfg.TLSGetCertificate != nil

	// Build our own http.Server so we can attach a TLS config.
	// We pass it to the SDK via WithHTTPServer so that sseSrv.Shutdown()
	// can drain sessions and then call httpSrv.Shutdown internally.
	httpSrv := &http.Server{
		Addr:              s.cfg.Addr,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if useTLS {
		httpSrv.TLSConfig = &tls.Config{
			MinVersion:     tls.VersionTLS13,
			GetCertificate: s.cfg.TLSGetCertificate,
		}
	}

	sseSrv := server.NewSSEServer(s.mcp, server.WithHTTPServer(httpSrv))
	// SSEServer implements http.Handler; wire it as the handler so that
	// both Start() (plaintext) and our direct ListenAndServeTLS (TLS) path
	// use the same SSE route mux.
	httpSrv.Handler = sseSrv

	slog.Info("mcp server starting", slog.String("transport", "sse"), slog.String("addr", s.cfg.Addr),
		slog.Bool("tls", useTLS))

	errCh := make(chan error, 1)
	go func() {
		var serveErr error
		if useTLS {
			// ListenAndServeTLS with empty cert/key files — the keypair is
			// supplied exclusively via TLSConfig.GetCertificate.
			serveErr = httpSrv.ListenAndServeTLS("", "")
		} else {
			slog.Warn("mcp sse: starting plaintext (LONGUE_VUE_MCP_ALLOW_PLAINTEXT=true) — bearer tokens are NOT protected on the wire",
				slog.String("addr", s.cfg.Addr))
			serveErr = httpSrv.ListenAndServe()
		}
		if !errors.Is(serveErr, http.ErrServerClosed) {
			errCh <- serveErr
		} else {
			errCh <- nil
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("mcp server shutting down (sse)")
		shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if shutErr := sseSrv.Shutdown(shutCtx); shutErr != nil {
			slog.Warn("mcp sse shutdown error", slog.Any("error", shutErr))
		}
		return fmt.Errorf("mcp server: %w", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("mcp sse serve: %w", err)
		}
		return nil
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
	if s.isStdio {
		return s.checkAccessStdio(ctx)
	}
	return s.checkAccessSSE(ctx, request.Header.Get("Authorization"))
}

// checkAccessStdio resolves the caller for stdio transport (no per-request
// auth) and applies the shared-bucket rate limit.
func (s *Server) checkAccessStdio(ctx context.Context) (context.Context, error) {
	caller := &Caller{Name: "mcp-stdio"}
	if s.stdioCaller != nil {
		caller = s.stdioCaller
	}
	ctx = context.WithValue(ctx, ctxKeyCaller{}, caller)
	if s.cfg.RateLimiter != nil && !s.cfg.RateLimiter.Allow(ctx, "stdio") {
		return ctx, errRateLimited
	}
	return ctx, nil
}

// checkAccessSSE validates the bearer token on the SSE transport and applies
// per-token rate limiting.
func (s *Server) checkAccessSSE(ctx context.Context, header string) (context.Context, error) {
	token := stripBearer(header)
	if token == "" {
		return ctx, errUnauthorized
	}
	caller, err := s.cfg.Auth(ctx, token)
	if err != nil {
		return ctx, err
	}
	ctx = context.WithValue(ctx, ctxKeyCaller{}, caller)
	if s.cfg.RateLimiter != nil && caller != nil && caller.TokenID != nil {
		if !s.cfg.RateLimiter.Allow(ctx, caller.TokenID.String()) {
			return ctx, errRateLimited
		}
	}
	return ctx, nil
}

// stripBearer removes a leading "Bearer " prefix from an Authorization header
// value. Returns the input unchanged when no prefix is present.
func stripBearer(token string) string {
	if len(token) > 7 && token[:7] == "Bearer " {
		return token[7:]
	}
	return token
}

// recordCheckAccessFailure records either a 401 (auth denial) or 429
// (rate limit) audit row based on the error type, then returns the
// error wrapped as an MCP tool result. It should be called immediately
// after checkAccess returns an error, before installing the deferred
// finish handler.
func (s *Server) recordCheckAccessFailure(ctx context.Context, tool string, args map[string]any, err error) *mcp.CallToolResult {
	if errors.Is(err, errRateLimited) {
		s.recordRateLimit(ctx, tool, args)
	} else {
		s.recordDenial(ctx, tool, args)
	}
	return mcp.NewToolResultError(err.Error())
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
