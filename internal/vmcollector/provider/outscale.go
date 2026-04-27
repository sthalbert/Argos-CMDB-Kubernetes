// Outscale provider — wraps the official osc-sdk-go/v2 SDK and maps
// osc.Vm into the canonical VM struct (ADR-0015 §7).

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	osc "github.com/outscale/osc-sdk-go/v2"
)

// roleTagKey is the Tags[Key] from which the Role field is derived for
// Outscale VMs. ADR-0015 §7 hard-codes ansible_group; future providers
// may use a different convention.
const roleTagKey = "ansible_group"

// Outscale is the Outscale-flavoured Provider implementation. Holds an
// instantiated osc.APIClient + the AK/SK pair injected in the request
// context as ContextAWSv4.
type Outscale struct {
	client    *osc.APIClient
	accessKey string
	secretKey string
	region    string
}

// NewOutscale builds an Outscale Provider for the given AK/SK/region.
// The default API endpoint pattern is api.{region}.outscale.com.
// Pass endpointURL to override (e.g. for Numspot, OUTSCALE-compatible
// private deployments, or a stub server).
func NewOutscale(accessKey, secretKey, region, endpointURL string) (*Outscale, error) {
	if accessKey == "" || secretKey == "" || region == "" {
		return nil, ErrMissingCredentials
	}
	if endpointURL == "" {
		endpointURL = fmt.Sprintf("https://api.%s.outscale.com/api/v1", region)
	}
	cfg := osc.NewConfiguration()
	cfg.UserAgent = "argos-vm-collector"
	cfg.Servers = osc.ServerConfigurations{
		{URL: endpointURL},
	}
	return &Outscale{
		client:    osc.NewAPIClient(cfg),
		accessKey: accessKey,
		secretKey: secretKey,
		region:    region,
	}, nil
}

// Kind returns "outscale".
func (o *Outscale) Kind() string { return "outscale" }

// ListVMs returns every VM visible under the configured AK/SK + region.
func (o *Outscale) ListVMs(ctx context.Context) ([]VM, error) {
	authCtx := context.WithValue(ctx, osc.ContextAWSv4, osc.AWSv4{
		AccessKey: o.accessKey,
		SecretKey: o.secretKey,
	})
	// Outscale rejects requests with a missing/null body ("3003: must be
	// a valid JSON object"). Pass an empty filter to force the SDK to
	// serialise `{}`.
	resp, httpResp, err := o.client.VmApi.ReadVms(authCtx).
		ReadVmsRequest(*osc.NewReadVmsRequest()).
		Execute()
	if err != nil {
		// The SDK's GenericOpenAPIError wraps the HTTP response body;
		// surface the first 512 bytes so 4xx responses ("filter X is
		// invalid", quota exceeded, …) reach the operator's logs.
		var body string
		if oapiErr, ok := err.(interface{ Body() []byte }); ok {
			b := oapiErr.Body()
			if len(b) > 512 {
				b = b[:512]
			}
			body = string(b)
		}
		status := ""
		if httpResp != nil {
			status = httpResp.Status
		}
		return nil, fmt.Errorf("outscale ReadVms (%s) %s: %w", status, body, err)
	}
	rawVMs := resp.GetVms()
	// Resolve AMI ids → human image names via a single ReadImages call
	// per tick (best-effort: a failure here logs a warning and leaves
	// ImageName empty, but the VMs still get upserted).
	imageNames := o.resolveImageNames(authCtx, rawVMs)
	out := make([]VM, 0, len(rawVMs))
	for i := range rawVMs {
		vm := mapOutscaleVM(&rawVMs[i], o.region)
		if name, ok := imageNames[vm.ImageID]; ok {
			vm.ImageName = name
		}
		out = append(out, vm)
	}
	return out, nil
}

// resolveImageNames batch-fetches human image names for every distinct
// ImageId in vms via a single ReadImages call filtered by ImageIds.
// Returns an empty map (never nil) on success or on error — the caller
// treats absence as "name not yet known", not as a fatal condition.
func (o *Outscale) resolveImageNames(authCtx context.Context, vms []osc.Vm) map[string]string {
	out := make(map[string]string)
	if len(vms) == 0 {
		return out
	}
	seen := make(map[string]struct{}, len(vms))
	ids := make([]string, 0, len(vms))
	for i := range vms {
		id := vms[i].GetImageId()
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return out
	}
	req := osc.NewReadImagesRequest()
	filter := osc.NewFiltersImage()
	filter.SetImageIds(ids)
	req.SetFilters(*filter)
	resp, _, err := o.client.ImageApi.ReadImages(authCtx).
		ReadImagesRequest(*req).
		Execute()
	if err != nil {
		// Best-effort — log via the SDK's wrapped error and continue
		// without image names. Operator-facing log lives in collector.
		return out
	}
	for _, img := range resp.GetImages() { //nolint:gocritic // rangeValCopy: osc.Image is SDK-owned; indexing would add unsafe coupling
		id := img.GetImageId()
		name := img.GetImageName()
		if id != "" && name != "" {
			out[id] = name
		}
	}
	return out
}

