# API Reference

Argos exposes a REST API at `/v1/`. The authoritative schema is the OpenAPI 3.1 spec at `api/openapi/openapi.yaml`. This page provides a concise reference with curl examples.

## Conventions

### Base URL

```
http://localhost:8080/v1
```

### Content types

- Requests: `application/json` for create/update; `application/merge-patch+json` for PATCH.
- Responses: `application/json`.
- Errors: `application/problem+json` (RFC 7807).

### Error format

All errors follow RFC 7807:

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "cluster not found"
}
```

### Authentication

Every endpoint except health probes, metrics, and the auth flow requires authentication.

**Session cookie** (humans):

```bash
curl -sS -c /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"secret"}'

# Subsequent requests carry the cookie:
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/clusters
```

**Bearer token** (machines):

```bash
curl -H "Authorization: Bearer argos_pat_xxxx_yyyy" http://localhost:8080/v1/clusters
```

### Pagination

All list endpoints use cursor-based pagination:

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | integer | 50 | Maximum items to return. |
| `cursor` | string | -- | Opaque cursor from a previous response's `next_cursor`. |

Response shape:

```json
{
  "items": [...],
  "next_cursor": "eyJpZCI6Ii4uLiJ9"
}
```

Pass `next_cursor` as the `cursor` query parameter to fetch the next page. When `next_cursor` is `null` or absent, there are no more pages.

---

## Clusters

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/clusters` | `read` | List clusters. Optional `?name=exact-match` filter. |
| POST | `/v1/clusters` | `write` | Register or fetch a cluster by name (idempotent — see below). |
| GET | `/v1/clusters/{id}` | `read` | Get a cluster by ID. |
| PATCH | `/v1/clusters/{id}` | `write` | Update mutable fields (merge-patch). |
| DELETE | `/v1/clusters/{id}` | `delete` | Delete a cluster and all its children. |

**`POST /v1/clusters` is idempotent on `name` (ADR-0016 §6):**

| Pre-state | Status | Body |
|-----------|--------|------|
| No row with that `name` | `201 Created` | The newly created cluster object. |
| Row already exists with that `name` | `200 OK` | The existing cluster object. The request body is ignored on a hit. |

When you get a `200`, follow up with `PATCH /v1/clusters/{id}` to apply any field updates you intended — the idempotent POST does not merge the request body on a hit.

> **Audit note:** audit consumers that query for "cluster creates" by filtering on status code 201 will now miss the `200` case. Query by `action=cluster.create` instead, or include both status codes.

**Create or fetch a cluster:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "prod",
    "display_name": "Production",
    "environment": "production"
  }'
# Returns 201 on first call, 200 on subsequent calls.
```

**Update curated metadata:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X PATCH http://localhost:8080/v1/clusters/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{
    "owner": "platform-team",
    "criticality": "high",
    "runbook_url": "https://wiki.example.com/runbooks/prod"
  }'
```

## Nodes

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/nodes` | `read` | List nodes. |
| POST | `/v1/nodes` | `write` | Create/upsert a node. |
| GET | `/v1/nodes/{id}` | `read` | Get a node by ID. |
| PATCH | `/v1/nodes/{id}` | `write` | Update a node. |
| DELETE | `/v1/nodes/{id}` | `delete` | Delete a node. |
| POST | `/v1/nodes/reconcile` | `write` | Reconcile nodes for a cluster. |

## Namespaces

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/namespaces` | `read` | List namespaces. |
| POST | `/v1/namespaces` | `write` | Create/upsert a namespace. |
| GET | `/v1/namespaces/{id}` | `read` | Get a namespace by ID. |
| PATCH | `/v1/namespaces/{id}` | `write` | Update a namespace. |
| DELETE | `/v1/namespaces/{id}` | `delete` | Delete a namespace. |
| POST | `/v1/namespaces/reconcile` | `write` | Reconcile namespaces for a cluster. |

## Pods

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/pods` | `read` | List pods. Supports `?image=substring` and `?node_name=exact` filters. |
| POST | `/v1/pods` | `write` | Create/upsert a pod. |
| GET | `/v1/pods/{id}` | `read` | Get a pod by ID. |
| PATCH | `/v1/pods/{id}` | `write` | Update a pod. |
| DELETE | `/v1/pods/{id}` | `delete` | Delete a pod. |
| POST | `/v1/pods/reconcile` | `write` | Reconcile pods for a namespace. |

**Filter pods by image:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/v1/pods?image=log4j:2.15'
```

