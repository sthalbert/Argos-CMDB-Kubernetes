package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
)

// memStore is an in-memory api.Store implementation used to exercise the
// HTTP handlers without a PostgreSQL dependency.
type memStore struct {
	mu                sync.Mutex
	byID              map[uuid.UUID]Cluster
	byName            map[string]uuid.UUID
	nodesByID         map[uuid.UUID]Node
	nodesByNatKey     map[string]uuid.UUID // "<cluster_id>/<name>" -> node id
	nsByID            map[uuid.UUID]Namespace
	nsByNatKey        map[string]uuid.UUID // "<cluster_id>/<name>" -> namespace id
	podsByID          map[uuid.UUID]Pod
	podsByNatKey      map[string]uuid.UUID // "<namespace_id>/<name>" -> pod id
	workloadsByID     map[uuid.UUID]Workload
	workloadsByNatKey map[string]uuid.UUID // "<namespace_id>/<kind>/<name>" -> workload id
	servicesByID      map[uuid.UUID]Service
	servicesByNatKey  map[string]uuid.UUID // "<namespace_id>/<name>" -> service id
	ingressesByID     map[uuid.UUID]Ingress
	ingressesByNatKey map[string]uuid.UUID // "<namespace_id>/<name>" -> ingress id
	pvsByID           map[uuid.UUID]PersistentVolume
	pvsByNatKey       map[string]uuid.UUID // "<cluster_id>/<name>" -> pv id
	pvcsByID          map[uuid.UUID]PersistentVolumeClaim
	pvcsByNatKey      map[string]uuid.UUID // "<namespace_id>/<name>" -> pvc id
	authState         memAuthState         // users / sessions / tokens (ADR-0007)
	pingErr           error
	createdN          int
}

func newMemStore() *memStore {
	return &memStore{
		byID:              make(map[uuid.UUID]Cluster),
		byName:            make(map[string]uuid.UUID),
		nodesByID:         make(map[uuid.UUID]Node),
		nodesByNatKey:     make(map[string]uuid.UUID),
		nsByID:            make(map[uuid.UUID]Namespace),
		nsByNatKey:        make(map[string]uuid.UUID),
		podsByID:          make(map[uuid.UUID]Pod),
		podsByNatKey:      make(map[string]uuid.UUID),
		workloadsByID:     make(map[uuid.UUID]Workload),
		workloadsByNatKey: make(map[string]uuid.UUID),
		servicesByID:      make(map[uuid.UUID]Service),
		servicesByNatKey:  make(map[string]uuid.UUID),
		ingressesByID:     make(map[uuid.UUID]Ingress),
		ingressesByNatKey: make(map[string]uuid.UUID),
		pvsByID:           make(map[uuid.UUID]PersistentVolume),
		pvsByNatKey:       make(map[string]uuid.UUID),
		pvcsByID:          make(map[uuid.UUID]PersistentVolumeClaim),
		pvcsByNatKey:      make(map[string]uuid.UUID),
		authState:         newMemAuthState(),
	}
}

func serviceNatKey(namespaceID uuid.UUID, name string) string {
	return namespaceID.String() + "/" + name
}

func ingressNatKey(namespaceID uuid.UUID, name string) string {
	return namespaceID.String() + "/" + name
}

func workloadNatKey(namespaceID uuid.UUID, kind WorkloadKind, name string) string {
	return namespaceID.String() + "/" + string(kind) + "/" + name
}

func podNatKey(namespaceID uuid.UUID, name string) string {
	return namespaceID.String() + "/" + name
}

func nodeNatKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func nsNatKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func (m *memStore) Ping(_ context.Context) error { return m.pingErr }

func (m *memStore) CreateCluster(_ context.Context, in ClusterCreate) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.byName[in.Name]; exists {
		return Cluster{}, fmt.Errorf("duplicate: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	c := Cluster{
		Id:                &id,
		Name:              in.Name,
		DisplayName:       in.DisplayName,
		Environment:       in.Environment,
		Provider:          in.Provider,
		Region:            in.Region,
		KubernetesVersion: in.KubernetesVersion,
		ApiEndpoint:       in.ApiEndpoint,
		Labels:            in.Labels,
		Owner:             in.Owner,
		Criticality:       in.Criticality,
		Notes:             in.Notes,
		RunbookUrl:        in.RunbookUrl,
		Annotations:       in.Annotations,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}
	m.byID[id] = c
	m.byName[in.Name] = id
	return c, nil
}

func (m *memStore) GetCluster(_ context.Context, id uuid.UUID) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	return c, nil
}

func (m *memStore) GetClusterByName(_ context.Context, name string) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byName[name]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	return m.byID[id], nil
}

func (m *memStore) ListClusters(_ context.Context, limit int, _ string) ([]Cluster, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Cluster, 0, len(m.byID))
	for _, c := range m.byID {
		out = append(out, c)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateCluster(_ context.Context, id uuid.UUID, in ClusterUpdate) (Cluster, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return Cluster{}, ErrNotFound
	}
	if in.DisplayName != nil {
		c.DisplayName = in.DisplayName
	}
	if in.Environment != nil {
		c.Environment = in.Environment
	}
	if in.Provider != nil {
		c.Provider = in.Provider
	}
	if in.Region != nil {
		c.Region = in.Region
	}
	if in.KubernetesVersion != nil {
		c.KubernetesVersion = in.KubernetesVersion
	}
	if in.ApiEndpoint != nil {
		c.ApiEndpoint = in.ApiEndpoint
	}
	if in.Labels != nil {
		c.Labels = in.Labels
	}
	if in.Owner != nil {
		c.Owner = in.Owner
	}
	if in.Criticality != nil {
		c.Criticality = in.Criticality
	}
	if in.Notes != nil {
		c.Notes = in.Notes
	}
	if in.RunbookUrl != nil {
		c.RunbookUrl = in.RunbookUrl
	}
	if in.Annotations != nil {
		c.Annotations = in.Annotations
	}
	now := time.Now().UTC()
	c.UpdatedAt = &now
	m.byID[id] = c
	return c, nil
}

func (m *memStore) DeleteCluster(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.byID, id)
	delete(m.byName, c.Name)
	return nil
}

// copyNodeMutableFromCreate mirrors every NodeMutable-derived field from a
// NodeCreate payload onto a Node. The fake carries them as-is so tests can
// round-trip any of the 23 fields the collector now populates.
// copyNodeCollectorFieldsFromCreate mirrors the UpsertNode DO UPDATE
// SET clause on the PG side — every field the collector derives from
// the Kubernetes API. Used for both fresh inserts and on-conflict
// updates so an operator's curated edits are not overwritten on the
// next collector tick (ADR-0008 invariant).
func copyNodeCollectorFieldsFromCreate(n *Node, in NodeCreate) {
	n.DisplayName = in.DisplayName
	n.Role = in.Role
	n.KubeletVersion = in.KubeletVersion
	n.KubeProxyVersion = in.KubeProxyVersion
	n.ContainerRuntimeVersion = in.ContainerRuntimeVersion
	n.OsImage = in.OsImage
	n.OperatingSystem = in.OperatingSystem
	n.KernelVersion = in.KernelVersion
	n.Architecture = in.Architecture
	n.InternalIp = in.InternalIp
	n.ExternalIp = in.ExternalIp
	n.PodCidr = in.PodCidr
	n.ProviderId = in.ProviderId
	n.InstanceType = in.InstanceType
	n.Zone = in.Zone
	n.CapacityCpu = in.CapacityCpu
	n.CapacityMemory = in.CapacityMemory
	n.CapacityPods = in.CapacityPods
	n.CapacityEphemeralStorage = in.CapacityEphemeralStorage
	n.AllocatableCpu = in.AllocatableCpu
	n.AllocatableMemory = in.AllocatableMemory
	n.AllocatablePods = in.AllocatablePods
	n.AllocatableEphemeralStorage = in.AllocatableEphemeralStorage
	n.Conditions = in.Conditions
	n.Taints = in.Taints
	n.Unschedulable = in.Unschedulable
	n.Ready = in.Ready
	n.Labels = in.Labels
}

// copyNodeCuratedFieldsFromCreate is the second half: the
// operator-owned columns (ADR-0008). Only applied on fresh inserts,
// never on upsert-conflict — the collector never carries these values.
func copyNodeCuratedFieldsFromCreate(n *Node, in NodeCreate) {
	n.Owner = in.Owner
	n.Criticality = in.Criticality
	n.Notes = in.Notes
	n.RunbookUrl = in.RunbookUrl
	n.Annotations = in.Annotations
	n.HardwareModel = in.HardwareModel
}

