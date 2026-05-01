---
title: "ADR-0015: VM collector for non-Kubernetes platform infrastructure"
status: "Proposed"
date: "2026-04-26"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "collector", "vm", "cloud-providers", "outscale", "secnumcloud", "asset-management", "binary"]
supersedes: ""
superseded_by: ""
---

# ADR-0015: VM collector for non-Kubernetes platform infrastructure

## Status

**Proposed** | Accepted | Rejected | Superseded | Deprecated

## Context

longue-vue catalogues every node *inside* every Kubernetes cluster (via the kube-API collector — ADR-0001, ADR-0005, ADR-0011) but the platform that runs *underneath* the clusters is invisible to the CMDB today. Production deployments rely on a small set of off-cluster VMs that are essential for the platform to function:

- **VPN gateway** — operator access to private networks
- **DNS server** — internal name resolution
- **Bastion** — SSH ingress into private subnets
- **STUN / TURN** — media relay for any in-platform realtime workloads
- **HashiCorp Vault** — secrets backend the clusters bootstrap from
- Other miscellaneous platform infra (jump hosts, monitoring collectors, build runners…)

These VMs:

- Are not part of any Kubernetes cluster — they sit alongside them in the same cloud account.
- Cannot be discovered through the kube-API; they are only visible through the cloud provider's management API.
- Carry the same security and compliance weight as kube nodes — their compromise breaks the platform.
- Must be inventoried for **ANSSI SecNumCloud chapter 8 (asset management — ADR-0008)** to deliver a complete cartography. A CMDB that lists kube nodes but omits the VPN, DNS and Vault servers is structurally incomplete for SNC qualification.

The infrastructure provider for the current target environment is **Outscale** (a French SecNumCloud-qualified cloud provider). Other providers (AWS, OVHcloud, Scaleway, Azure) are foreseeable but out of scope for v1. The Outscale API exposes:

- Per-VM identity (`VmId` like `i-96fff41b`), instance type, region/zone, IPs (private + public), state (`running` / `stopped` / `terminated` / …), tags, **plus** a wealth of cloud-native metadata not present on K8s nodes: `ImageId` (AMI), `KeypairName`, `BootMode`, `NetId` (VPC), `SubnetId`, `SecurityGroups[]`, `Nics[]`, `BlockDeviceMappings[]`, `CreationDate`.
- **No guest-OS information** (kernel version, OS image) — unless an in-guest agent is deployed (out of scope for v1).
- Authentication via **AK/SK** (Access Key / Secret Key) pairs, region-scoped.

**The deduplication problem.** A naive listing of every VM in the account returns the kube workers too — they are already inventoried in the `nodes` table by the kube collector. We need to recognise and skip them. The Outscale `VmId` is a stable globally unique identifier (`i-96fff41b`). The Outscale CCM (cloud-controller-manager) sets `node.spec.providerID` on every kube node to a string containing that same `VmId` (format `aws:///<az>/<vmid>`). This gives us a canonical way to dedup: any VM whose `VmId` substring appears in some `nodes.provider_id` is already inventoried as a kube node and must not appear again in `virtual_machines`.

**The credentials problem.** AK/SK pairs grant read access to every VM in an Outscale account. They are sensitive long-lived secrets that:

- Must be **encrypted at rest** in the CMDB.
- Must be **rotatable** without redeploying every collector.
- Must produce an **audit trail** on every access.
- Must be **scoped narrowly** so a leaked collector token cannot be used to impersonate a human user or read other entities.

**The deployment-topology problem.** Three constraints ruled out the "embed the collector in longue-vue" approach:

- The collector must be deployable **independently of longue-vue** — on a VM, in a Kubernetes cluster, behind firewalls — wherever it has outbound access to the cloud-provider API and to longue-vue's REST API.
- longue-vue must not be required to reach the cloud-provider API itself. Some SNC environments deploy longue-vue in a management zone with no egress to public Outscale endpoints.
- The pattern of "separate binary that pushes to longue-vue over HTTPS" already exists in the codebase for air-gapped Kubernetes clusters (**ADR-0009 push-collector**). Re-using that pattern keeps the architecture coherent.

This ADR decides the data model, the credential-management story, the deployment topology, the provider abstraction, the dedup logic, the lifecycle policy, and the UI surface for cataloguing non-Kubernetes platform VMs.

## Decision

**Ship a standalone push-mode binary `longue-vue-vm-collector` (mirroring ADR-0009) that polls a cloud provider's API, pushes observations to longue-vue over HTTPS, and fetches its cloud-provider AK/SK from longue-vue's central encrypted credential store via a narrow new token scope. longue-vue grows two new entities — `cloud_accounts` and `virtual_machines` — and a new server-side dedup against `nodes.provider_id` so K8s workers never appear in the VM inventory.**

### 1. Two-binary topology

```
                                    ┌─────────────────────────────────────┐
                                    │             longue-vue                  │
                                    │  (REST API + Postgres + UI)         │
                                    │                                     │
                                    │  cloud_accounts  (AK plain, SK enc) │
                                    │  virtual_machines                   │
                                    │  nodes  (existing)                  │
                                    └────────▲────────────────────────────┘
                                             │ HTTPS + Bearer PAT (vm-collector scope)
                                             │
                       ┌─────────────────────┴──────────────────────┐
                       │                                            │
              ┌────────┴───────────────┐                  ┌─────────┴──────────────┐
              │   longue-vue-vm-collector   │                  │   longue-vue-vm-collector   │
              │   (acme-prod)          │                  │   (acme-dr)            │
              │                        │                  │                        │
              │   1. Fetches creds     │                  │   1. Fetches creds     │
              │      from longue-vue       │                  │      from longue-vue       │
              │   2. Lists VMs from    │                  │   2. Lists VMs from    │
              │      Outscale          │                  │      Outscale          │
              │   3. Pushes to longue-vue  │                  │   3. Pushes to longue-vue  │
              └────────┬───────────────┘                  └──────────┬─────────────┘
                       │                                             │
                       ▼ Outscale API                                ▼ Outscale API
              ┌──────────────────┐                          ┌──────────────────┐
              │  Outscale        │                          │  Outscale        │
              │  acme-prod       │                          │  acme-dr         │
              └──────────────────┘                          └──────────────────┘
```

The collector binary is independent of longue-vue:

- Built from `cmd/longue-vue-vm-collector/` as a static Go binary, distroless base image, UID 65532.
- Stateless — no database, no file storage, no listening port.
- Configured purely via env vars (mirrors `longue-vue-collector` from ADR-0009).
- Deployable on any Kubernetes cluster, any VM, any container runtime — wherever outbound HTTPS works to longue-vue and to the cloud-provider endpoint.
- One binary instance per cloud account. Multi-account support = N deployments.

