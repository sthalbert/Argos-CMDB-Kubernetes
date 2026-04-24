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
| `ARGOS_SESSION_SECURE_COOKIE` | no | `auto` | Controls the `Secure` flag on the session cookie. Values: `auto` (mirror the request scheme -- HTTPS sets Secure), `always`, or `never`. Use `never` only for local HTTP development. |

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