// copyNodeMutableFromCreate sets every mutable column (collector-owned
// + curated). Used for the insert path on CreateNode and UpsertNode.
func copyNodeMutableFromCreate(n *Node, in NodeCreate) {
	copyNodeCollectorFieldsFromCreate(n, in)
	copyNodeCuratedFieldsFromCreate(n, in)
}

func (m *memStore) CreateNode(_ context.Context, in NodeCreate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Node{}, ErrNotFound
	}
	key := nodeNatKey(in.ClusterId, in.Name)
	if _, dup := m.nodesByNatKey[key]; dup {
		return Node{}, fmt.Errorf("duplicate node: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	n := Node{
		Id:        &id,
		ClusterId: in.ClusterId,
		Name:      in.Name,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	copyNodeMutableFromCreate(&n, in)
	m.nodesByID[id] = n
	m.nodesByNatKey[key] = id
	return n, nil
}

func (m *memStore) GetNode(_ context.Context, id uuid.UUID) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	return n, nil
}

func (m *memStore) ListNodes(_ context.Context, clusterID *uuid.UUID, limit int, _ string) ([]Node, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Node, 0, len(m.nodesByID))
	for _, n := range m.nodesByID {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateNode(_ context.Context, id uuid.UUID, in NodeUpdate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return Node{}, ErrNotFound
	}
	if in.DisplayName != nil {
		n.DisplayName = in.DisplayName
	}
	if in.Role != nil {
		n.Role = in.Role
	}
	if in.KubeletVersion != nil {
		n.KubeletVersion = in.KubeletVersion
	}
	if in.KubeProxyVersion != nil {
		n.KubeProxyVersion = in.KubeProxyVersion
	}
	if in.ContainerRuntimeVersion != nil {
		n.ContainerRuntimeVersion = in.ContainerRuntimeVersion
	}
	if in.OsImage != nil {
		n.OsImage = in.OsImage
	}
	if in.OperatingSystem != nil {
		n.OperatingSystem = in.OperatingSystem
	}
	if in.KernelVersion != nil {
		n.KernelVersion = in.KernelVersion
	}
	if in.Architecture != nil {
		n.Architecture = in.Architecture
	}
	if in.InternalIp != nil {
		n.InternalIp = in.InternalIp
	}
	if in.ExternalIp != nil {
		n.ExternalIp = in.ExternalIp
	}
	if in.PodCidr != nil {
		n.PodCidr = in.PodCidr
	}
	if in.ProviderId != nil {
		n.ProviderId = in.ProviderId
	}
	if in.InstanceType != nil {
		n.InstanceType = in.InstanceType
	}
	if in.Zone != nil {
		n.Zone = in.Zone
	}
	if in.CapacityCpu != nil {
		n.CapacityCpu = in.CapacityCpu
	}
	if in.CapacityMemory != nil {
		n.CapacityMemory = in.CapacityMemory
	}
	if in.CapacityPods != nil {
		n.CapacityPods = in.CapacityPods
	}
	if in.CapacityEphemeralStorage != nil {
		n.CapacityEphemeralStorage = in.CapacityEphemeralStorage
	}
	if in.AllocatableCpu != nil {
		n.AllocatableCpu = in.AllocatableCpu
	}
	if in.AllocatableMemory != nil {
		n.AllocatableMemory = in.AllocatableMemory
	}
	if in.AllocatablePods != nil {
		n.AllocatablePods = in.AllocatablePods
	}
	if in.AllocatableEphemeralStorage != nil {
		n.AllocatableEphemeralStorage = in.AllocatableEphemeralStorage
	}
	if in.Conditions != nil {
		n.Conditions = in.Conditions
	}
	if in.Taints != nil {
		n.Taints = in.Taints
	}
	if in.Unschedulable != nil {
		n.Unschedulable = in.Unschedulable
	}
	if in.Ready != nil {
		n.Ready = in.Ready
	}
	if in.Labels != nil {
		n.Labels = in.Labels
	}
	if in.Owner != nil {
		n.Owner = in.Owner
	}
	if in.Criticality != nil {
		n.Criticality = in.Criticality
	}
	if in.Notes != nil {
		n.Notes = in.Notes
	}
	if in.RunbookUrl != nil {
		n.RunbookUrl = in.RunbookUrl
	}
	if in.Annotations != nil {
		n.Annotations = in.Annotations
	}
	if in.HardwareModel != nil {
		n.HardwareModel = in.HardwareModel
	}
	now := time.Now().UTC()
	n.UpdatedAt = &now
	m.nodesByID[id] = n
	return n, nil
}

func (m *memStore) DeleteNode(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodesByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.nodesByID, id)
	delete(m.nodesByNatKey, nodeNatKey(n.ClusterId, n.Name))
	return nil
}

func (m *memStore) CreateNamespace(_ context.Context, in NamespaceCreate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Namespace{}, ErrNotFound
	}
	key := nsNatKey(in.ClusterId, in.Name)
	if _, dup := m.nsByNatKey[key]; dup {
		return Namespace{}, fmt.Errorf("duplicate namespace: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	n := Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		Owner:       in.Owner,
		Criticality: in.Criticality,
		Notes:       in.Notes,
		RunbookUrl:  in.RunbookUrl,
		Annotations: in.Annotations,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.nsByID[id] = n
	m.nsByNatKey[key] = id
	return n, nil
}

func (m *memStore) GetNamespace(_ context.Context, id uuid.UUID) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return Namespace{}, ErrNotFound
	}
	return n, nil
}

func (m *memStore) ListNamespaces(_ context.Context, clusterID *uuid.UUID, limit int, _ string) ([]Namespace, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Namespace, 0, len(m.nsByID))
	for _, n := range m.nsByID {
		if clusterID != nil && n.ClusterId != *clusterID {
			continue
		}
		out = append(out, n)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateNamespace(_ context.Context, id uuid.UUID, in NamespaceUpdate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return Namespace{}, ErrNotFound
	}
	if in.DisplayName != nil {
		n.DisplayName = in.DisplayName
	}
	if in.Phase != nil {
		n.Phase = in.Phase
	}
	if in.Labels != nil {
		n.Labels = in.Labels
	}
	if in.Owner != nil {
		n.Owner = in.Owner
	}
	if in.Criticality != nil {
		n.Criticality = in.Criticality
	}
	if in.Notes != nil {
		n.Notes = in.Notes
	}
	if in.RunbookUrl != nil {
		n.RunbookUrl = in.RunbookUrl
	}
	if in.Annotations != nil {
		n.Annotations = in.Annotations
	}
	now := time.Now().UTC()
	n.UpdatedAt = &now
	m.nsByID[id] = n
	return n, nil
}

func (m *memStore) DeleteNamespace(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.nsByID, id)
	delete(m.nsByNatKey, nsNatKey(n.ClusterId, n.Name))
	return nil
}

func (m *memStore) CreatePod(_ context.Context, in PodCreate) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Pod{}, ErrNotFound
	}
	if in.WorkloadId != nil {
		if _, ok := m.workloadsByID[*in.WorkloadId]; !ok {
			return Pod{}, fmt.Errorf("workload %s does not exist: %w", in.WorkloadId, ErrNotFound)
		}
	}
	key := podNatKey(in.NamespaceId, in.Name)
	if _, dup := m.podsByNatKey[key]; dup {
		return Pod{}, fmt.Errorf("duplicate pod: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	p := Pod{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Phase:       in.Phase,
		NodeName:    in.NodeName,
		PodIp:       in.PodIp,
		Containers:  in.Containers,
		Labels:      in.Labels,
		WorkloadId:  in.WorkloadId,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.podsByID[id] = p
	m.podsByNatKey[key] = id
	return p, nil
}

func (m *memStore) GetPod(_ context.Context, id uuid.UUID) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.podsByID[id]
	if !ok {
		return Pod{}, ErrNotFound
	}
	return p, nil
}

