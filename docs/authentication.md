# Authentication and Authorization

longue-vue uses dual-path authentication: humans authenticate via session cookies, machines authenticate via bearer tokens. Both paths feed the same role-based access control (RBAC) system.

## Overview

```
  Human (browser)                   Machine (CI, collector, script)
       |                                       |
  POST /v1/auth/login               Authorization: Bearer longue_vue_pat_...
  or OIDC flow                                 |
       |                                       |
  Set-Cookie: longue_vue_session          Token lookup (argon2id hash)
       |                                       |
       +------- Caller{id, role, scopes} ------+
                         |
                   Scope check per endpoint
```

Every authenticated request resolves to a `Caller` with an identity, role, and set of scopes. The middleware checks the required scope for each endpoint; if the caller lacks it, the request gets a 403.

## Roles and scopes

longue-vue has four fixed roles. Roles are not customizable -- they map directly to scope sets.

| Role | Scopes | Typical use |
|------|--------|-------------|
| `admin` | `read`, `write`, `delete`, `admin`, `audit` | Full access. Manages users, tokens, sessions. |
| `editor` | `read`, `write` | Creates and updates CMDB resources. Cannot manage users or tokens. |
| `auditor` | `read`, `audit` | Read-only access plus the audit log. For compliance teams. |
| `viewer` | `read` | Read-only access to all CMDB data. Default role for new OIDC users. |

Scope requirements per endpoint group:

| Endpoint pattern | Required scope |
|------------------|---------------|
| `GET /v1/*` (list, get) | `read` |
| `POST /v1/*` (create, upsert) | `write` |
| `PATCH /v1/*` (update) | `write` |
| `DELETE /v1/*` (delete) | `delete` |
| `POST /v1/*/reconcile` | `write` |
| `GET /v1/admin/audit` | `audit` |
| `GET/POST/PATCH/DELETE /v1/admin/*` (users, tokens, sessions) | `admin` |

## Local users

### Bootstrap admin

On first startup with an empty database, longue-vue creates a single `admin` user:

- If `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` is set, it uses that value.
- Otherwise, it generates a random 16-character password and prints it **once** to the startup log inside a banner.

The bootstrap admin has `must_change_password=true`, which blocks every API endpoint except `/v1/auth/change-password` until the password is rotated. `/healthz`, `/readyz`, and `/metrics` remain accessible.

### Creating additional users

Admins create users through the admin panel or the API:

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"initial-password","role":"editor"}'
```

The new user must change their password on first login.

### Updating users

Admins can change a user's role, reset their password, or disable them:

```bash
# Promote to admin
curl -sS -b /tmp/longue-vue.cookies -X PATCH http://localhost:8080/v1/admin/users/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{"role":"admin"}'

# Disable (revokes all active sessions)
curl -sS -b /tmp/longue-vue.cookies -X PATCH http://localhost:8080/v1/admin/users/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{"disabled":true}'
```

## OIDC federation

OIDC is optional. When enabled, a "Sign in with ..." button appears on the login page alongside the local username/password form.

### Setup

1. Register longue-vue at your IdP as an application:
   - **Redirect URI:** `https://<longue-vue-host>/v1/auth/oidc/callback`
   - **Grant types:** `authorization_code`
   - **Scopes:** `openid email profile`

2. Configure longue-vue (see [Configuration](configuration.md) for all variables):
   ```bash
   LONGUE_VUE_OIDC_ISSUER="https://idp.example.com/realms/longue-vue"
   LONGUE_VUE_OIDC_CLIENT_ID="longue-vue"
   LONGUE_VUE_OIDC_CLIENT_SECRET="your-secret"
   LONGUE_VUE_OIDC_REDIRECT_URL="https://longue-vue.example.com/v1/auth/oidc/callback"
   ```

3. Restart longue-vue. It fetches the issuer's OpenID Connect discovery document on boot and fails fatally if unreachable.

### How it works

1. The user clicks "Sign in with ..." in the UI.
2. The browser hits `GET /v1/auth/oidc/authorize`, which generates a state + PKCE challenge + nonce, stores them in the database, and 302-redirects to the IdP.
3. After authentication at the IdP, the browser arrives at `GET /v1/auth/oidc/callback` with an authorization code.
4. longue-vue exchanges the code, verifies the ID token (issuer, audience, signature, nonce), and finds or creates a "shadow user" keyed on `(issuer, sub)`.
5. A session cookie is set and the browser redirects to `/ui/`.

### Shadow users

OIDC users are called "shadow users" in longue-vue. Key properties:

