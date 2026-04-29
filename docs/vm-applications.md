# VM Applications

Platform VMs catalogued by the vm-collector arrive in Argos carrying cloud-provider metadata: AMI, instance type, IPs, VPC, security groups. That tells you the *machine*, but not what software runs inside it. Operators who need to answer "what version of Vault runs on this bastion?" today have to SSH in or check an Ansible inventory. SecNumCloud chapter 8 (asset management) requires a complete software inventory, not just hardware.

The `applications` field on a non-Kubernetes VM lets operators record exactly that: the platform software running on the guest (Vault, Cyberwatch, DNS, Nginx, OpenSSH, …), together with its version. Once declared, the EOL enricher automatically annotates the VM with lifecycle status for each application — the same `argos.io/eol.<product>` annotation it already writes for Kubernetes clusters and nodes. Those annotations surface in the EOL Inventory at `/ui/eol`, so you can see Vault 1.13 flagged EOL alongside any Kubernetes node also running an outdated release.

## Why declare applications

Declaring applications on a VM enables three things:

1. **EOL enrichment for platform software.** The enricher reads each VM's `applications` list and looks up every product on [endoflife.date](https://endoflife.date). Vault 1.13.4, BIND 9.18, OpenSSH 8.4, Nginx 1.24 — any product endoflife.date tracks will be annotated on the next enricher tick. Products it does not track (Cyberwatch, internal tools) receive a `lifecycle unknown` stub so auditors can see the row was evaluated rather than silently skipped.

2. **Searchable inventory.** `GET /v1/virtual-machines?application=vault` returns every VM in the fleet whose applications list includes vault, served by a GIN index. The VM list page (`/ui/virtual-machines`) exposes an Application filter with a cascading version dropdown.

3. **Audit evidence.** Each entry records who added it and when. The timestamp is visible in the UI ("recorded 2025-09-12") so operators can spot stale data.

## Declare applications via the UI

1. Navigate to **Virtual Machines** and open the VM's detail page at `/ui/virtual-machines/:id`.
2. Find the **Applications** card, between the Tags / Labels / Annotations card and the Curated Metadata card.
3. Click **Edit applications** (visible to editors and admins; viewers see a read-only table).
4. Use the per-row editor to add, edit, or remove entries. Each row has:
   - **Product** (required) — the software name, e.g. `vault`, `cyberwatch`, `bind`. The server normalizes the value to lowercase kebab-case on save, so `Hashicorp Vault` and `hashicorp-vault` become the same key. Tip: the field autocompletes against products already recorded in the fleet so your entry stays consistent.
   - **Version** (required) — the installed version, e.g. `1.15.4`. The enricher extracts the major.minor cycle (`1.15`) to look up lifecycle data.
   - **Name** (optional) — a label when the same product runs as multiple instances, e.g. `vault-prod-eu`.
   - **Notes** (optional) — free-form context, e.g. "autounseal via OSC KMS".
5. Click **Save**. The UI sends the full replacement list — remove a row and it is gone from the stored list on save.

The enricher annotates the VM on its next tick (default: every 2 minutes). Refresh the detail page and the EOL status badge appears next to each application in the read-mode table.

## Declare applications via the API

`PATCH /v1/virtual-machines/{id}` accepts an `applications` field. Supply the complete list each time — the semantics are replace, not append. Omitting `applications` from the request leaves the existing list unchanged.

**Add applications to a VM:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X PATCH \
  https://argos.internal:8080/v1/virtual-machines/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{
    "applications": [
      {
        "product": "vault",
        "version": "1.15.4",
        "name": "vault-prod-eu",
        "notes": "production secrets backend, autounseal via OSC KMS"
      },
      {
        "product": "cyberwatch",
        "version": "12.4"
      },
      {
        "product": "bind",
        "version": "9.18.30"
      }
    ]
  }'
```

The server normalizes each `product` value (trim, lowercase, whitespace / underscore / hyphen runs collapsed to a single hyphen) before writing. The response body is the updated VM object.

**Clear all applications:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" -X PATCH \
  https://argos.internal:8080/v1/virtual-machines/<id> \
  -H 'Content-Type: application/merge-patch+json' \
  -d '{"applications": []}'
```

### Field reference

| Field | Required | Max length | Notes |
|-------|----------|------------|-------|
| `product` | yes | 64 chars | Normalized to lowercase kebab-case on write. Should match an endoflife.date product id when one exists. |
| `version` | yes | 64 chars | Free-form. The enricher extracts `X.Y` from values like `1.15.4` or `v9.18.30`. |
| `name` | no | 200 chars | Disambiguates multiple instances of the same product on one VM. |
| `notes` | no | 4096 chars | Free-form operator context. |
| `added_at` | server-stamped | — | Set on first write; preserved across subsequent PATCHes for unchanged entries. Input values are ignored. |
| `added_by` | server-stamped | — | Username (for session auth) or `token:<name>` (for PATs). Input values are ignored. |

