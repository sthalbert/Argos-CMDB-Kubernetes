---
title: "ADR-0017: Public listener TLS posture and proxy trust model"
status: "Accepted"
date: "2026-04-29"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "security", "tls", "hsts", "cookies", "rate-limiting", "proxy", "secnumcloud", "pentest"]
supersedes: ""
superseded_by: ""
---

# ADR-0017: Public listener TLS posture and proxy trust model

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

## Context

A penetration test against an argosd deployment on 2026-04-28 found four exploitable issues that all stem from two related design gaps in the public listener (`:8080`, served by `cmd/argosd/main.go`):

1. **No TLS on the public listener.** `srv.ListenAndServe()` runs plaintext HTTP. The pentest captured `POST /v1/auth/login` credentials from a wire trace (AUTH-VULN-01), confirmed session cookies ride the same plaintext connection without the `Secure` flag (AUTH-VULN-02), and confirmed HSTS never reaches HTTP clients (AUTH-VULN-03) — the existing HSTS-emit logic in `internal/api/security_headers.go:16` requires `r.TLS != nil` or `X-Forwarded-Proto: https`, both false on this deployment.

2. **Unconditional trust of `X-Forwarded-For` and `X-Forwarded-Proto`.** `internal/api/auth_handlers.go:925` reads the leftmost XFF value from any peer. The login rate limiter (`/v1/auth/login`, 5 req/min per IP) keys on this value, so cycling fake XFF IPs gives an attacker an unlimited number of fresh rate-limit buckets (AUTH-VULN-04). The same uncontrolled trust applies to `X-Forwarded-Proto`, which feeds the HSTS-emit decision and the `Secure`-flag decision in `internal/auth/session.go:78`.

The existing transport-related primitives are already partially correct:

- `SecureCookiePolicy` (`internal/auth/session.go:23`) supports `auto`, `always`, `never` via `ARGOS_SESSION_SECURE_COOKIE`, and `secureFlag` already returns `true` when `r.TLS != nil` or `X-Forwarded-Proto: https`.
- `SecurityHeadersMiddleware` already emits HSTS, just with the same TLS-presence condition.
- The DMZ ingest listener (ADR-0016, `cmd/argosd/main.go:452-535`) already runs TLS 1.3 with hot certificate reload via `internal/ingestgw/tls_reload.go`.

The code is shaped correctly for "argosd behind a TLS terminator" but the project ships no opinionated guidance to operators about which deployment shapes are acceptable, refuses nothing at startup, and trusts every peer's headers equally. The pentest deployment was direct-exposed on `:8080` with no proxy, no TLS, and default `auto` cookie policy — the worst case all three vulnerabilities target.

This ADR also covers the proxy-trust gap behind AUTH-VULN-04 because the fix shares a configuration surface (`ARGOS_TRUSTED_PROXIES`) and a code path (`clientIP()` and a sibling `isHTTPS()`) with the TLS-posture work. Splitting them into two ADRs would force the same trusted-proxy primitive to be designed twice.

It does **not** cover the missing last-admin invariant guard called out in the same pentest (AUTHZ-VULN-01, -02). That is a defect against the existing authz model — the `CountActiveAdmins()` helper exists in the store but the delete and patch handlers don't call it — not an architectural change. The fix lands in the same delivery branch as a bug fix without an ADR.

## Decision

**The public listener gains an opt-in native-TLS mode, an explicit trusted-proxy CIDR list that gates every read of `X-Forwarded-For` and `X-Forwarded-Proto`, and a startup guard that refuses to come up in a transport posture that re-creates the pentest finding. All three knobs default off so existing deployments and dev workflows are unchanged.**

### 1. Native TLS on the public listener

Two new env vars opt argosd into TLS termination on its public listener:

| Variable | Purpose |
|---|---|
| `ARGOS_PUBLIC_LISTEN_TLS_CERT` | Path to PEM-encoded server certificate. |
| `ARGOS_PUBLIC_LISTEN_TLS_KEY` | Path to PEM-encoded private key. |

When both are set, `cmd/argosd/main.go` switches the public listener from `srv.ListenAndServe()` to `srv.ListenAndServeTLS("", "")` with a `tls.Config` that:

