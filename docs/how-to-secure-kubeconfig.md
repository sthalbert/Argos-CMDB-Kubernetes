# How to securely provide kubeconfigs to longue-vue

This guide shows how to supply Kubernetes credentials to the longue-vue collector without exposing them in environment variables, pod specs, or version control.

## Why longue-vue needs kubeconfigs

longue-vue is a multi-cluster CMDB: its collector polls the Kubernetes API of every catalogued cluster to build a live inventory of nodes, namespaces, pods, workloads, services, ingresses, and persistent volumes (per ADR-0005).

When longue-vue runs **inside** a cluster, it can catalogue that cluster using its own ServiceAccount — no kubeconfig needed. But to catalogue **remote** clusters, the collector needs a kubeconfig for each one. That kubeconfig authenticates a read-only ServiceAccount on the target cluster so the collector can `list` resources.

In a typical SecNumCloud deployment, a single longue-vue instance catalogues several clusters across zones and environments. Each remote cluster requires its own kubeconfig, making secure credential handling critical.

## Why the credentials must be mounted, not injected

Kubernetes stores environment variables in the Pod spec in plaintext. Anyone with `kubectl get pod -o yaml` access can read them. If a kubeconfig path or — worse — its content ends up in an env var, it is visible to every user with pod-read RBAC in the namespace.

Committed files are equally dangerous: once a kubeconfig, a cluster name list, or a credential hits a Git history, it stays there even after deletion.

The only safe pattern is **mounting kubeconfigs from a Kubernetes Secret as read-only files**, referenced by path in the collector configuration.

## Prerequisites

- `kubectl` configured with access to the cluster where longue-vue runs
- The target cluster's kubeconfig (or access to generate one)
- longue-vue deployed via Helm (`charts/longue-vue/`) or Kustomize (`deploy/`)

## 1. Extract the kubeconfig

Pull a standalone, self-contained kubeconfig for the target cluster context:

```sh
kubectl config view \
  --context <TARGET_CONTEXT> \
  --raw --flatten --minify \
  > /tmp/target-cluster.yaml
```

Verify it works in isolation:

```sh
KUBECONFIG=/tmp/target-cluster.yaml kubectl get nodes
```

> **Tip:** the kubeconfig should authenticate a ServiceAccount scoped to `list`-only on the resource kinds longue-vue ingests (see `deploy/rbac.yaml`). Never hand longue-vue a cluster-admin credential.

## 2. Create the Kubernetes Secret

### Single cluster

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> create secret generic longue-vue-kubeconfig \
  --from-file=target-cluster=/tmp/target-cluster.yaml
```

### Multiple clusters

Add one `--from-file` per cluster. Each key becomes a file in the mounted volume:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> create secret generic longue-vue-kubeconfig \
  --from-file=cluster-a=/tmp/cluster-a.yaml \
  --from-file=cluster-b=/tmp/cluster-b.yaml \
  --from-file=cluster-c=/tmp/cluster-c.yaml
```

### Update an existing Secret

Kubernetes `create secret` fails if the Secret already exists. Use the dry-run + apply pattern:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> create secret generic longue-vue-kubeconfig \
  --from-file=cluster-a=/tmp/cluster-a.yaml \
  --from-file=cluster-b=/tmp/cluster-b.yaml \
  --dry-run=client -o yaml | kubectl apply -f -
```

Delete the local files once the Secret is created:

```sh
rm /tmp/target-cluster.yaml /tmp/cluster-*.yaml
```

## 3. Configure longue-vue to mount the Secret

### Helm

In your values file:

```yaml
collector:
  enabled: true
  kubeconfigSecret: "longue-vue-kubeconfig"
  clusters:
    - name: "cluster-a"
      kubeconfig: "/etc/longue-vue/kubeconfigs/cluster-a"
    - name: "cluster-b"
      kubeconfig: "/etc/longue-vue/kubeconfigs/cluster-b"
```

The chart mounts the Secret at `/etc/longue-vue/kubeconfigs/` as a read-only volume. Each key in the Secret appears as a file at that path.

To also catalogue the cluster longue-vue runs on (via its ServiceAccount), add an entry with an empty `kubeconfig`:

```yaml
    - name: "local"
      kubeconfig: ""
```

Then deploy:

```sh
helm upgrade longue-vue charts/longue-vue -n <LONGUE_VUE_NAMESPACE> -f values.yaml
```

### Kustomize

Add the volume and mount to your deployment patch:

```yaml
spec:
  template:
    spec:
      containers:
        - name: longue-vue
          env:
            - name: LONGUE_VUE_COLLECTOR_CLUSTERS
              value: |
                [
                  {"name":"cluster-a","kubeconfig":"/etc/longue-vue/kubeconfigs/cluster-a"},
                  {"name":"cluster-b","kubeconfig":"/etc/longue-vue/kubeconfigs/cluster-b"}
                ]
          volumeMounts:
            - name: kubeconfigs
              mountPath: /etc/longue-vue/kubeconfigs
              readOnly: true
      volumes:
        - name: kubeconfigs
          secret:
            secretName: longue-vue-kubeconfig
```

Apply:

```sh
kubectl apply -k deploy/
```

## 4. Verify

### Check the pod spec is clean

Confirm no kubeconfig content or path appears in environment variables:

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> get pod -l app.kubernetes.io/name=longue-vue \
  -o jsonpath='{.items[0].spec.containers[0].env}' | jq .
```

The output should contain `LONGUE_VUE_COLLECTOR_CLUSTERS` (with file paths only) but no `LONGUE_VUE_KUBECONFIG` key.

### Check the volume is mounted

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> get pod -l app.kubernetes.io/name=longue-vue \
  -o jsonpath='{.items[0].spec.containers[0].volumeMounts}' | jq .
```

Expect a `kubeconfigs` mount at `/etc/longue-vue/kubeconfigs` with `readOnly: true`.

### Check the collector logs

```sh
kubectl -n <LONGUE_VUE_NAMESPACE> logs -l app.kubernetes.io/name=longue-vue --tail=50 | grep -i cluster
```

You should see successful poll entries for each configured cluster within one collector interval (default 5 minutes).

## What to avoid

| Anti-pattern | Risk |
|---|---|
| `LONGUE_VUE_KUBECONFIG` env var with a file path | Path visible in pod spec to anyone with pod-read RBAC |
| `LONGUE_VUE_KUBECONFIG` env var with kubeconfig content | Full credentials exposed in pod spec |
| Kubeconfig files committed to Git | Credentials in history forever, even after removal |
| Real cluster names in committed manifests | Leaks internal topology (zone names, environments, cluster roles) |
| `stringData` Secrets committed to Git | Plaintext credentials in version control |
