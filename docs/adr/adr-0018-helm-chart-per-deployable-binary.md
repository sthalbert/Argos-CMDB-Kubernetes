---
title: "ADR-0018: Helm chart per deployable binary"
status: "Accepted"
date: "2026-04-29"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "packaging", "helm", "deployment", "collector", "vm-collector", "secnumcloud"]
supersedes: ""
superseded_by: ""
---

# ADR-0018: Helm chart per deployable binary

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

## Context

longue-vue ships **four** deployable binaries today:

| Binary             | Role                                                                                             | Packaging today                                          |
|--------------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------------|
| `longue-vue`           | Central server: API, UI, store, in-process pull collector goroutines                             | Helm chart `charts/longue-vue/` (chart 0.14.0 / app 0.11.1)   |
| `longue-vue-ingest-gw`  | DMZ reverse-proxy (ADR-0016) — fronts longue-vue's mTLS-only ingest listener                         | Helm chart `charts/longue-vue-ingest-gw/` (chart 0.1.0)       |
| `longue-vue-collector`  | Push-mode Kubernetes collector for air-gapped / DMZ-isolated clusters (ADR-0009)                 | **Raw Kustomize manifests** under `deploy/collector/`    |
| `longue-vue-vm-collector` | Push-mode VM collector for cloud-provider VMs (ADR-0015 §IMP-003); one instance per cloud account | **Raw Kustomize manifests** under `deploy/vm-collector/` |

The two missing charts cause concrete operator pain:

1. **Inconsistent install UX.** A SecNumCloud operator running longue-vue via Helm must drop down to `kubectl apply -k deploy/collector/` for the collector. Two different upgrade procedures, two different secret-management stories, two different conventions for image override.
2. **No values surface for the air-gap profile.** `deploy/collector/deployment.airgap.yaml` is a *forked* manifest carrying proxy + extra-CA env vars. Operators copy-edit it instead of toggling values in a chart.
3. **Multi-account vm-collector is awkward.** ADR-0015 §IMP-004 mandates one binary instance per cloud account. With Kustomize, that means N hand-edited overlay directories. With Helm, it would be N `helm install` calls with `--set accountName=<x>` on the same chart.
4. **Inconsistent observability wiring.** `charts/longue-vue-ingest-gw/` ships an optional `ServiceMonitor` + `PrometheusRule`; the collector and vm-collector ship neither — operators write their own.
5. **mTLS to the DMZ gateway is undocumented in the deploy manifests.** ADR-0016 §7 introduced the gateway as the new collector ingress, but `deploy/collector/deployment.yaml` still bakes in the legacy "talk straight to longue-vue" shape. The chart is the natural place to expose `longue-vue-tls.existingSecret` + `longue-vue-tls.caSecret` + `longue-vue-tls.extraHeaders` as first-class values.
6. **SecNumCloud audit posture.** SNC chapter 8 (asset management, ADR-0008) requires a documented, repeatable deployment artifact. A Helm chart with pinned values is a stronger audit artifact than Kustomize overlays that the operator hand-edits per cluster.

The user directive: **every binary we want to deploy MUST have a Helm chart.** This ADR codifies that policy and resolves the resulting design questions.

## Decision

We add **two new independent Helm charts**, mirroring the layout and conventions of `charts/longue-vue-ingest-gw/`:

- `charts/longue-vue-collector/` — packages the `longue-vue-collector` push-mode Kubernetes collector binary.
- `charts/longue-vue-vm-collector/` — packages the `longue-vue-vm-collector` push-mode VM collector binary.

We keep `charts/longue-vue/` (longue-vue) and `charts/longue-vue-ingest-gw/` (gateway) unchanged. We deprecate `deploy/collector/` and `deploy/vm-collector/` raw manifests in a follow-up release; for ADR-0018 itself, we keep them as a fallback and add a pointer in `deploy/README.md` to the new charts.

### 1. Independent charts, not subcharts

Each chart is a top-level Helm chart, **not** a subchart of `charts/longue-vue/`. Reasons:

- The collector typically runs in a *target* cluster — different namespace, often a different physical cluster, from longue-vue. An umbrella subchart relationship forces co-installation, breaking the multi-cluster topology of ADR-0005.
- The vm-collector deploys **one Helm release per cloud account** (ADR-0015 §IMP-004). N subchart instances inside a single longue-vue release is ergonomically broken — you can't `helm upgrade` one account in isolation.
- The DMZ ingest gateway already established this pattern in ADR-0016. Extending it to the two collectors keeps the mental model uniform: **chart = binary**.

