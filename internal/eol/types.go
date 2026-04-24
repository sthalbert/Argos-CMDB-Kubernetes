// Package eol enriches CMDB entities with end-of-life lifecycle data
// sourced from endoflife.date (ADR-0012).
package eol

import (
	"errors"
	"time"
)

// Status represents the lifecycle state of a software version.
type Status string

// EOL status values.
const (
	StatusEOL            Status = "eol"
	StatusApproachingEOL Status = "approaching_eol"
	StatusSupported      Status = "supported"
	StatusUnknown        Status = "unknown"
)

// ErrProductNotFound is returned when endoflife.date has no data for a product.
var ErrProductNotFound = errors.New("product not found on endoflife.date")

// ErrCycleNotFound is returned when a product has no matching cycle.
var ErrCycleNotFound = errors.New("cycle not found")

// ErrUnexpectedStatus is returned when endoflife.date returns a non-200/404 status.
var ErrUnexpectedStatus = errors.New("unexpected HTTP status from endoflife.date")

// Cycle is one release cycle as returned by the endoflife.date API.
type Cycle struct {
	Cycle             string `json:"cycle"`
	ReleaseDate       string `json:"releaseDate"`
	EOL               any    `json:"eol"`     // string date or bool
	Support           any    `json:"support"` // string date or bool
	Latest            string `json:"latest"`
	LatestReleaseDate string `json:"latestReleaseDate"`
	LTS               any    `json:"lts"` // bool or string
}

// EOLDate parses the eol field. Returns zero time and false when the
// product has no fixed EOL date (eol=false) or the field is absent.
func (c *Cycle) EOLDate() (time.Time, bool) {
	return parseDateField(c.EOL)
}

// SupportDate parses the support field.
func (c *Cycle) SupportDate() (time.Time, bool) {
	return parseDateField(c.Support)
}

func parseDateField(v any) (time.Time, bool) {
	switch val := v.(type) {
	case string:
		t, err := time.Parse("2006-01-02", val)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	case bool:
		// false means "not yet EOL" / "no fixed date"; true means "already EOL, date unknown"
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// Annotation is the structured payload stored as a JSON string value
// in the entity's annotations map under "argos.io/eol.<product>".
type Annotation struct {
	Product   string `json:"product"`
	Cycle     string `json:"cycle"`
	EOL       string `json:"eol,omitempty"`
	EOLStatus Status `json:"eol_status"`
	Support   string `json:"support,omitempty"`
	Latest    string `json:"latest,omitempty"`
	CheckedAt string `json:"checked_at"`
}

// MatchResult is the output of a version matcher: the endoflife.date
// product identifier and the extracted major.minor cycle string.
type MatchResult struct {
	Product string // endoflife.date product id, e.g. "kubernetes"
	Cycle   string // extracted cycle, e.g. "1.28"
}
