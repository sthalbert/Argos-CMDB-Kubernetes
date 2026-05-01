#!/usr/bin/env bash
# Seed the longue-vue CMDB with a realistic-looking multi-cluster inventory for
# demo / screenshot purposes. Idempotent-ish: re-running after the fact
# will hit 409 Conflict on every POST, which is fine — the point is to
# populate an empty DB once.
#
# Per ADR-0007, longue-vue no longer accepts env-var bearer tokens. This script
# picks one of two auth paths:
#   - LONGUE_VUE_TOKEN=<lv_pat_...>  — use an admin-minted machine token.
#   - LONGUE_VUE_USER + LONGUE_VUE_PASSWORD  — log in as a human and ride the
#     session cookie. Defaults: admin / $LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD
#     if you set it on longue-vue (matches the local-dev pattern in README).
set -euo pipefail

BASE="${LONGUE_VUE_BASE:-http://localhost:8080}"
CT="Content-Type: application/json"
COOKIE_JAR="${COOKIE_JAR:-/tmp/lv-seed.cookies}"

if [ -n "${LONGUE_VUE_TOKEN:-}" ]; then
    AUTH_ARGS=(-H "Authorization: Bearer ${LONGUE_VUE_TOKEN}")
else
    # Session login.
    rm -f "$COOKIE_JAR"
    USER="${LONGUE_VUE_USER:-admin}"
    PASS="${LONGUE_VUE_PASSWORD:-${LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD:-}}"
    if [ -z "$PASS" ]; then
        echo "LONGUE_VUE_PASSWORD (or LONGUE_VUE_BOOTSTRAP_ADMIN_PASSWORD) must be set" >&2
        exit 2
    fi
    STATUS=$(curl -sS -c "$COOKIE_JAR" -o /dev/null -w '%{http_code}' \
        -X POST "${BASE}/v1/auth/login" \
        -H "$CT" \
        -d "{\"username\":\"${USER}\",\"password\":\"${PASS}\"}")
    if [ "$STATUS" != "204" ]; then
        echo "login failed with status $STATUS — is longue-vue running, and is must_change_password cleared?" >&2
        exit 2
    fi
    AUTH_ARGS=(-b "$COOKIE_JAR")
fi

post() {
    local path="$1" body="$2"
    curl -sS "${AUTH_ARGS[@]}" -X POST -H "$CT" -d "$body" "${BASE}${path}"
    echo
}

# Extract a field from the last POST response captured in $RESP.
jval() { echo "$1" | python3 -c "import sys,json; print(json.load(sys.stdin)['$2'])"; }

echo "=== clusters ==="
PROD=$(post /v1/clusters '{
  "name":"prod-eu-west-1",
  "display_name":"Production EU-West",
  "environment":"production",
  "provider":"aws",
  "region":"eu-west-1",
  "kubernetes_version":"v1.30.3",
  "api_endpoint":"https://kube.prod.example.com",
  "labels":{"tier":"prod","owner":"platform"}
}')
PROD_ID=$(jval "$PROD" id)
echo "prod id=$PROD_ID"

STAG=$(post /v1/clusters '{
  "name":"staging-eu-west-1",
  "display_name":"Staging EU-West",
  "environment":"staging",
  "provider":"aws",
  "region":"eu-west-1",
  "kubernetes_version":"v1.30.3"
}')
STAG_ID=$(jval "$STAG" id)
echo "stag id=$STAG_ID"

