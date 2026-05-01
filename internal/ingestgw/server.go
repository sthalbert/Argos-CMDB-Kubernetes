package ingestgw

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Config wires the gateway. All fields are required unless marked
// otherwise. Validate via Config.Check().
type Config struct {
	// UpstreamBaseURL is longue-vue's ingest listener URL, e.g.
	// "https://longue-vue-ingest.longue-vue.svc.cluster.local:8443". No
	// trailing slash; must include scheme.
	UpstreamBaseURL string

	// UpstreamHost optionally overrides the Host: header sent to
	// longue-vue. Useful when the upstream service hostname differs from
	// the cert SAN. Empty = use the host from UpstreamBaseURL.
	UpstreamHost string

	// UpstreamClient is the *http.Client used for forwarded writes
	// AND verify calls. Must be configured with mTLS via the cert
	// reloader's GetClientCertificate callback. Must enforce a
	// reasonable per-request timeout (the gateway does not impose one
	// on top — the client's Timeout is the budget).
	UpstreamClient *http.Client

	// MaxBodyBytes caps the inbound request body size. Requests
	// exceeding this are rejected with 413 before any upstream call.
	MaxBodyBytes int64

	// CacheConfig governs the verify-result cache. Pass
	// DefaultCacheConfig() unless tuning.
	CacheConfig CacheConfig

	// RequiredScope is the scope a token must declare to be allowed
	// through the gateway. ADR-0016 ships with "write" — the K8s
	// push collector's scope. Tokens with admin imply write per
	// CachedToken.HasScope. Empty disables the gateway-side scope
	// check entirely (longue-vue still re-validates).
	RequiredScope string

	// ReadyzCheck is a function that returns nil when the gateway is
	// ready to serve traffic (cert loaded and not too close to
	// expiry, upstream reachable). Called from /readyz; must be
	// fast. Pass nil for "always ready" in tests.
	ReadyzCheck func(ctx context.Context) error

	// Logger receives structured request logs. Pass slog.Default()
	// for production use.
	Logger *slog.Logger
}

// Check validates a Config.
func (c *Config) Check() error {
	if c.UpstreamBaseURL == "" {
		return fmt.Errorf("ingestgw: UpstreamBaseURL is required") //nolint:err113 // local validation error, not compared by callers
	}
	if c.UpstreamClient == nil {
		return fmt.Errorf("ingestgw: UpstreamClient is required") //nolint:err113 // local validation error, not compared by callers
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("ingestgw: MaxBodyBytes must be positive") //nolint:err113 // local validation error, not compared by callers
	}
	return nil
}

// Server is the gateway's HTTP handler factory. It owns the verify cache
// and the verify client; both are shared across every concurrent
// request.
type Server struct {
	upstreamBaseURL string
	upstreamHost    string
	upstream        *http.Client
	maxBodyBytes    int64
	cache           *Cache
	verifyClient    *VerifyClient
	requiredScope   string
	readyzCheck     func(ctx context.Context) error
	logger          *slog.Logger
}

