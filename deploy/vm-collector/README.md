# argos-vm-collector deployment

Reference Kustomize manifests for running [`argos-vm-collector`](../../cmd/argos-vm-collector) (ADR-0015).

The VM collector is a standalone binary that polls a cloud-provider API for VMs that are **not** part of any Kubernetes cluster (VPN gateway, DNS, Bastion, Vault, etc.) and pushes them to argosd over HTTPS.

One deployment per cloud account. To inventory three Outscale accounts, deploy three vm-collector pods, each with its own ConfigMap, Secret, and PAT.

## Topology

```
┌─────────────┐  HTTPS + Bearer PAT (vm-collector scope)  ┌─────────────┐
│ argos-vm-   │ ────────────────────────────────────────▶ │   argosd    │
│ collector   │                                           │  (REST API) │
│             │                                           │             │
│ Holds:      │  Cloud-provider API (e.g. Outscale)       │ Stores:     │
│ - PAT       │ ◀────────────────────────────────────────  │ - cloud_   │
│ - cached    │                                            │   accounts │
│   AK/SK     │                                            │ - virtual_ │
│ Reports:    │                                            │   machines │
│ - VMs       │                                            │ Encrypted: │
│ - status    │                                            │ - SK at    │
└─────────────┘                                            │   rest     │
                                                           └─────────────┘
```

## Prerequisites

1. **argosd** is deployed and reachable from the collector. argosd has `ARGOS_SECRETS_MASTER_KEY` configured (it MUST, otherwise the credentials-fetch endpoint returns 503).
2. **Admin pre-registers the cloud account** in the argosd UI:
   - Go to **Admin > Cloud Accounts > Add account**.
   - Pick the provider (e.g. `outscale`), enter a name (e.g. `acme-prod`) and region (e.g. `eu-west-2`).
   - Optionally fill in AK and SK now. If you skip them, the row is created with `status=pending_credentials` and the collector will wait until you fill them in.
3. **Admin issues a vm-collector PAT** bound to that account:
   - On the same Cloud Accounts detail page, click **Issue collector token**.
   - Give it a name (e.g. `prod-vm-collector-pod`).
   - The plaintext token is shown **exactly once**. Copy it into `secret.yaml` (see below).

## Deploy

```sh
# 1. Copy the secret template and paste your PAT into it.
cp deploy/vm-collector/secret.example.yaml deploy/vm-collector/secret.yaml
$EDITOR deploy/vm-collector/secret.yaml

# 2. Edit configmap.yaml to point at your argosd instance and your account.
$EDITOR deploy/vm-collector/configmap.yaml

# 3. Apply.
kubectl apply -f deploy/vm-collector/secret.yaml
kubectl apply -k deploy/vm-collector/
```

The collector will:

1. Boot, fetch credentials from argosd via `GET /v1/cloud-accounts/by-name/{name}/credentials`.
2. If the account is in `pending_credentials`, log a warning and retry on the next interval.
3. Once credentials are available, poll the cloud-provider API every `ARGOS_VM_COLLECTOR_INTERVAL` (default 5 minutes).
4. Upsert each non-Kubernetes VM via `POST /v1/virtual-machines`.
5. Reconcile (soft-delete) VMs that disappeared from the listing.
6. Update the cloud_account heartbeat (`last_seen_at`) on each successful tick.

## Network requirements

The pod needs egress to:

- argosd's HTTPS endpoint (cluster-internal Service or external URL via a gateway).
- The cloud-provider API endpoint (e.g. `api.eu-west-2.outscale.com:443`).

The shipped `networkpolicy.yaml` allows TCP 443 to any destination since Kubernetes NetworkPolicy cannot restrict by FQDN. Tighten with Cilium / a service mesh in production environments where outbound traffic is locked down.

## Gateway / proxy support

The collector is gateway-transparent (mirrors ADR-0009 §7):

- `ARGOS_SERVER_URL` accepts a path prefix (e.g. `https://gw.internal/argos`).
- `ARGOS_CA_CERT` for custom server-side CA.
- `ARGOS_CLIENT_CERT` + `ARGOS_CLIENT_KEY` for mTLS to the gateway.
- `ARGOS_EXTRA_HEADERS=X-Tenant-Id=zad-prod,X-Route-Key=argos` for header-based gateway routing.
- `HTTPS_PROXY` / `HTTP_PROXY` / `NO_PROXY` (Go's standard env vars).

Add these to the ConfigMap or Secret as appropriate.

## Observability

`/metrics` is exposed on `127.0.0.1:9090` inside the pod (localhost only by default). The deployment manifests don't ship a `Service` — Prometheus operators typically scrape via a sidecar or the `prometheus.io/scrape` annotation pattern; adapt to your monitoring stack.

Set `ARGOS_VM_COLLECTOR_METRICS_ADDR=""` in the ConfigMap to disable the listener entirely.

## References

- [ADR-0015](../../docs/adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) — design & rationale
- [ADR-0009](../../docs/adr/adr-0009-push-collector-for-airgapped-clusters.md) — sibling pattern for the kube push collector
