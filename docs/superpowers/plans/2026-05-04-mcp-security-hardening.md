# MCP Security Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the MCP server (`internal/mcp/`, `:8090`) up to the same security baseline as the public REST listener (ADR-0017), closing the 11 findings in `docs/audits/2026-05-04-mcp-security-baseline.md`.

**Architecture:** Wrap the SSE listener with native TLS (cert reloader reused from `cmd/longue-vue/main.go`); insert audit, rate-limit, and bounded-LRU auth-cache layers around `Server.checkAccess`; emit `audit_events` rows with `source="mcp"`; default-bind to loopback; replace ad-hoc scope check with `auth.HasScope`; verify stdio token at startup. New ADR-0021 documents the posture so the audit doc has a stable cross-reference.

**Tech Stack:** Go 1.23, `github.com/mark3labs/mcp-go`, `crypto/subtle`, `golang.org/x/time/rate`, existing `internal/auth`, `internal/httputil`, `internal/api` audit primitives. No new external deps.

---

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `internal/mcp/server.go` | modify | Add TLS + cert-reload to SSE; default loopback bind; integrate auth/audit/rate-limit hooks via `Config` |
| `internal/mcp/auth_cache.go` | create | Bounded LRU + negative cache + revocation channel for verified PATs |
| `internal/mcp/audit.go` | create | `recordToolCall(ctx, store, caller, tool, args, outcome)` writes one `audit_events` row with `source="mcp"`; never returns error to caller |
| `internal/mcp/ratelimit.go` | create | Per-token + per-IP token-bucket limiter |
| `internal/mcp/tools.go` | modify | Tool handlers call `recordToolCall` on every invocation; replace `fmt.Errorf` leakage in auth path with masked errors |
| `internal/mcp/server_test.go` | modify | Add e2e tests booting an SSE listener over TLS, presenting revoked / unscoped / rate-limited tokens |
| `cmd/longue-vue/main.go` | modify | Wire TLS env vars, cert reloader, `auth.HasScope`, stdio-token verification, revocation channel |
| `internal/auth/middleware.go` (or wherever `RevokeAPIToken` lives) | modify | Publish revocations on a process-wide channel the MCP cache subscribes to |
| `docs/adr/adr-0021-mcp-security-baseline.md` | create | Document the resulting posture (TLS-required default, audit trail, revocation latency target) |
| `charts/longue-vue/values.yaml` + templates | modify | Helm: MCP TLS Secret ref, loopback default, optional NetworkPolicy egress allow |
| `CLAUDE.md` | modify | Update the `internal/mcp/` paragraph to reflect the new posture |

---

## Task 1: Audit logging for MCP tool calls (CRIT-02)

**Files:**
- Create: `internal/mcp/audit.go`
- Modify: `internal/mcp/server.go` (add `AuditRecorder` field to `Config`)
- Modify: `internal/mcp/tools.go` (call recorder in every handler)
- Modify: `cmd/longue-vue/main.go` (inject the recorder)
- Test: `internal/mcp/server_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/mcp/server_test.go
func TestToolCallEmitsAuditEvent(t *testing.T) {
    fake := newFakeStore(t)
    var recorded []auditCall
    cfg := Config{
        Transport: "stdio",
        AuditRecorder: func(ctx context.Context, c auditCall) { recorded = append(recorded, c) },
    }
    s := NewServer(fake, nil, cfg)
    _, err := s.handleListClusters(context.Background(), mcp.CallToolRequest{})
    if err != nil { t.Fatal(err) }
    if len(recorded) != 1 { t.Fatalf("want 1 audit row, got %d", len(recorded)) }
    if recorded[0].Tool != "list_clusters" { t.Errorf("tool = %q", recorded[0].Tool) }
    if recorded[0].Source != "mcp" { t.Errorf("source = %q", recorded[0].Source) }
}
```

- [ ] **Step 2: Run, confirm fail**

`go test ./internal/mcp -run TestToolCallEmitsAuditEvent` → FAIL (`Config.AuditRecorder undefined`).

