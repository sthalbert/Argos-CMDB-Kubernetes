---
title: "ADR-0011: Collector auto-creates cluster record on first contact"
status: "Accepted"
date: "2026-04-24"
authors: "Steve ALBERT"
tags: ["architecture", "decision", "collector", "cluster", "lifecycle"]
supersedes: "ADR-0005 ALT-007/ALT-008 (partial)"
superseded_by: ""
---

# ADR-0011: Collector auto-creates cluster record on first contact

## Status

Proposed | **Accepted** | Rejected | Superseded | Deprecated

## Context

ADR-0005 explicitly rejected collector auto-creation of cluster records (ALT-007/ALT-008). The rationale was that explicit `POST /v1/clusters` registration forces the operator to commit metadata (display name, environment, labels) before the collector floods rows in, improving data quality.

In practice this created a boot-ordering problem: the collector cannot ingest anything until a human (or script) has registered the cluster via the API. For the common case — a fresh deployment where longue-vue runs inside the cluster it catalogues — this adds a mandatory manual step between `kubectl apply` and a working CMDB. Operators deploying via Helm or Kustomize expect the system to be functional after a single `apply`.

The same friction applies to the push collector (ADR-0009): the air-gapped cluster's `longue-vue-collector` cannot push until someone registers the cluster in the central longue-vue — which may itself require tunnelling through the air-gap boundary just to run a `curl`.

## Decision

**The collector auto-creates a minimal cluster record when `GetClusterByName` returns `ErrNotFound`.**

The auto-created record carries only the `name` field (matching `LONGUE_VUE_CLUSTER_NAME` or the entry in `LONGUE_VUE_COLLECTOR_CLUSTERS`). All curated-metadata columns (`display_name`, `environment`, `owner`, `criticality`, `notes`, `runbook_url`, `annotations`) remain at their zero values. The collector then proceeds with its normal ingestion tick.

This applies to both the pull collector (embedded in longue-vue) and the push collector (`longue-vue-collector`), which calls `CreateCluster` through the API client.

**Pre-registration via `POST /v1/clusters` remains supported and recommended** when the operator wants curated metadata populated before the first tick. If the cluster already exists, the collector uses it as-is — no fields are overwritten. The merge-patch semantics of `UpdateCluster` ensure that a later metadata edit is never clobbered by the collector.

## Consequences

### Positive

- **POS-001**: Zero-touch bootstrap — a fresh `kubectl apply -k deploy/` produces a working CMDB without any manual API call.
- **POS-002**: Push collectors in air-gapped clusters no longer require a round-trip to the central longue-vue just to register the cluster name.
- **POS-003**: The `scripts/seed-demo.sh` workflow and integration tests that `POST /v1/clusters` continue to work unchanged — pre-creation is additive, not required.

### Negative

- **NEG-001**: Auto-created clusters have empty metadata until an operator fills it in. This is a data-quality trade-off: the CMDB has the cluster immediately, but without curated context. Mitigated by the UI surfacing empty fields as prompts ("No environment set — edit to add").
- **NEG-002**: A typo in `LONGUE_VUE_CLUSTER_NAME` silently creates a new cluster row instead of failing. Operators must verify cluster names after first deployment.

## References

- **REF-001**: ADR-0005 — Multi-cluster collector topology (ALT-007/ALT-008: original rejection of auto-creation)
- **REF-002**: ADR-0009 — Push-based collector for air-gapped clusters
- **REF-003**: `internal/collector/collector.go` — `poll()` auto-creation path
- **REF-004**: `internal/collector/collector_test.go` — `TestPollAutoCreatesClusterWhenMissing`
