---
title: "ADR-0016: DMZ ingest gateway for collector push traffic"
status: "Proposed"
date: "2026-04-28"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "dmz", "gateway", "security", "mtls", "network", "secnumcloud", "ingestion", "collector", "vault", "pki"]
supersedes: ""
superseded_by: ""
---

# ADR-0016: DMZ ingest gateway for collector push traffic

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

Argos was built first as a single trusted-zone service: argosd holds the CMDB (cluster inventory, audit log, encrypted cloud-account secrets, sessions, tokens), serves the admin UI to humans, and accepts inventory pushes from collectors over the same REST API.

ADR-0009 introduced **`argos-collector`** — a push-mode Kubernetes collector that runs *inside* a remote cluster (typically air-gapped, customer-owned, or on the other side of a tightly-controlled network boundary) and pushes inventory snapshots to argosd over HTTPS using a bearer PAT (ADR-0007). This works as long as argosd is reachable from wherever the collector runs.

In high-security deployments aligned with **ANSSI SecNumCloud** (the qualification framework Argos targets — ADR-0001), argosd cannot be reachable from the internet. The data it holds is too sensitive:

- Encrypted cloud-account secret keys (ADR-0015) — plaintext leaves the database only via one narrow audited endpoint.
- Argon2id-hashed PATs and session cookies for every operator, auditor, and admin.
- The full audit log of who did what and when.
- The OIDC client secret (when configured).
- Cluster, node, pod, workload, service, and ingress topology — a high-fidelity map of the production estate.

A direct-internet-exposed argosd is incompatible with the SNC posture customers expect. But **collectors must still be able to push from outside the trusted zone** — that is the entire point of ADR-0009. The collectors run wherever the customer's clusters live, and "wherever" frequently means an environment that can reach the public internet but not directly into the operator's trusted zone.

The standard answer to this shape of problem is a **DMZ ingest barrier**: a hardened component, deployed in a perimeter network, that accepts inbound writes from the internet, performs a strict allowlist + auth check, and forwards approved traffic into the trusted zone over a controlled inbound port. argosd never sees the public internet directly.

This ADR also carries one small but important refactor on argosd's side. `argos-collector` today calls **one read endpoint** at startup (`GET /v1/clusters?name=…`) before deciding whether to POST or PATCH the cluster. A strict-write-only DMZ ingest path requires that read to disappear; the cleanest way to do that is to make `POST /v1/clusters` idempotent on `name`. The collector then unconditionally POSTs and never asks for a list.

This ADR does **not** apply to `argos-vm-collector` (ADR-0015): that binary is operator-deployed in the DMZ itself (it needs internet egress to talk to cloud-provider APIs anyway) and reaches argosd directly across the same DMZ → trusted-zone hop the gateway uses. The vm-collector's narrow `vm-collector` token scope and per-account binding remain the boundary for that traffic.

This ADR decides the deployment topology, the trust model between gateway and argosd, the cert-source story, the endpoint allowlist, the token-verification cache, the cluster-bootstrap refactor, the configuration surface, the observability story, and the testing strategy for the new component.

## Decision

**Ship `argos-ingest-gw`, a new stateless reverse-proxy binary deployed in the DMZ behind Envoy/WAF. It exposes a hardcoded write-only allowlist of 18 routes, verifies every bearer PAT against argosd via a new `POST /v1/auth/verify` endpoint with a 60 s in-memory cache, and forwards approved requests over mTLS to a new mTLS-only ingest listener on argosd. Argosd's existing public listener is unchanged. The gateway is never an auth authority — argosd re-validates every forwarded token with full argon2id. Argosd's `POST /v1/clusters` becomes idempotent on `name` so the collector no longer needs any read endpoint.**

### 1. Deployment topology

```
                  Internet                    DMZ                              Trusted zone

  ┌───────────────────┐    HTTPS    ┌──────────┐    HTTPS     ┌──────────────────┐  HTTPS+mTLS  ┌──────────┐
  │ argos-collector   │ ──────────► │  Envoy   │ ───────────► │ argos-ingest-gw  │ ───────────► │  argosd  │
  │ (remote K8s)      │ token=PAT   │  + WAF   │  (in-cluster │  (allowlist +    │  fwd token   │ :8443    │
  └───────────────────┘             │          │   to gw pod) │   verify cache)  │  fwd body    │ (ingest) │
                                    └──────────┘              └─────┬────────────┘              └──────────┘
                                                                    │ verify cache miss
                                                                    │ POST /v1/auth/verify
                                                                    │ (mTLS, no body trace)
                                                                    └──► argosd :8443

  Trusted-zone admins / UI / OIDC / MCP / cluster reads → argosd :8080 (existing public listener, unchanged)
```

The DMZ deploys **only** `argos-ingest-gw`. It has:

- No database. No file storage. No queue. No replay buffer.
- One inbound TLS listener (`:8443`, fronted by Envoy/WAF).
- One health/metrics listener (`:9090`, no TLS, bound to pod IP, never exposed via Envoy).
- Outbound egress allowed *only* to argosd's ingest listener (NetworkPolicy enforced).
- An mTLS client identity sourced from one of three mechanisms (§4): Vault PKI, a Kubernetes Secret, or files on disk. The gateway code is identical across modes — only the volume population differs.

argosd grows **exactly one new listener** (`:8443`, mTLS-only) and **one new endpoint** (`POST /v1/auth/verify`). Its existing public listener (`:8080`), serving the UI, admins, OIDC, MCP, EOL, impact, audit, settings, and trusted-zone collectors, is unchanged in every respect. When the new listener's env vars are unset, argosd behaves exactly as today and the gateway is opt-in per deployment.

### 2. Endpoint allowlist (the security boundary)

