package impact

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/api"
)

const maxPageSize = 500

// TraverserStore is the narrow store interface the traverser needs.
type TraverserStore interface {
	GetCluster(ctx context.Context, id uuid.UUID) (api.Cluster, error)
	GetNode(ctx context.Context, id uuid.UUID) (api.Node, error)
	GetNamespace(ctx context.Context, id uuid.UUID) (api.Namespace, error)
	GetPod(ctx context.Context, id uuid.UUID) (api.Pod, error)
	GetWorkload(ctx context.Context, id uuid.UUID) (api.Workload, error)
	GetService(ctx context.Context, id uuid.UUID) (api.Service, error)
	GetIngress(ctx context.Context, id uuid.UUID) (api.Ingress, error)
	GetPersistentVolume(ctx context.Context, id uuid.UUID) (api.PersistentVolume, error)
	GetPersistentVolumeClaim(ctx context.Context, id uuid.UUID) (api.PersistentVolumeClaim, error)

	ListNodes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Node, string, error)
	ListNamespaces(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.Namespace, string, error)
	ListPods(ctx context.Context, filter api.PodListFilter, limit int, cursor string) ([]api.Pod, string, error)
	ListWorkloads(ctx context.Context, filter api.WorkloadListFilter, limit int, cursor string) ([]api.Workload, string, error)
	ListServices(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Service, string, error)
	ListIngresses(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.Ingress, string, error)
	ListPersistentVolumes(ctx context.Context, clusterID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolume, string, error)
	ListPersistentVolumeClaims(ctx context.Context, namespaceID *uuid.UUID, limit int, cursor string) ([]api.PersistentVolumeClaim, string, error)
}

// Traverser walks FK relationships to build an impact graph.
type Traverser struct {
	store TraverserStore
}

// NewTraverser creates a Traverser.
func NewTraverser(store TraverserStore) *Traverser {
	return &Traverser{store: store}
}

// Traverse builds an impact graph starting from the given entity,
// walking relationships up to depth hops in both directions.
func (t *Traverser) Traverse(ctx context.Context, entityType EntityType, id uuid.UUID, depth int) (*Graph, error) {
	b := &builder{
		store:    t.store,
		seen:     make(map[string]bool),
		nodeMap:  make(map[string]GraphNode),
		edgeMap:  make(map[string]bool),
		maxDepth: depth,
		maxNodes: defaultMaxNodes,
	}

	root, err := b.fetchNode(ctx, entityType, id)
	if err != nil {
		return nil, fmt.Errorf("fetch root %s/%s: %w", entityType, id, err)
	}

	b.addNode(*root)
	if err := b.expand(ctx, *root, 0); err != nil {
		return nil, fmt.Errorf("expand %s/%s: %w", entityType, id, err)
	}

	nodes := make([]GraphNode, 0, len(b.nodeMap))
	for _, n := range b.nodeMap {
		nodes = append(nodes, n)
	}
	edges := make([]GraphEdge, 0, len(b.edges))
	edges = append(edges, b.edges...)

	return &Graph{Root: *root, Nodes: nodes, Edges: edges, Truncated: b.truncated}, nil
}

const defaultMaxNodes = 500

type builder struct {
	store     TraverserStore
	seen      map[string]bool // "type:id" → visited
	nodeMap   map[string]GraphNode
	edgeMap   map[string]bool // "from->to" dedup
	edges     []GraphEdge
	maxDepth  int
	maxNodes  int
	truncated bool
}

func nodeKey(t EntityType, id string) string { return string(t) + ":" + id }

//nolint:gocritic // hugeParam: GraphNode is passed by value intentionally for map storage.
func (b *builder) addNode(n GraphNode) {
	key := nodeKey(n.Type, n.ID)
	if _, exists := b.nodeMap[key]; exists {
		return
	}
	if b.maxNodes > 0 && len(b.nodeMap) >= b.maxNodes {
		b.truncated = true
		return
	}
	b.nodeMap[key] = n
}

func (b *builder) addEdge(from, to string, rel Relation) {
	key := from + "->" + to
	if b.edgeMap[key] {
		return
	}
	b.edgeMap[key] = true
	b.edges = append(b.edges, GraphEdge{From: from, To: to, Relation: rel})
}

