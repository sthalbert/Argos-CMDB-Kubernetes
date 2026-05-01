# End-of-Life Enrichment

longue-vue can automatically flag software running past its end-of-life date. The EOL enricher periodically queries [endoflife.date](https://endoflife.date) and annotates clusters, nodes, and non-Kubernetes platform VMs with lifecycle status so you can spot obsolescence risk at a glance.

## How it works

The enricher runs as a background goroutine inside longue-vue. On each tick it:

1. Reads the `eol_enabled` setting from the database. If disabled, the tick is skipped.
2. Lists every cluster in the CMDB and processes them, including their nodes.
3. Lists every non-terminated non-Kubernetes VM in the CMDB.
4. Extracts version strings from known fields (Kubernetes version, container runtime, OS image, kernel version on clusters and nodes; operator-declared applications on VMs).
5. Matches each version against the endoflife.date API to determine lifecycle status.
6. Writes a structured annotation under the `longue-vue.io/eol.<product>` key on the entity.

For VMs, stale `longue-vue.io/eol.*` annotations (from applications that were removed from the list) are dropped on each tick. Annotations under any other key are preserved. For clusters and nodes, the enricher only overwrites its own `longue-vue.io/eol.*` keys; all other annotations are untouched.

## Enable the enricher

EOL enrichment is **disabled by default**. An admin enables it from the UI:

1. Sign in as an `admin` user.
2. Navigate to **Admin > Settings**.
3. Click **Enable** on the "End-of-Life enrichment" card.

The enricher picks up the change on its next tick (default: every 2 minutes). No pod restart is required.

To disable it again, click **Disable** on the same card.

> **Alternative: env var.** Setting `LONGUE_VUE_EOL_ENABLED=true` on the longue-vue Deployment seeds the database setting to `true` on startup. The UI toggle overrides it at runtime. See [Configuration](configuration.md) for all env vars.

## Use the EOL inventory

All authenticated users can access the inventory at **EOL** in the top navigation bar (or `/ui/eol`).

The inventory covers three entity types: **clusters**, **nodes**, and **virtual machines**. Kubernetes clusters and nodes are enriched from their version fields (Kubernetes, container runtime, OS image, kernel). Non-Kubernetes platform VMs are enriched from their operator-declared `applications` list (see [VM Applications](vm-applications.md)).

### Summary cards

Three cards at the top show counts by severity:

| Card | Colour | Meaning |
|------|--------|---------|
| **End of Life** | Red | The product version is past its EOL date. No patches are being released. |
| **Approaching EOL** | Orange | The product version will reach EOL within the configured window (default: 90 days). |
| **Supported** | Green | The product version is still under active support. |

**Click a card** to filter the table to that status only. Click the same card again (or the "clear" link) to remove the filter.

### Table

The table lists every enriched entity with its lifecycle data. Columns are grouped into two sections separated by a visual border:

**What we run** — the software currently deployed:

| Column | Description |
|--------|-------------|
| Status | Lifecycle badge (End of Life, Approaching EOL, Supported). Rows are highlighted red for EOL, orange for approaching EOL. |
| Product | The endoflife.date product identifier (e.g. `kubernetes`, `containerd`, `ubuntu`, `vault`). |
| Version | The matched major.minor release cycle (e.g. `1.28`, `22.04`, `1.15`). |
| Patch | The latest patch version available for the entity's current cycle. |
| Entity | Link to the cluster, node, or virtual machine detail page. A small type label (`cluster`, `node`, `vm`) appears alongside the entity name so you can distinguish a bastion VM from a kube node when both run the same software. |
| Cluster | Which cluster the entity belongs to. Empty for virtual machines (VMs are not cluster-scoped). |

**What's available** — upgrade targets and lifecycle dates:

| Column | Description |
|--------|-------------|
| Latest Available | The newest version of the product published on endoflife.date (e.g. `1.32.3` when the entity runs `1.28`). |
| EOL Date | The date the current cycle reaches end of life (from endoflife.date). |
| Checked | When the enricher last verified this annotation. |

**Click a column header** to sort by that column. Click again to reverse the sort direction. An arrow indicator shows the current sort column and direction.

## Matched products

### Kubernetes clusters and nodes

The enricher extracts versions from these fields:

| Entity | Field | Products matched |
|--------|-------|-----------------|
| Cluster | `kubernetes_version` | `kubernetes` |
| Node | `kubelet_version` | `kubernetes` |
| Node | `container_runtime_version` | `containerd`, `cri-o`, `docker` |
| Node | `os_image` | `ubuntu`, `debian`, `alpine`, `rhel`, `rocky-linux`, `alma-linux`, `amazon-linux`, `centos`, `fedora`, `oracle-linux`, `opensuse`, `sles`, `flatcar`, `cos` |
| Node | `kernel_version` | `linux` |

Products not listed on endoflife.date, or versions that don't match any known cycle, are silently skipped.

### Virtual machines

Non-Kubernetes platform VMs are enriched via the operator-curated `applications` field. The enricher reads each VM's `applications` array and writes one `longue-vue.io/eol.<product>` annotation per declared entry. See [VM Applications](vm-applications.md) for how to declare applications.

**What the enricher does with each application entry:**

1. Normalizes the `product` name (trim, lowercase, whitespace / underscore / hyphen runs to single hyphens) — same normalization applied at write time.
2. Calls endoflife.date for the product. If the product is not tracked by endoflife.date (e.g. Cyberwatch, an internal tool), the enricher writes a stub annotation with `eol_status: unknown` rather than silently skipping the row. Operators can see the data was evaluated.
3. Extracts the major.minor cycle from the declared `version` string. A leading `v` and any build suffix are stripped (`v1.15.4-ent` → cycle `1.15`). If no major.minor can be parsed, the stub annotation is used.
4. Matches the cycle against the product's release list on endoflife.date and writes a full annotation including `eol_status`, `eol` date, `latest`, and `latest_available`.

On each tick, the enricher drops any `longue-vue.io/eol.*` annotations that no longer correspond to a declared application, so stale annotations from removed entries disappear automatically. Annotations under any other key are preserved.

Terminated VMs are skipped.

| Entity | Source | Products matched |
|--------|--------|-----------------|
| Virtual machine | `applications[].product` + `applications[].version` | Any product on endoflife.date (e.g. `vault`, `bind`, `nginx`, `openssh`, `postgresql`). Unknown products receive a `lifecycle unknown` stub. |

## Annotation format

Each enriched entity carries one annotation per matched product. The key is `longue-vue.io/eol.<product>` and the value is a JSON string:

```json
{
  "product": "kubernetes",
  "cycle": "1.28",
  "eol": "2025-01-28",
  "eol_status": "eol",
  "support": "2024-11-28",
  "latest": "1.28.15",
  "latest_available": "1.32.3",
  "checked_at": "2026-04-24T10:00:00Z"
}
```

| Field | Description |
|-------|-------------|
| `product` | endoflife.date product identifier. |
| `cycle` | Matched major.minor release cycle. |
| `eol` | EOL date in `YYYY-MM-DD` format. Empty when the product has no fixed EOL date. |
| `eol_status` | One of `eol`, `approaching_eol`, `supported`, `unknown`. |
| `support` | End of active support date, when available. |
| `latest` | Latest patch version for the entity's current cycle. |
| `latest_available` | Latest version of the product overall (newest cycle's latest patch from endoflife.date). |
| `checked_at` | UTC timestamp of the last enrichment check. |

These annotations are visible on the entity detail pages (cluster, node, and virtual machine) in the "Annotations" section, and are queryable via the REST API. For VMs, the status badge also appears in the Applications card read-mode table.

## Configuration

The enricher behaviour is tuned with environment variables on the longue-vue Deployment. See the [Configuration Reference](configuration.md) for the full table. Key variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LONGUE_VUE_EOL_ENABLED` | -- | Seeds the `eol_enabled` database setting on startup. The UI toggle overrides it at runtime. |
| `LONGUE_VUE_EOL_INTERVAL` | `2m` | Time between enrichment ticks. |
| `LONGUE_VUE_EOL_APPROACHING_DAYS` | `90` | Number of days before EOL to flag as "approaching". |
| `LONGUE_VUE_EOL_BASE_URL` | `https://endoflife.date` | Base URL for the endoflife.date API. Override to point at an internal mirror in air-gapped environments. |

## Monitoring

The enricher exports Prometheus metrics on the `/metrics` endpoint:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `longue_vue_eol_enrichments_total` | counter | `cluster`, `resource`, `status` | Annotations written per tick. |
| `longue_vue_eol_errors_total` | counter | `cluster`, `resource`, `phase` | Errors during enrichment. `phase` is `list`, `resolve`, or `update`. |
| `longue_vue_eol_last_run_timestamp_seconds` | gauge | -- | Unix timestamp of the last completed enrichment run. |

A simple freshness alert:

```
time() - longue_vue_eol_last_run_timestamp_seconds > 600
```

## Air-gapped environments

The enricher makes outbound HTTPS requests to `endoflife.date`. In environments where this is not possible:

1. Set up an internal mirror of the endoflife.date API (the project publishes its data as JSON files).
2. Set `LONGUE_VUE_EOL_BASE_URL` to the mirror URL.
3. Standard proxy environment variables (`HTTPS_PROXY`, `NO_PROXY`) are honored by Go's HTTP client.

## Limitations

- **Container images are not matched.** Image tags are unstructured (`nginx:1.25-alpine`, `myapp:latest`) and matching them to endoflife.date products would require a registry-aware parser. This is planned for a future version.
- **VM application data is operator-curated.** The enricher annotates whatever version was last written to the `applications` field. If a VM's Vault version is upgraded without updating longue-vue, the EOL annotation reflects the old version. The `added_at` timestamp on each application entry is shown in the UI to help spot stale records. An in-guest agent for automatic discovery is planned for a future version.
- **Data accuracy depends on endoflife.date.** The project is community-maintained. Verify critical EOL decisions against vendor documentation.
- **A typo in `LONGUE_VUE_CLUSTER_NAME` creates a new cluster.** The auto-created cluster will be enriched, but with the wrong name. Verify cluster names after first deployment.