The gateway compiles a hardcoded `(method, path-pattern)` table at build time. Anything not on this table returns `404 Not Found` (not `403` — the gateway does not reveal which routes exist on argosd):

| # | Method | Path | Purpose |
|---|---|---|---|
| 1 | POST | `/v1/clusters` | Idempotent upsert on `name` (see §6) |
| 2 | PATCH | `/v1/clusters/{uuid}` | Cluster metadata patch from collector |
| 3 | POST | `/v1/nodes` | Upsert node |
| 4 | POST | `/v1/nodes/reconcile` | Cluster-scoped reconcile keep-list |
| 5 | POST | `/v1/namespaces` | Upsert namespace |
| 6 | POST | `/v1/namespaces/reconcile` | Cluster-scoped reconcile |
| 7 | POST | `/v1/pods` | Upsert pod |
| 8 | POST | `/v1/pods/reconcile` | Namespace-scoped reconcile |
| 9 | POST | `/v1/workloads` | Upsert workload |
| 10 | POST | `/v1/workloads/reconcile` | Namespace-scoped reconcile keyed on `(kind, name)` |
| 11 | POST | `/v1/services` | Upsert service |
| 12 | POST | `/v1/services/reconcile` | Namespace-scoped reconcile |
| 13 | POST | `/v1/ingresses` | Upsert ingress |
| 14 | POST | `/v1/ingresses/reconcile` | Namespace-scoped reconcile |
| 15 | POST | `/v1/persistentvolumes` | Upsert PV |
| 16 | POST | `/v1/persistentvolumes/reconcile` | Cluster-scoped reconcile |
| 17 | POST | `/v1/persistentvolumeclaims` | Upsert PVC |
| 18 | POST | `/v1/persistentvolumeclaims/reconcile` | Namespace-scoped reconcile |

Plus, locally served by the gateway (never proxied):

- `GET /healthz` — liveness
- `GET /readyz` — ready when (a) the cert files are present and not within 1 hour of expiry **and** (b) the gateway can complete an mTLS handshake against argosd's `/healthz`
- `GET /metrics` — Prometheus, on the separate `:9090` listener bound to the pod IP

Path-matching rules:

- `{uuid}` segments validated with the same regex argosd uses; a malformed UUID returns `400` at the gateway without making any upstream call.
- No globbing, no regex on path components, no trailing-slash flexibility — the request line either matches an entry exactly or it is `404`.
- Method is part of the key: `GET /v1/clusters` is `404` even though `POST /v1/clusters` is allowed.
- Body cap: **10 MiB** (configurable, sized for the largest realistic snapshot — pod listing for a ~5k-pod cluster). Enforced as belt-and-braces with the WAF.
- `Content-Type: application/json` enforced on proxied routes.
- Hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Connection`, `Te`, `Trailer`, `Transfer-Encoding`, `Upgrade`) stripped per RFC 7230.
- Any `X-Argos-Verified-*` header on the inbound request is **stripped at ingress** so a collector cannot inject a "trusted caller" identity downstream.

The allowlist is the security boundary: a security review can read it as a single literal table and answer "what can reach argosd from the internet" without needing to understand the gateway's code paths.

**Read endpoints, admin endpoints, audit endpoints, settings endpoints, OIDC endpoints, MCP endpoints, EOL endpoints, impact endpoints, session endpoints, token-management endpoints, cloud-account credential endpoints, and VM-collector endpoints are all unreachable from the DMZ.** They live only on argosd's `:8080` listener, which is not exposed to the gateway's network or to the internet. Defense in depth: the gateway's allowlist already excludes them, and the ingest listener (§3) physically does not register them.

### 3. Argosd ingest listener

A second `http.Server` started by `cmd/argosd/main.go` when `LONGUE_VUE_INGEST_LISTEN_ADDR` is set. Same process, separate listener, separate mux:

```go
ingestMux := api.NewIngestMux(server)   // registers exactly the 18 writes + verify
ingestSrv := &http.Server{
    Addr:    cfg.IngestAddr,
    Handler: middleware.AuditMiddleware(middleware.AuthMiddleware(ingestMux)),
    TLSConfig: &tls.Config{
        MinVersion:               tls.VersionTLS13,
        ClientAuth:               tls.RequireAndVerifyClientCert,
        ClientCAs:                clientCAs,                    // from --client-ca-file
        SessionTicketsDisabled:   true,
        VerifyPeerCertificate:    enforceCNAllowlist(cfg.CNAllow),
    },
}
```

The ingest mux registers exactly **19** routes: the 18 writes from §2 plus `POST /v1/auth/verify` (§5). Any other path returns `404` on this listener even if it exists on `:8080`. The auth middleware reuses the existing `auth.Middleware` (cookie/bearer dual path) — bearer-only in practice on this listener since cookies don't survive the DMZ hop. The audit middleware reuses the existing `AuditMiddleware`, with two extensions:

1. The `audit_events.source` column distinguishes `ingest_gw` from `api`, so operators can answer "what came through the DMZ" vs "what came from inside" with a single query.
2. The body scrubber learns to redact the `token` field on `POST /v1/auth/verify` so the audit log never contains a full PAT.

When `LONGUE_VUE_INGEST_LISTEN_ADDR` is empty, none of this listener's machinery starts and argosd behaves identically to today. Existing deployments are unaffected.

### 4. mTLS client identity (cert-source-agnostic)

The gateway is mTLS-required outbound. The cert source is operator-selectable; the gateway code is identical across modes. The Helm chart picks how the volume gets populated.

| Mode | How it gets cert into the pod | Rotation | When to pick |
|---|---|---|---|
| `vault` | Vault Agent sidecar (Kubernetes auth method) issues + writes cert to a shared volume. | Automatic at 50% TTL. | Operator runs Vault or OpenBao and wants hands-off rotation. |
| `secret` | Operator (or cert-manager) populates a Kubernetes `kubernetes.io/tls` Secret; chart mounts it at the same path. | Triggered by Secret update — cert-manager handles automatically, or operator runs `kubectl apply` on rotation. | Operator does not run Vault but already uses cert-manager / manual cert ops. |
| `file` | Operator mounts cert/key from any source (sealed-secrets, SOPS, hostPath, etc.) at the same path. | Whatever the operator wires up. | Edge cases / dev / air-gapped environments without K8s Secret tooling. |

**The gateway code only knows about two paths**: `--tls-cert-file` and `--tls-key-file`, defaulting to `/etc/argos-ingest-gw/tls/tls.crt` and `/etc/argos-ingest-gw/tls/tls.key`. An `fsnotify` watcher on the directory invalidates an `atomic.Pointer[tls.Certificate]` used by `tls.Config.GetClientCertificate`. **Hot-reload, no pod restart, no matter who wrote the file.** A Prometheus counter increments on each successful reload; a separate counter increments on reload failures so renewal regressions are visible before the cert actually expires.

Argosd's CA trust is orthogonal: a `--client-ca-file` (or `LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE`) points at whichever CA bundle signs the gateway's cert — Vault PKI intermediate, an internal CA, cert-manager's `ClusterIssuer`, etc. argosd does not care about the gateway's cert source.

**Recommended (but not required) Vault PKI configuration:**

- Mount: `pki_int`, role `argos-ingest-gw` bound to namespace + ServiceAccount via the Kubernetes auth method.
- Cert TTL: **24 h** default, max **48 h** (operator-tunable in Helm values).
- Renew at **50%** of TTL (12 h before expiry by default) — gives a 12 h window to alert on renewal failure before the running cert expires.
- Vault Agent template writes `/etc/argos-ingest-gw/tls/tls.crt` + `tls.key` atomically.
- Renewal failures: cert eventually expires → mTLS handshakes to argosd start failing → all writes 503 → `argos_ingest_gw_cert_renewal_failures_total` and `argos_ingest_gw_cert_not_after_seconds` Prometheus alerts page the operator. Collectors retry with backoff; no data loss provided argosd recovers within the collector's retry window.

Argosd-side cert validation (defense in depth on top of "signed by `--client-ca-file`"):

1. **CN allowlist** (`LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW`, optional) — comma-separated allowed Subject CNs. When set, e.g. `argos-ingest-gw`, blocks any other cert the same CA might be issuing.
2. **Cert expiry** — Go's stdlib enforces this; argosd re-checks in a `VerifyPeerCertificate` callback so the failure mode lands as a structured `argos_ingest_listener_client_cert_failures_total{reason="expired"}` increment instead of an opaque handshake reset.
3. **No SAN-IP / SAN-DNS check** — the gateway dials argosd, never the reverse; argosd does not care what name the gateway thinks it has.

### 5. Token verification — `POST /v1/auth/verify` and the gateway cache

#### The new endpoint

A minimal, internal-only endpoint that reuses argosd's existing `auth.VerifyBearerToken` (same argon2id check the existing middleware does, no new auth path):

```http
POST /v1/auth/verify HTTP/1.1
Content-Type: application/json
# mTLS client cert required (same as proxied writes)

