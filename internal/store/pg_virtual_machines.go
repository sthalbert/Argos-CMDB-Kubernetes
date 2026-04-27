package store

// PostgreSQL implementation of the virtual_machines methods on api.Store
// (ADR-0015). Server-side dedup against nodes.provider_id ensures a VM
// already inventoried as a Kubernetes node can never appear in the
// virtual_machines table.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/argos/internal/api"
)

// vmColumns is the canonical SELECT/INSERT column order for the
// virtual_machines table — kept as one constant so adding a column is
// a three-line change (const + values + scan).
const vmColumns = `id, cloud_account_id,
	provider_vm_id, name, display_name, role,
	private_ip, public_ip, private_dns_name, vpc_id, subnet_id,
	nics, security_groups,
	instance_type, architecture, zone, region,
	image_id, image_name, keypair_name, boot_mode, provider_account_id, provider_creation_date,
	power_state, state_reason, ready, deletion_protection,
	kernel_version, operating_system,
	capacity_cpu, capacity_memory,
	block_devices, root_device_type, root_device_name,
	tags, labels, annotations,
	owner, criticality, notes, runbook_url,
	created_at, updated_at, last_seen_at, terminated_at`

// UpsertVirtualMachine inserts a new VM or updates the existing row
// keyed on (cloud_account_id, provider_vm_id). Server-side dedup
// against nodes.provider_id: returns ErrConflict when the VM is
// already known as a Kubernetes node.
//
//nolint:gocritic // hugeParam: Store interface requires value param; insert columns are inherently long
func (p *PG) UpsertVirtualMachine(ctx context.Context, in api.VirtualMachineUpsert) (api.VirtualMachine, error) {
	// Server-side dedup. Outscale's CCM sets node.spec.providerID to a
	// string containing the VmId, e.g. "aws:///<az>/i-96fff41b". A
	// substring match catches every observed format.
	//
	// Defense in depth (ADR-0015 H1):
	//   - Validate the value shape before it reaches SQL — reject any
	//     character not used by real cloud-provider VM IDs. A
	//     vm-collector PAT could otherwise post `%` or `_` to abuse the
	//     LIKE wildcard semantics (force every dedup query to match,
	//     which would 409 every upsert and tombstone the account on
	//     reconciliation).
	//   - Escape LIKE metacharacters in the parameterised value with an
	//     explicit ESCAPE clause, so even if a future caller bypasses
	//     the validator, the SQL is still safe.
	if in.ProviderVMID != "" {
		if !validProviderVMID(in.ProviderVMID) {
			return api.VirtualMachine{}, fmt.Errorf("provider_vm_id %q contains disallowed characters: %w", in.ProviderVMID, api.ErrConflict)
		}
		var existsCount int
		if err := p.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM nodes WHERE provider_id LIKE '%' || $1 || '%' ESCAPE '\'`,
			escapeLIKE(in.ProviderVMID),
		).Scan(&existsCount); err != nil {
			return api.VirtualMachine{}, fmt.Errorf("dedup check against nodes: %w", err)
		}
		if existsCount > 0 {
			return api.VirtualMachine{}, fmt.Errorf("provider_vm_id %q already inventoried as a node: %w", in.ProviderVMID, api.ErrConflict)
		}
	}

	id := uuid.New()
	now := time.Now().UTC()

	nicsJSON := jsonOrEmptyArray(in.NICs)
	sgJSON := jsonOrEmptyArray(in.SecurityGroups)
	bdJSON := jsonOrEmptyArray(in.BlockDevices)

	tagsJSON, err := marshalStringMap(in.Tags)
	if err != nil {
		return api.VirtualMachine{}, fmt.Errorf("marshal tags: %w", err)
	}
	labelsJSON, err := marshalStringMap(in.Labels)
	if err != nil {
		return api.VirtualMachine{}, fmt.Errorf("marshal labels: %w", err)
	}

	const q = `
		INSERT INTO virtual_machines (
			id, cloud_account_id,
			provider_vm_id, name, display_name, role,
			private_ip, public_ip, private_dns_name, vpc_id, subnet_id,
			nics, security_groups,
			instance_type, architecture, zone, region,
			image_id, image_name, keypair_name, boot_mode, provider_account_id, provider_creation_date,
			power_state, state_reason, ready, deletion_protection,
			kernel_version, operating_system,
			capacity_cpu, capacity_memory,
			block_devices, root_device_type, root_device_name,
			tags, labels, annotations,
			created_at, updated_at, last_seen_at
		) VALUES (
			$1, $2,
			$3, $4, NULL, $5,
			$6, $7, $8, $9, $10,
			$11, $12,
			$13, $14, $15, $16,
			$17, $18, $19, $20, $21, $22,
			$23, $24, $25, $26,
			$27, $28,
			$29, $30,
			$31, $32, $33,
			$34, $35, '{}'::jsonb,
			$36, $36, $36
		)
		ON CONFLICT (cloud_account_id, provider_vm_id) DO UPDATE
		SET name                  = EXCLUDED.name,
		    role                  = COALESCE(virtual_machines.role, EXCLUDED.role),
		    private_ip            = EXCLUDED.private_ip,
		    public_ip             = EXCLUDED.public_ip,
		    private_dns_name      = EXCLUDED.private_dns_name,
		    vpc_id                = EXCLUDED.vpc_id,
		    subnet_id             = EXCLUDED.subnet_id,
		    nics                  = EXCLUDED.nics,
		    security_groups       = EXCLUDED.security_groups,
		    instance_type         = EXCLUDED.instance_type,
		    architecture          = EXCLUDED.architecture,
		    zone                  = EXCLUDED.zone,
		    region                = EXCLUDED.region,
		    image_id              = EXCLUDED.image_id,
		    image_name            = EXCLUDED.image_name,
		    keypair_name          = EXCLUDED.keypair_name,
		    boot_mode             = EXCLUDED.boot_mode,
		    provider_account_id   = EXCLUDED.provider_account_id,
		    provider_creation_date= EXCLUDED.provider_creation_date,
		    power_state           = EXCLUDED.power_state,
		    state_reason          = EXCLUDED.state_reason,
		    ready                 = EXCLUDED.ready,
		    deletion_protection   = EXCLUDED.deletion_protection,
		    kernel_version        = EXCLUDED.kernel_version,
		    operating_system      = EXCLUDED.operating_system,
		    capacity_cpu          = EXCLUDED.capacity_cpu,
		    capacity_memory       = EXCLUDED.capacity_memory,
		    block_devices         = EXCLUDED.block_devices,
		    root_device_type      = EXCLUDED.root_device_type,
		    root_device_name      = EXCLUDED.root_device_name,
		    tags                  = EXCLUDED.tags,
		    labels                = EXCLUDED.labels,
		    updated_at            = EXCLUDED.updated_at,
		    last_seen_at          = EXCLUDED.last_seen_at,
		    terminated_at         = NULL
		RETURNING id
	`
	var rowID uuid.UUID
	err = p.pool.QueryRow(ctx, q,
		id, in.CloudAccountID,
		in.ProviderVMID, in.Name, in.Role,
		in.PrivateIP, in.PublicIP, in.PrivateDNSName, in.VPCID, in.SubnetID,
		nicsJSON, sgJSON,
		in.InstanceType, in.Architecture, in.Zone, in.Region,
		in.ImageID, in.ImageName, in.KeypairName, in.BootMode, in.ProviderAccountID, in.ProviderCreationDate,
		in.PowerState, in.StateReason, in.Ready, in.DeletionProtection,
		in.KernelVersion, in.OperatingSystem,
		in.CapacityCPU, in.CapacityMemory,
		bdJSON, in.RootDeviceType, in.RootDeviceName,
		tagsJSON, labelsJSON,
		now,
	).Scan(&rowID)
	if err != nil {
		return api.VirtualMachine{}, fmt.Errorf("upsert virtual machine: %w", err)
	}
	return p.GetVirtualMachine(ctx, rowID)
}

