---
title: "ADR-0023: Per-account login lockout with boot-time admin rescue"
status: "Accepted"
date: "2026-05-09"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "security", "auth", "rate-limiting", "secnumcloud"]
supersedes: ""
superseded_by: ""
---

# ADR-0023: Per-account login lockout with boot-time admin rescue

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

- **Date:** 2026-05-09
- **Supersedes:** none
- **Superseded by:** none

## Context

The pre-existing per-IP login rate limiter (`internal/api/ratelimit.go`, 5 req/min/IP, ADR-0007 §IMP-009) is bypassable by IP rotation: an attacker spraying credentials from N source IPs gets 5×N attempts/minute against a single account with no per-account counter to detect or stop them. A Shannon AI pentester run flagged this as **AUTH-VULN-03** with a working live demonstration of the bypass.

## Decision

### Lockout

- After **6 consecutive failed password verifications**, the account auto-locks. The threshold lives as a package-level `const failedLoginLockoutThreshold` in `internal/api/auth_handlers.go`.
- Two new columns on `users`: `failed_login_count INTEGER NOT NULL DEFAULT 0` and `locked_at TIMESTAMPTZ` (NULL = not locked). Migration `00041_user_login_lockout.sql`.
- Counter increment happens in a `BEGIN + SELECT ... FOR UPDATE + conditional UPDATE + COMMIT` transaction (`*PG.IncrementFailedLogin`). Concurrent failed logins for the same account serialise; only one returns `locked=true` on the threshold-crossing call. Already-locked accounts are an idempotent no-op (counter does not advance past threshold; `locked_at` is not refreshed).
- Successful login clears the counter via `*PG.ResetFailedLogin`, called after `CreateSession` succeeds (so a session-create failure does not erroneously clear the counter).
- The per-IP limiter is unchanged — defense in depth.

### Anti-enumeration

The 401 returned for a locked account is **byte-identical** to the 401 returned for a wrong password and for an unknown user: same body (`{"detail":"invalid credentials","status":401,...}`), same headers (including `Www-Authenticate`). All three paths run a real or dummy `argon2` verify for timing uniformity. An attacker cannot use the lockout state itself to enumerate valid usernames. `TestLogin_LockedAccountReturnsGeneric401` asserts this with `bytes.Equal` on the body and equality on `Content-Type` + `Www-Authenticate`.

A `slog.Warn` fires once per lockout transition for operator visibility — internal only, not observable from the wire.

### Unknown usernames are never counted

`GetUserByUsername` returns `ErrNotFound` for missing users; the handler returns 401 immediately without invoking `IncrementFailedLogin`. There is no row to count against, and no row to insert (which would itself create an enumeration vector via "lockout state on a never-existed user"). 100 attempts against `nosuchuser` mutate nothing.

### No last-admin guard

Unlike `UpdateUserGuarded` / `DeleteUserGuarded`, the lockout has **no** last-admin guard. Two reasons:

1. The single-admin account is the most-attacked target in any deployment. Granting it unlimited unprotected login attempts would defeat the entire purpose for the highest-value account.
2. The recovery path below is robust enough to cover the bricked-system scenario.

Recovery — and only recovery — is the boot-time admin-rescue hook.

### Boot-time admin rescue

A new `rescueLockedAdminIfNeeded(ctx, *store.PG)` runs on every startup, immediately after `bootstrapAdminIfNeeded`:

1. Read `LONGUE_VUE_ADMIN_RESCUE_PASSWORD` from env. Empty → no-op.
2. `SELECT COUNT(*) FROM users WHERE role='admin' AND disabled_at IS NULL AND locked_at IS NULL`. If `> 0` → no-op.
3. `SELECT ... ORDER BY last_login_at DESC NULLS LAST, created_at ASC LIMIT 1` to pick the most-recently-active admin.
4. In one transaction: reset `password_hash`, clear `failed_login_count`, clear `locked_at`, clear `disabled_at`, force `must_change_password=TRUE`, delete all sessions for the user.
5. Insert a `source=system, action=auth.admin_rescue` audit event.
6. `slog.Error` with a loud banner so the rescue is visible in `kubectl logs` and any monitoring stack.

The Helm chart exposes `server.adminRescuePassword`, gated through the existing secret template alongside `bootstrapAdminPassword`.

### Admin unlock for non-rescue cases

For routine cases (user contacts helpdesk after being locked), an admin issues `PATCH /v1/admin/users/{id}` with `{"unlock": true}`. This is a new optional field on `UserUpdate`; the patch sets `failed_login_count = 0` and `locked_at = NULL` in the same statement as any other concurrent fields. The existing `AuditMiddleware` records the patch automatically.

## Consequences

- **Positive**: distributed brute-force at scale is bounded — even against the bootstrap admin. Anti-enumeration is preserved across all 401 paths. Operator-controlled recovery via env-var means a bricked deployment is recoverable in one pod restart, no DB shell required.
- **Negative**: the wrong-password code path now does an extra DB write (`IncrementFailedLogin`) that the locked / unknown paths do not, introducing a small timing observable that is not visible at the wire-level response but could in theory be measured. The spec accepts this trade-off; a defender could narrow it later by issuing the increment async.
- **Operational**: deployments that rely on the bootstrap admin should also baseline `LONGUE_VUE_ADMIN_RESCUE_PASSWORD` in their values, exactly as they do `LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD`. Without it set, a fully-locked deployment is unrecoverable without direct SQL.
- **Out of scope**: lockout on `POST /v1/auth/change-password` (already behind a session, so brute-force surface is much smaller); auto-unlock with backoff (rejected — permanent lock forces incident detection); email/Slack notification on lockout transitions (the `slog.Warn` is the substrate).

## References

- [`docs/authentication.md`](../authentication.md#account-lockout) — User-facing description of lockout + unlock + rescue.
- [`docs/operations.md`](../operations.md#locked-admin-recovery-rescue-env-var) — Runbook for the rescue procedure.
- [`docs/configuration.md`](../configuration.md#bootstrap) — `LONGUE_VUE_ADMIN_RESCUE_PASSWORD` reference.
- ADR-0007 — Original auth substrate, including the per-IP login limiter this layers on top of.
- ADR-0017 — Listener / TLS / proxy posture (the cookie `Secure` flag conditional referenced by sibling auth findings).
- Shannon AI pentester finding **AUTH-VULN-03** (driver).
