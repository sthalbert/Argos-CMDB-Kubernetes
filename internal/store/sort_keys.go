// sort_keys.go — shared sort-key string constants for the sortSpec/sortVal
// switches in each pg_*.go file. Using named constants keeps goconst happy
// and makes typos in key names a compile-time error.
package store

const (
	sortKeyName              = "name"
	sortKeyCreatedAt         = "created_at"
	sortKeyUpdatedAt         = "updated_at"
	sortKeyLastSeenAt        = "last_seen_at"
	sortKeyRegion            = "region"
	sortKeyRole              = "role"
	sortKeyProvider          = "provider"
	sortKeyEnvironment       = "environment"
	sortKeyKubernetesVersion = "kubernetes_version"
	sortKeyStatus            = "status"
	sortKeyPowerState        = "power_state"
	sortKeyZone              = "zone"
	sortKeyInstanceType      = "instance_type"
	sortKeyImageName         = "image_name"
	sortKeyPhase             = "phase"
	sortKeyStorageClassName  = "storage_class_name"
	sortKeyCsiDriver         = "csi_driver"
	sortKeyCapacity          = "capacity"
	sortKeyRequestedStorage  = "requested_storage"
	sortKeyIngressClassName  = "ingress_class_name"
	sortKeyType              = "type"
	sortKeyClusterIP         = "cluster_ip"
	sortKeyUsername          = "username"
	sortKeyLastUsedAt        = "last_used_at"
	sortKeyExpiresAt         = "expires_at"

	// Audit sort keys.
	sortKeyOccurredAt    = "occurred_at"
	sortKeyAction        = "action"
	sortKeyResourceType  = "resource_type"
	sortKeyActorUsername = "actor_username"
	sortKeySource        = "source"

	// Application / ApplicationBlock sort keys.
	sortKeyOwner       = "owner"
	sortKeyCriticality = "criticality"

	// Security group sort keys.
	sortKeyVPCID = "vpc_id"

	// Shared reconcile sort key (security groups, network policies).
	sortKeyReconcileSeenAt = "reconcile_seen_at"

	// Kyverno cluster policy sort keys (sortKeyAction and sortKeyResourceType
	// already declared under Audit above — they share the same string value).
	sortKeyBackground    = "background"
	sortKeySeverity      = "severity"
	sortKeyRulesCount    = "rules_count"
	sortKeyFailurePolicy = "failure_policy"
	sortKeyCategory      = "category"
	sortKeyReady         = "ready"
	sortKeyScope         = "scope"

	// Policy report sort keys.
	sortKeyScopeKind    = "scope_kind"
	sortKeyScopeName    = "scope_name"
	sortKeySummaryPass  = "summary_pass"
	sortKeySummaryFail  = "summary_fail"
	sortKeySummaryWarn  = "summary_warn"
	sortKeySummaryError = "summary_error"
	sortKeySummarySkip  = "summary_skip"
)