echo "=== nodes ==="
# Helper: build a full enriched Node payload. Assigns one zone /
# instance type per worker so the UI cartography looks lived-in.
post_node() {
    local cluster_id="$1"
    local name="$2"
    local display_name="$3"
    local role="$4"
    local inst="$5"
    local zone="$6"
    local ip_suffix="$7"
    local ready="$8"
    local unschedulable="$9"
    local taints_json="${10}"
    post /v1/nodes "{
      \"cluster_id\":\"$cluster_id\",
      \"name\":\"$name\",
      \"display_name\":\"$display_name\",
      \"role\":\"$role\",
      \"kubelet_version\":\"v1.30.3\",
      \"kube_proxy_version\":\"v1.30.3\",
      \"container_runtime_version\":\"containerd://1.7.13\",
      \"os_image\":\"Bottlerocket OS 1.20.0\",
      \"operating_system\":\"linux\",
      \"kernel_version\":\"6.1.84\",
      \"architecture\":\"amd64\",
      \"internal_ip\":\"10.0.1.$ip_suffix\",
      \"pod_cidr\":\"10.244.$ip_suffix.0/24\",
      \"provider_id\":\"aws:///$zone/i-0abc12345$ip_suffix\",
      \"instance_type\":\"$inst\",
      \"zone\":\"$zone\",
      \"capacity_cpu\":\"4\",
      \"capacity_memory\":\"16Gi\",
      \"capacity_pods\":\"110\",
      \"capacity_ephemeral_storage\":\"100Gi\",
      \"allocatable_cpu\":\"3900m\",
      \"allocatable_memory\":\"15Gi\",
      \"allocatable_pods\":\"110\",
      \"allocatable_ephemeral_storage\":\"95Gi\",
      \"ready\":$ready,
      \"unschedulable\":$unschedulable,
      \"conditions\":[
        {\"type\":\"Ready\",\"status\":\"$( [ "$ready" = "true" ] && echo True || echo False )\",\"reason\":\"KubeletReady\",\"message\":\"kubelet is posting ready status\"},
        {\"type\":\"MemoryPressure\",\"status\":\"False\",\"reason\":\"KubeletHasSufficientMemory\"},
        {\"type\":\"DiskPressure\",\"status\":\"False\",\"reason\":\"KubeletHasNoDiskPressure\"},
        {\"type\":\"PIDPressure\",\"status\":\"False\",\"reason\":\"KubeletHasSufficientPID\"},
        {\"type\":\"NetworkUnavailable\",\"status\":\"False\",\"reason\":\"RouteCreated\"}
      ],
      \"taints\":$taints_json,
      \"labels\":{
        \"node.kubernetes.io/instance-type\":\"$inst\",
        \"topology.kubernetes.io/zone\":\"$zone\",
        \"topology.kubernetes.io/region\":\"eu-west-1\"
      }
    }" >/dev/null
}

# Prod: control plane + 3 workers spread across zones. worker-03 is
# cordoned (unschedulable=true) so the UI shows the "Ready · Cordoned"
# state; worker-02 carries a dedicated=gpu taint so it illustrates the
# scheduling-hint view.
post_node "$PROD_ID" "cp-01.prod"     "cp-01"     "control-plane" "m6i.large"   "eu-west-1a" "10" "true"  "false" '[{"key":"node-role.kubernetes.io/control-plane","effect":"NoSchedule"}]'
post_node "$PROD_ID" "worker-01.prod" "worker-01" "worker"        "m6i.xlarge"  "eu-west-1a" "21" "true"  "false" '[]'
post_node "$PROD_ID" "worker-02.prod" "worker-02" "worker"        "g5.2xlarge"  "eu-west-1b" "22" "true"  "false" '[{"key":"dedicated","value":"gpu","effect":"NoSchedule"}]'
post_node "$PROD_ID" "worker-03.prod" "worker-03" "worker"        "m6i.xlarge"  "eu-west-1c" "23" "true"  "true"  '[]'

# Staging: a single, simpler worker.
post_node "$STAG_ID" "worker-01.staging" "worker-01" "worker"     "t3.medium"   "eu-west-1a" "11" "true"  "false" '[]'
echo "nodes seeded"

echo "=== namespaces ==="
make_ns() {
    local cid="$1" name="$2"
    post /v1/namespaces "{\"cluster_id\":\"$cid\",\"name\":\"$name\",\"phase\":\"Active\"}"
}
KUBE_SYSTEM_PROD=$(jval "$(make_ns "$PROD_ID" kube-system)" id)
PLATFORM_PROD=$(jval "$(make_ns "$PROD_ID" platform)" id)
SHOP_PROD=$(jval "$(make_ns "$PROD_ID" shop)" id)
MONITORING_PROD=$(jval "$(make_ns "$PROD_ID" monitoring)" id)
make_ns "$STAG_ID" kube-system >/dev/null
SHOP_STAG=$(jval "$(make_ns "$STAG_ID" shop)" id)
echo "namespaces seeded"

echo "=== workloads ==="
make_wl() {
    local nsid="$1" kind="$2" name="$3" replicas="$4" image="$5"
    post /v1/workloads "{
      \"namespace_id\":\"$nsid\",
      \"kind\":\"$kind\",
      \"name\":\"$name\",
      \"replicas\":$replicas,
      \"ready_replicas\":$replicas,
      \"containers\":[{\"name\":\"$name\",\"image\":\"$image\",\"init\":false}],
      \"labels\":{\"app\":\"$name\"}
    }"
}
WEB_PROD=$(jval "$(make_wl "$SHOP_PROD" Deployment web 3 "nginx:1.27-alpine")" id)
API_PROD=$(jval "$(make_wl "$SHOP_PROD" Deployment api 2 "registry.example.com/shop/api:1.4.2")" id)
DB_PROD=$(jval "$(make_wl "$SHOP_PROD" StatefulSet postgres 1 "postgres:16-alpine")" id)
FLUENT_PROD=$(jval "$(make_wl "$KUBE_SYSTEM_PROD" DaemonSet fluent-bit 3 "fluent/fluent-bit:3.1")" id)
PROM_PROD=$(jval "$(make_wl "$MONITORING_PROD" StatefulSet prometheus 1 "prom/prometheus:v2.54.0")" id)
GRAFANA_PROD=$(jval "$(make_wl "$MONITORING_PROD" Deployment grafana 1 "grafana/grafana:11.1.4")" id)
CERT_PROD=$(jval "$(make_wl "$PLATFORM_PROD" Deployment cert-manager 1 "quay.io/jetstack/cert-manager-controller:v1.15.1")" id)
WEB_STAG=$(jval "$(make_wl "$SHOP_STAG" Deployment web 1 "nginx:1.27-alpine")" id)
echo "workloads seeded"

echo "=== pods ==="
make_pod() {
    local nsid="$1" name="$2" phase="$3" node="$4" ip="$5" wlid="$6" image="$7"
    post /v1/pods "{
      \"namespace_id\":\"$nsid\",
      \"name\":\"$name\",
      \"phase\":\"$phase\",
      \"node_name\":\"$node\",
      \"pod_ip\":\"$ip\",
      \"workload_id\":\"$wlid\",
      \"containers\":[{\"name\":\"app\",\"image\":\"$image\",\"image_id\":\"$image@sha256:$(printf '%064x' $RANDOM)\",\"init\":false}]
    }" >/dev/null
}
make_pod "$SHOP_PROD"  "web-7d4cb-abcde"        Running "worker-01.prod" "10.244.0.11" "$WEB_PROD"  "nginx:1.27-alpine"
make_pod "$SHOP_PROD"  "web-7d4cb-fghij"        Running "worker-02.prod" "10.244.1.21" "$WEB_PROD"  "nginx:1.27-alpine"
make_pod "$SHOP_PROD"  "web-7d4cb-klmno"        Running "worker-03.prod" "10.244.2.31" "$WEB_PROD"  "nginx:1.27-alpine"
make_pod "$SHOP_PROD"  "api-6f9a1-11111"        Running "worker-01.prod" "10.244.0.12" "$API_PROD"  "registry.example.com/shop/api:1.4.2"
make_pod "$SHOP_PROD"  "api-6f9a1-22222"        Running "worker-02.prod" "10.244.1.22" "$API_PROD"  "registry.example.com/shop/api:1.4.2"
make_pod "$SHOP_PROD"  "postgres-0"             Running "worker-03.prod" "10.244.2.32" "$DB_PROD"   "postgres:16-alpine"
make_pod "$KUBE_SYSTEM_PROD" "fluent-bit-abc12" Running "worker-01.prod" "10.244.0.5"  "$FLUENT_PROD" "fluent/fluent-bit:3.1"
make_pod "$KUBE_SYSTEM_PROD" "fluent-bit-def34" Running "worker-02.prod" "10.244.1.5"  "$FLUENT_PROD" "fluent/fluent-bit:3.1"
make_pod "$KUBE_SYSTEM_PROD" "fluent-bit-ghi56" Running "worker-03.prod" "10.244.2.5"  "$FLUENT_PROD" "fluent/fluent-bit:3.1"
make_pod "$MONITORING_PROD"  "prometheus-0"     Running "worker-01.prod" "10.244.0.41" "$PROM_PROD"    "prom/prometheus:v2.54.0"
make_pod "$MONITORING_PROD"  "grafana-5c8-xyz1" Running "worker-02.prod" "10.244.1.41" "$GRAFANA_PROD" "grafana/grafana:11.1.4"
make_pod "$PLATFORM_PROD"    "cert-manager-8b9-aa" Running "worker-01.prod" "10.244.0.51" "$CERT_PROD" "quay.io/jetstack/cert-manager-controller:v1.15.1"
make_pod "$SHOP_STAG" "web-2a3b4-99999" Running "worker-01.staging" "10.244.0.11" "$WEB_STAG" "nginx:1.27-alpine"
echo "pods seeded"

