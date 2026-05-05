// Package timetravel implements the history-capture subsystem from ADR-0021 §4.
// It writes sidecar history rows for clusters, namespaces, nodes, and workloads
// whenever a watched field changes, allowing point-in-time cartography queries.
package timetravel

import (
	"context"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/auth"
)

// Actor records who caused a history row to be written. Mirrors the
// audit_events.actor_* shape so the two tables can be joined on
// (occurred_at, actor_id).
type Actor struct {
	// ID is nil for system/collector-driven changes.
	ID *uuid.UUID
	// Kind is one of "user", "token", "system", "collector".
	Kind string
}

// ActorFromContext extracts the auth.Caller from ctx and maps it to an Actor.
// Falls back to {Kind: "system"} when no caller is on the context (e.g. the
// collector goroutine which doesn't go through the HTTP auth middleware).
func ActorFromContext(ctx context.Context) Actor {
	caller := auth.CallerFromContext(ctx)
	if caller == nil {
		return Actor{Kind: "system"}
	}
	switch caller.Kind {
	case auth.CallerKindUser:
		id := caller.UserID
		return Actor{ID: &id, Kind: "user"}
	case auth.CallerKindToken:
		id := caller.TokenID
		return Actor{ID: &id, Kind: "token"}
	default:
		return Actor{Kind: "system"}
	}
}
