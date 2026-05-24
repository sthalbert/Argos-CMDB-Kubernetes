package timetravel

// KindCluster, KindNamespace, KindNode, KindWorkload are the string
// identifiers used as keys into WatchedFields and history table names.
const (
	KindCluster   = "clusters"
	KindNamespace = "namespaces"
	KindNode      = "nodes"
	KindWorkload  = "workloads"
)

// WatchedFields maps each entity kind to the set of column names that, when
// changed, trigger a new history row. Columns not in the watched set (and not
// in ExcludedFields) must be classified in a test so that adding a parent
// column without updating history is a CI failure.
//
// Decision rationale per ADR-0021 §3:
//   - clusters:   version + curated metadata + topology scalars + terminated_at.
//     Excluded: labels (high churn), display_name, api_endpoint.
//   - namespaces: curated metadata + terminated_at.
//     Excluded: labels, display_name, phase.
//   - nodes:      configuration + identity + curated metadata + lifecycle flags.
//     Excluded: internal_ip, external_ip, pod_cidr (transient in cloud),
//     conditions, taints (flap-prone JSONB), capacity_*/allocatable_* (numeric drift).
//   - workloads:  kind + spec JSONB + curated metadata (including the
//     curator-only application_id soft-pointer, ADR-0029 §7) + terminated_at.
//     Excluded: labels, replicas, ready_replicas, containers (collector noise).
var WatchedFields = map[string][]string{
	KindCluster: {
		"kubernetes_version",
		"provider",
		"region",
		"environment",
		"owner",
		"criticality",
		"notes",
		"runbook_url",
		"annotations",
		"terminated_at",
	},
	KindNamespace: {
		"owner",
		"criticality",
		"notes",
		"runbook_url",
		"annotations",
		"terminated_at",
	},
	KindNode: {
		"role",
		"kubelet_version",
		"kernel_version",
		"operating_system",
		"container_runtime_version",
		"kube_proxy_version",
		"instance_type",
		"zone",
		"hardware_model",
		"owner",
		"criticality",
		"notes",
		"runbook_url",
		"annotations",
		"ready",
		"unschedulable",
		"terminated_at",
	},
	KindWorkload: {
		"kind",
		"spec",
		"owner",
		"criticality",
		"notes",
		"runbook_url",
		"annotations",
		"application_id",
		"terminated_at",
	},
}

// ExcludedFields maps each kind to the set of columns that are intentionally
// NOT watched and NOT in WatchedFields. Any parent-table column that is neither
// watched nor excluded causes TestWatchedFields_AllColumnsClassified to fail,
// enforcing classification of new columns at code-review time.
var ExcludedFields = map[string][]string{
	KindCluster: {
		// envelope / identity — not watched, not interesting for cartography
		"id",
		"name",
		"created_at",
		"updated_at",
		// high-churn or low-signal
		"display_name",
		"api_endpoint",
		"labels",
	},
	KindNamespace: {
		"id",
		"cluster_id",
		"name",
		"created_at",
		"updated_at",
		"display_name",
		"phase",
		"labels",
	},
	KindNode: {
		"id",
		"cluster_id",
		"name",
		"created_at",
		"updated_at",
		"display_name",
		"architecture",
		"os_image",
		"labels",
		// transient in cloud topologies
		"internal_ip",
		"external_ip",
		"pod_cidr",
		"provider_id",
		// flap-prone JSONB
		"conditions",
		"taints",
		// numeric drift unrelated to configuration
		"capacity_cpu",
		"capacity_memory",
		"capacity_pods",
		"capacity_ephemeral_storage",
		"allocatable_cpu",
		"allocatable_memory",
		"allocatable_pods",
		"allocatable_ephemeral_storage",
	},
	KindWorkload: {
		"id",
		"namespace_id",
		"name",
		"created_at",
		"updated_at",
		"labels",
		// collector-driven noise
		"replicas",
		"ready_replicas",
		"containers",
	},
}
