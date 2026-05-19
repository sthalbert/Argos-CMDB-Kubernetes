-- +goose Up
-- Spec: docs/superpowers/specs/2026-05-18-cluster-cascade-delete-design.md
--
-- Before this release, SoftDeleteCluster / SoftDeleteNamespace marked the
-- parent terminated but never DELETE'd pods, services, ingresses, PVs, or
-- PVCs. FK ON DELETE CASCADE only fires on hard-delete; soft-delete left
-- the children attached to a terminated parent, where they remained
-- queryable. ADR-0021 §IMP-007 keeps these tables out-of-scope for the
-- history sidecars, so a hard delete is the correct repair.
--
-- This migration deletes those orphan rows. It is idempotent and re-runnable.

DELETE FROM pods
 WHERE namespace_id IN (SELECT id FROM namespaces WHERE terminated_at IS NOT NULL);

DELETE FROM services
 WHERE namespace_id IN (SELECT id FROM namespaces WHERE terminated_at IS NOT NULL);

DELETE FROM ingresses
 WHERE namespace_id IN (SELECT id FROM namespaces WHERE terminated_at IS NOT NULL);

DELETE FROM persistent_volume_claims
 WHERE namespace_id IN (SELECT id FROM namespaces WHERE terminated_at IS NOT NULL);

DELETE FROM persistent_volumes
 WHERE cluster_id IN (SELECT id FROM clusters WHERE terminated_at IS NOT NULL);

-- +goose Down
-- Irreversible: the deleted orphan rows cannot be reconstructed without
-- an external backup. Down is a deliberate no-op.
SELECT 1;