{"token": "argos_pat_<prefix>_<suffix>"}
```

```json
{
  "valid": true,
  "caller_id": "8c4b…-uuid",
  "kind": "token",
  "scopes": ["read", "write"],
  "exp": 1735689600
}
```

Invalid tokens return `401` with an RFC 7807 problem doc (`{"type":"…/invalid_token"}`) and no detail body. Auth on this endpoint is mTLS-only — there is no Authorization header on the verify call itself; the client-cert identifies the caller as the gateway. Any non-mTLS caller is rejected at the TLS handshake by the listener's `RequireAndVerifyClientCert` setting; the verify endpoint is not reachable on argosd's public `:8080` listener at all. Rate-limited at argosd to 100 req/s per source IP as a backstop if the gateway's cache is bypassed by a buggy build.

#### The gateway cache

A bounded in-memory LRU keyed on `sha256(token-bytes)` (full token, not just prefix — defends against any collision on the 8-character prefix used elsewhere):

| Property | Value | Why |
|---|---|---|
| Max entries | **10 000** | Caps RAM at ~5 MiB even if every active collector has unique tokens |
| Positive TTL | **60 s** | Revocation lag = 60 s worst case; argosd revoke + 60 s = fully effective at gateway |
| Negative TTL (invalid) | **10 s** | Absorbs brute-force / scanner traffic without 60 s of denial-of-service against a freshly-issued token |
| Cache-vs-`exp` | `min(now+TTL, exp)` | Never serve "valid" past the token's actual expiry |
| Forced eviction | `Cache-Control: no-store` on the verify response | Lets argosd push same-second revocation when needed |
| Concurrency | One in-flight verify per `sha256(token)`; siblings wait | Stops a thundering herd on token rotation or cold start |

The cache is *not* a substitute for argosd's per-request auth. Every proxied write is *also* argon2id-checked at argosd. The cache only saves the gateway from forwarding garbage. The gateway is never an auth authority: argosd re-validates everything, the cache exists purely to short-circuit invalid tokens before they cross the firewall and to drop verify-call cardinality on argosd.

Failure semantics:

| Situation | Gateway behavior |
|---|---|
| Cache miss → argosd `401` | Cache negative result 10 s, return `401` to collector. |
| Cache miss → argosd unreachable | Return `503` to collector. **Do not** fail-open. **Do not** cache. |
| Cache hit `valid` → upstream write returns `401` (revoked between verify and now) | Forward `401` to collector. **Invalidate cache entry**, next request re-verifies. |
| Token expired between hit and write | Argosd is the truth. Forward its `401`. |

### 6. Argosd refactor — `POST /v1/clusters` is idempotent on `name`

Today, `POST /v1/clusters` returns `409 Conflict` on duplicate `name`. The collector handles this with a GET-then-decide pattern, calling `GET /v1/clusters?name=<name>&limit=1` first. That GET is the only read in `argos-collector` and the only thing blocking strict-write-only.

Change:

| Pre-state | Behavior |
|---|---|
| No row with that `name` | Insert, return `201 Created` with the new row. |
| Row exists with same `name` | **Return `200 OK` with the existing row.** Body of the POST is ignored on hit. |
| Row exists, request body differs | Same as above — `200 OK` with the existing row. The collector follows up with the existing `PATCH /v1/clusters/{id}` for any updates (which it already does today). |

Why "ignore body on hit" rather than upsert: POST + PATCH is the existing two-step pattern. Keeping POST as identity-only and PATCH as the merge keeps the model clear. An idempotent-create-or-fetch is cleaner than a hidden upsert; callers wanting strict-create can opt back into 409 with `If-None-Match: *` if a future ADR ever needs it (not wired up here).

Collector change in `internal/collector/apiclient/client.go`:

```go
// before
GET /v1/clusters?name=...     // ← removed
if found { PATCH ... } else { POST ...; PATCH ... }

