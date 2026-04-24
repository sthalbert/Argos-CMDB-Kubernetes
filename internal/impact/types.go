// Package impact provides dependency-graph traversal for CMDB entities,
// enabling blast-radius analysis when a component changes (ADR-0013).
package impact

import "errors"

// ErrUnknownType is returned when the entity type is not recognised.
var ErrUnknownType = errors.New("unknown entity type")

// EntityType identifies the kind of CMDB entity.
type EntityType string

// Supported entity types.
const (
	TypeCluster               EntityType = "cluster"
	TypeNode                  EntityType = "node"
	TypeNamespace             EntityType = "namespace"
	TypePod                   EntityType = "pod"
	TypeWorkload              EntityType = "workload"
	TypeService               EntityType = "service"
	TypeIngress               EntityType = "ingress"
	TypePersistentVolume      EntityType = "persistentvolume"
	TypePersistentVolumeClaim EntityType = "persistentvolumeclaim"
)

// Relation describes the nature of a dependency edge.
type Relation string

// Edge relation types.
const (
	RelContains Relation = "contains"
	RelOwns     Relation = "owns"
	RelHosts    Relation = "hosts"
	RelBinds    Relation = "binds"
)

// GraphNode is one entity in the impact graph.
type GraphNode struct {
	ID     string     `json:"id"`
	Type   EntityType `json:"type"`
	Name   string     `json:"name"`
	Status string     `json:"status,omitempty"`
	Kind   string     `json:"kind,omitempty"` // workload kind (Deployment, etc.)
}

// GraphEdge connects two graph nodes.
type GraphEdge struct {
	From     string   `json:"from"`
	To       string   `json:"to"`
	Relation Relation `json:"relation"`
}

// Graph is the API response: a flat set of nodes and edges.
type Graph struct {
	Root      GraphNode   `json:"root"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated,omitempty"`
}

// ValidEntityType returns true when t is a recognised entity type.
func ValidEntityType(t string) bool {
	switch EntityType(t) {
	case TypeCluster, TypeNode, TypeNamespace, TypePod, TypeWorkload,
		TypeService, TypeIngress, TypePersistentVolume, TypePersistentVolumeClaim:
		return true
	}
	return false
}
