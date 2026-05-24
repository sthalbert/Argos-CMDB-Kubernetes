package api_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
)

// appExtractStubStore implements api.ApplicationExtractStore in-memory.
// ListApplications honours the criticality + dict_min filters (the cases
// the tests exercise); other filters pass through unfiltered.
type appExtractStubStore struct {
	apps []api.Application
}

func (s *appExtractStubStore) ListApplications(
	_ context.Context,
	filter api.ApplicationListFilter,
	_ int,
	_ string,
) ([]api.Application, string, error) {
	out := make([]api.Application, 0, len(s.apps))
	for i := range s.apps {
		a := s.apps[i]
		if filter.Criticality != nil && (a.Criticality == nil || *a.Criticality != *filter.Criticality) {
			continue
		}
		if filter.DICTMin != nil && maxDICT(&a) < *filter.DICTMin {
			continue
		}
		out = append(out, a)
	}
	return out, "", nil
}

func maxDICT(a *api.Application) int {
	m := 0
	for _, v := range []*int{a.SecDisponibilite, a.SecIntegrite, a.SecConfidentialite, a.SecTracabilite} {
		if v != nil && *v > m {
			m = *v
		}
	}
	return m
}

func intPtr(n int) *int { return &n }

// appBillingName is referenced across several extract assertions; a const
// keeps goconst quiet without scattering string literals.
const appBillingName = "billing-api"

func seedTwoApplications() *appExtractStubStore {
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	block := "billing"
	critical := "critical"
	standard := "standard"
	disp := "Billing API"
	owner := "team-payments"
	return &appExtractStubStore{
		apps: []api.Application{
			{
				ID:                   uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"),
				Name:                 appBillingName,
				DisplayName:          &disp,
				ApplicationBlockName: &block,
				Owner:                &owner,
				Criticality:          &critical,
				SecDisponibilite:     intPtr(3),
				SecIntegrite:         intPtr(4),
				SecConfidentialite:   intPtr(2),
				SecTracabilite:       intPtr(1),
				CreatedAt:            now,
				UpdatedAt:            now,
				MemberCounts:         api.ApplicationMemberCounts{Workloads: 2, VirtualMachines: 1, VMApplications: 3},
			},
			{
				ID:           uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002"),
				Name:         "docs-portal",
				Criticality:  &standard,
				CreatedAt:    now,
				UpdatedAt:    now,
				MemberCounts: api.ApplicationMemberCounts{Workloads: 1},
			},
		},
	}
}

func newAppExtractServer(t *testing.T, store *appExtractStubStore, maxRows int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	wrap := func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := &auth.Caller{Kind: auth.CallerKindUser, Role: "viewer", Scopes: []string{auth.ScopeRead}}
			r = r.WithContext(auth.WithCaller(r.Context(), caller))
			h(w, r)
		})
	}
	mux.Handle("GET /v1/applications/extract.csv", wrap(api.HandleExtractApplicationsCSV(store, maxRows)))
	mux.Handle("GET /v1/applications/extract.json", wrap(api.HandleExtractApplicationsJSON(store, maxRows)))
	return httptest.NewServer(mux)
}

//nolint:gocyclo // sequential column assertions inflate the score; each check is trivial
func TestApplicationsExtractCSV_HeaderAndRows(t *testing.T) {
	srv := newAppExtractServer(t, seedTwoApplications(), 50000)
	defer srv.Close()

	status, _, body := getWithAuth(t, srv.URL+"/v1/applications/extract.csv")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	body = strings.TrimPrefix(body, "\xEF\xBB\xBF")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("rows = %d, want 3 (1 header + 2 data)", len(records))
	}
	wantHeader := []string{
		"id", "name", "display_name", "application_block", "owner", "criticality",
		"sec_disponibilite", "sec_integrite", "sec_confidentialite", "sec_tracabilite", "sec_notes",
		"workloads", "virtual_machines", "vm_applications", "created_at", "updated_at",
	}
	if strings.Join(records[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("header = %v\nwant %v", records[0], wantHeader)
	}
	// First data row is billing-api.
	row := records[1]
	if row[1] != appBillingName || row[3] != "billing" || row[5] != "critical" {
		t.Errorf("row = %v; name/block/criticality unexpected", row)
	}
	if row[6] != "3" || row[7] != "4" || row[8] != "2" || row[9] != "1" {
		t.Errorf("DICT columns = %v; want 3,4,2,1", row[6:10])
	}
	if row[11] != "2" || row[12] != "1" || row[13] != "3" {
		t.Errorf("member-count columns = %v; want 2,1,3", row[11:14])
	}
}

func TestApplicationsExtractJSON_Array(t *testing.T) {
	srv := newAppExtractServer(t, seedTwoApplications(), 50000)
	defer srv.Close()

	status, hdr, body := getWithAuth(t, srv.URL+"/v1/applications/extract.json")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got []api.Application
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, body)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Name != appBillingName || got[0].MemberCounts.VMApplications != 3 {
		t.Errorf("got[0] = %+v; member_counts not preserved", got[0])
	}
}

func TestApplicationsExtract_CriticalityFilter(t *testing.T) {
	srv := newAppExtractServer(t, seedTwoApplications(), 50000)
	defer srv.Close()

	status, _, body := getWithAuth(t, srv.URL+"/v1/applications/extract.json?criticality=critical")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	var got []api.Application
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != appBillingName {
		t.Errorf("got = %+v; want only billing-api", got)
	}
}

func TestApplicationsExtract_CapTruncates(t *testing.T) {
	srv := newAppExtractServer(t, seedTwoApplications(), 1)
	defer srv.Close()

	status, hdr, body := getWithAuth(t, srv.URL+"/v1/applications/extract.json")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := hdr.Get("X-Longue-Vue-Truncated"); got != "true" {
		t.Errorf("X-Longue-Vue-Truncated = %q, want true", got)
	}
	var got []api.Application
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len = %d, want 1 (capped)", len(got))
	}
}

func TestApplicationsExtract_RequiresReadScope(t *testing.T) {
	store := seedTwoApplications()
	mux := http.NewServeMux()
	// No caller injected → anonymous → 403.
	mux.Handle("GET /v1/applications/extract.csv", api.HandleExtractApplicationsCSV(store, 50000))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	status, _, _ := getWithAuth(t, srv.URL+"/v1/applications/extract.csv")
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
}