- They are created automatically on first OIDC login.
- They start with role `viewer`. longue-vue does **not** trust OIDC group claims for authorization -- an admin must promote them manually.
- They carry an unusable password hash, so they cannot log in via the local username/password form.
- They are identified by the `(issuer, sub)` pair, not by email or username.

### Important notes

- OIDC is additive. The local `admin` bootstrap still works, providing a break-glass login.
- `GET /v1/auth/config` is public and tells the UI whether OIDC is configured and what the button label should be.
- The authorize-code flow uses PKCE (S256) and a server-side nonce for security.

## Machine tokens (PAT)

Machine tokens are for non-human callers: CI pipelines, the push-mode collector, monitoring scripts, and automation.

### Creating tokens

Only admins can create tokens, via the admin panel or the API:

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
```

The response contains the plaintext token **exactly once**:

```json
{
  "id": "...",
  "name": "ci-pipeline",
  "prefix": "abcd1234",
  "token": "longue_vue_pat_abcd1234_xxxxxxxxxxxxxxxxxxxxxxxx",
  "scopes": ["read", "write"],
  "created_at": "..."
}
```

Store the `token` value in a secrets manager immediately. longue-vue persists only its argon2id hash -- the plaintext cannot be retrieved later.

### Token format

Tokens follow the format `longue_vue_pat_<8-char-prefix>_<32-char-secret>`. The prefix is stored in plaintext for O(1) lookup; the secret is hashed with argon2id.

### Using tokens

```bash
curl -H "Authorization: Bearer longue_vue_pat_abcd1234_xxxxxxxxxxxxxxxxxxxxxxxx" \
  http://localhost:8080/v1/clusters
```

### Revoking tokens

```bash
# Via API
curl -sS -b /tmp/longue-vue.cookies -X DELETE http://localhost:8080/v1/admin/tokens/<id>

# Or via the admin panel at /ui/admin/tokens
```

Revocation is immediate. Subsequent requests bearing the revoked token receive 401. Token rows are retained for audit continuity.

### Best practices

- One token per use case (one per push collector, one per CI pipeline).
- Grant minimal scopes -- the push collector needs `read` + `write`, not `admin`.
- Rotate tokens periodically. Revoke and re-mint.

## Forced password rotation

Users with `must_change_password=true` are blocked from every endpoint except:

- `POST /v1/auth/change-password`
- `GET /v1/auth/me` (so the UI knows to redirect)
- `/healthz`, `/readyz`, `/metrics` (always unauthenticated)

The UI detects this flag on every route change and redirects to the password-change page.

Changing the password clears the flag and invalidates all other active sessions for the same user.

## Session management

### How sessions work

- Sessions are server-side, stored in the database.
- The `longue_vue_session` cookie is `HttpOnly` + `SameSite=Strict` with an 8-hour sliding expiry.
- The `Secure` flag is controlled by `LONGUE_VUE_SESSION_SECURE_COOKIE` (default: `auto`).

### Viewing and revoking sessions

Admins can view and revoke active sessions in the admin panel at `/ui/admin/sessions`, or via the API:

```bash
# List sessions
curl -sS -b /tmp/longue-vue.cookies http://localhost:8080/v1/admin/sessions | jq .

# Revoke a session
curl -sS -b /tmp/longue-vue.cookies -X DELETE http://localhost:8080/v1/admin/sessions/<id>
```

### Logout

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST http://localhost:8080/v1/auth/logout
```

Deletes the server-side session and clears the cookie.

## Unauthenticated endpoints

These endpoints do not require any authentication:

| Endpoint | Purpose |
|----------|---------|
| `GET /healthz` | Liveness probe. |
| `GET /readyz` | Readiness probe (verifies database connectivity). |
| `GET /metrics` | Prometheus metrics. |
| `GET /v1/auth/config` | Public auth configuration (OIDC enabled? button label?). |
| `POST /v1/auth/login` | Local username/password login. |
| `GET /v1/auth/oidc/authorize` | OIDC flow start. |
| `GET /v1/auth/oidc/callback` | OIDC flow completion. |
| `GET /ui/*` | Static UI assets. |

## Audit log

Every state-changing API call and every admin-panel read is recorded in the `audit_events` table. Read-only cluster-browsing GETs are not logged to keep the table bounded.

Sensitive fields (passwords, tokens, OIDC secrets) are scrubbed before persistence. Audit insertion failures are logged at ERROR but never cause a 5xx response.

The audit log is accessible to users with the `audit` scope (roles `admin` and `auditor`):

```bash
curl -sS -b /tmp/longue-vue.cookies 'http://localhost:8080/v1/admin/audit?resource_type=user&action=user.create' | jq .
```

See [API Reference](api-reference.md) for full filter options.
