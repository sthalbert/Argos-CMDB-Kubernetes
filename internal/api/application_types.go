package api

// Application + ApplicationBlock domain types (ADR-0029). Hand-written
// because the related endpoints are mounted as hand-written handlers
// alongside the codegen mux (mirrors the settings + cloud pattern), so
// these types are not in the OpenAPI spec.

import (
	"time"

	"github.com/google/uuid"
)

// ApplicationBlock is the SNC two-level grouping above Application
// (ADR-0029 §1). Operator-curated; never written by collectors.
type ApplicationBlock struct {
	ID          uuid.UUID         `json:"id"`
	Name        string            `json:"name"`
	DisplayName *string           `json:"display_name,omitempty"`
	Description *string           `json:"description,omitempty"`
	Owner       *string           `json:"owner,omitempty"`
	Notes       *string           `json:"notes,omitempty"`
	Annotations map[string]string `json:"annotations"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`

	// Denormalised for list responses (ADR-0027 pattern).
	ApplicationCount int `json:"application_count,omitempty"`
}

// ApplicationBlockCreate is the body for POST /v1/application-blocks.
type ApplicationBlockCreate struct {
	Name        string             `json:"name"`
	DisplayName *string            `json:"display_name,omitempty"`
	Description *string            `json:"description,omitempty"`
	Owner       *string            `json:"owner,omitempty"`
	Notes       *string            `json:"notes,omitempty"`
	Annotations *map[string]string `json:"annotations,omitempty"`
}

// ApplicationBlockPatch is the merge-patch body for PATCH /v1/application-blocks/{id}.
type ApplicationBlockPatch struct {
	DisplayName *string            `json:"display_name,omitempty"`
	Description *string            `json:"description,omitempty"`
	Owner       *string            `json:"owner,omitempty"`
	Notes       *string            `json:"notes,omitempty"`
	Annotations *map[string]string `json:"annotations,omitempty"`
}

// ApplicationBlockListFilter is the GET /v1/application-blocks query shape.
type ApplicationBlockListFilter struct {
	Name  *string
	Owner *string
}