**Filter pods by node (impact analysis):**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/v1/pods?node_name=worker-02.prod'
```

**Filter pods by workload:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'http://localhost:8080/v1/pods?workload_id=<uuid>'
```

## Workloads

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/workloads` | `read` | List workloads. Supports `?image=substring` filter. |
| POST | `/v1/workloads` | `write` | Create/upsert a workload. |
| GET | `/v1/workloads/{id}` | `read` | Get a workload by ID. |
| PATCH | `/v1/workloads/{id}` | `write` | Update a workload. |
| DELETE | `/v1/workloads/{id}` | `delete` | Delete a workload. |
| POST | `/v1/workloads/reconcile` | `write` | Reconcile workloads for a namespace. |

Workloads are polymorphic on `kind` (Deployment, StatefulSet, DaemonSet). Kind-specific detail lives in the `spec` JSONB field.

## Services

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/services` | `read` | List services. |
| POST | `/v1/services` | `write` | Create/upsert a service. |
| GET | `/v1/services/{id}` | `read` | Get a service by ID. |
| PATCH | `/v1/services/{id}` | `write` | Update a service. |
| DELETE | `/v1/services/{id}` | `delete` | Delete a service. |
| POST | `/v1/services/reconcile` | `write` | Reconcile services for a namespace. |

## Ingresses

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/ingresses` | `read` | List ingresses. |
| POST | `/v1/ingresses` | `write` | Create/upsert an ingress. |
| GET | `/v1/ingresses/{id}` | `read` | Get an ingress by ID. |
| PATCH | `/v1/ingresses/{id}` | `write` | Update an ingress. |
| DELETE | `/v1/ingresses/{id}` | `delete` | Delete an ingress. |
| POST | `/v1/ingresses/reconcile` | `write` | Reconcile ingresses for a namespace. |

## Persistent Volumes

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/persistentvolumes` | `read` | List persistent volumes. |
| POST | `/v1/persistentvolumes` | `write` | Create/upsert a PV. |
| GET | `/v1/persistentvolumes/{id}` | `read` | Get a PV by ID. |
| PATCH | `/v1/persistentvolumes/{id}` | `write` | Update a PV. |
| DELETE | `/v1/persistentvolumes/{id}` | `delete` | Delete a PV. |
| POST | `/v1/persistentvolumes/reconcile` | `write` | Reconcile PVs for a cluster. |

## Persistent Volume Claims

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/persistentvolumeclaims` | `read` | List PVCs. |
| POST | `/v1/persistentvolumeclaims` | `write` | Create/upsert a PVC. |
| GET | `/v1/persistentvolumeclaims/{id}` | `read` | Get a PVC by ID. |
| PATCH | `/v1/persistentvolumeclaims/{id}` | `write` | Update a PVC. |
| DELETE | `/v1/persistentvolumeclaims/{id}` | `delete` | Delete a PVC. |
| POST | `/v1/persistentvolumeclaims/reconcile` | `write` | Reconcile PVCs for a namespace. |

## Reconcile endpoints

Reconcile endpoints delete CMDB rows that no longer exist in the live Kubernetes cluster. They are used by the push collector and mirror the pull collector's internal `Delete*NotIn` logic.

**Cluster-scoped** (nodes, namespaces, persistent volumes):

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/v1/nodes/reconcile \
  -H 'Content-Type: application/json' \
  -d '{
    "cluster_id": "<uuid>",
    "keep_names": ["node-1", "node-2", "node-3"]
  }'
```

**Namespace-scoped** (pods, services, ingresses, PVCs):

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/v1/pods/reconcile \
  -H 'Content-Type: application/json' \
  -d '{
    "namespace_id": "<uuid>",
    "keep_names": ["web-abc123", "api-def456"]
  }'
```

**Workloads** (namespace-scoped, keyed on kind + name):

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/v1/workloads/reconcile \
  -H 'Content-Type: application/json' \
  -d '{
    "namespace_id": "<uuid>",
    "keep_kinds": ["Deployment", "StatefulSet"],
    "keep_names": ["web", "redis"]
  }'
```

Response:

```json
{"deleted": 3}
```

---

## Auth

