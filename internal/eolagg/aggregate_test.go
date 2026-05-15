package eolagg

import (
	"testing"
)

func TestFlatten_Empty(t *testing.T) {
	rows := Flatten(nil, nil, nil)
	if len(rows) != 0 {
		t.Fatalf("expected zero rows, got %d", len(rows))
	}
}

func TestRow_Fields(t *testing.T) {
	// Compile-time check that the Row struct exposes the columns
	// the CSV writer will need.
	r := Row{
		EntityType:      "cluster",
		EntityID:        "id",
		EntityName:      "name",
		Cluster:         "c",
		Product:         "vault",
		Cycle:           "1.15",
		Status:          "eol",
		EOLDate:         "2024-12-09",
		Latest:          "1.15.5",
		LatestAvailable: "1.18.0",
		Support:         "2024-06-09",
		CheckedAt:       "2026-05-15T00:00:00Z",
	}
	if r.EntityType == "" {
		t.Fatalf("EntityType not addressable")
	}
}
