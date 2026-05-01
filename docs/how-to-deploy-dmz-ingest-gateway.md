# How to deploy the DMZ ingest gateway

This guide walks through deploying `longue-vue-ingest-gw` in a DMZ so that push-mode Kubernetes collectors can reach longue-vue from outside the trusted zone without exposing longue-vue to the internet.

Read ADR-0016 for the full design rationale. This guide covers the operational steps.

## Overview

The topology is:

```
Internet                    DMZ                           Trusted zone

longue-vue-collector  ──HTTPS──► Envoy/WAF ──► longue-vue-ingest-gw ──mTLS──► longue-vue :8443
(remote cluster)  (PAT)                   (allowlist +               (ingest listener)
                                           verify cache)
```

`longue-vue-ingest-gw` is a stateless reverse proxy. It enforces a hardcoded 18-route write-only allowlist, verifies every bearer token against longue-vue via a `POST /v1/auth/verify` call (with a 60 s in-memory cache), and forwards approved requests to longue-vue's mTLS-only ingest listener. longue-vue's existing public listener (`:8080`) is unchanged and never directly reachable from the DMZ.

## Prerequisites

- A Kubernetes cluster to host the gateway (the DMZ namespace).
- An existing longue-vue deployment in the trusted zone, reachable from the DMZ namespace on a new port (`:8443`).
- Envoy (or a compatible WAF/ingress) in front of the DMZ namespace to terminate public TLS and forward collector traffic.
- A CA you can use for mTLS between the gateway and longue-vue. Three options are supported:
  - **Vault PKI** — recommended for hands-off cert rotation.
  - **cert-manager** issuing a Kubernetes `kubernetes.io/tls` Secret — good if you don't run Vault.
  - **Self-signed / manual** — for dev or edge cases.
- `kubectl` configured with access to the gateway namespace.
- Helm 3.

---

## Step 1 — Enable the ingest listener on longue-vue

longue-vue starts a second mTLS-only listener when `LONGUE_VUE_INGEST_LISTEN_ADDR` is set. The existing `:8080` listener is unaffected.

You need three files:

- `tls.crt` — the server certificate longue-vue presents on the ingest port (signed by your internal CA).
- `tls.key` — the corresponding private key.
- `client-ca.crt` — the CA bundle that must have signed the gateway's mTLS client cert.

Create a Kubernetes Secret with those files:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> create secret generic longue-vue-ingest-tls \
  --from-file=tls.crt=./server.crt \
  --from-file=tls.key=./server.key \
  --from-file=client-ca.crt=./ca.crt
```

### Helm values overlay

Add the following block to your longue-vue values file and deploy:

```yaml
ingestListener:
  enabled: true
  addr: ":8443"
  tls:
    secretName: longue-vue-ingest-tls   # keys: tls.crt, tls.key, client-ca.crt
  # Optional: only accept certs with this exact Subject CN.
  # Remove or leave empty to allow any cert signed by client-ca.crt.
  clientCNAllow: "longue-vue-ingest-gw"
```

```sh
helm upgrade longue-vue charts/longue-vue -n <LONGUE_VUE_NAMESPACE> -f values.yaml
```

### Kustomize / raw env vars

If you manage longue-vue without Helm, set these environment variables and mount the cert files from the Secret above:

```yaml
env:
  - name: LONGUE_VUE_INGEST_LISTEN_ADDR
    value: ":8443"
  - name: LONGUE_VUE_INGEST_LISTEN_TLS_CERT
    value: /etc/longue-vue/ingest-tls/tls.crt
  - name: LONGUE_VUE_INGEST_LISTEN_TLS_KEY
    value: /etc/longue-vue/ingest-tls/tls.key
  - name: LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE
    value: /etc/longue-vue/ingest-tls/client-ca.crt
  # Optional: restrict accepted client CN
  - name: LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW
    value: "longue-vue-ingest-gw"
volumeMounts:
  - name: ingest-tls
    mountPath: /etc/longue-vue/ingest-tls
    readOnly: true
volumes:
  - name: ingest-tls
    secret:
      secretName: longue-vue-ingest-tls
```

Verify longue-vue started the ingest listener by checking its logs:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> logs -l app.kubernetes.io/name=longue-vue --tail=20 | grep ingest
# Expect: INFO ingest listener started addr=:8443
```

Also expose port 8443 on the longue-vue Service so the gateway pod can reach it:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> patch service longue-vue --type=json \
  -p='[{"op":"add","path":"/spec/ports/-","value":{"name":"ingest","port":8443,"targetPort":8443,"protocol":"TCP"}}]'
