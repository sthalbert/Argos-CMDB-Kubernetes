# End-of-Life Enrichment

Argos can automatically flag software running past its end-of-life date. The EOL enricher periodically queries [endoflife.date](https://endoflife.date) and annotates clusters and nodes with lifecycle status so you can spot obsolescence risk at a glance.

## How it works

The enricher runs as a background goroutine inside argosd. On each tick it:

1. Reads the `eol_enabled` setting from the database. If disabled, the tick is skipped.
2. Lists every cluster and node in the CMDB.
3. Extracts version strings from known fields (Kubernetes version, container runtime, OS image, kernel version).
4. Matches each version against the endoflife.date API to determine lifecycle status.
5. Writes a structured annotation under the `argos.io/eol.<product>` key on the entity.

Existing annotations are preserved. The enricher only overwrites its own `argos.io/eol.*` keys.

## Enable the enricher

EOL enrichment is **disabled by default**. An admin enables it from the UI:

1. Sign in as an `admin` user.
2. Navigate to **Admin > Settings**.
3. Click **Enable** on the "End-of-Life enrichment" card.

The enricher picks up the change on its next tick (default: every 2 minutes). No pod restart is required.

To disable it again, click **Disable** on the same card.

> **Alternative: env var.** Setting `ARGOS_EOL_ENABLED=true` on the argosd Deployment seeds the database setting to `true` on startup. The UI toggle overrides it at runtime. See [Configuration](configuration.md) for all env vars.

## Use the EOL dashboard

All authenticated users can access the dashboard at **EOL** in the top navigation bar (or `/ui/eol`).

### Summary cards

Three cards at the top show counts by severity:

| Card | Colour | Meaning |
|------|--------|---------|
| **End of Life** | Red | The product version is past its EOL date. No patches are being released. |
| **Approaching EOL** | Orange | The product version will reach EOL within the configured window (default: 90 days). |
| **Supported** | Green | The product version is still under active support. |

**Click a card** to filter the table to that status only. Click the same card again (or the "clear" link) to remove the filter.

### Table

The table lists every enriched entity with its lifecycle data. Columns:

| Column | Description |
|--------|-------------|
| Status | Lifecycle badge (End of Life, Approaching EOL, Supported). |
| Product | The endoflife.date product identifier (e.g. `kubernetes`, `containerd`, `ubuntu`). |
| Cycle | The matched major.minor release cycle (e.g. `1.28`, `22.04`). |
| Entity | Link to the cluster or node detail page. |
| Cluster | Which cluster the entity belongs to. |
| EOL Date | The date the cycle reaches end of life (from endoflife.date). |
| Latest | The latest patch version available for that cycle. |
| Checked | When the enricher last verified this annotation. |

**Click a column header** to sort by that column. Click again to reverse the sort direction. An arrow indicator shows the current sort column and direction.

## Matched products

The enricher extracts versions from these fields:

| Entity | Field | Products matched |
|--------|-------|-----------------|
| Cluster | `kubernetes_version` | `kubernetes` |
| Node | `kubelet_version` | `kubernetes` |
| Node | `container_runtime_version` | `containerd`, `cri-o`, `docker` |
| Node | `os_image` | `ubuntu`, `debian`, `alpine`, `rhel`, `rocky-linux`, `alma-linux`, `amazon-linux`, `centos`, `fedora`, `oracle-linux`, `opensuse`, `sles`, `flatcar`, `cos` |
| Node | `kernel_version` | `linux` |

Products not listed on endoflife.date, or versions that don't match any known cycle, are silently skipped.

## Annotation format

Each enriched entity carries one annotation per matched product. The key is `argos.io/eol.<product>` and the value is a JSON string:

```json
{
  "product": "kubernetes",
  "cycle": "1.28",
  "eol": "2025-01-28",
  "eol_status": "eol",
  "support": "2024-11-28",
  "latest": "1.28.15",
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
| `latest` | Latest patch version for the cycle. |
| `checked_at` | UTC timestamp of the last enrichment check. |

These annotations are visible on the entity detail pages (cluster and node) in the "Annotations" section, and are queryable via the REST API.

## Configuration

The enricher behaviour is tuned with environment variables on the argosd Deployment. See the [Configuration Reference](configuration.md) for the full table. Key variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `ARGOS_EOL_ENABLED` | -- | Seeds the `eol_enabled` database setting on startup. The UI toggle overrides it at runtime. |
| `ARGOS_EOL_INTERVAL` | `2m` | Time between enrichment ticks. |
| `ARGOS_EOL_APPROACHING_DAYS` | `90` | Number of days before EOL to flag as "approaching". |
| `ARGOS_EOL_BASE_URL` | `https://endoflife.date` | Base URL for the endoflife.date API. Override to point at an internal mirror in air-gapped environments. |

## Monitoring

The enricher exports Prometheus metrics on the `/metrics` endpoint:

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `argos_eol_enrichments_total` | counter | `cluster`, `resource`, `status` | Annotations written per tick. |
| `argos_eol_errors_total` | counter | `cluster`, `resource`, `phase` | Errors during enrichment. `phase` is `list`, `resolve`, or `update`. |
| `argos_eol_last_run_timestamp_seconds` | gauge | -- | Unix timestamp of the last completed enrichment run. |

A simple freshness alert:

```
time() - argos_eol_last_run_timestamp_seconds > 600
```

## Air-gapped environments

The enricher makes outbound HTTPS requests to `endoflife.date`. In environments where this is not possible:

1. Set up an internal mirror of the endoflife.date API (the project publishes its data as JSON files).
2. Set `ARGOS_EOL_BASE_URL` to the mirror URL.
3. Standard proxy environment variables (`HTTPS_PROXY`, `NO_PROXY`) are honored by Go's HTTP client.

## Limitations

- **Container images are not matched.** Image tags are unstructured (`nginx:1.25-alpine`, `myapp:latest`) and matching them to endoflife.date products would require a registry-aware parser. This is planned for a future version.
- **Data accuracy depends on endoflife.date.** The project is community-maintained. Verify critical EOL decisions against vendor documentation.
- **A typo in `ARGOS_CLUSTER_NAME` creates a new cluster.** The auto-created cluster will be enriched, but with the wrong name. Verify cluster names after first deployment.