// after
POST /v1/clusters {name, ...}   // 200 or 201, returns the row
PATCH /v1/clusters/{id} {...}   // unchanged
```

One round-trip removed, no behavior change for operators. The collector tests gain an assertion that no GET is issued during the bootstrap path.

Migration impact:

- Argosd handler change: ~30 lines in `internal/api/server.go` plus a duplicate-key catch in `internal/store/clusters.go`.
- No DB migration — uniqueness on `name` already exists.
- OpenAPI spec change: `POST /v1/clusters` adds `200` alongside `201` as a success status. Triggers the mandatory `pb33f/libopenapi-validator` test from the feature-workflow skill.
- Backwards compat: clients on the old code path still work (the GET endpoint stays in the spec for legacy callers); they just take an extra round-trip for a request they no longer need to make. The gateway's allowlist forbids the GET regardless.

### 7. Listener wire configuration

Both gateway and argosd run TLS listeners with identical hardening:

```go
tls.Config{
    MinVersion:             tls.VersionTLS13,           // hard floor
    CurvePreferences:       []{X25519, P256},
    SessionTicketsDisabled: true,                       // simplifies forward-secrecy reasoning
    GetCertificate:         hotReloadingCert(...),      // both ends, fsnotify-driven
    GetClientCertificate:   hotReloadingCert(...),      // gateway only
    ClientAuth:             tls.RequireAndVerifyClientCert,  // argosd ingest listener only
    ClientCAs:              <PEM bundle>,                     // argosd ingest listener only
}
```

- TLS 1.3 floor — no negotiation with anything older. WAF/Envoy already drops 1.2-and-below at the edge in modern deployments; we do not accept it inside the DMZ either.
- Hot-reload via `GetCertificate` / `GetClientCertificate` callbacks (not by recreating `http.Server`). Tested by writing a new cert mid-test and asserting the next handshake uses it.
- Session tickets disabled — small perf cost, eliminates "what's in the ticket key, who rotates it" as a design concern.

Connection pooling (gateway → argosd):

| Knob | Value | Rationale |
|---|---|---|
| `MaxIdleConnsPerHost` | 32 | Caps file descriptors under burst |
| `IdleConnTimeout` | 90 s | Well below 24 h cert TTL — renewal triggers fresh handshakes naturally |
| `DisableKeepAlives` | false | Reduces handshake CPU in steady state |
| Per-request timeout | 30 s default, configurable | On timeout: `503` to collector, abort upstream request |

Header handling at the proxy hop:

| Header | Treatment |
|---|---|
| `Authorization: Bearer …` | **Forwarded as-is.** Argosd re-validates with full argon2id. |
| `X-Forwarded-For` (from Envoy) | Forwarded; argosd's audit log records this as source IP. |
| `X-Forwarded-Proto`, `X-Forwarded-Host` | Forwarded. |
| `X-Argos-Verified-*` (any) | **Stripped on ingress.** Collector cannot inject a "trusted" header. |
| `X-Real-IP` | Stripped, replaced with the connecting peer (Envoy). |
| `Host` | Rewritten to argosd's expected hostname. |
| Hop-by-hop (RFC 7230) | Stripped. |
| Everything else | Forwarded unchanged. |

### 8. Behavior when argosd is unreachable

Synchronous proxy. No buffering. No queue. No disk spool. On argosd unreachable (5xx, timeout, mTLS handshake failure):

- The gateway returns `503 Service Unavailable` to the collector.
- `argos_ingest_gw_requests_total{outcome="upstream_error"}` increments.
- The verify cache is **not** populated (negative or positive) for the failing request.
- `argos-collector`'s existing exponential-backoff retry handles recovery — the gateway sees the next attempt as a fresh request.

Buffering was considered (see Alternatives) and rejected. A stateless DMZ component is materially easier to reason about, harden, and audit than one with durable state in the DMZ.

### 9. Configuration surface

Gateway env vars (all `LONGUE_VUE_INGEST_GW_*`):

| Var | Required | Default | Purpose |
|---|---|---|---|
| `LONGUE_VUE_INGEST_GW_LISTEN_ADDR` | no | `:8443` | Ingest listener bind |
| `LONGUE_VUE_INGEST_GW_LISTEN_TLS_CERT` | yes | — | Server cert presented to Envoy |
| `LONGUE_VUE_INGEST_GW_LISTEN_TLS_KEY` | yes | — | Server key |
| `LONGUE_VUE_INGEST_GW_HEALTH_ADDR` | no | `:9090` | Health/metrics, no TLS, pod-IP only |
| `LONGUE_VUE_INGEST_GW_UPSTREAM_URL` | yes | — | e.g. `https://argosd-ingest.argos.svc.cluster.local:8443` |
| `LONGUE_VUE_INGEST_GW_UPSTREAM_HOST` | no | host from URL | `Host:` rewrite target |
| `LONGUE_VUE_INGEST_GW_UPSTREAM_TIMEOUT` | no | `30s` | Per-request upstream timeout |
| `LONGUE_VUE_INGEST_GW_UPSTREAM_CA_FILE` | yes | — | CA bundle to verify argosd's cert |
| `LONGUE_VUE_INGEST_GW_CLIENT_CERT_FILE` | yes | `/etc/argos-ingest-gw/tls/tls.crt` | mTLS client cert (vault/secret/file all converge) |
| `LONGUE_VUE_INGEST_GW_CLIENT_KEY_FILE` | yes | `/etc/argos-ingest-gw/tls/tls.key` | mTLS client key |
| `LONGUE_VUE_INGEST_GW_CACHE_TTL` | no | `60s` | Positive cache TTL |
| `LONGUE_VUE_INGEST_GW_CACHE_NEGATIVE_TTL` | no | `10s` | Negative cache TTL |
| `LONGUE_VUE_INGEST_GW_CACHE_MAX_ENTRIES` | no | `10000` | LRU cap |
| `LONGUE_VUE_INGEST_GW_MAX_BODY_BYTES` | no | `10485760` (10 MiB) | Per-request body cap |
| `LONGUE_VUE_INGEST_GW_LOG_LEVEL` | no | `info` | `debug`/`info`/`warn`/`error` |
| `LONGUE_VUE_INGEST_GW_SHUTDOWN_TIMEOUT` | no | `30s` | Graceful drain on SIGTERM |

