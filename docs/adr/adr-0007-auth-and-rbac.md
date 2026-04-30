---
title: "ADR-0007: Human authentication, roles, and admin-issued machine tokens"
status: "Proposed"
date: "2026-04-19"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "auth", "rbac", "oidc", "security"]
supersedes: ""
superseded_by: ""
---

# ADR-0007: Human authentication, roles, and admin-issued machine tokens

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

Argos today has exactly one auth primitive: bearer tokens declared through `LONGUE_VUE_API_TOKEN` / `LONGUE_VUE_API_TOKENS` environment variables, each carrying a `name` and a set of scopes (`read` / `write` / `delete` / `admin`). Scope enforcement is wired into every operation through `BearerAuth` middleware and the OpenAPI `security` blocks.

That works for machine callers and for one solo developer. It breaks down now that three new features are on the roadmap:

- **Human login with roles** (operators, auditors, viewers). Tokens have no notion of an actual person; every caller shows up in logs as its `token.name`, which is a label, not an identity.
- **Audit log** (who did what, when). Needs a human-actor concept by construction.
- **Role-gated edit forms in the UI** (per ADR-0006 follow-up). Needs a role model that maps to the existing scope grants.

Two further constraints from product:

- **Customers should be able to pick** between local credentials and their OIDC provider. Neither mode alone is acceptable: OIDC-only bricks installations during IdP outages; local-only doesn't match what enterprise buyers expect for SNC-aligned tooling.
- **Machine tokens must be created by a human in an admin UI**, not pulled from environment variables an operator has to hand-roll. The existing env-var path has no audit trail, no easy rotation, and every rotation is a Deployment edit.

Finally, ADR-0006 already anticipated this: its "Auth in the browser" paragraph parked `sessionStorage`-plus-bearer as an explicit v1 choice with a door open to "a stricter cookie-based session flow… if the threat model tightens." The threat model has tightened.

## Decision

Adopt a **dual-path auth model** with a unified downstream:

- **Humans** authenticate via either local username + password, **or** a configured OIDC provider, and receive a **server-side session cookie**.
- **Machines** continue to authenticate via `Authorization: Bearer <token>`, but tokens now live in a database table and are **created in the admin UI by a human**, not injected via env vars.
- Both paths feed the same request-context `caller{id, kind, role, scopes}` object. The existing per-operation scope checks (declared in `openapi.yaml`) are unchanged.

**Local users.**
- `users` table: `id`, `username` (case-insensitive unique), `password_hash` (argon2id, not bcrypt — the default parameters argon2id ships with resist GPU attacks better), `role`, `created_at`, `last_login_at`, `must_change_password`, `disabled_at`.
- Passwords validated at the API boundary: minimum 12 characters, no other complexity rules (NIST 800-63B line). No "strength meter" in the UI; a long single-word passphrase beats a short complex one.