echo "=== services ==="
make_svc() {
    local nsid="$1" name="$2" type="$3" cip="$4" lb_json="${5:-[]}"
    post /v1/services "{
      \"namespace_id\":\"$nsid\",
      \"name\":\"$name\",
      \"type\":\"$type\",
      \"cluster_ip\":\"$cip\",
      \"ports\":[{\"name\":\"http\",\"port\":80,\"protocol\":\"TCP\",\"target_port\":\"8080\"}],
      \"load_balancer\":$lb_json
    }" >/dev/null
}
# The ingress controller's front Service is a LoadBalancer — MetalLB
# handed it a VIP from the on-prem pool. This is the address the external
# router DNATs to, and what every Ingress in prod ultimately resolves to.
make_svc "$KUBE_SYSTEM_PROD" ingress-nginx-controller LoadBalancer 10.96.0.50 \
  '[{"ip":"192.168.10.42","ports":[{"port":80,"protocol":"TCP"},{"port":443,"protocol":"TCP"}]}]'
make_svc "$SHOP_PROD" web       ClusterIP 10.96.100.10
make_svc "$SHOP_PROD" api       ClusterIP 10.96.100.11
make_svc "$SHOP_PROD" postgres  ClusterIP 10.96.100.12
make_svc "$MONITORING_PROD" grafana NodePort 10.96.100.20
make_svc "$MONITORING_PROD" prometheus ClusterIP 10.96.100.21
make_svc "$SHOP_STAG" web       ClusterIP 10.96.100.10
echo "services seeded"