Argosd env vars (all `LONGUE_VUE_INGEST_LISTEN_*`):

| Var | Required | Default | Purpose |
|---|---|---|---|
| `LONGUE_VUE_INGEST_LISTEN_ADDR` | no | empty (disabled) | Enables the new mTLS-only ingest listener when set |
| `LONGUE_VUE_INGEST_LISTEN_TLS_CERT` | when ingest enabled | — | Server cert for the ingest listener |
| `LONGUE_VUE_INGEST_LISTEN_TLS_KEY` | when ingest enabled | — | Server key |
| `LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE` | when ingest enabled | — | CA bundle that signs accepted client certs |
| `LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW` | no | empty (any CN) | Comma-separated allowed Subject CNs |

When `LONGUE_VUE_INGEST_LISTEN_ADDR` is empty, none of the new listener machinery starts. Existing deployments are unaffected; the gateway is opt-in per deployment.

### 10. Helm chart layout

Two charts ship in this repo. The DMZ deploys *only* `argos-ingest-gw` — never the umbrella `argos` chart — keeping the DMZ blast radius small and the trust boundary clean.

```
charts/
├── argos/                       # existing — bumped appVersion; gains conditional ingest-listener bits
│   └── templates/
│       └── argosd-ingest-listener.yaml
└── argos-ingest-gw/             # new chart
    ├── Chart.yaml
    ├── values.yaml
    ├── README.md
    └── templates/
        ├── _helpers.tpl
        ├── deployment.yaml          # default 2 replicas, HPA-friendly
        ├── service.yaml             # ClusterIP, exposed via Envoy/Ingress
        ├── serviceaccount.yaml      # used by Vault k8s auth in vault mode
        ├── secret-tls.yaml          # rendered when tls.mode=secret with inline cert
        ├── configmap-ca.yaml        # upstream CA bundle
        ├── networkpolicy.yaml       # egress only to argosd ingest port + Vault (vault mode only)
        ├── poddisruptionbudget.yaml # minAvailable: 1
        ├── servicemonitor.yaml      # optional, when Prometheus Operator present
        └── prometheusrule.yaml      # optional, ships the §11 alerts when enabled
```

`values.yaml` shape:

```yaml
image:
  repository: ghcr.io/argos/argos-ingest-gw
  tag: ""               # defaults to chart's appVersion
  pullPolicy: IfNotPresent

replicaCount: 2

resources:
  limits:   { cpu: 500m, memory: 256Mi }
  requests: { cpu: 50m,  memory: 64Mi }

upstream:
  url: ""               # required: https://argosd-ingest.<argos-ns>.svc.cluster.local:8443
  caBundle: ""          # required: PEM, mounted as a ConfigMap
  timeout: 30s

listener:
  port: 8443
  tls:
    secretName: argos-ingest-gw-server-tls   # cert presented to Envoy

mtls:
  mode: secret          # vault | secret | file
  secret:
    name: argos-ingest-gw-mtls
  vault:
    enabled: false
    address: https://vault.example.com
    role: argos-ingest-gw
    pkiMount: pki_int
    pkiRole: argos-ingest-gw
    certTTL: 24h
    renewAt: 50          # %
    agentImage: hashicorp/vault:1.18

cache:
  ttl: 60s
  negativeTtl: 10s
  maxEntries: 10000

maxBodyBytes: 10485760  # 10 MiB

logLevel: info

networkPolicy:
  enabled: true
  argosdSelector:
    matchLabels: { app.kubernetes.io/name: argosd }
  argosdNamespace: argos
  argosdIngestPort: 8443
  vaultEgress:
    enabled: false
    cidrs: []           # operator fills in, e.g. ["10.0.5.10/32"]

podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile: { type: RuntimeDefault }

containerSecurityContext:
  allowPrivilegeEscalation: false
  capabilities: { drop: ["ALL"] }
  readOnlyRootFilesystem: true

monitoring:
  serviceMonitor:
    enabled: false
  prometheusRule:
    enabled: false
```

