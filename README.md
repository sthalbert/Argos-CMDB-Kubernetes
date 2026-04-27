# Argos

[![CI](https://img.shields.io/github/actions/workflow/status/sthalbert/Argos/ci.yml?branch=main&label=CI)](https://github.com/sthalbert/Argos/actions/workflows/ci.yml)
[![Latest Release](https://img.shields.io/github/v/release/sthalbert/Argos?sort=semver&label=Latest%20Release)](https://github.com/sthalbert/Argos/releases)
[![License](https://img.shields.io/github/license/sthalbert/Argos?label=License)](LICENSE)
[![Contributors](https://img.shields.io/github/contributors/sthalbert/Argos?label=Contributors)](https://github.com/sthalbert/Argos/graphs/contributors)
[![Stars](https://img.shields.io/github/stars/sthalbert/Argos?style=flat&label=Stars)](https://github.com/sthalbert/Argos/stargazers)

A Configuration Management Database (CMDB) for Kubernetes environments, aligned with the [ANSSI SecNumCloud](https://cyber.gouv.fr/enjeux-technologiques/cloud/) (SNC) qualification framework. Argos polls one or more Kubernetes clusters, mirrors the inventory into PostgreSQL, and exposes a REST API and web UI so auditors can see the cartography and operators can annotate assets with business context.

**Status:** alpha -- data model and HTTP contract are stable; expect additive changes until 1.0.

## Features

- **Full Kubernetes inventory** -- Clusters, Nodes, Namespaces, Workloads (Deployment / StatefulSet / DaemonSet), Pods, Services, Ingresses, PersistentVolumes, PVCs.
- **Inventory of non-Kubernetes platform VMs** (VPN, DNS, Bastion, Vault, …) per cloud account, with encrypted-at-rest credentials and a separate push-mode collector binary (ADR-0015).
- **Dual-mode collection** -- pull (argosd polls clusters) and push (argos-collector runs inside air-gapped clusters and pushes over HTTPS).
- **Multi-cluster** -- one argosd catalogues N clusters in parallel, or mixed pull+push.
- **ANSSI cartography layers** -- every kind maps to an SNC layer (ecosystem, applicative, infrastructure, etc.).
- **Dual-path auth** -- humans use session cookies (local login or OIDC); machines use bearer tokens (PAT).
- **Curated metadata** -- operators annotate clusters with owner, criticality, runbook URL, and free-form notes.
- **End-of-life inventory** -- enricher queries endoflife.date, flags EOL / approaching-EOL software, shows the latest available version to upgrade to.
- **Impact analysis graph** -- interactive dependency diagram on every entity page; assess blast radius before a change.
- **MCP server** -- Model Context Protocol interface exposing read-only CMDB tools for AI agents; SSE and stdio transports.
- **Audit log** -- every state-changing call is recorded; passwords and tokens are scrubbed.
- **Embedded web UI** -- React SPA shipped inside the binary at `/ui/`.

## Quick start

### With Helm (recommended for Kubernetes)

```bash
helm install argos charts/argos -n argos-system --create-namespace
kubectl -n argos-system logs -l app.kubernetes.io/name=argos | grep "ARGOS FIRST-RUN"
```

### From source (local development)

```bash
docker run -d --rm --name argos-pg \
  -e POSTGRES_PASSWORD=argos -e POSTGRES_DB=argos \
  -p 5432:5432 postgres:16-alpine

make ui-build && make build
ARGOS_DATABASE_URL="postgres://postgres:argos@localhost:5432/argos?sslmode=disable" \
  ARGOS_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login" \
  ./bin/argosd
# Open http://localhost:8080/ and sign in as admin
```

See [Getting Started](docs/getting-started.md) for the full walkthrough including cluster registration, collector setup, and demo data seeding.

## Documentation

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | From zero to a working installation. |
| [Configuration](docs/configuration.md) | All environment variables for argosd and argos-collector. |
| [Deploy with Helm](docs/deployment/helm.md) | One-command Kubernetes install with optional bundled PostgreSQL. |
| [Deploy with Kustomize](docs/deployment/kubernetes.md) | Production deployment with plain manifests. |
| [Push Collector](docs/deployment/push-collector.md) | Deploy argos-collector in air-gapped clusters. |
| [VM Collector](docs/vm-collector.md) | Deploy argos-vm-collector to inventory non-Kubernetes platform VMs. |
| [Cloud Accounts](docs/cloud-accounts.md) | Register cloud-provider accounts, manage AK/SK rotation, master key handling. |
| [Docker (local dev)](docs/deployment/docker.md) | Run locally with Docker. |
| [Authentication](docs/authentication.md) | Local users, OIDC, tokens, roles, sessions. |
| [API Reference](docs/api-reference.md) | REST endpoints with curl examples. |
| [EOL Enrichment](docs/eol-enrichment.md) | End-of-life inventory: setup, dashboard, annotation format. |
| [Impact Analysis](docs/impact-analysis.md) | Dependency graph: assess blast radius of a change. |
| [MCP Server](docs/mcp-server.md) | Model Context Protocol server for AI agent integrations. |
| [Monitoring](docs/monitoring.md) | Prometheus metrics, alerts, Grafana tips. |
| [Architecture](docs/architecture.md) | How Argos works internally. |

## Architecture

Argos ships as **two binaries**: `argosd` (the central server with API, UI, PostgreSQL store, and the in-process pull collectors) and `argos-vm-collector` (a standalone push-mode binary that polls cloud-provider APIs for non-Kubernetes platform VMs and pushes observations to argosd). The same push-mode pattern powers `argos-collector` for air-gapped Kubernetes clusters.

```
   Kubernetes cluster(s)        Air-gapped cluster        Cloud-provider account
          |                            |                           |
          | client-go (list)           | client-go (list)          | cloud SDK (list)
          v                            v                           v
   ┌──────────────┐            ┌──────────────────┐       ┌─────────────────────┐
   │ pull collector│            │ argos-collector  │       │ argos-vm-collector  │
   │ (goroutine/  │            │ (push over HTTPS)│       │ (push over HTTPS,   │
   │  cluster)    │            └────────┬─────────┘       │  fetches AK/SK from │
   └──────┬───────┘                     |                 │  argosd at boot)    │
          │ direct store                | REST API +      └──────────┬──────────┘
          v                             | Bearer                      |
   ┌──────────────┐     ┌────────────┐  v                              | REST API +
   │   argosd     │────>│ PostgreSQL │<──── REST API / SPA / Prometheus | Bearer (vm-collector scope)
   │ (API + UI +  │     │ (JSONB +   │<─────────────────────────────────┘
   │  collector)  │     │  goose)    │
   └──────────────┘     └────────────┘
```

## Architectural decisions

| # | Topic |
|---|-------|
| [0001](docs/adr/adr-0001-cmdb-for-snc-using-kube.md) | Build a CMDB for SNC against the Kubernetes API. |
| [0002](docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md) | Map every Kubernetes kind onto one of the six ANSSI cartography layers. |
| [0003](docs/adr/adr-0003-workload-polymorphism.md) | Single `workloads` table polymorphic on `kind`, with a JSONB `spec` column. |
| [0004](docs/adr/adr-0004-ingress-layer-classification.md) | Classify Ingress in the `applicative` layer. |
| [0005](docs/adr/adr-0005-multi-cluster-collector.md) | Central-pull multi-cluster topology: one argosd, N collector goroutines. |
| [0006](docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md) | Web UI bundled into argosd for audit views and curated asset metadata. |
| [0007](docs/adr/adr-0007-auth-and-rbac.md) | Dual-path auth (session + bearer) and four-role RBAC. |
| [0008](docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md) | SecNumCloud chapter 8 asset management alignment. |
| [0009](docs/adr/adr-0009-push-collector-for-airgapped-clusters.md) | Push-based collector for air-gapped clusters. |
| [0010](docs/adr/adr-0010-pre-deletion-cascade-audit.md) | Pre-deletion cascade audit enrichment. |
| [0011](docs/adr/adr-0011-persistent-volumes-and-claims.md) | PersistentVolumes and PVCs in the CMDB. |
| [0012](docs/adr/adr-0012-eol-enrichment-via-endoflife-date.md) | End-of-life enrichment via endoflife.date. |
| [0013](docs/adr/adr-0013-impact-analysis-graph.md) | Impact analysis graph for blast-radius assessment. |
| [0014](docs/adr/adr-0014-mcp-server.md) | MCP server for AI agent access to the CMDB. |
| [0015](docs/adr/adr-0015-vm-collector-for-non-kubernetes-platform-vms.md) | VM collector for non-Kubernetes platform infrastructure (cloud_accounts + virtual_machines + standalone collector binary). |

## Contributing

Prerequisites: Go (version in `go.mod`), Node 22+, Docker (for integration tests).

```bash
make check    # fmt + vet + lint + test -- the CI-equivalent gate
```

Integration tests require `PGX_TEST_DATABASE` pointing at a PostgreSQL instance. See [Docker (local dev)](docs/deployment/docker.md) for the development workflow.

## License

GPL-3.0 -- see [LICENSE](LICENSE).
