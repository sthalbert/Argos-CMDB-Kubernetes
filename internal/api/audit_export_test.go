package api

import "context"

// WithAuditBagForTest exposes withAuditBag to the api_test package so
// handler tests can pre-seed a bag and inspect SetAuditSkip behavior
// without spinning up the full AuditMiddleware.
func WithAuditBagForTest(ctx context.Context) (context.Context, *AuditBagTestView) {
	c, bag := withAuditBag(ctx)
	return c, &AuditBagTestView{bag: bag}
}

// AuditBagTestView is a narrow read-only window into the audit bag for
// _test packages. Production code reads/writes the bag indirectly via
// SetAuditSkip / SetAuditDetails / SetAuditSkipReason.
type AuditBagTestView struct{ bag *auditBag }

func (v *AuditBagTestView) SkipForTest() bool         { return v.bag.skip }
func (v *AuditBagTestView) SkipReasonForTest() string { return v.bag.skipReason() }