### 2. Chart layout

Each new chart follows the same skeleton as `charts/longue-vue-ingest-gw/`:

```
charts/<chart>/
├── Chart.yaml                     # chart 0.1.0, appVersion 0.11.1
├── values.yaml                    # documented, opinionated defaults
└── templates/
    ├── _helpers.tpl               # standard chart helpers
    ├── NOTES.txt                  # post-install hints (token secret, log tail)
    ├── deployment.yaml            # the binary
    ├── serviceaccount.yaml        # the SA
    ├── clusterrole.yaml           # collector only — read-only Kube API
    ├── clusterrolebinding.yaml    # collector only
    ├── networkpolicy.yaml         # opt-in, default off
    ├── poddisruptionbudget.yaml   # opt-in for >1 replica
    └── servicemonitor.yaml        # opt-in, requires Prometheus Operator
```

The collector chart owns RBAC; the vm-collector chart does not (the VM collector talks to a cloud API, not the Kubernetes API).

### 3. Values surface — common to both charts

Both charts expose the same shape for the parts that match. The keys mirror existing longue-vue / gateway conventions so operators reuse muscle memory.

| Key                            | Default                                  | Purpose                                                           |
|--------------------------------|------------------------------------------|-------------------------------------------------------------------|
| `image.repository`             | `ghcr.io/sthalbert/longue-vue-<chart-name>`   | Image registry override for air-gap mirrors                       |
| `image.tag`                    | `""` (falls back to `Chart.appVersion`)  | Pin a specific version                                            |
| `image.pullPolicy`             | `IfNotPresent`                           | Standard                                                          |
| `imagePullSecrets`             | `[]`                                     | For private registries                                            |
| `replicaCount`                 | `1` (collector) / `1` (vm-collector)     | Both are single-instance per scope; HA is a follow-up             |
| `serverURL`                    | `""` (required)                          | longue-vue or DMZ gateway URL — fails install if empty                |
| `tokenSecret.existingSecret`   | `""` (required)                          | Kubernetes Secret carrying `LONGUE_VUE_API_TOKEN`                      |
| `tokenSecret.tokenKey`         | `LONGUE_VUE_API_TOKEN`                        | Override if the operator named the secret key differently         |
| `longue-vue-tls.existingSecret`      | `""`                                     | Optional client cert for mTLS (DMZ gateway shape — ADR-0016)      |
| `longue-vue-tls.caSecret`            | `""`                                     | Optional CA bundle for verifying longue-vue/gateway                   |
| `longue-vue-tls.extraHeaders`        | `""`                                     | Optional `X-Longue-Vue-*` headers (gateway → longue-vue injection defence) |
| `proxy.httpsProxy`             | `""`                                     | Air-gap egress proxy                                              |
| `proxy.noProxy`                | `""`                                     | Air-gap proxy bypass list                                         |
| `resources`                    | `requests:25m/32Mi limits:250m/128Mi`    | Conservative defaults — collectors are I/O-bound, not CPU-bound   |
| `podSecurityContext`           | `runAsNonRoot:true,fsGroup:65532`        | Distroless UID                                                    |
| `containerSecurityContext`     | `readOnlyRootFilesystem:true,drop:[ALL]` | SNC-hardened default                                              |
| `nodeSelector` / `tolerations` / `affinity` | `{}` / `[]` / `{}`          | Standard Helm scheduling escape hatches                           |
| `serviceMonitor.enabled`       | `false`                                  | Requires Prometheus Operator                                      |
| `networkPolicy.enabled`        | `false`                                  | Opt-in egress lock-down                                           |

### 4. Values surface — collector-specific

| Key                              | Default      | Purpose                                                                 |
|----------------------------------|--------------|-------------------------------------------------------------------------|
| `clusterName`                    | (required)   | Sent as `LONGUE_VUE_CLUSTER_NAME` — the cluster's CMDB row name              |
| `interval`                       | `5m`         | `LONGUE_VUE_COLLECTOR_INTERVAL`                                              |
| `reconcile`                      | `true`       | `LONGUE_VUE_COLLECTOR_RECONCILE` — required for ANSSI cartography fidelity   |
| `kubeconfig.mode`                | `in-cluster` | `in-cluster` (default), `secret` (mount a kubeconfig from a Secret)     |
| `kubeconfig.existingSecret`      | `""`         | Required when `kubeconfig.mode=secret`                                  |
| `rbac.create`                    | `true`       | When `false`, the chart does not create the ClusterRole/Binding         |
| `rbac.serviceAccountName`        | `""`         | When set, override the SA name (caller brings their own SA)             |

