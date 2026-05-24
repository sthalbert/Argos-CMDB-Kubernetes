package store

// PostgreSQL implementation of the applications methods on api.Store
// (ADR-0029 §1, §2.1). Operator-curated entity that sits between
// application_blocks and workloads / virtual_machines.
//
// Follows the same pattern as pg_application_blocks.go: direct pgx
// queries, pgx.ErrNoRows ⇒ ErrNotFound, 23505 unique-violation ⇒
// ErrConflict, opaque (created_at, id) keyset cursor matching
// encodeCursor / decodeCursor (pg.go).
//
// On top of the block implementation this file adds:
//   - block resolution by EITHER id OR name (id wins on conflict, mirrors
//     the cloud_account_id/_name precedence in ADR-0019);
//   - application_block_name denormalisation via LEFT JOIN
//     application_blocks (ADR-0027 pattern);
//   - the three MemberCounts subqueries piggybacking on the SELECT so
//     every read returns workload + VM + vm-app counts in one round
//     trip;
//   - the per-entry virtual_machines.applications JSONB sweep on
//     DELETE, since PG FKs don't reach inside JSONB columns;
//   - the ListApplicationMembers walk across the three sources.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sthalbert/longue-vue/internal/api"
)

// errApplicationNameRequired is the symmetric counterpart to
// errApplicationBlockNameRequired (pg_application_blocks.go) — surfaced
// when CreateApplication is called with an empty / whitespace-only name
// (NormalizeApplicationName collapses to ""). Static sentinel so err113
// doesn't trip on ad-hoc fmt.Errorf.
var errApplicationNameRequired = errors.New("application: name required")

// errApplicationBlockIDNotFound and errApplicationBlockNameNotFound are
// the two failure modes of resolveBlockID. Kept as static sentinels for
// the same err113 reason; callers don't currently branch on them but the
// handler layer (Task 1.9) may.
var (
	errApplicationBlockIDNotFound   = errors.New("application_block id not found")
	errApplicationBlockNameNotFound = errors.New("application_block name not found")
)

// errMemberCursorInvalid is returned when ListApplicationMembers gets a
// cursor that's syntactically valid base64+JSON but points at an unknown
// stage or a negative offset (most likely a hand-crafted client).
var errMemberCursorInvalid = errors.New("application members cursor invalid")

// applicationSelect projects the application row PLUS the denormalised
// block name AND the three member-count subqueries. The subqueries are
// independent and run on the same round-trip; for 10K applications this
// is still a few ms total because each predicate hits the
// (application_id) index added by migration 00047.
//
// Future work (FUT-005): if the JSONB count becomes a hotspot on large
// fleets, replace with a materialised view + trigger refresh.
const applicationSelect = `
	a.id, a.name, a.display_name, a.description,
	a.application_block_id, b.name AS application_block_name,
	a.owner, a.criticality, a.notes, a.runbook_url, a.annotations,
	a.sec_disponibilite, a.sec_integrite, a.sec_confidentialite,
	a.sec_tracabilite, a.sec_notes,
	a.created_at, a.updated_at,
	(SELECT COUNT(*) FROM workloads        WHERE application_id = a.id),
	(SELECT COUNT(*) FROM virtual_machines WHERE application_id = a.id),
	(SELECT COUNT(*) FROM virtual_machines vm,
	 jsonb_array_elements(vm.applications) AS e
	 WHERE (e->>'application_id')::uuid = a.id)`

const applicationFrom = `FROM applications a
	LEFT JOIN application_blocks b ON a.application_block_id = b.id`

