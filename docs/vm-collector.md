# Deploy the VM Collector

`argos-vm-collector` is a standalone push-mode binary that catalogues platform VMs running **outside** any Kubernetes cluster — VPN gateways, DNS servers, bastions, Vault clusters, build runners. It polls a cloud provider's management API, deduplicates against the kube-node inventory, and pushes the rest to argosd over HTTPS.

This guide covers what the binary is, how to configure it, three deployment recipes (Kubernetes, bare VM with systemd, `docker run`), gateway and proxy setup, troubleshooting, and the Prometheus metrics it exposes.

For background on why this is a separate binary (and not a goroutine inside argosd), see [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md).

## What it is

A small static Go binary, distroless base image, UID 65532, no listening port except `/metrics`. One instance per cloud account. Stateless — no database, no on-disk state, no kubeconfig.

It does three things on a loop:

1. Fetches the cloud-provider AK/SK from argosd over HTTPS (no creds in env vars).
2. Lists VMs from the cloud provider's API and pre-filters anything tagged as a kube node.
3. Pushes each remaining VM to `POST /v1/virtual-machines` and reconciles the rest with `POST /v1/virtual-machines/reconcile`.

Argosd performs the canonical dedup against the `nodes` table server-side: any VM whose provider ID matches an existing kube node returns `409 Conflict` and the collector silently skips it. The pre-filter is just optimisation.

## Prerequisites