```

---

## Step 2 — Mint a collector PAT

The push collectors need a PAT with `write` scope.

In the longue-vue admin UI at `/ui/admin/tokens`, create a new token with role `editor` (which grants `read` + `write` scope). Copy the token value — it is shown once.

The gateway forwards this PAT as-is to longue-vue, which re-validates it with full argon2id on every request.

---

## Step 3 — Install the gateway chart

The gateway chart lives at `charts/longue-vue-ingest-gw/`. It must be deployed into the DMZ namespace — **not** into the same namespace as longue-vue.

Three TLS modes are available for the mTLS client cert the gateway presents to longue-vue. Pick the one that fits your infrastructure.

### Mode A — Vault PKI (recommended)

Prerequisites: Vault (or OpenBao) with a PKI secrets engine, and the Vault Agent injector running in the DMZ cluster.

Create a Vault role bound to the gateway's ServiceAccount before deploying. Example Vault configuration:

```sh
vault write auth/kubernetes/role/longue-vue-ingest-gw \
  bound_service_account_names=longue-vue-ingest-gw \
  bound_service_account_namespaces=<DMZ_NAMESPACE> \
  policies=longue-vue-ingest-gw-pki \
  ttl=1h

vault write pki_int/roles/longue-vue-ingest-gw \
  allowed_domains="longue-vue-ingest-gw" \
  allow_bare_domains=true \
  max_ttl=48h
```

Minimal `values.yaml`:

```yaml
upstream:
  url: "https://longue-vue-ingest.<LONGUE_VUE_NAMESPACE>.svc.cluster.local:8443"
  caBundle: |
    -----BEGIN CERTIFICATE-----
    <your internal CA PEM>
    -----END CERTIFICATE-----

listener:
  tls:
    secretName: longue-vue-ingest-gw-server-tls  # cert presented to Envoy

mtls:
  mode: vault
  vault:
    enabled: true
    address: "https://vault.example.com"
    role: "longue-vue-ingest-gw"
    pkiMount: "pki_int"
    pkiRole: "longue-vue-ingest-gw"
    certTTL: "24h"
    renewAt: 50          # renew at 50% of TTL (12 h before expiry)
```

```sh
helm install longue-vue-ingest-gw charts/longue-vue-ingest-gw \
  -n <DMZ_NAMESPACE> -f values.yaml
```

Vault Agent issues a 24 h cert, writes it to `/etc/longue-vue-ingest-gw/tls/tls.crt` and `tls.key`, and the gateway hot-reloads it without a pod restart via an `fsnotify` watcher.

### Mode B — Kubernetes Secret (cert-manager or manual)

**Option 1: cert-manager.** Create a `Certificate` resource pointing at your `ClusterIssuer`. cert-manager writes the resulting Secret:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: longue-vue-ingest-gw-mtls
  namespace: <DMZ_NAMESPACE>
spec:
  secretName: longue-vue-ingest-gw-mtls
  commonName: longue-vue-ingest-gw
  duration: 24h
  renewBefore: 12h
  issuerRef:
    name: internal-ca
    kind: ClusterIssuer
```

**Option 2: manual.** Create the Secret yourself from your cert files:

```sh
kubectl -n <DMZ_NAMESPACE> create secret tls longue-vue-ingest-gw-mtls \
  --cert=./client.crt \
  --key=./client.key
```

Minimal `values.yaml`:

```yaml
upstream:
  url: "https://longue-vue-ingest.<LONGUE_VUE_NAMESPACE>.svc.cluster.local:8443"
  caBundle: |
    -----BEGIN CERTIFICATE-----
    <your internal CA PEM>
    -----END CERTIFICATE-----

listener:
  tls:
    secretName: longue-vue-ingest-gw-server-tls

mtls:
  mode: secret
  secret:
    name: longue-vue-ingest-gw-mtls   # keys: tls.crt, tls.key
```

```sh
helm install longue-vue-ingest-gw charts/longue-vue-ingest-gw \
  -n <DMZ_NAMESPACE> -f values.yaml
```

When cert-manager rotates the Secret, Kubernetes propagates the new cert to the mounted volume automatically. The gateway picks it up via the same `fsnotify` hot-reload.

### Mode C — File mount (niche / dev)

For development environments or edge cases where neither Vault nor cert-manager is available. Mount cert files from any source (hostPath, sealed-secrets, SOPS, etc.) at the default paths.