// GetVirtualMachine fetches a VM by id.
func (p *PG) GetVirtualMachine(ctx context.Context, id uuid.UUID) (api.VirtualMachine, error) {
	q := `SELECT ` + vmColumns + ` FROM virtual_machines WHERE id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	vm, err := scanVirtualMachine(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.VirtualMachine{}, api.ErrNotFound
		}
		return api.VirtualMachine{}, fmt.Errorf("select virtual machine: %w", err)
	}
	return vm, nil
}

// ListVirtualMachines returns paged VMs filtered by VirtualMachineListFilter.
//
//nolint:gocyclo // cursor-paginated query builder with optional filters
func (p *PG) ListVirtualMachines(
	ctx context.Context,
	filter api.VirtualMachineListFilter,
	limit int,
	cursor string,
) ([]api.VirtualMachine, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT `)
	sb.WriteString(vmColumns)
	sb.WriteString(` FROM virtual_machines`)
	args := make([]any, 0, 6)
	conds := make([]string, 0, 5)

	if !filter.IncludeTerminated {
		conds = append(conds, "terminated_at IS NULL")
	}
	if filter.CloudAccountID != nil {
		args = append(args, *filter.CloudAccountID)
		conds = append(conds, fmt.Sprintf("cloud_account_id = $%d", len(args)))
	}
	if filter.Region != nil {
		args = append(args, *filter.Region)
		conds = append(conds, fmt.Sprintf("region = $%d", len(args)))
	}
	if filter.Role != nil {
		args = append(args, *filter.Role)
		conds = append(conds, fmt.Sprintf("role = $%d", len(args)))
	}
	if filter.PowerState != nil {
		args = append(args, *filter.PowerState)
		conds = append(conds, fmt.Sprintf("power_state = $%d", len(args)))
	}
	if cursor != "" {
		ts, cid, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		args = append(args, ts)
		tsIdx := len(args)
		args = append(args, cid)
		idIdx := len(args)
		conds = append(conds, fmt.Sprintf("(created_at, id) < ($%d, $%d)", tsIdx, idIdx))
	}
	if len(conds) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conds, " AND "))
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY created_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query virtual machines: %w", err)
	}
	defer rows.Close()

	items := make([]api.VirtualMachine, 0, limit)
	for rows.Next() {
		vm, err := scanVirtualMachine(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan virtual machine: %w", err)
		}
		items = append(items, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate virtual machines: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateVirtualMachine applies merge-patch on curated-only fields.
//
//nolint:gocyclo // merge-patch checks each optional field; branching is unavoidable
func (p *PG) UpdateVirtualMachine(ctx context.Context, id uuid.UUID, in api.VirtualMachinePatch) (api.VirtualMachine, error) {
	sets := make([]string, 0, 8)
	args := make([]any, 0, 9)
	idx := 1
	appendSet := func(col string, val any) {
		sets = append(sets, fmt.Sprintf("%s=$%d", col, idx))
		args = append(args, val)
		idx++
	}
	if in.DisplayName != nil {
		appendSet("display_name", *in.DisplayName)
	}
	if in.Role != nil {
		appendSet("role", *in.Role)
	}
	if in.Owner != nil {
		appendSet("owner", *in.Owner)
	}
	if in.Criticality != nil {
		appendSet("criticality", *in.Criticality)
	}
	if in.Notes != nil {
		appendSet("notes", *in.Notes)
	}
	if in.RunbookURL != nil {
		appendSet("runbook_url", *in.RunbookURL)
	}
	if in.Annotations != nil {
		b, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.VirtualMachine{}, fmt.Errorf("marshal vm annotations: %w", err)
		}
		appendSet("annotations", b)
	}
	if len(sets) == 0 {
		return p.GetVirtualMachine(ctx, id)
	}
	appendSet("updated_at", time.Now().UTC())
	args = append(args, id)
	q := fmt.Sprintf("UPDATE virtual_machines SET %s WHERE id=$%d", strings.Join(sets, ", "), idx)
	tag, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.VirtualMachine{}, fmt.Errorf("update virtual machine: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.VirtualMachine{}, api.ErrNotFound
	}
	return p.GetVirtualMachine(ctx, id)
}

// DeleteVirtualMachine soft-deletes by setting terminated_at + power_state.
func (p *PG) DeleteVirtualMachine(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	tag, err := p.pool.Exec(ctx,
		`UPDATE virtual_machines
		 SET terminated_at = $1,
		     power_state = 'terminated',
		     ready = false,
		     updated_at = $1
		 WHERE id = $2 AND terminated_at IS NULL`,
		now, id,
	)
	if err != nil {
		return fmt.Errorf("delete virtual machine: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Check if it exists at all (it might already be terminated).
		var exists bool
		if err := p.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM virtual_machines WHERE id = $1)`, id,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check vm existence: %w", err)
		}
		if !exists {
			return api.ErrNotFound
		}
		// Already terminated — idempotent success.
	}
	return nil
}

// ReconcileVirtualMachines soft-deletes every row of the given account
// whose provider_vm_id is not in keep and that is not already terminated.
func (p *PG) ReconcileVirtualMachines(ctx context.Context, accountID uuid.UUID, keepProviderVMIDs []string) (int64, error) {
	now := time.Now().UTC()
	if len(keepProviderVMIDs) == 0 {
		tag, err := p.pool.Exec(ctx,
			`UPDATE virtual_machines
			 SET terminated_at = $1, power_state = 'terminated', ready = false, updated_at = $1
			 WHERE cloud_account_id = $2 AND terminated_at IS NULL`,
			now, accountID,
		)
		if err != nil {
			return 0, fmt.Errorf("reconcile virtual machines (none kept): %w", err)
		}
		return tag.RowsAffected(), nil
	}
	tag, err := p.pool.Exec(ctx,
		`UPDATE virtual_machines
		 SET terminated_at = $1, power_state = 'terminated', ready = false, updated_at = $1
		 WHERE cloud_account_id = $2 AND provider_vm_id <> ALL($3) AND terminated_at IS NULL`,
		now, accountID, keepProviderVMIDs,
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile virtual machines: %w", err)
	}
	return tag.RowsAffected(), nil
}

// jsonOrEmptyArray returns the raw JSON if non-nil, else "[]".
func jsonOrEmptyArray(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

func marshalStringMap(m map[string]string) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(m) //nolint:errchkjson // map[string]string always serialisable
	if err != nil {
		return nil, fmt.Errorf("marshal map: %w", err)
	}
	return b, nil
}

//nolint:gocyclo // long flat field list — verbose but boring
func scanVirtualMachine(row pgx.Row) (api.VirtualMachine, error) {
	var (
		out                  api.VirtualMachine
		displayName          sql.NullString
		role                 sql.NullString
		privateIP            sql.NullString
		publicIP             sql.NullString
		privateDNSName       sql.NullString
		vpcID                sql.NullString
		subnetID             sql.NullString
		nicsJSON             []byte
		sgJSON               []byte
		instanceType         sql.NullString
		architecture         sql.NullString
		zone                 sql.NullString
		region               sql.NullString
		imageID              sql.NullString
		imageName            sql.NullString
		keypairName          sql.NullString
		bootMode             sql.NullString
		providerAccountID    sql.NullString
		providerCreationDate *time.Time
		stateReason          sql.NullString
		kernelVersion        sql.NullString
		operatingSystem      sql.NullString
		capacityCPU          sql.NullString
		capacityMemory       sql.NullString
		blockDevicesJSON     []byte
		rootDeviceType       sql.NullString
		rootDeviceName       sql.NullString
		tagsJSON             []byte
		labelsJSON           []byte
		annotationsJSON      []byte
		owner                sql.NullString
		criticality          sql.NullString
		notes                sql.NullString
		runbookURL           sql.NullString
		terminatedAt         *time.Time
	)
	if err := row.Scan(
		&out.ID, &out.CloudAccountID,
		&out.ProviderVMID, &out.Name, &displayName, &role,
		&privateIP, &publicIP, &privateDNSName, &vpcID, &subnetID,
		&nicsJSON, &sgJSON,
		&instanceType, &architecture, &zone, &region,
		&imageID, &imageName, &keypairName, &bootMode, &providerAccountID, &providerCreationDate,
		&out.PowerState, &stateReason, &out.Ready, &out.DeletionProtection,
		&kernelVersion, &operatingSystem,
		&capacityCPU, &capacityMemory,
		&blockDevicesJSON, &rootDeviceType, &rootDeviceName,
		&tagsJSON, &labelsJSON, &annotationsJSON,
		&owner, &criticality, &notes, &runbookURL,
		&out.CreatedAt, &out.UpdatedAt, &out.LastSeenAt, &terminatedAt,
	); err != nil {
		return api.VirtualMachine{}, fmt.Errorf("scan virtual machine: %w", err)
	}
	out.DisplayName = nullableString(displayName)
	out.Role = nullableString(role)
	out.PrivateIP = nullableString(privateIP)
	out.PublicIP = nullableString(publicIP)
	out.PrivateDNSName = nullableString(privateDNSName)
	out.VPCID = nullableString(vpcID)
	out.SubnetID = nullableString(subnetID)
	if len(nicsJSON) > 0 {
		out.NICs = json.RawMessage(nicsJSON)
	}
	if len(sgJSON) > 0 {
		out.SecurityGroups = json.RawMessage(sgJSON)
	}
	out.InstanceType = nullableString(instanceType)
	out.Architecture = nullableString(architecture)
	out.Zone = nullableString(zone)
	out.Region = nullableString(region)
	out.ImageID = nullableString(imageID)
	out.ImageName = nullableString(imageName)
	out.KeypairName = nullableString(keypairName)
	out.BootMode = nullableString(bootMode)
	out.ProviderAccountID = nullableString(providerAccountID)
	out.ProviderCreationDate = providerCreationDate
	out.StateReason = nullableString(stateReason)
	out.KernelVersion = nullableString(kernelVersion)
	out.OperatingSystem = nullableString(operatingSystem)
	out.CapacityCPU = nullableString(capacityCPU)
	out.CapacityMemory = nullableString(capacityMemory)
	if len(blockDevicesJSON) > 0 {
		out.BlockDevices = json.RawMessage(blockDevicesJSON)
	}
	out.RootDeviceType = nullableString(rootDeviceType)
	out.RootDeviceName = nullableString(rootDeviceName)
	if len(tagsJSON) > 0 {
		var m map[string]string
		if err := json.Unmarshal(tagsJSON, &m); err != nil {
			return api.VirtualMachine{}, fmt.Errorf("unmarshal vm tags: %w", err)
		}
		if len(m) > 0 {
			out.Tags = m
		}
	}
	if len(labelsJSON) > 0 {
		var m map[string]string
		if err := json.Unmarshal(labelsJSON, &m); err != nil {
			return api.VirtualMachine{}, fmt.Errorf("unmarshal vm labels: %w", err)
		}
		if len(m) > 0 {
			out.Labels = m
		}
	}
	if len(annotationsJSON) > 0 {
		var m map[string]string
		if err := json.Unmarshal(annotationsJSON, &m); err != nil {
			return api.VirtualMachine{}, fmt.Errorf("unmarshal vm annotations: %w", err)
		}
		if len(m) > 0 {
			out.Annotations = m
		}
	}
	out.Owner = nullableString(owner)
	out.Criticality = nullableString(criticality)
	out.Notes = nullableString(notes)
	out.RunbookURL = nullableString(runbookURL)
	out.TerminatedAt = terminatedAt
	return out, nil
}

// validProviderVMID reports whether s is shaped like a real
// cloud-provider VM identifier. Outscale uses `i-<hex>`; AWS the same;
// other providers use letters, digits, dot, hyphen, underscore. We
// reject every other byte — including the LIKE metacharacters % and _
// (well, the LIKE _ is allowed by underscore in identifiers, so that
// part is best handled by the ESCAPE clause). Empty strings are
// accepted by the caller's `if in.ProviderVMID != ""` guard.
//
//nolint:gocyclo // character-class validation switch; each case is one allowed character class
func validProviderVMID(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// escapeLIKE escapes the three SQL LIKE metacharacters with backslash
// so the surrounding query can use `ESCAPE '\'`. Defensive — the
// validator above already rejects `%`, but keeping the escape here
// means a future relaxation of the validator (e.g. allowing `+` /
// `:`) doesn't accidentally re-open the wildcard injection path.
func escapeLIKE(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
