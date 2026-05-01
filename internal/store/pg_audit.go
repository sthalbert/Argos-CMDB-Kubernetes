package store

// PostgreSQL implementation of the audit_events store methods. Append-only
// insertion + a cursor-paginated reader with optional filters for the
// admin UI's /ui/admin/audit page.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// auditEventSelectColumns is the column list used for SELECT queries
// against audit_events. Order must match the Scan() in ListAuditEvents.
const auditEventSelectColumns = `id, occurred_at, actor_id, actor_kind, actor_username, actor_role,
	action, resource_type, resource_id, http_method, http_path, http_status,
	source, source_ip, user_agent, details`

// auditEventColumns is the column list used for INSERT statements.
// Mirrors auditEventSelectColumns column-for-column so the $N
// placeholders in InsertAuditEvent stay in lock-step.
const auditEventColumns = auditEventSelectColumns

// InsertAuditEvent appends an audit event to the append-only audit_events table.
//
//nolint:gocritic // hugeParam: Store interface requires value param
func (p *PG) InsertAuditEvent(ctx context.Context, in api.AuditEventInsert) error {
	var detailsArg any
	if in.Details != nil {
		b, err := json.Marshal(in.Details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		detailsArg = b
	}
	source := in.Source
	if source == "" {
		// Migration default + back-compat for callers built before ADR-0016
		// added the column. The CHECK constraint on the column rejects any
		// other empty / unknown value, so always emit a known label here.
		source = "api"
	}
	// nullable string columns: store empty strings as SQL NULL so filter
	// queries on IS NULL stay clean.
	_, err := p.pool.Exec(ctx,
		`INSERT INTO audit_events (`+auditEventColumns+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		in.ID, in.OccurredAt, in.ActorID, in.ActorKind,
		nullableTextArg(in.ActorUsername), nullableTextArg(in.ActorRole),
		in.Action,
		nullableTextArg(in.ResourceType), nullableTextArg(in.ResourceID),
		in.HTTPMethod, in.HTTPPath, in.HTTPStatus,
		source,
		nullableTextArg(in.SourceIP), nullableTextArg(in.UserAgent),
		detailsArg,
	)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

// ListAuditEvents returns a cursor-paginated, newest-first list of audit events
// with optional filters on actor, resource, action, and time range.
//
//nolint:gocyclo // cursor-paginated query builder with multiple optional filters
func (p *PG) ListAuditEvents(ctx context.Context, filter api.AuditEventFilter, limit int, cursor string) ([]api.AuditEvent, string, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	sb := strings.Builder{}
	sb.WriteString(`SELECT ` + auditEventSelectColumns + ` FROM audit_events WHERE 1=1`)
	args := make([]any, 0, 8)
	if filter.ActorID != nil {
		args = append(args, *filter.ActorID)
		fmt.Fprintf(&sb, " AND actor_id = $%d", len(args))
	}
	if filter.ResourceType != nil {
		args = append(args, *filter.ResourceType)
		fmt.Fprintf(&sb, " AND resource_type = $%d", len(args))
	}
	if filter.ResourceID != nil {
		args = append(args, *filter.ResourceID)
		fmt.Fprintf(&sb, " AND resource_id = $%d", len(args))
	}
	if filter.Action != nil {
		args = append(args, *filter.Action)
		fmt.Fprintf(&sb, " AND action = $%d", len(args))
	}
	if filter.Source != nil {
		args = append(args, *filter.Source)
		fmt.Fprintf(&sb, " AND source = $%d", len(args))
	}
	if filter.Since != nil {
		args = append(args, *filter.Since)
		fmt.Fprintf(&sb, " AND occurred_at >= $%d", len(args))
	}
	if filter.Until != nil {
		args = append(args, *filter.Until)
		fmt.Fprintf(&sb, " AND occurred_at < $%d", len(args))
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
		// (occurred_at, id) is strictly decreasing through the result set;
		// paginate on the tuple so concurrent inserts don't shift rows
		// between pages.
		fmt.Fprintf(&sb, " AND (occurred_at, id) < ($%d, $%d)", tsIdx, idIdx)
	}
	args = append(args, limit+1)
	fmt.Fprintf(&sb, " ORDER BY occurred_at DESC, id DESC LIMIT $%d", len(args))

	rows, err := p.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, "", fmt.Errorf("query audit events: %w", err)
	}
	defer rows.Close()

	items := make([]api.AuditEvent, 0, limit)
	for rows.Next() {
		var (
			ev            api.AuditEvent
			actorID       *uuid.UUID
			actorKind     string
			actorUsername *string
			actorRole     *string
			resourceType  *string
			resourceID    *string
			source        string
			sourceIP      *string
			userAgent     *string
			detailsRaw    []byte
		)
		if err := rows.Scan(
			&ev.Id, &ev.OccurredAt, &actorID, &actorKind, &actorUsername, &actorRole,
			&ev.Action, &resourceType, &resourceID, &ev.HttpMethod, &ev.HttpPath, &ev.HttpStatus,
			&source, &sourceIP, &userAgent, &detailsRaw,
		); err != nil {
			return nil, "", fmt.Errorf("scan audit event: %w", err)
		}
		ev.ActorId = actorID
		ev.ActorKind = api.AuditEventActorKind(actorKind)
		ev.ActorUsername = actorUsername
		ev.ActorRole = actorRole
		ev.ResourceType = resourceType
		ev.ResourceId = resourceID
		if source != "" {
			s := api.AuditEventSource(source)
			ev.Source = &s
		}
		ev.SourceIp = sourceIP
		ev.UserAgent = userAgent
		if len(detailsRaw) > 0 {
			var d map[string]interface{}
			if err := json.Unmarshal(detailsRaw, &d); err != nil {
				return nil, "", fmt.Errorf("unmarshal audit details: %w", err)
			}
			ev.Details = &d
		}
		items = append(items, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate audit events: %w", err)
	}

	var next string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(last.OccurredAt, last.Id)
	}
	return items, next, nil
}

// nullableTextArg converts "" to a SQL NULL so LIKE / equality filters
// against IS NULL stay well-defined and we don't store empty-string
// placeholders in the audit log.
func nullableTextArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}
