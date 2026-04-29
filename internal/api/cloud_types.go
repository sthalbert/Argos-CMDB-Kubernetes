package api

// Cloud-account and virtual-machine domain types (ADR-0015). Hand-written
// because the related endpoints are mounted as hand-written handlers
// alongside the codegen mux (mirrors the settings + impact pattern), so
// these types are not in the OpenAPI spec.

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/secrets"
)

// VMApplication is one entry in a virtual machine's `applications` JSONB
// column (ADR-0019). Operators record what's running on a platform VM
// (Vault, DNS, Cyberwatch, …) and the EOL enricher uses `Product` +
// `Version` to look up lifecycle data on endoflife.date.
//
// `Product` is normalized server-side (NormalizeProductName) so that
// "Hashicorp Vault", "hashicorp-vault", and "Vault" deduplicate to the
// same key. `AddedAt` and `AddedBy` are server-stamped on insert and
// preserved across PATCH calls when (product, version, name) is unchanged.
type VMApplication struct {
	Product string    `json:"product"`
	Version string    `json:"version"`
	Name    *string   `json:"name,omitempty"`
	Notes   *string   `json:"notes,omitempty"`
	AddedAt time.Time `json:"added_at"`
	AddedBy string    `json:"added_by"`
}

// NormalizeProductName collapses operator-typed product names into a
// stable kebab-case key. Trim, lowercase, collapse runs of whitespace
// and underscores into single hyphens. The result is what gets indexed,
// matched, and used as the suffix in `argos.io/eol.<product>` annotations.
func NormalizeProductName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevHyphen := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '_', '-':
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		default:
			b.WriteRune(r)
			prevHyphen = false
		}
	}
	out := b.String()
	return strings.TrimRight(out, "-")
}

// VMApplicationKey returns a stable identity key for diffing PATCH input
// against the existing applications list — `(product, version, name)` so
// two `vault@1.15.4` entries with different `name` labels are distinct.
func VMApplicationKey(a *VMApplication) string {
	name := ""
	if a.Name != nil {
		name = *a.Name
	}
	return a.Product + "|" + a.Version + "|" + name
}

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
	Applications         []VMApplication   `json:"applications"`
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
//
// Applications has replace-not-merge semantics (ADR-0019 §4): a non-nil
// pointer replaces the entire list. The handler diffs the input against
// the stored list to preserve `added_at` / `added_by` on entries whose
// (product, version, name) key is unchanged, and stamps fresh values on
// new entries. The store sees the final list.
type VirtualMachinePatch struct {
	DisplayName  *string
	Role         *string
	Owner        *string
	Criticality  *string
	Notes        *string
	RunbookURL   *string
	Annotations  *map[string]string
	Applications *[]VMApplication
}

// VirtualMachineListFilter collects the optional filters for ListVirtualMachines.
//
// Name and Image are bounded substring filters (LIKE-escape applied at the
// SQL layer). CloudAccountName resolves via an inner subquery against the
// cloud_accounts UNIQUE index. Application is a JSONB containment filter
// matching any entry whose normalized product equals the given value.
type VirtualMachineListFilter struct {
	CloudAccountID   *uuid.UUID
	CloudAccountName *string
	Region           *string
	Role             *string
	PowerState       *string
	Name             *string
	Image            *string
	Application      *string
	// ApplicationVersion narrows Application to a specific version. Only
	// honoured when Application is also set; ignored otherwise so callers
	// can't bypass the product-name normalization. Matches the JSONB entry
	// (product, version) tuple via containment.
	ApplicationVersion *string
	IncludeTerminated  bool
}

// VMApplicationDistinct is one row of the distinct-applications response.
// `Versions` is the sorted, deduplicated list of versions seen for the
// product across every non-terminated VM. Drives the cascading
// product → version dropdown in the VM list UI.
type VMApplicationDistinct struct {
	Product  string   `json:"product"`
	Versions []string `json:"versions"`
}

// _ enforces secrets.Ciphertext stays imported even when no method
// on this file references it directly — the Store interface methods
// declared in store.go pick it up via the import side-effect.
var _ = secrets.Ciphertext{}
