# Cloud Accounts

A **cloud account** is longue-vue's record of a single cloud-provider account that hosts platform infrastructure outside Kubernetes — VPN gateways, DNS servers, bastions, Vault clusters, and other supporting VMs. Each account stores the AK/SK that lets a [vm-collector](vm-collector.md) list its VMs, plus the curated metadata that completes the SecNumCloud chapter 8 asset cartography.

This guide covers the admin tasks: registering an account, setting and rotating credentials, issuing a collector token, disabling and deleting accounts, and the operationally-critical master-key backup procedure.

For background on why cloud accounts exist as a first-class entity (and not, say, env vars on the collector), see [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md).

## Why two-step onboarding

longue-vue uses a hybrid onboarding flow: the **collector self-registers** a placeholder row on first contact, then an **admin fills in the credentials** through the UI. This separation is intentional — the AK/SK never leaves longue-vue, the collector binary stays stateless and credential-free, and rotation works without redeploying anything.

The flow looks like this:

```
Operator                  Collector                 longue-vue
    |                         |                         |
    | deploy --account=X      |                         |
    |------------------------>|                         |
    |                         | GET /credentials        |
    |                         |------------------------>|
    |                         |       404 not_registered|
    |                         |<------------------------|
    |                         | POST /v1/cloud-accounts |
    |                         |------------------------>|
    |                         |  pending_credentials    |
    |                         |<------------------------|
    |                         |   (waits, retries)      |
    |                         |                         |
Admin                         |                         |
    | open Admin > Cloud      |                         |
    |     Accounts; sees 🔴   |                         |
    | enter AK + SK           |                         |
    |---------------------------------------------------|
    |                         |                  active |
    |                         | GET /credentials        |
    |                         |------------------------>|
    |                         |       200 AK + SK       |
    |                         |<------------------------|
    |                         | (begins polling)        |
```

Until the admin enters credentials, the row is in `pending_credentials` status and the collector loops harmlessly. The admin home banner surfaces the count of pending accounts so nothing sits unattended.

## Register a cloud account

You can register an account two ways: ahead of time through the UI, or implicitly by deploying the collector first.

### Option A — admin-first (UI)

1. Sign in as an `admin` user.
2. Navigate to **Admin > Cloud Accounts**.
3. Click **Register account**.
4. Fill in:
   - **Provider** — `outscale` is the only value in v1.
   - **Name** — operator-friendly. Must be unique per provider. Editable later. Example: `acme-prod`.
   - **Region** — provider region code. Example: `eu-west-2`.
   - **Access Key** and **Secret Key** — optional at this stage. If left blank, the row is created in `pending_credentials` status and you can fill creds later.
   - **Owner**, **Criticality**, **Notes**, **Runbook URL** — curated metadata, all optional.
5. Click **Create**.

If you provided AK/SK, the row is immediately `active` and the collector (once deployed) will pick it up. If not, the row is `pending_credentials` and waits for credentials.

**API equivalent:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST https://longue-vue.internal:8080/v1/admin/cloud-accounts \
  -H 'Content-Type: application/json' \
  -d '{
    "provider": "outscale",
    "name": "acme-prod",
    "region": "eu-west-2",
    "access_key": "AKIA...",
    "secret_key": "wJalrXUt...",
    "owner": "platform-team",
    "criticality": "high",
    "runbook_url": "https://wiki.example.com/runbooks/acme-prod"
  }'