```yaml
upstream:
  url: "https://longue-vue-ingest.<LONGUE_VUE_NAMESPACE>.svc.cluster.local:8443"
  caBundle: |
    -----BEGIN CERTIFICATE-----
    <your internal CA PEM>
    -----END CERTIFICATE-----

listener:
  tls:
    secretName: longue-vue-ingest-gw-server-tls

mtls:
  mode: file
  # Cert files must be mounted at the default paths below, or override via
  # LONGUE_VUE_INGEST_GW_CLIENT_CERT_FILE / LONGUE_VUE_INGEST_GW_CLIENT_KEY_FILE.
```

> **Note:** file mode gives you full responsibility for cert rotation. The gateway hot-reloads when files change, but nothing drives the rotation. Use this mode only for local dev or in environments with a bespoke secret-distribution mechanism.

---

## Step 4 — Wire Envoy in front of the gateway

The gateway expects to sit behind an Envoy (or WAF) that terminates the public TLS connection and forwards traffic to the gateway Service on port 8443. The gateway does **not** duplicate WAF rules — rate limiting, IP allowlisting, and request-size enforcement at the edge are Envoy's responsibility. The gateway adds the route allowlist and token verification on top.

**Important:** The gateway strips any inbound `X-Forwarded-For` header from collectors and relies on Envoy to set a fresh, accurate one. This prevents audit-log forgery — a malicious collector cannot claim to originate from a trusted IP by injecting a header. If you need the public-internet client IP in longue-vue's audit log, read it from Envoy's access log, not from longue-vue.

Example Envoy `HTTPRoute` (Gateway API) forwarding to the gateway Service:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: longue-vue-ingest
  namespace: <DMZ_NAMESPACE>
spec:
  parentRefs:
    - name: envoy-gateway
      namespace: envoy-system
  hostnames:
    - "ingest.longue-vue.example.com"
  rules:
    - backendRefs:
        - name: longue-vue-ingest-gw
          port: 8443
```

Or as a minimal Envoy cluster + listener snippet (xDS / static config):

```yaml
clusters:
  - name: longue_vue_ingest_gw
    type: STRICT_DNS
    load_assignment:
      cluster_name: longue_vue_ingest_gw
      endpoints:
        - lb_endpoints:
            - endpoint:
                address:
                  socket_address:
                    address: longue-vue-ingest-gw.<DMZ_NAMESPACE>.svc.cluster.local
                    port_value: 8443
    transport_socket:
      name: envoy.transport_sockets.tls
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext
```

Ensure Envoy sets `X-Forwarded-For` on each forwarded request (this is the default in most Envoy configurations via `use_remote_address: true`).

---

## Step 5 — Point a collector at the gateway

Configure `longue-vue-collector` (or `longue-vue-vm-collector`) with the gateway's public hostname rather than longue-vue's URL:

```sh
LONGUE_VUE_SERVER_URL=https://ingest.longue-vue.example.com/
LONGUE_VUE_API_TOKEN=<the editor PAT minted in Step 2>
LONGUE_VUE_CA_CERT=/etc/longue-vue/ca.crt   # CA that signed the gateway's server cert
```

If the gateway's server cert is signed by your internal CA (not a public CA), mount the CA bundle and set `LONGUE_VUE_CA_CERT` pointing to it:

```yaml
# Helm values overlay for longue-vue-collector
collector:
  serverUrl: "https://ingest.longue-vue.example.com/"
  apiToken: "<PAT>"
  caCert: "/etc/longue-vue/ca.crt"
  caCertSecretName: "longue-vue-gateway-ca"
```

The collector does not need to know it is talking to a gateway rather than longue-vue directly. The API surface is identical — `POST /v1/clusters` (now idempotent on `name`, returns 200 or 201) followed by write and reconcile operations.

---

## Step 6 — Verify the chain end-to-end

### Confirm a write reaches longue-vue through the gateway

```sh
curl -sS \
  --cacert ./ca.crt \
  -H "Authorization: Bearer $PAT" \
  -H "Content-Type: application/json" \
  -X POST https://ingest.longue-vue.example.com/v1/clusters \
  -d '{"name":"test-cluster","environment":"staging"}' | jq .
# Expect: HTTP 200 (existing row) or 201 (new row) with the cluster object.
```

### Check gateway metrics

```sh
# Port-forward the gateway's health/metrics port (not exposed via Envoy):
kubectl -n <DMZ_NAMESPACE> port-forward svc/longue-vue-ingest-gw 9090:9090 &

curl -s http://localhost:9090/metrics | grep longue_vue_ingest_gw_requests_total
# Expect: longue_vue_ingest_gw_requests_total{...,outcome="allowed",...} <count>
```

### Check the longue-vue audit log

```sh
curl -sS -b /tmp/longue-vue.cookies \
  'https://longue-vue.internal:8080/v1/admin/audit?action=cluster.create' \
  | jq '.items[0]'