- [ ] **Step 3: Add type + Config field**

```go
// internal/mcp/audit.go
package mcp

import "context"

type auditCall struct {
    Source    string
    Tool      string
    CallerID  string
    Scopes    []string
    Args      map[string]string // UUIDs only — substring args are summarised as "<set>"/"<unset>"
    Outcome   string            // "ok" | "denied" | "error"
    Err       string            // generic; never the raw store error
}

type AuditRecorder func(ctx context.Context, c auditCall)
```

Add to `Config` in `server.go`:

```go
AuditRecorder AuditRecorder
```

- [ ] **Step 4: Wire recorder into every tool handler**

Add helper in `tools.go`:

```go
func (s *Server) record(ctx context.Context, tool string, args map[string]string, outcome, errStr string) {
    if s.cfg.AuditRecorder == nil { return }
    caller, _ := callerFromContext(ctx) // populated by checkAccess
    s.cfg.AuditRecorder(ctx, auditCall{
        Source: "mcp", Tool: tool,
        CallerID: caller.ID, Scopes: caller.Scopes,
        Args: args, Outcome: outcome, Err: errStr,
    })
}
```

Call at the end of each handler (replace existing `defer metrics.Observe...` with one that also records). Outcome is `"denied"` if `checkAccess` failed, `"error"` on store error, `"ok"` otherwise.

- [ ] **Step 5: Bind in `main.go`**

```go
cfg := argmcp.Config{
    Transport: transport, Addr: addr, Token: token, Auth: authFn,
    AuditRecorder: func(ctx context.Context, c argmcp.AuditCall) {
        if err := s.InsertAuditEvent(ctx, api.AuditEvent{
            Source: c.Source, Action: c.Tool,
            ActorTokenID: c.CallerID, Scopes: c.Scopes,
            Metadata: c.Args, Outcome: c.Outcome,
        }); err != nil {
            slog.Error("mcp audit insert failed", slog.Any("error", err))
        }
    },
}
```

(If `InsertAuditEvent` does not yet exist on `Store`, wrap whatever the existing `AuditMiddleware` calls; do not re-implement.)

- [ ] **Step 6: Run all mcp tests**

`make test-one TEST=TestToolCallEmitsAuditEvent` then `go test ./internal/mcp/...` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/audit.go internal/mcp/server.go internal/mcp/tools.go internal/mcp/server_test.go cmd/longue-vue/main.go
git commit -m "feat(mcp): emit audit_events for every tool call (source=mcp)"
```

---

## Task 2: Replace open-coded scope check with `auth.HasScope` (HIGH-04)

**Files:**
- Modify: `cmd/longue-vue/main.go:1143-1152`
- Test: `internal/mcp/server_test.go`

- [ ] **Step 1: Write failing test (vm-collector PAT must be denied)**

```go
func TestVMCollectorTokenDeniedFromMCP(t *testing.T) {
    authFn := buildAuthFn(t, fakeTokenStore{
        scopes: []string{"vm-collector"},
        prefix: "abcd1234", full: "longue_vue_pat_abcd1234_xxx",
    })
    err := authFn(context.Background(), "longue_vue_pat_abcd1234_xxx")
    if err == nil { t.Fatal("vm-collector token must be denied") }
}
```

- [ ] **Step 2: Run, confirm fail** (current code accepts because vm-collector → !read && !admin → already denied; if the test passes accidentally, change scopes to `[]string{"admin","vm-collector"}` to flush out the drift risk).

- [ ] **Step 3: Replace logic**

```go
// main.go
import "github.com/sthalbert/longue-vue/internal/auth"

if !auth.HasScope(tok.Scopes, "read") {
    return errors.New("token lacks read scope")
}
```

Delete the manual `for _, scope := range tok.Scopes` loop.

- [ ] **Step 4: Run, PASS.**

- [ ] **Step 5: Commit**

```bash
git commit -am "refactor(mcp): use auth.HasScope for read-scope check (HIGH-04)"
```

---

## Task 3: Bounded LRU + revocation invalidation for the auth cache (HIGH-01, HIGH-03, MED-03)

**Files:**
- Create: `internal/mcp/auth_cache.go`
- Modify: `cmd/longue-vue/main.go` (use new cache; subscribe to revocation channel)
- Modify: `internal/store/pg_auth.go::RevokeAPIToken` (publish on channel)
- Test: `internal/mcp/auth_cache_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/mcp/auth_cache_test.go
package mcp

