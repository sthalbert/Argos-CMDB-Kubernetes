# Configuration Reference

All Argos configuration is environment-variable based. There are no config files.

## argosd

The main daemon that serves the REST API, web UI, and optionally runs the pull-mode collector.

### Core

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_DATABASE_URL` | yes | -- | PostgreSQL connection string (DSN). Example: `postgres://user:pass@host:5432/argos?sslmode=require`. |
| `ARGOS_ADDR` | no | `:8080` | HTTP listen address. Set to `:443` or `127.0.0.1:8080` as needed. |
| `ARGOS_AUTO_MIGRATE` | no | `true` | Run embedded goose migrations on startup. Set to `false` if you manage migrations externally. |
| `ARGOS_SHUTDOWN_TIMEOUT` | no | `15s` | Graceful shutdown budget on SIGINT / SIGTERM. In-flight requests drain until this deadline. |

### Bootstrap

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_BOOTSTRAP_ADMIN_PASSWORD` | no | random 16-char string | Password for the auto-created `admin` user on first boot. Only consulted when no active admin exists in the database. If unset, argosd generates a random password and prints it **once** to the startup log. |

### Session and cookies

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_SESSION_SECURE_COOKIE` | no | `auto` | Controls the `Secure` flag on the session cookie. Values: `auto` (resolve from the trust-aware request scheme — see `ARGOS_TRUSTED_PROXIES`), `always`, or `never`. Pin to `always` when fronted by a TLS-terminating proxy so cookies never travel over plaintext. Use `never` only for local HTTP development. |

### Public-listener TLS posture (ADR-0017)

argosd supports two postures for the public listener; pick exactly one
and configure the matching variables. `ARGOS_REQUIRE_HTTPS=true` makes
the daemon refuse to start unless one of the two is fully configured —
failing closed beats accidentally serving credentials over plain HTTP.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_PUBLIC_LISTEN_TLS_CERT` | for native TLS | -- | Path to a PEM-encoded TLS certificate. When set with `ARGOS_PUBLIC_LISTEN_TLS_KEY`, argosd terminates TLS itself (TLS 1.3 floor, session tickets disabled). The keypair is reloaded on the next TLS handshake whenever the cert file's mtime changes — covers cert-manager rotations, Vault Agent atomic-rename, and manual rewrites. |
| `ARGOS_PUBLIC_LISTEN_TLS_KEY` | for native TLS | -- | Path to the matching PEM-encoded private key. Must be readable by the argosd process (UID 65532 in the distroless image). |
| `ARGOS_TRUSTED_PROXIES` | for trusted-proxy posture | (empty = none) | Comma-separated CIDR list of TLS-terminating proxies whose `X-Forwarded-For` and `X-Forwarded-Proto` argosd will honor. Empty = ignore both headers entirely (the secure default). Example: `10.0.0.0/8,172.16.0.0/12`. Set this when running behind ingress-nginx, Envoy, a cloud LB, or any other TLS-terminating proxy so audit logs, rate-limit buckets, and the Secure-cookie check see the real client transport instead of the proxy's. |
| `ARGOS_REQUIRE_HTTPS` | no | `false` | When `true`, argosd refuses to start unless either `ARGOS_PUBLIC_LISTEN_TLS_CERT` + `_KEY` are set (native TLS) OR `ARGOS_TRUSTED_PROXIES` is non-empty AND `ARGOS_SESSION_SECURE_COOKIE=always` (trusted-proxy posture). Also force-emits `Strict-Transport-Security` so browsers refuse plaintext fallback. |

### OIDC (optional)

OIDC is disabled by default. Set `ARGOS_OIDC_ISSUER` to enable it.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_OIDC_ISSUER` | no | (empty = off) | OIDC issuer URL. argosd fetches the `.well-known/openid-configuration` from this URL on startup and fails fatally if unreachable. Example: `https://idp.example.com/realms/argos`. |
| `ARGOS_OIDC_CLIENT_ID` | when OIDC enabled | -- | OAuth 2.0 client ID registered at the IdP. |
| `ARGOS_OIDC_CLIENT_SECRET` | for confidential clients | -- | OAuth 2.0 client secret. Omit for public clients (not recommended). |
| `ARGOS_OIDC_REDIRECT_URL` | when OIDC enabled | -- | Full callback URL registered at the IdP. Must be `https://<argos-host>/v1/auth/oidc/callback`. |
| `ARGOS_OIDC_SCOPES` | no | `openid,email,profile` | Comma-separated list of scopes to request from the IdP. |
| `ARGOS_OIDC_LABEL` | no | `OIDC` | Text shown on the "Sign in with ..." button in the UI. |

