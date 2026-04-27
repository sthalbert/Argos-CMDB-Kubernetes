package api

// Cloud-account and virtual-machine domain types (ADR-0015). Hand-written
// because the related endpoints are mounted as hand-written handlers
// alongside the codegen mux (mirrors the settings + impact pattern), so
// these types are not in the OpenAPI spec.

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/secrets"
)

// CloudAccount status constants — matches the CHECK constraint on the
// cloud_accounts table.
const (
	CloudAccountStatusPendingCredentials = "pending_credentials"
	CloudAccountStatusActive             = "active"
	CloudAccountStatusError              = "error"
	CloudAccountStatusDisabled           = "disabled"
)

// CloudAccount is the persisted view of a cloud-provider account
// registered in argosd. The plaintext SK is intentionally absent — it
// lives only in the encrypted column and is only ever returned by
// GetCloudAccountCredentials, which decrypts it on the way out.
type CloudAccount struct {
	ID          uuid.UUID         `json:"id"`
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Region      string            `json:"region"`
	Status      string            `json:"status"`
	AccessKey   *string           `json:"access_key,omitempty"`
	LastSeenAt  *time.Time        `json:"last_seen_at,omitempty"`
	LastError   *string           `json:"last_error,omitempty"`
	LastErrorAt *time.Time        `json:"last_error_at,omitempty"`
	Owner       *string           `json:"owner,omitempty"`
	Criticality *string           `json:"criticality,omitempty"`
	Notes       *string           `json:"notes,omitempty"`
	RunbookURL  *string           `json:"runbook_url,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	DisabledAt  *time.Time        `json:"disabled_at,omitempty"`
}

// CloudAccountUpsert carries the fields used by UpsertCloudAccount
// (idempotent first-contact registration). Curated metadata is set
// separately via UpdateCloudAccount.
type CloudAccountUpsert struct {
	Provider string
	Name     string
	Region   string
}

// CloudAccountPatch is the merge-patch view for UpdateCloudAccount.
// Nil fields are left untouched. Status / LastSeenAt / LastError /
// LastErrorAt are admin-only fields here; collector heartbeats go
// through UpdateCloudAccountStatus which gates allowed transitions.
type CloudAccountPatch struct {
	Name        *string
	Region      *string
	Owner       *string
	Criticality *string
	Notes       *string
	RunbookURL  *string
	Annotations *map[string]string
	Status      *string
	LastSeenAt  *time.Time
	LastError   *string
	LastErrorAt *time.Time
}

// VirtualMachine is the persisted view of a non-Kubernetes platform
// VM. Mirrors the enriched nodes shape where it makes sense, drops the
// K8s-specific fields, and adds the cloud-native columns from the
// rich provider payload (image, keypair, VPC, NICs, SGs, block devices).
type VirtualMachine struct {
	ID                   uuid.UUID         `json:"id"`
	CloudAccountID       uuid.UUID         `json:"cloud_account_id"`
	ProviderVMID         string            `json:"provider_vm_id"`
	Name                 string            `json:"name"`
	DisplayName          *string           `json:"display_name,omitempty"`
	Role                 *string           `json:"role,omitempty"`
	PrivateIP            *string           `json:"private_ip,omitempty"`
	PublicIP             *string           `json:"public_ip,omitempty"`
	PrivateDNSName       *string           `json:"private_dns_name,omitempty"`
	VPCID                *string           `json:"vpc_id,omitempty"`
	SubnetID             *string           `json:"subnet_id,omitempty"`
	NICs                 json.RawMessage   `json:"nics,omitempty"`
	SecurityGroups       json.RawMessage   `json:"security_groups,omitempty"`
	InstanceType         *string           `json:"instance_type,omitempty"`
	Architecture         *string           `json:"architecture,omitempty"`
	Zone                 *string           `json:"zone,omitempty"`
	Region               *string           `json:"region,omitempty"`
	ImageID              *string           `json:"image_id,omitempty"`
	ImageName            *string           `json:"image_name,omitempty"`
	KeypairName          *string           `json:"keypair_name,omitempty"`
	BootMode             *string           `json:"boot_mode,omitempty"`
	ProviderAccountID    *string           `json:"provider_account_id,omitempty"`
	ProviderCreationDate *time.Time        `json:"provider_creation_date,omitempty"`
	PowerState           string            `json:"power_state"`
	StateReason          *string           `json:"state_reason,omitempty"`
	Ready                bool              `json:"ready"`
	DeletionProtection   bool              `json:"deletion_protection"`
	KernelVersion        *string           `json:"kernel_version,omitempty"`
	OperatingSystem      *string           `json:"operating_system,omitempty"`
	CapacityCPU          *string           `json:"capacity_cpu,omitempty"`
	CapacityMemory       *string           `json:"capacity_memory,omitempty"`
	BlockDevices         json.RawMessage   `json:"block_devices,omitempty"`
	RootDeviceType       *string           `json:"root_device_type,omitempty"`
	RootDeviceName       *string           `json:"root_device_name,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
	Labels               map[string]string `json:"labels,omitempty"`
	Annotations          map[string]string `json:"annotations,omitempty"`
	Owner                *string           `json:"owner,omitempty"`
	Criticality          *string           `json:"criticality,omitempty"`
	Notes                *string           `json:"notes,omitempty"`
	RunbookURL           *string           `json:"runbook_url,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`
	LastSeenAt           time.Time         `json:"last_seen_at"`
	TerminatedAt         *time.Time        `json:"terminated_at,omitempty"`
}

// VirtualMachineUpsert is the collector-side payload for upserting
// a VM. Mirrors VirtualMachine minus curated / lifecycle fields.
type VirtualMachineUpsert struct {
	CloudAccountID       uuid.UUID
	ProviderVMID         string
	Name                 string
	Role                 *string
	PrivateIP            *string
	PublicIP             *string
	PrivateDNSName       *string
	VPCID                *string
	SubnetID             *string
	NICs                 json.RawMessage
	SecurityGroups       json.RawMessage
	InstanceType         *string
	Architecture         *string
	Zone                 *string
	Region               *string
	ImageID              *string
	ImageName            *string
	KeypairName          *string
	BootMode             *string
	ProviderAccountID    *string
	ProviderCreationDate *time.Time
	PowerState           string
	StateReason          *string
	Ready                bool
	DeletionProtection   bool
	KernelVersion        *string
	OperatingSystem      *string
	CapacityCPU          *string
	CapacityMemory       *string
	BlockDevices         json.RawMessage
	RootDeviceType       *string
	RootDeviceName       *string
	Tags                 map[string]string
	Labels               map[string]string
}

// VirtualMachinePatch is the merge-patch for UpdateVirtualMachine.
// Curated-only — the collector path goes through UpsertVirtualMachine.
type VirtualMachinePatch struct {
	DisplayName *string
	Role        *string
	Owner       *string
	Criticality *string
	Notes       *string
	RunbookURL  *string
	Annotations *map[string]string
}

// VirtualMachineListFilter collects the optional filters for ListVirtualMachines.
type VirtualMachineListFilter struct {
	CloudAccountID    *uuid.UUID
	Region            *string
	Role              *string
	PowerState        *string
	IncludeTerminated bool
}

// _ enforces secrets.Ciphertext stays imported even when no method
// on this file references it directly — the Store interface methods
// declared in store.go pick it up via the import side-effect.
var _ = secrets.Ciphertext{}