1. **argosd is running** and reachable over HTTPS (directly or through a gateway).
2. **A `vm-collector` PAT bound to the target account** has been issued. See [Issue a collector token](cloud-accounts.md#issue-a-collector-token).
3. **The cloud account exists in argosd** — either pre-registered with credentials, or you are using the [hybrid onboarding flow](cloud-accounts.md#option-b--collector-first-hybrid) where the collector creates the placeholder.
4. **`ARGOS_SECRETS_MASTER_KEY` is set on argosd** so credentials can be encrypted at rest. See [master-key backup](cloud-accounts.md#master-key-backup).

## Configuration

All configuration is via environment variables.

### Connection

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_SERVER_URL` | yes | -- | argosd base URL. Example: `https://argos.internal:8080`. Supports a path prefix for gateway deployments: `https://gateway:443/argos`. |
| `ARGOS_API_TOKEN` | yes | -- | Bearer PAT with `vm-collector` scope, bound to the target cloud account. Format: `argos_pat_<prefix>_<suffix>`. |

### Cloud account identity

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_VM_COLLECTOR_PROVIDER` | no | `outscale` | Cloud provider. Only `outscale` is supported in v1. |
| `ARGOS_VM_COLLECTOR_ACCOUNT_NAME` | yes | -- | Cloud account name. Must match `cloud_accounts.name` in argosd. The collector self-registers a placeholder if the account does not yet exist. |
| `ARGOS_VM_COLLECTOR_REGION` | yes | -- | Cloud region. Example: `eu-west-2`. |
| `ARGOS_VM_COLLECTOR_PROVIDER_ENDPOINT_URL` | no | provider default | Override the cloud-provider API endpoint. Useful for sovereign-cloud regions or in-network mirrors. |

### Behaviour

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_VM_COLLECTOR_INTERVAL` | no | `5m` | Time between polling ticks. Accepts Go duration syntax (`30s`, `5m`, `1h`). |
| `ARGOS_VM_COLLECTOR_FETCH_TIMEOUT` | no | `30s` | Per-tick timeout for cloud-provider API calls. |
| `ARGOS_VM_COLLECTOR_RECONCILE` | no | `true` | Call `POST /v1/virtual-machines/reconcile` after each tick to soft-delete VMs that disappeared. |
| `ARGOS_VM_COLLECTOR_CREDENTIAL_REFRESH` | no | `1h` | How often to re-fetch credentials from argosd. Lower this if you rotate AK/SK frequently. |
| `ARGOS_VM_COLLECTOR_METRICS_ADDR` | no | `127.0.0.1:9090` | Listen address for the `/metrics` endpoint. **Set to `0.0.0.0:9090` when running in Kubernetes** so the kubelet can reach the liveness probe. |

### TLS and gateway

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ARGOS_CA_CERT` | no | system CA pool | Path to a PEM-encoded CA certificate for verifying the argosd (or gateway) TLS certificate. Required when argosd uses a private CA. |
| `ARGOS_CLIENT_CERT` | no | -- | Path to a PEM-encoded client certificate for mTLS to a gateway. |
| `ARGOS_CLIENT_KEY` | no | -- | Path to a PEM-encoded client private key for mTLS. Required when `ARGOS_CLIENT_CERT` is set. |
| `ARGOS_EXTRA_HEADERS` | no | -- | Comma-separated `key=value` pairs injected into every outbound HTTP request to argosd. Useful for gateway routing or tenant identification. Example: `X-Tenant-Id=acme-prod,X-Route-Key=argos`. |

### Proxy

The collector honours the standard Go proxy environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `HTTPS_PROXY` | no | -- | Forward proxy URL for HTTPS traffic. |
| `HTTP_PROXY` | no | -- | Forward proxy URL for HTTP traffic. |
| `NO_PROXY` | no | -- | Comma-separated list of hosts/CIDRs to bypass the proxy. |

### What is *not* configurable

There is **no `ARGOS_AK` or `ARGOS_SK` env var**. AK/SK live exclusively in argosd's `cloud_accounts` table and are fetched at runtime. This is deliberate — see [ADR-0015 §4](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) for the rationale.

## Deployment recipes

### Kubernetes (Kustomize)

Reference manifests live in `deploy/vm-collector/`.

```sh
# 1. Mint the collector PAT in the Argos UI (Admin > Cloud Accounts > Issue collector token).
# 2. Stash it in a Kubernetes Secret.
kubectl -n argos-system create secret generic argos-vm-collector-token \
  --from-literal=ARGOS_API_TOKEN='argos_pat_3f9c1e7a_5N2pKdQ...'

# 3. Edit deploy/vm-collector/configmap.yaml — set ARGOS_SERVER_URL, ARGOS_VM_COLLECTOR_ACCOUNT_NAME, ARGOS_VM_COLLECTOR_REGION.
# 4. Apply.
kubectl apply -k deploy/vm-collector/

# 5. Watch the logs.
kubectl -n argos-system logs -l app.kubernetes.io/component=vm-collector -f
```

The Kustomize bundle ships:

- `Deployment` (replicas: 1) running `argos-vm-collector`.
- `Secret` carrying `ARGOS_API_TOKEN`.
- `ConfigMap` for non-secret env vars.
- Egress `NetworkPolicy` allowing HTTPS to the argosd endpoint and the cloud-provider API only.

> **Important:** when running in Kubernetes, set `ARGOS_VM_COLLECTOR_METRICS_ADDR=0.0.0.0:9090` so the kubelet can reach the liveness probe. The default (`127.0.0.1:9090`) is fine for systemd / docker but rejects external scrapes.

### Bare VM with systemd

For deployments where Kubernetes itself is one of the VMs being catalogued, run the collector on a small management VM:

```ini
# /etc/systemd/system/argos-vm-collector.service
[Unit]
Description=Argos VM Collector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=argos
Group=argos
ExecStart=/usr/local/bin/argos-vm-collector
Restart=on-failure
RestartSec=10s

# Environment
Environment="ARGOS_SERVER_URL=https://argos.internal:8080"
EnvironmentFile=/etc/argos/vm-collector.env
Environment="ARGOS_VM_COLLECTOR_PROVIDER=outscale"
Environment="ARGOS_VM_COLLECTOR_ACCOUNT_NAME=acme-prod"
Environment="ARGOS_VM_COLLECTOR_REGION=eu-west-2"
Environment="ARGOS_VM_COLLECTOR_INTERVAL=5m"

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ReadOnlyPaths=/
ReadWritePaths=

[Install]
WantedBy=multi-user.target
```

Stash the PAT in a root-only file:

```sh
sudo install -m 0600 -o root -g root /dev/null /etc/argos/vm-collector.env
sudo tee /etc/argos/vm-collector.env >/dev/null <<'EOF'
ARGOS_API_TOKEN=argos_pat_3f9c1e7a_5N2pKdQ...
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now argos-vm-collector.service
sudo journalctl -u argos-vm-collector -f
```

### docker run

Quick smoke test or one-off catalogue from a workstation:

```sh
docker run --rm \
  -e ARGOS_SERVER_URL=https://argos.internal:8080 \
  -e ARGOS_API_TOKEN=argos_pat_3f9c1e7a_5N2pKdQ... \
  -e ARGOS_VM_COLLECTOR_PROVIDER=outscale \
  -e ARGOS_VM_COLLECTOR_ACCOUNT_NAME=acme-prod \
  -e ARGOS_VM_COLLECTOR_REGION=eu-west-2 \
  -e ARGOS_VM_COLLECTOR_INTERVAL=1m \
  -p 9090:9090 \
  -e ARGOS_VM_COLLECTOR_METRICS_ADDR=0.0.0.0:9090 \
  argos-vm-collector:dev
```

Watch the logs in the foreground; hit Ctrl+C to stop.

## Behind a gateway or proxy

In SecNumCloud environments, outbound traffic from the collector typically transits through an API gateway, forward proxy, or both. The same conventions used by the [push collector](deployment/push-collector.md) apply here.

### HTTP(S) proxy

```yaml
env:
  - name: HTTPS_PROXY
    value: "http://proxy.zad.internal:3128"
  - name: NO_PROXY
    value: "10.0.0.0/8,.zad.internal"
```

Go's `net/http` honours these automatically. The proxy applies to **both** the argosd connection and the cloud-provider API connection — make sure both endpoints are reachable through it (or list the cloud-provider endpoint in `NO_PROXY` if the proxy doesn't egress to the public internet).

### Reverse proxy / API gateway in front of argosd

If a gateway exposes argosd under a sub-path, include the prefix in the server URL:

```yaml
env:
  - name: ARGOS_SERVER_URL
    value: "https://gateway.zad.internal:443/argos"
```

The collector prepends this base path to every API request (`/argos/v1/cloud-accounts/...`, `/argos/v1/virtual-machines`, etc.).

### Custom headers

```yaml
env:
  - name: ARGOS_EXTRA_HEADERS
    value: "X-Tenant-Id=acme-prod,X-Route-Key=argos"
```

### mTLS to the gateway

```yaml
env:
  - name: ARGOS_CA_CERT
    value: "/etc/argos/tls/ca.pem"
  - name: ARGOS_CLIENT_CERT
    value: "/etc/argos/tls/client.crt"
  - name: ARGOS_CLIENT_KEY
    value: "/etc/argos/tls/client.key"
volumes:
  - name: tls
    secret:
      secretName: argos-vm-collector-tls
volumeMounts:
  - name: tls
    mountPath: /etc/argos/tls
    readOnly: true
```

The mTLS material applies only to the argosd connection. Cloud-provider authentication uses AK/SK fetched at runtime — there is no separate cloud-side TLS material to manage.

## Troubleshooting

### `credentials unavailable` / `cloud_account_not_registered`

The admin has not yet entered AK/SK for this account. Open **Admin > Cloud Accounts**, find the row in `pending_credentials` status, and set credentials. The collector retries every credential-refresh interval (default 1 hour) — if you don't want to wait, restart the collector after entering credentials.

If the account does not exist at all, verify `ARGOS_VM_COLLECTOR_ACCOUNT_NAME` matches the registered name exactly (case-sensitive). The collector should self-register a placeholder on first contact; if it doesn't, check the PAT scope and account binding.

### `outscale ReadVms 422` (or similar provider-side error)

Usually means the AK/SK or region is wrong. Verify in the cloud-provider console that:

- The AK/SK pair is active and not rotated.
- The user behind the AK has permission to call `ReadVms` (or the equivalent on other providers).
- `ARGOS_VM_COLLECTOR_REGION` matches a region the AK is authorised for.

Rotate the credentials in **Admin > Cloud Accounts > Rotate credentials** if the issue is on the AK/SK side.

### `token bound to a different cloud account`

The PAT was issued for cloud account A but the collector is configured with `ARGOS_VM_COLLECTOR_ACCOUNT_NAME=B`. Each `vm-collector` PAT is bound to exactly one account at issuance — there is no way to retarget it.

Mint a new PAT bound to account B (see [Issue a collector token](cloud-accounts.md#issue-a-collector-token)) and update `ARGOS_API_TOKEN`.

### `409 Conflict` / `already_inventoried_as_kubernetes_node`

This is **not an error** — it is the deduplication flow. The VM with that ID is already in the `nodes` table because a Kubernetes collector inventoried it. The vm-collector logs `vm i-96fff41b is a kube node, skipping` and continues. The pre-filter (Outscale CCM tags `OscK8sClusterID/*`, `OscK8sNodeName=*`) catches most of these locally; the server-side check is the safety net.

### Liveness probe loop / metrics not reachable

The default `ARGOS_VM_COLLECTOR_METRICS_ADDR` is `127.0.0.1:9090` — fine for systemd and `docker run`, but the kubelet cannot reach `127.0.0.1:9090` inside the pod's network namespace.

When deploying in Kubernetes, set:

```yaml
env:
  - name: ARGOS_VM_COLLECTOR_METRICS_ADDR
    value: "0.0.0.0:9090"
```

The reference Kustomize manifest (`deploy/vm-collector/configmap.yaml`) sets this by default; it only bites if you fork the manifests.

### `401 Unauthorized` from argosd

- The PAT is invalid, expired, or revoked. Check **Admin > Tokens** in the argosd UI.
- The `Authorization: Bearer` header is malformed. Verify `ARGOS_API_TOKEN` starts with `argos_pat_`.

### `403 Forbidden` from `GET /credentials`

- The cloud account is `disabled`. Re-enable it from **Admin > Cloud Accounts** if appropriate.
- The PAT does not have the `vm-collector` scope, or it is bound to a different account. Mint a fresh PAT.

### TLS certificate errors

- The argosd (or gateway) TLS certificate is signed by a private CA. Set `ARGOS_CA_CERT` to the CA PEM file.
- For mTLS, ensure both `ARGOS_CLIENT_CERT` and `ARGOS_CLIENT_KEY` are set and the files are mounted.

### No data appears in the UI

- Wait for at least one polling interval (default 5 minutes).
- Check collector logs for upsert errors.
- Verify `ARGOS_VM_COLLECTOR_ACCOUNT_NAME` is correct — a typo creates a different placeholder account.
- Confirm the cloud account is `active` (not `pending_credentials` or `disabled`) in the admin UI.
- Confirm there are actually non-Kubernetes VMs in the account. If every VM is a kube worker, every one will be deduplicated and the `virtual_machines` list will legitimately be empty. Check the `argos_vm_collector_vms_skipped_kubernetes_total` metric.

## Observability

The collector exposes Prometheus metrics on `ARGOS_VM_COLLECTOR_METRICS_ADDR` (default `127.0.0.1:9090`):

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `argos_vm_collector_ticks_total` | counter | `status` (`success`, `error`) | Number of tick attempts. |
| `argos_vm_collector_tick_duration_seconds` | histogram | -- | End-to-end tick duration (fetch creds + list + push + reconcile). |
| `argos_vm_collector_vms_observed` | gauge | -- | Number of VMs returned by the cloud provider on the last tick (before pre-filter). |
| `argos_vm_collector_vms_skipped_kubernetes_total` | counter | -- | VMs dropped because they are kube nodes (sum of pre-filter + server-side 409). |
| `argos_vm_collector_credential_refreshes_total` | counter | `result` (`success`, `error`) | Credential-fetch attempts. |
| `argos_vm_collector_last_success_timestamp_seconds` | gauge | -- | Unix timestamp of the last successful tick. Use this for staleness alerts. |
| `argos_vm_collector_build_info` | gauge | `version`, `commit` | Build identification (always `1`). |

Suggested alerts:

```yaml
# Collector hasn't ticked successfully in 30 minutes
- alert: ArgosVMCollectorStale
  expr: time() - argos_vm_collector_last_success_timestamp_seconds > 1800
  for: 5m

# Repeated credential-fetch failures
- alert: ArgosVMCollectorCredentialFailures
  expr: rate(argos_vm_collector_credential_refreshes_total{result="error"}[15m]) > 0
  for: 15m
```

Argosd itself exposes complementary metrics — `argos_cloud_accounts_pending_credentials` (a non-zero value means an admin needs to set credentials), `argos_credentials_reads_total{cloud_account}`, and `argos_virtual_machines_total{cloud_account, terminated}`. See [Monitoring](monitoring.md).

## See also

- [Cloud accounts — admin guide](cloud-accounts.md) — register accounts, set credentials, master-key backup.
- [Configuration reference](configuration.md) — full env-var table for the collector and `ARGOS_SECRETS_MASTER_KEY` on argosd.
- [API reference — cloud accounts and virtual machines](api-reference.md).
- [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) — design rationale, dedup logic, alternatives considered.
- [ADR-0009](adr/adr-0009-push-collector-for-airgapped-clusters.md) — the push-collector pattern this binary mirrors.