Pod hardening defaults baked into the chart:

- `runAsNonRoot: true`, UID 65532 (matches the distroless base).
- `readOnlyRootFilesystem: true` with two `emptyDir` mounts for `/tmp` and `/var/run`.
- `allowPrivilegeEscalation: false`, `capabilities.drop: [ALL]`.
- `seccompProfile.type: RuntimeDefault`.
- `automountServiceAccountToken: false` unless `mtls.mode: vault` (only Vault k8s auth needs the SA token).
- Default NetworkPolicy: egress allowed only to `argosdSelector` on `argosdIngestPort`, plus Vault CIDRs if vault mode is enabled. **No egress to internet, no egress to anything else in-cluster.**
- 2 replicas + `PodDisruptionBudget{minAvailable: 1}` so a node drain or rolling update never takes the gateway fully down.

Container image: distroless static, single static binary (~15 MiB), built from `Dockerfile.ingest-gw` (two-stage, no Node, no UI). Built by a new GH Actions job mirroring the existing `docker-build` job for argosd.

### 11. Observability

Gateway Prometheus metrics, exposed on `:9090/metrics` (unauthenticated, bound to pod IP only):

| Metric | Type | Labels |
|---|---|---|
| `argos_ingest_gw_requests_total` | counter | `method`, `route`, `status_class`, `outcome` (allowed/denied_path/denied_method/denied_token/denied_scope/upstream_error/upstream_timeout) |
| `argos_ingest_gw_request_duration_seconds` | histogram | `route`, `outcome` |
| `argos_ingest_gw_upstream_duration_seconds` | histogram | `route` |
| `argos_ingest_gw_token_verify_total` | counter | `result` (valid/invalid/error) |
| `argos_ingest_gw_token_cache_total` | counter | `event` (hit/miss/negative_hit/evict/inflight_dedupe) |
| `argos_ingest_gw_token_cache_size` | gauge | — |
| `argos_ingest_gw_cert_not_after_seconds` | gauge | — |
| `argos_ingest_gw_cert_reload_total` | counter | `result` (success/failure) |
| `argos_ingest_gw_cert_renewal_failures_total` | counter | — |
| `argos_ingest_gw_body_bytes` | histogram (powers-of-two, 1 KiB to 16 MiB) | `route` |
| `argos_ingest_gw_inflight_requests` | gauge | — |
| `argos_ingest_gw_build_info` | gauge (always 1) | `version`, `commit`, `go_version` |

Argosd ingest-listener metrics (extending existing):

- `argos_http_requests_total{listener="ingest"|"public"}` — split traffic by listener.
- `argos_auth_verify_total{result}` — calls to `POST /v1/auth/verify`.
- `argos_ingest_listener_client_cert_failures_total{reason}` — `bad_ca` / `expired` / `cn_not_allowed` / `none_provided`.

Suggested alerts (shipped as a values-block in the chart):

| Alert | Expression | Severity |
|---|---|---|
| `IngestGwCertExpiringSoon` | `argos_ingest_gw_cert_not_after_seconds - time() < 3600` | warning <1 h, critical <15 min |
| `IngestGwCertReloadFailing` | `increase(argos_ingest_gw_cert_reload_total{result="failure"}[10m]) > 0` | warning |
| `IngestGwUpstream5xx` | `rate(argos_ingest_gw_requests_total{status_class="5xx"}[5m]) > 0.05 * rate(argos_ingest_gw_requests_total[5m])` | warning |
| `IngestGwHighDenials` | `rate(argos_ingest_gw_requests_total{outcome=~"denied_.*"}[5m]) > 1` | info — likely scanner / misconfigured collector |
| `IngestGwArgosdUnreachable` | `rate(argos_ingest_gw_requests_total{outcome="upstream_error"}[2m]) > 0 and up{job="argosd"} == 0` | critical — paging |
| `IngestGwClientCertFailures` | `increase(argos_ingest_listener_client_cert_failures_total[10m]) > 0` | warning |

Gateway logs (structured JSON via `slog`, single line per request, written after the response):

```json
{
  "ts": "...",
  "level": "info",
  "msg": "request",
  "method": "POST",
  "route": "/v1/pods",
  "status": 201,
  "outcome": "allowed",
  "client_ip": "203.0.113.42",
  "envoy_request_id": "abc-…",
  "token_prefix": "a1b2c3d4",
  "body_bytes": 8421,
  "duration_ms": 27,
  "upstream_ms": 22,
  "cache": "hit"
}
```

**Never logged**: full token, full request body, full `Authorization` header, response body. `token_prefix` is the same 8-char hex that lives in `tokens.token_prefix` — enough to correlate with argosd's audit log, useless for replay. `envoy_request_id` is propagated end-to-end so a single ID traces an incident through Envoy → gateway → argosd's audit log.

Audit: argosd is the durable system of record. Every gateway-allowed request lands in `audit_events`. Every gateway-denied request is observable as a metric counter. The gateway itself does **not** keep an audit log — adding a second persistent log in the DMZ would create a second redaction surface and a second backup target without buying any audit fidelity.

OpenTelemetry tracing is out of scope for this ADR. The `envoy_request_id` correlation + structured logs cover the operational need.

## Consequences

### Positive

