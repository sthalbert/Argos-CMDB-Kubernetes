-- +goose Up
-- ADR-0021 Phase 2: backfill one synthetic 'create' history row per existing
-- entity in the four K8s parent tables. ON CONFLICT DO NOTHING on the unique
-- partial index (entity_id) WHERE change_type = 'create' makes this
-- idempotent across re-runs.

INSERT INTO clusters_history (
    history_id, entity_id, valid_from, valid_to, change_type,
    actor_id, actor_kind,
    name, display_name, environment, provider, region,
    kubernetes_version, api_endpoint, labels,
    owner, criticality, notes, runbook_url, annotations,
    terminated_at, created_at, updated_at
)
SELECT
    gen_random_uuid(), id, created_at, NULL, 'create',
    NULL, 'system',
    name, display_name, environment, provider, region,
    kubernetes_version, api_endpoint, labels,
    owner, criticality, notes, runbook_url, annotations,
    terminated_at, created_at, updated_at
FROM clusters
ON CONFLICT DO NOTHING;

INSERT INTO namespaces_history (
    history_id, entity_id, valid_from, valid_to, change_type,
    actor_id, actor_kind,
    cluster_id, name, display_name, phase, labels,
    owner, criticality, notes, runbook_url, annotations,
    terminated_at, created_at, updated_at
)
SELECT
    gen_random_uuid(), id, created_at, NULL, 'create',
    NULL, 'system',
    cluster_id, name, display_name, phase, labels,
    owner, criticality, notes, runbook_url, annotations,
    terminated_at, created_at, updated_at
FROM namespaces
ON CONFLICT DO NOTHING;

INSERT INTO nodes_history (
    history_id, entity_id, valid_from, valid_to, change_type,
    actor_id, actor_kind,
    cluster_id, name, display_name, role,
    kubelet_version, kube_proxy_version, container_runtime_version,
    os_image, operating_system, kernel_version, architecture,
    internal_ip, external_ip, pod_cidr,
    provider_id, instance_type, zone,
    capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
    allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
    conditions, taints, unschedulable, ready,
    labels, owner, criticality, notes, runbook_url, annotations,
    hardware_model, terminated_at, created_at, updated_at
)
SELECT
    gen_random_uuid(), id, created_at, NULL, 'create',
    NULL, 'system',
    cluster_id, name, display_name, role,
    kubelet_version, kube_proxy_version, container_runtime_version,
    os_image, operating_system, kernel_version, architecture,
    internal_ip, external_ip, pod_cidr,
    provider_id, instance_type, zone,
    capacity_cpu, capacity_memory, capacity_pods, capacity_ephemeral_storage,
    allocatable_cpu, allocatable_memory, allocatable_pods, allocatable_ephemeral_storage,
    conditions, taints, unschedulable, ready,
    labels, owner, criticality, notes, runbook_url, annotations,
    hardware_model, terminated_at, created_at, updated_at
FROM nodes
ON CONFLICT DO NOTHING;

INSERT INTO workloads_history (
    history_id, entity_id, valid_from, valid_to, change_type,
    actor_id, actor_kind,
    namespace_id, kind, name, replicas, ready_replicas,
    labels, spec, containers,
    terminated_at, created_at, updated_at
)
SELECT
    gen_random_uuid(), id, created_at, NULL, 'create',
    NULL, 'system',
    namespace_id, kind, name, replicas, ready_replicas,
    labels, spec, containers,
    terminated_at, created_at, updated_at
FROM workloads
ON CONFLICT DO NOTHING;

-- +goose Down
-- Down removes all backfill rows identified by actor_kind='system' and
-- change_type='create'. This is safe for a clean environment; a production
-- rollback should be done manually.
DELETE FROM workloads_history  WHERE change_type = 'create' AND actor_kind = 'system';
DELETE FROM nodes_history      WHERE change_type = 'create' AND actor_kind = 'system';
DELETE FROM namespaces_history WHERE change_type = 'create' AND actor_kind = 'system';
DELETE FROM clusters_history   WHERE change_type = 'create' AND actor_kind = 'system';
