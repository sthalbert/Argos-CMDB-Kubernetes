package api

// Per-entity ANSSI cartography layer assignments, per ADR-0002. The layer is
// a property of the entity *kind*, not of any individual row, so it is set
// by the server on every response rather than persisted.
//
// When adding a new entity kind, add its layer constant here and use the
// matching decorator helper below in its handlers.
const (
	LayerCluster   = InfrastructureLogical
	LayerNode      = InfrastructurePhysical
	LayerNamespace = InfrastructureLogical
)

func withClusterLayer(c Cluster) Cluster {
	l := LayerCluster
	c.Layer = &l
	return c
}

func withNodeLayer(n Node) Node {
	l := LayerNode
	n.Layer = &l
	return n
}

func withNamespaceLayer(n Namespace) Namespace {
	l := LayerNamespace
	n.Layer = &l
	return n
}
