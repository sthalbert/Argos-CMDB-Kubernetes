<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# Operations Runbook

This document is the SRE/operator runbook hub for longue-vue. It covers day-to-day operational procedures: first-run bootstrap, credential rotation, backup and restore, incident response, and upgrade steps.

**What this document does not cover:** initial deployment, Helm chart values, Kubernetes RBAC, or TLS certificate provisioning. For those, see `docs/deployment/`.

---

## Contents

1. [Day-1: First-run bootstrap](#day-1-first-run-bootstrap)
2. [Day-2: Token and credential rotation](#day-2-token-and-credential-rotation)
3. [Backup and restore](#backup-and-restore)
4. [Incident response](#incident-response)
5. [Last-admin lockout recovery](#last-admin-lockout-recovery)
6. [Upgrade procedure](#upgrade-procedure)
7. [Shutdown semantics](#shutdown-semantics)
8. [Health and readiness](#health-and-readiness)
9. [Observability](#observability)
10. [Common operator pitfalls](#common-operator-pitfalls)
11. [References](#references)

---

## Day-1: First-run bootstrap

### First-admin creation

When longue-vue starts and the database contains zero active admin users (`COUNT(users WHERE role='admin' AND disabled_at IS NULL) = 0`), it automatically creates an admin account named `admin`.

The password is sourced in this order:

1. **`LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD`** — set this env var to a known password before first boot. Recommended for automated deployments.
2. **Random 16-character string** — if the env var is absent, longue-vue generates a random password and prints it exactly once at startup inside a loud banner:

   ```
   ================================================================
     LONGUE-VUE FIRST-RUN BOOTSTRAP
     username : admin
     password : <generated>
   ================================================================
   ```

   Capture this from your container or service logs immediately. It is not stored in plaintext and will not be printed again. On Kubernetes:

   ```bash
   kubectl -n longue-vue-system logs -l app.kubernetes.io/name=longue-vue | grep -A5 "FIRST-RUN"
   ```

### Forced password rotation

The bootstrap admin account is created with `must_change_password=true`. Every API endpoint — except `POST /v1/auth/change-password` — returns `403` with `{"error":"password_change_required"}` until the password is rotated.

**Rotate immediately on first login:**

1. Open the UI at `/ui/` and log in as `admin`.
2. You will be redirected to the change-password screen automatically.
3. Set a strong password and submit.

Or via API:

```bash
curl -sS -c /tmp/lv.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<bootstrap-password>"}'

curl -sS -b /tmp/lv.cookies -X POST http://localhost:8080/v1/auth/change-password \
  -H 'Content-Type: application/json' \
  -d '{"current_password":"<bootstrap-password>","new_password":"<strong-password>"}'
```

Bootstrap only fires once. Subsequent restarts skip it as long as at least one active admin exists.

---

## Day-2: Token and credential rotation

### Rotating personal access tokens (PATs)

PATs are shown in plaintext exactly once at creation — they are argon2id-hashed at rest. There is no way to retrieve the plaintext of an existing token.

**Procedure:**

1. Admin UI → Admin → Tokens.
2. Create a new token with the same name and scopes. Copy the plaintext value into your secrets manager.
3. Update the consumer (collector, CI pipeline, API client) with the new token.
4. Revoke the old token from the Tokens tab.

For `vm-collector` tokens, the new token must be bound to the same `cloud_account_id` as the old one.

### Rotating cloud-account AK/SK

The secret key (`secret_key`) is AES-256-GCM encrypted in the database. The access key (`access_key`) is stored in plaintext because it is a public identifier used in request signatures.

**Procedure:**

1. Admin UI → Admin → Cloud Accounts → select account → Edit.
2. Paste the new `access_key` and `secret_key`.
3. Save. The store re-encrypts the new SK immediately.
4. The VM collector picks up the rotated credentials within `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH` (default 1 hour) on its next credentials-fetch tick — no restart required.

To force an immediate refresh, restart the `longue-vue-vm-collector` pod.

### Rotating the master encryption key

> **Future work.** Only `kid=v1` is recognised today. A formal multi-key rotation ADR is queued but not yet shipped.

The current state: there is one master key (`LONGUE_VUE_SECRETS_MASTER_KEY`). Every `cloud_accounts` row with a non-NULL `secret_key_encrypted` is encrypted under this key with AAD bound to the row's UUID. There is no online key-rotation API.

**If you must rotate the master key today:**

1. Take a full database backup first (see [Backup and restore](#backup-and-restore)).
2. Decrypt every `secret_key_encrypted` row using the old key — this requires a one-off migration script outside of longue-vue.
3. Re-encrypt each row with the new key.
4. Replace `LONGUE_VUE_SECRETS_MASTER_KEY` in your secrets manager and restart longue-vue.

At startup, longue-vue logs the first 8 hex characters of `SHA-256(masterKey)` as a fingerprint:

```
secrets: master key loaded, fingerprint=a3f7c201
```

Use this fingerprint to confirm the correct key is loaded before proceeding.

### Session secrets / cookie rotation

> **Not yet exposed as a configuration option.** Session cookies are signed/encrypted using an internal key that is not configurable via env var in the current release.

A rolling restart of longue-vue effectively invalidates all active sessions — users will be prompted to log in again. If you need to force a session purge for a security incident, a rolling restart is the current mechanism. Track the missing configuration knob as a follow-up.

---

## Backup and restore

PostgreSQL is the single source of truth for all longue-vue data: clusters, namespaces, nodes, workloads, pods, services, ingresses, PVs, PVCs, cloud accounts, VMs, users, tokens, sessions, audit events, and settings.

### Taking a backup

Standard `pg_dump`:

```bash
pg_dump -h <host> -U <user> -Fc longue-vue > longue-vue-$(date +%Y%m%d-%H%M%S).pgdump
```

Or from inside a Kubernetes cluster:

```bash
kubectl -n longue-vue-system exec deploy/postgresql -- \
  pg_dump -U postgres -Fc longue-vue > longue-vue-$(date +%Y%m%d-%H%M%S).pgdump
```

### Restoring a backup

```bash
pg_restore -h <host> -U <user> -d longue-vue --clean --if-exists longue-vue-<timestamp>.pgdump
```

### Critical: encrypted columns and UUID preservation

Rows in `cloud_accounts` carry `secret_key_encrypted` bytes that are AAD-bound to the row's primary key (a UUID). **The UUID must be preserved exactly through backup and restore.** If a row's UUID changes — for example, by importing into a new table with `INSERT … DEFAULT` or by using a migration script that generates new UUIDs — the AAD check will fail and the SK decrypt will return an error. The collector will then be unable to fetch credentials for that account.

`pg_dump`/`pg_restore` preserves UUIDs by default. If you reconstruct data via SQL `INSERT` statements, always supply the original UUID explicitly.

### Master key

The master key (`LONGUE_VUE_SECRETS_MASTER_KEY`) is **not** stored in the database. Back it up alongside each database dump but in a separate, access-controlled secret store (e.g. Vault, a cloud KMS, or a hardware HSM). A database dump is useless without the matching key, and vice versa.

### Audit log retention

The audit log lives in the `audit_events` table — it is included in every `pg_dump`. There is no built-in TTL or automatic pruning today; retention is managed externally (e.g. a scheduled SQL job):

```sql
-- Example: delete audit events older than 1 year
DELETE FROM audit_events WHERE created_at < NOW() - INTERVAL '1 year';
```

> **Future work.** Built-in retention policy (configurable TTL via admin settings) is queued but not yet shipped.

---

## Incident response

### Where to look first

| Signal | Where to look |
|--------|---------------|
| Application errors | Structured JSON logs; filter on `"level":"error"` |
| State-changing calls | `GET /v1/admin/audit` (requires `audit` scope) |
| Throughput / latency | `/metrics` scrape → Prometheus → Grafana; see `docs/monitoring.md` |
| DB health | `/readyz` — returns `503` when the DB is unreachable |

### 5xx storm

1. Check `/readyz` — a `503` means the DB connection pool is exhausted or the database is down.
2. Look for `"level":"error"` log lines with `"component":"audit"` — audit insertion failures are non-fatal but indicate a table or index problem.
3. Check the collector tick: look for `"component":"collector"` + `"level":"error"` log lines. A Kubernetes API timeout causes the tick to fail but leaves existing store data intact (reconciliation only runs after a successful list).

### Authentication outage

- OIDC login fails? Check whether the OIDC issuer's discovery document is reachable from longue-vue. Local username+password login continues to work in parallel — OIDC and local auth are independent paths.
- All logins fail? Check that the database is reachable (`/readyz`) and that the `users` table is not locked.
- Bearer tokens rejected? Verify the token prefix matches `longue_vue_pat_` or the legacy `argos_pat_` prefix. Tokens with `vm-collector` scope can only be used by the VM collector endpoints — not for general API access.

### Stuck `pending_credentials` cloud account

A cloud account with status `pending_credentials` means the VM collector has registered the account placeholder (via `POST /v1/cloud-accounts`) but an admin has not yet pasted the AK/SK. A red banner on the admin home page surfaces this state.

Resolution: Admin UI → Cloud Accounts → select the account → Edit → paste `access_key` and `secret_key` → Save.

---

## Last-admin lockout recovery

### What 409 `last_admin_protection` means

`PATCH /v1/admin/users/{id}` and `DELETE /v1/admin/users/{id}` refuse any operation that would leave the deployment with zero active admins. The guard runs inside a serialised database transaction so concurrent demotions cannot race past it. The HTTP response is:

```json
HTTP 409 Conflict
{"error": "last_admin_protection"}
```

This is a safety feature, not a bug. The operation you are trying to perform would lock every admin out of the admin panel.

### Recovery: re-enable a disabled admin via direct database update

If you are already locked out (no active admin exists, e.g. after a data migration error), use direct SQL:

```sql
UPDATE users
   SET disabled_at = NULL,
       role        = 'admin'
 WHERE email = 'someone@example.com';
```

Restart longue-vue — it re-reads session state from the database. The recovered account can log in normally.

> **Note:** this update bypasses the audit log. Record an out-of-band justification (incident ticket, change record) before executing it.

If you do not know which email to use:

```sql
SELECT id, email, role, disabled_at
  FROM users
 ORDER BY created_at ASC
 LIMIT 10;
```

---

## Upgrade procedure

### Migration model

Database migrations are managed with [goose](https://github.com/pressly/goose) and embedded in the longue-vue binary. When `LONGUE_VUE_AUTO_MIGRATE=true` (the default), longue-vue runs pending migrations on startup before accepting traffic.

### Recommended upgrade steps

1. **Take a database backup** (see [Backup and restore](#backup-and-restore)) before deploying the new version.
2. Deploy the new binary or image with `LONGUE_VUE_AUTO_MIGRATE=true`.
3. Verify that startup logs show `migrations: all up to date` (or the list of applied migrations).
4. Run smoke checks: `/healthz`, `/readyz`, `/v1/clusters`.
5. Optionally revert `LONGUE_VUE_AUTO_MIGRATE` to `false` for subsequent restarts if your policy requires manual migration sign-off.

On Kubernetes with a rolling deployment:

```bash
kubectl -n longue-vue-system set image deploy/longue-vue longue-vue=longue-vue:<new-version>
kubectl -n longue-vue-system rollout status deploy/longue-vue
```

### Rollback

goose `down` (rollback) is not exposed via an env var. To roll back:

1. Restore the pre-upgrade database backup.
2. Redeploy the previous binary or image.

Some migrations are intentionally forward-only (e.g. columns dropped for legacy API tokens). Check the migration file's rollback comment before attempting a partial SQL rollback.

---

## Shutdown semantics

longue-vue handles `SIGINT` and `SIGTERM` with a graceful drain:

- The HTTP server stops accepting new connections and waits for in-flight requests to complete.
- Collector goroutines receive context cancellation. An in-flight collector tick will complete its current operation or hit `LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT` before the goroutine exits.
- `LONGUE_VUE_SHUTDOWN_TIMEOUT` (default 15 seconds) bounds the entire drain window. Requests still in-flight after this deadline are forcibly terminated.

For zero-downtime restarts, use a rolling deployment strategy (Kubernetes `RollingUpdate` or a process supervisor with overlap). `/readyz` going healthy on the new instance signals readiness.

---

## Health and readiness

| Endpoint | Auth | Healthy response | When unhealthy |
|----------|------|-----------------|----------------|
| `GET /healthz` | None | `200 OK` | Never — process is up if it responds |
| `GET /readyz` | None | `200 OK` | `503` when DB is unreachable or pending migrations exist |

Use `/readyz` as the Kubernetes `readinessProbe` target. Use `/healthz` as the `livenessProbe` target.

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
```

---

## Observability

### Metrics

Prometheus metrics are served at `GET /metrics` (unauthenticated, by Prometheus convention). Key metrics:

| Metric | Description |
|--------|-------------|
| `longue_vue_http_requests_total` | Request count by method, route, status class |
| `longue_vue_http_request_duration_seconds` | Latency histogram |
| `longue_vue_collector_upserts_total` | Collector upserts per cluster and resource type |
| `longue_vue_collector_errors_total` | Tick failures per cluster |
| `longue_vue_collector_last_poll_timestamp_seconds` | Unix timestamp of last successful tick |
| `longue_vue_impact_queries_total` | Impact graph queries by entity type |
| `longue_vue_build_info` | Binary version label |

See `docs/monitoring.md` for alert rules and Grafana dashboard guidance.

### Logs

Logs are structured JSON. Set your log aggregator to parse JSON fields. Key correlation fields:

| Field | Description |
|-------|-------------|
| `request_id` | UUID generated per HTTP request; present on every log line emitted during that request |
| `level` | `debug`, `info`, `warn`, `error` |
| `component` | Subsystem: `collector`, `audit`, `auth`, `eol`, etc. |
| `caller_id` | UUID of the authenticated user or token that made the request |

To correlate an audit event with application logs, match `audit_events.id` against the `audit_event_id` log field emitted on insertion.

---

## Common operator pitfalls

### Removed env vars — startup will fail fast

`LONGUE_VUE_API_TOKEN` and `LONGUE_VUE_API_TOKENS` were removed in ADR-0007. If either is set in your environment or Helm values, longue-vue refuses to start with a clear error:

```
FATAL: LONGUE_VUE_API_TOKEN is no longer supported; issue tokens via the admin panel
```

Migrate to PATs issued through Admin → Tokens. Remove the env var from all deployment manifests.

### `LONGUE_VUE_REQUIRE_HTTPS=true` without TLS configured

This env var instructs longue-vue to refuse startup if it cannot guarantee credentials will travel over HTTPS. The check passes when either:

- Native TLS is configured (`LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT` + `LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY` both set), **or**
- A trusted-proxy posture is in place: `LONGUE_VUE_TRUSTED_PROXIES` is non-empty **and** the session cookie policy is `SecureAlways`.

If neither condition holds, longue-vue exits at startup. Fix: configure TLS, or set `LONGUE_VUE_TRUSTED_PROXIES` to the CIDR of your TLS-terminating reverse proxy.

### `LONGUE_VUE_TRUSTED_PROXIES` left empty

When empty (the default), longue-vue ignores `X-Forwarded-For` and `X-Forwarded-Proto` headers entirely — this is the secure default. If your ingress or load balancer sits upstream and you rely on XFF for client IP logging or HSTS, set this CIDR list explicitly:

```
LONGUE_VUE_TRUSTED_PROXIES=10.0.0.0/8
```

Be as narrow as possible — the CIDR list defines which peers longue-vue trusts to set proxy headers.

### Master key not set when cloud accounts exist

If at least one `cloud_accounts` row has a non-NULL `secret_key_encrypted`, longue-vue requires `LONGUE_VUE_SECRETS_MASTER_KEY` at startup and refuses to start without it. Check the startup log for:

```
FATAL: LONGUE_VUE_SECRETS_MASTER_KEY is required: N cloud accounts have encrypted credentials
```

Retrieve the key from your secrets manager and set the env var.

---

## References

- [ADR-0007](adr/adr-0007-auth-and-rbac.md) — auth tokens, bootstrap admin, PAT format
- [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) — cloud accounts, AK/SK encryption, vm-collector token scope
- [ADR-0017](adr/adr-0017-public-listener-tls-posture-and-proxy-trust.md) — last-admin guard, trusted proxies, REQUIRE_HTTPS
- [Authentication](authentication.md) — full auth reference: local users, OIDC, roles, sessions
- [Audit Log](audit-log.md) — what gets recorded, scrubbing rules, querying, retention
- [Configuration](configuration.md) — all environment variables
- [Cloud Accounts](cloud-accounts.md) — AK/SK rotation workflow, master key fingerprint, credential-fetch endpoint
- [Monitoring](monitoring.md) — Prometheus metrics, alert rules, Grafana dashboard