// NewServer constructs a gateway Server from a validated Config, wiring
// the verify cache, the verify client, and the upstream HTTP transport.
// Returns an error when cfg.Check() fails (missing required fields).
// Prefer calling Config.Check() before NewServer to get field-level error
// detail; NewServer calls Check internally as a safeguard.
//
//nolint:gocritic // hugeParam: Config is 104 bytes; pass-by-value is intentional for immutability at the call site
func NewServer(
	cfg Config,
) (*Server, error) {
	if err := cfg.Check(); err != nil {
		return nil, err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	host := cfg.UpstreamHost
	if host == "" {
		// Strip scheme to keep the host header host-only — net/http
		// will set the Host based on the URL otherwise, but operators
		// may want to override (cert SAN drift). Empty = default.
		host = ""
	}
	return &Server{
		upstreamBaseURL: strings.TrimRight(cfg.UpstreamBaseURL, "/"),
		upstreamHost:    host,
		upstream:        cfg.UpstreamClient,
		maxBodyBytes:    cfg.MaxBodyBytes,
		cache:           NewCache(cfg.CacheConfig),
		verifyClient:    NewVerifyClient(cfg.UpstreamClient, strings.TrimRight(cfg.UpstreamBaseURL, "/")),
		requiredScope:   cfg.RequiredScope,
		readyzCheck:     cfg.ReadyzCheck,
		logger:          cfg.Logger,
	}, nil
}

// Handler returns the HTTP handler that serves the gateway's inbound
// listener: GET /healthz, GET /readyz, plus every (method, path) in
// Routes. Anything else returns 404.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	for _, r := range Routes {
		// All routes share the same handler; matchAllowlist resolves
		// the route label inside.
		mux.HandleFunc(r.Method+" "+r.Pattern, s.handleProxy)
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.readyzCheck == nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}
	if err := s.readyzCheck(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleProxy is the workhorse. Resolves the allowlist, verifies the
// bearer token (cache → singleflight verify → cache), enforces scope,
// then forwards to longue-vue.
//
//nolint:gocyclo // central request dispatcher; flat is better than nested helpers
func (s *Server) handleProxy(
	w http.ResponseWriter,
	r *http.Request,
) {
	start := time.Now()
	inflightRequests.Inc()
	defer inflightRequests.Dec()

	route, ok := matchAllowlist(r.Method, r.URL.Path)
	if !ok {
		// Should be unreachable — ServeMux only dispatches Routes here.
		// Treat as a configuration bug. 404 to avoid revealing internals.
		s.respond(w, r, "", http.StatusNotFound, OutcomeDeniedPath, start, "route not allowed", nil)
		return
	}

	// Body cap. Snapshot the body so we can forward it after auth
	// without re-reading from a closed reader.
	limited := http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		s.respond(w, r, route, http.StatusRequestEntityTooLarge, OutcomeDeniedBody, start,
			"request body exceeded MaxBodyBytes", err)
		return
	}
	observeBody(route, len(body))
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	// Bearer extraction.
	token, ok := extractBearer(r)
	if !ok {
		s.respond(w, r, route, http.StatusUnauthorized, OutcomeDeniedToken, start,
			"missing Authorization header", nil)
		return
	}

	// Cached / singleflight-deduped verify.
	entry, err := s.cache.SingleflightGet(r.Context(), token, func(ctx context.Context) (CachedToken, time.Time, error) {
		return s.verifyClient.Verify(ctx, token)
	})
	if err != nil {
		if errors.Is(err, ErrVerifyDenied) {
			s.respond(w, r, route, http.StatusUnauthorized, OutcomeDeniedToken, start,
				"token verify denied", nil)
			return
		}
		// Transport / 5xx — longue-vue is unreachable. Don't fail-open;
		// don't cache; let the collector retry.
		s.respond(w, r, route, http.StatusServiceUnavailable, OutcomeUpstreamError, start,
			"verify call failed", err)
		return
	}
	if !entry.Valid {
		// Cached negative result.
		s.respond(w, r, route, http.StatusUnauthorized, OutcomeDeniedToken, start,
			"cached invalid token", nil)
		return
	}

	// Scope check — gateway-side short-circuit. longue-vue re-checks too.
	if s.requiredScope != "" && !entry.HasScope(s.requiredScope) {
		s.respond(w, r, route, http.StatusForbidden, OutcomeDeniedScope, start,
			"token lacks required scope", nil)
		return
	}

	// Forward.
	upstreamStatus, upErr := s.proxyRequest(r.Context(), w, r, route, body)
	if upErr != nil {
		s.respond(w, r, route, upstreamStatus, upstreamOutcome(upErr), start,
			"upstream error", upErr)
		return
	}
	// longue-vue 401 means the token was revoked between our cache hit and
	// now. Drop the cache entry so the next request re-verifies.
	if upstreamStatus == http.StatusUnauthorized {
		s.cache.Invalidate(token)
	}
	// observeRequest will run via respond().
	s.respond(w, r, route, upstreamStatus, OutcomeAllowed, start, "", nil)
}