// mapOutscaleVM converts an osc.Vm into the canonical VM struct.
func mapOutscaleVM(v *osc.Vm, fallbackRegion string) VM {
	tags := flattenTags(v.GetTags())
	name := tags["Name"]
	if name == "" {
		name = v.GetVmId()
	}
	role := tags[roleTagKey] // empty string when absent — does not exclude the VM

	zone := ""
	providerAccountID := ""
	if v.HasPlacement() {
		p := v.GetPlacement()
		zone = p.GetSubregionName()
	}
	region := DeriveRegionFromZone(zone)
	if region == "" {
		region = fallbackRegion
	}

	creationDate := time.Time{}
	if cd := v.GetCreationDate(); cd != "" {
		if t, err := time.Parse(time.RFC3339, cd); err == nil {
			creationDate = t
		}
	}

	cpu, mem := ParseInstanceTypeCapacity(v.GetVmType())

	bootMode := ""
	if v.HasBootMode() {
		bootMode = string(v.GetBootMode())
	}

	// NICs — opaque JSON forwarded as-is. AccountId on the first NIC is
	// the canonical "provider account id" mapping.
	nicsJSON := jsonOrNull(v.GetNics())
	if v.HasNics() {
		nics := v.GetNics()
		if len(nics) > 0 {
			providerAccountID = nics[0].GetAccountId()
		}
	}

	sgJSON := jsonOrNull(v.GetSecurityGroups())
	bdJSON := jsonOrNull(v.GetBlockDeviceMappings())

	return VM{
		ProviderVMID:         v.GetVmId(),
		Name:                 name,
		Role:                 role,
		Tags:                 tags,
		PrivateIP:            v.GetPrivateIp(),
		PublicIP:             v.GetPublicIp(),
		PrivateDNSName:       v.GetPrivateDnsName(),
		InstanceType:         v.GetVmType(),
		Architecture:         v.GetArchitecture(),
		Zone:                 zone,
		Region:               region,
		ImageID:              v.GetImageId(),
		KeypairName:          v.GetKeypairName(),
		BootMode:             bootMode,
		VPCID:                v.GetNetId(),
		SubnetID:             v.GetSubnetId(),
		ProviderAccountID:    providerAccountID,
		ProviderCreationDate: creationDate,
		PowerState:           CanonicalPowerState(v.GetState()),
		StateReason:          v.GetStateReason(),
		DeletionProtection:   v.GetDeletionProtection(),
		CapacityCPU:          cpu,
		CapacityMemory:       mem,
		NICs:                 nicsJSON,
		SecurityGroups:       sgJSON,
		BlockDevices:         bdJSON,
		RootDeviceType:       v.GetRootDeviceType(),
		RootDeviceName:       v.GetRootDeviceName(),
	}
}

// flattenTags converts the SDK's []ResourceTag into a flat map.
func flattenTags(tags []osc.ResourceTag) map[string]string {
	if len(tags) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(tags))
	for i := range tags {
		k := tags[i].GetKey()
		v := tags[i].GetValue()
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out
}

// jsonOrNull marshals v to JSON; returns []byte("null") on error to
// keep the column non-empty.
func jsonOrNull(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return b
}

// CanonicalPowerState maps Outscale's vm.State to the canonical set
// stored in virtual_machines.power_state. The Outscale API uses
// `shutting-down` where AWS uses `terminating` — we normalise to the
// AWS spelling to keep a single vocabulary across providers.
func CanonicalPowerState(state string) string {
	switch state {
	case "pending":
		return "pending"
	case "running":
		return "running"
	case "stopping":
		return "stopping"
	case "stopped":
		return "stopped"
	case "shutting-down":
		return "terminating"
	case "terminated":
		return "terminated"
	}
	if state == "" {
		return "unknown"
	}
	return state
}

// zoneRE captures the Outscale zone naming convention: a region is the
// prefix of the zone with the trailing letter removed (eu-west-2b →
// eu-west-2). Multi-segment names with extra dashes (e.g.
// cloudgouv-eu-west-1) are supported by allowing repeated <prefix>-
// segments before the final numeric AZ index.
var zoneRE = regexp.MustCompile(`^((?:[a-z]+-)+\d+)[a-z]$`) //nolint:gochecknoglobals // immutable regex

// DeriveRegionFromZone returns the parent region from a zone name, or
// "" when the format is unrecognised.
func DeriveRegionFromZone(zone string) string {
	m := zoneRE.FindStringSubmatch(zone)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

// instanceTypeRE matches the TINA family suffix with explicit cN/rN/pN
// segments — e.g. tinav7.c4r8p2 → cpu=4, memory=8 GiB.
var instanceTypeRE = regexp.MustCompile(`\.c(\d+)r(\d+)p\d+`) //nolint:gochecknoglobals // immutable regex

// ParseInstanceTypeCapacity returns ("4", "8Gi") for a tinav7.c4r8p2
// instance type, or ("", "") for unrecognised families. Memory is
// returned in Gi suffix to match the Kubernetes node convention used
// elsewhere in the CMDB.
func ParseInstanceTypeCapacity(instanceType string) (cpu, mem string) {
	m := instanceTypeRE.FindStringSubmatch(instanceType)
	if len(m) != 3 {
		return "", ""
	}
	cpu = m[1]
	memGi, err := strconv.Atoi(m[2])
	if err != nil {
		return cpu, ""
	}
	return cpu, strconv.Itoa(memGi) + "Gi"
}

// hasPrefix is a tiny helper used by tests; kept to avoid importing
// the strings package twice.
func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) } //nolint:unused // exported via test helpers
