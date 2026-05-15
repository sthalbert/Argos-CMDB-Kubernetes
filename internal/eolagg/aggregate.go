// Package eolagg flattens EOL annotations from clusters, nodes, and VMs
// into a row shape suitable for tabular export (CSV / JSON) or future
// list endpoints. It is the server-side counterpart to the buildRows()
// function in ui/src/pages/EolDashboard.tsx; the two must stay in lockstep
// (shared fixtures under testdata/ enforce it).
package eolagg

import (
	"encoding/json"
	"sort"
	"strings"
)

const eolPrefix = "longue-vue.io/eol."

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

// annotation mirrors eol.Annotation; duplicated to keep eolagg free
// of an internal/eol dependency (the eol package depends on the API
// layer indirectly, and we want eolagg importable from anywhere).
type annotation struct {
	Product         string `json:"product"`
	Cycle           string `json:"cycle"`
	EOLStatus       string `json:"eol_status"`
	EOL             string `json:"eol"`
	Support         string `json:"support"`
	Latest          string `json:"latest"`
	LatestAvailable string `json:"latest_available"`
	CheckedAt       string `json:"checked_at"`
}

// Flatten walks clusters/nodes/vms and emits one Row per
// longue-vue.io/eol.<product> annotation. Output is grouped by entity
// type (cluster, node, vm), and within an entity sorted alphabetically
// by product name. Malformed annotation values are silently skipped —
// the dashboard does the same.
func Flatten(clusters []ClusterInput, nodes []NodeInput, vms []VMInput) []Row {
	out := make([]Row, 0)
	clusterNameByID := make(map[string]string, len(clusters))
	for _, c := range clusters {
		clusterNameByID[c.ID] = displayOrName(c.DisplayName, c.Name)
	}

	for _, c := range clusters {
		appendRows(&out, c.Annotations, Row{
			EntityType: "cluster",
			EntityID:   c.ID,
			EntityName: displayOrName(c.DisplayName, c.Name),
			Cluster:    displayOrName(c.DisplayName, c.Name),
		})
	}
	for _, n := range nodes {
		appendRows(&out, n.Annotations, Row{
			EntityType: "node",
			EntityID:   n.ID,
			EntityName: n.Name,
			Cluster:    clusterNameByID[n.ClusterID],
		})
	}
	for _, v := range vms {
		appendRows(&out, v.Annotations, Row{
			EntityType: "vm",
			EntityID:   v.ID,
			EntityName: displayOrName(v.DisplayName, v.Name),
			Cluster:    "",
		})
	}
	return out
}

func appendRows(out *[]Row, annotations map[string]string, base Row) {
	products := make([]string, 0, len(annotations))
	parsed := make(map[string]annotation, len(annotations))
	for k, v := range annotations {
		if !strings.HasPrefix(k, eolPrefix) {
			continue
		}
		var a annotation
		if err := json.Unmarshal([]byte(v), &a); err != nil {
			continue
		}
		product := strings.TrimPrefix(k, eolPrefix)
		parsed[product] = a
		products = append(products, product)
	}
	sort.Strings(products)
	for _, p := range products {
		a := parsed[p]
		row := base
		row.Product = a.Product
		row.Cycle = a.Cycle
		row.Status = a.EOLStatus
		row.EOLDate = a.EOL
		row.Latest = a.Latest
		row.LatestAvailable = a.LatestAvailable
		row.Support = a.Support
		row.CheckedAt = a.CheckedAt
		*out = append(*out, row)
	}
}

func displayOrName(display, name string) string {
	if display != "" {
		return display
	}
	return name
}