| Method | Path | Auth required | Description |
|--------|------|---------------|-------------|
| GET | `/v1/auth/config` | no | Public: is OIDC enabled? button label? |
| POST | `/v1/auth/login` | no | Username/password login. Sets session cookie. Rate-limited: 5 req/min per IP (429 when exceeded). |
| POST | `/v1/auth/logout` | yes | End the current session. |
| GET | `/v1/auth/me` | yes | Caller identity, role, scopes, `must_change_password`. |
| POST | `/v1/auth/change-password` | session only | Change the current user's password. |
| POST | `/v1/auth/verify` | mTLS client cert | Token verification for the DMZ ingest gateway (see below). |
| GET | `/v1/auth/oidc/authorize` | no | Start the OIDC flow (302 to IdP). |
| GET | `/v1/auth/oidc/callback` | no | Complete the OIDC flow (302 to UI). |

**Login:**

```bash
curl -sS -c /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"my-password"}'
# 204 No Content on success; session cookie set.
```

**Who am I:**

```bash
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/auth/me | jq .
```

```json
{
  "id": "...",
  "username": "admin",
  "role": "admin",
  "scopes": ["read","write","delete","admin","audit"],
  "must_change_password": false
}
```

**Change password:**

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/auth/change-password \
  -H 'Content-Type: application/json' \
  -d '{"current_password":"old","new_password":"new-strong-passphrase"}'
# 204 on success. All other sessions invalidated.
```

**`POST /v1/auth/verify` — token verification (DMZ ingest gateway, ADR-0016 §5):**

This endpoint is served **only on argosd's mTLS ingest listener** (`:8443` when `ARGOS_INGEST_LISTEN_ADDR` is set). It is not reachable on argosd's public `:8080` listener. The caller must present a valid mTLS client certificate — there is no `Authorization` header on the verify call itself. The ingest gateway uses this endpoint to verify collector PATs before forwarding requests.

Request body:

```json
{"token": "argos_pat_<prefix>_<suffix>"}
```

Successful response (`200 OK`):

```json
{
  "valid": true,
  "caller_id": "8c4b1234-...",
  "kind": "token",
  "token_name": "prod-collector",
  "scopes": ["read", "write"],
  "bound_cloud_account_id": null,
  "exp": 1735689600
}
```

Response fields:

| Field | Type | Description |
|-------|------|-------------|
| `valid` | boolean | Always `true` on a 200 response. |
| `caller_id` | UUID | The token's owner (user ID). |
| `kind` | string | Always `"token"` for PATs. |
| `token_name` | string | The human-readable name given to the token at issuance. |
| `scopes` | string[] | The scopes granted to the token. |
| `bound_cloud_account_id` | UUID or null | For `vm-collector` PATs: the cloud account the token is bound to. Null for all other token types. |
| `exp` | integer | Unix timestamp when the token expires. `0` means no expiry. |

Invalid token (`401 Unauthorized`): returns an RFC 7807 problem doc with no detail — no information about why the token was rejected is returned:

```json
{
  "type": "about:blank",
  "title": "Unauthorized",
  "status": 401
}
```

Rate limit: 100 req/s per source IP on argosd (the gateway cache reduces steady-state call volume to well below this). The `token` field in the request body is redacted from the audit log.

---

## Admin

All admin endpoints require the `admin` scope.

### Users

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/admin/users` | List users (paginated). |
| POST | `/v1/admin/users` | Create a user. |
| GET | `/v1/admin/users/{id}` | Get a user. |
| PATCH | `/v1/admin/users/{id}` | Update role, password, or disabled state. Returns `409 Conflict` if the patch would demote (`role != admin`) or disable the only remaining active admin. |
| DELETE | `/v1/admin/users/{id}` | Delete a user. Returns `409 Conflict` when the caller targets themselves, when the user owns active API tokens, or when the target is the only remaining active admin. |

Both PATCH and DELETE enforce the last-admin invariant atomically inside
a single PostgreSQL transaction — two simultaneous demotion / disable /
delete requests on different admins will serialize and the second to
commit will receive `409` rather than orphaning the deployment.

**Create a user:**

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/admin/users \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"initial-pass","role":"editor"}'
```

### Tokens

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/admin/tokens` | List tokens (metadata only, no plaintext). |
| POST | `/v1/admin/tokens` | Mint a new token. Plaintext shown once. |
| DELETE | `/v1/admin/tokens/{id}` | Revoke a token. |