func (b *builder) visited(t EntityType, id string) bool {
	return b.seen[nodeKey(t, id)]
}

func (b *builder) markVisited(t EntityType, id string) {
	b.seen[nodeKey(t, id)] = true
}

//nolint:gocyclo,wrapcheck // switch over 9 entity types is inherently high-branching; wrapping per-type adds noise.
func (b *builder) fetchNode(ctx context.Context, t EntityType, id uuid.UUID) (*GraphNode, error) {
	switch t {
	case TypeCluster:
		c, err := b.store.GetCluster(ctx, id)
		if err != nil {
			return nil, err
		}
		ver := ""
		if c.KubernetesVersion != nil {
			ver = *c.KubernetesVersion
		}
		return &GraphNode{ID: idStr(c.Id), Type: TypeCluster, Name: displayOrName(c.DisplayName, c.Name), Status: ver}, nil

	case TypeNode:
		n, err := b.store.GetNode(ctx, id)
		if err != nil {
			return nil, err
		}
		status := "NotReady"
		if n.Ready != nil && *n.Ready {
			status = "Ready"
		}
		return &GraphNode{ID: idStr(n.Id), Type: TypeNode, Name: displayOrName(n.DisplayName, n.Name), Status: status}, nil

	case TypeNamespace:
		ns, err := b.store.GetNamespace(ctx, id)
		if err != nil {
			return nil, err
		}
		return &GraphNode{ID: idStr(ns.Id), Type: TypeNamespace, Name: displayOrName(ns.DisplayName, ns.Name), Status: ptrStr(ns.Phase)}, nil

	case TypePod:
		p, err := b.store.GetPod(ctx, id)
		if err != nil {
			return nil, err
		}
		return &GraphNode{ID: idStr(p.Id), Type: TypePod, Name: p.Name, Status: ptrStr(p.Phase)}, nil

	case TypeWorkload:
		w, err := b.store.GetWorkload(ctx, id)
		if err != nil {
			return nil, err
		}
		status := ""
		if w.ReadyReplicas != nil && w.Replicas != nil {
			status = fmt.Sprintf("%d/%d", *w.ReadyReplicas, *w.Replicas)
		}
		return &GraphNode{ID: idStr(w.Id), Type: TypeWorkload, Name: w.Name, Status: status, Kind: string(w.Kind)}, nil

	case TypeService:
		s, err := b.store.GetService(ctx, id)
		if err != nil {
			return nil, err
		}
		svcType := ""
		if s.Type != nil {
			svcType = string(*s.Type)
		}
		return &GraphNode{ID: idStr(s.Id), Type: TypeService, Name: s.Name, Status: svcType}, nil

	case TypeIngress:
		ig, err := b.store.GetIngress(ctx, id)
		if err != nil {
			return nil, err
		}
		return &GraphNode{ID: idStr(ig.Id), Type: TypeIngress, Name: ig.Name, Status: ptrStr(ig.IngressClassName)}, nil

	case TypePersistentVolume:
		pv, err := b.store.GetPersistentVolume(ctx, id)
		if err != nil {
			return nil, err
		}
		return &GraphNode{ID: idStr(pv.Id), Type: TypePersistentVolume, Name: pv.Name, Status: ptrStr(pv.Phase)}, nil

	case TypePersistentVolumeClaim:
		pvc, err := b.store.GetPersistentVolumeClaim(ctx, id)
		if err != nil {
			return nil, err
		}
		return &GraphNode{ID: idStr(pvc.Id), Type: TypePersistentVolumeClaim, Name: pvc.Name, Status: ptrStr(pvc.Phase)}, nil

	default:
		return nil, fmt.Errorf("unknown entity type %q: %w", t, ErrUnknownType)
	}
}