### Pull collector

The embedded pull-mode collector is disabled by default.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_COLLECTOR_ENABLED` | no | `false` | Enable the polling collector. Set to `true` to start ingesting data from Kubernetes. |
| `ARGOS_COLLECTOR_INTERVAL` | no | `60s` | Time between polling ticks. Accepts Go duration syntax (`30s`, `5m`). |
| `ARGOS_COLLECTOR_FETCH_TIMEOUT` | no | `20s` | Per-tick timeout for Kubernetes API calls. |
| `ARGOS_COLLECTOR_RECONCILE` | no | `true` | Delete rows from the CMDB that no longer appear in the live Kubernetes listing. Required for ANSSI cartography fidelity. Set to `false` for append-only behavior. |

#### Single-cluster mode

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_CLUSTER_NAME` | when single-cluster | -- | Name of the cluster to poll. The collector auto-creates the cluster record if it doesn't exist (ADR-0011); pre-registering via `POST /v1/clusters` is optional but recommended to populate curated metadata. When using in-cluster ServiceAccount credentials, no kubeconfig is needed. |

#### Multi-cluster mode

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_COLLECTOR_CLUSTERS` | no | -- | JSON array of `{"name":"...","kubeconfig":"..."}` tuples. One collector goroutine spawns per entry. An empty `kubeconfig` falls back to in-cluster config. Overrides `ARGOS_CLUSTER_NAME` when set. Each `kubeconfig` value is a path to a file mounted from a Kubernetes Secret — see [How to securely provide kubeconfigs](how-to-secure-kubeconfig.md). |

Example:

```json
[
  {"name":"prod","kubeconfig":"/etc/argos/kubeconfigs/prod.yaml"},
  {"name":"staging","kubeconfig":"/etc/argos/kubeconfigs/staging.yaml"},
  {"name":"in-cluster","kubeconfig":""}
]
```

> **Security:** kubeconfig files must be mounted from a Kubernetes Secret, never passed as environment variable values. See [How to securely provide kubeconfigs](how-to-secure-kubeconfig.md) for the full procedure.

### EOL enrichment

The EOL enricher is controlled at runtime via the **Admin > Settings** UI. These env vars configure the enricher behaviour and optionally seed the initial database setting. See [EOL Enrichment](eol-enrichment.md) for the full guide.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_EOL_ENABLED` | no | -- | Seeds the `eol_enabled` database setting on startup. When set, its value is written to the settings table on boot. The admin can override it at runtime via the UI. |
| `ARGOS_EOL_INTERVAL` | no | `2m` | Time between enrichment ticks. Accepts Go duration syntax. |
| `ARGOS_EOL_APPROACHING_DAYS` | no | `90` | Number of days before EOL to flag a product as "approaching EOL". |
| `ARGOS_EOL_BASE_URL` | no | `https://endoflife.date` | Base URL for the endoflife.date API. Override to point at an internal mirror in air-gapped environments. |

### MCP server