import (
    "testing"
    "time"
)

func TestAuthCacheLRUEvicts(t *testing.T) {
    c := newAuthCache(2, 5*time.Minute)
    c.Put("a", "fullA")
    c.Put("b", "fullB")
    c.Put("c", "fullC")
    if c.Get("a", "fullA") { t.Error("a should be evicted") }
    if !c.Get("b", "fullB") { t.Error("b should remain") }
}

func TestAuthCacheInvalidate(t *testing.T) {
    c := newAuthCache(8, 5*time.Minute)
    c.Put("a", "fullA")
    c.Invalidate("a")
    if c.Get("a", "fullA") { t.Error("invalidated entry returned") }
}

func TestAuthCacheConstantTimeCompare(t *testing.T) {
    c := newAuthCache(8, 5*time.Minute)
    c.Put("a", "fullA")
    if c.Get("a", "fullB") { t.Error("wrong full token must not hit") }
}
```

- [ ] **Step 2: Run, confirm fail** (`newAuthCache undefined`).

- [ ] **Step 3: Implement**

```go
// internal/mcp/auth_cache.go
package mcp

import (
    "container/list"
    "crypto/subtle"
    "sync"
    "time"
)

type authCacheEntry struct {
    prefix     string
    fullToken  string
    validUntil time.Time
    elem       *list.Element
}

type authCache struct {
    mu    sync.Mutex
    cap   int
    ttl   time.Duration
    items map[string]*authCacheEntry
    lru   *list.List
}

func newAuthCache(capacity int, ttl time.Duration) *authCache {
    return &authCache{cap: capacity, ttl: ttl, items: make(map[string]*authCacheEntry), lru: list.New()}
}

func (c *authCache) Get(prefix, full string) bool {
    c.mu.Lock(); defer c.mu.Unlock()
    e, ok := c.items[prefix]
    if !ok || time.Now().After(e.validUntil) { return false }
    if subtle.ConstantTimeCompare([]byte(e.fullToken), []byte(full)) != 1 { return false }
    c.lru.MoveToFront(e.elem)
    return true
}

func (c *authCache) Put(prefix, full string) {
    c.mu.Lock(); defer c.mu.Unlock()
    if e, ok := c.items[prefix]; ok {
        e.fullToken = full
        e.validUntil = time.Now().Add(c.ttl)
        c.lru.MoveToFront(e.elem)
        return
    }
    e := &authCacheEntry{prefix: prefix, fullToken: full, validUntil: time.Now().Add(c.ttl)}
    e.elem = c.lru.PushFront(e)
    c.items[prefix] = e
    if c.lru.Len() > c.cap {
        oldest := c.lru.Back()
        if oldest != nil {
            ev := oldest.Value.(*authCacheEntry)
            c.lru.Remove(oldest)
            delete(c.items, ev.prefix)
        }
    }
}