- argosd is no longer reachable from the internet in DMZ deployments. Compromise of the gateway grants only the ability to forward requests argosd would also have allowed — no read access, no admin access, no token forging.
- The DMZ component is stateless: no DB, no queue, no disk spool. Easier to harden, audit, and reason about.
- Cert source is operator's choice: Vault for the full-rotation story, Kubernetes Secret + cert-manager for operators without Vault, files-on-disk for edge cases.
- The 18-route allowlist is auditable as a single literal table during a security review.
- Token revocation propagates within 60 s worst case; same-second revoke is possible via `Cache-Control: no-store` on the verify response.
- The cluster-bootstrap refactor (`POST /v1/clusters` idempotent) removes one HTTP round-trip from every collector startup, regardless of whether the gateway is deployed. Net wins for everybody.
- argosd's existing trust model is unchanged. Every forwarded token is still argon2id-checked at argosd. The gateway is a filter, not an auth authority.
- The new ingest listener is opt-in via a single env var. Deployments that don't need DMZ ingest are unaffected.

### Negative

- One more binary, one more image, one more chart, one more deployment to operate.
- `POST /v1/clusters` semantics change subtly (`200` is a new success status alongside `201`); audit consumers querying for "cluster creates" by status code may need to adjust.
- An extra hop adds ~5–20 ms to collector write latency in the synchronous proxy path. Acceptable for the security gain; collectors are already designed to tolerate seconds, not milliseconds.
- A 60 s cache TTL means token revocation is not instantaneous through the gateway. Operators who need same-second revoke can issue `Cache-Control: no-store` from argosd or shorten the TTL. The default trades 60 s of revocation lag for a ~60× drop in verify-call cardinality on argosd.
- The two-listener split on argosd (`:8080` public + `:8443` ingest) doubles the TLS-cert ops surface for operators who enable ingest. Mitigated by Helm templating of the ingest-listener bits.
- A bug in the gateway's allowlist parser would be a privilege boundary failure. Mitigated by extensive table-driven negative tests (§testing) and the simplicity of the allowlist (a literal slice of 18 entries; no globs, no regex).

### Neutral

- The gateway is mTLS-required to argosd. Operators must wire a CA and a client cert one way or another. Three modes mitigate the ergonomic burden but the requirement itself is non-negotiable.
- The gateway does not buffer. Argosd outage = collector backoff loop, not gateway-side replay. This is the correct trade for a stateless DMZ component but means a long argosd outage produces collector-side retry storms that need their own rate limiting (existing in `argos-collector`).

## Alternatives Considered

### Pull-mode / store-and-forward via a queue in the DMZ

Considered: gateway accepts inbound writes from collectors and pushes them to a queue (NATS/Redis/Kafka) co-located in the DMZ; argosd polls the queue from the trusted zone. **Zero inbound from DMZ to trusted zone** — strictest possible network posture.

**Rejected for this ADR** (not forever): the customer environments targeted today permit a tightly-allowlisted DMZ → trusted-zone hop, and the operational complexity of running a queue, ordering replays, sizing buffers, and giving collectors meaningful synchronous error responses outweighs the marginal security gain. A future ADR can add this as a second deployment mode if a customer with strictly-no-inbound trusted-zone policy emerges.

### Off-the-shelf reverse proxy (nginx / Caddy / Envoy with Lua/WASM)

Considered: rather than ship a Go binary, configure an off-the-shelf proxy with allowlist rules, rate limits, and a Lua/WASM module to call argosd's verify endpoint and cache results.

**Rejected**: the data-plane parts are well-served by these proxies, but the verify-cache + structured-log + Prometheus story all require custom code, and that code has to live somewhere. Lua/WASM modules are debugged differently from Go code, do not benefit from the project's existing test/lint/security toolchain, and create a second reviewer-skillset boundary in the codebase. A small focused Go binary (~15 MiB distroless) gives essentially the same runtime hardening (non-root, readOnlyRootFS, dropped caps, distroless base) with strictly less operational complexity.

### Reuse argosd as the gateway via a `--mode=gateway` flag

Considered: same binary, different runtime posture gated by a flag — no DB pool, no UI embed (or refuses to serve them), only the proxy code path runs.

**Rejected**: one image to maintain, but two very different runtime postures sharing the same binary. Risk: "gateway mode accidentally exposes admin route because someone forgot to gate it" — exactly the failure mode you most want to avoid in a DMZ component. A separate binary with its own surface area makes "what code runs in the DMZ" answerable by reading `cmd/argos-ingest-gw/main.go`.

### Switch PATs to JWTs so the gateway validates locally

Considered: argosd issues short-lived JWTs signed with a private key; gateway validates locally with the public key (JWKS). No verify call to argosd. Gateway works even during an argosd outage.

**Rejected for this ADR**: requires changing the auth model (collectors need refresh logic, argosd needs signing-key + JWKS endpoint, revocation becomes a denylist problem instead of a "delete the row" problem). The verify-cache approach gets most of the operational benefit (~60× cardinality drop on argosd) without inventing a second token format. JWTs can be layered on later if a future requirement is "the gateway must keep authenticating during an argosd outage" — until that requirement exists, the simpler model wins.

### Identity handoff via `X-Argos-Verified-Caller-Id` header (gateway as auth authority)

Considered: after a successful verify, gateway strips `Authorization`, injects `X-Argos-Verified-Caller-Id: <uuid>` + `X-Argos-Verified-Scope: write`, and argosd trusts the header (relying on mTLS for trust). Saves one argon2id check per request on argosd.

**Rejected**: the gateway becomes an auth authority. If anyone ever loosens mTLS or adds a header-based bypass code path on argosd, that header becomes a `sudo` primitive. The current design — forward the original token, let argosd re-validate — has a strictly weaker compromise outcome: a fully malicious gateway can only forward requests argosd would also have allowed.

### Strict write-only without the cluster-lookup refactor

Considered: keep `argos-collector`'s startup `GET /v1/clusters?name=`, document the gateway as "writes plus this one bootstrap read".