- Sets `MinVersion: tls.VersionTLS13`.
- Sources the certificate via the existing private `newCertReloader` helper in `cmd/argosd/main.go` — already used by the ADR-0016 ingest listener. mtime-poll on every handshake; sufficient for the public listener's expected rotation cadence (cert-manager rewrites the file once per renewal). The fsnotify-driven `internal/ingestgw.CertReloader` stays where it is — it serves the standalone gateway binary which sees more frequent Vault-Agent rotations.

When either variable is unset, the listener stays plain HTTP exactly as today. **There is only ever one public listener.** Argosd does not also bind a plaintext port to redirect to HTTPS — operators that need HTTP→HTTPS redirects do it at the ingress / network layer where it belongs, and an argosd that is direct-exposed in production is already a misconfiguration the §3 startup guard refuses.

### 2. Trusted proxy list and header trust gate

A new env var declares which immediate peers are allowed to speak `X-Forwarded-For` and `X-Forwarded-Proto`:

```
ARGOS_TRUSTED_PROXIES = "10.0.0.0/8,192.168.0.0/16,fd00::/8"
```

The value is a comma-separated CIDR list. Empty (the default) means **no peer is trusted**: XFF and XFP are ignored unconditionally. Both `127.0.0.1/32` and `::1/128` are NOT implicitly trusted — operators that want to allow a co-located proxy must list the loopback explicitly.

Two helpers in a new `internal/httputil/` package consume this list:

```go
// ClientIP returns the source IP for rate-limiting and audit logging.
// XFF is honored only when the immediate peer is in the trusted list,
// and the walk is right-to-left through the trusted prefix to find the
// first untrusted hop — that is the real client. Attackers can prepend
// arbitrary values to XFF but cannot append past trusted hops.
func ClientIP(r *http.Request, trusted []*net.IPNet) net.IP

// IsHTTPS returns true when the request actually arrived over TLS,
// either natively (r.TLS != nil) or via a TLS-terminating peer
// in the trusted list that set X-Forwarded-Proto: https.
func IsHTTPS(r *http.Request, trusted []*net.IPNet) bool
```

`ClientIP` replaces the package-private `clientIP()` in `internal/api/auth_handlers.go:928`. `IsHTTPS` is consumed by `internal/api/security_headers.go` (HSTS emission) and `internal/auth/session.go:78` (`secureFlag` in `SecureAuto` mode). Single source of truth, no inline duplications of the trust check.

### 3. `ARGOS_REQUIRE_HTTPS` startup guard

A third new env var forces a production posture:

```
ARGOS_REQUIRE_HTTPS = true   # default: false
```

When `true`, argosd refuses to start unless **at least one** of the following holds:

- `ARGOS_PUBLIC_LISTEN_TLS_CERT` and `_KEY` are both set (native TLS), **or**
- `ARGOS_TRUSTED_PROXIES` is non-empty **and** `ARGOS_SESSION_SECURE_COOKIE=always`.

The first branch covers airgap / k3s / "I have no ingress controller" deployments. The second branch covers the standard Kubernetes-Ingress shape, where a TLS terminator (nginx-ingress, Envoy, ALB) already exists and `X-Forwarded-Proto: https` will arrive — but only if the operator has explicitly listed the proxy CIDR.

When the guard is on and either branch is satisfied:

- HSTS emits **unconditionally** on every response. The trust check still gates whether the request itself is "HTTPS" for cookie purposes, but HSTS is a forward-looking policy directed at the browser; emitting it on a stripped HTTP response is harmless per the HSTS spec (browsers ignore it) and protective the next time the same browser visits over HTTPS.
- The session cookie's `Secure` flag is forced on regardless of the per-request `IsHTTPS` outcome. This is equivalent to `ARGOS_SESSION_SECURE_COOKIE=always` and is what the second branch of the guard already requires.

When the guard is off (the default), behavior is exactly today's: HSTS conditional on `IsHTTPS`, cookie policy obeyed as configured. Dev workflows (`make ui-dev`, local `:8080`) keep working.

### 4. Configuration surface — full list