// CreateApplication inserts a new applications row. The name is
// normalised (NormalizeApplicationName ⇒ kebab-case) before insert so
// the UNIQUE (name) index treats "HashiCorp Vault" and "hashicorp-vault"
// as the same key. Block resolution accepts either ApplicationBlockID
// or ApplicationBlockName (id wins on conflict).
//
//nolint:gocritic // hugeParam: interface-mandated signature
func (p *PG) CreateApplication(ctx context.Context, in api.ApplicationCreate) (api.Application, error) {
	name := api.NormalizeApplicationName(in.Name)
	if name == "" {
		return api.Application{}, errApplicationNameRequired
	}

	blockID, err := p.resolveBlockID(ctx, in.ApplicationBlockID, in.ApplicationBlockName)
	if err != nil {
		return api.Application{}, err
	}

	annotationsJSON, err := marshalLabels(in.Annotations)
	if err != nil {
		return api.Application{}, fmt.Errorf("marshal application annotations: %w", err)
	}

	id := uuid.New()
	now := time.Now().UTC()

	const ins = `
		INSERT INTO applications (
			id, name, display_name, description, application_block_id,
			owner, criticality, notes, runbook_url, annotations,
			sec_disponibilite, sec_integrite, sec_confidentialite,
			sec_tracabilite, sec_notes,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $16)`
	if _, err := p.pool.Exec(ctx, ins,
		id, name, in.DisplayName, in.Description, blockID,
		in.Owner, in.Criticality, in.Notes, in.RunbookURL, annotationsJSON,
		in.SecDisponibilite, in.SecIntegrite, in.SecConfidentialite,
		in.SecTracabilite, in.SecNotes,
		now,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return api.Application{}, fmt.Errorf("application name %q already exists: %w", name, api.ErrConflict)
		}
		return api.Application{}, fmt.Errorf("insert application: %w", err)
	}
	// Re-fetch with the denorm projection so the returned shape is
	// identical to GetApplication.
	return p.GetApplication(ctx, id)
}

// GetApplication fetches by id with denormalised block name + member
// counts.
func (p *PG) GetApplication(ctx context.Context, id uuid.UUID) (api.Application, error) {
	q := `SELECT ` + applicationSelect + ` ` + applicationFrom + ` WHERE a.id = $1`
	row := p.pool.QueryRow(ctx, q, id)
	app, err := scanApplication(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Application{}, api.ErrNotFound
		}
		return api.Application{}, fmt.Errorf("select application: %w", err)
	}
	return app, nil
}

