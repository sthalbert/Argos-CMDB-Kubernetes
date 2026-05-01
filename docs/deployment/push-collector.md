# Deploy the Push Collector (Air-Gapped Clusters)

The push collector (`longue-vue-collector`) runs inside a cluster that longue-vue cannot reach -- air-gapped environments, dedicated administration zones (ZAD), or clusters behind strict egress firewalls. It polls the local Kubernetes API and pushes observations to a remote longue-vue instance over HTTPS.

## When to use push vs. pull

| Scenario | Mode |
|----------|------|
| longue-vue can reach the target cluster's API server | **Pull** -- enable the embedded collector in longue-vue. No extra binary needed. |
| The target cluster is air-gapped or network-restricted | **Push** -- deploy `longue-vue-collector` inside the target cluster. |
| Multi-tenant: the CMDB operator cannot obtain a kubeconfig | **Push** -- the tenant deploys the collector and pushes data out. |

Both modes coexist. An environment can mix pulled and pushed clusters -- the CMDB sees no difference.

## Prerequisites

1. **longue-vue is running** and reachable over HTTPS from the air-gapped cluster (directly or through a gateway/proxy).
2. **The cluster is registered** in longue-vue:
   ```bash
   curl -sS -H "Authorization: Bearer $TOKEN" -X POST https://longue-vue.internal:8080/v1/clusters \
     -H 'Content-Type: application/json' \
     -d '{"name":"zad-prod","display_name":"ZAD Production","environment":"production"}'
   ```
   The `name` must match `LONGUE_VUE_CLUSTER_NAME` in the collector config.
3. **A PAT with `write` scope** is minted in the longue-vue admin panel:
   ```bash
   curl -sS -b /tmp/longue-vue.cookies -X POST https://longue-vue.internal:8080/v1/admin/tokens \
     -H 'Content-Type: application/json' \
     -d '{"name":"zad-prod-collector","scopes":["read","write"]}'
   ```
   Store the plaintext token securely -- it is shown only once.

## Build the image

```bash
make docker-build-collector    # tags longue-vue-collector:dev
```

This produces a minimal static binary in a distroless image (`gcr.io/distroless/static-debian12:nonroot`, UID 65532). Transfer it to the air-gapped cluster's registry as needed.

## Deploy with Helm (recommended)

Per ADR-0018, every longue-vue deployable ships with a Helm chart of its own. The collector chart lives at `charts/longue-vue-collector` -- one Helm release per source cluster.

### 1. Create the credentials Secret

The chart never templates plaintext PATs into release state. Create the Secret out-of-band:

```bash
kubectl create namespace longue-vue-collector
kubectl create secret generic longue-vue-collector-credentials \
  --namespace longue-vue-collector \
  --from-literal=LONGUE_VUE_API_TOKEN="longue_vue_pat_xxxxxxxx_yyyyyyyyyyyyyyyyyyyyyyyy"
```

### 2. Install the chart

```bash
helm install zad-prod charts/longue-vue-collector \
  --namespace longue-vue-collector \
  --set serverURL=https://longue-vue.internal:8080 \
  --set clusterName=zad-prod \
  --set tokenSecret.existingSecret=longue-vue-collector-credentials
```

The chart creates the ServiceAccount + ClusterRole (`list`-only on the eleven resource types the collector polls) + ClusterRoleBinding + Deployment automatically. Verify:

```bash
kubectl -n longue-vue-collector rollout status deployment/zad-prod-longue-vue-collector
kubectl -n longue-vue-collector logs deployment/zad-prod-longue-vue-collector --tail=50
```

### 3. (Optional) Wire mTLS to a DMZ ingest gateway

When pushing through `longue-vue-ingest-gw` (ADR-0016), point `serverURL` at the gateway and supply the mTLS material:

```bash
helm upgrade zad-prod charts/longue-vue-collector \
  --namespace longue-vue-collector \
  --reuse-values \
  --set serverURL=https://longue-vue-gw.dmz.internal:8443 \
  --set longue-vue-tls.existingSecret=longue-vue-collector-mtls \
  --set longue-vue-tls.caSecret=longue-vue-gateway-ca \
  --set longue-vue-tls.extraHeaders="X-Longue-Vue-Tenant-Id=zad-prod"
```

### 4. (Optional) Lock down egress with NetworkPolicy

```bash
helm upgrade zad-prod charts/longue-vue-collector \
  --namespace longue-vue-collector \
  --reuse-values \
  --set networkPolicy.enabled=true \
  --set 'networkPolicy.egressCIDRs={10.96.0.0/12,10.0.5.0/24}'
```

The first CIDR must include the cluster's kube-API service range; the second points at longue-vue / the DMZ gateway. When `egressCIDRs` is set, the chart scopes the TCP/443 egress rule to those CIDRs only — leaving it empty falls back to "any 443" (the safe default for new releases).

See `charts/longue-vue-collector/values.yaml` for the full surface (proxy block, alternate kubeconfig source via Secret, PodDisruptionBudget, custom node selectors / tolerations / extra env / volumes).

## Deploy with Kustomize (legacy)

The reference Kustomize manifests under `deploy/collector/` remain available for operators who need raw YAML. They are positioned as a quick-start example, not the supported production path.

### 1. Create the credentials Secret

```bash
cp deploy/collector/secret.example.yaml /tmp/longue-vue-collector-secret.yaml
```

Edit `/tmp/longue-vue-collector-secret.yaml`:

```yaml
stringData:
  LONGUE_VUE_SERVER_URL: "https://longue-vue.internal:8080"
  LONGUE_VUE_API_TOKEN: "longue_vue_pat_xxxxxxxx_yyyyyyyyyyyyyyyyyyyyyyyy"
```