// upstreamOutcome maps a proxy error to the metrics outcome label.
func upstreamOutcome(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeUpstreamTimeout
	}
	return OutcomeUpstreamError
}

// extractBearer pulls the bearer token from the Authorization header.
// Returns ok=false if missing or malformed.
func extractBearer(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	tok := strings.TrimPrefix(h, prefix)
	if tok == "" {
		return "", false
	}
	return tok, true
}

// respond emits one structured log line, records request metrics, and
// (when the response hasn't been written yet) writes the status. For
// responses already written by proxyRequest the status arg matches what
// was written so metrics line up.
//
// On terminal errors before the upstream call we write a tiny problem
// JSON body so collectors see a structured error code, not an empty
// stream.
func (s *Server) respond(
	w http.ResponseWriter,
	r *http.Request,
	route string,
	status int,
	outcome string,
	start time.Time,
	reason string,
	err error,
) {
	// Only write the response for outcomes that didn't already get one
	// from proxyRequest (which streams longue-vue's body verbatim).
	switch outcome {
	case OutcomeAllowed, OutcomeUpstreamError, OutcomeUpstreamTimeout:
		// Body already written (or partially written) by proxyRequest.
	default:
		writeProblem(w, status, reason)
	}

	dur := time.Since(start)
	observeRequest(r.Method, route, status, outcome, dur)

	logTokenPrefix := ""
	if t, ok := extractBearer(r); ok {
		logTokenPrefix = tokenPrefixFor(t)
	}
	level := slog.LevelInfo
	if outcome != OutcomeAllowed {
		level = slog.LevelWarn
	}
	s.logger.LogAttrs(r.Context(), level, "ingest gw request",
		slog.String("method", r.Method),
		slog.String("route", route),
		slog.Int("status", status),
		slog.String("outcome", outcome),
		slog.String("client_ip", clientIP(r)),
		slog.String("envoy_request_id", r.Header.Get("X-Request-Id")),
		slog.String("token_prefix", logTokenPrefix),
		slog.Int64("body_bytes", r.ContentLength),
		slog.Duration("duration", dur),
		slog.String("reason", reason),
		slog.Any("error", err),
	)
}

// writeProblem emits a tiny RFC 7807 problem body. Mirrors longue-vue's
// shape so collectors can parse one error model regardless of which
// node in the chain rejected the request.
func writeProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{
		"type":   "about:blank",
		"title":  http.StatusText(status),
		"status": status,
	}
	if detail != "" {
		body["detail"] = detail
	}
	_ = json.NewEncoder(w).Encode(body) //nolint:errchkjson // best-effort response write; connection may already be closed
}

// tokenPrefixFor returns the first 8 hex characters of sha256(token) —
// useful for log correlation with longue-vue's audit log without exposing
// the token itself. (Differs from the longue-vue-stored prefix, which is
// the literal first 8 chars of the token after the lv_pat_ prefix;
// the gateway logs the hash prefix because it never sees the token's
// stored canonical form.)
func tokenPrefixFor(token string) string {
	if token == "" {
		return ""
	}
	full := keyOf(token)
	if len(full) >= 8 {
		return full[:8]
	}
	return full
}

// AppendCertPoolFromPEM parses PEM-encoded certificates from pem and
// returns a new cert pool containing them. Returns an error when no
// certificate block is found — a common misconfiguration when the CA
// bundle path is set but the file is empty or not yet populated by
// Vault Agent / cert-manager. Used by cmd/longue-vue-ingest-gw/main.go to
// assemble the upstream CA bundle from the operator-provided file.
func AppendCertPoolFromPEM(pem []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("no PEM certificates parsed from input") //nolint:err113 // local sentinel, not compared by callers
	}
	return pool, nil
}