### 2. Data model — `virtual_machines` table

A new top-level table, **not** an extension of `nodes`. The `nodes` table stays semantically "Kubernetes node"; the `virtual_machines` table is "logical server outside any Kubernetes cluster".

The schema mirrors the enriched `nodes` schema where it makes sense, drops K8s-specific columns, and **adds cloud-native columns** unlocked by the rich Outscale API payload:

```sql
CREATE TABLE virtual_machines (
    id                      UUID PRIMARY KEY,
    cloud_account_id        UUID NOT NULL REFERENCES cloud_accounts(id) ON DELETE CASCADE,

    -- identity
    provider_vm_id          TEXT NOT NULL,        -- e.g. "i-96fff41b"
    name                    TEXT NOT NULL,        -- from Tags[Name], fallback to provider_vm_id
    display_name            TEXT,                 -- curated, editable
    role                    TEXT,                 -- from Tags[ansible_group], nullable

    -- networking
    private_ip              TEXT,
    public_ip               TEXT,
    private_dns_name        TEXT,
    vpc_id                  TEXT,                 -- Outscale NetId
    subnet_id               TEXT,
    nics                    JSONB NOT NULL DEFAULT '[]'::jsonb,   -- full Nics[] from API
    security_groups         JSONB NOT NULL DEFAULT '[]'::jsonb,   -- full SecurityGroups[]

    -- cloud identity
    instance_type           TEXT,                 -- e.g. "tinav7.c4r8p2"
    architecture            TEXT,                 -- e.g. "x86_64"
    zone                    TEXT,                 -- e.g. "eu-west-2b"
    region                  TEXT,                 -- e.g. "eu-west-2" (derived)
    image_id                TEXT,                 -- e.g. "ami-75374985"
    keypair_name            TEXT,                 -- e.g. "prod-std-vm"
    boot_mode               TEXT,                 -- legacy | uefi
    provider_account_id     TEXT,                 -- Nics[0].AccountId
    provider_creation_date  TIMESTAMPTZ,          -- CreationDate from API

    -- power
    power_state             TEXT NOT NULL,        -- canonical: running | stopped | terminated | …
    state_reason            TEXT,
    ready                   BOOLEAN NOT NULL DEFAULT FALSE,  -- derived: power_state = 'running'
    deletion_protection     BOOLEAN NOT NULL DEFAULT FALSE,

    -- guest OS (nullable until/unless an in-guest agent is deployed)
    kernel_version          TEXT,
    operating_system        TEXT,

    -- capacity (parsed from instance_type when the family is recognised, NULL otherwise)
    capacity_cpu            TEXT,
    capacity_memory         TEXT,

    -- storage
    block_devices           JSONB NOT NULL DEFAULT '[]'::jsonb,  -- BlockDeviceMappings[]
    root_device_type        TEXT,                                -- ebs | instance-store
    root_device_name        TEXT,                                -- e.g. "/dev/sda1"

    -- semi-structured
    tags                    JSONB NOT NULL DEFAULT '{}'::jsonb,    -- raw provider Tags as map
    labels                  JSONB NOT NULL DEFAULT '{}'::jsonb,    -- normalised labels
    annotations             JSONB NOT NULL DEFAULT '{}'::jsonb,    -- longue-vue.io/* (EOL etc.)

    -- curated
    owner                   TEXT,
    criticality             TEXT,
    notes                   TEXT,
    runbook_url             TEXT,

    -- lifecycle (soft-delete tombstone)
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL,
    last_seen_at            TIMESTAMPTZ NOT NULL,
    terminated_at           TIMESTAMPTZ,

    UNIQUE (cloud_account_id, provider_vm_id)
);

CREATE INDEX virtual_machines_cloud_account_id_idx ON virtual_machines (cloud_account_id);
CREATE INDEX virtual_machines_terminated_at_idx ON virtual_machines (terminated_at);
CREATE INDEX virtual_machines_role_idx ON virtual_machines (role);
CREATE INDEX virtual_machines_created_at_id_idx ON virtual_machines (created_at DESC, id DESC);
```

**Drops** the K8s-specific `kubelet_version`, `kube_proxy_version`, `container_runtime_version`, `pod_cidr`, `conditions`, `taints`, `unschedulable`, `allocatable_*` (none of these have meaning outside Kubernetes).

**Inferred at collector boundary:**

- `name` ← `Tags[Key=="Name"]` value, fallback to `provider_vm_id`.
- `role` ← `Tags[Key=="ansible_group"]` value, NULL when absent. **Absence of the `ansible_group` tag does NOT filter the VM out** — the row is still ingested with `role = NULL`; operators can fill it in later via the curated-metadata edit. Hardcoded to `ansible_group` in the Outscale provider; future providers can choose their own convention.
- `region` ← parsed from `zone` (`eu-west-2b` → `eu-west-2`).
- `capacity_cpu` / `capacity_memory` ← parsed from `instance_type` for known Outscale families (TINAv*, t*); NULL otherwise.
- `power_state` ← canonical mapping from Outscale states: `pending`, `running`, `stopping`, `stopped`, `terminating` (Outscale `shutting-down`), `terminated`.
- `ready` ← `power_state == 'running'`.

### 3. Data model — `cloud_accounts` table

VMs FK to a `cloud_accounts` table. The **name field is operator-editable** — operators rename `acme-prod-eu-west-2` to `Production OSC EU` from the admin UI without breaking foreign keys.

```sql
CREATE TABLE cloud_accounts (
    id                   UUID PRIMARY KEY,
    provider             TEXT NOT NULL,                 -- "outscale" (only value in v1)
    name                 TEXT NOT NULL,                 -- operator-editable, unique per provider
    region               TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending_credentials',
                                                        -- pending_credentials | active | error | disabled
    -- credentials (NULL until admin sets them)
    access_key           TEXT,                          -- AK plaintext (public identifier)
    secret_key_encrypted BYTEA,                         -- AES-256-GCM(SK) — NULL until set
    secret_key_nonce     BYTEA,                         -- 12-byte GCM nonce
    secret_key_kid       TEXT,                          -- key id, supports rotation
    -- collector heartbeat / last activity
    last_seen_at         TIMESTAMPTZ,                   -- last successful tick (collector-set)
    last_error           TEXT,
    last_error_at        TIMESTAMPTZ,
    -- curated
    owner                TEXT,
    criticality          TEXT,
    notes                TEXT,
    runbook_url          TEXT,
    annotations          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- lifecycle
    created_at           TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL,
    disabled_at          TIMESTAMPTZ,
    UNIQUE (provider, name)
);
```

