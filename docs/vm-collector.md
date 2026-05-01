# Deploy the VM Collector

`longue-vue-vm-collector` is a standalone push-mode binary that catalogues platform VMs running **outside** any Kubernetes cluster — VPN gateways, DNS servers, bastions, Vault clusters, build runners. It polls a cloud provider's management API, deduplicates against the kube-node inventory, and pushes the rest to longue-vue over HTTPS.

This guide covers what the binary is, how to configure it, three deployment recipes (Kubernetes, bare VM with systemd, `docker run`), gateway and proxy setup, troubleshooting, and the Prometheus metrics it exposes.

For background on why this is a separate binary (and not a goroutine inside longue-vue), see [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md).

## What it is

A small static Go binary, distroless base image, UID 65532, no listening port except `/metrics`. One instance per cloud account. Stateless — no database, no on-disk state, no kubeconfig.

It does three things on a loop:

1. Fetches the cloud-provider AK/SK from longue-vue over HTTPS (no creds in env vars).
2. Lists VMs from the cloud provider's API and pre-filters anything tagged as a kube node.
3. Pushes each remaining VM to `POST /v1/virtual-machines` and reconciles the rest with `POST /v1/virtual-machines/reconcile`.

longue-vue performs the canonical dedup against the `nodes` table server-side: any VM whose provider ID matches an existing kube node returns `409 Conflict` and the collector silently skips it. The pre-filter is just optimisation.

## Prerequisites