The MCP (Model Context Protocol) server is disabled by default. It exposes read-only CMDB tools for AI agents. See [MCP Server](mcp-server.md) for the full guide.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_MCP_ENABLED` | no | `false` | Enable the MCP server. When set, its value is also written to the `mcp_enabled` database setting on boot. The admin can override it at runtime via Admin > Settings. |
| `ARGOS_MCP_TRANSPORT` | no | `sse` | Transport protocol. Values: `sse` (Server-Sent Events over HTTP) or `stdio` (standard I/O, for local tool integration). |
| `ARGOS_MCP_ADDR` | no | `:3001` | Listen address for the SSE transport. Ignored when transport is `stdio`. |
| `ARGOS_MCP_TOKEN` | no | -- | Bearer token required for MCP requests. When unset, the MCP server inherits the standard argosd bearer token authentication. |

### DMZ ingest gateway (ADR-0016)

The ingest listener is disabled by default. Set `ARGOS_INGEST_LISTEN_ADDR` to enable it. See [How to deploy the DMZ ingest gateway](how-to-deploy-dmz-ingest-gateway.md) for the full operator walkthrough.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_LISTEN_ADDR` | no | empty (disabled) | Bind address for the mTLS-only ingest listener. Setting this variable enables the second listener. Example: `:8443`. When unset, none of the ingest listener machinery starts and argosd behaves identically to today. |
| `ARGOS_INGEST_LISTEN_TLS_CERT` | when ingest enabled | — | Path to a PEM-encoded server certificate presented on the ingest listener. Must be signed by a CA your mTLS client (the gateway) trusts. |
| `ARGOS_INGEST_LISTEN_TLS_KEY` | when ingest enabled | — | Path to the private key matching `ARGOS_INGEST_LISTEN_TLS_CERT`. |
| `ARGOS_INGEST_LISTEN_CLIENT_CA_FILE` | when ingest enabled | — | Path to a PEM-encoded CA bundle. Only client certs signed by this CA are accepted at the mTLS handshake. Typically the Vault PKI intermediate, an internal CA, or the cert-manager ClusterIssuer CA. |
| `ARGOS_INGEST_LISTEN_CLIENT_CN_ALLOW` | no | empty (any CN) | Comma-separated list of allowed Subject CNs on the client cert. When set (e.g. `argos-ingest-gw`), blocks any other cert the same CA might issue. Leave unset to accept any cert signed by the CA above. |

---

### Secrets master key