**OIDC.**
- Parallel login path, not a replacement. An argosd instance can have OIDC configured, local-only, or both simultaneously.
- Config via env vars: `LONGUE_VUE_OIDC_ISSUER`, `LONGUE_VUE_OIDC_CLIENT_ID`, `LONGUE_VUE_OIDC_CLIENT_SECRET`, `LONGUE_VUE_OIDC_REDIRECT_URL`, `LONGUE_VUE_OIDC_SCOPES` (default `openid email profile`).
- Standard authorization-code flow with PKCE. State cookie gets HttpOnly + SameSite=Lax (Lax so the IdP's redirect works; Strict is fine for all other cookies).
- `user_identities` table links local users to external identities: `(issuer, subject) → user_id`. First successful OIDC login creates a shadow local user keyed on the stable `sub` claim; subsequent logins match on `(issuer, sub)`.
- First-login role: **`viewer`**. Not claim-driven — the IdP is trusted for identity, not authorization. Admins promote manually after first login. A future enhancement can map a named OIDC group claim to a role via config.

**Sessions.**
- Server-side. `sessions` table: `id` (random 32 bytes, base64-url), `user_id`, `created_at`, `last_used_at`, `expires_at`, `user_agent`, `source_ip`.
- Cookie: `argos_session=<id>`, `HttpOnly`, `SameSite=Strict`, `Secure` (when behind TLS), `Path=/`.
- 8-hour sliding window (`last_used_at` refreshed on each authenticated request; expiry = `last_used_at + 8h`). Logout deletes the row server-side — a revoked session stops working immediately across every tab.

**Roles.**
Four fixed roles, mapped onto the scopes we already have plus one new (`audit`):

| Role     | Grants                                                                 |
|----------|------------------------------------------------------------------------|
| `admin`  | All scopes + `manage:users` + `manage:tokens` + OIDC / audit settings  |
| `editor` | `read` + `write` on catalogued entities. No `delete`, no user/token mgmt |
| `auditor`| `read` + `audit` (will gate access to the future audit-log endpoint)   |
| `viewer` | `read` only                                                            |

Fixed-role shape is deliberate v1; granular RBAC (per-namespace, per-kind, custom roles) remains open for a future ADR if demand emerges.

**Machine tokens.**
- `api_tokens` table: `id`, `name`, `hash` (argon2id of the plaintext), `scopes` (text[]), `created_by_user_id`, `created_at`, `last_used_at`, `expires_at` (nullable), `revoked_at` (nullable).
- Only **admins** may issue tokens. Creation happens in the admin UI; the plaintext is shown **exactly once** at creation time (GitHub PAT model) and never again.
- Scopes at creation are a free subset of `{read, write, delete}`. No meta-scopes: tokens can't manage users, can't issue more tokens, can't read the audit log — human responsibilities stay with humans.
- Token validation: lookup by a short prefix stored in the clear, then constant-time hash compare against the matching row. Prefix scheme: `argos_pat_<8-char-random>_<plaintext>`. The prefix is also the only identifier ever logged; the plaintext never touches logs.
- Revocation (`revoked_at` set) is immediate: middleware checks `revoked_at IS NULL` on every authenticated request.

**First-install bootstrap.**
- When argosd starts and finds **zero users** in the database, it creates a single admin user named `admin`.
- If `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD` is set, that's used as the password.
- Otherwise, a cryptographically random 16-character password is generated and printed **once** to the startup log at WARN level, wrapped in a visible banner so it can't be missed in `kubectl logs`.
- The user is flagged `must_change_password=true`. The UI blocks every endpoint except the password-change form until the password is rotated.
- Idempotent: on subsequent starts the block is skipped entirely (it keys on "zero users", not on the env var).

**Env-var token path is cut in the same PR.**
- `LONGUE_VUE_API_TOKEN` and `LONGUE_VUE_API_TOKENS` are **removed**. argosd refuses to start if either is set, with an error message pointing at the admin panel. No shim, no deprecation warning window — per explicit direction, clean cut.
- Operators rotating from v0 to v1 read the bootstrap admin password from the startup log, log in, rotate it, create tokens in the admin UI, and update their Deployments / CI to inject those tokens.

**Unchanged.**
- OpenAPI per-operation scope declarations (the `security: - BearerAuth: [read]` blocks) stay as-is; scope checks continue to compare against `caller.scopes`.
- The in-process collector never goes through the HTTP layer and therefore needs no token. Its audit actor (ADR-0008 territory) is synthesised as `collector:<cluster-name>`.
- Existing response shapes (RFC 7807) and the `/metrics` endpoint (unauthenticated, Prometheus convention) are unaffected.

## Consequences

### Positive

- **POS-001**: Proper human identity unblocks audit log (ADR-0008 forthcoming) and role-gated editing (ADR-0006 IMP-001). Both were parked on "needs a `who`"; now they have one.
- **POS-002**: Two-path auth matches real customer ops: mixed environments where humans use OIDC and scripts still carry bearer tokens. Neither path is a downgrade of the other.
- **POS-003**: Admin-issued tokens move secrets out of Deployment manifests. Rotation is a UI click + paste-new-token-in-CI-secret, not a `kubectl rollout restart`.
- **POS-004**: Server-side sessions beat JWT-in-cookie here. Revocation is a single row delete. No clock-skew issues, no "forgot to rotate the signing key" class of bugs. Matches the "customer picks OIDC" goal: IdP access-token lifetimes don't leak into argosd's session lifetime.
- **POS-005**: First-login-is-`viewer` default for OIDC keeps authorization out of claim trust. An attacker who can federate into the IdP gets `viewer`, not `admin`.
- **POS-006**: Bootstrap banner (random password printed once) is a standard industry pattern (Grafana, Jenkins, Keycloak, Kafka UI). Operators know how to read it.

### Negative

- **NEG-001**: New schema surface: `users`, `user_identities`, `sessions`, `api_tokens`. Four tables, four migrations' worth of review.
- **NEG-002**: OIDC configuration is real work to get right — PKCE, state, nonce, clock drift, clock skew, issuer-URL validation. Using a maintained library (`coreos/go-oidc` + `golang.org/x/oauth2`) mitigates but doesn't eliminate.
- **NEG-003**: Cutting the env-var tokens cleanly means v0 → v1 is a **breaking upgrade**. Every operator has to rotate at install time. Acceptable at current scale; would need a deprecation window if this were already deployed broadly.
- **NEG-004**: Admin-only token issuance means non-admin operators who want a CI token have to ask an admin. Low friction in small teams; annoying at scale. Editor-level issuance can be added later without a breaking change — the scope column already exists.
- **NEG-005**: First-run log banner is easy to miss in noisy environments (CI re-starting argosd). Mitigation: banner repeats on every start until `must_change_password=false`. Operators will grep for it; the string stays predictable.

### Neutral

- **NEU-001**: We keep `argon2id` dependencies and nothing else crypto-heavy. The Go standard library plus `golang.org/x/crypto/argon2` and `coreos/go-oidc` are the only auth-relevant adds.
- **NEU-002**: `admin` scope from the existing token model becomes slightly different in semantics: it was "all scopes". Now it's "all scopes plus user / token / config management". Machine tokens stop being able to grant themselves the admin scope — only UI-issued human role assignments can.

## Alternatives Considered

### JWT instead of server-side sessions

- **ALT-001**: **Description**: Sign a JWT on login, store in cookie, verify the signature on each request. No sessions table.
- **ALT-002**: **Rejection Reason**: Revocation is the killer. Session rotation, password change, and admin-disable-user need a shared revocation check anyway — so you either carry a server-side blocklist (at which point the JWT buys nothing) or accept that a compromised token is valid until expiry. Also, JWT lifetime coupling is awkward when the customer's OIDC provider wants short access tokens: either the UI re-authenticates against the IdP constantly, or the argosd JWT outlives the IdP session, neither of which matches the "customer picks IdP, we don't care about their lifetimes" goal.

### OIDC-only

- **ALT-003**: **Description**: No local users. Every human authenticates against the configured IdP; env-var bootstrap creates a machine-only admin.
- **ALT-004**: **Rejection Reason**: Customer explicitly asked for both. On top of that: IdP outages would lock argosd admins out during the exact incident they need the CMDB to investigate. A local break-glass admin is cheap insurance.

### Local-only

- **ALT-005**: **Description**: Username/password only. Customers who want OIDC run an IdP-aware proxy in front of argosd.
- **ALT-006**: **Rejection Reason**: Reverse-proxy auth is a lot of tribal knowledge to push onto every customer deployment, and the proxy ends up having to carry the role mapping anyway. Federation is worth owning natively.

### OIDC groups / claims drive roles

- **ALT-007**: **Description**: Map an OIDC group claim to a role automatically (e.g., `argos-admins` → `admin`).
- **ALT-008**: **Rejection Reason** (v1): Moves authorization trust to the IdP. Many enterprise IdPs don't carry clean group claims; those that do aren't uniformly named. Customer-configurable claim mapping is a lot of UI and documentation for a v1. Manual promotion after first login covers the common case ("one admin bootstraps, invites a few, done"); claim-based mapping can be added later as an opt-in via a config table.

### Fixed bootstrap creds (`admin`/`admin`)

- **ALT-009**: **Description**: Ship with a well-known initial password so the customer can just log in.
- **ALT-010**: **Rejection Reason**: A CMDB containing infrastructure topology is a reconnaissance gold mine; shipping with default creds is indefensible even for "dev mode." The random-password-in-log + must-change-password pattern gives the same UX with none of the risk.

### External policy engine (OPA / Cedar)

- **ALT-011**: **Description**: Route every authorization decision to an external policy engine.
- **ALT-012**: **Rejection Reason**: Overkill for four fixed roles. The runtime dependency, the policy-as-code toolchain, and the per-request network hop all cost more than they buy until we need per-namespace / per-entity rules. Revisit if and when the fixed-role model proves too coarse.

### TOTP / hardware-key second factor at login

- **ALT-013**: **Description**: Require TOTP or WebAuthn on every human login.
- **ALT-014**: **Rejection Reason** (v1): Worth doing; out of scope for 0007. Customers who need 2FA today put it on their OIDC provider; that path already gives every argosd instance MFA as a side-effect. A future ADR can add native TOTP/WebAuthn on the local-password path without disrupting this one.

## Implementation Notes

- **IMP-001**: Migration `00014_users_sessions_tokens.sql` creates `users`, `user_identities`, `sessions`, `api_tokens`. Strict foreign keys: `user_identities.user_id` ON DELETE CASCADE, `sessions.user_id` ON DELETE CASCADE, `api_tokens.created_by_user_id` ON DELETE RESTRICT (tokens survive the user who minted them until explicitly revoked — lets an admin depart without all their tokens going dead the same day).
- **IMP-002**: OpenAPI additions:
  - `POST /v1/auth/login` (username/password → `Set-Cookie: argos_session=…`)
  - `POST /v1/auth/logout` (deletes the session row)
  - `GET /v1/auth/oidc/authorize` (redirects to the IdP)
  - `GET /v1/auth/oidc/callback` (exchanges code, creates session, redirects to `/ui/`)
  - `GET /v1/auth/me` (`{id, username, role, scopes, must_change_password}` — drives UI role-aware rendering)
  - `POST /v1/auth/change-password` (gated on the logged-in user)
  - Admin: `/v1/admin/users` (list/create/disable/update-role), `/v1/admin/tokens` (list/create/revoke), `/v1/admin/sessions` (list/revoke for support)
- **IMP-003**: Auth middleware ordering: try cookie first (check `sessions` table, verify not expired, refresh `last_used_at`), fall through to `Authorization: Bearer` (check `api_tokens` table, verify not revoked, refresh `last_used_at`). If both fail → 401 with `WWW-Authenticate` per RFC 7235. On success, attach `caller{kind: session|token, id, role, scopes, username|token_name}` to ctx.
- **IMP-004**: `internal/api/auth.go` grows into a package `internal/auth/` with submodules: `sessions`, `tokens`, `oidc`, `passwords`. The existing `BearerAuth` middleware stays at `internal/api/auth.go` as a thin adapter that delegates.
- **IMP-005**: First-run bootstrap runs in `cmd/argosd/main.go` before `http.ListenAndServe`. If `COUNT(*) FROM users WHERE role='admin' AND disabled_at IS NULL` is zero: determine password (env or random), argon2id-hash, insert user, log the banner. The check uses a single-statement transaction to avoid a race under horizontal scaling (two argosd replicas booting simultaneously — harmless, one INSERT wins on the unique username index).
- **IMP-006**: OIDC library: `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`. Issuer URL validated on argosd startup; invalid issuer → fatal so misconfiguration surfaces early.
- **IMP-007**: UI changes: replace the token-paste login with a form that has (a) username/password inputs always, (b) a "Sign in with <IdP>" button when `/v1/auth/me?probe` reports OIDC configured. Add admin panel at `/ui/admin/` with Users / Tokens / Sessions tabs. Hide admin nav entries for non-admins via `/v1/auth/me` role.
- **IMP-008**: Cookie security: `Secure` set when `LONGUE_VUE_ADDR` implies TLS or when `X-Forwarded-Proto: https` is received (configurable via `LONGUE_VUE_SESSION_SECURE_COOKIE=true|false|auto`, default `auto`). Plain HTTP in dev still works.
- **IMP-009**: Rate-limit the login endpoint at 5 requests / minute per source IP (sliding window, in-memory is fine at current scale; Postgres-backed if we ever run N replicas). Lockout is **not** added — opens a DOS vector where any internet-accessible argosd can have its admin locked out by anyone.
- **IMP-010**: Tests: fake OIDC provider using `httptest` so the callback flow can be exercised in CI without a real IdP. PG integration tests for session lifecycle + token hash lookup. Golden test for the bootstrap banner output (operators grep for the exact string).
- **IMP-011**: CLAUDE.md + README updates: new "Auth" section documenting local / OIDC / bootstrap. `deploy/README.md` gets a step 2.5 ("read the admin password from the log, rotate it") inserted into the install walkthrough.
- **IMP-012**: Rollout as three PRs:
  1. Users / sessions / local login + bootstrap + env-var removal (breaking upgrade).
  2. Admin panel: users CRUD, tokens CRUD, sessions read + revoke.
  3. OIDC.
  Each lands behind the feature's scope check and is independently mergeable; audit log (ADR-0008) follows once human identity is live.

## References

- **REF-001**: ADR-0006 — UI for audit and curated metadata — `docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md` (NEG-005 parked the sessionStorage-vs-cookie choice; this ADR takes it.)
- **REF-002**: RFC 6749 — OAuth 2.0 Authorization Framework — https://datatracker.ietf.org/doc/html/rfc6749
- **REF-003**: OpenID Connect Core 1.0 — https://openid.net/specs/openid-connect-core-1_0.html
- **REF-004**: NIST SP 800-63B — Digital Identity Guidelines (password composition) — https://pages.nist.gov/800-63-3/sp800-63b.html
- **REF-005**: OWASP Session Management Cheat Sheet — https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html
- **REF-006**: argon2id reference — https://github.com/P-H-C/phc-winner-argon2
- **REF-007**: `coreos/go-oidc` — https://github.com/coreos/go-oidc
- **REF-008**: ANSSI SecNumCloud requirements around authentication and session management — https://cyber.gouv.fr/enjeux-technologiques/cloud/