// Application is the first-class entity introduced by ADR-0029.
type Application struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	DisplayName        *string    `json:"display_name,omitempty"`
	Description        *string    `json:"description,omitempty"`
	ApplicationBlockID *uuid.UUID `json:"application_block_id,omitempty"`
	// Denormalised on list responses (ADR-0027).
	ApplicationBlockName *string `json:"application_block_name,omitempty"`

	Owner       *string           `json:"owner,omitempty"`
	Criticality *string           `json:"criticality,omitempty"`
	Notes       *string           `json:"notes,omitempty"`
	RunbookURL  *string           `json:"runbook_url,omitempty"`
	Annotations map[string]string `json:"annotations"`

	SecDisponibilite   *int    `json:"sec_disponibilite,omitempty"`
	SecIntegrite       *int    `json:"sec_integrite,omitempty"`
	SecConfidentialite *int    `json:"sec_confidentialite,omitempty"`
	SecTracabilite     *int    `json:"sec_tracabilite,omitempty"`
	SecNotes           *string `json:"sec_notes,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	MemberCounts ApplicationMemberCounts `json:"member_counts"`
}

// ApplicationMemberCounts is the small summary shipped with every
// application GET / list row.
type ApplicationMemberCounts struct {
	Workloads       int `json:"workloads"`
	VirtualMachines int `json:"virtual_machines"`
	VMApplications  int `json:"vm_applications"`
}

// ApplicationCreate is the body for POST /v1/applications.
type ApplicationCreate struct {
	Name                 string             `json:"name"`
	DisplayName          *string            `json:"display_name,omitempty"`
	Description          *string            `json:"description,omitempty"`
	ApplicationBlockID   *uuid.UUID         `json:"application_block_id,omitempty"`
	ApplicationBlockName *string            `json:"application_block_name,omitempty"`
	Owner                *string            `json:"owner,omitempty"`
	Criticality          *string            `json:"criticality,omitempty"`
	Notes                *string            `json:"notes,omitempty"`
	RunbookURL           *string            `json:"runbook_url,omitempty"`
	Annotations          *map[string]string `json:"annotations,omitempty"`
	SecDisponibilite     *int               `json:"sec_disponibilite,omitempty"`
	SecIntegrite         *int               `json:"sec_integrite,omitempty"`
	SecConfidentialite   *int               `json:"sec_confidentialite,omitempty"`
	SecTracabilite       *int               `json:"sec_tracabilite,omitempty"`
	SecNotes             *string            `json:"sec_notes,omitempty"`
}

// ApplicationPatch is the merge-patch body for PATCH /v1/applications/{id}.
// nil means "leave alone" on every field. Unlinking from a block is done
// by deleting the application or by re-PATCHing with a different
// application_block_id (the v1 surface does not expose an explicit
// "set block to null" path).
type ApplicationPatch struct {
	DisplayName          *string            `json:"display_name,omitempty"`
	Description          *string            `json:"description,omitempty"`
	ApplicationBlockID   *uuid.UUID         `json:"application_block_id,omitempty"`
	ApplicationBlockName *string            `json:"application_block_name,omitempty"`
	Owner                *string            `json:"owner,omitempty"`
	Criticality          *string            `json:"criticality,omitempty"`
	Notes                *string            `json:"notes,omitempty"`
	RunbookURL           *string            `json:"runbook_url,omitempty"`
	Annotations          *map[string]string `json:"annotations,omitempty"`
	SecDisponibilite     *int               `json:"sec_disponibilite,omitempty"`
	SecIntegrite         *int               `json:"sec_integrite,omitempty"`
	SecConfidentialite   *int               `json:"sec_confidentialite,omitempty"`
	SecTracabilite       *int               `json:"sec_tracabilite,omitempty"`
	SecNotes             *string            `json:"sec_notes,omitempty"`
}

// ApplicationListFilter is the GET /v1/applications query shape.
type ApplicationListFilter struct {
	Name                 *string // case-insensitive substring on name + display_name
	ApplicationBlockID   *uuid.UUID
	ApplicationBlockName *string
	Criticality          *string
	HasDICT              *bool
	DICTMin              *int // MAX(sec_*) >= N
}

// ApplicationMemberKind enumerates the member types of an Application.
type ApplicationMemberKind string

// ApplicationMemberKind values surfaced by GET /v1/applications/{id}/members.
const (
	ApplicationMemberKindWorkload       ApplicationMemberKind = "workload"
	ApplicationMemberKindVirtualMachine ApplicationMemberKind = "virtual_machine"
	ApplicationMemberKindVMApplication  ApplicationMemberKind = "vm_application"
)

// ApplicationMember is a single row from GET /v1/applications/{id}/members.
type ApplicationMember struct {
	Kind     ApplicationMemberKind `json:"kind"`
	ID       string                `json:"id"` // UUID for workload/VM; "<vm_id>#<product>#<name>" for vm_application
	Name     string                `json:"name"`
	Parent   *ApplicationMemberRef `json:"parent,omitempty"`
	Linked   time.Time             `json:"linked_at"`
	LinkedBy string                `json:"linked_by"`
}

// ApplicationMemberRef points back to the parent entity of an
// ApplicationMember row (e.g. the workload's namespace, the VM that owns
// a vm_application). nil on top-level members.
type ApplicationMemberRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NormalizeApplicationName normalises an Application or ApplicationBlock
// name for use as a stable key: trim → lowercase → collapse runs of
// whitespace, underscores, and hyphens into single hyphens. Delegates to
// NormalizeProductName (cloud_types.go) so Application names follow the
// same kebab-case convention as VMApplication.Product (ADR-0019).
func NormalizeApplicationName(s string) string {
	return NormalizeProductName(s)
}