### 4. Credential storage — encrypted at rest, fetched by the collector

- **AK is plaintext.** It is a public identifier (it appears in request signatures and isn't independently authoritative).
- **SK is encrypted with AES-256-GCM.** Master key from `LONGUE_VUE_SECRETS_MASTER_KEY` env var (32 bytes, base64-encoded). The AAD is the row UUID — a database backup-restore cannot move a SK from one row to another.
- **Master-key handling:** required only when `cloud_accounts` contains at least one row with non-NULL `secret_key_encrypted`. longue-vue refuses to start with such rows present and no master key. A master-key fingerprint (first 8 chars of SHA-256) is logged at startup so operators can confirm the right key is loaded. The master key is never logged, returned, or surfaced in the UI.
- **Key id (`secret_key_kid`)** stored on every row so a future rotation ADR can introduce a multi-key scheme without a schema change.
- **Plaintext SK is never returned by the regular admin UI endpoints.** `GET /v1/admin/cloud-accounts/{id}` returns AK and metadata only. SK is write-only — set on `POST` / `PATCH` / `PATCH /credentials`, never read back through admin endpoints.
- **Plaintext SK is returned by exactly one endpoint, gated on a new narrow scope** (see §5):

  ```
  GET /v1/cloud-accounts/by-name/{name}/credentials       (vm-collector scope)
  GET /v1/cloud-accounts/{id}/credentials                 (vm-collector scope)

  Response: { "access_key": "...", "secret_key": "...", "region": "...", "provider": "outscale" }
  ```

  Returned over TLS only. Audit-logged on every call (caller id, account name, timestamp). Returns 403 when `disabled_at IS NOT NULL` or `status = 'pending_credentials'`.

- **Audit.** Every `POST` / `PATCH` / `DELETE` on `/v1/admin/cloud-accounts/*` and every `GET .../credentials` is captured by the existing `AuditMiddleware` (ADR-0007) with the SK field scrubbed.

### 5. New token scope — `vm-collector`

A new role and scope are added alongside the existing fixed set from ADR-0007:

| Role           | Scopes                                                            |
|----------------|-------------------------------------------------------------------|
| `admin`        | read + write + delete + admin + audit                              |
| `editor`       | read + write                                                       |
| `auditor`      | read + audit                                                       |
| `viewer`       | read                                                               |
| `vm-collector` | **vm-collector** (new — narrow)                                    |

The `vm-collector` scope grants exactly:

- `GET /v1/cloud-accounts/by-name/{name}/credentials` — fetch its own credentials
- `GET /v1/cloud-accounts/{id}/credentials` — same, by id
- `POST /v1/cloud-accounts` — register placeholder row on first contact (auto-create — see §6)
- `PATCH /v1/cloud-accounts/{id}/status` — heartbeat-only update (`last_seen_at`, `last_error`, `last_error_at`); **cannot** modify name, AK, SK, curated metadata, or `disabled_at`
- `POST /v1/virtual-machines` — upsert VM
- `POST /v1/virtual-machines/reconcile` — soft-delete VMs not in the keep list

It cannot:

- Read or modify users, tokens, sessions, settings, audit log.
- Read or modify clusters, nodes, namespaces, pods, workloads, services, ingresses, PVs, PVCs.
- Read or modify other cloud accounts' credentials. (Each PAT is bound to a single account UUID at issuance — see IMP.)
- Modify a cloud account's name, AK, SK, status (except `error`/`active` transitions on heartbeat), or curated metadata.

A leaked `vm-collector` PAT therefore exposes **exactly one cloud account's AK/SK** (read) and **the VMs of that one account** (write). Strictly less than a `write`-scope PAT. Strictly less than a `read` PAT (which can list every entity in the CMDB).

### 6. Hybrid onboarding — collector-first registration, admin fills credentials

Onboarding flow:

1. **Operator deploys `longue-vue-vm-collector`** with:
   ```
   LONGUE_VUE_SERVER_URL=https://longue-vue.internal:8080
   LONGUE_VUE_API_TOKEN=longue_vue_pat_xxxx_yyyy        (vm-collector scope, bound to account name)
   LONGUE_VUE_VM_COLLECTOR_PROVIDER=outscale
   LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME=acme-prod
   LONGUE_VUE_VM_COLLECTOR_REGION=eu-west-2
   ```
2. **Collector boots.** Calls `GET /v1/cloud-accounts/by-name/acme-prod/credentials`.
3. **First contact** — longue-vue has no row with that name:
   - longue-vue's handler **does not auto-create** on `GET /credentials` (a credential read should never side-effect).
   - Returns `404 Not Found` with body `{"error":"cloud_account_not_registered"}`.
   - Collector receives the 404 and fires `POST /v1/cloud-accounts` with `{provider, name, region}`. The handler creates a placeholder row in `status = 'pending_credentials'`, AK and SK NULL.
   - Collector logs `cloud account "acme-prod" registered, awaiting admin to provide credentials` and waits.
4. **Admin sees the placeholder.** The admin UI surfaces the row prominently: 🔴 `acme-prod (pending credentials)`. Admin clicks → form → enters AK + SK → POST `/v1/admin/cloud-accounts/{id}/credentials` (admin scope). longue-vue encrypts the SK, stores it, transitions row to `status = 'active'`.
5. **Collector retries.** Next interval, `GET .../credentials` returns the pair. Collector starts polling Outscale.
6. **Steady state.** Collector posts VMs every interval, reconciles, updates `last_seen_at` via `PATCH /status`. Admin can rotate the SK at any time via `PATCH /credentials`; the collector picks up the new SK on the next periodic refresh (every hour by default; configurable via `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`).

The status field has four values:

| status                | Meaning                                                  |
|-----------------------|----------------------------------------------------------|
| `pending_credentials` | Collector registered the row; admin has not set AK/SK    |
| `active`              | Credentials present; collector ticking successfully       |
| `error`               | Collector reported an error on its last tick              |
| `disabled`            | Admin disabled the account (collector gets 403 on credentials fetch) |

### 7. Provider abstraction — pluggable from day one

A new package `internal/vmcollector/provider/` defines the seam:

```go
package provider

type VM struct {
    ProviderVMID         string
    Name                 string                  // from Tags["Name"], fallback ProviderVMID
    Role                 string                  // from Tags["ansible_group"] for Outscale
    Tags                 map[string]string
    PrivateIP            string
    PublicIP             string
    PrivateDNSName       string
    InstanceType         string
    Architecture         string
    Zone                 string
    Region               string
    ImageID              string
    KeypairName          string
    BootMode             string
    VPCID                string
    SubnetID             string
    ProviderAccountID    string
    ProviderCreationDate time.Time
    PowerState           string                  // canonical
    StateReason          string
    DeletionProtection   bool
    KernelVersion        string                  // empty without agent
    OperatingSystem      string                  // empty without agent
    CapacityCPU          string
    CapacityMemory       string
    NICs                 json.RawMessage         // forwarded as opaque JSON
    SecurityGroups       json.RawMessage         // same
    BlockDevices         json.RawMessage         // same
    RootDeviceType       string
    RootDeviceName       string
}

type Provider interface {
    Kind() string                                   // "outscale"
    ListVMs(ctx context.Context) ([]VM, error)
}
```

One file per provider. **Only `outscale.go`** in v1 (using `github.com/outscale/osc-sdk-go/v2`). AWS / OVHcloud / Scaleway / Azure are listed as future work (`FUT-*`).

### 8. Filter logic and dedup

A VM ends up in `virtual_machines` **unless** one of three conditions is true. **No other tag matters for inclusion** — in particular, the `ansible_group` tag is used only to populate `role`, never to filter the VM out.

| Condition                                          | Where it's checked        | Outcome                                |
|----------------------------------------------------|---------------------------|----------------------------------------|
| Has `OscK8sClusterID/*` or `OscK8sNodeName=*` tag  | Collector pre-filter      | Dropped before any HTTP POST           |
| Has `longue-vue.io/ignore=true` tag                     | Collector pre-filter      | Dropped before any HTTP POST           |
| `provider_vm_id` matches an existing `nodes.provider_id` | longue-vue server-side  | `409 Conflict`; collector logs and skips |

The collector applies the **cheap pre-filter** at its boundary:

- Drop any VM bearing tags matching `OscK8sClusterID/*` or `OscK8sNodeName=*` (Outscale CCM ownership tags). Saves an HTTP round-trip per K8s worker.
- Drop any VM bearing `longue-vue.io/ignore=true` (operator-set escape hatch — longue-vue only reads it, never writes it).

longue-vue applies the **canonical dedup** server-side on `POST /v1/virtual-machines`:

- Look up `nodes` rows where `provider_id` contains the posted `provider_vm_id` (e.g. `provider_id LIKE '%i-96fff41b%'`).
- If found → return `409 Conflict` body `{"error":"already_inventoried_as_kubernetes_node","node_id":"<uuid>"}`. The collector logs `vm i-96fff41b is a kube node, skipping`.
- Otherwise → upsert into `virtual_machines`.

The pre-filter is performance optimisation; the server-side check is the single source of truth. A race (VM joined K8s but kube collector hasn't ticked yet) resolves itself on the next VM-collector tick — the row exists in `virtual_machines` for one tick window, then is naturally pushed out by the kube collector reconciliation pass.

### 9. Reconciliation — soft-delete via API

The kube push collector reconciles by calling `POST /v1/<resource>/reconcile` (ADR-0009). The VM collector follows the same pattern:

```
POST /v1/virtual-machines/reconcile           (vm-collector scope)
{
  "cloud_account_id": "<uuid>",
  "keep_provider_vm_ids": ["i-96fff41b", "i-aabbccdd", ...]
}

Response: { "tombstoned": 3 }
```

longue-vue updates rows where `cloud_account_id = $1 AND provider_vm_id NOT IN ($2…) AND terminated_at IS NULL`:

- `terminated_at = NOW()`
- `power_state = 'terminated'`
- `ready = false`

**Soft-delete only.** Rows are never hard-deleted by reconciliation — preserves SecNumCloud audit history. A retention policy (purge `terminated_at < NOW() - 1 year`) is left as a follow-up.

A VM that re-appears with the same `(cloud_account_id, provider_vm_id)` after being tombstoned is treated as a re-creation: `terminated_at` is cleared, `power_state` is updated. (Outscale doesn't reuse VM IDs in practice; defensive only.)

### 10. UI surface

Two surfaces:

- **Top-level "Virtual Machines" sidebar entry** alongside Nodes / Pods / Services. List page (filters: cloud account, region, role, power state, include-terminated) + detail page mirroring the Node detail layout (Identity card, Networking card with NICs/SGs, Cloud identity card with image+keypair+VPC, Storage card with block devices, OS stack card, Capacity card, Tags/Labels/Annotations card, Curated-metadata card with inline edit). The Node detail components are extracted into `ui/src/components/inventory/` so Node and VM detail pages share `IdentityCard`, `NetworkingCard`, `CapacityCard`, `LabelsCard`, `AnnotationsCard`, `CuratedMetadataCard`.

- **Admin → "Cloud Accounts" page** — admin-only:
  - List of accounts with status badges (🟢 active / 🟡 error / 🔴 pending_credentials / ⚪ disabled).
  - "Set credentials" / "Rotate credentials" form for any row (form fields: AK, SK; SK is `<input type="password">` and never displayed back).
  - "Disable" toggle.
  - Curated-metadata edit (name, owner, criticality, notes, runbook_url).
  - Audit-logged.
  - Read-only for non-admin roles.

A new SVG icon (server/tower glyph) is added for the Virtual Machines nav entry, distinct from the Node icon.

### 11. Configuration

**On longue-vue:**

| Env var                       | Purpose                                                                  | Default                |
|-------------------------------|--------------------------------------------------------------------------|------------------------|
| `LONGUE_VUE_SECRETS_MASTER_KEY`    | Base64-encoded 32-byte AES-256 master key for SK envelope encryption     | (required if accounts) |

**On `longue-vue-vm-collector`:**

| Env var                                  | Purpose                                              | Default          |
|------------------------------------------|------------------------------------------------------|------------------|
| `LONGUE_VUE_SERVER_URL`                       | longue-vue base URL (gateway-aware path prefix supported)| (required)       |
| `LONGUE_VUE_API_TOKEN`                        | Bearer PAT with `vm-collector` scope                 | (required)       |
| `LONGUE_VUE_VM_COLLECTOR_PROVIDER`            | Cloud provider (only `outscale` in v1)               | `outscale`       |
| `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME`        | Cloud account name (matches cloud_accounts.name)     | (required)       |
| `LONGUE_VUE_VM_COLLECTOR_REGION`              | Cloud region                                         | (required)       |
| `LONGUE_VUE_VM_COLLECTOR_INTERVAL`            | Tick interval                                        | `5m`             |
| `LONGUE_VUE_VM_COLLECTOR_FETCH_TIMEOUT`       | Per-tick context timeout                             | `30s`            |
| `LONGUE_VUE_VM_COLLECTOR_RECONCILE`           | Whether to call `/reconcile` after each tick         | `true`           |
| `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`  | How often to refetch creds (rotation pickup)         | `1h`             |
| `LONGUE_VUE_CA_CERT`                          | Custom CA for longue-vue TLS                             | (system)         |
| `LONGUE_VUE_CLIENT_CERT`                      | Client cert for mTLS to longue-vue / gateway             | (none)           |
| `LONGUE_VUE_CLIENT_KEY`                       | Client key for mTLS                                  | (none)           |
| `LONGUE_VUE_EXTRA_HEADERS`                    | Extra HTTP headers (`k=v,k=v`) for gateway routing   | (none)           |
| `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY`| Standard Go-honoured proxy vars                       | (env)            |

**There is no AK/SK env var on the collector.** Credentials live exclusively in longue-vue's `cloud_accounts` table.

### 12. Deployment

A new directory `deploy/vm-collector/` ships reference Kustomize manifests:

- `Deployment` (replicas: 1) running `longue-vue-vm-collector`
- `Secret` carrying `LONGUE_VUE_API_TOKEN` (the PAT)
- Egress NetworkPolicy allowing HTTPS to the longue-vue endpoint and to the cloud-provider API endpoint only
- A `ConfigMap` for non-secret env vars

A new `Dockerfile.vm-collector` (or build stage) produces the image. CI builds and pushes both `longue-vue:<version>` and `longue-vue-vm-collector:<version>`.

## Consequences

### Positive

- **POS-001** Closes the "what runs underneath the cluster" gap that has been outstanding since ADR-0001. The CMDB now covers the full SecNumCloud chapter 8 asset surface (clusters, kube nodes *and* the supporting platform infrastructure).
- **POS-002** The `virtual_machines` schema captures **substantially more** than the Node equivalent — image AMI, keypair, VPC/subnet, NICs, SGs, block devices, deletion protection, provider creation date. SNC cartography reviewers get a complete logical-server picture.
- **POS-003** Encrypted-at-rest credentials with explicit master-key handling, AAD-bound ciphertexts, audit-logged CRUD and credential reads, and the GitHub-PAT pattern (write-only from admin endpoints) raise the security bar materially.
- **POS-004** **The new `vm-collector` scope is the narrowest scope in the system.** A leaked collector PAT exposes exactly one cloud account's read access — strictly less than a `read`-scope user PAT (which can list everything).
- **POS-005** Two-binary topology mirrors ADR-0009 — the architecture is coherent with the existing airgap-cluster solution. Operators who deployed `longue-vue-collector` already understand the deployment shape of `longue-vue-vm-collector`.
- **POS-006** Provider abstraction in place from day one. Adding AWS / OVHcloud / Scaleway is a one-file PR (a new `internal/vmcollector/provider/<provider>.go`).
- **POS-007** Multi-account is free with the per-deployment shape — one collector deployment per account, isolated blast radius, independent error budgets.
- **POS-008** Server-side VM-ID dedup against `nodes.provider_id` is deterministic and tag-independent. Works regardless of which cloud-controller-manager is in use.
- **POS-009** Soft-delete preserves audit history of decommissioned VMs — answers "what was running on date X?" without external archival.
- **POS-010** Operator-editable account name decouples display from credential identity. Renaming a `cloud_accounts` row doesn't break any FKs.
- **POS-011** Hybrid onboarding lets operators deploy collectors *before* admin curation. A new collector showing up as `pending_credentials` is a useful UI signal — visible reminder that the SNC asset list is incomplete.
- **POS-012** Hot rotation of AK/SK works without redeploying the collector. Admin pastes new SK in the UI; collector picks it up on the next credential-refresh interval.

### Negative

- **NEG-001** Master-key handling is a new failure mode. Loss of `LONGUE_VUE_SECRETS_MASTER_KEY` means every stored SK is unrecoverable — operators must re-enter every account's SK. Mitigation: documented backup procedure for the master key (separate from the database backup), startup banner showing the master-key fingerprint, planned future ADR (`FUT-001`) for KMS integration so the master key never lives in an env var on prod.
- **NEG-002** No agent-based guest-OS information in v1. `kernel_version` and `operating_system` are empty for every VM until a separate agent track lands. The EOL enricher is therefore a no-op for VMs in v1.
- **NEG-003** A second binary to build, release, and version. Mitigated by sharing `internal/vmcollector` — the polling logic is compiled once, consumed by both the static-build artefact and the integration tests in `longue-vue`.
- **NEG-004** The `virtual_machines` table is unbounded by tombstone retention. After several years of churn, the table accumulates terminated rows. A retention policy is a follow-up (`FUT-006`).
- **NEG-005** A leaked `vm-collector` PAT exposes one cloud account's plaintext SK. Mitigation: the scope is bound to a single account (the admin issues the token tied to a specific `cloud_account_id`); rotation is one click in the UI; every credential read is audit-logged.
- **NEG-006** Two collectors with overlapping concerns (kube push collector writes `nodes`, VM push collector writes `virtual_machines`) — operators must understand the split. Mitigated by clear UI labelling ("Kubernetes Nodes" / "Virtual Machines") and the deterministic server-side dedup in §8.
- **NEG-007** Credential fetch over HTTPS adds a round-trip on collector startup and every refresh interval. Acceptable: O(1) per hour per collector, far below any meaningful overhead.
- **NEG-008** The hybrid-onboarding flow has a "pending credentials" state that requires admin attention to clear. If admins ignore the UI banner, a collector can sit idle indefinitely. Mitigation: surface the state prominently on the admin home page, expose a `longue_vue_cloud_accounts_pending_credentials` Prometheus gauge for alerting.

## Alternatives Considered

### Reuse the `nodes` table with a discriminator

- **ALT-001** **Description**: Add `kind = 'kubernetes' | 'standalone_vm'` and make `cluster_id` nullable. Single inventory of "logical servers".
- **ALT-002** **Rejection Reason**: Pollutes the `nodes` semantics with rows that aren't Kubernetes nodes. Drops K8s-specific columns to NULL-by-discriminator. The UI would have to branch on `kind` everywhere. A new table is cheaper and the rich cloud-native columns (NICs, SGs, block_devices, image_id, keypair_name) have no Node equivalent.

### Embed the VM collector in longue-vue as a goroutine

- **ALT-003** **Description**: longue-vue reads `cloud_accounts`, decrypts SKs in-process, runs one `Collector` goroutine per row.
- **ALT-004** **Rejection Reason**: longue-vue would need outbound HTTPS to every cloud-provider endpoint. Some SNC environments deploy longue-vue in a management zone with no public egress. Operators wanted the airgap-collector pattern (separate binary, push over HTTPS) for symmetry. The two-binary shape mirrors ADR-0009 exactly.

### Env-var-managed credentials on the collector

- **ALT-005** **Description**: AK/SK delivered to the collector via env vars / a Kubernetes Secret. No DB storage of secrets at all.
- **ALT-006** **Rejection Reason**: Distributes the secret across N collector deployments. Rotation requires updating N Secrets. No central audit. SecNumCloud chapter 9 (cryptographic asset protection) prefers centralised secret management with audit. The fetch-API design centralises rotation and audit while keeping the collector binary stateless.

### longue-vue-as-proxy (collector never sees AK/SK)

- **ALT-007** **Description**: longue-vue holds AK/SK and itself calls Outscale; the collector exists only as a thin proxy that posts results.
- **ALT-008** **Rejection Reason**: Collapses to the central-pull model with extra steps. The collector adds no value if longue-vue is doing the cloud API call. The whole point of the separate binary is that longue-vue does **not** need cloud-provider reachability.

### Hard-delete VMs that disappear

- **ALT-009** **Description**: Match the kube collector's hard-delete reconciliation.
- **ALT-010** **Rejection Reason**: Loses the audit trace ("what was running on date X?"). Soft-delete is one extra column for a meaningful compliance benefit.

### Outscale-only package, no provider seam

- **ALT-011** **Description**: Put Outscale in `internal/outscale/` directly; refactor when the next provider arrives.
- **ALT-012** **Rejection Reason**: The seam (a `Provider` interface + a `VM` struct) costs ~40 lines and turns the second provider from a refactor into a one-file contribution.

### Tag-driven opt-in (`longue-vue.io/platform=true`)

- **ALT-013** **Description**: Operator must tag every platform VM with `longue-vue.io/platform=true`; collector ingests only tagged VMs.
- **ALT-014** **Rejection Reason**: Operators don't tag their existing VMs with longue-vue-specific tags and can't reasonably be asked to retag a fleet. The VM-ID dedup against `nodes.provider_id` is sufficient: every VM that isn't a kube node IS a platform VM by definition. The `longue-vue.io/ignore=true` opt-out remains as an escape hatch (read by longue-vue, set by operators if they want to exclude something).

### Collector-side `LONGUE_VUE_VM_COLLECTOR_ROLE_TAG` env var

- **ALT-015** **Description**: Make the role-derivation tag (default `ansible_group`) configurable per collector deployment.
- **ALT-016** **Rejection Reason**: YAGNI. There is one operator, one convention. If a future provider needs a different tag, the right place for that knob is a column on `cloud_accounts` (per-account, not per-deployment), not a collector env var. The Outscale provider hardcodes `ansible_group`.

### Auto-create cloud_account on first POST without admin curation

- **ALT-017** **Description**: Collector POSTs `{provider, name, region, access_key, secret_key}` on first contact, creating the row with credentials in one shot.
- **ALT-018** **Rejection Reason**: Re-introduces AK/SK in the collector's environment, which we explicitly removed (ALT-006). The hybrid flow (collector creates placeholder, admin fills creds) gives the same operational shape ("deploy collector first") while keeping creds centralised.

### Curated `depends_on` edges in v1 (impact-graph integration)

- **ALT-019** **Description**: Extend the impact-graph traverser to follow operator-curated `depends_on` edges from clusters/services to VMs.
- **ALT-020** **Rejection Reason**: Premature. Inventory is the prerequisite; relationships are the next layer. Designing the curated-edge model properly is its own ADR (`FUT-003`).

### Normalised child tables for NICs / SGs / block_devices

- **ALT-021** **Description**: Instead of JSONB columns, create `vm_nics`, `vm_security_groups`, `vm_block_devices` tables with FKs to `virtual_machines`.
- **ALT-022** **Rejection Reason**: JSONB suffices for inventory display. Cross-VM queries (e.g. "which VMs share security group X?") are not v1 requirements. If they become needed, normalising later is a backward-compatible migration (extract JSONB to child tables, drop the JSONB column).

## Implementation Notes

- **IMP-001** New migrations:
  - `00023_create_cloud_accounts.sql` — `cloud_accounts` table with status / encryption columns / curated metadata.
  - `00024_create_virtual_machines.sql` — `virtual_machines` table with full schema from §2.
  - No `settings` toggle migration: the VM collector's "enabled" state is purely "is the binary deployed?". There is no goroutine inside longue-vue to enable/disable.
- **IMP-002** New package `internal/secrets/` with an `Encrypter` interface and a single AES-256-GCM impl. Master key parsed from `LONGUE_VUE_SECRETS_MASTER_KEY` (base64 → 32 bytes; reject other lengths at startup). Encrypter is constructed once in `main.go` and passed into the cloud-accounts handlers and the credential-read handler. Covered by unit tests with NIST known-answer vectors and round-trip property tests including AAD mismatch.
- **IMP-003** New package `internal/vmcollector/` shared between the collector binary and longue-vue's tests:
  - `provider/provider.go` — `Provider` interface, `VM` struct.
  - `provider/outscale.go` — Outscale impl using `github.com/outscale/osc-sdk-go/v2`. Maps `osc.Vm` → `provider.VM`. Canonical state mapping for `vm.State`. Instance-type → CPU/memory parser for known TINA families. Tags flattened from `[]ResourceTag` to `map[string]string`. Hardcoded `ansible_group` as the role-tag key.
  - `provider/fake.go` — test fake.
  - `apiclient/store.go` — HTTP-backed store implementing the narrow collector interface: `FetchCredentials`, `RegisterCloudAccount`, `UpdateCloudAccountStatus`, `UpsertVirtualMachine`, `ReconcileVirtualMachines`. Retry with exponential backoff on 5xx (3 attempts max). Stop on 401/403 (token revoked or misconfigured). Inherits gateway/proxy/mTLS support from the same `http.Transport` shape as `longue-vue-collector`.
  - `filter/filter.go` — pre-filter (drops `OscK8sClusterID/*`, `OscK8sNodeName=*`, `longue-vue.io/ignore=true`).
  - `collector.go` — the polling loop: fetch creds (with cache + periodic refresh), instantiate provider, list VMs, pre-filter, upsert each VM, reconcile after success, update status. Handles 409 from `POST /v1/virtual-machines` (already-a-kube-node) by logging and continuing.
- **IMP-004** New binary `cmd/longue-vue-vm-collector/main.go`. Env parsing, signal handling (SIGINT/SIGTERM → context cancel → graceful drain), one collector loop. Build with `CGO_ENABLED=0`. Same distroless base, UID 65532.
- **IMP-005** longue-vue-side store interface additions:
  - `UpsertCloudAccount(ctx, provider, name, region) (CloudAccount, error)` — used by both the admin UI flow and the collector's first-contact flow. Idempotent on `(provider, name)`. Creates with `status='pending_credentials'`.
  - `GetCloudAccountByName(ctx, provider, name)` and `GetCloudAccount(ctx, id)`.
  - `ListCloudAccounts(ctx, filter)`.
  - `UpdateCloudAccount(ctx, id, patch)` — merge-patch for curated metadata + name. Admin only.
  - `SetCloudAccountCredentials(ctx, id, ak, encryptedSK, nonce, kid)` — admin-only path. Transitions status to `active`.
  - `GetCloudAccountCredentials(ctx, id)` — returns AK + ciphertext+nonce+kid; caller (handler) decrypts. Audit-logged. Returns `ErrNotFound` if status != `active`.
  - `UpdateCloudAccountStatus(ctx, id, status, lastSeenAt, lastError)` — collector heartbeat path. Cannot transition to/from `disabled` or `pending_credentials`.
  - `DisableCloudAccount(ctx, id)` / `EnableCloudAccount(ctx, id)` — admin only.
  - `DeleteCloudAccount(ctx, id)` — admin only. Cascades to VMs.
  - `UpsertVirtualMachine(ctx, vm)` — server-side dedup against `nodes.provider_id`; returns `ErrConflict` (mapped to 409) if the VM is already a kube node.
  - `ListVirtualMachines(ctx, filter)`.
  - `GetVirtualMachine(ctx, id)`.
  - `UpdateVirtualMachine(ctx, id, patch)` — merge-patch for curated-metadata fields only.
  - `ReconcileVirtualMachines(ctx, accountID, keepProviderVMIDs []string)` — soft-delete the rest.
- **IMP-006** API endpoints (added to `api/openapi/openapi.yaml` and handlers):
  - `POST /v1/admin/cloud-accounts` — admin scope. Body `{provider, name, region, access_key?, secret_key?, owner?, criticality?, notes?, runbook_url?}`. If creds present, encrypts and stores in one shot; if absent, creates row in `pending_credentials`.
  - `GET /v1/admin/cloud-accounts` — admin scope.
  - `GET /v1/admin/cloud-accounts/{id}` — admin scope.
  - `PATCH /v1/admin/cloud-accounts/{id}` — admin scope. Curated-metadata + name (no AK/SK).
  - `PATCH /v1/admin/cloud-accounts/{id}/credentials` — admin scope. Body `{access_key, secret_key}`. Encrypts SK, stores, transitions to `active`.
  - `POST /v1/admin/cloud-accounts/{id}/disable` and `/enable` — admin scope.
  - `DELETE /v1/admin/cloud-accounts/{id}` — admin scope. Cascades.
  - `GET /v1/cloud-accounts/by-name/{name}/credentials` and `GET /v1/cloud-accounts/{id}/credentials` — `vm-collector` scope. Returns plaintext AK/SK over TLS. 403 on `disabled`, 404 on `pending_credentials`. Audit-logged with the SK field automatically scrubbed from the request body (the response body is intentionally NOT logged — see audit-handler change in IMP-007).
  - `POST /v1/cloud-accounts` — `vm-collector` scope. Body `{provider, name, region}`. Idempotent first-contact registration. Creates placeholder row in `pending_credentials`. Returns the row.
  - `PATCH /v1/cloud-accounts/{id}/status` — `vm-collector` scope. Body `{last_seen_at?, last_error?, last_error_at?, status?}`. Status may transition between `active` and `error` only.
  - `POST /v1/virtual-machines` — `vm-collector` scope. Server-side `nodes.provider_id` dedup; 409 if already a kube node.
  - `GET /v1/virtual-machines`, `GET /v1/virtual-machines/{id}` — `read` scope.
  - `PATCH /v1/virtual-machines/{id}` — `write` scope. Curated-metadata only.
  - `DELETE /v1/virtual-machines/{id}` — `delete` scope. Soft-delete (sets `terminated_at`).
  - `POST /v1/virtual-machines/reconcile` — `vm-collector` scope.
- **IMP-007** Audit handling. Extend the existing audit scrubber list to include `secret_key`, `access_key`. Special-case the credentials-read endpoints: log the call (caller, account, timestamp) but **never** log the response body, even at debug level. Add an explicit unit test that round-trips a credentials-read and asserts no SK string appears in the audit row.
- **IMP-008** New role and scope plumbing in `internal/auth/`:
  - Add `RoleVMCollector` and `ScopeVMCollector` constants.
  - Update `ScopesForRole`.
  - Token issuance UI: admin form to create a `vm-collector` PAT requires selecting the bound `cloud_account_id`. Backend stores the binding on the token row (new column `bound_cloud_account_id` on the `tokens` table; nullable for non-vm-collector tokens). Middleware enforces the binding: a vm-collector PAT can only access its own account's endpoints.
  - Migration `00025_add_token_bound_cloud_account.sql` adds the column.
- **IMP-009** Prometheus metrics:
  - longue-vue: `longue_vue_cloud_accounts_total{status}`, `longue_vue_cloud_accounts_pending_credentials` (gauge), `longue_vue_virtual_machines_total{cloud_account, terminated}`, `longue_vue_credentials_reads_total{cloud_account}`.
  - collector binary: `longue_vue_vm_collector_ticks_total{status}`, `longue_vue_vm_collector_tick_duration_seconds`, `longue_vue_vm_collector_vms_observed`, `longue_vue_vm_collector_vms_skipped_kubernetes`, `longue_vue_vm_collector_credential_refreshes_total{result}`, `longue_vue_vm_collector_last_success_timestamp_seconds`. Exposed on a localhost-only `/metrics` listener (port from `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR`, default `127.0.0.1:9090`).
- **IMP-010** UI work (`ui/src/`):
  - New routes: `/ui/virtual-machines`, `/ui/virtual-machines/:id`, `/ui/admin/cloud-accounts`, `/ui/admin/cloud-accounts/new`, `/ui/admin/cloud-accounts/:id`.
  - New nav entry "Virtual Machines" under the existing inventory section, with a new SVG icon (server/tower glyph distinct from the Node icon). Update `ui/src/icons.tsx`.
  - Reusable cards extracted into `ui/src/components/inventory/` for shared use between Node and VM detail pages.
  - Cloud-accounts admin page:
    - List view with status badges and a "pending credentials" alert pill.
    - "Set credentials" / "Rotate credentials" form: AK + SK fields; SK is `<input type="password">`, never displayed back.
    - "Disable" / "Enable" buttons.
    - "Issue collector token" button (admin-only): opens token-creation modal pre-bound to this `cloud_account_id` and pre-set to the `vm-collector` role.
  - Home-page admin banner: surface count of `pending_credentials` accounts.
- **IMP-011** Build and CI:
  - New `Dockerfile.vm-collector` (or new build stage in the existing Dockerfile) producing `longue-vue-vm-collector`.
  - CI matrix: build both `longue-vue:<version>` and `longue-vue-vm-collector:<version>`. Run integration tests for each.
  - `make build-vm-collector` and `make docker-build-vm-collector` Makefile targets.
- **IMP-012** Deployment:
  - New `deploy/vm-collector/` Kustomize directory: `Deployment`, `Secret` (PAT), `ConfigMap` (env vars), `NetworkPolicy` (egress to longue-vue + Outscale endpoint only).
  - `deploy/vm-collector/README.md` walks operators through token issuance, collector deployment, admin credential entry — the full hybrid-onboarding flow.
- **IMP-013** Tests:
  - Unit: `internal/secrets/` round-trip + AAD-mismatch negative test + master-key fingerprint test.
  - Unit: `internal/vmcollector/filter/` pre-filter logic with table-driven tests covering all tag combinations.
  - Unit: `internal/vmcollector/provider/outscale_test.go` — state mapping, instance-type capacity parser, tag flattening.
  - Unit: token-binding middleware (vm-collector PAT bound to account A cannot access account B).
  - Integration: store CRUD on `cloud_accounts` and `virtual_machines`, soft-delete reconcile, server-side dedup against `nodes.provider_id`.
  - Integration: full hybrid-onboarding flow — collector POSTs to register, admin sets creds, collector fetches and runs.
  - End-to-end: `longue-vue-vm-collector` running against a `provider/fake.go` fake provider, against a real longue-vue + test PG.
- **IMP-014** Documentation deliverables (Phase 5 of the workflow):
  - New `docs/cloud-accounts.md` — admin guide: how to register an account, what cloud-provider permissions the AK needs, hybrid-onboarding flow, rotation, master-key backup.
  - New `docs/vm-collector.md` — operator guide: deploying `longue-vue-vm-collector`, env-var reference, gateway/proxy/mTLS configuration, troubleshooting.
  - `docs/configuration.md` — add `LONGUE_VUE_SECRETS_MASTER_KEY` and the collector-binary env vars.
  - `docs/api-reference.md` — new endpoints with scope annotations.
  - `CLAUDE.md` — new architecture notes for `internal/secrets/`, `internal/vmcollector/`, `cmd/longue-vue-vm-collector/`, the `cloud_accounts` and `virtual_machines` tables, the dedup logic, the `vm-collector` scope, the hybrid-onboarding flow.
  - `README.md` — feature list update, ADR index entry, docs table entry, two-binary topology diagram.
  - `CHANGELOG.md` — `Added` entries under the next minor version.
- **IMP-015** Helm chart:
  - Bump `version` and `appVersion`.
  - Add a `secrets.masterKey` value (delivered via a Kubernetes Secret, never in `values.yaml`). Document master-key backup separately from the database.
  - Add an optional sub-chart or values block for deploying `longue-vue-vm-collector` alongside longue-vue in the same release.

## Future work

- **FUT-001** KMS-backed master key (AWS KMS, HashiCorp Vault Transit, GCP Cloud KMS) so the symmetric master key never lives in an env var on production.
- **FUT-002** In-guest agent (or SSH-agentless probe) to populate `kernel_version`, `operating_system`, `architecture` — unblocks EOL enrichment for VMs.
- **FUT-003** Curated `depends_on` edges so VMs participate in the impact graph.
- **FUT-004** Additional cloud providers — AWS EC2, OVHcloud Public Cloud, Scaleway Instances, Azure VMs.
- **FUT-005** Per-account `role_tag_key` column on `cloud_accounts` for operators with non-`ansible_group` conventions.
- **FUT-006** Retention policy for tombstoned `virtual_machines` rows.
- **FUT-007** Bulk push endpoint (`POST /v1/collect`) for collector tick payloads — mirrors ADR-0009's deferred bulk-push optimisation.
- **FUT-008** Normalised child tables for NICs / security_groups / block_devices when cross-VM queries become a real need.

## References

- **REF-001** ADR-0001 — CMDB for SNC using Kubernetes — `docs/adr/adr-0001-cmdb-for-snc-using-kube.md`
- **REF-002** ADR-0005 — Multi-cluster collector topology — `docs/adr/adr-0005-multi-cluster-collector.md`
- **REF-003** ADR-0007 — Auth and RBAC (PAT pattern, audit middleware, role-scope mapping) — `docs/adr/adr-0007-auth-and-rbac.md`
- **REF-004** ADR-0008 — SecNumCloud chapter 8 asset management — `docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md`
- **REF-005** ADR-0009 — Push-based collector for air-gapped clusters (binary topology, gateway support) — `docs/adr/adr-0009-push-collector-for-airgapped-clusters.md`
- **REF-006** ADR-0011 — Collector auto-creates cluster — `docs/adr/adr-0011-collector-auto-creates-cluster.md`
- **REF-007** ADR-0012 — EOL enrichment via endoflife.date — `docs/adr/adr-0012-eol-enrichment-via-endoflife-date.md`
- **REF-008** ADR-0013 — Impact analysis graph — `docs/adr/adr-0013-impact-analysis-graph.md`
- **REF-009** Outscale Go SDK — https://github.com/outscale/osc-sdk-go
- **REF-010** Outscale Cloud Controller Manager (source of `OscK8sClusterID/*` and `OscK8sNodeName` tags) — https://github.com/outscale/cloud-provider-osc
- **REF-011** AES-256-GCM with AAD (NIST SP 800-38D) — https://csrc.nist.gov/publications/detail/sp/800-38d/final
- **REF-012** ANSSI SecNumCloud reference — https://cyber.gouv.fr/secnumcloud-pour-les-fournisseurs-de-services-cloud