**Mint a token:**

```bash
curl -sS -b /tmp/argos.cookies -X POST http://localhost:8080/v1/admin/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-pipeline","scopes":["read","write"]}'
```

### Sessions

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/admin/sessions` | List active sessions. |
| DELETE | `/v1/admin/sessions/{id}` | Revoke a session. |

### Settings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/admin/settings` | Get current runtime settings. |
| PATCH | `/v1/admin/settings` | Update runtime settings (merge-patch). |

**Get settings:**

```bash
curl -sS -b /tmp/argos.cookies http://localhost:8080/v1/admin/settings | jq .
```

```json
{
  "eol_enabled": true,
  "updated_at": "2026-04-24T10:54:34Z"
}
```

**Toggle EOL enrichment:**

```bash
curl -sS -b /tmp/argos.cookies -X PATCH http://localhost:8080/v1/admin/settings \
  -H 'Content-Type: application/json' \
  -d '{"eol_enabled": false}'
```

| Field | Type | Description |
|-------|------|-------------|
| `eol_enabled` | boolean | Enable/disable the EOL enricher at runtime. See [EOL Enrichment](eol-enrichment.md). |
| `updated_at` | datetime | Last time settings were modified. |

### Audit

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/admin/audit` | `audit` | Paginated audit events (newest first). |

**Query audit events:**

```bash
curl -sS -b /tmp/argos.cookies \
  'http://localhost:8080/v1/admin/audit?resource_type=cluster&action=cluster.create&since=2026-01-01T00:00:00Z' \
  | jq '.items[:3]'
```

Filter parameters:

| Parameter | Type | Description |
|-----------|------|-------------|
| `actor_id` | UUID | Events by a specific user. |
| `resource_type` | string | e.g., `cluster`, `user`, `token`. |
| `resource_id` | string | Events for a specific resource. |
| `action` | string | e.g., `user.create`, `cluster.delete`. |
| `since` | datetime | Lower bound (inclusive). |
| `until` | datetime | Upper bound (exclusive). |

---

## Impact Analysis

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| GET | `/v1/impact/{entity_type}/{id}` | `read` | Dependency graph for an entity. |

**Entity types:** `cluster`, `node`, `namespace`, `pod`, `workload`, `service`, `ingress`, `persistentvolume`, `persistentvolumeclaim`.

**Query parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `depth` | integer (1–3) | `2` | Number of relationship hops to traverse. |

**Example:**

```bash
curl -sS -b /tmp/argos.cookies \
  'http://localhost:8080/v1/impact/node/<uuid>?depth=2' | jq .
