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
| POST | `/v1/clusters` | `write` | Register a cluster. |
| GET | `/v1/clusters/{id}` | `read` | Get a cluster by ID. |
| PATCH | `/v1/clusters/{id}` | `write` | Update mutable fields (merge-patch). |
| DELETE | `/v1/clusters/{id}` | `delete` | Delete a cluster and all its children. |

**Create a cluster:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X POST http://localhost:8080/v1/clusters \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "prod",
    "display_name": "Production",
    "environment": "production"
  }'
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
| POST | `/v1/auth/login` | no | Username/password login. Sets session cookie. |
| POST | `/v1/auth/logout` | yes | End the current session. |
| GET | `/v1/auth/me` | yes | Caller identity, role, scopes, `must_change_password`. |
| POST | `/v1/auth/change-password` | session only | Change the current user's password. |
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

---

## Admin

All admin endpoints require the `admin` scope.

### Users

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/admin/users` | List users (paginated). |
| POST | `/v1/admin/users` | Create a user. |
| GET | `/v1/admin/users/{id}` | Get a user. |
| PATCH | `/v1/admin/users/{id}` | Update role, password, or disabled state. |
| DELETE | `/v1/admin/users/{id}` | Delete a user. |

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

## Health

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/healthz` | no | Liveness probe. Returns `{"status":"ok"}`. |
| GET | `/readyz` | no | Readiness probe. Verifies database connectivity. |

---

For the full schema (request/response bodies, field types, constraints), see the OpenAPI specification at `api/openapi/openapi.yaml`.