//nolint:cyclop,gocyclo,gocritic // graph expansion has many entity types but each branch is simple.
func (b *builder) expand(ctx context.Context, node GraphNode, depth int) error {
	if b.truncated {
		return nil
	}
	if depth >= b.maxDepth {
		return nil
	}
	if b.visited(node.Type, node.ID) {
		return nil
	}
	b.markVisited(node.Type, node.ID)

	id, err := uuid.Parse(node.ID)
	if err != nil {
		return fmt.Errorf("parse id %q: %w", node.ID, err)
	}

	switch node.Type {
	case TypeCluster:
		if err := b.expandCluster(ctx, id, node.ID, depth); err != nil {
			return err
		}
	case TypeNode:
		if err := b.expandNode(ctx, id, node, depth); err != nil {
			return err
		}
	case TypeNamespace:
		if err := b.expandNamespace(ctx, id, node.ID, depth); err != nil {
			return err
		}
	case TypePod:
		if err := b.expandPod(ctx, id, node.ID, depth); err != nil {
			return err
		}
	case TypeWorkload:
		if err := b.expandWorkload(ctx, id, node.ID, depth); err != nil {
			return err
		}
	case TypePersistentVolume:
		if err := b.expandPV(ctx, id, node.ID, depth); err != nil {
			return err
		}
	case TypePersistentVolumeClaim:
		if err := b.expandPVC(ctx, id, node.ID, depth); err != nil {
			return err
		}
	// Services and ingresses are leaf-like: they connect upward to namespace only.
	case TypeService, TypeIngress:
		if err := b.expandNamespaceScoped(ctx, id, node, depth); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocritic // rangeValCopy: clarity over micro-optimisation in graph builder.
func (b *builder) expandCluster(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	// downstream: nodes
	nodes, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Node, string, error) {
		return b.store.ListNodes(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range nodes {
		gn := GraphNode{ID: idStr(n.Id), Type: TypeNode, Name: displayOrName(n.DisplayName, n.Name), Status: readyStatus(n.Ready)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}

	// downstream: namespaces
	nss, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Namespace, string, error) {
		return b.store.ListNamespaces(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}
	for _, ns := range nss {
		gn := GraphNode{ID: idStr(ns.Id), Type: TypeNamespace, Name: displayOrName(ns.DisplayName, ns.Name), Status: ptrStr(ns.Phase)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}

	// downstream: PVs
	pvs, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.PersistentVolume, string, error) {
		return b.store.ListPersistentVolumes(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list PVs: %w", err)
	}
	for _, pv := range pvs {
		gn := GraphNode{ID: idStr(pv.Id), Type: TypePersistentVolume, Name: pv.Name, Status: ptrStr(pv.Phase)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocritic // hugeParam: GraphNode passed by value matches expand() signature.
func (b *builder) expandNode(ctx context.Context, id uuid.UUID, node GraphNode, depth int) error {
	// upstream: cluster
	n, err := b.store.GetNode(ctx, id)
	if err != nil {
		return fmt.Errorf("get node: %w", err)
	}
	clusterNode, err := b.fetchNode(ctx, TypeCluster, n.ClusterId)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	b.addNode(*clusterNode)
	b.addEdge(clusterNode.ID, node.ID, RelContains)
	if err := b.expand(ctx, *clusterNode, depth+1); err != nil {
		return err
	}

	// downstream: pods on this node (by node_name)
	nodeName := n.Name
	pods, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Pod, string, error) {
		return b.store.ListPods(ctx, api.PodListFilter{NodeName: &nodeName}, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list pods by node: %w", err)
	}
	for _, p := range pods {
		gn := GraphNode{ID: idStr(p.Id), Type: TypePod, Name: p.Name, Status: ptrStr(p.Phase)}
		b.addNode(gn)
		b.addEdge(node.ID, gn.ID, RelHosts)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // walks 5 child types; each branch is a simple list+add pattern.
func (b *builder) expandNamespace(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	// upstream: cluster
	ns, err := b.store.GetNamespace(ctx, id)
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	clusterNode, err := b.fetchNode(ctx, TypeCluster, ns.ClusterId)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	b.addNode(*clusterNode)
	b.addEdge(clusterNode.ID, nodeID, RelContains)
	if err := b.expand(ctx, *clusterNode, depth+1); err != nil {
		return err
	}

	// downstream: workloads
	wls, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Workload, string, error) {
		return b.store.ListWorkloads(ctx, api.WorkloadListFilter{NamespaceID: &id}, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list workloads: %w", err)
	}
	for _, w := range wls {
		status := ""
		if w.ReadyReplicas != nil && w.Replicas != nil {
			status = fmt.Sprintf("%d/%d", *w.ReadyReplicas, *w.Replicas)
		}
		gn := GraphNode{ID: idStr(w.Id), Type: TypeWorkload, Name: w.Name, Status: status, Kind: string(w.Kind)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}

	// downstream: services
	svcs, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Service, string, error) {
		return b.store.ListServices(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	for _, s := range svcs {
		svcType := ""
		if s.Type != nil {
			svcType = string(*s.Type)
		}
		gn := GraphNode{ID: idStr(s.Id), Type: TypeService, Name: s.Name, Status: svcType}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
	}

	// downstream: ingresses
	ings, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Ingress, string, error) {
		return b.store.ListIngresses(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list ingresses: %w", err)
	}
	for _, ig := range ings {
		gn := GraphNode{ID: idStr(ig.Id), Type: TypeIngress, Name: ig.Name, Status: ptrStr(ig.IngressClassName)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
	}

	// downstream: PVCs
	pvcs, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.PersistentVolumeClaim, string, error) {
		return b.store.ListPersistentVolumeClaims(ctx, &id, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list PVCs: %w", err)
	}
	for _, pvc := range pvcs {
		gn := GraphNode{ID: idStr(pvc.Id), Type: TypePersistentVolumeClaim, Name: pvc.Name, Status: ptrStr(pvc.Phase)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelContains)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocyclo,gocognit // pod walks 3 upstreams with nullable FKs; complexity is inherent.
func (b *builder) expandPod(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	p, err := b.store.GetPod(ctx, id)
	if err != nil {
		return fmt.Errorf("get pod: %w", err)
	}

	// upstream: namespace
	nsNode, err := b.fetchNode(ctx, TypeNamespace, p.NamespaceId)
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	b.addNode(*nsNode)
	b.addEdge(nsNode.ID, nodeID, RelContains)
	if err := b.expand(ctx, *nsNode, depth+1); err != nil {
		return err
	}

	// upstream: workload
	if p.WorkloadId != nil {
		wlNode, err := b.fetchNode(ctx, TypeWorkload, *p.WorkloadId)
		if err == nil {
			b.addNode(*wlNode)
			b.addEdge(wlNode.ID, nodeID, RelOwns)
			if err := b.expand(ctx, *wlNode, depth+1); err != nil {
				return err
			}
		}
	}

	// upstream: node (by node_name string match)
	if p.NodeName != nil && *p.NodeName != "" {
		// Find the node by listing with the cluster's nodes — we need the
		// namespace → cluster path to scope the search.
		ns, err := b.store.GetNamespace(ctx, p.NamespaceId)
		if err == nil {
			allNodes, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Node, string, error) {
				return b.store.ListNodes(ctx, &ns.ClusterId, maxPageSize, cursor)
			})
			if err == nil {
				for i := range allNodes {
					n := &allNodes[i]
					if n.Name != *p.NodeName || n.Id == nil {
						continue
					}
					gn := GraphNode{ID: idStr(n.Id), Type: TypeNode, Name: displayOrName(n.DisplayName, n.Name), Status: readyStatus(n.Ready)}
					b.addNode(gn)
					b.addEdge(gn.ID, nodeID, RelHosts)
					if err := b.expand(ctx, gn, depth+1); err != nil {
						return err
					}
					break
				}
			}
		}
	}

	return nil
}

func (b *builder) expandWorkload(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	w, err := b.store.GetWorkload(ctx, id)
	if err != nil {
		return fmt.Errorf("get workload: %w", err)
	}

	// upstream: namespace
	nsNode, err := b.fetchNode(ctx, TypeNamespace, w.NamespaceId)
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	b.addNode(*nsNode)
	b.addEdge(nsNode.ID, nodeID, RelContains)
	if err := b.expand(ctx, *nsNode, depth+1); err != nil {
		return err
	}

	// downstream: pods owned by this workload
	pods, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Pod, string, error) {
		return b.store.ListPods(ctx, api.PodListFilter{NamespaceID: &w.NamespaceId}, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	for _, p := range pods {
		if p.WorkloadId == nil || *p.WorkloadId != id {
			continue
		}
		gn := GraphNode{ID: idStr(p.Id), Type: TypePod, Name: p.Name, Status: ptrStr(p.Phase)}
		b.addNode(gn)
		b.addEdge(nodeID, gn.ID, RelOwns)
		if err := b.expand(ctx, gn, depth+1); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocyclo,gocritic // PV scans all namespaces for bound PVCs; loop nesting is inherent.
func (b *builder) expandPV(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	pv, err := b.store.GetPersistentVolume(ctx, id)
	if err != nil {
		return fmt.Errorf("get PV: %w", err)
	}

	// upstream: cluster
	clusterNode, err := b.fetchNode(ctx, TypeCluster, pv.ClusterId)
	if err != nil {
		return fmt.Errorf("get cluster: %w", err)
	}
	b.addNode(*clusterNode)
	b.addEdge(clusterNode.ID, nodeID, RelContains)
	if err := b.expand(ctx, *clusterNode, depth+1); err != nil {
		return err
	}

	// downstream: PVCs bound to this PV — scan all PVCs in the cluster
	// and match on bound_volume_id.
	nss, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.Namespace, string, error) {
		return b.store.ListNamespaces(ctx, &pv.ClusterId, maxPageSize, cursor)
	})
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}
	for _, ns := range nss {
		nsID := *ns.Id
		pvcs, err := collectAll(ctx, func(ctx context.Context, cursor string) ([]api.PersistentVolumeClaim, string, error) {
			return b.store.ListPersistentVolumeClaims(ctx, &nsID, maxPageSize, cursor)
		})
		if err != nil {
			continue
		}
		for _, pvc := range pvcs {
			if pvc.BoundVolumeId != nil && *pvc.BoundVolumeId == id {
				gn := GraphNode{ID: idStr(pvc.Id), Type: TypePersistentVolumeClaim, Name: pvc.Name, Status: ptrStr(pvc.Phase)}
				b.addNode(gn)
				b.addEdge(nodeID, gn.ID, RelBinds)
				if err := b.expand(ctx, gn, depth+1); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (b *builder) expandPVC(ctx context.Context, id uuid.UUID, nodeID string, depth int) error {
	pvc, err := b.store.GetPersistentVolumeClaim(ctx, id)
	if err != nil {
		return fmt.Errorf("get PVC: %w", err)
	}

	// upstream: namespace
	nsNode, err := b.fetchNode(ctx, TypeNamespace, pvc.NamespaceId)
	if err != nil {
		return fmt.Errorf("get namespace: %w", err)
	}
	b.addNode(*nsNode)
	b.addEdge(nsNode.ID, nodeID, RelContains)
	if err := b.expand(ctx, *nsNode, depth+1); err != nil {
		return err
	}

	// upstream: bound PV
	if pvc.BoundVolumeId != nil {
		pvNode, err := b.fetchNode(ctx, TypePersistentVolume, *pvc.BoundVolumeId)
		if err == nil {
			b.addNode(*pvNode)
			b.addEdge(pvNode.ID, nodeID, RelBinds)
			if err := b.expand(ctx, *pvNode, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

//nolint:gocritic,wrapcheck // hugeParam: matches expand() sig; wrapcheck: errors surface to handler.
func (b *builder) expandNamespaceScoped(ctx context.Context, id uuid.UUID, node GraphNode, depth int) error {
	// Services and ingresses only connect upward to their namespace.
	var nsID uuid.UUID
	switch node.Type {
	case TypeService:
		s, err := b.store.GetService(ctx, id)
		if err != nil {
			return err
		}
		nsID = s.NamespaceId
	case TypeIngress:
		ig, err := b.store.GetIngress(ctx, id)
		if err != nil {
			return err
		}
		nsID = ig.NamespaceId
	default:
		return nil
	}

	nsNode, err := b.fetchNode(ctx, TypeNamespace, nsID)
	if err != nil {
		return err
	}
	b.addNode(*nsNode)
	b.addEdge(nsNode.ID, node.ID, RelContains)
	return b.expand(ctx, *nsNode, depth+1)
}

// --- helpers ----------------------------------------------------------------

func idStr(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func displayOrName(display *string, name string) string {
	if display != nil && *display != "" {
		return *display
	}
	return name
}

func readyStatus(ready *bool) string {
	if ready != nil && *ready {
		return "Ready"
	}
	return "NotReady"
}

// collectAll paginates through all results for a list query.
func collectAll[T any](ctx context.Context, fn func(ctx context.Context, cursor string) ([]T, string, error)) ([]T, error) {
	var all []T
	cursor := ""
	for {
		items, next, err := fn(ctx, cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}