Apply it:

```bash
kubectl apply -f /tmp/longue-vue-collector-secret.yaml
```

### 2. Customize the Deployment

Edit `deploy/collector/deployment.yaml` if needed:

- Set `LONGUE_VUE_CLUSTER_NAME` to match the cluster name registered in longue-vue.
- Adjust `LONGUE_VUE_COLLECTOR_INTERVAL` (default: `5m`).
- Update the `image:` field to point to your registry.

### 3. Apply

```bash
kubectl apply -k deploy/collector/
```

### 4. Verify

```bash
kubectl -n longue-vue-system logs -l app.kubernetes.io/component=push-collector -f
```

You should see log lines indicating successful upserts for nodes, namespaces, pods, workloads, services, ingresses, PVs, and PVCs. Check longue-vue:

```bash
curl -sS -H "Authorization: Bearer $TOKEN" https://longue-vue.internal:8080/v1/namespaces?cluster_name=zad-prod | jq '.items | length'
```

## Configuration

All configuration is via environment variables. See the [configuration reference](../configuration.md) for the full table.

Key variables for the push collector:

| Variable | Required | Description |
|----------|----------|-------------|
| `LONGUE_VUE_SERVER_URL` | yes | longue-vue base URL. |
| `LONGUE_VUE_API_TOKEN` | yes | PAT with `write` scope. |
| `LONGUE_VUE_CLUSTER_NAME` | yes | Name of this cluster. Auto-created if it doesn't exist (ADR-0011). |
| `LONGUE_VUE_COLLECTOR_INTERVAL` | no | Polling interval (default `5m`). |
| `LONGUE_VUE_COLLECTOR_RECONCILE` | no | Delete stale rows (default `true`). |

## Gateway and proxy support

In SecNumCloud environments, outbound traffic from an air-gapped cluster typically transits through an API gateway or forward proxy.

### HTTP(S) proxy

Set the standard environment variables:

```yaml
env:
  - name: HTTPS_PROXY
    value: "http://proxy.zad.internal:3128"
  - name: NO_PROXY
    value: "10.0.0.0/8,.zad.internal"
```

Go's `net/http` honors these automatically.

### Reverse proxy / API gateway in front of longue-vue

If a gateway (Envoy, HAProxy, Nginx) exposes longue-vue under a sub-path, include the path prefix in the server URL:

```yaml
env:
  - name: LONGUE_VUE_SERVER_URL
    value: "https://gateway.zad.internal:443/longue-vue"
```

The collector prepends this base path to every API request (`/longue-vue/v1/clusters`, etc.).

### Custom headers

Some gateways require extra headers for routing or tenant identification:

```yaml
env:
  - name: LONGUE_VUE_EXTRA_HEADERS
    value: "X-Tenant-Id=zad-prod,X-Route-Key=longue-vue"
```

### mTLS to the gateway

When the gateway requires client-certificate authentication:

```yaml
env:
  - name: LONGUE_VUE_CA_CERT
    value: "/etc/longue-vue/tls/ca.pem"
  - name: LONGUE_VUE_CLIENT_CERT
    value: "/etc/longue-vue/tls/client.crt"
  - name: LONGUE_VUE_CLIENT_KEY
    value: "/etc/longue-vue/tls/client.key"
```

Mount the certificates from a Secret:

```yaml
volumes:
  - name: tls
    secret:
      secretName: longue-vue-collector-tls
containers:
  - name: longue-vue-collector
    volumeMounts:
      - name: tls
        mountPath: /etc/longue-vue/tls
        readOnly: true
```

## RBAC

The push collector needs the same read-only Kubernetes RBAC as the pull collector. The `deploy/collector/rbac.yaml` grants `list` on:

- Core: `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims`
- Apps: `deployments`, `statefulsets`, `daemonsets`, `replicasets`
- Networking: `ingresses`

No write access to Kubernetes. The collector is read-only.

## Troubleshooting

### 401 Unauthorized

- The PAT is invalid, expired, or revoked. Check the longue-vue admin panel under Tokens.
- The `Authorization: Bearer` header is malformed. Verify `LONGUE_VUE_API_TOKEN` starts with `longue_vue_pat_`.

### 404 on upsert calls

- The cluster auto-creation failed (e.g. a name conflict or a transient error). Check collector logs for `auto-create cluster failed` messages.

### 403 Forbidden

- The PAT does not have the `write` scope. Create a new token with `scopes: ["read", "write"]`.

### Connection refused / timeout

- longue-vue is unreachable from the collector pod. Check network policies and egress rules.
- If using a proxy, verify `HTTPS_PROXY` is set correctly and the proxy allows traffic to the longue-vue host.

### TLS certificate errors

- The longue-vue (or gateway) TLS certificate is signed by a private CA. Set `LONGUE_VUE_CA_CERT` to the CA PEM file.
- For mTLS, ensure both `LONGUE_VUE_CLIENT_CERT` and `LONGUE_VUE_CLIENT_KEY` are set and the files are mounted.

### 503 from gateway

- The gateway cannot reach longue-vue. This is a gateway-side issue, not a collector issue. Check the gateway logs.
- The collector logs the full response status and body to help distinguish gateway errors from longue-vue errors.

### Data not appearing

- Wait for at least one full polling interval.
- Check collector logs for upsert errors.
- Verify `LONGUE_VUE_CLUSTER_NAME` is correct — a typo silently creates a new cluster record (ADR-0011 NEG-002).