```

Rows that came through the gateway have `"source": "ingest_gw"` in the audit event. Rows from direct trusted-zone clients have `"source": "api"`.

---

## Troubleshooting

### 503 from the gateway to collectors

The gateway cannot reach longue-vue's ingest listener. Check:

1. The longue-vue Service exposes port 8443 (`kubectl -n <LONGUE_VUE_NAMESPACE> get svc longue-vue -o yaml`).
2. A NetworkPolicy in either namespace is not blocking the gateway's egress to longue-vue port 8443.
3. longue-vue's ingest listener actually started — check longue-vue logs for `ingest listener started addr=:8443`.

```sh
kubectl -n <DMZ_NAMESPACE> exec -it <gateway-pod> -- \
  wget -qO- --no-check-certificate https://longue-vue-ingest.<LONGUE_VUE_NAMESPACE>.svc.cluster.local:8443/healthz
```

### 401 returned to collectors

The token is missing, malformed, or revoked. Check:

- The PAT is correctly set in the collector's `LONGUE_VUE_API_TOKEN`.
- The token still appears in longue-vue's admin panel at `/ui/admin/tokens` (not revoked).
- longue-vue's audit log shows the token prefix and the rejection reason.

```sh
curl -sS -b /tmp/longue-vue.cookies \
  'https://longue-vue.internal:8080/v1/admin/audit?action=auth.verify' \
  | jq '.items[:5]'
```

### mTLS handshake failure between gateway and longue-vue

Check the longue-vue-side metric for the failure reason:

```sh
curl -s https://longue-vue.internal:8080/metrics 2>/dev/null \
  | grep longue_vue_ingest_listener_client_cert_failures_total
```

Possible `reason` label values:

| Reason | Cause |
|--------|-------|
| `bad_ca` | Gateway cert not signed by the CA in `LONGUE_VUE_INGEST_LISTEN_CLIENT_CA_FILE`. |
| `expired` | Gateway cert has passed its `Not After` date. |
| `cn_not_allowed` | Gateway cert's Subject CN is not in `LONGUE_VUE_INGEST_LISTEN_CLIENT_CN_ALLOW`. Remove the env var or add the CN. |
| `none_provided` | Gateway connected without a client cert. Check that `LONGUE_VUE_INGEST_GW_CLIENT_CERT_FILE` and `LONGUE_VUE_INGEST_GW_CLIENT_KEY_FILE` are correctly mounted. |

### Cert renewal failing

Check the gateway cert-reload metric:

```sh
curl -s http://localhost:9090/metrics \
  | grep longue_vue_ingest_gw_cert_reload_total
# outcome="failure" incrementing means the watcher found a new file but failed to parse it.
```

Also check cert expiry:

```sh
curl -s http://localhost:9090/metrics \
  | grep longue_vue_ingest_gw_cert_not_after_seconds
# Compare to $(date +%s). Less than 3600 s away = certificate expires within 1 hour.
```

For Vault mode: check Vault Agent sidecar logs in the gateway pod:

```sh
kubectl -n <DMZ_NAMESPACE> logs <gateway-pod> -c vault-agent --tail=50
```

For cert-manager mode: check the `Certificate` resource status:

```sh
kubectl -n <DMZ_NAMESPACE> describe certificate longue-vue-ingest-gw-mtls
```

### Read or admin endpoints return 404 from the gateway

Expected. The gateway enforces a strict 18-route write-only allowlist. `GET /v1/clusters`, admin endpoints, audit endpoints, and any other read paths are all `404` at the gateway by design — they are only reachable on longue-vue's `:8080` listener from inside the trusted zone.

---

## Security notes

- The gateway is **not** an auth authority. longue-vue re-validates every forwarded bearer token with full argon2id on every request. The gateway's 60 s verify cache exists only to reduce verify-call cardinality — it does not change longue-vue's authorization decisions.
- Token revocation propagates within 60 s worst case. For immediate revocation, delete the token in longue-vue's admin UI; longue-vue will return 401 on the next forwarded request and the gateway will evict the cache entry.
- The gateway never buffers or spools requests. If longue-vue is unreachable, collectors receive 503 and retry with backoff. No inventory data is lost — the next successful collector tick reconciles the gap.
- The ingest listener on longue-vue registers only 19 routes (the 18 allowed writes + `POST /v1/auth/verify`). Any other path returns 404 on this listener even if the route exists on `:8080`.