func (m *memStore) ListPods(_ context.Context, filter PodListFilter, limit int, _ string) ([]Pod, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	needle := ""
	if filter.ImageSubstring != nil {
		needle = strings.ToLower(*filter.ImageSubstring)
	}
	out := make([]Pod, 0, len(m.podsByID))
	for _, p := range m.podsByID {
		if filter.NamespaceID != nil && p.NamespaceId != *filter.NamespaceID {
			continue
		}
		if filter.NodeName != nil {
			if p.NodeName == nil || *p.NodeName != *filter.NodeName {
				continue
			}
		}
		if needle != "" && !podContainersMatch(p.Containers, needle) {
			continue
		}
		out = append(out, p)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

// podContainersMatch is the mem-store analogue of the PG ILIKE filter —
// case-insensitive substring match over every container's image string.
func podContainersMatch(containers *ContainerList, needleLower string) bool {
	if containers == nil {
		return false
	}
	for _, c := range *containers {
		img, ok := c["image"].(string)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(img), needleLower) {
			return true
		}
	}
	return false
}

func (m *memStore) UpdatePod(_ context.Context, id uuid.UUID, in PodUpdate) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.podsByID[id]
	if !ok {
		return Pod{}, ErrNotFound
	}
	if in.Phase != nil {
		p.Phase = in.Phase
	}
	if in.NodeName != nil {
		p.NodeName = in.NodeName
	}
	if in.PodIp != nil {
		p.PodIp = in.PodIp
	}
	if in.Containers != nil {
		p.Containers = in.Containers
	}
	if in.Labels != nil {
		p.Labels = in.Labels
	}
	if in.WorkloadId != nil {
		if _, ok := m.workloadsByID[*in.WorkloadId]; !ok {
			return Pod{}, fmt.Errorf("workload %s does not exist: %w", in.WorkloadId, ErrNotFound)
		}
		p.WorkloadId = in.WorkloadId
	}
	now := time.Now().UTC()
	p.UpdatedAt = &now
	m.podsByID[id] = p
	return p, nil
}

func (m *memStore) DeletePod(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.podsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.podsByID, id)
	delete(m.podsByNatKey, podNatKey(p.NamespaceId, p.Name))
	return nil
}