**Rejected**: pollutes the strict-write-only stance with a single asterisk forever. The `POST /v1/clusters` idempotency change is a ~30-line refactor that benefits every deployment whether the gateway is in play or not, so it's the right place to spend the budget.

### Spool buffer in the DMZ for argosd-down replay

Considered: 5-minute, 100 MiB on-disk spool that the gateway drains when argosd recovers, hiding brief outages from collectors.

**Rejected**: introduces durable state in the DMZ — encryption-at-rest, replay-ordering bugs, "what if the spool fills" edge cases, a second backup target. Material complexity bump for a problem that doesn't exist (collectors already retry with backoff and tolerate minutes of unavailability).

## Implementation Notes

Suggested phasing:

1. **Argosd refactor** (smallest, lands first): make `POST /v1/clusters` idempotent on `name`, drop the GET from `argos-collector`. Triggers the OpenAPI validation test from the feature-workflow skill. No new binary, no new chart — just a regression-safe API change that benefits every deployment.
2. **Argosd ingest listener + verify endpoint**: add `internal/api/ingest_mux.go`, `POST /v1/auth/verify`, the new env vars, the `audit_events.source` extension, and the body scrubber for the `token` field. Listener is gated on `LONGUE_VUE_INGEST_LISTEN_ADDR`; existing deployments unaffected. Tests: per §test-strategy.
3. **Gateway binary**: `cmd/argos-ingest-gw/`, `internal/ingestgw/{allowlist,cache,verify_client,proxy,tls_reload,metrics}`. Distroless image via `Dockerfile.ingest-gw`. Unit tests exhaust the allowlist negatives.
4. **Helm chart `argos-ingest-gw`**: three TLS modes (vault/secret/file), NetworkPolicy, PDB, optional ServiceMonitor + PrometheusRule. README walks through Vault-PKI setup, cert-manager setup, and manual-cert setup.
5. **Integration tests**: build-tagged `integration`, run against a real Postgres + real argosd + real gateway with a self-signed CA.
6. **Documentation**: how-to in `docs/how-to/deploy-dmz-ingest-gateway.md`, env vars in `docs/configuration.md`, endpoint in `docs/api-reference.md`, CLAUDE.md architecture note.

Test strategy, summarised:

- **Unit (gateway)**: allowlist (~50 negative cases), cache TTL/eviction/inflight-dedupe, verify client behavior, header strip rules, body cap, TLS hot-reload, metric label coverage.
- **Unit (argosd)**: `POST /v1/auth/verify` (mTLS-only, audit redaction, rate limit), `POST /v1/clusters` idempotency, ingest listener route registration + CN allowlist.
- **Integration**: happy path, mid-stream revoke (cached "valid" doesn't override argosd's `401`), argosd restart returns `503` not buffer, bad-CA cert is rejected with metric increment, cert hot-reload mid-test, body cap at 11 MiB.
- **OpenAPI validation** (mandatory): `pb33f/libopenapi-validator` over the new + modified endpoints — `ValidateDocument()`, `ValidateHttpRequest`, `ValidateHttpResponse`. Triggered by the `POST /v1/clusters` dual-success-status change.
- **Security** (`internal/ingestgw/security_test.go`): attacker-goal-driven — every read/admin endpoint is `404`, path-normalisation tricks blocked, header injection stripped, cache-poisoning via prefix collision impossible, log redaction enforced, body smuggling rejected, slow-loris timed out.

Per the feature-workflow skill, **all CI testing and CI fix loops are delegated to a Sonnet-tier agent** — Opus only re-engages on a failure that needs architectural judgement. The mandatory `pb33f/libopenapi-validator` test is part of Phase 3.

## Future work

- **Pull-mode / queue variant** (alternative §A): a future ADR can add this as a second supported deployment mode if a customer with strictly-no-inbound trusted-zone policy emerges. Same gateway binary, different upstream interface — replaces the synchronous reverse-proxy with a queue producer.
- **JWT-style ingest tickets** (alternative §D): if a future requirement is "the gateway must keep authenticating during an argosd outage," argosd can grow a "mint signed ingest ticket" endpoint and the gateway gains a JWKS-validation path. Reversible under the current design.
- **Per-collector rate limiting at the gateway**: today the gateway delegates rate limiting to Envoy/WAF + argosd. If a customer needs collector-tier rate limiting that survives an Envoy reconfiguration, a token-bucketed limiter keyed on `token_prefix` is a small addition.
- **OpenTelemetry tracing**: not in this ADR. Pick up across argosd in a future cross-cutting ADR.
- **Multi-region argosd / DR**: out of scope. The gateway addresses one upstream URL; HA is the ingress controller's problem above the gateway.

## References

- ADR-0001 — CMDB for SecNumCloud using Kubernetes
- ADR-0007 — Authentication and RBAC (the dual-path auth model and PAT format the gateway forwards)
- ADR-0009 — Push collector for air-gapped clusters (the existing collector this ADR puts a barrier in front of)
- ADR-0011 — Collector auto-creates cluster (the bootstrap flow the §6 refactor cleans up)
- ADR-0015 — VM collector for non-Kubernetes platform VMs (clarifies why this ADR explicitly does *not* apply to vm-collector traffic)
- ANSSI **SecNumCloud** referential — the qualification framework that motivates the DMZ posture
- RFC 7230 — HTTP/1.1 Message Syntax (hop-by-hop header rules followed by the proxy)
- RFC 7807 — Problem Details for HTTP APIs (the error-shape argosd already returns)
- HashiCorp Vault PKI Secrets Engine — recommended `vault` cert-source mode
- cert-manager — recommended `secret` cert-source mode
- pb33f/libopenapi-validator — mandated by feature-workflow skill for any OpenAPI spec change
