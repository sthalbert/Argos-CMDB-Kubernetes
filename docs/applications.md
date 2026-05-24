<div align="center"><img src="logo.svg" alt="longue-vue" height="38" /></div>

---

# Applications

An **Application** is longue-vue's first-class record of an applicative-layer asset — "Vault", "BIND", "the billing service" — the thing an SNC auditor points at to read an owner, a security classification, and the list of technical assets that implement it. An **ApplicationBlock** is the optional grouping one level above (office applications, management applications, development applications, …), exactly as the ANSSI SecNumCloud chapter 8 framing describes.

Both are **operator-curated**: collectors never create or modify them. A workload or a VM is linked to at most one Application; an Application optionally belongs to one Block.

This guide covers the admin/editor tasks: creating blocks and applications, linking workloads and VMs, classifying DICT security needs, reading the per-application EOL summary, and using the classification heat-map for SNC evidence.

For background on why Application is a first-class entity (and not a tag on workloads or an alias for Namespace), see [ADR-0029](adr/adr-0029-first-class-application-entity.md). DICT itself originates in [ADR-0008](adr/adr-0008-secnumcloud-chapter-8-asset-management.md).

## The two-level model

```
ApplicationBlock        "management-applications"
   └── Application       "vault"          (owner, criticality, DICT, runbook)
         ├── Workload    kube-prod / vault
         ├── Workload    kube-staging / vault
         └── VM-app      bastion-eu-west-2 / vault   (a per-VM applications[] entry)
```

An Application can span Kubernetes **and** VMs — the cross-substrate "this is one application" view that the per-asset model could not express. Membership is 1:N (each asset belongs to at most one Application); the link is a nullable FK, so deleting an Application unlinks its members but never destroys them.

Who can do what:

| Action | viewer / auditor | editor | admin |
|--------|:---:|:---:|:---:|
| View applications / blocks | ✓ | ✓ | ✓ |
| Create / edit applications, link / unlink assets | ✗ | ✓ | ✓ |
| Create / edit blocks | ✗ | ✓ | ✓ |
| Delete an application or block | ✗ | ✗ | ✓ |

## Create an application block

Blocks are optional — an Application with no block lives in the "Unblocked" group. Create one when you want the second-level SNC grouping.

**UI:**

1. Sign in as `admin` or `editor`.
2. Navigate to **Admin > Application blocks**.
3. Click **Create block**, fill in **Name** (normalised to kebab-case), optional **Display name**, **Description**, **Owner**, **Notes**.
4. Click **Create**.

**API equivalent:**

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST https://longue-vue.internal:8080/v1/application-blocks \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "management-applications",
    "display_name": "Management applications",
    "owner": "platform-team"
  }'
```

`POST` is idempotent on `name` (200 on hit, 201 on insert), so re-running the call never errors.

## Create an application

**UI:**

1. Navigate to **Applications** and click **Create application** (or use the inline "Create new application…" modal from a workload/VM picker).
2. Fill in:
   - **Name** — operator-friendly, unique, normalised to kebab-case (`vault`, `billing-api`). Editable later via display name.
   - **Block** — optional; pick an existing block or leave Unblocked.
   - **Owner**, **Criticality**, **Notes**, **Runbook URL** — curated metadata, all optional.
   - **DICT** — the four security-need axes, optional (see [DICT classification](#dict-classification)).
3. Click **Create**.

**API equivalent (with a block and DICT):**

```bash
curl -sS -b /tmp/longue-vue.cookies -X POST https://longue-vue.internal:8080/v1/applications \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "vault",
    "display_name": "HashiCorp Vault",
    "application_block_name": "management-applications",
    "owner": "platform-team",
    "criticality": "critical",
    "runbook_url": "https://wiki.example.com/runbooks/vault",
    "sec_disponibilite": 4,
    "sec_integrite": 4,
    "sec_confidentialite": 4,
    "sec_tracabilite": 3,
    "sec_notes": "Holds the platform root secrets; max DICT on C/I/A."
  }'
```

`application_block_id` and `application_block_name` are both accepted; the id wins if both are set. The body may omit the block, the DICT axes, or both — everything except `name` is optional.

## Link a workload

A workload links to one Application. The collector never touches the link — its per-tick patches preserve `application_id` by omission.

**UI:** open the workload detail page (**Workloads > …**). The **Application** card shows the current link or "Not linked". Click **Link…**, search the autocomplete (`vault`), and confirm — or create a new application inline.

**API equivalent:**

```bash
# Link by name (resolved server-side) …
curl -sS -b /tmp/longue-vue.cookies -X PATCH \
  https://longue-vue.internal:8080/v1/workloads/<id> \
  -H 'Content-Type: application/json' \
  -d '{"application_name": "vault"}'

# … or by id, which wins if both are sent.
curl -sS -b /tmp/longue-vue.cookies -X PATCH \
  https://longue-vue.internal:8080/v1/workloads/<id> \
  -H 'Content-Type: application/json' \
  -d '{"application_id": "b9c1...-uuid"}'

# Unlink:
curl -sS -b /tmp/longue-vue.cookies -X PATCH \
  https://longue-vue.internal:8080/v1/workloads/<id> \
  -H 'Content-Type: application/json' \
  -d '{"application_id": null}'