**Validation limits:** maximum 100 application entries per VM. Empty `product` or `version` is rejected with `400`. Duplicate `(product, version, name)` tuples within the same submission are rejected with `400`.

## Search the VM inventory

The VM list page (`/ui/virtual-machines`) exposes a toolbar of filters. Filters compose: all active filters are AND-ed server-side.

### Dropdown filters (apply immediately)

| Filter | Match type | Notes |
|--------|-----------|-------|
| Cloud account | Exact UUID | Admin only — viewers and editors see a UUID prefix fallback. |
| Region | Exact | Populated from accounts and ingested VMs. |
| Role | Exact | Client-side split on comma-joined role strings. |
| Power state | Exact | One of `running`, `stopped`, `stopped`, `pending`, `terminated`, `error`, `unknown`. |
| Application | Exact (normalized product) | Populated from `GET /v1/virtual-machines/applications/distinct`. |
| App version | Exact | Enabled only after an Application is selected; shows versions for that product. Selecting a different product resets the version filter. |

The **Application** and **App version** dropdowns are cascading: picking a product in the Application dropdown repopulates the App version dropdown with only the versions of that product currently recorded in the fleet.

### Text search inputs (explicit submission)

The **Name** and **Image** inputs perform case-insensitive substring matching against the VM name / display name and the AMI image name / image ID respectively. The inputs do not auto-apply on typing. After entering your terms, click **Search** (or press Enter) to send the query. Click **Clear** to remove both text filters.

This explicit-submit behaviour is intentional: substring queries involve a full table scan at typical fleet sizes and should be user-initiated rather than firing on every keystroke.

### Searching via the API

The same filters are available as query parameters on `GET /v1/virtual-machines`. See the [API reference](api-reference.md#virtual-machines) for the full parameter table.

**Find every VM running Vault:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'https://argos.internal:8080/v1/virtual-machines?application=vault'
```

**Find every VM running a specific Vault version:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'https://argos.internal:8080/v1/virtual-machines?application=vault&application_version=1.13.4'
```

**Find every VM in `eu-west-2` named "bastion":**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'https://argos.internal:8080/v1/virtual-machines?region=eu-west-2&name=bastion'
```

**Find every VM running a specific AMI:**

```bash
curl -sS -H "Authorization: Bearer $TOKEN" \
  'https://argos.internal:8080/v1/virtual-machines?image=ami-75374985'
```

## Image and application search across all entities

The global search at `/ui/search/image` (renamed **Search by image or application**) runs queries across Kubernetes workloads, pods, and non-Kubernetes VMs in parallel. For VMs, it runs two searches:

- `?image=<query>` — substring match on the AMI `image_name` and `image_id` fields.
- `?application=<query>` — exact match on the normalized product name.

The two VM result sets are unioned by id so a VM whose AMI name and applications both match the query is not counted twice. The page shows three tables: Matching workloads, Matching pods, and Matching virtual machines.

The **Match** column in the VM table explains what triggered each hit: `image: <name>` for AMI matches, or `<product> <version>` for application matches. A VM can appear because of either reason or both.

Non-admin users see a UUID prefix in place of the cloud account name in the Matching virtual machines table (cloud account names are only readable by admins).

## How EOL enrichment flows back

The enricher runs on its configured interval (default: 2 minutes). On each tick it:

1. Checks the `eol_enabled` runtime setting. If disabled, it skips.
2. Paginates through all non-terminated VMs.
3. For each VM, reads the `applications` array. Skips entries with an empty `product` or `version`.
4. Calls the endoflife.date API for each product to get lifecycle data for the declared version's cycle.
5. Writes `argos.io/eol.<product>` annotations on the VM:
   - **Known product, matched cycle:** full annotation with `eol_status`, `eol` date, `latest`, `latest_available`.
   - **Unknown product** (not on endoflife.date, or version does not parse to a major.minor cycle): stub annotation with `eol_status: unknown`. The row still appears in the EOL Inventory so auditors see it was evaluated.
6. Drops any existing `argos.io/eol.*` annotations that no longer correspond to a declared application — removing an application from the list removes its annotation on the next tick.

Annotations under any other key (owner team, custom tags, other `argos.io/*` namespaces) are preserved untouched.

The updated annotations are visible on the VM detail page under **Annotations**, and in the **EOL Inventory** at `/ui/eol`. For more information on the enricher itself, see [EOL Enrichment](eol-enrichment.md).

## Keeping application data accurate

Application declarations are operator-curated. The enricher uses whatever `version` was last written; it does not detect upgrades automatically. The `added_at` timestamp is shown in the read-mode Applications table to help operators spot entries that have not been updated in a long time.

When a VM is decommissioned (soft-deleted via reconciliation), the enricher skips it. Terminated VMs' EOL rows gradually age out of the dashboard as the `checked_at` field stops advancing.
