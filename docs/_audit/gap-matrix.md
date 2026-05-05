# Documentation Gap Matrix

This is a working file produced by Task 1 of the documentation audit (2026-05-05).
It maps every shipped feature to its existing end-user doc, tech/architecture doc, and ADR, then
records what is missing in the **Gaps** column. It will be deleted in Task 12 once all gaps
have been closed.

Legend for **Gaps**: `–` = nothing missing; `end-user` = operator/user-facing how-to absent;
`tech` = internal/architecture doc absent; `adr` = decision record absent; `both` = end-user + tech;
`out of date` = doc exists but lags shipped behaviour.

| Feature | End-user doc | Tech doc | ADR | Gaps |
|---------|-------------|----------|-----|------|
| Local auth (username + password login) | `docs/authentication.md` | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | – |
| OIDC auth (authorize-code + PKCE) | `docs/authentication.md` | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | – |
| Bootstrap admin (first-run password) | `docs/getting-started.md` | `docs/architecture.md` (CLAUDE.md) | `adr-0007-auth-and-rbac.md` | tech (no standalone tech doc section) |
| RBAC roles & scopes | `docs/authentication.md` | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | – |
| PAT / Bearer token issuance | `docs/authentication.md` | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | – |
| Session cookie management | `docs/authentication.md` | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | tech (cookie lifecycle not detailed) |
| Audit log | `docs/authentication.md` (brief) | `docs/architecture.md` | `adr-0007-auth-and-rbac.md` | end-user (no dedicated how-to / query guide) |
| Last-admin guard | (none) | `docs/architecture.md` (CLAUDE.md) | `adr-0017-public-listener-tls-posture-and-proxy-trust.md` (AUTHZ-VULN-01/02) | end-user, tech |
| Public-listener TLS posture & proxy trust | `docs/configuration.md` | `docs/architecture.md` | `adr-0017-public-listener-tls-posture-and-proxy-trust.md` | – |
| Rate limiting | `docs/configuration.md` (env vars) | (none) | (none) | tech, adr |
| Security headers / HSTS | (none) | (none) | (none) | end-user, tech, adr |
| K8s pull collector (in-process) | `docs/getting-started.md`, `docs/deployment/helm.md` | `docs/architecture.md` | `adr-0005-multi-cluster-collector.md`, `adr-0011-collector-auto-creates-cluster.md` | – |
| Multi-cluster topology | `docs/deployment/helm.md` | `docs/architecture.md` | `adr-0005-multi-cluster-collector.md` | – |
| Kubeconfig security | `docs/how-to-secure-kubeconfig.md` | (none) | (none) | tech, adr |
| Push collector for air-gapped clusters | `docs/deployment/push-collector.md` | `docs/architecture.md` | `adr-0009-push-collector-for-airgapped-clusters.md` | – |
| DMZ ingest gateway | `docs/how-to-deploy-dmz-ingest-gateway.md` | `docs/architecture.md` | `adr-0016-dmz-ingest-gateway.md` | – |
| VM collector (Outscale / cloud VMs) | `docs/vm-collector.md` | `docs/architecture.md` | `adr-0015-vm-collector-for-non-kubernetes-platform-vms.md` | – |
| VM applications (curated software list) | `docs/vm-applications.md` | `docs/architecture.md` | `adr-0019-vm-applications-and-eol-and-search.md` | – |
| Cloud accounts admin | `docs/cloud-accounts.md` | `docs/architecture.md` | `adr-0015-vm-collector-for-non-kubernetes-platform-vms.md` | – |
| Credential encryption (AES-256-GCM) | `docs/cloud-accounts.md` (partial) | `docs/architecture.md` | `adr-0015-vm-collector-for-non-kubernetes-platform-vms.md` | tech (no standalone encryption doc) |
| EOL enrichment | `docs/eol-enrichment.md` | `docs/architecture.md` | `adr-0012-eol-enrichment-via-endoflife-date.md`, `adr-0019-vm-applications-and-eol-and-search.md` | – |
| EOL inventory page (UI) | `docs/eol-enrichment.md` | (none) | `adr-0012-eol-enrichment-via-endoflife-date.md` | tech |
| MCP server | `docs/mcp-server.md` | `docs/architecture.md` | `adr-0014-mcp-server.md` | – |
| Impact analysis | `docs/impact-analysis.md` | `docs/architecture.md` | `adr-0013-impact-analysis-graph.md` | – |
| Settings table (runtime feature flags) | `docs/configuration.md` (env vars), `docs/mcp-server.md` (toggle) | `docs/architecture.md` | (none) | adr |
| Admin UI – Users tab | `docs/authentication.md` | (none) | `adr-0006-ui-for-audit-and-curated-metadata.md` | tech |
| Admin UI – Tokens tab | `docs/authentication.md` | (none) | `adr-0007-auth-and-rbac.md` | tech |
| Admin UI – Sessions tab | `docs/authentication.md` | (none) | `adr-0007-auth-and-rbac.md` | tech |
| Admin UI – Audit tab | `docs/authentication.md` (brief) | (none) | `adr-0007-auth-and-rbac.md` | end-user, tech |
| Admin UI – Settings tab | `docs/configuration.md` (env vars only) | (none) | (none) | end-user, tech, adr |
| Image + application search UI | `docs/vm-applications.md` (partial) | (none) | `adr-0019-vm-applications-and-eol-and-search.md` | tech |
| Curated metadata on clusters | `docs/getting-started.md` (brief) | (none) | `adr-0006-ui-for-audit-and-curated-metadata.md` | tech |
| Workload polymorphism | (none) | `docs/architecture.md` | `adr-0003-workload-polymorphism.md` | end-user |
| Ingress layer classification | (none) | `docs/architecture.md` | `adr-0004-ingress-layer-classification.md` | end-user |
| Admin-only cluster deletion + audit | (none) | `docs/architecture.md` | `adr-0010-admin-only-cluster-deletion-with-audit.md` | end-user |
| Prometheus metrics & monitoring | `docs/monitoring.md` | `docs/architecture.md` | (none) | adr |
| Docker deployment | `docs/deployment/docker.md` | (none) | (none) | tech, adr |
| Helm chart – longue-vue (umbrella) | `docs/deployment/helm.md` | (none) | `adr-0018-helm-chart-per-deployable-binary.md` | tech |
| Helm chart – longue-vue-ingest-gw | `docs/how-to-deploy-dmz-ingest-gateway.md` | (none) | `adr-0016-dmz-ingest-gateway.md`, `adr-0018-helm-chart-per-deployable-binary.md` | tech |
| Helm chart – longue-vue-collector | `docs/deployment/push-collector.md`, `docs/deployment/helm.md` | (none) | `adr-0018-helm-chart-per-deployable-binary.md` | tech |
| Helm chart – longue-vue-vm-collector | `docs/vm-collector.md` | (none) | `adr-0018-helm-chart-per-deployable-binary.md` | tech |
| SecNumCloud alignment | (none) | `docs/architecture.md` | `adr-0001-cmdb-for-snc-using-kube.md`, `adr-0002-kubernetes-to-anssi-cartography-layers.md`, `adr-0008-secnumcloud-chapter-8-asset-management.md` | end-user |
| Rename argos → longue-vue migration | (none) | (none) | `adr-0020-rename-argos-to-longue-vue.md` | end-user, tech |
| Time-travel snapshots (ADR-0021, phase 1) | (none) | (none) | `adr-0021-time-travel-snapshots.md` | end-user, tech (feature not yet fully shipped) |
| API reference | `docs/api-reference.md` | (none) | (none) | – |
| Configuration reference | `docs/configuration.md` | (none) | (none) | – |
