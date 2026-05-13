package api

// UpsertOutcome classifies the result of an Upsert* operation so the
// audit middleware can decide whether to record a row. A tick that only
// refreshes clock fields (last_seen / updated_at) returns OutcomeNoChange
// and is filtered out; everything else is recorded as before.
//
// The enum lives in internal/api (not internal/store) to avoid an
// import cycle: internal/store imports internal/api for entity types,
// so the Store interface — which lives here — must reference a type
// declared here. See ADR-0024.
type UpsertOutcome int

const (
	// OutcomeInserted — the row did not exist; INSERT path was taken.
	OutcomeInserted UpsertOutcome = iota
	// OutcomeBusinessChanged — the row existed and ≥1 business field changed.
	OutcomeBusinessChanged
	// OutcomeNoChange — the row existed and only clock fields were touched.
	OutcomeNoChange
)

// String returns a stable lowercase token suitable for a Prometheus label.
func (o UpsertOutcome) String() string {
	switch o {
	case OutcomeInserted:
		return "inserted"
	case OutcomeBusinessChanged:
		return "business_changed"
	case OutcomeNoChange:
		return "no_change"
	default:
		return "unknown"
	}
}
