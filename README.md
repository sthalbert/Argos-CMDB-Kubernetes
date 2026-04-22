# Argos

[![Latest Release](https://img.shields.io/github/v/release/sthalbert/Argos?sort=semver&label=Latest%20Release)](https://github.com/sthalbert/Argos/releases)
[![License](https://img.shields.io/github/license/sthalbert/Argos?label=License)](LICENSE)
[![Contributors](https://img.shields.io/github/contributors/sthalbert/Argos?label=Contributors)](https://github.com/sthalbert/Argos/graphs/contributors)
[![Stars](https://img.shields.io/github/stars/sthalbert/Argos?style=flat&label=Stars)](https://github.com/sthalbert/Argos/stargazers)

A Configuration Management Database (CMDB) for Kubernetes environments, aligned with the [ANSSI SecNumCloud](https://cyber.gouv.fr/enjeux-technologiques/cloud/) (SNC) qualification framework. Argos polls one or more Kubernetes clusters, mirrors the inventory into PostgreSQL, and exposes a REST API and web UI so auditors can see the cartography and operators can annotate assets with business context.

Replaces the Kubernetes-scoped portion of [Mercator](https://github.com/dbsystel/mercator). **Status:** alpha -- data model and HTTP contract are stable; expect additive changes until 1.0.

## Features

- **Full Kubernetes inventory** -- Clusters, Nodes, Namespaces, Workloads (Deployment / StatefulSet / DaemonSet), Pods, Services, Ingresses, PersistentVolumes, PVCs.
- **Dual-mode collection** -- pull (argosd polls clusters) and push (argos-collector runs inside air-gapped clusters and pushes over HTTPS).
- **Multi-cluster** -- one argosd catalogues N clusters in parallel, or mixed pull+push.
- **ANSSI cartography layers** -- every kind maps to an SNC layer (ecosystem, applicative, infrastructure, etc.).
- **Dual-path auth** -- humans use session cookies (local login or OIDC); machines use bearer tokens (PAT).
- **Curated metadata** -- operators annotate clusters with owner, criticality, runbook URL, and free-form notes.
- **Audit log** -- every state-changing call is recorded; passwords and tokens are scrubbed.
- **Embedded web UI** -- React SPA shipped inside the binary at `/ui/`.

## Quick start

```bash
# 1. Start PostgreSQL
docker run -d --rm --name argos-pg \
  -e POSTGRES_PASSWORD=argos -e POSTGRES_DB=argos \
  -p 5432:5432 postgres:16-alpine

# 2. Build and run argosd
make ui-build && make build
ARGOS_DATABASE_URL="postgres://postgres:argos@localhost:5432/argos?sslmode=disable" \
  ARGOS_BOOTSTRAP_ADMIN_PASSWORD="changeme-on-first-login" \
  ./bin/argosd

# 3. Open http://localhost:8080/ and sign in as admin
```

See [Getting Started](docs/getting-started.md) for the full walkthrough including cluster registration, collector setup, and demo data seeding.

## Documentation

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | From zero to a working installation. |
| [Configuration](docs/configuration.md) | All environment variables for argosd and argos-collector. |
| [Deploy on Kubernetes](docs/deployment/kubernetes.md) | Production deployment with Kustomize. |
| [Push Collector](docs/deployment/push-collector.md) | Deploy argos-collector in air-gapped clusters. |
| [Docker (local dev)](docs/deployment/docker.md) | Run locally with Docker. |
| [Authentication](docs/authentication.md) | Local users, OIDC, tokens, roles, sessions. |
| [API Reference](docs/api-reference.md) | REST endpoints with curl examples. |
| [Monitoring](docs/monitoring.md) | Prometheus metrics, alerts, Grafana tips. |
| [Architecture](docs/architecture.md) | How Argos works internally. |

## Architecture

```
   Kubernetes cluster(s)              Air-gapped cluster
          |                                   |
          | client-go (list)                  | client-go (list)
          v                                   v
   ┌──────────────┐                  ┌──────────────────┐
   │ pull collector│                  │ argos-collector   │
   │ (goroutine/  │                  │ (push over HTTPS) │
   │  cluster)    │                  └────────┬─────────┘
   └──────┬───────┘                           |
          │ direct store                      | REST API + Bearer
          v                                   v
   ┌──────────────┐     ┌────────────┐
   │   argosd     │────>│ PostgreSQL │<──── REST API / SPA / Prometheus
   │ (API + UI +  │     │ (JSONB +   │
   │  collector)  │     │  goose)    │
   └──────────────┘     └────────────┘
```

## Architectural decisions

| # | Topic |
|---|-------|
| [0001](docs/adr/adr-0001-cmdb-for-snc-using-kube.md) | Build a CMDB for SNC against the Kubernetes API (replaces Mercator for the K8s scope). |
| [0002](docs/adr/adr-0002-kubernetes-to-anssi-cartography-layers.md) | Map every Kubernetes kind onto one of the six ANSSI cartography layers. |
| [0003](docs/adr/adr-0003-workload-polymorphism.md) | Single `workloads` table polymorphic on `kind`, with a JSONB `spec` column. |
| [0004](docs/adr/adr-0004-ingress-layer-classification.md) | Classify Ingress in the `applicative` layer. |
| [0005](docs/adr/adr-0005-multi-cluster-collector.md) | Central-pull multi-cluster topology: one argosd, N collector goroutines. |
| [0006](docs/adr/adr-0006-ui-for-audit-and-curated-metadata.md) | Web UI bundled into argosd for audit views and curated asset metadata. |
| [0007](docs/adr/adr-0007-auth-and-rbac.md) | Dual-path auth (session + bearer) and four-role RBAC. |
| [0008](docs/adr/adr-0008-secnumcloud-chapter-8-asset-management.md) | SecNumCloud chapter 8 asset management alignment. |
| [0009](docs/adr/adr-0009-push-collector-for-airgapped-clusters.md) | Push-based collector for air-gapped clusters. |

## Contributing

Prerequisites: Go (version in `go.mod`), Node 22+, Docker (for integration tests).

```bash
make check    # fmt + vet + lint + test -- the CI-equivalent gate
```

Integration tests require `PGX_TEST_DATABASE` pointing at a PostgreSQL instance. See [Docker (local dev)](docs/deployment/docker.md) for the development workflow.

## License

GPL-3.0 -- see [LICENSE](LICENSE).