func (c *authCache) Invalidate(prefix string) {
    c.mu.Lock(); defer c.mu.Unlock()
    if e, ok := c.items[prefix]; ok {
        c.lru.Remove(e.elem)
        delete(c.items, prefix)
    }
}
```

- [ ] **Step 4: Run, PASS.**

- [ ] **Step 5: Wire revocation channel**

In `internal/store/pg_auth.go`, add a process-wide `chan string` that `RevokeAPIToken` writes to (token prefix). In `main.go`:

```go
cache := argmcp.NewAuthCache(1024, 30*time.Second) // 30s TTL — see audit HIGH-01
go func() {
    for prefix := range s.RevocationChan() {
        cache.Invalidate(prefix)
    }
}()
```

Replace existing inline cache with `cache.Get` / `cache.Put`.

- [ ] **Step 6: Add test that revocation invalidates cache**

```go
func TestRevocationInvalidatesMCPCache(t *testing.T) { /* boot store, mint token, hit MCP, revoke, hit again — expect deny */ }
```

- [ ] **Step 7: Commit**

```bash
git commit -am "feat(mcp): bounded LRU auth cache with revocation invalidation (HIGH-01/03, MED-03)"
```

---

## Task 4: Native TLS on the SSE listener with hot cert reload (CRIT-01, MED-02)

**Files:**
- Modify: `internal/mcp/server.go::runSSE`
- Modify: `cmd/longue-vue/main.go::maybeStartMCPServer`
- Modify: `cmd/longue-vue/main.go` (extend cert reloader to take MCP cert paths)
- Test: `internal/mcp/server_test.go`

- [ ] **Step 1: Write failing test** — boot SSE on `127.0.0.1:0` with a self-signed cert, call a tool over TLS, expect success; call without TLS, expect connection refused.

- [ ] **Step 2: Run, confirm fail.**

- [ ] **Step 3: Add TLS fields to `Config`**

```go
type Config struct {
    // …existing…
    TLSCertFile string
    TLSKeyFile  string
    GetCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error) // hot reload
    AllowPlaintext bool // explicit opt-in if no TLS
}
```

- [ ] **Step 4: Rewrite `runSSE`**

```go
func (s *Server) runSSE(ctx context.Context) error {
    sseSrv := server.NewSSEServer(s.mcp)
    httpSrv := &http.Server{Addr: s.cfg.Addr, Handler: sseSrv}
    if s.cfg.GetCertificate != nil {
        httpSrv.TLSConfig = &tls.Config{
            MinVersion: tls.VersionTLS13,
            GetCertificate: s.cfg.GetCertificate,
        }
    } else if !s.cfg.AllowPlaintext {
        return errors.New("mcp sse: TLS not configured and LONGUE_VUE_MCP_ALLOW_PLAINTEXT not set — refusing to start")
    }
    errCh := make(chan error, 1)
    go func() {
        if httpSrv.TLSConfig != nil { errCh <- httpSrv.ListenAndServeTLS("", "") }
        else { errCh <- httpSrv.ListenAndServe() }
    }()
    select {
    case <-ctx.Done():
        _ = httpSrv.Shutdown(context.Background())
        return fmt.Errorf("mcp server: %w", ctx.Err())
    case err := <-errCh:
        return fmt.Errorf("mcp sse serve: %w", err)
    }
}
```

- [ ] **Step 5: Wire env vars in `main.go`**

```go
mcpCert := os.Getenv("LONGUE_VUE_MCP_TLS_CERT")
mcpKey  := os.Getenv("LONGUE_VUE_MCP_TLS_KEY")
allowPlain := os.Getenv("LONGUE_VUE_MCP_ALLOW_PLAINTEXT") == "true"
var getCert func(*tls.ClientHelloInfo) (*tls.Certificate, error)
if mcpCert != "" && mcpKey != "" {
    reloader, err := newCertReloader(mcpCert, mcpKey)
    if err != nil { return nil, fmt.Errorf("mcp tls: %w", err) }
    getCert = reloader.GetCertificate
}
cfg.TLSCertFile, cfg.TLSKeyFile, cfg.GetCertificate, cfg.AllowPlaintext = mcpCert, mcpKey, getCert, allowPlain
```

- [ ] **Step 6: Default-bind to loopback**

```go
addr := envOr("LONGUE_VUE_MCP_ADDR", "127.0.0.1:8090") // was ":8090"
```

- [ ] **Step 7: Run tests, PASS.**

- [ ] **Step 8: Commit**

```bash
git commit -am "feat(mcp): native TLS 1.3 with hot cert reload; default bind to loopback (CRIT-01, MED-02)"
```

---

## Task 5: Per-token + per-IP rate limiting (HIGH-02)

**Files:**
- Create: `internal/mcp/ratelimit.go`
- Modify: `internal/mcp/server.go` (Config field + apply in checkAccess)
- Test: `internal/mcp/ratelimit_test.go`

- [ ] **Step 1: Write failing test**

```go
func TestRateLimitPerToken(t *testing.T) {
    rl := newRateLimiter(2, 2) // 2 rps burst 2
    ctx := context.Background()
    if !rl.Allow(ctx, "tok-A") { t.Fatal("first call should pass") }
    if !rl.Allow(ctx, "tok-A") { t.Fatal("second should pass (burst)") }
    if rl.Allow(ctx, "tok-A") { t.Fatal("third should be limited") }
    if !rl.Allow(ctx, "tok-B") { t.Fatal("different key not affected") }
}
```

- [ ] **Step 2: Run, confirm fail.**

- [ ] **Step 3: Implement using `golang.org/x/time/rate`**

```go
// internal/mcp/ratelimit.go
package mcp