```

```json
{
  "root": { "id": "...", "type": "node", "name": "worker-1", "status": "Ready" },
  "nodes": [
    { "id": "...", "type": "node", "name": "worker-1", "status": "Ready" },
    { "id": "...", "type": "cluster", "name": "prod", "status": "v1.30.2" },
    { "id": "...", "type": "pod", "name": "nginx-abc", "status": "Running" }
  ],
  "edges": [
    { "from": "<cluster-id>", "to": "<node-id>", "relation": "contains" },
    { "from": "<node-id>", "to": "<pod-id>", "relation": "hosts" }
  ]
}
```

**Relation types:**

| Relation | Meaning |
|----------|---------|
| `contains` | Parent scope contains child (Cluster → Namespace, Namespace → Pod). |
| `owns` | Controller owns managed resource (Workload → Pod). |
| `hosts` | Infrastructure hosts workload (Node → Pod). |
| `binds` | Storage binding (PV ↔ PVC). |

---

## Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | no | Liveness probe. Returns `{"status":"ok"}`. |
| GET | `/readyz` | no | Readiness probe. Verifies database connectivity. |

---

For the full schema (request/response bodies, field types, constraints), see the OpenAPI specification at `api/openapi/openapi.yaml`.

---

## Cloud accounts and virtual machines (ADR-0015)

[ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) introduces non-Kubernetes platform VMs as a first-class entity. Cloud accounts hold the AK/SK that lets a [vm-collector](vm-collector.md) list its VMs; virtual machines are the inventory rows produced by the collector.

Two new path families and one new scope are involved:

- `/v1/admin/cloud-accounts/*` — admin-only management of cloud accounts and their credentials.
- `/v1/cloud-accounts/*` — narrow `vm-collector` scope used by the collector binary at runtime.
- `/v1/virtual-machines/*` — read/write VM inventory (mostly read for humans, write for the collector).

### Cloud accounts

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| GET | `/v1/admin/cloud-accounts` | `admin` | List paginated. |
| POST | `/v1/admin/cloud-accounts` | `admin` | Create / pre-register. |
| GET | `/v1/admin/cloud-accounts/{id}` | `admin` | SK never returned. |
| PATCH | `/v1/admin/cloud-accounts/{id}` | `admin` | Curated metadata + name. |
| PATCH | `/v1/admin/cloud-accounts/{id}/credentials` | `admin` | Set / rotate AK/SK. |
| POST | `/v1/admin/cloud-accounts/{id}/disable` | `admin` | |
| POST | `/v1/admin/cloud-accounts/{id}/enable` | `admin` | |
| DELETE | `/v1/admin/cloud-accounts/{id}` | `admin` | Cascades to VMs + tokens. |
| POST | `/v1/admin/cloud-accounts/{id}/tokens` | `admin` | Mint a `vm-collector` PAT bound to this account. |
| POST | `/v1/cloud-accounts` | `vm-collector` | Idempotent first-contact registration. |
| PATCH | `/v1/cloud-accounts/{id}/status` | `vm-collector` | Heartbeat-only (`last_seen_at`, `last_error`). |
| GET | `/v1/cloud-accounts/by-name/{name}/credentials` | `vm-collector` | Plaintext AK/SK over TLS. |
| GET | `/v1/cloud-accounts/{id}/credentials` | `vm-collector` | Same, by id. |

**Create a cloud account (admin):**

```bash
curl -sS -b /tmp/argos.cookies -X POST \
  https://argos.internal:8080/v1/admin/cloud-accounts \
  -H 'Content-Type: application/json' \
  -d '{
    "provider": "outscale",
    "name": "acme-prod",
    "region": "eu-west-2",
    "access_key": "AKIA...",
    "secret_key": "wJalrXUt..."
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
  "created_at": "2026-04-26T09:12:00Z",
  "updated_at": "2026-04-26T09:12:00Z"
}
```

If `access_key` and `secret_key` are omitted, the row is created in `status: pending_credentials` and a later `PATCH /credentials` is required.

**Set or rotate credentials (admin):**

```bash
curl -sS -b /tmp/argos.cookies -X PATCH \
  https://argos.internal:8080/v1/admin/cloud-accounts/<id>/credentials \
  -H 'Content-Type: application/json' \
  -d '{
    "access_key": "AKIA...",
    "secret_key": "wJalrXUt..."
  }'
# 204 No Content
```

The SK is encrypted with AES-256-GCM under `ARGOS_SECRETS_MASTER_KEY` before it touches the database.

**Mint a collector token (admin):**

```bash
curl -sS -b /tmp/argos.cookies -X POST \
  https://argos.internal:8080/v1/admin/cloud-accounts/<id>/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name": "acme-prod-collector"}'
```

Response (token shown **once**):

```json
{
  "id": "8a3b...",
  "name": "acme-prod-collector",
  "role": "vm-collector",
  "bound_cloud_account_id": "1f2c4a3e-...",
  "token": "argos_pat_3f9c1e7a_5N2pKdQ...",
  "created_at": "2026-04-26T09:30:00Z"
}
```

The PAT is bound to this `cloud_account_id` at issuance — it can only access this account's credentials and VMs.

**Fetch credentials (vm-collector):**

```bash
curl -sS -H "Authorization: Bearer argos_pat_3f9c1e7a_..." \
  https://argos.internal:8080/v1/cloud-accounts/by-name/acme-prod/credentials
```

```json
{
  "access_key": "AKIA...",
  "secret_key": "wJalrXUt...",
  "region": "eu-west-2",
  "provider": "outscale"
}
```

Returned over TLS only. Audit-logged on every call (caller, account name, timestamp). Returns `404` if the account does not exist; `404` with `{"error":"cloud_account_not_registered"}` if it exists but is `pending_credentials`; `403` if it is `disabled`. The response body is **never** logged.

**First-contact registration (vm-collector):**

```bash
curl -sS -H "Authorization: Bearer argos_pat_3f9c1e7a_..." \
  -X POST https://argos.internal:8080/v1/cloud-accounts \
  -H 'Content-Type: application/json' \
  -d '{
    "provider": "outscale",
    "name": "acme-prod",
    "region": "eu-west-2"
  }'
```

Idempotent on `(provider, name)`. Returns the row with `status: pending_credentials`.

**Heartbeat (vm-collector):**

```bash
curl -sS -H "Authorization: Bearer argos_pat_3f9c1e7a_..." \
  -X PATCH https://argos.internal:8080/v1/cloud-accounts/<id>/status \
  -H 'Content-Type: application/json' \
  -d '{
    "last_seen_at": "2026-04-26T10:00:00Z",
    "status": "active"
  }'
```

Status may transition between `active` and `error` only; the collector cannot disable, enable, or delete its own row.

### Virtual machines

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| POST | `/v1/virtual-machines` | `vm-collector` | 409 if already a kube node. |
| POST | `/v1/virtual-machines/reconcile` | `vm-collector` | Soft-delete tombstones. |
| GET | `/v1/virtual-machines` | `read` | Paginated, filters. |
| GET | `/v1/virtual-machines/{id}` | `read` | |
| PATCH | `/v1/virtual-machines/{id}` | `write` | Curated metadata only. |
| DELETE | `/v1/virtual-machines/{id}` | `delete` | Soft-delete. |

**List VMs:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `cloud_account_id` | UUID | Scope to a single account. |
| `region` | string | Exact match. |
| `role` | string | Exact match (e.g. `bastion`, `vault`). |
| `power_state` | string | One of `running`, `stopped`, `terminated`, etc. |
| `include_terminated` | bool | Default `false` (tombstones hidden). |

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'https://argos.internal:8080/v1/virtual-machines?cloud_account_id=<uuid>&power_state=running'
```

**Upsert a VM (vm-collector):**

```bash
curl -sS -H "Authorization: Bearer argos_pat_3f9c1e7a_..." \
  -X POST https://argos.internal:8080/v1/virtual-machines \
  -H 'Content-Type: application/json' \
  -d '{
    "cloud_account_id": "<uuid>",
    "provider_vm_id": "i-96fff41b",
    "name": "vault-01",
    "role": "vault",
    "private_ip": "10.0.1.5",
    "instance_type": "tinav7.c4r8p2",
    "zone": "eu-west-2b",
    "region": "eu-west-2",
    "power_state": "running"
  }'
```

If `provider_vm_id` matches a substring of any existing `nodes.provider_id` (i.e. it is already inventoried as a kube node), argosd returns:

```
409 Conflict
{"error":"already_inventoried_as_kubernetes_node","node_id":"<uuid>"}
```

The collector logs and skips. This is the canonical dedup; the collector's tag-based pre-filter is just optimisation.

**Reconcile (vm-collector):**

```bash
curl -sS -H "Authorization: Bearer argos_pat_3f9c1e7a_..." \
  -X POST https://argos.internal:8080/v1/virtual-machines/reconcile \
  -H 'Content-Type: application/json' \
  -d '{
    "cloud_account_id": "<uuid>",
    "keep_provider_vm_ids": ["i-96fff41b", "i-aabbccdd"]
  }'
```

Response:

```json
{"tombstoned": 3}
```

Rows whose `provider_vm_id` is not in the keep list (and which are not already terminated) get `terminated_at = NOW()`, `power_state = 'terminated'`, `ready = false`. **Soft-delete only** — rows are never hard-deleted by reconciliation, preserving SecNumCloud audit history.

**Edit curated metadata:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X PATCH \
  https://argos.internal:8080/v1/virtual-machines/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{
    "display_name": "Vault Primary",
    "owner": "platform-team",
    "criticality": "critical",
    "runbook_url": "https://wiki.example.com/runbooks/vault"
  }'
```

Only curated fields (`display_name`, `owner`, `criticality`, `notes`, `runbook_url`, `annotations`) are accepted. Provider-sourced fields (`provider_vm_id`, `private_ip`, `instance_type`, etc.) cannot be patched — they are owned by the collector.

---

## MCP server (alternative query interface)

Argos also exposes a [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) server with 17 read-only tools that mirror the REST query surface. The MCP interface is designed for AI agents and supports SSE and stdio transports. It is **not** part of the REST API -- see [MCP Server](mcp-server.md) for setup, tool catalogue, and authentication details.
