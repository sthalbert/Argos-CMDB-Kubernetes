package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

func clusterPolicyUniqueKey(clusterID uuid.UUID, namespaceID *uuid.UUID, name string) string {
	ns := uuid.Nil.String()
	if namespaceID != nil {
		ns = namespaceID.String()
	}
	return fmt.Sprintf("%s/%s/%s", clusterID.String(), ns, name)
}

func policyReportUniqueKey(clusterID uuid.UUID, namespaceID *uuid.UUID, name string) string {
	ns := uuid.Nil.String()
	if namespaceID != nil {
		ns = namespaceID.String()
	}
	return fmt.Sprintf("%s/%s/%s", clusterID.String(), ns, name)
}

func (m *memStore) GetClusterPolicy(_ context.Context, id uuid.UUID) (ClusterPolicyRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.clusterPolicies[id]
	if !ok {
		return ClusterPolicyRow{}, ErrNotFound
	}
	return cp, nil
}

func (m *memStore) ListClusterPolicies(_ context.Context, _ ClusterPolicyListFilter, _ ListPage) ([]ClusterPolicyRow, string, error) {
	return nil, "", nil
}

//nolint:gocritic // hugeParam: Store interface mandates the value param
func (m *memStore) UpsertClusterPolicy(_ context.Context, cp ClusterPolicyRow) (uuid.UUID, error) {
	if cp.Source == "" {
		cp.Source = SourceAPI
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	uk := clusterPolicyUniqueKey(cp.ClusterID, cp.NamespaceID, cp.Name)
	for id, existing := range m.clusterPolicies { //nolint:gocritic // rangeValCopy: small test fake; clarity over micro-optimisation
		existingUK := clusterPolicyUniqueKey(existing.ClusterID, existing.NamespaceID, existing.Name)
		if existingUK == uk {
			if cp.Source == SourceAPI && existing.Source == SourceCollector {
				return uuid.Nil, ErrConflict
			}
			delete(m.clusterPolicies, id)
			break
		}
	}
	id := uuid.New()
	cp.ID = id
	m.clusterPolicies[id] = cp
	return id, nil
}

func (m *memStore) DeleteClusterScopedPoliciesNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeleteClusterPoliciesByNamespace(_ context.Context, _, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeleteClusterPolicy(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.clusterPolicies[id]
	if !ok || cp.Source == SourceCollector {
		return ErrNotFound
	}
	delete(m.clusterPolicies, id)
	return nil
}

func (m *memStore) GetPolicyReport(_ context.Context, id uuid.UUID) (PolicyReportRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.policyReports[id]
	if !ok {
		return PolicyReportRow{}, ErrNotFound
	}
	return pr, nil
}

func (m *memStore) ListPolicyReports(_ context.Context, _ PolicyReportListFilter, _ ListPage) ([]PolicyReportRow, string, error) {
	return nil, "", nil
}

//nolint:gocritic // hugeParam: Store interface mandates the value param
func (m *memStore) UpsertPolicyReport(_ context.Context, pr PolicyReportRow) (uuid.UUID, error) {
	if pr.Source == "" {
		pr.Source = SourceAPI
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	uk := policyReportUniqueKey(pr.ClusterID, pr.NamespaceID, pr.Name)
	for id, existing := range m.policyReports { //nolint:gocritic // rangeValCopy: small test fake; clarity over micro-optimisation
		existingUK := policyReportUniqueKey(existing.ClusterID, existing.NamespaceID, existing.Name)
		if existingUK == uk {
			if pr.Source == SourceAPI && existing.Source == SourceCollector {
				return uuid.Nil, ErrConflict
			}
			delete(m.policyReports, id)
			break
		}
	}
	id := uuid.New()
	pr.ID = id
	m.policyReports[id] = pr
	return id, nil
}

func (m *memStore) DeleteClusterScopedPolicyReportsNotIn(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeletePolicyReportsByNamespace(_ context.Context, _, _ uuid.UUID, _ []uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *memStore) DeletePolicyReport(_ context.Context, id uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pr, ok := m.policyReports[id]
	if !ok || pr.Source == SourceCollector {
		return ErrNotFound
	}
	delete(m.policyReports, id)
	return nil
}
