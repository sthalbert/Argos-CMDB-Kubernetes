package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

func seedApplication(id uuid.UUID, name, criticality string) api.Application {
	now := time.Now().UTC()
	crit := criticality
	return api.Application{
		ID:               id,
		Name:             name,
		Criticality:      &crit,
		SecDisponibilite: intp(3),
		SecIntegrite:     intp(4),
		CreatedAt:        now,
		UpdatedAt:        now,
		MemberCounts:     api.ApplicationMemberCounts{Workloads: 2, VirtualMachines: 1, VMApplications: 3},
	}
}

func intp(n int) *int { return &n }

func TestHandleListApplications_HappyPath(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.applications = []api.Application{
		seedApplication(uuid.New(), "billing-api", "critical"),
		seedApplication(uuid.New(), "docs-portal", "standard"),
	}
	s := newServer(t, store)

	r, err := s.handleListApplications(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got struct {
		Items []api.Application `json:"items"`
	}
	if err := json.Unmarshal([]byte(resultText(t, r)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len = %d; want 2", len(got.Items))
	}
	if got.Items[0].MemberCounts.VMApplications != 3 {
		t.Errorf("member_counts not preserved: %+v", got.Items[0].MemberCounts)
	}
}

func TestHandleListApplications_CriticalityFilter(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.applications = []api.Application{
		seedApplication(uuid.New(), "billing-api", "critical"),
		seedApplication(uuid.New(), "docs-portal", "standard"),
	}
	s := newServer(t, store)

	r, _ := s.handleListApplications(context.Background(), makeRequest("", map[string]any{"criticality": "critical"}))
	var got struct {
		Items []api.Application `json:"items"`
	}
	_ = json.Unmarshal([]byte(resultText(t, r)), &got)
	if len(got.Items) != 1 || got.Items[0].Name != "billing-api" {
		t.Errorf("got = %+v; want only billing-api", got.Items)
	}
	if store.lastAppFilter.Criticality == nil || *store.lastAppFilter.Criticality != "critical" {
		t.Errorf("filter.Criticality = %v; want 'critical'", store.lastAppFilter.Criticality)
	}
}

func TestHandleListApplications_RejectsBadDictMin(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	s := newServer(t, store)

	r, _ := s.handleListApplications(context.Background(), makeRequest("", map[string]any{"dict_min": "9"}))
	if r == nil || !r.IsError {
		t.Fatalf("expected an error result for dict_min=9")
	}
}

func TestHandleGetApplication_ByID(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	id := uuid.New()
	store.applications = []api.Application{seedApplication(id, "billing-api", "critical")}
	s := newServer(t, store)

	r, err := s.handleGetApplication(context.Background(), makeRequest("", map[string]any{"id": id.String()}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got api.Application
	if err := json.Unmarshal([]byte(resultText(t, r)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != id || got.Name != "billing-api" {
		t.Errorf("got = %+v; want billing-api/%s", got, id)
	}
}

func TestHandleGetApplication_ByName(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.applications = []api.Application{seedApplication(uuid.New(), "billing-api", "critical")}
	s := newServer(t, store)

	// "Billing API" normalizes to "billing-api".
	r, err := s.handleGetApplication(context.Background(), makeRequest("", map[string]any{"name": "Billing API"}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got api.Application
	if err := json.Unmarshal([]byte(resultText(t, r)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "billing-api" {
		t.Errorf("got = %+v; want billing-api", got)
	}
}

func TestHandleGetApplication_RequiresIDOrName(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	s := newServer(t, store)

	r, _ := s.handleGetApplication(context.Background(), makeRequest("", nil))
	if r == nil || !r.IsError {
		t.Fatalf("expected an error result when neither id nor name supplied")
	}
}

func TestHandleGetApplication_NotFound(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	s := newServer(t, store)

	r, err := s.handleGetApplication(context.Background(), makeRequest("", map[string]any{"id": uuid.New().String()}))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if r == nil || !r.IsError {
		t.Fatalf("expected a not-found error result")
	}
}

func TestHandleListApplicationBlocks_HappyPath(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	owner := "platform-team"
	store.appBlocks = []api.ApplicationBlock{
		{ID: uuid.New(), Name: "billing", Owner: &owner, ApplicationCount: 4},
		{ID: uuid.New(), Name: "edge"},
	}
	s := newServer(t, store)

	r, err := s.handleListApplicationBlocks(context.Background(), makeRequest("", nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got struct {
		Items []api.ApplicationBlock `json:"items"`
	}
	if err := json.Unmarshal([]byte(resultText(t, r)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("len = %d; want 2", len(got.Items))
	}
	if got.Items[0].ApplicationCount != 4 {
		t.Errorf("application_count = %d; want 4", got.Items[0].ApplicationCount)
	}
}

func TestHandleListApplicationBlocks_OwnerFilter(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	owner := "platform-team"
	store.appBlocks = []api.ApplicationBlock{
		{ID: uuid.New(), Name: "billing", Owner: &owner},
		{ID: uuid.New(), Name: "edge"},
	}
	s := newServer(t, store)

	r, _ := s.handleListApplicationBlocks(context.Background(), makeRequest("", map[string]any{"owner": "platform-team"}))
	var got struct {
		Items []api.ApplicationBlock `json:"items"`
	}
	_ = json.Unmarshal([]byte(resultText(t, r)), &got)
	if len(got.Items) != 1 || got.Items[0].Name != "billing" {
		t.Errorf("got = %+v; want only billing", got.Items)
	}
}