func (m *memStore) UpsertPod(_ context.Context, in PodCreate) (Pod, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Pod{}, ErrNotFound
	}
	if in.WorkloadId != nil {
		if _, ok := m.workloadsByID[*in.WorkloadId]; !ok {
			return Pod{}, fmt.Errorf("workload %s does not exist: %w", in.WorkloadId, ErrNotFound)
		}
	}
	key := podNatKey(in.NamespaceId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.podsByNatKey[key]; exists {
		p := m.podsByID[existingID]
		p.Phase = in.Phase
		p.NodeName = in.NodeName
		p.PodIp = in.PodIp
		p.Containers = in.Containers
		p.Labels = in.Labels
		p.WorkloadId = in.WorkloadId
		p.UpdatedAt = &now
		m.podsByID[existingID] = p
		return p, nil
	}

	id := uuid.New()
	p := Pod{
		Id:          &id,
		NamespaceId: in.NamespaceId,
		Name:        in.Name,
		Phase:       in.Phase,
		NodeName:    in.NodeName,
		PodIp:       in.PodIp,
		Containers:  in.Containers,
		Labels:      in.Labels,
		WorkloadId:  in.WorkloadId,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.podsByID[id] = p
	m.podsByNatKey[key] = id
	return p, nil
}

func (m *memStore) DeletePodsNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	var deleted int64
	for id, p := range m.podsByID {
		if p.NamespaceId != namespaceID {
			continue
		}
		if _, ok := keep[p.Name]; ok {
			continue
		}
		delete(m.podsByID, id)
		delete(m.podsByNatKey, podNatKey(p.NamespaceId, p.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) CreateWorkload(_ context.Context, in WorkloadCreate) (Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Workload{}, ErrNotFound
	}
	key := workloadNatKey(in.NamespaceId, in.Kind, in.Name)
	if _, dup := m.workloadsByNatKey[key]; dup {
		return Workload{}, fmt.Errorf("duplicate workload: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	wl := Workload{
		Id:            &id,
		NamespaceId:   in.NamespaceId,
		Kind:          in.Kind,
		Name:          in.Name,
		Replicas:      in.Replicas,
		ReadyReplicas: in.ReadyReplicas,
		Containers:    in.Containers,
		Labels:        in.Labels,
		Spec:          in.Spec,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	m.workloadsByID[id] = wl
	m.workloadsByNatKey[key] = id
	return wl, nil
}

func (m *memStore) GetWorkload(_ context.Context, id uuid.UUID) (Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wl, ok := m.workloadsByID[id]
	if !ok {
		return Workload{}, ErrNotFound
	}
	return wl, nil
}

func (m *memStore) ListWorkloads(_ context.Context, filter WorkloadListFilter, limit int, _ string) ([]Workload, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	needle := ""
	if filter.ImageSubstring != nil {
		needle = strings.ToLower(*filter.ImageSubstring)
	}
	out := make([]Workload, 0, len(m.workloadsByID))
	for _, wl := range m.workloadsByID {
		if filter.NamespaceID != nil && wl.NamespaceId != *filter.NamespaceID {
			continue
		}
		if filter.Kind != nil && wl.Kind != *filter.Kind {
			continue
		}
		if needle != "" && !podContainersMatch(wl.Containers, needle) {
			continue
		}
		out = append(out, wl)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateWorkload(_ context.Context, id uuid.UUID, in WorkloadUpdate) (Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	wl, ok := m.workloadsByID[id]
	if !ok {
		return Workload{}, ErrNotFound
	}
	if in.Replicas != nil {
		wl.Replicas = in.Replicas
	}
	if in.ReadyReplicas != nil {
		wl.ReadyReplicas = in.ReadyReplicas
	}
	if in.Containers != nil {
		wl.Containers = in.Containers
	}
	if in.Labels != nil {
		wl.Labels = in.Labels
	}
	if in.Spec != nil {
		wl.Spec = in.Spec
	}
	now := time.Now().UTC()
	wl.UpdatedAt = &now
	m.workloadsByID[id] = wl
	return wl, nil
}

func (m *memStore) DeleteWorkload(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	wl, ok := m.workloadsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.workloadsByID, id)
	delete(m.workloadsByNatKey, workloadNatKey(wl.NamespaceId, wl.Kind, wl.Name))
	return nil
}

func (m *memStore) UpsertWorkload(_ context.Context, in WorkloadCreate) (Workload, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Workload{}, ErrNotFound
	}
	key := workloadNatKey(in.NamespaceId, in.Kind, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.workloadsByNatKey[key]; exists {
		wl := m.workloadsByID[existingID]
		wl.Replicas = in.Replicas
		wl.ReadyReplicas = in.ReadyReplicas
		wl.Containers = in.Containers
		wl.Labels = in.Labels
		wl.Spec = in.Spec
		wl.UpdatedAt = &now
		m.workloadsByID[existingID] = wl
		return wl, nil
	}

	id := uuid.New()
	wl := Workload{
		Id:            &id,
		NamespaceId:   in.NamespaceId,
		Kind:          in.Kind,
		Name:          in.Name,
		Replicas:      in.Replicas,
		ReadyReplicas: in.ReadyReplicas,
		Containers:    in.Containers,
		Labels:        in.Labels,
		Spec:          in.Spec,
		CreatedAt:     &now,
		UpdatedAt:     &now,
	}
	m.workloadsByID[id] = wl
	m.workloadsByNatKey[key] = id
	return wl, nil
}

func (m *memStore) DeleteWorkloadsNotIn(_ context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepKinds))
	for i := range keepKinds {
		keep[keepKinds[i]+"/"+keepNames[i]] = struct{}{}
	}
	var deleted int64
	for id, wl := range m.workloadsByID {
		if wl.NamespaceId != namespaceID {
			continue
		}
		if _, ok := keep[string(wl.Kind)+"/"+wl.Name]; ok {
			continue
		}
		delete(m.workloadsByID, id)
		delete(m.workloadsByNatKey, workloadNatKey(wl.NamespaceId, wl.Kind, wl.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) CreateIngress(_ context.Context, in IngressCreate) (Ingress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Ingress{}, ErrNotFound
	}
	key := ingressNatKey(in.NamespaceId, in.Name)
	if _, dup := m.ingressesByNatKey[key]; dup {
		return Ingress{}, fmt.Errorf("duplicate ingress: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	i := Ingress{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		IngressClassName: in.IngressClassName,
		Rules:            in.Rules,
		Tls:              in.Tls,
		LoadBalancer:     in.LoadBalancer,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
	m.ingressesByID[id] = i
	m.ingressesByNatKey[key] = id
	return i, nil
}

func (m *memStore) GetIngress(_ context.Context, id uuid.UUID) (Ingress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ingressesByID[id]
	if !ok {
		return Ingress{}, ErrNotFound
	}
	return i, nil
}

func (m *memStore) ListIngresses(_ context.Context, namespaceID *uuid.UUID, limit int, _ string) ([]Ingress, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Ingress, 0, len(m.ingressesByID))
	for _, i := range m.ingressesByID {
		if namespaceID != nil && i.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, i)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateIngress(_ context.Context, id uuid.UUID, in IngressUpdate) (Ingress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ingressesByID[id]
	if !ok {
		return Ingress{}, ErrNotFound
	}
	if in.IngressClassName != nil {
		i.IngressClassName = in.IngressClassName
	}
	if in.Rules != nil {
		i.Rules = in.Rules
	}
	if in.Tls != nil {
		i.Tls = in.Tls
	}
	if in.LoadBalancer != nil {
		i.LoadBalancer = in.LoadBalancer
	}
	if in.Labels != nil {
		i.Labels = in.Labels
	}
	now := time.Now().UTC()
	i.UpdatedAt = &now
	m.ingressesByID[id] = i
	return i, nil
}

func (m *memStore) DeleteIngress(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.ingressesByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.ingressesByID, id)
	delete(m.ingressesByNatKey, ingressNatKey(i.NamespaceId, i.Name))
	return nil
}

func (m *memStore) UpsertIngress(_ context.Context, in IngressCreate) (Ingress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Ingress{}, ErrNotFound
	}
	key := ingressNatKey(in.NamespaceId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	if existingID, exists := m.ingressesByNatKey[key]; exists {
		i := m.ingressesByID[existingID]
		i.IngressClassName = in.IngressClassName
		i.Rules = in.Rules
		i.Tls = in.Tls
		i.LoadBalancer = in.LoadBalancer
		i.Labels = in.Labels
		i.UpdatedAt = &now
		m.ingressesByID[existingID] = i
		return i, nil
	}
	id := uuid.New()
	i := Ingress{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		IngressClassName: in.IngressClassName,
		Rules:            in.Rules,
		Tls:              in.Tls,
		LoadBalancer:     in.LoadBalancer,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
	m.ingressesByID[id] = i
	m.ingressesByNatKey[key] = id
	return i, nil
}

func (m *memStore) DeleteIngressesNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	var deleted int64
	for id, i := range m.ingressesByID {
		if i.NamespaceId != namespaceID {
			continue
		}
		if _, ok := keep[i.Name]; ok {
			continue
		}
		delete(m.ingressesByID, id)
		delete(m.ingressesByNatKey, ingressNatKey(i.NamespaceId, i.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) CreateService(_ context.Context, in ServiceCreate) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Service{}, ErrNotFound
	}
	key := serviceNatKey(in.NamespaceId, in.Name)
	if _, dup := m.servicesByNatKey[key]; dup {
		return Service{}, fmt.Errorf("duplicate service: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	s := Service{
		Id:           &id,
		NamespaceId:  in.NamespaceId,
		Name:         in.Name,
		Type:         in.Type,
		ClusterIp:    in.ClusterIp,
		Selector:     in.Selector,
		Ports:        in.Ports,
		LoadBalancer: in.LoadBalancer,
		Labels:       in.Labels,
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}
	m.servicesByID[id] = s
	m.servicesByNatKey[key] = id
	return s, nil
}

func (m *memStore) GetService(_ context.Context, id uuid.UUID) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servicesByID[id]
	if !ok {
		return Service{}, ErrNotFound
	}
	return s, nil
}

func (m *memStore) ListServices(_ context.Context, namespaceID *uuid.UUID, limit int, _ string) ([]Service, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]Service, 0, len(m.servicesByID))
	for _, s := range m.servicesByID {
		if namespaceID != nil && s.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, s)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdateService(_ context.Context, id uuid.UUID, in ServiceUpdate) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servicesByID[id]
	if !ok {
		return Service{}, ErrNotFound
	}
	if in.Type != nil {
		s.Type = in.Type
	}
	if in.ClusterIp != nil {
		s.ClusterIp = in.ClusterIp
	}
	if in.Selector != nil {
		s.Selector = in.Selector
	}
	if in.Ports != nil {
		s.Ports = in.Ports
	}
	if in.LoadBalancer != nil {
		s.LoadBalancer = in.LoadBalancer
	}
	if in.Labels != nil {
		s.Labels = in.Labels
	}
	now := time.Now().UTC()
	s.UpdatedAt = &now
	m.servicesByID[id] = s
	return s, nil
}

func (m *memStore) DeleteService(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.servicesByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.servicesByID, id)
	delete(m.servicesByNatKey, serviceNatKey(s.NamespaceId, s.Name))
	return nil
}

func (m *memStore) UpsertService(_ context.Context, in ServiceCreate) (Service, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return Service{}, ErrNotFound
	}
	key := serviceNatKey(in.NamespaceId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	if existingID, exists := m.servicesByNatKey[key]; exists {
		s := m.servicesByID[existingID]
		s.Type = in.Type
		s.ClusterIp = in.ClusterIp
		s.Selector = in.Selector
		s.Ports = in.Ports
		s.LoadBalancer = in.LoadBalancer
		s.Labels = in.Labels
		s.UpdatedAt = &now
		m.servicesByID[existingID] = s
		return s, nil
	}
	id := uuid.New()
	s := Service{
		Id:           &id,
		NamespaceId:  in.NamespaceId,
		Name:         in.Name,
		Type:         in.Type,
		ClusterIp:    in.ClusterIp,
		LoadBalancer: in.LoadBalancer,
		Selector:     in.Selector,
		Ports:        in.Ports,
		Labels:       in.Labels,
		CreatedAt:    &now,
		UpdatedAt:    &now,
	}
	m.servicesByID[id] = s
	m.servicesByNatKey[key] = id
	return s, nil
}

func (m *memStore) DeleteServicesNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	var deleted int64
	for id, s := range m.servicesByID {
		if s.NamespaceId != namespaceID {
			continue
		}
		if _, ok := keep[s.Name]; ok {
			continue
		}
		delete(m.servicesByID, id)
		delete(m.servicesByNatKey, serviceNatKey(s.NamespaceId, s.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) UpsertNamespace(_ context.Context, in NamespaceCreate) (Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Namespace{}, ErrNotFound
	}
	key := nsNatKey(in.ClusterId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.nsByNatKey[key]; exists {
		n := m.nsByID[existingID]
		n.DisplayName = in.DisplayName
		n.Phase = in.Phase
		n.Labels = in.Labels
		n.UpdatedAt = &now
		m.nsByID[existingID] = n
		return n, nil
	}

	id := uuid.New()
	n := Namespace{
		Id:          &id,
		ClusterId:   in.ClusterId,
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Phase:       in.Phase,
		Labels:      in.Labels,
		Owner:       in.Owner,
		Criticality: in.Criticality,
		Notes:       in.Notes,
		RunbookUrl:  in.RunbookUrl,
		Annotations: in.Annotations,
		CreatedAt:   &now,
		UpdatedAt:   &now,
	}
	m.nsByID[id] = n
	m.nsByNatKey[key] = id
	return n, nil
}

func (m *memStore) DeleteNodesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, name := range keepNames {
		keep[name] = struct{}{}
	}
	var deleted int64
	for id, n := range m.nodesByID {
		if n.ClusterId != clusterID {
			continue
		}
		if _, ok := keep[n.Name]; ok {
			continue
		}
		delete(m.nodesByID, id)
		delete(m.nodesByNatKey, nodeNatKey(n.ClusterId, n.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) DeleteNamespacesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, name := range keepNames {
		keep[name] = struct{}{}
	}
	var deleted int64
	for id, n := range m.nsByID {
		if n.ClusterId != clusterID {
			continue
		}
		if _, ok := keep[n.Name]; ok {
			continue
		}
		delete(m.nsByID, id)
		delete(m.nsByNatKey, nsNatKey(n.ClusterId, n.Name))
		deleted++
	}
	return deleted, nil
}

func (m *memStore) UpsertNode(_ context.Context, in NodeCreate) (Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return Node{}, ErrNotFound
	}
	key := nodeNatKey(in.ClusterId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.nodesByNatKey[key]; exists {
		n := m.nodesByID[existingID]
		// On conflict: copy only collector-owned fields. Mirrors the
		// DO UPDATE SET clause on the PG side so operator-set curated
		// columns (owner / criticality / notes / runbook_url /
		// annotations / hardware_model) survive the collector's tick.
		copyNodeCollectorFieldsFromCreate(&n, in)
		n.UpdatedAt = &now
		m.nodesByID[existingID] = n
		return n, nil
	}

	id := uuid.New()
	n := Node{
		Id:        &id,
		ClusterId: in.ClusterId,
		Name:      in.Name,
		CreatedAt: &now,
		UpdatedAt: &now,
	}
	copyNodeMutableFromCreate(&n, in)
	m.nodesByID[id] = n
	m.nodesByNatKey[key] = id
	return n, nil
}

func pvNatKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func pvcNatKey(namespaceID uuid.UUID, name string) string {
	return namespaceID.String() + "/" + name
}

func (m *memStore) CreatePersistentVolume(_ context.Context, in PersistentVolumeCreate) (PersistentVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return PersistentVolume{}, ErrNotFound
	}
	key := pvNatKey(in.ClusterId, in.Name)
	if _, dup := m.pvsByNatKey[key]; dup {
		return PersistentVolume{}, fmt.Errorf("duplicate pv: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	pv := PersistentVolume{
		Id:                &id,
		ClusterId:         in.ClusterId,
		Name:              in.Name,
		Capacity:          in.Capacity,
		AccessModes:       in.AccessModes,
		ReclaimPolicy:     in.ReclaimPolicy,
		Phase:             in.Phase,
		StorageClassName:  in.StorageClassName,
		CsiDriver:         in.CsiDriver,
		VolumeHandle:      in.VolumeHandle,
		ClaimRefNamespace: in.ClaimRefNamespace,
		ClaimRefName:      in.ClaimRefName,
		Labels:            in.Labels,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}
	m.pvsByID[id] = pv
	m.pvsByNatKey[key] = id
	return pv, nil
}

func (m *memStore) GetPersistentVolume(_ context.Context, id uuid.UUID) (PersistentVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pv, ok := m.pvsByID[id]
	if !ok {
		return PersistentVolume{}, ErrNotFound
	}
	return pv, nil
}

func (m *memStore) ListPersistentVolumes(_ context.Context, clusterID *uuid.UUID, limit int, _ string) ([]PersistentVolume, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]PersistentVolume, 0, len(m.pvsByID))
	for _, pv := range m.pvsByID {
		if clusterID != nil && pv.ClusterId != *clusterID {
			continue
		}
		out = append(out, pv)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdatePersistentVolume(_ context.Context, id uuid.UUID, in PersistentVolumeUpdate) (PersistentVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pv, ok := m.pvsByID[id]
	if !ok {
		return PersistentVolume{}, ErrNotFound
	}
	if in.Capacity != nil {
		pv.Capacity = in.Capacity
	}
	if in.AccessModes != nil {
		pv.AccessModes = in.AccessModes
	}
	if in.ReclaimPolicy != nil {
		pv.ReclaimPolicy = in.ReclaimPolicy
	}
	if in.Phase != nil {
		pv.Phase = in.Phase
	}
	if in.StorageClassName != nil {
		pv.StorageClassName = in.StorageClassName
	}
	if in.CsiDriver != nil {
		pv.CsiDriver = in.CsiDriver
	}
	if in.VolumeHandle != nil {
		pv.VolumeHandle = in.VolumeHandle
	}
	if in.ClaimRefNamespace != nil {
		pv.ClaimRefNamespace = in.ClaimRefNamespace
	}
	if in.ClaimRefName != nil {
		pv.ClaimRefName = in.ClaimRefName
	}
	if in.Labels != nil {
		pv.Labels = in.Labels
	}
	now := time.Now().UTC()
	pv.UpdatedAt = &now
	m.pvsByID[id] = pv
	return pv, nil
}

func (m *memStore) DeletePersistentVolume(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pv, ok := m.pvsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.pvsByID, id)
	delete(m.pvsByNatKey, pvNatKey(pv.ClusterId, pv.Name))
	// Mirror ON DELETE SET NULL on the FK from PVCs.
	for pvcID, pvc := range m.pvcsByID {
		if pvc.BoundVolumeId != nil && *pvc.BoundVolumeId == id {
			pvc.BoundVolumeId = nil
			m.pvcsByID[pvcID] = pvc
		}
	}
	return nil
}

func (m *memStore) UpsertPersistentVolume(_ context.Context, in PersistentVolumeCreate) (PersistentVolume, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[in.ClusterId]; !ok {
		return PersistentVolume{}, ErrNotFound
	}
	key := pvNatKey(in.ClusterId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.pvsByNatKey[key]; exists {
		pv := m.pvsByID[existingID]
		pv.Capacity = in.Capacity
		pv.AccessModes = in.AccessModes
		pv.ReclaimPolicy = in.ReclaimPolicy
		pv.Phase = in.Phase
		pv.StorageClassName = in.StorageClassName
		pv.CsiDriver = in.CsiDriver
		pv.VolumeHandle = in.VolumeHandle
		pv.ClaimRefNamespace = in.ClaimRefNamespace
		pv.ClaimRefName = in.ClaimRefName
		pv.Labels = in.Labels
		pv.UpdatedAt = &now
		m.pvsByID[existingID] = pv
		return pv, nil
	}

	id := uuid.New()
	pv := PersistentVolume{
		Id:                &id,
		ClusterId:         in.ClusterId,
		Name:              in.Name,
		Capacity:          in.Capacity,
		AccessModes:       in.AccessModes,
		ReclaimPolicy:     in.ReclaimPolicy,
		Phase:             in.Phase,
		StorageClassName:  in.StorageClassName,
		CsiDriver:         in.CsiDriver,
		VolumeHandle:      in.VolumeHandle,
		ClaimRefNamespace: in.ClaimRefNamespace,
		ClaimRefName:      in.ClaimRefName,
		Labels:            in.Labels,
		CreatedAt:         &now,
		UpdatedAt:         &now,
	}
	m.pvsByID[id] = pv
	m.pvsByNatKey[key] = id
	return pv, nil
}

func (m *memStore) DeletePersistentVolumesNotIn(_ context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	var deleted int64
	for id, pv := range m.pvsByID {
		if pv.ClusterId != clusterID {
			continue
		}
		if _, ok := keep[pv.Name]; ok {
			continue
		}
		delete(m.pvsByID, id)
		delete(m.pvsByNatKey, pvNatKey(pv.ClusterId, pv.Name))
		for pvcID, pvc := range m.pvcsByID {
			if pvc.BoundVolumeId != nil && *pvc.BoundVolumeId == id {
				pvc.BoundVolumeId = nil
				m.pvcsByID[pvcID] = pvc
			}
		}
		deleted++
	}
	return deleted, nil
}

func (m *memStore) CreatePersistentVolumeClaim(_ context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return PersistentVolumeClaim{}, ErrNotFound
	}
	if in.BoundVolumeId != nil {
		if _, ok := m.pvsByID[*in.BoundVolumeId]; !ok {
			return PersistentVolumeClaim{}, fmt.Errorf("persistent volume %s does not exist: %w", in.BoundVolumeId, ErrNotFound)
		}
	}
	key := pvcNatKey(in.NamespaceId, in.Name)
	if _, dup := m.pvcsByNatKey[key]; dup {
		return PersistentVolumeClaim{}, fmt.Errorf("duplicate pvc: %w", ErrConflict)
	}
	id := uuid.New()
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++
	pvc := PersistentVolumeClaim{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		Phase:            in.Phase,
		StorageClassName: in.StorageClassName,
		VolumeName:       in.VolumeName,
		BoundVolumeId:    in.BoundVolumeId,
		AccessModes:      in.AccessModes,
		RequestedStorage: in.RequestedStorage,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
	m.pvcsByID[id] = pvc
	m.pvcsByNatKey[key] = id
	return pvc, nil
}

func (m *memStore) GetPersistentVolumeClaim(_ context.Context, id uuid.UUID) (PersistentVolumeClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pvc, ok := m.pvcsByID[id]
	if !ok {
		return PersistentVolumeClaim{}, ErrNotFound
	}
	return pvc, nil
}

func (m *memStore) ListPersistentVolumeClaims(_ context.Context, namespaceID *uuid.UUID, limit int, _ string) ([]PersistentVolumeClaim, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 {
		limit = 50
	}
	out := make([]PersistentVolumeClaim, 0, len(m.pvcsByID))
	for _, pvc := range m.pvcsByID {
		if namespaceID != nil && pvc.NamespaceId != *namespaceID {
			continue
		}
		out = append(out, pvc)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, "", nil
}

func (m *memStore) UpdatePersistentVolumeClaim(_ context.Context, id uuid.UUID, in PersistentVolumeClaimUpdate) (PersistentVolumeClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pvc, ok := m.pvcsByID[id]
	if !ok {
		return PersistentVolumeClaim{}, ErrNotFound
	}
	if in.Phase != nil {
		pvc.Phase = in.Phase
	}
	if in.StorageClassName != nil {
		pvc.StorageClassName = in.StorageClassName
	}
	if in.VolumeName != nil {
		pvc.VolumeName = in.VolumeName
	}
	if in.BoundVolumeId != nil {
		if _, ok := m.pvsByID[*in.BoundVolumeId]; !ok {
			return PersistentVolumeClaim{}, fmt.Errorf("persistent volume %s does not exist: %w", in.BoundVolumeId, ErrNotFound)
		}
		pvc.BoundVolumeId = in.BoundVolumeId
	}
	if in.AccessModes != nil {
		pvc.AccessModes = in.AccessModes
	}
	if in.RequestedStorage != nil {
		pvc.RequestedStorage = in.RequestedStorage
	}
	if in.Labels != nil {
		pvc.Labels = in.Labels
	}
	now := time.Now().UTC()
	pvc.UpdatedAt = &now
	m.pvcsByID[id] = pvc
	return pvc, nil
}

func (m *memStore) DeletePersistentVolumeClaim(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pvc, ok := m.pvcsByID[id]
	if !ok {
		return ErrNotFound
	}
	delete(m.pvcsByID, id)
	delete(m.pvcsByNatKey, pvcNatKey(pvc.NamespaceId, pvc.Name))
	return nil
}

func (m *memStore) UpsertPersistentVolumeClaim(_ context.Context, in PersistentVolumeClaimCreate) (PersistentVolumeClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nsByID[in.NamespaceId]; !ok {
		return PersistentVolumeClaim{}, ErrNotFound
	}
	if in.BoundVolumeId != nil {
		if _, ok := m.pvsByID[*in.BoundVolumeId]; !ok {
			return PersistentVolumeClaim{}, fmt.Errorf("persistent volume %s does not exist: %w", in.BoundVolumeId, ErrNotFound)
		}
	}
	key := pvcNatKey(in.NamespaceId, in.Name)
	now := time.Now().UTC().Add(time.Duration(m.createdN) * time.Nanosecond)
	m.createdN++

	if existingID, exists := m.pvcsByNatKey[key]; exists {
		pvc := m.pvcsByID[existingID]
		pvc.Phase = in.Phase
		pvc.StorageClassName = in.StorageClassName
		pvc.VolumeName = in.VolumeName
		pvc.BoundVolumeId = in.BoundVolumeId
		pvc.AccessModes = in.AccessModes
		pvc.RequestedStorage = in.RequestedStorage
		pvc.Labels = in.Labels
		pvc.UpdatedAt = &now
		m.pvcsByID[existingID] = pvc
		return pvc, nil
	}

	id := uuid.New()
	pvc := PersistentVolumeClaim{
		Id:               &id,
		NamespaceId:      in.NamespaceId,
		Name:             in.Name,
		Phase:            in.Phase,
		StorageClassName: in.StorageClassName,
		VolumeName:       in.VolumeName,
		BoundVolumeId:    in.BoundVolumeId,
		AccessModes:      in.AccessModes,
		RequestedStorage: in.RequestedStorage,
		Labels:           in.Labels,
		CreatedAt:        &now,
		UpdatedAt:        &now,
	}
	m.pvcsByID[id] = pvc
	m.pvcsByNatKey[key] = id
	return pvc, nil
}

func (m *memStore) DeletePersistentVolumeClaimsNotIn(_ context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := make(map[string]struct{}, len(keepNames))
	for _, n := range keepNames {
		keep[n] = struct{}{}
	}
	var deleted int64
	for id, pvc := range m.pvcsByID {
		if pvc.NamespaceId != namespaceID {
			continue
		}
		if _, ok := keep[pvc.Name]; ok {
			continue
		}
		delete(m.pvcsByID, id)
		delete(m.pvcsByNatKey, pvcNatKey(pvc.NamespaceId, pvc.Name))
		deleted++
	}
	return deleted, nil
}

func newTestHandler(t *testing.T, store Store) http.Handler {
	t.Helper()
	return Handler(NewServer("test", store, auth.SecureNever, nil))
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()

	t.Run("healthz ok", func(t *testing.T) {
		t.Parallel()
		h := newTestHandler(t, newMemStore())
		rr := do(h, http.MethodGet, "/healthz", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d", rr.Code)
		}
		var got Health
		if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Status != Ok {
			t.Errorf("status = %q", got.Status)
		}
	})

	t.Run("readyz ok when store pings", func(t *testing.T) {
		t.Parallel()
		h := newTestHandler(t, newMemStore())
		rr := do(h, http.MethodGet, "/readyz", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
		}
	})

	t.Run("readyz 503 when store ping fails", func(t *testing.T) {
		t.Parallel()
		m := newMemStore()
		m.pingErr = errors.New("db down")
		h := newTestHandler(t, m)
		rr := do(h, http.MethodGet, "/readyz", "")
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("Content-Type=%q", ct)
		}
	})
}

func TestClusterCRUD(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())

	// Create
	create := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1","environment":"prod"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/clusters/") {
		t.Errorf("Location=%q", loc)
	}
	var created Cluster
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Id == nil {
		t.Fatal("created.Id is nil")
	}

	// Duplicate create → 409
	dup := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1"}`)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate create status=%d", dup.Code)
	}

	// Get
	getURL := "/v1/clusters/" + created.Id.String()
	get := do(h, http.MethodGet, getURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%q", get.Code, get.Body.String())
	}

	// Get missing → 404
	miss := do(h, http.MethodGet, "/v1/clusters/"+uuid.Nil.String(), "")
	if miss.Code != http.StatusNotFound {
		t.Errorf("get missing status=%d", miss.Code)
	}

	// Patch
	patch := do(h, http.MethodPatch, getURL, `{"provider":"gke"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Cluster
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Provider == nil || *patched.Provider != "gke" {
		t.Errorf("provider=%v", patched.Provider)
	}

	// List
	list := do(h, http.MethodGet, "/v1/clusters", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page ClusterList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	// Delete
	del := do(h, http.MethodDelete, getURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	// Delete again → 404
	del2 := do(h, http.MethodDelete, getURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestNodeCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Seed a cluster so node creates have a valid FK.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-eu-west-1"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	clusterIDStr := cluster.Id.String()

	// Create node
	createBody := fmt.Sprintf(`{"cluster_id":%q,"name":"node-1","kubelet_version":"v1.29.5"}`, clusterIDStr)
	create := do(h, http.MethodPost, "/v1/nodes", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create node: status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/nodes/") {
		t.Errorf("Location=%q", loc)
	}
	var node Node
	if err := json.Unmarshal(create.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if node.Id == nil {
		t.Fatal("node.Id is nil")
	}
	if node.ClusterId != *cluster.Id {
		t.Errorf("node.ClusterId=%v, want %v", node.ClusterId, *cluster.Id)
	}

	// Duplicate (cluster_id, name) → 409
	dup := do(h, http.MethodPost, "/v1/nodes", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate node status=%d", dup.Code)
	}

	// Create with unknown cluster_id → 404
	missing := do(h, http.MethodPost, "/v1/nodes", fmt.Sprintf(`{"cluster_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing cluster create status=%d", missing.Code)
	}

	// Get
	nodeURL := "/v1/nodes/" + node.Id.String()
	get := do(h, http.MethodGet, nodeURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get node status=%d body=%q", get.Code, get.Body.String())
	}

	// Patch
	patch := do(h, http.MethodPatch, nodeURL, `{"architecture":"arm64"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Node
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Architecture == nil || *patched.Architecture != "arm64" {
		t.Errorf("architecture=%v", patched.Architecture)
	}

	// List all
	list := do(h, http.MethodGet, "/v1/nodes", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page NodeList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	// List filtered by cluster_id
	filtered := do(h, http.MethodGet, "/v1/nodes?cluster_id="+clusterIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", filtered.Code)
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d", len(page.Items))
	}

	// List filtered by a different cluster id → empty
	empty := do(h, http.MethodGet, "/v1/nodes?cluster_id="+uuid.New().String(), "")
	if empty.Code != http.StatusOK {
		t.Fatalf("empty-filter list status=%d", empty.Code)
	}
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("empty-filter list len=%d", len(page.Items))
	}

	// Delete
	del := do(h, http.MethodDelete, nodeURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	// Delete again → 404
	del2 := do(h, http.MethodDelete, nodeURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestNamespaceCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-ns"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	clusterIDStr := cluster.Id.String()

	createBody := fmt.Sprintf(`{"cluster_id":%q,"name":"kube-system","phase":"Active"}`, clusterIDStr)
	create := do(h, http.MethodPost, "/v1/namespaces", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create ns: status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/namespaces/") {
		t.Errorf("Location=%q", loc)
	}
	var ns Namespace
	if err := json.Unmarshal(create.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode ns: %v", err)
	}
	if ns.Id == nil {
		t.Fatal("ns.Id nil")
	}

	dup := do(h, http.MethodPost, "/v1/namespaces", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate namespace status=%d", dup.Code)
	}

	missing := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing cluster create status=%d", missing.Code)
	}

	nsURL := "/v1/namespaces/" + ns.Id.String()

	get := do(h, http.MethodGet, nsURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get ns status=%d body=%q", get.Code, get.Body.String())
	}

	patch := do(h, http.MethodPatch, nsURL, `{"phase":"Terminating"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Namespace
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Phase == nil || *patched.Phase != "Terminating" {
		t.Errorf("phase=%v", patched.Phase)
	}

	list := do(h, http.MethodGet, "/v1/namespaces", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page NamespaceList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}

	filtered := do(h, http.MethodGet, "/v1/namespaces?cluster_id="+clusterIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", filtered.Code)
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode filtered list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d", len(page.Items))
	}

	del := do(h, http.MethodDelete, nsURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}

	del2 := do(h, http.MethodDelete, nsURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestPodCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Seed cluster and namespace so the pod has a valid FK chain.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-pods"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}

	nsBody := fmt.Sprintf(`{"cluster_id":%q,"name":"kube-system"}`, cluster.Id.String())
	nsResp := do(h, http.MethodPost, "/v1/namespaces", nsBody)
	if nsResp.Code != http.StatusCreated {
		t.Fatalf("seed namespace: status=%d body=%q", nsResp.Code, nsResp.Body.String())
	}
	var ns Namespace
	if err := json.Unmarshal(nsResp.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}
	nsIDStr := ns.Id.String()

	createBody := fmt.Sprintf(`{"namespace_id":%q,"name":"coredns-abc","phase":"Running","node_name":"node-a"}`, nsIDStr)
	create := do(h, http.MethodPost, "/v1/pods", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create pod: status=%d body=%q", create.Code, create.Body.String())
	}
	if loc := create.Header().Get("Location"); !strings.HasPrefix(loc, "/v1/pods/") {
		t.Errorf("Location=%q", loc)
	}
	var pod Pod
	if err := json.Unmarshal(create.Body.Bytes(), &pod); err != nil {
		t.Fatalf("decode pod: %v", err)
	}
	if pod.Id == nil {
		t.Fatal("pod.Id nil")
	}
	if pod.Layer == nil || *pod.Layer != LayerPod {
		t.Errorf("pod layer=%v, want %q", pod.Layer, LayerPod)
	}
	if pod.NamespaceId != *ns.Id {
		t.Errorf("pod.NamespaceId=%v, want %v", pod.NamespaceId, *ns.Id)
	}

	dup := do(h, http.MethodPost, "/v1/pods", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate pod status=%d", dup.Code)
	}

	missing := do(h, http.MethodPost, "/v1/pods", fmt.Sprintf(`{"namespace_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("missing namespace create status=%d", missing.Code)
	}

	podURL := "/v1/pods/" + pod.Id.String()

	get := do(h, http.MethodGet, podURL, "")
	if get.Code != http.StatusOK {
		t.Fatalf("get pod status=%d body=%q", get.Code, get.Body.String())
	}

	patch := do(h, http.MethodPatch, podURL, `{"phase":"Succeeded"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%q", patch.Code, patch.Body.String())
	}
	var patched Pod
	if err := json.Unmarshal(patch.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Phase == nil || *patched.Phase != "Succeeded" {
		t.Errorf("phase=%v", patched.Phase)
	}

	list := do(h, http.MethodGet, "/v1/pods", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d", list.Code)
	}
	var page PodList
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("list len=%d", len(page.Items))
	}
	if page.Items[0].Layer == nil || *page.Items[0].Layer != LayerPod {
		t.Errorf("list item layer=%v", page.Items[0].Layer)
	}

	filtered := do(h, http.MethodGet, "/v1/pods?namespace_id="+nsIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d", filtered.Code)
	}
	if err := json.Unmarshal(filtered.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d", len(page.Items))
	}

	del := do(h, http.MethodDelete, podURL, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete status=%d", del.Code)
	}
	del2 := do(h, http.MethodDelete, podURL, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete status=%d", del2.Code)
	}
}

func TestWorkloadCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Seed cluster + namespace.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-wl"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("seed cluster: %d %q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	_ = json.Unmarshal(clusterResp.Body.Bytes(), &cluster)

	nsResp := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"apps"}`, cluster.Id.String()))
	if nsResp.Code != http.StatusCreated {
		t.Fatalf("seed ns: %d", nsResp.Code)
	}
	var ns Namespace
	_ = json.Unmarshal(nsResp.Body.Bytes(), &ns)
	nsIDStr := ns.Id.String()

	createBody := fmt.Sprintf(`{"namespace_id":%q,"kind":"Deployment","name":"web","replicas":3}`, nsIDStr)
	create := do(h, http.MethodPost, "/v1/workloads", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %q", create.Code, create.Body.String())
	}
	var wl Workload
	_ = json.Unmarshal(create.Body.Bytes(), &wl)
	if wl.Id == nil {
		t.Fatal("workload id nil")
	}
	if wl.Kind != Deployment {
		t.Errorf("kind=%q, want Deployment", wl.Kind)
	}
	if wl.Layer == nil || *wl.Layer != LayerWorkload {
		t.Errorf("layer=%v, want %q", wl.Layer, LayerWorkload)
	}

	// Deployment 'web' and StatefulSet 'web' coexist in same namespace.
	sfsBody := fmt.Sprintf(`{"namespace_id":%q,"kind":"StatefulSet","name":"web"}`, nsIDStr)
	sfs := do(h, http.MethodPost, "/v1/workloads", sfsBody)
	if sfs.Code != http.StatusCreated {
		t.Errorf("sts with same name in same ns should be allowed: %d %q", sfs.Code, sfs.Body.String())
	}

	// Duplicate (ns, kind, name) is 409.
	dup := do(h, http.MethodPost, "/v1/workloads", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate: %d", dup.Code)
	}

	// Invalid kind rejected.
	bogus := do(h, http.MethodPost, "/v1/workloads", fmt.Sprintf(`{"namespace_id":%q,"kind":"Pony","name":"x"}`, nsIDStr))
	if bogus.Code != http.StatusBadRequest {
		t.Errorf("bogus kind: %d %q", bogus.Code, bogus.Body.String())
	}

	// Unknown namespace → 404.
	missing := do(h, http.MethodPost, "/v1/workloads", fmt.Sprintf(`{"namespace_id":%q,"kind":"Deployment","name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown ns: %d", missing.Code)
	}

	url := "/v1/workloads/" + wl.Id.String()
	patch := do(h, http.MethodPatch, url, `{"replicas":5}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %q", patch.Code, patch.Body.String())
	}
	var patched Workload
	_ = json.Unmarshal(patch.Body.Bytes(), &patched)
	if patched.Replicas == nil || *patched.Replicas != 5 {
		t.Errorf("replicas=%v", patched.Replicas)
	}

	// List filtered by kind.
	byKind := do(h, http.MethodGet, "/v1/workloads?kind=StatefulSet", "")
	if byKind.Code != http.StatusOK {
		t.Fatalf("list by kind: %d", byKind.Code)
	}
	var page WorkloadList
	_ = json.Unmarshal(byKind.Body.Bytes(), &page)
	if len(page.Items) != 1 || page.Items[0].Kind != StatefulSet {
		t.Errorf("kind filter returned %d items with wrong kind", len(page.Items))
	}

	// Invalid kind filter → 400.
	badFilter := do(h, http.MethodGet, "/v1/workloads?kind=Pony", "")
	if badFilter.Code != http.StatusBadRequest {
		t.Errorf("invalid kind filter: %d %q", badFilter.Code, badFilter.Body.String())
	}

	// List filtered by namespace (returns both).
	byNS := do(h, http.MethodGet, "/v1/workloads?namespace_id="+nsIDStr, "")
	_ = json.Unmarshal(byNS.Body.Bytes(), &page)
	if len(page.Items) != 2 {
		t.Errorf("ns filter returned %d items, want 2", len(page.Items))
	}

	// Delete original deployment.
	del := do(h, http.MethodDelete, url, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete: %d", del.Code)
	}
	del2 := do(h, http.MethodDelete, url, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete: %d", del2.Code)
	}
}

func TestIngressCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-ing"}`)
	var cluster Cluster
	_ = json.Unmarshal(clusterResp.Body.Bytes(), &cluster)

	nsResp := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"apps"}`, cluster.Id.String()))
	var ns Namespace
	_ = json.Unmarshal(nsResp.Body.Bytes(), &ns)
	nsIDStr := ns.Id.String()

	createBody := fmt.Sprintf(`{"namespace_id":%q,"name":"web","ingress_class_name":"nginx"}`, nsIDStr)
	create := do(h, http.MethodPost, "/v1/ingresses", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %q", create.Code, create.Body.String())
	}
	var ing Ingress
	_ = json.Unmarshal(create.Body.Bytes(), &ing)
	if ing.Id == nil {
		t.Fatal("ingress id nil")
	}
	if ing.Layer == nil || *ing.Layer != LayerIngress {
		t.Errorf("layer=%v, want %q", ing.Layer, LayerIngress)
	}
	if ing.IngressClassName == nil || *ing.IngressClassName != "nginx" {
		t.Errorf("ingress_class_name=%v", ing.IngressClassName)
	}

	dup := do(h, http.MethodPost, "/v1/ingresses", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate: %d", dup.Code)
	}

	missing := do(h, http.MethodPost, "/v1/ingresses", fmt.Sprintf(`{"namespace_id":%q,"name":"x"}`, uuid.New().String()))
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown namespace: %d", missing.Code)
	}

	url := "/v1/ingresses/" + ing.Id.String()
	patch := do(h, http.MethodPatch, url, `{"ingress_class_name":"traefik"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %q", patch.Code, patch.Body.String())
	}
	var patched Ingress
	_ = json.Unmarshal(patch.Body.Bytes(), &patched)
	if patched.IngressClassName == nil || *patched.IngressClassName != "traefik" {
		t.Errorf("class after patch=%v", patched.IngressClassName)
	}

	filtered := do(h, http.MethodGet, "/v1/ingresses?namespace_id="+nsIDStr, "")
	if filtered.Code != http.StatusOK {
		t.Fatalf("filtered list: %d", filtered.Code)
	}
	var page IngressList
	_ = json.Unmarshal(filtered.Body.Bytes(), &page)
	if len(page.Items) != 1 {
		t.Errorf("filtered list len=%d, want 1", len(page.Items))
	}

	del := do(h, http.MethodDelete, url, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete: %d", del.Code)
	}
	del2 := do(h, http.MethodDelete, url, "")
	if del2.Code != http.StatusNotFound {
		t.Errorf("second delete: %d", del2.Code)
	}
}

func TestServiceCRUD(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"prod-svc"}`)
	var cluster Cluster
	_ = json.Unmarshal(clusterResp.Body.Bytes(), &cluster)

	nsResp := do(h, http.MethodPost, "/v1/namespaces", fmt.Sprintf(`{"cluster_id":%q,"name":"apps"}`, cluster.Id.String()))
	var ns Namespace
	_ = json.Unmarshal(nsResp.Body.Bytes(), &ns)
	nsIDStr := ns.Id.String()

	createBody := fmt.Sprintf(`{"namespace_id":%q,"name":"web","type":"ClusterIP","cluster_ip":"10.0.0.1"}`, nsIDStr)
	create := do(h, http.MethodPost, "/v1/services", createBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %q", create.Code, create.Body.String())
	}
	var svc Service
	_ = json.Unmarshal(create.Body.Bytes(), &svc)
	if svc.Layer == nil || *svc.Layer != LayerService {
		t.Errorf("layer=%v, want %q", svc.Layer, LayerService)
	}
	if svc.Type == nil || *svc.Type != ClusterIP {
		t.Errorf("type=%v, want ClusterIP", svc.Type)
	}

	bogus := do(h, http.MethodPost, "/v1/services", fmt.Sprintf(`{"namespace_id":%q,"name":"bad","type":"Quantum"}`, nsIDStr))
	if bogus.Code != http.StatusBadRequest {
		t.Errorf("bogus type: %d %q", bogus.Code, bogus.Body.String())
	}

	dup := do(h, http.MethodPost, "/v1/services", createBody)
	if dup.Code != http.StatusConflict {
		t.Errorf("duplicate: %d", dup.Code)
	}

	url := "/v1/services/" + svc.Id.String()
	patch := do(h, http.MethodPatch, url, `{"type":"LoadBalancer"}`)
	if patch.Code != http.StatusOK {
		t.Fatalf("patch: %d %q", patch.Code, patch.Body.String())
	}
	var patched Service
	_ = json.Unmarshal(patch.Body.Bytes(), &patched)
	if patched.Type == nil || *patched.Type != LoadBalancer {
		t.Errorf("type after patch=%v", patched.Type)
	}

	del := do(h, http.MethodDelete, url, "")
	if del.Code != http.StatusNoContent {
		t.Errorf("delete: %d", del.Code)
	}
}

func TestResponsesCarryAnssiLayer(t *testing.T) {
	t.Parallel()
	store := newMemStore()
	h := newTestHandler(t, store)

	// Cluster.
	clusterResp := do(h, http.MethodPost, "/v1/clusters", `{"name":"layer-check"}`)
	if clusterResp.Code != http.StatusCreated {
		t.Fatalf("create cluster: status=%d body=%q", clusterResp.Code, clusterResp.Body.String())
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode cluster: %v", err)
	}
	if cluster.Layer == nil || *cluster.Layer != LayerCluster {
		t.Errorf("cluster layer=%v, want %q", cluster.Layer, LayerCluster)
	}

	// Node.
	nodeBody := fmt.Sprintf(`{"cluster_id":%q,"name":"node-a"}`, cluster.Id.String())
	nodeResp := do(h, http.MethodPost, "/v1/nodes", nodeBody)
	if nodeResp.Code != http.StatusCreated {
		t.Fatalf("create node: status=%d body=%q", nodeResp.Code, nodeResp.Body.String())
	}
	var node Node
	if err := json.Unmarshal(nodeResp.Body.Bytes(), &node); err != nil {
		t.Fatalf("decode node: %v", err)
	}
	if node.Layer == nil || *node.Layer != LayerNode {
		t.Errorf("node layer=%v, want %q", node.Layer, LayerNode)
	}

	// Namespace.
	nsBody := fmt.Sprintf(`{"cluster_id":%q,"name":"default"}`, cluster.Id.String())
	nsResp := do(h, http.MethodPost, "/v1/namespaces", nsBody)
	if nsResp.Code != http.StatusCreated {
		t.Fatalf("create namespace: status=%d body=%q", nsResp.Code, nsResp.Body.String())
	}
	var ns Namespace
	if err := json.Unmarshal(nsResp.Body.Bytes(), &ns); err != nil {
		t.Fatalf("decode namespace: %v", err)
	}
	if ns.Layer == nil || *ns.Layer != LayerNamespace {
		t.Errorf("namespace layer=%v, want %q", ns.Layer, LayerNamespace)
	}

	// Layer must also be set on GET and on list items.
	getResp := do(h, http.MethodGet, "/v1/clusters/"+cluster.Id.String(), "")
	if err := json.Unmarshal(getResp.Body.Bytes(), &cluster); err != nil {
		t.Fatalf("decode get cluster: %v", err)
	}
	if cluster.Layer == nil || *cluster.Layer != LayerCluster {
		t.Errorf("get cluster layer=%v, want %q", cluster.Layer, LayerCluster)
	}

	listResp := do(h, http.MethodGet, "/v1/nodes", "")
	var page NodeList
	if err := json.Unmarshal(listResp.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode node list: %v", err)
	}
	if len(page.Items) == 0 || page.Items[0].Layer == nil || *page.Items[0].Layer != LayerNode {
		t.Errorf("list node layer=%v, want %q", page.Items, LayerNode)
	}
}

func TestCreateClusterValidation(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"empty body", "", http.StatusBadRequest},
		{"missing name", `{"environment":"dev"}`, http.StatusBadRequest},
		{"unknown field", `{"name":"x","bogus":true}`, http.StatusBadRequest},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rr := do(h, http.MethodPost, "/v1/clusters", tt.body)
			if rr.Code != tt.wantStatus {
				t.Errorf("status=%d want=%d body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}

func TestUnknownRoute404(t *testing.T) {
	t.Parallel()
	h := newTestHandler(t, newMemStore())
	rr := do(h, http.MethodGet, "/no-such-path", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("status=%d", rr.Code)
	}
}

func do(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