import (
    "context"
    "sync"
    "golang.org/x/time/rate"
)

type rateLimiter struct {
    mu  sync.Mutex
    lim map[string]*rate.Limiter
    rps rate.Limit
    burst int
}

func newRateLimiter(rps float64, burst int) *rateLimiter {
    return &rateLimiter{lim: map[string]*rate.Limiter{}, rps: rate.Limit(rps), burst: burst}
}

func (r *rateLimiter) Allow(_ context.Context, key string) bool {
    r.mu.Lock(); l, ok := r.lim[key]
    if !ok { l = rate.NewLimiter(r.rps, r.burst); r.lim[key] = l }
    r.mu.Unlock()
    return l.Allow()
}
```

(Add LRU eviction matching Task 3 if memory becomes a concern — defer for now, capped at expected token count.)

- [ ] **Step 4: Apply in `checkAccess`**

After auth succeeds, `if !s.cfg.RateLimiter.Allow(ctx, tokenID) { return errRateLimited }`. Map to `mcp.NewToolResultError("rate limit exceeded")` so the AI back-off handler fires correctly.

- [ ] **Step 5: Run, PASS.**

- [ ] **Step 6: Commit**

```bash
git commit -am "feat(mcp): per-token rate limiting on tool calls (HIGH-02)"
```

---

## Task 6: stdio token enforcement (MED-01)

**Files:**
- Modify: `internal/mcp/server.go::runStdio` — verify `cfg.Token` once before `ServeStdio`; reject if invalid; treat `cfg.Token == ""` as "process-level trust, log a loud warning".
- Modify: `internal/mcp/server.go::checkAccess` — when stdio path with token, reuse cached scope set instead of bypassing.
- Test: `internal/mcp/server_test.go::TestStdioTokenEnforced`

- [ ] **Step 1: Write failing test** asserting that an invalid `Config.Token` causes `runStdio` to return an error before serving.

- [ ] **Step 2: Implement**

```go
func (s *Server) runStdio(ctx context.Context) error {
    if s.cfg.Token != "" {
        if err := s.cfg.Auth(ctx, s.cfg.Token); err != nil {
            return fmt.Errorf("mcp stdio: invalid LONGUE_VUE_MCP_TOKEN: %w", err)
        }
        s.stdioAuthorized = true
    } else {
        slog.Warn("mcp stdio: no LONGUE_VUE_MCP_TOKEN set — caller inherits process trust")
    }
    // …existing serve loop…
}
```

In `checkAccess`, when `s.cfg.Auth != nil` (always true now after main.go change) and stdio: skip per-call auth if `s.stdioAuthorized` already proven; the runtime gate (`checkEnabled`) still applies.

- [ ] **Step 3: Run, PASS.**

- [ ] **Step 4: Commit**

```bash
git commit -am "feat(mcp): verify LONGUE_VUE_MCP_TOKEN at stdio startup (MED-01)"
```

---

## Task 7: Mask auth-path errors to client (MED-04)

**Files:**
- Modify: `cmd/longue-vue/main.go::authFn`
- Modify: `internal/mcp/server.go::checkAccess`

- [ ] **Step 1: Write failing test** that asserts the tool-result error string is exactly `"authentication failed"` (or `errUnauthorized.Error()`) for any auth-path failure, not the wrapped pgx error.

- [ ] **Step 2: Replace `fmt.Errorf("token lookup failed: %w", err)` with**

```go
slog.Warn("mcp token lookup failed", slog.Any("error", err))
return errUnauthorized
```

Same pattern for the `VerifyPassword` failure.

- [ ] **Step 3: Run, PASS.**

- [ ] **Step 4: Commit**

```bash
git commit -am "fix(mcp): mask auth-path errors before returning to client (MED-04)"
```

---

## Task 8: ADR-0021 + CLAUDE.md update + Helm

**Files:**
- Create: `docs/adr/adr-0021-mcp-security-baseline.md`
- Modify: `CLAUDE.md` — `internal/mcp/` paragraph
- Modify: `charts/longue-vue/values.yaml` + relevant template — `mcp.tls.existingSecret`, `mcp.bindAddress`, `mcp.networkPolicy`

- [ ] **Step 1: Draft ADR-0021** documenting: TLS-required default, audit `source="mcp"`, 30 s revocation latency budget, 1024-entry LRU, per-token 30 rps / burst 60, stdio token verification at startup, default loopback bind. Status: Accepted on merge.

- [ ] **Step 2: Update CLAUDE.md `internal/mcp/` bullet** to reference ADR-0021 and the TLS env vars.

- [ ] **Step 3: Helm values**

```yaml
mcp:
  enabled: false
  bindAddress: "127.0.0.1:8090"
  tls:
    existingSecret: ""           # required when bindAddress is non-loopback
  allowPlaintext: false
  networkPolicy:
    enabled: false
    allowedSources: []           # CIDR list, only honoured when networkPolicy.enabled