The ClusterRole carries the **same `list`-only permissions** as today's `deploy/collector/rbac.yaml`: `nodes`, `namespaces`, `pods`, `services`, `persistentvolumes`, `persistentvolumeclaims`, `deployments`, `statefulsets`, `daemonsets`, `replicasets`, `ingresses`. No `watch`, no `create`, no `delete` — the binary has no need for them.

### 5. Values surface — vm-collector-specific

| Key                       | Default         | Purpose                                                          |
|---------------------------|-----------------|------------------------------------------------------------------|
| `accountName`             | (required)      | `LONGUE_VUE_VM_COLLECTOR_ACCOUNT_NAME` — must match the `cloud_accounts.name` row |
| `provider`                | `outscale`      | `LONGUE_VUE_VM_COLLECTOR_PROVIDER` — only `outscale` today (ADR-0015) |
| `region`                  | (required)      | `LONGUE_VUE_VM_COLLECTOR_REGION`                                      |
| `interval`                | `15m`           | `LONGUE_VUE_VM_COLLECTOR_INTERVAL`                                    |
| `fetchTimeout`            | `2m`            | `LONGUE_VUE_VM_COLLECTOR_FETCH_TIMEOUT`                               |
| `reconcile`               | `true`          | `LONGUE_VUE_VM_COLLECTOR_RECONCILE`                                   |
| `credentialRefresh`       | `1h`            | `LONGUE_VUE_VM_COLLECTOR_CREDENTIAL_REFRESH`                          |
| `networkPolicy.cloudAPICIDRs` | `[]`        | Egress allow list when `networkPolicy.enabled=true`              |

The vm-collector chart's NOTES.txt prints a copy-paste hint for "install one release per cloud account":

```
helm install longue-vue-vm-collector-<account> charts/longue-vue-vm-collector \
  --namespace longue-vue-vm-collectors --create-namespace \
  --set accountName=<account> --set region=<region> \
  --set tokenSecret.existingSecret=longue-vue-vm-collector-<account>-pat
```

### 6. Secret handling

Neither chart generates tokens or PATs. Both expect the operator to create the secret out-of-band (typically via `kubectl create secret generic` or via Vault Agent). This matches the `charts/longue-vue/` bootstrap pattern (the admin password is operator-supplied) and avoids putting plaintext PATs in Helm release state, where they would persist in `helm get values` output forever.

The chart's NOTES.txt prints the exact `kubectl create secret` invocation needed before `helm install` succeeds. A `helm install --dry-run` validates the values surface; the Deployment starts in `CrashLoopBackOff` if the secret is missing — same failure mode as today's `deploy/` manifests.

### 7. Versioning policy

| Chart                   | Initial chart version | Initial appVersion |
|-------------------------|-----------------------|--------------------|
| `longue-vue-collector`       | `0.1.0`               | `0.11.1`           |
| `longue-vue-vm-collector`    | `0.1.0`               | `0.11.1`           |

Going forward:

- A patch bump to a chart (template fix, comment fix, no values change, no app bump) increments the chart `version` patch.
- An app version bump (binary release) syncs `appVersion` and bumps the chart `version` minor.
- A breaking values change (rename, removal, default flip) bumps the chart `version` major and ships a CHANGELOG migration note.

The four chart versions evolve independently. Releasing a new longue-vue version does **not** force a chart bump on the collector charts unless their bundled defaults need to change.

### 8. Migration: `deploy/` vs. `charts/`

We keep `deploy/collector/` and `deploy/vm-collector/` as a fallback for operators who don't run Helm. We:

1. Add a `## Helm` section at the top of each `deploy/*/README.md` pointing to the new chart as the recommended path.
2. Mark the raw manifests as "minimal example, prefer the Helm chart for production" in their headers.
3. Defer outright removal to a future ADR — too many existing CI pipelines reference these paths to delete them in a single release.