// GetApplicationsByIDs bulk-fetches applications by id in a single query,
// returned keyed by id. Unknown ids are silently omitted. Used by the
// effective-DICT decoration on workload + VM list responses to avoid an
// N+1 (ADR-0029 §6). Returns an empty (non-nil) map when ids is empty.
func (p *PG) GetApplicationsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]api.Application, error) {
	out := make(map[uuid.UUID]api.Application, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	q := `SELECT ` + applicationSelect + ` ` + applicationFrom + ` WHERE a.id = ANY($1)`
	rows, err := p.pool.Query(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("query applications by ids: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		app, err := scanApplication(rows)
		if err != nil {
			return nil, fmt.Errorf("scan application: %w", err)
		}
		out[app.ID] = app
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applications by ids: %w", err)
	}
	return out, nil
}

// GetApplicationByName normalises the input so operator-typed
// "HashiCorp Vault" and the stored "hashicorp-vault" both match.
func (p *PG) GetApplicationByName(ctx context.Context, name string) (api.Application, error) {
	norm := api.NormalizeApplicationName(name)
	if norm == "" {
		return api.Application{}, api.ErrNotFound
	}
	q := `SELECT ` + applicationSelect + ` ` + applicationFrom + ` WHERE a.name = $1`
	row := p.pool.QueryRow(ctx, q, norm)
	app, err := scanApplication(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return api.Application{}, api.ErrNotFound
		}
		return api.Application{}, fmt.Errorf("select application by name: %w", err)
	}
	return app, nil
}

// ListApplications returns paged rows ordered by (created_at DESC, id
// DESC). The opaque cursor matches the codebase encoding (base64 of
// "RFC3339Nano|uuid"). Filters are documented inline.
//
//nolint:gocyclo // cursor-paginated query builder with six optional filters
func (p *PG) ListApplications(
	ctx context.Context,
	filter api.ApplicationListFilter,
	limit int,
	cursor string,
) ([]api.Application, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	conds := make([]string, 0, 7)
	args := make([]any, 0, 8)

	// Name: case-insensitive substring on (name, display_name). Bind
	// once and reference twice ($N OR $N).
	if filter.Name != nil && *filter.Name != "" {
		args = append(args, "%"+strings.ToLower(escapeLike(*filter.Name))+"%")
		idx := len(args)
		conds = append(conds, fmt.Sprintf(
			"(LOWER(a.name) LIKE $%d ESCAPE '\\' OR LOWER(COALESCE(a.display_name,'')) LIKE $%d ESCAPE '\\')",
			idx, idx,
		))
	}
	if filter.ApplicationBlockID != nil {
		args = append(args, *filter.ApplicationBlockID)
		conds = append(conds, fmt.Sprintf("a.application_block_id = $%d", len(args)))
	}
	if filter.ApplicationBlockName != nil && *filter.ApplicationBlockName != "" {
		args = append(args, api.NormalizeApplicationName(*filter.ApplicationBlockName))
		conds = append(conds, fmt.Sprintf(
			"a.application_block_id = (SELECT id FROM application_blocks WHERE name = $%d)",
			len(args),
		))
	}
	if filter.Criticality != nil && *filter.Criticality != "" {
		args = append(args, *filter.Criticality)
		conds = append(conds, fmt.Sprintf("a.criticality = $%d", len(args)))
	}
	if filter.HasDICT != nil {
		if *filter.HasDICT {
			conds = append(conds,
				"(a.sec_disponibilite IS NOT NULL OR a.sec_integrite IS NOT NULL "+
					"OR a.sec_confidentialite IS NOT NULL OR a.sec_tracabilite IS NOT NULL)")
		} else {
			conds = append(conds,
				"(a.sec_disponibilite IS NULL AND a.sec_integrite IS NULL "+
					"AND a.sec_confidentialite IS NULL AND a.sec_tracabilite IS NULL)")
		}
	}
	if filter.DICTMin != nil {
		args = append(args, *filter.DICTMin)
		conds = append(conds, fmt.Sprintf(
			"GREATEST(COALESCE(a.sec_disponibilite,0), COALESCE(a.sec_integrite,0), "+
				"COALESCE(a.sec_confidentialite,0), COALESCE(a.sec_tracabilite,0)) >= $%d",
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
		conds = append(conds, fmt.Sprintf("(a.created_at, a.id) < ($%d, $%d)", tsIdx, idIdx))
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	args = append(args, limit+1)
	q := fmt.Sprintf(
		`SELECT %s %s %s ORDER BY a.created_at DESC, a.id DESC LIMIT $%d`,
		applicationSelect, applicationFrom, where, len(args),
	)

	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query applications: %w", err)
	}
	defer rows.Close()

	items := make([]api.Application, 0, limit)
	for rows.Next() {
		app, err := scanApplication(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan application: %w", err)
		}
		items = append(items, app)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate applications: %w", err)
	}

	var next string
	if len(items) > limit {
		last := items[limit-1]
		next = encodeCursor(last.CreatedAt, last.ID)
		items = items[:limit]
	}
	return items, next, nil
}

// UpdateApplication applies merge-patch on the mutable fields. Name is
// NOT mutable via this path (mirrors ApplicationBlock for the same
// "rename orphans inbound links" reason). updated_at is bumped when at
// least one field is patched; an all-nil patch is a no-op that returns
// the current row.
//
//nolint:gocyclo,gocritic // wide patch surface; hugeParam: interface-mandated signature
func (p *PG) UpdateApplication(ctx context.Context, id uuid.UUID, in api.ApplicationPatch) (api.Application, error) {
	// Resolve block first so an invalid id/name surfaces before the
	// UPDATE rather than as a 23503 FK violation buried in the wrapped
	// error.
	var blockID *uuid.UUID
	if in.ApplicationBlockID != nil || in.ApplicationBlockName != nil {
		resolved, err := p.resolveBlockID(ctx, in.ApplicationBlockID, in.ApplicationBlockName)
		if err != nil {
			return api.Application{}, err
		}
		blockID = resolved
	}

	sets := make([]string, 0, 14)
	args := make([]any, 0, 16)
	addSet := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if in.DisplayName != nil {
		addSet("display_name", *in.DisplayName)
	}
	if in.Description != nil {
		addSet("description", *in.Description)
	}
	if in.ApplicationBlockID != nil || in.ApplicationBlockName != nil {
		addSet("application_block_id", blockID)
	}
	if in.Owner != nil {
		addSet("owner", *in.Owner)
	}
	if in.Criticality != nil {
		addSet("criticality", *in.Criticality)
	}
	if in.Notes != nil {
		addSet("notes", *in.Notes)
	}
	if in.RunbookURL != nil {
		addSet("runbook_url", *in.RunbookURL)
	}
	if in.Annotations != nil {
		js, err := marshalLabels(in.Annotations)
		if err != nil {
			return api.Application{}, fmt.Errorf("marshal application annotations: %w", err)
		}
		addSet("annotations", js)
	}
	if in.SecDisponibilite != nil {
		addSet("sec_disponibilite", *in.SecDisponibilite)
	}
	if in.SecIntegrite != nil {
		addSet("sec_integrite", *in.SecIntegrite)
	}
	if in.SecConfidentialite != nil {
		addSet("sec_confidentialite", *in.SecConfidentialite)
	}
	if in.SecTracabilite != nil {
		addSet("sec_tracabilite", *in.SecTracabilite)
	}
	if in.SecNotes != nil {
		addSet("sec_notes", *in.SecNotes)
	}

	if len(sets) == 0 {
		// All-nil patch → no DB write, but still surface a 404 if the
		// row is missing (matches GetApplication).
		return p.GetApplication(ctx, id)
	}

	addSet("updated_at", time.Now().UTC())
	args = append(args, id)
	q := fmt.Sprintf(
		`UPDATE applications SET %s WHERE id = $%d`,
		strings.Join(sets, ", "), len(args),
	)

	ct, err := p.pool.Exec(ctx, q, args...)
	if err != nil {
		return api.Application{}, fmt.Errorf("update application: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.Application{}, api.ErrNotFound
	}
	return p.GetApplication(ctx, id)
}

// DeleteApplication removes the row. PG FK on workloads.application_id
// and virtual_machines.application_id is ON DELETE SET NULL (migration
// 00047), so curated linkage on those tables is cleared automatically.
// The per-entry application_id inside virtual_machines.applications
// JSONB is NOT covered by PG FKs — sweep it inside the same
// transaction so a deleted application leaves no dangling references.
func (p *PG) DeleteApplication(ctx context.Context, id uuid.UUID) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete application: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Strip application_id from any JSONB entries referencing this app.
	// The CASE expression preserves the surrounding entry (product,
	// version, name, …) and only removes the application_id key so
	// curated VM-app curation is not lost on application delete.
	//
	// We pass the id twice: once as a uuid (for the WHERE-row predicate
	// inside the SET projection, which compares uuid-cast JSONB text to
	// the parameter) and once as text (for the @> JSONB containment
	// pre-filter, which compares the raw stringified application_id).
	// Using two parameters avoids the "operator does not exist: uuid =
	// text" ambiguity pgx hits when one parameter is used in both
	// contexts.
	idStr := id.String()
	if _, err := tx.Exec(ctx, `
		UPDATE virtual_machines
		SET applications = (
			SELECT COALESCE(jsonb_agg(
				CASE WHEN (e->>'application_id')::uuid = $1
				     THEN e - 'application_id'
				     ELSE e END), '[]'::jsonb)
			FROM jsonb_array_elements(applications) AS e
		)
		WHERE applications @> jsonb_build_array(jsonb_build_object('application_id', $2::text))
	`, id, idStr); err != nil {
		return fmt.Errorf("sweep vm-app references: %w", err)
	}

	ct, err := tx.Exec(ctx, `DELETE FROM applications WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete application: %w", err)
	}
	return nil
}

// memberCursor is the small opaque cursor carried across pages of
// ListApplicationMembers. The walk traverses three sources in order
// (workloads → virtual_machines → vm_application JSONB) and bumps
// Kind+Off as it progresses.
//
// Offset pagination is fine at the expected fleet size (typical
// application has dozens of members, not thousands). If a single
// application grows past ~10K members, promote to keyset (FUT-004).
type memberCursor struct {
	Kind string `json:"k"`
	Off  int    `json:"o"`
}

// memberKind names map to memberCursor.Kind values. Kept as constants
// so a typo in one of the three branches surfaces at compile time.
const (
	memberKindWorkload = "workload"
	memberKindVM       = "virtual_machine"
	memberKindVMApp    = "vm_application"
)

// ListApplicationMembers walks the three sources of application
// membership in a stable order so the API surface is deterministic
// across calls. Each call returns up to `limit` members (default 100,
// hard cap 500) and an opaque cursor that resumes mid-walk.
//
//nolint:gocyclo // three-source walk; each branch is delegated to a helper but the dispatcher still trips the threshold.
func (p *PG) ListApplicationMembers(
	ctx context.Context,
	id uuid.UUID,
	limit int,
	cursor string,
) ([]api.ApplicationMember, string, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	c := memberCursor{Kind: memberKindWorkload, Off: 0}
	if cursor != "" {
		raw, err := decodeMemberCursor(cursor)
		if err != nil {
			return nil, "", fmt.Errorf("decode members cursor: %w", err)
		}
		c = raw
	}

	out := make([]api.ApplicationMember, 0, limit)

	if c.Kind == memberKindWorkload {
		page, full, err := p.listWorkloadMembers(ctx, id, limit, c.Off)
		if err != nil {
			return nil, "", err
		}
		out = append(out, page...)
		if full {
			next, err := encodeMemberCursor(memberCursor{Kind: memberKindWorkload, Off: c.Off + limit})
			if err != nil {
				return nil, "", err
			}
			return out, next, nil
		}
		c = memberCursor{Kind: memberKindVM, Off: 0}
	}

	if c.Kind == memberKindVM {
		remaining := limit - len(out)
		page, full, err := p.listVMMembers(ctx, id, remaining, c.Off)
		if err != nil {
			return nil, "", err
		}
		out = append(out, page...)
		if full {
			next, err := encodeMemberCursor(memberCursor{Kind: memberKindVM, Off: c.Off + remaining})
			if err != nil {
				return nil, "", err
			}
			return out, next, nil
		}
		c = memberCursor{Kind: memberKindVMApp, Off: 0}
	}

	if c.Kind == memberKindVMApp {
		remaining := limit - len(out)
		page, full, err := p.listVMAppMembers(ctx, id, remaining, c.Off)
		if err != nil {
			return nil, "", err
		}
		out = append(out, page...)
		if full {
			next, err := encodeMemberCursor(memberCursor{Kind: memberKindVMApp, Off: c.Off + remaining})
			if err != nil {
				return nil, "", err
			}
			return out, next, nil
		}
	}

	return out, "", nil
}

// listWorkloadMembers fetches the workloads currently FK-linked to the
// application. Returns (page, fullPage, err) where fullPage signals the
// caller to emit a next-page cursor (LIMIT $limit+1 sentinel).
func (p *PG) listWorkloadMembers(ctx context.Context, id uuid.UUID, limit, offset int) ([]api.ApplicationMember, bool, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT w.id, w.name, n.name AS namespace, w.updated_at
		FROM workloads w
		JOIN namespaces n ON n.id = w.namespace_id
		WHERE w.application_id = $1
		ORDER BY w.name, w.id
		LIMIT $2 OFFSET $3`,
		id, limit+1, offset,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query workload members: %w", err)
	}
	defer rows.Close()

	out := make([]api.ApplicationMember, 0, limit)
	for rows.Next() {
		var (
			wid       uuid.UUID
			name, ns  string
			updatedAt time.Time
		)
		if err := rows.Scan(&wid, &name, &ns, &updatedAt); err != nil {
			return nil, false, fmt.Errorf("scan workload member: %w", err)
		}
		out = append(out, api.ApplicationMember{
			Kind:   api.ApplicationMemberKindWorkload,
			ID:     wid.String(),
			Name:   name,
			Parent: &api.ApplicationMemberRef{Kind: "namespace", Name: ns},
			Linked: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate workload members: %w", err)
	}
	full := len(out) > limit
	if full {
		out = out[:limit]
	}
	return out, full, nil
}

// listVMMembers fetches the VMs currently FK-linked to the application.
func (p *PG) listVMMembers(ctx context.Context, id uuid.UUID, limit, offset int) ([]api.ApplicationMember, bool, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT id, name, updated_at
		FROM virtual_machines
		WHERE application_id = $1
		ORDER BY name, id
		LIMIT $2 OFFSET $3`,
		id, limit+1, offset,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query vm members: %w", err)
	}
	defer rows.Close()

	out := make([]api.ApplicationMember, 0, limit)
	for rows.Next() {
		var (
			vid       uuid.UUID
			name      string
			updatedAt time.Time
		)
		if err := rows.Scan(&vid, &name, &updatedAt); err != nil {
			return nil, false, fmt.Errorf("scan vm member: %w", err)
		}
		out = append(out, api.ApplicationMember{
			Kind:   api.ApplicationMemberKindVirtualMachine,
			ID:     vid.String(),
			Name:   name,
			Linked: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate vm members: %w", err)
	}
	full := len(out) > limit
	if full {
		out = out[:limit]
	}
	return out, full, nil
}

// listVMAppMembers walks the per-VM applications JSONB array and surfaces
// every entry whose application_id matches.
func (p *PG) listVMAppMembers(ctx context.Context, id uuid.UUID, limit, offset int) ([]api.ApplicationMember, bool, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT vm.id, vm.name, vm.updated_at,
		       e->>'product' AS product, COALESCE(e->>'name','') AS label
		FROM virtual_machines vm,
		     jsonb_array_elements(vm.applications) AS e
		WHERE (e->>'application_id')::uuid = $1
		ORDER BY vm.name, e->>'product', COALESCE(e->>'name',''), vm.id
		LIMIT $2 OFFSET $3`,
		id, limit+1, offset,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query vm-app members: %w", err)
	}
	defer rows.Close()

	out := make([]api.ApplicationMember, 0, limit)
	for rows.Next() {
		var (
			vmID           uuid.UUID
			vmName         string
			updatedAt      time.Time
			product, label string
		)
		if err := rows.Scan(&vmID, &vmName, &updatedAt, &product, &label); err != nil {
			return nil, false, fmt.Errorf("scan vm-app member: %w", err)
		}
		displayName := product
		if label != "" {
			displayName = product + " / " + label
		}
		out = append(out, api.ApplicationMember{
			Kind:   api.ApplicationMemberKindVMApplication,
			ID:     fmt.Sprintf("%s#%s#%s", vmID, product, label),
			Name:   displayName,
			Parent: &api.ApplicationMemberRef{Kind: "virtual_machine", ID: vmID.String(), Name: vmName},
			Linked: updatedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate vm-app members: %w", err)
	}
	full := len(out) > limit
	if full {
		out = out[:limit]
	}
	return out, full, nil
}

// resolveBlockID accepts either id or name and returns the canonical id
// for use as a FK value. id wins on conflict (mirrors the
// cloud_account_id / cloud_account_name precedence in ADR-0019). Both
// nil is the legitimate "no block" case → returns (nil, nil) so the
// caller writes NULL to application_block_id.
//
//nolint:nilnil // (nil, nil) is the documented "no block selected" signal; not an error.
func (p *PG) resolveBlockID(ctx context.Context, byID *uuid.UUID, byName *string) (*uuid.UUID, error) {
	if byID != nil {
		// Confirm the row exists so the caller gets a meaningful error
		// rather than a 23503 FK violation from the surrounding INSERT
		// / UPDATE.
		var exists bool
		err := p.pool.QueryRow(ctx,
			`SELECT TRUE FROM application_blocks WHERE id = $1`, *byID,
		).Scan(&exists)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errApplicationBlockIDNotFound
			}
			return nil, fmt.Errorf("resolve application_block by id: %w", err)
		}
		return byID, nil
	}
	if byName != nil && *byName != "" {
		var id uuid.UUID
		err := p.pool.QueryRow(ctx,
			`SELECT id FROM application_blocks WHERE name = $1`,
			api.NormalizeApplicationName(*byName),
		).Scan(&id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, errApplicationBlockNameNotFound
			}
			return nil, fmt.Errorf("resolve application_block by name: %w", err)
		}
		return &id, nil
	}
	return nil, nil
}

// scanApplication matches applicationSelect (denormalised projection
// with member counts at the tail). Annotations is always populated as a
// non-nil map because the Application json contract has no `omitempty`.
func scanApplication(row pgx.Row) (api.Application, error) {
	var (
		out            api.Application
		annotationsRaw []byte
	)
	if err := row.Scan(
		&out.ID, &out.Name, &out.DisplayName, &out.Description,
		&out.ApplicationBlockID, &out.ApplicationBlockName,
		&out.Owner, &out.Criticality, &out.Notes, &out.RunbookURL, &annotationsRaw,
		&out.SecDisponibilite, &out.SecIntegrite, &out.SecConfidentialite,
		&out.SecTracabilite, &out.SecNotes,
		&out.CreatedAt, &out.UpdatedAt,
		&out.MemberCounts.Workloads,
		&out.MemberCounts.VirtualMachines,
		&out.MemberCounts.VMApplications,
	); err != nil {
		// Don't wrap: the caller layers a domain-specific message
		// (and translates pgx.ErrNoRows into api.ErrNotFound).
		return api.Application{}, fmt.Errorf("scan application: %w", err)
	}

	anns := map[string]string{}
	if len(annotationsRaw) > 0 {
		if err := json.Unmarshal(annotationsRaw, &anns); err != nil {
			return api.Application{}, fmt.Errorf("unmarshal application annotations: %w", err)
		}
	}
	out.Annotations = anns
	return out, nil
}

// encodeMemberCursor / decodeMemberCursor wrap a memberCursor into the
// same base64-of-JSON convention used by other opaque cursors in the
// codebase. Kept private to this file because no other store consumer
// needs the shape.
//
// The error returns are kept on the signatures (rather than a single
// panic) so a future cursor shape with non-trivially-marshalable fields
// doesn't force a behaviour change in callers.
func encodeMemberCursor(c memberCursor) (string, error) {
	// memberCursor is a struct of {string, int} — json.Marshal will
	// never fail. Suppress errchkjson with an explicit nolint rather
	// than fall back to manual hex assembly.
	b, err := json.Marshal(c) //nolint:errchkjson // memberCursor fields are JSON-safe (string + int)
	if err != nil {
		return "", fmt.Errorf("marshal members cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeMemberCursor(s string) (memberCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return memberCursor{}, fmt.Errorf("base64 decode: %w", err)
	}
	var c memberCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return memberCursor{}, fmt.Errorf("unmarshal members cursor: %w", err)
	}
	switch c.Kind {
	case memberKindWorkload, memberKindVM, memberKindVMApp:
	default:
		return memberCursor{}, fmt.Errorf("%w: unknown kind %q", errMemberCursorInvalid, c.Kind)
	}
	if c.Off < 0 {
		return memberCursor{}, fmt.Errorf("%w: negative offset %d", errMemberCursorInvalid, c.Off)
	}
	return c, nil
}