| Variable | Default | Purpose | Affects |
|---|---|---|---|
| `ARGOS_PUBLIC_LISTEN_TLS_CERT` | unset | Server cert path. Both this and `_KEY` must be set together. | §1 |
| `ARGOS_PUBLIC_LISTEN_TLS_KEY` | unset | Server key path. | §1 |
| `ARGOS_TRUSTED_PROXIES` | unset | Comma-separated CIDR list. | §2 |
| `ARGOS_REQUIRE_HTTPS` | `false` | Enforce production transport posture. | §3 |

The existing `ARGOS_SESSION_SECURE_COOKIE` (`auto` / `always` / `never`) is unchanged in behavior but participates in the §3 guard. The existing `ARGOS_ADDR` is unchanged — it's the address argosd binds; the new `_TLS_CERT/_KEY` decide whether that bind is plaintext or TLS.

### 5. Per-vulnerability mapping

| Pentest finding | Closed by |
|---|---|
| AUTH-VULN-01 — plaintext credentials | §1 (native TLS) **or** operator-supplied terminator + §3 enforcement. |
| AUTH-VULN-02 — `Secure` flag absent | §3 forces `Secure=true` whenever the production-mode guard is satisfied. |
| AUTH-VULN-03 — HSTS missing | §3 emits HSTS unconditionally in production mode; §2 stops attacker-injected `X-Forwarded-Proto: https` from making HSTS appear to emit on plain HTTP. |
| AUTH-VULN-04 — XFF rate-limit bypass | §2 — `ClientIP` ignores XFF unless the peer is trusted; right-to-left walk returns the real client even with attacker prefix. |

### 6. Code touch list and refactor

- `cmd/argosd/main.go` — public listener config block, startup guard, env var parsing for the three new variables.
- `internal/httputil/` (new package) — `ClientIP`, `IsHTTPS`, `ParseTrustedProxies`. Pure functions, table-driven tests, no package dependencies beyond `net`, `net/http`, and `strings`.
- `internal/api/auth_handlers.go` — delete the package-private `clientIP()`, route the four call sites (`auth_handlers.go:367, 530`; audit and session logging) through `httputil.ClientIP`. The rate limiter struct gains a `trustedProxies []*net.IPNet` field; the `Server` constructor takes the parsed list.
- `internal/api/audit.go:151` — same migration.
- `internal/api/security_headers.go` — call `httputil.IsHTTPS`; honor a `requireHTTPS bool` field on the middleware to force HSTS.
- `internal/auth/session.go:78` — `secureFlag` calls `httputil.IsHTTPS`; the `SecureCookiePolicy` enum is unchanged but the `SecureAuto` branch consults the trusted-proxy list now via `IsHTTPS`.
- `charts/argos/values.yaml` — add `tls.publicListener.enabled`, `tls.publicListener.existingSecret`, `trustedProxies`, `requireHTTPS` keys with no-op defaults. Helm wires the env vars into the deployment.
- `docs/configuration.md` and `docs/how-to-deploy-*.md` — full operator-facing documentation of the four knobs and the production checklist.

### 7. Backward compatibility

Every new behavior is opt-in. With all four new env vars unset:

- Public listener stays plaintext HTTP on `ARGOS_ADDR`.
- `clientIP()` (now `httputil.ClientIP` with an empty trust list) returns `r.RemoteAddr` minus port — XFF is **ignored**, which is a behavior change from "trust XFF unconditionally" but is the *secure* default and is also the behavior pentest reports want. Operators who today deploy behind an Ingress and rely on XFF reaching argosd's audit log must add their proxy CIDR to `ARGOS_TRUSTED_PROXIES` to keep that behavior — this is documented as an upgrade note in the CHANGELOG.
- HSTS still emits when `r.TLS != nil` (so a deployment that already runs argosd behind a real terminator with mTLS-pass-through keeps emitting HSTS).
- Cookie `Secure` flag still follows `ARGOS_SESSION_SECURE_COOKIE` (default `auto`).
- Startup never fails on transport configuration.

The CHANGELOG entry for this feature carries a **prominent "behavior change" note** about XFF: the previous default of "trust XFF from any peer" is gone, and operators who relied on it for source-IP audit logging must declare their proxy CIDRs.

### 8. Threat model after the fix

The fix is targeted at the four pentest findings; out of scope:

