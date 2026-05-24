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

	"github.com/sthalbert/longue-vue/internal/api"
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
	applications, application_id,
	created_at, updated_at, last_seen_at, terminated_at`

// UpsertVirtualMachine inserts a new VM or updates the existing row
// keyed on (cloud_account_id, provider_vm_id). Server-side dedup
// against nodes.provider_id: returns ErrConflict when the VM is
// already known as a Kubernetes node.
//
//nolint:gocritic // hugeParam: Store interface requires value param; insert columns are inherently long
func (p *PG) UpsertVirtualMachine(ctx context.Context, in api.VirtualMachineUpsert) (api.VirtualMachine, api.UpsertOutcome, error) {
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
			return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf(
				"provider_vm_id %q contains disallowed characters: %w",
				in.ProviderVMID,
				api.ErrConflict,
			)
		}
		var existsCount int
		if err := p.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM nodes WHERE provider_id LIKE '%' || $1 || '%' ESCAPE '\'`,
			escapeLIKE(in.ProviderVMID),
		).Scan(&existsCount); err != nil {
			return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf("dedup check against nodes: %w", err)
		}
		if existsCount > 0 {
			return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf(
				"provider_vm_id %q already inventoried as a node: %w",
				in.ProviderVMID,
				api.ErrConflict,
			)
		}
	}

	id := uuid.New()
	now := time.Now().UTC()

	nicsJSON := jsonOrEmptyArray(in.NICs)
	sgJSON := jsonOrEmptyArray(in.SecurityGroups)
	bdJSON := jsonOrEmptyArray(in.BlockDevices)

	tagsJSON, err := marshalStringMap(in.Tags)
	if err != nil {
		return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf("marshal tags: %w", err)
	}
	labelsJSON, err := marshalStringMap(in.Labels)
	if err != nil {
		return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf("marshal labels: %w", err)
	}

	// AUDIT_BUSINESS_FIELDS: name, role, private_ip, public_ip,
	// private_dns_name, vpc_id, subnet_id, nics, security_groups,
	// instance_type, architecture, zone, region, image_id, image_name,
	// keypair_name, boot_mode, provider_account_id, provider_creation_date,
	// power_state, state_reason, ready, deletion_protection, kernel_version,
	// operating_system, capacity_cpu, capacity_memory, block_devices,
	// root_device_type, root_device_name, tags, labels, terminated_at
	// (restore flips it). Excluded: applications (curator-only, never
	// written by collector); last_seen_at and updated_at (clock fields);
	// owner/criticality/notes/runbook_url/annotations/display_name
	// (curator metadata, never overwritten by collector upsert).
	//
	// COLLECTOR INVARIANT (ADR-0029 §7): application_id is operator-curated
	// and is deliberately absent from both the INSERT column list and the
	// ON CONFLICT SET list below. The per-entry application_id inside the
	// `applications` JSONB column is likewise untouched — the collector
	// never writes the applications column at all. Pinned by
	// TestPGVMApplicationLinkage in pg_virtual_machines_test.go.
	const q = `
		WITH old AS (
		  SELECT name, role, private_ip, public_ip, private_dns_name,
		         vpc_id, subnet_id, nics, security_groups,
		         instance_type, architecture, zone, region,
		         image_id, image_name, keypair_name, boot_mode,
		         provider_account_id, provider_creation_date,
		         power_state, state_reason, ready, deletion_protection,
		         kernel_version, operating_system,
		         capacity_cpu, capacity_memory,
		         block_devices, root_device_type, root_device_name,
		         tags, labels, terminated_at
		    FROM virtual_machines
		    WHERE cloud_account_id=$2 AND provider_vm_id=$3
		),
		upserted AS (
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
		  RETURNING id, name, role, private_ip, public_ip, private_dns_name,
		            vpc_id, subnet_id, nics, security_groups,
		            instance_type, architecture, zone, region,
		            image_id, image_name, keypair_name, boot_mode,
		            provider_account_id, provider_creation_date,
		            power_state, state_reason, ready, deletion_protection,
		            kernel_version, operating_system,
		            capacity_cpu, capacity_memory,
		            block_devices, root_device_type, root_device_name,
		            tags, labels, terminated_at, xmax
		)
		SELECT id,
		       (xmax = 0) AS inserted,
		       (xmax <> 0 AND (
		           o.name                  IS DISTINCT FROM upserted.name                  OR
		           o.role                  IS DISTINCT FROM upserted.role                  OR
		           o.private_ip            IS DISTINCT FROM upserted.private_ip            OR
		           o.public_ip             IS DISTINCT FROM upserted.public_ip             OR
		           o.private_dns_name      IS DISTINCT FROM upserted.private_dns_name      OR
		           o.vpc_id                IS DISTINCT FROM upserted.vpc_id                OR
		           o.subnet_id             IS DISTINCT FROM upserted.subnet_id             OR
		           o.nics                  IS DISTINCT FROM upserted.nics                  OR
		           o.security_groups       IS DISTINCT FROM upserted.security_groups       OR
		           o.instance_type         IS DISTINCT FROM upserted.instance_type         OR
		           o.architecture          IS DISTINCT FROM upserted.architecture          OR
		           o.zone                  IS DISTINCT FROM upserted.zone                  OR
		           o.region                IS DISTINCT FROM upserted.region                OR
		           o.image_id              IS DISTINCT FROM upserted.image_id              OR
		           o.image_name            IS DISTINCT FROM upserted.image_name            OR
		           o.keypair_name          IS DISTINCT FROM upserted.keypair_name          OR
		           o.boot_mode             IS DISTINCT FROM upserted.boot_mode             OR
		           o.provider_account_id   IS DISTINCT FROM upserted.provider_account_id   OR
		           o.provider_creation_date IS DISTINCT FROM upserted.provider_creation_date OR
		           o.power_state           IS DISTINCT FROM upserted.power_state           OR
		           o.state_reason          IS DISTINCT FROM upserted.state_reason          OR
		           o.ready                 IS DISTINCT FROM upserted.ready                 OR
		           o.deletion_protection   IS DISTINCT FROM upserted.deletion_protection   OR
		           o.kernel_version        IS DISTINCT FROM upserted.kernel_version        OR
		           o.operating_system      IS DISTINCT FROM upserted.operating_system      OR
		           o.capacity_cpu          IS DISTINCT FROM upserted.capacity_cpu          OR
		           o.capacity_memory       IS DISTINCT FROM upserted.capacity_memory       OR
		           o.block_devices         IS DISTINCT FROM upserted.block_devices         OR
		           o.root_device_type      IS DISTINCT FROM upserted.root_device_type      OR
		           o.root_device_name      IS DISTINCT FROM upserted.root_device_name      OR
		           o.tags                  IS DISTINCT FROM upserted.tags                  OR
		           o.labels                IS DISTINCT FROM upserted.labels                OR
		           o.terminated_at         IS DISTINCT FROM upserted.terminated_at
		       )) AS business_changed
		  FROM upserted LEFT JOIN old o ON true
	`
	var rowID uuid.UUID
	var inserted, businessChanged bool
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
	).Scan(&rowID, &inserted, &businessChanged)
	if err != nil {
		return api.VirtualMachine{}, api.OutcomeNoChange, fmt.Errorf("upsert virtual machine: %w", err)
	}
	vm, err := p.GetVirtualMachine(ctx, rowID)
	if err != nil {
		return api.VirtualMachine{}, api.OutcomeNoChange, err
	}
	return vm, classifyOutcome(inserted, businessChanged), nil
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
//nolint:gocyclo,gocritic // gocyclo: cursor-paginated query builder with optional filters; gocritic/hugeParam: signature matches api.Store interface
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
	if filter.CloudAccountName != nil {
		// Inner subquery against the cloud_accounts UNIQUE (provider, name)
		// index — O(1). Returns 0 rows if the account name doesn't exist,
		// which collapses the outer query to empty without an error.
		args = append(args, *filter.CloudAccountName)
		conds = append(conds, fmt.Sprintf("cloud_account_id IN (SELECT id FROM cloud_accounts WHERE name = $%d)", len(args)))
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
	if filter.Name != nil {
		// Case-insensitive substring on LOWER(name), index-backed by
		// virtual_machines_name_lower_idx for prefix queries; falls back
		// to seq scan for full substring (acceptable for ≤ a few thousand
		// VMs per typical SecNumCloud deployment). LIKE-escape applied
		// so operator-pasted `%` / `_` is treated literally.
		args = append(args, escapeLIKE(strings.ToLower(*filter.Name)))
		conds = append(conds, fmt.Sprintf("LOWER(name) LIKE '%%' || $%d || '%%' ESCAPE '\\'", len(args)))
	}
	if filter.Image != nil {
		// Match across image_id (e.g. "ami-75374985") and image_name
		// (e.g. "rocky-9.3-2024-08") — the operator may know either.
		args = append(args, escapeLIKE(*filter.Image))
		idx := len(args)
		conds = append(conds, fmt.Sprintf(
			"(image_id LIKE '%%' || $%d || '%%' ESCAPE '\\' OR LOWER(image_name) LIKE '%%' || LOWER($%d) || '%%' ESCAPE '\\')",
			idx, idx,
		))
	}
	if filter.Application != nil {
		// JSONB containment via GIN jsonb_path_ops index on `applications`.
		// Build the canonical containment fragment server-side from the
		// normalized product name, so the client cannot inject arbitrary
		// JSONB query operators. Use json.Marshal to escape any stray
		// special characters in the normalized name (single quotes,
		// backslashes, control chars) — even though NormalizeProductName
		// already restricts the alphabet, defense in depth is cheap.
		entry := map[string]string{"product": api.NormalizeProductName(*filter.Application)}
		// ApplicationVersion narrows the match to a specific version when
		// both fields are present. Honoured only with Application — the
		// filter doesn't expose version-only search.
		if filter.ApplicationVersion != nil {
			entry["version"] = strings.TrimSpace(*filter.ApplicationVersion)
		}
		probe, _ := json.Marshal([]map[string]string{entry})
		args = append(args, string(probe))
		conds = append(conds, fmt.Sprintf("applications @> $%d::jsonb", len(args)))
	}
	// ADR-0029 link-aware filters. ApplicationID wins on conflict with
	// ApplicationName (mirrors the cloud_account_id / cloud_account_name
	// precedence from ADR-0019). ApplicationName is normalised server-side
	// to match the kebab-case stored in applications.name.
	if filter.ApplicationID != nil {
		args = append(args, *filter.ApplicationID)
		conds = append(conds, fmt.Sprintf("application_id = $%d", len(args)))
	} else if filter.ApplicationName != nil && *filter.ApplicationName != "" {
		args = append(args, api.NormalizeApplicationName(*filter.ApplicationName))
		conds = append(conds, fmt.Sprintf(
			"application_id = (SELECT id FROM applications WHERE name = $%d)",
			len(args),
		))
	}
	if filter.Unlinked != nil && *filter.Unlinked {
		conds = append(conds, "application_id IS NULL")
	}
	// ADR-0029 §2.4: substring match on linked application name (used by
	// the Search endpoint). LIKE metacharacters are escaped; case-insensitive
	// via LOWER on both sides.
	if filter.ApplicationNameSubstring != nil && *filter.ApplicationNameSubstring != "" {
		args = append(args, "%"+escapeLike(strings.ToLower(*filter.ApplicationNameSubstring))+"%")
		conds = append(conds, fmt.Sprintf(
			"application_id IN (SELECT id FROM applications WHERE LOWER(name) LIKE $%d ESCAPE '\\')",
			len(args),
		))
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
//nolint:gocyclo,gocritic // merge-patch checks each optional field; hugeParam: signature matches api.Store interface
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
	if in.Applications != nil {
		// Per-entry ADR-0029 link validation: every non-nil application_id
		// must reference an existing applications row. Batched in one
		// COUNT query to avoid N+1 on VMs with many entries.
		if err := p.validateVMAppEntryApplicationIDs(ctx, *in.Applications); err != nil {
			return api.VirtualMachine{}, err
		}
		// Replace-not-merge semantics (ADR-0019 §4): the handler has
		// already diffed the input against the stored list to preserve
		// added_at / added_by where the (product, version, name) key
		// matches. The store sees the final canonical list.
		b, err := json.Marshal(*in.Applications)
		if err != nil {
			return api.VirtualMachine{}, fmt.Errorf("marshal vm applications: %w", err)
		}
		appendSet("applications", b)
	}
	// ADR-0029 row-level link. The PATCH handler has already resolved
	// ApplicationName → ID via ResolveApplicationID, so by the time the
	// store sees it the field is a concrete (and validated) UUID. A nil
	// pointer means "leave the link untouched" (merge-patch semantics);
	// expressing set-to-null is a future surface (mirrors the workload
	// behaviour described in UpdateWorkload).
	if in.ApplicationID != nil {
		appendSet("application_id", *in.ApplicationID)
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

// validateVMAppEntryApplicationIDs verifies every non-nil per-entry
// application_id references an existing applications row. Batches into a
// single COUNT(*) query so VMs with many application entries don't pay
// the N+1 penalty. Returns nil when no ids are present.
func (p *PG) validateVMAppEntryApplicationIDs(ctx context.Context, entries []api.VMApplication) error {
	if len(entries) == 0 {
		return nil
	}
	// Dedup distinct ids in Go so a count mismatch surfaces a missing row,
	// not a duplicate-reference noise hit.
	distinct := make(map[uuid.UUID]struct{}, len(entries))
	refIDs := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		if entry.ApplicationID == nil {
			continue
		}
		id := *entry.ApplicationID
		if _, seen := distinct[id]; seen {
			continue
		}
		distinct[id] = struct{}{}
		refIDs = append(refIDs, id)
	}
	if len(refIDs) == 0 {
		return nil
	}
	var count int
	if err := p.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM applications WHERE id = ANY($1)`,
		refIDs,
	).Scan(&count); err != nil {
		return fmt.Errorf("validate vm-app application_ids: %w", err)
	}
	if count != len(refIDs) {
		return fmt.Errorf("one or more application_id references invalid: %w", api.ErrNotFound)
	}
	return nil
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

// ListDistinctVMApplications returns one row per distinct product, with
// the sorted list of distinct versions seen for that product across every
// non-terminated VM's applications array. Drives the cascading
// product → version dropdown in the VM list UI (ADR-0019 §3).
func (p *PG) ListDistinctVMApplications(ctx context.Context) ([]api.VMApplicationDistinct, error) {
	const q = `
		SELECT product, array_agg(DISTINCT version ORDER BY version) AS versions
		FROM virtual_machines, jsonb_array_elements(applications) AS app,
		     LATERAL (SELECT app->>'product' AS product, app->>'version' AS version) AS x
		WHERE terminated_at IS NULL
		  AND product IS NOT NULL AND product <> ''
		  AND version IS NOT NULL AND version <> ''
		GROUP BY product
		ORDER BY product
	`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query distinct vm applications: %w", err)
	}
	defer rows.Close()
	out := make([]api.VMApplicationDistinct, 0, 16)
	for rows.Next() {
		var entry api.VMApplicationDistinct
		if err := rows.Scan(&entry.Product, &entry.Versions); err != nil {
			return nil, fmt.Errorf("scan distinct vm application: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct vm applications: %w", err)
	}
	return out, nil
}

// ListVMsWithApplicationEntry returns every non-terminated VM that has at
// least one applications[] JSONB entry whose per-entry application_id
// matches appID, regardless of the VM's row-level application_id. Powers
// the per-application EOL aggregator's third source (ADR-0029 §5): a
// VM-application entry contributes even when its parent VM is not itself
// linked. The match uses jsonb containment so the GIN jsonb_path_ops index
// on `applications` is eligible. The full VM row is returned so the caller
// reads annotations + filters the matching entries in Go.
func (p *PG) ListVMsWithApplicationEntry(ctx context.Context, appID uuid.UUID) ([]api.VirtualMachine, error) {
	// Containment probe: [{"application_id":"<uuid>"}]. Built server-side
	// from the typed UUID so no injection surface; json.Marshal of a
	// []map[string]string is always serialisable.
	entry := []map[string]string{{"application_id": appID.String()}}
	probe, _ := json.Marshal(entry)
	q := `SELECT ` + vmColumns + `
		FROM virtual_machines
		WHERE terminated_at IS NULL
		  AND applications @> $1::jsonb
		ORDER BY name, id`
	rows, err := p.pool.Query(ctx, q, string(probe))
	if err != nil {
		return nil, fmt.Errorf("query vms with application entry: %w", err)
	}
	defer rows.Close()
	out := make([]api.VirtualMachine, 0, 8)
	for rows.Next() {
		vm, err := scanVirtualMachine(rows)
		if err != nil {
			return nil, fmt.Errorf("scan vm with application entry: %w", err)
		}
		out = append(out, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vms with application entry: %w", err)
	}
	return out, nil
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
		applicationsJSON     []byte
		applicationID        uuid.NullUUID
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
		&applicationsJSON, &applicationID,
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
	out.Applications = []api.VMApplication{}
	if len(applicationsJSON) > 0 {
		var apps []api.VMApplication
		if err := json.Unmarshal(applicationsJSON, &apps); err != nil {
			return api.VirtualMachine{}, fmt.Errorf("unmarshal vm applications: %w", err)
		}
		if len(apps) > 0 {
			out.Applications = apps
		}
	}
	if applicationID.Valid {
		v := applicationID.UUID
		out.ApplicationID = &v
	}
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