```

An unknown UUID is rejected with `400`. Filter workloads by their link with `GET /v1/workloads?application_name=vault` or find unlinked ones with `?unlinked=true`.

## Link a VM and per-VM-application entries

A VM links the same way as a workload — the **Application** card on the VM detail page, or `PATCH /v1/virtual-machines/{id}` with `application_id` / `application_name`.

A VM that runs **several** products (Vault *and* BIND on one bastion) routes each product independently. The per-VM `applications[]` entries from [ADR-0019](adr/adr-0019-vm-applications-and-eol-and-search.md) each gained an optional `application_id`, so each declared product can point at its own Application even when the VM as a whole is not linked:

```bash
curl -sS -b /tmp/longue-vue.cookies -X PATCH \
  https://longue-vue.internal:8080/v1/virtual-machines/<id> \
  -H 'Content-Type: application/json' \
  -d '{
    "applications": [
      {"product": "vault", "version": "1.15.4", "name": "vault-prod-eu", "application_id": "b9c1...-uuid"},
      {"product": "bind",  "version": "9.18.24", "name": "ns1",          "application_id": "7d2e...-uuid"}
    ]
  }'
```

In the UI, the per-VM-app row editor has a "linked application" picker; when a product name matches an existing application name exactly, the picker pre-selects it for you to confirm. The server never auto-links.

## DICT classification

DICT — **D**isponibilité / **I**ntégrité / **C**onfidentialité / **T**raçabilité — is the EBIOS-RM security-need vector. In longue-vue it lives **only on the Application** (per ADR-0029): four `sec_*` axes scored 0..4 plus a free-form `sec_notes` justification.

> **Note.** ADR-0008 originally planned DICT columns on namespaces and workloads, but that migration was never shipped (see ADR-0008's correction note and ADR-0029 §6 post-implementation notes). The Application is the single home for DICT today.

Set DICT when you create the application, or later via the **Classification (DICT)** card on the detail page (or `PATCH /v1/applications/{id}` with the `sec_*` fields). Validation: each axis is `0..4` and nullable; `sec_notes` ≤ 4096 characters.

**Inheritance.** Linked workloads and VMs surface a **read-only** `effective_dict` block on their list/detail responses, computed at read time with application-wins precedence:

```json
"effective_dict": {
  "disponibilite": 4,
  "integrite": 4,
  "confidentialite": 4,
  "tracabilite": 3,
  "notes": "Holds the platform root secrets; max DICT on C/I/A.",
  "source": "application"
}
```

`source` is `application` when the linked Application has any axis set, otherwise `none`. Operators classify at the Application level once; every linked asset inherits it, so MCP, extracts, and downstream reporting never re-implement the precedence. The `longue_vue_dict_coverage{source}` gauge tracks how many workloads sit in each effective-DICT state.

## Read the per-application EOL summary

The Application detail page's **End-of-life** card (and `GET /v1/applications/{id}/eol`) roll up the EOL signal across all members at read time — no background pass, no extra writes:

```bash
curl -sS -b /tmp/longue-vue.cookies \
  https://longue-vue.internal:8080/v1/applications/<id>/eol | jq '.rows[:3]'
```

The two member sources contribute differently:

- **Workloads** carry no EOL annotations; their signal comes from per-container **image-versions** enrichment ([ADR-0022](adr/adr-0022-image-versions-enrichment.md)). A workload whose container image is behind the latest available tag reports `eol_status: "outdated"`.
- **VMs** (and their per-app entries) contribute the `longue-vue.io/eol.<product>` annotations the EOL enricher writes from endoflife.date ([ADR-0019](adr/adr-0019-vm-applications-and-eol-and-search.md)), with `eol_status` ∈ `eol` / `approaching` / `supported` / `unknown`.

Each row lists a `sources` array naming the contributing assets, so "Vault 1.13 — EOL since 2024-12-09" resolves to the exact workloads and VM-apps behind the signal without a click.

## Classification heat-map and SNC evidence

**Admin > Classification** (`/ui/admin/classification`) is a single table of every Application whose `MAX(DICT)` meets an adjustable threshold (default 3). Columns: app name, block, D / I / C / T (heat-coloured), and a `sec_notes` excerpt. This is the SNC chapter 8 §8.3 evidence view.

Export it for the audit package:

```bash
# CSV of applications at DICT >= 3, audit-logged:
curl -sS -b /tmp/longue-vue.cookies \
  'https://longue-vue.internal:8080/v1/applications/extract.csv?dict_min=3' -o applications-dict.csv
```

`/v1/applications/extract.{csv,json}` is capped by `LONGUE_VUE_EXTRACT_MAX_ROWS` (default 50 000); an `X-Longue-Vue-Truncated: true` header signals the cap. Every extract is audit-logged.

## MCP tools

When the `mcp_enabled` setting is on, three **read-only** Application tools are exposed to MCP clients (gated by the caller's `read` scope):

| Tool | Purpose |
|------|---------|
| `list_applications` | Cursor-paginated; same filters as `GET /v1/applications`. |
| `get_application` | By id or name; full detail plus the member list. |
| `list_application_blocks` | Cursor-paginated with per-block application counts. |

There are no write tools — LLM-driven misclassification of SNC-relevant data is a risk class longue-vue deliberately avoids; operators edit via the UI or API.

> **Known follow-up.** `get_application` does **not** include the EOL summary in this release. Read the per-application EOL roll-up via `GET /v1/applications/{id}/eol` instead.

## See also

- [VM Applications](vm-applications.md) — declaring platform software on a VM (the `applications[]` entries an Application links to).
- [EOL Enrichment](eol-enrichment.md) — how the per-asset EOL annotations the application summary aggregates are produced.
- [SecNumCloud chapter 8](compliance/snc-chapter-8.md) — how Application closes the chapter-8 applicative-asset framing.
- [API Reference](api-reference.md) — endpoint catalogue.
- [ADR-0029](adr/adr-0029-first-class-application-entity.md) — design rationale.
- [ADR-0008](adr/adr-0008-secnumcloud-chapter-8-asset-management.md) — DICT origin and the asset-management data model.