1. **longue-vue is running** and reachable over HTTPS (directly or through a gateway).
2. **A `vm-collector` PAT bound to the target account** has been issued. See [Issue a collector token](cloud-accounts.md#issue-a-collector-token).
3. **The cloud account exists in longue-vue** — either pre-registered with credentials, or you are using the [hybrid onboarding flow](cloud-accounts.md#option-b--collector-first-hybrid) where the collector creates the placeholder.
4. **`LONGUE_VUE_SECRETS_MASTER_KEY` is set on longue-vue** so credentials can be encrypted at rest. See [master-key backup](cloud-accounts.md#master-key-backup).

## Configuration

All configuration is via environment variables.

### Connection

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGUE_VUE_SERVER_URL` | yes | -- | longue-vue base URL. Example: `https://longue-vue.internal:8080`. Supports a path prefix for gateway deployments: `https://gateway:443/longue-vue`. |
| `LONGUE_VUE_API_TOKEN` | yes | -- | Bearer PAT with `vm-collector` scope, bound to the target cloud account. Format: `longue_vue_pat_<prefix>_<suffix>`. |

### Cloud account identity

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGUE_VUE_VM_COLLECTOR_PROVIDER` | no | `outscale` | Cloud provider. Only `outscale` is supported in v1. |
| `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME` | yes | -- | Cloud account name. Must match `cloud_accounts.name` in longue-vue. The collector self-registers a placeholder if the account does not yet exist. |
| `LONGUE_VUE_VM_COLLECTOR_REGION` | yes | -- | Cloud region. Example: `eu-west-2`. |
| `LONGUE_VUE_VM_COLLECTOR_PROVIDER_ENDPOINT_URL` | no | provider default | Override the cloud-provider API endpoint. Useful for sovereign-cloud regions or in-network mirrors. |

### Behaviour

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGUE_VUE_VM_COLLECTOR_INTERVAL` | no | `5m` | Time between polling ticks. Accepts Go duration syntax (`30s`, `5m`, `1h`). |
| `LONGUE_VUE_VM_COLLECTOR_FETCH_TIMEOUT` | no | `30s` | Per-tick timeout for cloud-provider API calls. |
| `LONGUE_VUE_VM_COLLECTOR_RECONCILE` | no | `true` | Call `POST /v1/virtual-machines/reconcile` after each tick to soft-delete VMs that disappeared. |
| `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH` | no | `1h` | How often to re-fetch credentials from longue-vue. Lower this if you rotate AK/SK frequently. |
| `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR` | no | `127.0.0.1:9090` | Listen address for the `/metrics` endpoint. **Set to `0.0.0.0:9090` when running in Kubernetes** so the kubelet can reach the liveness probe. |

### TLS and gateway

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGUE_VUE_CA_CERT` | no | system CA pool | Path to a PEM-encoded CA certificate for verifying the longue-vue (or gateway) TLS certificate. Required when longue-vue uses a private CA. |
| `LONGUE_VUE_CLIENT_CERT` | no | -- | Path to a PEM-encoded client certificate for mTLS to a gateway. |
| `LONGUE_VUE_CLIENT_KEY` | no | -- | Path to a PEM-encoded client private key for mTLS. Required when `LONGUE_VUE_CLIENT_CERT` is set. |
| `LONGUE_VUE_EXTRA_HEADERS` | no | -- | Comma-separated `key=value` pairs injected into every outbound HTTP request to longue-vue. Useful for gateway routing or tenant identification. Example: `X-Tenant-Id=acme-prod,X-Route-Key=longue-vue`. |

### Proxy

The collector honours the standard Go proxy environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `HTTPS_PROXY` | no | -- | Forward proxy URL for HTTPS traffic. |
| `HTTP_PROXY` | no | -- | Forward proxy URL for HTTP traffic. |
| `NO_PROXY` | no | -- | Comma-separated list of hosts/CIDRs to bypass the proxy. |

### What is *not* configurable

There is **no `LONGUE_VUE_AK` or `LONGUE_VUE_SK` env var**. AK/SK live exclusively in longue-vue's `cloud_accounts` table and are fetched at runtime. This is deliberate — see [ADR-0015 §4](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) for the rationale.

## Deployment recipes

### Kubernetes (Helm — recommended)

Per ADR-0018, the VM collector ships with a first-class Helm chart at `charts/longue-vue-vm-collector`. One Helm release per cloud account.

```sh
# 1. Mint the collector PAT in the longue-vue UI (Admin > Cloud Accounts > Issue collector token).
# 2. Stash it in a Kubernetes Secret out-of-band — the chart never templates plaintext PATs.
kubectl create namespace longue-vue-vm-collector
kubectl -n longue-vue-vm-collector create secret generic longue-vue-vm-collector-credentials \
  --from-literal=LONGUE_VUE_API_TOKEN='longue_vue_pat_3f9c1e7a_5N2pKdQ...'

# 3. Install the chart.
helm install acme-prod charts/longue-vue-vm-collector \
  --namespace longue-vue-vm-collector \
  --set serverURL=https://longue-vue.internal:8080 \
  --set account.name=acme-prod \
  --set account.region=eu-west-2 \
  --set tokenSecret.existingSecret=longue-vue-vm-collector-credentials

# 4. Watch the logs.
kubectl -n longue-vue-vm-collector rollout status deployment/acme-prod-longue-vue-vm-collector
kubectl -n longue-vue-vm-collector logs deployment/acme-prod-longue-vue-vm-collector -f
```

The chart wires the standard hardening defaults (UID 65532, `runAsNonRoot:true`, `readOnlyRootFilesystem`, drop ALL capabilities, seccomp `RuntimeDefault`) and disables `automountServiceAccountToken` — the collector never calls the Kubernetes API. `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR` is automatically set to `0.0.0.0:9090` so the metrics Service / ServiceMonitor can scrape it.

Multi-account = N releases — one per cloud account. The chart is intentionally scoped narrow.

#### Optional: ServiceMonitor for Prometheus Operator

```sh
helm upgrade acme-prod charts/longue-vue-vm-collector \
  --namespace longue-vue-vm-collector \
  --reuse-values \
  --set metrics.serviceMonitor.enabled=true
```

#### Optional: mTLS to a DMZ ingest gateway

```sh
helm upgrade acme-prod charts/longue-vue-vm-collector \
  --namespace longue-vue-vm-collector \
  --reuse-values \
  --set serverURL=https://longue-vue-gw.dmz.internal:8443 \
  --set longue-vue-tls.existingSecret=longue-vue-vm-collector-mtls \
  --set longue-vue-tls.caSecret=longue-vue-gateway-ca \
  --set longue-vue-tls.extraHeaders="X-Longue-Vue-Tenant-Id=acme-prod"
```

#### Optional: NetworkPolicy lockdown

```sh
helm upgrade acme-prod charts/longue-vue-vm-collector \
  --namespace longue-vue-vm-collector \
  --reuse-values \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.egressCIDRs={10.0.5.0/24,52.47.0.0/16}'
```

The first CIDR points at longue-vue / the DMZ gateway, the second at the cloud-provider API. When `egressCIDRs` is empty the policy falls back to "any 443" (safe default for new releases).

See `charts/longue-vue-vm-collector/values.yaml` for the full surface.

### Kubernetes (Kustomize — legacy)

Reference manifests live in `deploy/vm-collector/`. Positioned as a quick-start example, not the supported production path — Helm is the recommended deployment surface (ADR-0018).

```sh
# 1. Mint the collector PAT in the longue-vue UI (Admin > Cloud Accounts > Issue collector token).
# 2. Stash it in a Kubernetes Secret.
kubectl -n longue-vue-system create secret generic longue-vue-vm-collector-token \
  --from-literal=LONGUE_VUE_API_TOKEN='longue_vue_pat_3f9c1e7a_5N2pKdQ...'

# 3. Edit deploy/vm-collector/configmap.yaml — set LONGUE_VUE_SERVER_URL, LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME, LONGUE_VUE_VM_COLLECTOR_REGION.
# 4. Apply.
kubectl apply -k deploy/vm-collector/

# 5. Watch the logs.
kubectl -n longue-vue-system logs -l app.kubernetes.io/component=vm-collector -f
```

The Kustomize bundle ships:

- `Deployment` (replicas: 1) running `longue-vue-vm-collector`.
- `Secret` carrying `LONGUE_VUE_API_TOKEN`.
- `ConfigMap` for non-secret env vars.
- Egress `NetworkPolicy` allowing HTTPS to the longue-vue endpoint and the cloud-provider API only.

> **Important:** when running in Kubernetes, set `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR=0.0.0.0:9090` so the kubelet can reach the liveness probe. The default (`127.0.0.1:9090`) is fine for systemd / docker but rejects external scrapes.

### Bare VM with systemd

For deployments where Kubernetes itself is one of the VMs being catalogued, run the collector on a small management VM:

```ini
# /etc/systemd/system/longue-vue-vm-collector.service
[Unit]
Description=longue-vue VM Collector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=longue-vue
Group=longue-vue
ExecStart=/usr/local/bin/longue-vue-vm-collector
Restart=on-failure
RestartSec=10s

# Environment
Environment="LONGUE_VUE_SERVER_URL=https://longue-vue.internal:8080"
EnvironmentFile=/etc/longue-vue/vm-collector.env
Environment="LONGUE_VUE_VM_COLLECTOR_PROVIDER=outscale"
Environment="LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME=acme-prod"
Environment="LONGUE_VUE_VM_COLLECTOR_REGION=eu-west-2"
Environment="LONGUE_VUE_VM_COLLECTOR_INTERVAL=5m"

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
sudo install -m 0600 -o root -g root /dev/null /etc/longue-vue/vm-collector.env
sudo tee /etc/longue-vue/vm-collector.env >/dev/null <<'EOF'
LONGUE_VUE_API_TOKEN=longue_vue_pat_3f9c1e7a_5N2pKdQ...
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now longue-vue-vm-collector.service
sudo journalctl -u longue-vue-vm-collector -f
```

### docker run

Quick smoke test or one-off catalogue from a workstation:

```sh
docker run --rm \
  -e LONGUE_VUE_SERVER_URL=https://longue-vue.internal:8080 \
  -e LONGUE_VUE_API_TOKEN=longue_vue_pat_3f9c1e7a_5N2pKdQ... \
  -e LONGUE_VUE_VM_COLLECTOR_PROVIDER=outscale \
  -e LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME=acme-prod \
  -e LONGUE_VUE_VM_COLLECTOR_REGION=eu-west-2 \
  -e LONGUE_VUE_VM_COLLECTOR_INTERVAL=1m \
  -p 9090:9090 \
  -e LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR=0.0.0.0:9090 \
  longue-vue-vm-collector:dev
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

Go's `net/http` honours these automatically. The proxy applies to **both** the longue-vue connection and the cloud-provider API connection — make sure both endpoints are reachable through it (or list the cloud-provider endpoint in `NO_PROXY` if the proxy doesn't egress to the public internet).

### Reverse proxy / API gateway in front of longue-vue

If a gateway exposes longue-vue under a sub-path, include the prefix in the server URL:

```yaml
env:
  - name: LONGUE_VUE_SERVER_URL
    value: "https://gateway.zad.internal:443/longue-vue"
```

The collector prepends this base path to every API request (`/longue-vue/v1/cloud-accounts/...`, `/longue-vue/v1/virtual-machines`, etc.).

### Custom headers

```yaml
env:
  - name: LONGUE_VUE_EXTRA_HEADERS
    value: "X-Tenant-Id=acme-prod,X-Route-Key=longue-vue"
```

### mTLS to the gateway

```yaml
env:
  - name: LONGUE_VUE_CA_CERT
    value: "/etc/longue-vue/tls/ca.pem"
  - name: LONGUE_VUE_CLIENT_CERT
    value: "/etc/longue-vue/tls/client.crt"
  - name: LONGUE_VUE_CLIENT_KEY
    value: "/etc/longue-vue/tls/client.key"
volumes:
  - name: tls
    secret:
      secretName: longue-vue-vm-collector-tls
volumeMounts:
  - name: tls
    mountPath: /etc/longue-vue/tls
    readOnly: true
```

The mTLS material applies only to the longue-vue connection. Cloud-provider authentication uses AK/SK fetched at runtime — there is no separate cloud-side TLS material to manage.

## Troubleshooting

### `credentials unavailable` / `cloud_account_not_registered`

The admin has not yet entered AK/SK for this account. Open **Admin > Cloud Accounts**, find the row in `pending_credentials` status, and set credentials. The collector retries every credential-refresh interval (default 1 hour) — if you don't want to wait, restart the collector after entering credentials.

If the account does not exist at all, verify `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME` matches the registered name exactly (case-sensitive). The collector should self-register a placeholder on first contact; if it doesn't, check the PAT scope and account binding.

### `outscale ReadVms 422` (or similar provider-side error)

Usually means the AK/SK or region is wrong. Verify in the cloud-provider console that:

- The AK/SK pair is active and not rotated.
- The user behind the AK has permission to call `ReadVms` (or the equivalent on other providers).
- `LONGUE_VUE_VM_COLLECTOR_REGION` matches a region the AK is authorised for.

Rotate the credentials in **Admin > Cloud Accounts > Rotate credentials** if the issue is on the AK/SK side.

### `token bound to a different cloud account`

The PAT was issued for cloud account A but the collector is configured with `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME=B`. Each `vm-collector` PAT is bound to exactly one account at issuance — there is no way to retarget it.

Mint a new PAT bound to account B (see [Issue a collector token](cloud-accounts.md#issue-a-collector-token)) and update `LONGUE_VUE_API_TOKEN`.

### `409 Conflict` / `already_inventoried_as_kubernetes_node`

This is **not an error** — it is the deduplication flow. The VM with that ID is already in the `nodes` table because a Kubernetes collector inventoried it. The vm-collector logs `vm i-96fff41b is a kube node, skipping` and continues. The pre-filter (Outscale CCM tags `OscK8sClusterID/*`, `OscK8sNodeName=*`) catches most of these locally; the server-side check is the safety net.

### Liveness probe loop / metrics not reachable

The default `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR` is `127.0.0.1:9090` — fine for systemd and `docker run`, but the kubelet cannot reach `127.0.0.1:9090` inside the pod's network namespace.

When deploying in Kubernetes, set:

```yaml
env:
  - name: LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR
    value: "0.0.0.0:9090"
```

The reference Kustomize manifest (`deploy/vm-collector/configmap.yaml`) sets this by default; it only bites if you fork the manifests.

### `401 Unauthorized` from longue-vue

- The PAT is invalid, expired, or revoked. Check **Admin > Tokens** in the longue-vue UI.
- The `Authorization: Bearer` header is malformed. Verify `LONGUE_VUE_API_TOKEN` starts with `longue_vue_pat_`.

### `403 Forbidden` from `GET /credentials`

- The cloud account is `disabled`. Re-enable it from **Admin > Cloud Accounts** if appropriate.
- The PAT does not have the `vm-collector` scope, or it is bound to a different account. Mint a fresh PAT.

### TLS certificate errors

- The longue-vue (or gateway) TLS certificate is signed by a private CA. Set `LONGUE_VUE_CA_CERT` to the CA PEM file.
- For mTLS, ensure both `LONGUE_VUE_CLIENT_CERT` and `LONGUE_VUE_CLIENT_KEY` are set and the files are mounted.

### No data appears in the UI

- Wait for at least one polling interval (default 5 minutes).
- Check collector logs for upsert errors.
- Verify `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME` is correct — a typo creates a different placeholder account.
- Confirm the cloud account is `active` (not `pending_credentials` or `disabled`) in the admin UI.
- Confirm there are actually non-Kubernetes VMs in the account. If every VM is a kube worker, every one will be deduplicated and the `virtual_machines` list will legitimately be empty. Check the `longue_vue_vm_collector_vms_skipped_kubernetes_total` metric.

## Observability

The collector exposes Prometheus metrics on `LONGUE_VUE_VM_COLLECTOR_METRICS_ADDR` (default `127.0.0.1:9090`):

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `longue_vue_vm_collector_ticks_total` | counter | `status` (`success`, `error`) | Number of tick attempts. |
| `longue_vue_vm_collector_tick_duration_seconds` | histogram | -- | End-to-end tick duration (fetch creds + list + push + reconcile). |
| `longue_vue_vm_collector_vms_observed` | gauge | -- | Number of VMs returned by the cloud provider on the last tick (before pre-filter). |
| `longue_vue_vm_collector_vms_skipped_kubernetes_total` | counter | -- | VMs dropped because they are kube nodes (sum of pre-filter + server-side 409). |
| `longue_vue_vm_collector_credential_refreshes_total` | counter | `result` (`success`, `error`) | Credential-fetch attempts. |
| `longue_vue_vm_collector_last_success_timestamp_seconds` | gauge | -- | Unix timestamp of the last successful tick. Use this for staleness alerts. |
| `longue_vue_vm_collector_build_info` | gauge | `version`, `commit` | Build identification (always `1`). |

Suggested alerts:

```yaml
# Collector hasn't ticked successfully in 30 minutes
- alert: LongueVueVMCollectorStale
  expr: time() - longue_vue_vm_collector_last_success_timestamp_seconds > 1800
  for: 5m

# Repeated credential-fetch failures
- alert: LongueVueVMCollectorCredentialFailures
  expr: rate(longue_vue_vm_collector_credential_refreshes_total{result="error"}[15m]) > 0
  for: 15m
```

longue-vue itself exposes complementary metrics — `longue_vue_cloud_accounts_pending_credentials` (a non-zero value means an admin needs to set credentials), `longue_vue_credentials_reads_total{cloud_account}`, and `longue_vue_virtual_machines_total{cloud_account, terminated}`. See [Monitoring](monitoring.md).

## See also

- [Cloud accounts — admin guide](cloud-accounts.md) — register accounts, set credentials, master-key backup.
- [Configuration reference](configuration.md) — full env-var table for the collector and `LONGUE_VUE_SECRETS_MASTER_KEY` on longue-vue.
- [API reference — cloud accounts and virtual machines](api-reference.md).
- [ADR-0015](adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) — design rationale, dedup logic, alternatives considered.
- [ADR-0009](adr/adr-0009-push-collector-for-airgapped-clusters.md) — the push-collector pattern this binary mirrors.