```

Template the Deployment env vars and a NetworkPolicy gated on `mcp.networkPolicy.enabled`.

- [ ] **Step 4: `make check`** — must pass.

- [ ] **Step 5: Commit**

```bash
git commit -am "docs(mcp): ADR-0021 baseline + CLAUDE.md + Helm wiring"
```

---

## Task 9: E2E coverage on the live SSE listener (LOW-01)

**Files:**
- Modify: `internal/mcp/server_test.go`

- [ ] **Step 1: Add table-driven test** that boots `runSSE` with a self-signed cert, then:
  - calls `list_clusters` with a valid token → expect 200 + non-empty result
  - calls with a revoked token (revoke between calls) → expect deny within 1 s of revocation
  - calls 200 times in a tight loop → expect `429`-equivalent on the configured limit
  - calls without `Authorization` → expect `errUnauthorized`
  - asserts that each call produced exactly one `audit_events` row via the recorder hook

- [ ] **Step 2: Run, PASS.**

- [ ] **Step 3: Commit**

```bash
git commit -am "test(mcp): e2e coverage for TLS, revocation, rate limit, audit trail"
```

---

## Self-review notes

- **Spec coverage:** All 11 audit findings map to a task — CRIT-01→T4, CRIT-02→T1, HIGH-01→T3, HIGH-02→T5, HIGH-03→T3, HIGH-04→T2, MED-01→T6, MED-02→T4, MED-03→T3, MED-04→T7, LOW-01→T9. LOW-02 (prompt-injection sanitisation) is intentionally deferred to a future ADR — flag in T8 ADR-0021 "Out of scope" section.
- **Type consistency:** `auditCall`/`AuditCall` capitalisation — kept lowercase inside package, exported alias used in `main.go` (T1 mentions `argmcp.AuditCall`; ensure the type is exported as `AuditCall` when referenced from outside the package).
- **Sequencing:** T1 and T2 are independent and small — do them first to establish the audit trail before behaviour changes. T3 requires the revocation channel; if `RevokeAPIToken` cannot be modified in scope, fall back to a 30 s TTL only and log the residual risk. T4 is the longest task and has the highest blast radius; commit it on a feature flag in `main.go` (`if mcpCert != "" || allowPlain`) so revert is one env-var flip.

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-04-mcp-security-hardening.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