```

Response (SK never returned):

```json
{
  "id": "1f2c4a3e-...",
  "provider": "outscale",
  "name": "acme-prod",
  "region": "eu-west-2",
  "status": "active",
  "access_key": "AKIA...",
  "owner": "platform-team",
  "criticality": "high",
  "runbook_url": "https://wiki.example.com/runbooks/acme-prod",
  "created_at": "2026-04-26T09:12:00Z",
  "updated_at": "2026-04-26T09:12:00Z"
}
```

### Option B — collector-first (hybrid)

1. Issue a `vm-collector` PAT bound to a not-yet-registered account name (see [Issue a collector token](#issue-a-collector-token)).
2. Deploy `longue-vue-vm-collector` with `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME=acme-prod` (see [vm-collector guide](vm-collector.md)).
3. The collector calls `POST /v1/cloud-accounts` and creates the placeholder row.
4. The admin home banner lights up: **1 cloud account pending credentials**.
5. Admin opens **Admin > Cloud Accounts**, finds the 🔴 row, clicks **Set credentials**, and enters AK/SK.
6. The collector picks up the credentials within `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH` (default 1 hour) and starts ingesting.

This option is convenient when the operator deploying the collector is not the same person as the admin holding the cloud-provider credentials.

## Set or rotate credentials

Credentials are write-only from the admin endpoints — the SK is never returned in any response. Set or rotate them whenever:

- A new `pending_credentials` account needs activation.
- The cloud-provider AK/SK has been rotated upstream.
- A SK has leaked or is suspected compromised.
- The `last_error` field shows authentication failures for several ticks.

**UI:**

1. **Admin > Cloud Accounts**.
2. Click the account row.
3. Click **Set credentials** (or **Rotate credentials** if creds already exist).
4. Enter the new AK and SK. The SK field is `<input type="password">` and never displayed back.
5. Click **Save**.

The status transitions to `active` immediately. The collector picks up the new SK on its next credential-refresh tick (default 1 hour, configurable via `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`).

**API equivalent:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X PATCH \
  https://longue-vue.internal:8080/v1/admin/cloud-accounts/<id>/credentials \
  -H 'Content-Type: application/json' \
  -d '{
    "access_key": "AKIA...",
    "secret_key": "wJalrXUt..."
  }'
```

Response: `204 No Content`.

The SK is encrypted with AES-256-GCM using the master key from `LONGUE_VUE_SECRETS_MASTER_KEY` before it touches the database. The plaintext is never logged, never returned by `GET /v1/admin/cloud-accounts/{id}`, and never appears in audit-log payloads.

## Issue a collector token

Each `vm-collector` PAT is **bound to exactly one cloud account at issuance**. A leaked token can only access that one account's credentials and VMs — strictly less than a regular `read`-scope PAT can do.

**UI (recommended):**

1. **Admin > Cloud Accounts**.
2. Open the account.
3. Click **Issue collector token**.
4. Enter a name (e.g. `acme-prod-collector`).
5. Click **Mint**.
6. Copy the plaintext token (`longue_vue_pat_<prefix>_<suffix>`). It is shown **once** and never again.

The form pre-binds the token to the account UUID and pre-selects the `vm-collector` role. There are no other choices to make.

**API equivalent:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST \
  https://longue-vue.internal:8080/v1/admin/cloud-accounts/<id>/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name": "acme-prod-collector"}'
```

Response (token shown once):

```json
{
  "id": "8a3b...",
  "name": "acme-prod-collector",
  "role": "vm-collector",
  "bound_cloud_account_id": "1f2c4a3e-...",
  "token": "longue_vue_pat_3f9c1e7a_5N2pKdQ...",
  "created_at": "2026-04-26T09:30:00Z"
}
```

Pass the `token` value to the collector as `LONGUE_VUE_API_TOKEN`. See the [vm-collector guide](vm-collector.md) for deployment.

If you ever need to revoke a token (rotation, suspected leak, decommission), do it from **Admin > Tokens** — the bound account context is informational only, the revoke flow is the same as for any PAT.

## Disable, enable, delete

### Disable

Disabling pauses the account without losing data: the collector starts getting `403` on `GET /credentials`, stops polling, and the VMs already in the CMDB are left in place. Use this when:

- A cloud account is being decommissioned but its VMs are still live and should remain visible.
- A SK has leaked and you want to stop the collector immediately while you rotate.
- An account is suspected of misconfiguration and you want to investigate without a noisy error rate.

**UI:** click **Disable** on the account row.

**API:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST \
  https://longue-vue.internal:8080/v1/admin/cloud-accounts/<id>/disable
```

The status transitions to `disabled`. Re-enable with the symmetric `/enable` endpoint or the **Enable** button.

### Delete

Deleting **cascades to every VM** in the account and to every PAT bound to it. There is no undo.

**UI:** click **Delete** on the account row, then confirm the modal.

**API:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X DELETE \
  https://longue-vue.internal:8080/v1/admin/cloud-accounts/<id>