- **A trusted-proxy that forwards attacker headers without sanitizing.** A misconfigured nginx that passes `X-Forwarded-For: <attacker>, <real-client>` is treated as authoritative because the peer (nginx) is trusted. This is the standard semantics — fixing it is the proxy operator's job, not argosd's. The CIDR list is the trust boundary; configure it carefully.
- **Direct-internet exposure with `ARGOS_REQUIRE_HTTPS=false`.** The guard is opt-in; operators can still ship the pentest topology if they explicitly want to. The README and docs/configuration.md make `ARGOS_REQUIRE_HTTPS=true` the recommended production setting; the SecNumCloud-aligned deployment path requires it.
- **TLS termination at a proxy without `X-Forwarded-Proto`.** If the proxy terminates TLS but doesn't set `X-Forwarded-Proto: https`, `IsHTTPS` returns `false`, HSTS doesn't emit, `Secure` cookie is not forced. The §3 guard's second branch insists on `ARGOS_SESSION_SECURE_COOKIE=always` precisely to make this case safe, but operators still need a proxy that sets the header for HSTS to work. Documented.

### 9. Observability

No new Prometheus metrics are required. The existing `argos_http_requests_total{...}` covers the listener; the listener mode is recorded at startup as a structured log line:

```
public_listener_mode=tls           tls_min_version=1.3   trusted_proxies=2  require_https=true
public_listener_mode=plaintext     trusted_proxies=0     require_https=false
```

The startup-guard refusal path emits a clear error to stderr citing the variable that needs to change, before exiting 1.

### 10. Testing strategy

- **`internal/httputil/` table-driven tests** — 20+ rows: empty trust list, single-CIDR list, multi-CIDR list, IPv4 + IPv6, peer-not-in-list ignores XFF, peer-in-list honors XFF, attacker-prefix XFF returns real client, malformed XFF returns peer, port stripping, `r.TLS != nil` short-circuits.
- **`internal/api/auth_handlers_test.go`** — rate limiter tests assert that a peer outside the trust list cannot bypass the limiter by cycling XFF (the pentest reproducer, encoded as a test).
- **`internal/api/security_headers_test.go`** — HSTS emit table: TLS-on-request, untrusted-peer-with-XFP, trusted-peer-with-XFP, `requireHTTPS=true` force-emit.
- **`cmd/argosd/main_test.go`** — startup-guard table: `requireHTTPS=true` + every combination of TLS / trusted-proxies / cookie-policy variables; expect refusal in the documented cases.
- **OpenAPI validation test** — none required; this ADR adds no spec changes.

## Consequences

### Positive

- All four pentest findings (AUTH-VULN-01 through -04) close on a single ADR-driven delivery.
- One trusted-proxy primitive serves XFF and XFP — operators learn it once and it applies everywhere headers are read from peers.
- Native TLS as a first-class option unblocks airgap / minimal-cluster deployments that don't have an ingress controller in front.
- The `ARGOS_REQUIRE_HTTPS=true` guard turns a runtime vulnerability into a deploy-time error, which is what SecNumCloud audits want to see.

### Negative

- XFF default-off is a behavior change for deployments that relied on argosd seeing the real client IP through an Ingress without `ARGOS_TRUSTED_PROXIES` being set. Audit logs on those deployments will start recording the proxy IP until operators add their CIDRs. Documented as an upgrade note; not silent.

### Neutral

- The four env vars are additive; nothing is removed. `ARGOS_SESSION_SECURE_COOKIE`'s three values are unchanged.
- No database migration. No OpenAPI change. No Helm-chart breaking change (all new keys default to no-op).

## Alternatives Considered

### Native TLS only, drop the trusted-proxy story

Force every operator to terminate TLS on argosd's listener and never trust peer headers at all. **Rejected** — this breaks the default Kubernetes Ingress model where a controller terminates TLS and forwards plain HTTP with `X-Forwarded-Proto: https`. Operators would have to terminate TLS twice (at the Ingress and at argosd) or run argosd with `hostNetwork: true`. The trusted-proxy story is well-trodden ground in every modern web framework; cutting it would surprise operators who reasonably expect `ARGOS_TRUSTED_PROXIES` to exist.

### Trusted-proxy story only, keep plaintext listener

Argue that TLS termination is always the proxy's job and ship the XFF / XFP fix without `ARGOS_PUBLIC_LISTEN_TLS_CERT`. **Rejected** — this leaves airgap and "I have no ingress" deployments with no first-class secure-by-default option. The DMZ ingest listener already proves the pattern (`internal/ingestgw/tls_reload.go`) and the marginal complexity of reusing it on the public listener is small.