When you catalogue cloud accounts (ADR-0015), the cloud-provider Secret Keys are encrypted at rest with AES-256-GCM under a master key supplied via env var. See [Cloud accounts — master-key backup](cloud-accounts.md#master-key-backup) for the full operational guide.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_SECRETS_MASTER_KEY` | when cloud accounts have credentials | -- | Base64-encoded 32-byte AES-256 master key. argosd refuses to start if any `cloud_accounts` row has a non-NULL `secret_key_encrypted` and this variable is unset or malformed. Lengths other than 32 bytes (after base64 decode) are rejected at startup. |

**Generate a key:**

```sh
openssl rand -base64 32
```

**Verify the right key is loaded.** On startup, argosd logs a master-key fingerprint (first 8 hex chars of the SHA-256 of the key):

```
INFO secrets master key loaded fingerprint=3f9c1e7a
```

The key itself is never logged, never returned by any endpoint, and never surfaced in the UI.

> **Critical:** losing this key means every encrypted Secret Key in the database becomes unrecoverable. Argosd will start, but every vm-collector tick will fail until an admin re-enters every Secret Key by hand. Treat the master key with the same care as a database backup encryption key — back it up to your vault separately from the database itself. See [the recovery procedure](cloud-accounts.md#recover-from-master-key-loss).

### Security

The following security features are built-in and require no configuration:

| Feature | Default | Notes |
|---------|---------|-------|
| HTTP security headers | Always on | CSP, X-Content-Type-Options, X-Frame-Options, Referrer-Policy. HSTS set only over TLS. |
| Login rate limiting | 5 req/min per IP | Returns 429 when exceeded. Implements ADR-0007 IMP-009. |
| Request body size limit | 1 MiB | Returns 413 when exceeded. |
| Server timeouts | Read: 30s, Write: 60s, Idle: 120s | Prevents slowloris attacks. |
| Impact graph node cap | 500 nodes | Response includes `truncated: true` when cap is hit. |

### Legacy (removed)

| Variable | Status | Migration |
|----------|--------|-----------|
| `ARGOS_API_TOKEN` | **removed** | argosd refuses to start if set. Migrate to admin-panel-issued tokens per ADR-0007. |
| `ARGOS_API_TOKENS` | **removed** | Same as above. |
| `ARGOS_KUBECONFIG` | **removed** | Use `ARGOS_COLLECTOR_CLUSTERS` with kubeconfig files mounted via `kubeconfigSecret`. See [How to securely provide kubeconfigs](how-to-secure-kubeconfig.md). |

---

## argos-collector (push mode)

The standalone push-mode collector binary. It runs inside an air-gapped or network-restricted cluster and pushes observations to a remote argosd over HTTPS. See [ADR-0009](../docs/adr/adr-0009-push-collector-for-airgapped-clusters.md) for background.

### Connection

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_SERVER_URL` | yes | -- | Base URL of the argosd instance. Example: `https://argos.internal:8080`. Supports a path prefix for gateway deployments: `https://gateway:443/argos`. |
| `ARGOS_API_TOKEN` | yes | -- | Bearer token (PAT) with `write` scope, created in the argosd admin panel. Format: `argos_pat_<prefix>_<secret>`. |

### Cluster identity

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_CLUSTER_NAME` | yes | -- | Name of this cluster. The push collector auto-creates the record if it doesn't exist (ADR-0011); pre-registering via `POST /v1/clusters` is optional but recommended to populate curated metadata. |

### Collector behavior

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_COLLECTOR_INTERVAL` | no | `5m` | Time between polling ticks. |
| `ARGOS_COLLECTOR_RECONCILE` | no | `true` | Delete stale rows via the `/reconcile` endpoints after each successful listing. |

### TLS and gateway

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_CA_CERT` | no | system CA pool | Path to a PEM-encoded CA certificate for verifying the argosd (or gateway) TLS certificate. Required when argosd uses a private CA. |
| `ARGOS_CLIENT_CERT` | no | -- | Path to a PEM-encoded client certificate for mTLS to a gateway. |
| `ARGOS_CLIENT_KEY` | no | -- | Path to a PEM-encoded client private key for mTLS. Required when `ARGOS_CLIENT_CERT` is set. |
| `ARGOS_EXTRA_HEADERS` | no | -- | Comma-separated `key=value` pairs injected into every outbound HTTP request. Useful for gateway routing or tenant identification. Example: `X-Tenant-Id=zad-prod,X-Route-Key=argos`. |

### Proxy

The collector honors the standard Go proxy environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `HTTPS_PROXY` | no | -- | Forward proxy URL for HTTPS traffic. |
| `HTTP_PROXY` | no | -- | Forward proxy URL for HTTP traffic. |
| `NO_PROXY` | no | -- | Comma-separated list of hosts/CIDRs to bypass the proxy. |

---

## argos-vm-collector

The standalone push-mode collector for non-Kubernetes platform VMs (ADR-0015). Polls a cloud provider's API, deduplicates against the kube-node inventory, and pushes the rest to argosd over HTTPS. See the [vm-collector operator guide](vm-collector.md) for deployment recipes and the [cloud accounts admin guide](cloud-accounts.md) for credential management.

### Connection

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_SERVER_URL` | yes | -- | argosd base URL. Supports a path prefix for gateway deployments. |
| `ARGOS_API_TOKEN` | yes | -- | Bearer PAT with the `vm-collector` scope, bound to the target cloud account at issuance. |

### Cloud account identity

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_VM_COLLECTOR_PROVIDER` | no | `outscale` | Cloud provider. Only `outscale` is supported in v1. |
| `ARGOS_VM_COLLECTOR_ACCOUNT_NAME` | yes | -- | Cloud account name. Must match `cloud_accounts.name` in argosd. The collector self-registers a placeholder if the account does not exist. |
| `ARGOS_VM_COLLECTOR_REGION` | yes | -- | Cloud region (e.g. `eu-west-2`). |
| `ARGOS_VM_COLLECTOR_PROVIDER_ENDPOINT_URL` | no | provider default | Override the cloud-provider API endpoint. Useful for sovereign-cloud regions or in-network mirrors. |

### Behaviour

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_VM_COLLECTOR_INTERVAL` | no | `5m` | Time between polling ticks. |
| `ARGOS_VM_COLLECTOR_FETCH_TIMEOUT` | no | `30s` | Per-tick timeout for cloud-provider API calls. |
| `ARGOS_VM_COLLECTOR_RECONCILE` | no | `true` | Soft-delete VMs that disappeared after each tick. |
| `ARGOS_VM_COLLECTOR_CREDENTIAL_REFRESH` | no | `1h` | How often to re-fetch AK/SK from argosd. |
| `ARGOS_VM_COLLECTOR_METRICS_ADDR` | no | `127.0.0.1:9090` | Listen address for the `/metrics` endpoint. **Set to `0.0.0.0:9090` when running in Kubernetes** so the kubelet can reach the liveness probe. |

### TLS, gateway, and proxy

The collector uses the same TLS / gateway / proxy variables as the kube push collector — see [`argos-collector` → TLS and gateway](#tls-and-gateway) and [`argos-collector` → Proxy](#proxy). Variables: `ARGOS_CA_CERT`, `ARGOS_CLIENT_CERT`, `ARGOS_CLIENT_KEY`, `ARGOS_EXTRA_HEADERS`, `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`.

> **No AK/SK env var.** Cloud-provider credentials live exclusively in argosd's `cloud_accounts` table and are fetched at runtime over HTTPS. This is deliberate — see [ADR-0015 §4](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md).

---

## argos-ingest-gw

The DMZ ingest gateway (ADR-0016). A stateless reverse proxy that runs in the DMZ, enforces a write-only allowlist of 18 collector routes, verifies bearer PATs against argosd, and forwards approved requests over mTLS. See [How to deploy the DMZ ingest gateway](how-to-deploy-dmz-ingest-gateway.md) for the full operator walkthrough.

### Inbound listener

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_GW_LISTEN_ADDR` | no | `:8443` | Bind address for the ingest listener (the port Envoy/WAF forwards to). |
| `ARGOS_INGEST_GW_LISTEN_TLS_CERT` | yes | — | Path to the PEM-encoded server certificate presented to Envoy (and to collectors, via Envoy passthrough). |
| `ARGOS_INGEST_GW_LISTEN_TLS_KEY` | yes | — | Path to the private key matching `ARGOS_INGEST_GW_LISTEN_TLS_CERT`. |
| `ARGOS_INGEST_GW_HEALTH_ADDR` | no | `:9090` | Bind address for the health and Prometheus metrics endpoints. No TLS. Bind to the pod IP — never expose via Envoy. |

### Upstream (argosd ingest listener)

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_GW_UPSTREAM_URL` | yes | — | Full URL of argosd's mTLS ingest listener. Example: `https://argosd-ingest.argos.svc.cluster.local:8443`. |
| `ARGOS_INGEST_GW_UPSTREAM_HOST` | no | host extracted from `UPSTREAM_URL` | Override the `Host:` header rewritten on proxied requests. Useful when argosd's TLS cert CN differs from the DNS name in the URL. |
| `ARGOS_INGEST_GW_UPSTREAM_TIMEOUT` | no | `30s` | Per-request timeout for upstream calls. On timeout the gateway returns 503 to the collector without caching the failure. |
| `ARGOS_INGEST_GW_UPSTREAM_CA_FILE` | yes | — | Path to the PEM-encoded CA bundle used to verify argosd's server cert. Required because argosd's ingest listener is typically signed by an internal CA. |

### mTLS client certificate (gateway → argosd)

The gateway always presents a client cert when connecting to argosd. The cert is loaded from two files and hot-reloaded on change via an `fsnotify` watcher — no pod restart required on rotation. See [the how-to guide](how-to-deploy-dmz-ingest-gateway.md) for the three ways to populate these files (Vault PKI, Kubernetes Secret / cert-manager, or file mount).

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_GW_CLIENT_CERT_FILE` | yes | `/etc/argos-ingest-gw/tls/tls.crt` | Path to the PEM-encoded mTLS client certificate. The Helm chart writes cert data here regardless of which TLS mode (vault / secret / file) is selected. |
| `ARGOS_INGEST_GW_CLIENT_KEY_FILE` | yes | `/etc/argos-ingest-gw/tls/tls.key` | Path to the private key matching `ARGOS_INGEST_GW_CLIENT_CERT_FILE`. |

### Token verification cache

The gateway verifies each collector PAT against argosd once and caches the result. The cache is keyed on the full token's SHA-256 — not just its 8-character prefix.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_GW_CACHE_TTL` | no | `60s` | How long a valid token verification result is cached. Revocation lag = this value worst case. |
| `ARGOS_INGEST_GW_CACHE_NEGATIVE_TTL` | no | `10s` | How long an invalid token (401 from argosd) is cached. Absorbs brute-force / scanner traffic without a full verify round-trip per request. |
| `ARGOS_INGEST_GW_CACHE_MAX_ENTRIES` | no | `10000` | Maximum number of cache entries (LRU eviction). 10 000 entries cap RAM at ~5 MiB. |

### Request handling

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_INGEST_GW_MAX_BODY_BYTES` | no | `10485760` (10 MiB) | Maximum accepted request body size in bytes. Requests exceeding this limit are rejected with 413 before any upstream call. |
| `ARGOS_INGEST_GW_LOG_LEVEL` | no | `info` | Structured log verbosity. One of `debug`, `info`, `warn`, `error`. |
| `ARGOS_INGEST_GW_SHUTDOWN_TIMEOUT` | no | `30s` | Graceful drain budget after SIGTERM. In-flight proxied requests complete up to this deadline.