```

The cascade chain:

| Deleting `cloud_accounts` row | Cascades to |
|-------------------------------|-------------|
| `virtual_machines` | All rows with this `cloud_account_id` are hard-deleted (no tombstone — the account itself is gone). |
| `tokens` | All PATs with `bound_cloud_account_id = <this id>` are revoked. |

The audit log retains the deletion event with the actor, timestamp, and the list of affected VM IDs.

## Master-key backup

> **Critical.** Losing `LONGUE_VUE_SECRETS_MASTER_KEY` means every encrypted SK in the database becomes unrecoverable. longue-vue will start, but it cannot decrypt any account's credentials. You will need to **re-enter every SK by hand** through the admin UI. There is no recovery path. Treat the master key with the same care as a database backup encryption key.

### Generate a master key

```sh
openssl rand -base64 32
```

This produces a 32-byte (256-bit) key encoded as base64. Set it as `LONGUE_VUE_SECRETS_MASTER_KEY` on the longue-vue Deployment — never commit it to git, never put it in `values.yaml`, always deliver it via a Kubernetes Secret or your secret manager of choice.

### Verify the right key is loaded

On startup, longue-vue logs a master-key fingerprint:

```
INFO secrets master key loaded fingerprint=3f9c1e7a
```

The fingerprint is the first 8 hex characters of the SHA-256 of the master key. The key itself is never logged. If you rotate the master key, the fingerprint changes — a quick visual check after a deploy confirms that the right key landed.

If longue-vue is started with cloud accounts present in the database but no master key set, it refuses to start with a fatal error. This is intentional: silently running without the ability to decrypt credentials would mean every VM-collector tick fails, and the operator might not notice for hours.

### Back up the master key

The master key is **separate from your database backup**. Standard practice:

1. Store it in your organisation's vault — HashiCorp Vault, AWS Secrets Manager, GCP Secret Manager, Azure Key Vault, or a tightly-permissioned offline-only secret store. Restrict access to the same group that holds the database root credentials.
2. Document where it lives in the same runbook that describes the database backup.
3. After any rotation, update both the live secret and the backup copy in the same change window.

### Recover from master-key loss

If the master key is lost and unrecoverable:

1. Generate a new master key.
2. Set it as `LONGUE_VUE_SECRETS_MASTER_KEY`.
3. Restart longue-vue.
4. Open **Admin > Cloud Accounts**. Every account will still exist with its name, region, owner, etc., but `secret_key_kid` will reference the old (lost) key.
5. For each account, click **Rotate credentials** and re-enter the AK and SK from the cloud-provider console.
6. longue-vue encrypts each new SK under the new master key.

This is the only recovery path. The retained AK and metadata reduce the work — you don't have to re-create accounts from scratch — but every SK must be re-entered.

### Future: KMS-backed master key

A planned follow-up (`FUT-001` in ADR-0015) integrates a KMS — AWS KMS, Vault Transit, or GCP Cloud KMS — so the symmetric master key never lives in an env var on production. Until then, treat the env-var-backed master key as the single most sensitive secret in the deployment.

## Audit log

Every operation on cloud accounts and credentials is recorded in the audit log:

| Action | Resource | Recorded |
|--------|----------|----------|
| Create account | `cloud_account` | actor, body (SK scrubbed), result |
| Update account | `cloud_account` | actor, merge-patch body, result |
| Set / rotate credentials | `cloud_account` | actor, AK only (SK scrubbed) |
| Disable / enable | `cloud_account` | actor, target id |
| Delete account | `cloud_account` | actor, cascade list |
| Issue collector token | `token` | actor, token name, bound account id |
| Credential read by collector | `cloud_account` | caller (vm-collector PAT id), account name. **Response body is never logged**, even at debug level. |

Filter the audit log by `resource_type=cloud_account` from **Admin > Audit** or the API:

```bash
curl -sS -b /tmp/longue-vue.cookies \
  'https://longue-vue.internal:8080/v1/admin/audit?resource_type=cloud_account&since=2026-04-01T00:00:00Z' \
  | jq '.items[:5]'
```

## Cloud-provider permissions

The AK/SK only needs **read** access to the VM list:

| Provider | Required permission |
|----------|---------------------|
| Outscale | `ReadVms` (and the implied authentication: API access enabled on the account, the AK/SK pair issued under a user with the `vm:read` policy or equivalent). |

Future providers will get their own row in this table. The collector never calls any write endpoint on the cloud provider — it lists VMs and metadata, nothing more.

## See also

- [vm-collector — operator guide](vm-collector.md) — deploying the binary that consumes these credentials.
- [Configuration reference](configuration.md) — `LONGUE_VUE_SECRETS_MASTER_KEY` and the full vm-collector env-var table.
- [API reference — cloud accounts and virtual machines](api-reference.md) — endpoint catalogue.
- [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) — design rationale.
- [ADR-0007](adr/adr-0007-auth-and-rbac.md) — token / scope / role model that the `vm-collector` scope plugs into.
