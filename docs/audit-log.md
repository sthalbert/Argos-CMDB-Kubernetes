<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# Audit Log

## Overview

longue-vue maintains an append-only audit trail of every state-changing call made through the API. This satisfies the ANSSI SecNumCloud (SNC) requirement for traceability of administrative actions: auditors can answer "who changed what, when, and from where" without relying on reverse-engineering application logs. Every audit event is a permanent row in the `audit_events` table; rows are never modified or deleted by the application.

## What gets recorded

The audit middleware records:

- **Every non-GET request** — any `POST`, `PUT`, `PATCH`, or `DELETE` through the API, regardless of the resource type or the caller's role.
- **Every `GET /v1/admin/*` request** — reads of user, token, session, audit, and settings data. Who looked at the admin panel matters for SNC auditors.
- **Every credentials-fetch request** — `GET /v1/cloud-accounts/.../credentials` (and the by-name variant) is audited on every call because it is the only path where a plaintext secret key leaves the database.

**Cluster-browsing GETs are deliberately not logged.** A `GET /v1/clusters`, `GET /v1/pods`, or similar inventory read does not produce an audit row. Recording every read would generate unbounded noise and make the audit table impractical for meaningful review — the goal is traceability of state changes, not a clickstream log.

`/healthz`, `/readyz`, `/metrics`, `/ui/*`, and `/v1/auth/config` are also excluded as they are chatty operational endpoints with no security relevance.

## Sensitive field scrubbing

Request bodies are snapshotted (up to 4 KiB) and stored in the `details` column. Before storage, the following fields are replaced with `[redacted]`:

| Field | Reason |
|---|---|
| `password` | Local-login credential |
| `new_password` | Password-change payload |
| `current_password` | Password-change payload |
| `client_secret` | OIDC client secret |
| `token` | Raw PAT value on verify calls |
| `code_verifier` | PKCE value on OIDC callback |
| `secret_key` | Cloud-provider secret key |
| `access_key` | Cloud-provider access key |

Response bodies are never stored. The credentials-fetch endpoints are special-cased: even request metadata is logged but the response body — which carries the plaintext secret key — is never captured at any log level.

## Viewing the audit log (UI)

Navigate to **Admin → Audit** (`/ui/admin/audit`).

- **Auditors** (`auditor` role) see only the Audit tab. All other admin tabs are hidden.
- **Admins** (`admin` role) see the Audit tab alongside Users, Tokens, and Sessions.

The table is newest-first and paginated. You can filter by:

- Actor (user or token name)
- Resource type and ID
- HTTP status class (2xx / 4xx / 5xx)
- Date range

## Querying the audit log (API)

```
GET /v1/admin/audit
```

Requires the `audit` scope. Both the `admin` and `auditor` roles carry this scope.

### Query parameters

| Parameter | Type | Description |
|---|---|---|
| `limit` | integer | Page size (default 50, max 200) |
| `before` | string (UUID) | Cursor — return events older than this event ID |

The response is newest-first. Use the last `id` in the current page as `before` in the next request to page through the full history.

### Example

```bash
# Log in and store a session cookie
curl -sS -c /tmp/lv.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<password>"}'

# Fetch the most recent 20 audit events
curl -sS -b /tmp/lv.cookies \
  'http://localhost:8080/v1/admin/audit?limit=20'

# Fetch the next page (replace <last-id> with the id from the last row above)
curl -sS -b /tmp/lv.cookies \
  'http://localhost:8080/v1/admin/audit?limit=20&before=<last-id>'
```

With a bearer token:

```bash
curl -sS -H 'Authorization: Bearer longue_vue_pat_...' \
  'http://localhost:8080/v1/admin/audit?limit=20'
```

## Failure mode

Audit insertion failures are logged at `ERROR` level but are **never** surfaced to the caller as a `5xx` response. The rationale: if the `audit_events` table is briefly unreachable (disk full, replica lag, transient network partition), the correct outcome is a gap in the audit log — not a complete outage of the CMDB. Operators should monitor the `longue_vue_*` Prometheus metrics and application logs for audit insert errors and investigate promptly, but a brief gap is recoverable; a total service outage is not.

## Source attribution

Every audit event carries a `source` column that identifies which listener handled the request:

| Value | Meaning |
|---|---|
| `api` | Request arrived on the public listener (`:8080`) |
| `ingest_gw` | Request arrived on the mTLS-only ingest listener (`:8443`), forwarded by the DMZ ingest gateway |
| `system` | Synthetic event generated internally (bootstrap, migration side-effects) |
| `mcp` | Request originated from the MCP server |

This makes it straightforward to answer "what writes came through the DMZ perimeter?" with a single `WHERE source = 'ingest_gw'` clause.

The `source_ip` column records the effective client IP. When `LONGUE_VUE_TRUSTED_PROXIES` is configured, `X-Forwarded-For` is walked right-to-left through trusted hops and the first untrusted IP is used — prepended (attacker-controlled) values cannot reach the stored IP. With an empty trust list, `X-Forwarded-For` is ignored entirely and the TCP peer address is used directly.

## Retention

longue-vue does not implement a built-in retention policy. Audit rows accumulate indefinitely. This is intentional for SNC compliance: the qualification framework requires long-term traceability, and automatic deletion without an explicit operator decision would be a compliance risk.

**Recommended approach for operators:**

- Archive rows older than your retention threshold to cold storage (object store, WORM bucket) before deleting.
- Add a scheduled job that runs `DELETE FROM audit_events WHERE occurred_at < NOW() - INTERVAL '2 years'` (or your policy period) after archival completes.
- The `occurred_at` column is indexed descending, so range deletes are efficient.

Built-in retention configuration is planned as a follow-up feature.

## References

- [ADR-0006](adr/adr-0006-ui-for-audit-and-curated-metadata.md) — Web UI design, audit view rationale
- [ADR-0007](adr/adr-0007-auth-and-rbac.md) — Role and scope definitions; `audit` scope assignment
- [ADR-0017](adr/adr-0017-public-listener-tls-posture-and-proxy-trust.md) — Proxy trust, source IP resolution, last-admin guard
- [Authentication guide](authentication.md) — Local users, OIDC, tokens, roles, sessions