### `ARGOS_REQUIRE_HTTPS` strict mode that refuses any non-TLS startup

Make the guard a single boolean: native TLS required, no trusted-proxy escape hatch. **Rejected** — operators with a perfectly-secure ingress termination shouldn't be forced to also terminate TLS at argosd. The two-branch guard (native TLS OR trusted-proxy + always-secure-cookie) is the minimum that captures both deployment shapes safely.

### Separate ADRs for TLS posture and XFF rate-limit fix

**Rejected** — both fixes share `ARGOS_TRUSTED_PROXIES` and the new `internal/httputil/` package. Splitting forces the same primitive to be designed twice, in slightly different ways, by reviewers reading two separate ADRs.

### Add `ARGOS_REQUIRE_HTTPS=true` as the default

Tempting from a SecNumCloud posture standpoint, but **rejected for this release** — it would break every existing dev and demo deployment that uses `make ui-dev` or `localhost:8080`. The CHANGELOG carries a deprecation-style notice that the default will flip in a future major version, giving operators a release cycle to opt in.

### Honor `127.0.0.1/32` as an implicit trusted proxy

Tempting because almost every "argosd behind a reverse proxy on the same pod" topology terminates on loopback. **Rejected** — implicit trust is exactly the class of bug this ADR is closing. Operators who want loopback trusted list it explicitly. The cost is one CIDR in a YAML file; the gain is a model that has no hidden-trust corners.

### Force the right-to-left XFF walk to be a single-trusted-hop check

Some implementations only allow XFF when the peer is the *single* known proxy and reject any XFF that contains multiple hops. **Rejected** — multi-hop XFF is real (CDN → ingress → argosd), and the right-to-left walk handles it correctly: peel off trusted hops from the right, return the first untrusted IP. Single-hop-only would be safer in a narrow sense but would force operators to flatten their proxy chain or lose source-IP fidelity.

## Implementation Notes

- The `internal/httputil/` package is intentionally small and dependency-free so it can be shared by both API handlers and the ingestgw allowlist (which today has its own `clientIP` in `internal/ingestgw/proxy.go:177` — that one IGNORES XFF on purpose, see §2 of ADR-0016, and that intent is preserved).
- `ParseTrustedProxies` returns `([]*net.IPNet, error)` with an empty slice for an unset / empty env var. Errors at parse time block startup with a clear message — no silent fallback to "trust everyone".
- The startup guard runs **after** all env-var parsing so it can produce a single error message that names every unsatisfied condition, not just the first one. Operators get a checklist, not a hunt.
- No package moves: argosd's existing `newCertReloader` helper (mtime-poll, `cmd/argosd/main.go:546`) covers both listeners; the standalone gateway's fsnotify reloader stays in `internal/ingestgw` because the two binaries have different rotation cadences and observability registries.

## Future work

- Flip `ARGOS_REQUIRE_HTTPS=true` to be the default in a future major release; deprecate the `false` path.
- Consider adding `ARGOS_PUBLIC_LISTEN_CLIENT_CA_FILE` to support optional client-cert auth on the public listener for the highest-tier SecNumCloud customers, paralleling the ingest listener's mTLS posture.
- Investigate whether the rate limiter should also burst-limit on (`username`, time-window) tuples in addition to source IP, so that even a correctly-trusted origin can't password-spray a single account. This is an additional defense layer; not strictly needed once §2 closes the IP-bypass route.

## References

- Pentest report — `comprehensive_security_assessment_report.md` (2026-04-28), findings AUTH-VULN-01, -02, -03, -04.
- ADR-0007 — Authentication model (humans + machines), `docs/adr/adr-0007-auth-humans-and-machines.md`.
- ADR-0016 — DMZ ingest gateway, `docs/adr/adr-0016-dmz-ingest-gateway.md` (source of the existing TLS-listener and CertReloader patterns).
- RFC 6797 — HTTP Strict Transport Security (HSTS).
- RFC 7239 — Forwarded HTTP Extension (the `Forwarded` header is the modern alternative to `X-Forwarded-*`; out of scope for this ADR but a candidate for a future one).