echo "=== ingresses ==="
# shop ingress has its LB address populated via MetalLB — same VIP as the
# ingress controller Service above. In the UI this shows up in the
# "Load balancer" column on the list and in a dedicated section on the
# detail page.
post /v1/ingresses "{
  \"namespace_id\":\"$SHOP_PROD\",
  \"name\":\"shop\",
  \"ingress_class_name\":\"nginx\",
  \"rules\":[
    {\"host\":\"shop.example.com\",\"paths\":[{\"path\":\"/\",\"path_type\":\"Prefix\",\"backend\":{\"service_name\":\"web\",\"service_port_number\":80}}]},
    {\"host\":\"api.shop.example.com\",\"paths\":[{\"path\":\"/\",\"path_type\":\"Prefix\",\"backend\":{\"service_name\":\"api\",\"service_port_number\":80}}]}
  ],
  \"tls\":[{\"hosts\":[\"shop.example.com\",\"api.shop.example.com\"],\"secret_name\":\"shop-tls\"}],
  \"load_balancer\":[{\"ip\":\"192.168.10.42\",\"ports\":[{\"port\":80,\"protocol\":\"TCP\"},{\"port\":443,\"protocol\":\"TCP\"}]}]
}" >/dev/null
echo "ingresses seeded"

echo "=== persistent volumes / claims ==="
PG_PV=$(post /v1/persistentvolumes "{
  \"cluster_id\":\"$PROD_ID\",
  \"name\":\"pv-postgres-data-0\",
  \"capacity\":\"50Gi\",
  \"access_modes\":[\"ReadWriteOnce\"],
  \"reclaim_policy\":\"Retain\",
  \"phase\":\"Bound\",
  \"storage_class_name\":\"gp3\",
  \"csi_driver\":\"ebs.csi.aws.com\",
  \"volume_handle\":\"vol-0abc1234567890def\",
  \"claim_ref_namespace\":\"shop\",
  \"claim_ref_name\":\"postgres-data-postgres-0\"
}")
PG_PV_ID=$(jval "$PG_PV" id)

PROM_PV=$(post /v1/persistentvolumes "{
  \"cluster_id\":\"$PROD_ID\",
  \"name\":\"pv-prometheus-data-0\",
  \"capacity\":\"100Gi\",
  \"access_modes\":[\"ReadWriteOnce\"],
  \"reclaim_policy\":\"Delete\",
  \"phase\":\"Bound\",
  \"storage_class_name\":\"gp3\",
  \"csi_driver\":\"ebs.csi.aws.com\",
  \"volume_handle\":\"vol-0ffe9876543210abc\"
}")
PROM_PV_ID=$(jval "$PROM_PV" id)

post /v1/persistentvolumeclaims "{
  \"namespace_id\":\"$SHOP_PROD\",
  \"name\":\"postgres-data-postgres-0\",
  \"phase\":\"Bound\",
  \"storage_class_name\":\"gp3\",
  \"volume_name\":\"pv-postgres-data-0\",
  \"bound_volume_id\":\"$PG_PV_ID\",
  \"access_modes\":[\"ReadWriteOnce\"],
  \"requested_storage\":\"50Gi\"
}" >/dev/null

post /v1/persistentvolumeclaims "{
  \"namespace_id\":\"$MONITORING_PROD\",
  \"name\":\"prometheus-data-prometheus-0\",
  \"phase\":\"Bound\",
  \"storage_class_name\":\"gp3\",
  \"volume_name\":\"pv-prometheus-data-0\",
  \"bound_volume_id\":\"$PROM_PV_ID\",
  \"access_modes\":[\"ReadWriteOnce\"],
  \"requested_storage\":\"100Gi\"
}" >/dev/null
echo "pv/pvc seeded"

echo
echo "=== summary ==="
for kind in clusters nodes namespaces workloads pods services ingresses persistentvolumes persistentvolumeclaims; do
    count=$(curl -sS "${AUTH_ARGS[@]}" "${BASE}/v1/${kind}?limit=200" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['items']))")
    printf "  %-25s %s\n" "$kind" "$count"
done
