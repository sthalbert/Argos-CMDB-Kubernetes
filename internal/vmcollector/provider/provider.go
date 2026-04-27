// Package provider defines the cloud-provider seam used by the
// argos-vm-collector binary (ADR-0015 §7). One file per implementation;
// only outscale.go is shipped in v1.
package provider

import (
	"context"
	"encoding/json"
	"time"
)

// VM is the canonical, provider-neutral view of a cloud VM. The
// collector boundary maps each provider's native shape into this struct;
// the rest of the pipeline (filter, apiclient, server-side dedup) only
// reads VM, never the provider SDK types.
type VM struct {
	ProviderVMID         string
	Name                 string            // from Tags["Name"], fallback ProviderVMID
	Role                 string            // from Tags["ansible_group"] for Outscale; "" when absent
	Tags                 map[string]string // raw provider tags flattened
	PrivateIP            string
	PublicIP             string
	PrivateDNSName       string
	InstanceType         string
	Architecture         string
	Zone                 string
	Region               string
	ImageID              string
	ImageName            string // resolved via ReadImages(ImageId) — best-effort, empty when unavailable
	KeypairName          string
	BootMode             string
	VPCID                string
	SubnetID             string
	ProviderAccountID    string
	ProviderCreationDate time.Time
	PowerState           string // canonical: pending|running|stopping|stopped|terminating|terminated
	StateReason          string
	DeletionProtection   bool
	KernelVersion        string // empty without an in-guest agent
	OperatingSystem      string // empty without an in-guest agent
	CapacityCPU          string
	CapacityMemory       string
	NICs                 json.RawMessage // forwarded as opaque JSON
	SecurityGroups       json.RawMessage // forwarded as opaque JSON
	BlockDevices         json.RawMessage // forwarded as opaque JSON
	RootDeviceType       string
	RootDeviceName       string
}

// Provider is the interface argos-vm-collector consumes. New cloud
// providers slot in by adding a new file in this package + a
// constructor entry in the collector wiring.
type Provider interface {
	// Kind returns the provider name used for logging and the
	// cloud_accounts.provider column ("outscale", "aws", …).
	Kind() string

	// ListVMs returns every VM visible under the configured
	// account/region. Pagination + retries are the implementation's
	// concern.
	ListVMs(ctx context.Context) ([]VM, error)
}