The `deploy/collector/deployment.airgap.yaml` profile is reproducible via the chart by setting `proxy.httpsProxy=...` and `longue-vue-tls.caSecret=...`; the README documents this 1:1 mapping.

### 9. Helm linting in CI

Phase 3 adds `helm lint charts/longue-vue-collector charts/longue-vue-vm-collector` and `helm template ...` smoke tests to the existing `make check` flow (or the GitHub Actions `ci.yml` if `make check` doesn't already run helm). The smoke test asserts:

- `helm lint` returns 0 errors and 0 warnings on default values.
- `helm template --set serverURL=https://x --set tokenSecret.existingSecret=foo --set clusterName=test` (or the vm-collector equivalent) renders without templating errors.
- The rendered Deployment has the expected `securityContext` block (defence in depth: a typo in `_helpers.tpl` shouldn't silently drop `runAsNonRoot`).

These run on every PR — the same gate the umbrella chart sits behind today.

## Consequences

### Positive

- **Uniform install UX.** All four binaries install via `helm install`. Operators learn one packaging convention.
- **First-class values surface for the air-gap and DMZ-mTLS profiles.** No more forked YAML.
- **Multi-account vm-collector becomes ergonomic.** `helm install longue-vue-vm-collector-<account> ... --set accountName=<account>` per account.
- **Out-of-the-box ServiceMonitor / NetworkPolicy / PDB toggles.** Operators get production-grade observability and isolation by flipping a values key.
- **Stronger SNC audit artifact.** A version-pinned chart with documented values is auditable; a Kustomize tree the operator hand-edited is not.

### Negative

- **More charts to maintain.** Four independent charts means four `Chart.yaml` files, four `values.yaml` files, four NOTES.txt strings to keep in sync. Mitigation: shared conventions in `_helpers.tpl`, charts diff cleanly when reviewed side-by-side.
- **Helm-lint coverage adds CI cost.** Two extra `helm lint` calls per PR. Negligible (sub-second each).
- **Fallback `deploy/` directories drift.** If we update the chart values surface but don't re-sync `deploy/`, the manifests fall behind. Mitigation: deprecation banner + planned removal ADR in a follow-up release.

### Neutral

- The Docker images for both binaries already exist (`Dockerfile.collector`, `Dockerfile.vm-collector` build to `ghcr.io/sthalbert/longue-vue-collector:<tag>` and `longue-vue-vm-collector:<tag>`). The charts simply reference them. No changes to the build pipeline.
- No code changes to the binaries themselves. The charts are pure packaging.
- No OpenAPI spec changes. The Phase 3 OpenAPI validation harness still runs as a regression guard.

## Alternatives considered

1. **Subcharts under `charts/longue-vue/`.** Rejected: forces co-installation with longue-vue, which breaks the multi-cluster (ADR-0005) and one-instance-per-cloud-account (ADR-0015) topologies. Also pollutes the umbrella chart's release history with binary-specific bumps.
2. **A single "collectors" chart deploying both binaries with feature flags.** Rejected: the binaries have orthogonal RBAC needs (one needs cluster-scoped Kube API list, the other needs zero Kube API access), orthogonal release cadence, and different deployment scopes (per-cluster vs. per-cloud-account). Bundling them couples concerns.
3. **Continue shipping raw Kustomize.** Rejected: the user directive is explicit. Even setting that aside, the gaps in §Context (air-gap profile, multi-account, mTLS to gateway, observability) are real today and grow as more deployment shapes accrete.
4. **Generate the chart from a CRD or operator.** Out of scope. longue-vue is not an operator and adopting one for packaging is disproportionate to the problem.

## References

- ADR-0005 — Multi-cluster collector topology (`charts/longue-vue-collector/` deploys per-cluster, per ADR-0005).
- ADR-0008 — SecNumCloud chapter 8 asset management (audit-grade deployment artifacts).
- ADR-0009 — Push collector for air-gapped clusters (the `longue-vue-collector` binary).
- ADR-0015 — VM collector for non-Kubernetes platform VMs (the `longue-vue-vm-collector` binary; one instance per cloud account).
- ADR-0016 — DMZ ingest gateway (precedent for an independent chart per binary; mTLS posture inherited by both new charts).
- ADR-0017 — Public listener TLS posture (the `longue-vue-tls.*` values surface mirrors ADR-0017's `longue-vue.tls.*` shape).
