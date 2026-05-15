// Package eolagg flattens EOL annotations from clusters, nodes, and VMs
// into a row shape suitable for tabular export (CSV / JSON) or future
// list endpoints. It is the server-side counterpart to the buildRows()
// function in ui/src/pages/EolDashboard.tsx; the two must stay in lockstep
// (shared fixtures under testdata/ enforce it).
package eolagg

// Row is one flattened (entity, product) tuple.
type Row struct {
	EntityType      string `json:"entity_type"`
	EntityID        string `json:"entity_id"`
	EntityName      string `json:"entity_name"`
	Cluster         string `json:"cluster"`
	Product         string `json:"product"`
	Cycle           string `json:"cycle"`
	Status          string `json:"status"`
	EOLDate         string `json:"eol_date"`
	Latest          string `json:"latest"`
	LatestAvailable string `json:"latest_available"`
	Support         string `json:"support"`
	CheckedAt       string `json:"checked_at"`
}

// Flatten is a stub that returns no rows. Filled in by Task 2.
func Flatten(_ []ClusterInput, _ []NodeInput, _ []VMInput) []Row {
	return nil
}

// ClusterInput is the minimal cluster shape Flatten needs.
type ClusterInput struct {
	ID          string
	Name        string
	DisplayName string
	Annotations map[string]string
}

// NodeInput is the minimal node shape Flatten needs.
type NodeInput struct {
	ID          string
	Name        string
	ClusterID   string
	Annotations map[string]string
}

// VMInput is the minimal VM shape Flatten needs.
type VMInput struct {
	ID          string
	Name        string
	DisplayName string
	Annotations map[string]string
}
