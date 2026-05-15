package api_test

import (
	"archive/zip"
	"encoding/json"
	"strings"
	"testing"
)

func TestEolExtract_CSV_HappyPath(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=csv")
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d, body: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type: %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="longue-vue-eol-`) {
		t.Errorf("Content-Disposition: %q", cd)
	}
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Errorf("missing UTF-8 BOM")
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	wantHeader := "entity_type,entity_id,entity_name,cluster,product,cycle,status,eol_date,latest,latest_available,support,checked_at"
	if lines[0] != wantHeader {
		t.Errorf("header:\n got %q\nwant %q", lines[0], wantHeader)
	}
	if len(lines) < 2 {
		t.Fatalf("expected at least one data row, got %d lines", len(lines))
	}
}

func TestEolExtract_JSON_HappyPath(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=json")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type: %q", ct)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, body)
	}
	if len(rows) < 1 {
		t.Fatalf("no rows: %v", rows)
	}
	if rows[0]["entity_type"] == nil {
		t.Errorf("first row missing entity_type: %v", rows[0])
	}
}

func TestEolExtract_StatusFilter(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=json&status=eol")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	for _, r := range rows {
		if r["status"] != "eol" {
			t.Errorf("non-EOL row in filtered output: %v", r)
		}
	}
}

func TestEolExtract_BadFormat(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, _ := getWithAuth(t, srv.URL+"/v1/eol/extract?format=xml")
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type on 400: %q", ct)
	}
}

func TestSearchExtract_Workloads_CSV(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d, body: %s", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "longue-vue-search-workloads-log4j-") {
		t.Errorf("Content-Disposition: %q", cd)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	wantHeader := "cluster,namespace,kind,name,image_matches,replicas,ready_replicas,updated_at"
	if lines[0] != wantHeader {
		t.Fatalf("header:\n got %q\nwant %q", lines[0], wantHeader)
	}
	if len(lines) != 2 {
		t.Fatalf("want 1 header + 1 data row, got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "log4j-app") || !strings.Contains(lines[1], "log4j:2.15") {
		t.Errorf("data row missing fields: %q", lines[1])
	}
}

func TestSearchExtract_Pods_CSV(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=log4j&kind=pods&format=csv")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	wantHeader := "cluster,namespace,name,workload_kind,workload_name,image_matches,phase,node,updated_at"
	if lines[0] != wantHeader {
		t.Fatalf("header:\n got %q\nwant %q", lines[0], wantHeader)
	}
	if len(lines) != 2 {
		t.Fatalf("want 1+1 lines, got %d", len(lines))
	}
}

func TestSearchExtract_VMs_CSV(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=vault&kind=virtual_machines&format=csv")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d body: %s", resp.StatusCode, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	wantHeader := "cloud_account,region,name,display_name,role,power_state,image_id,image_name,applications_matched,updated_at"
	if lines[0] != wantHeader {
		t.Fatalf("header:\n got %q\nwant %q", lines[0], wantHeader)
	}
	if len(lines) != 2 {
		t.Fatalf("want 1+1 lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "acme-prod") {
		t.Errorf("expected cloud account name 'acme-prod' in row: %q", lines[1])
	}
	if !strings.Contains(lines[1], "vault") {
		t.Errorf("expected 'vault' in applications_matched: %q", lines[1])
	}
}

func TestSearchExtract_BadKind(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, _ := getWithAuth(t, srv.URL+"/v1/search/extract?q=x&kind=nodes&format=csv")
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestSearchExtract_QueryTooLong(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	q := strings.Repeat("x", 257)
	resp, _ := getWithAuth(t, srv.URL+"/v1/search/extract?q="+q+"&kind=workloads&format=csv")
	if resp.StatusCode != 400 {
		t.Fatalf("status: want 400, got %d", resp.StatusCode)
	}
}

func TestSearchExtractZip_HasThreeCSVsAndREADME(t *testing.T) {
	srv, _ := newExtractTestServer(t)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/search/extract.zip?q=log4j")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type: %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition: %q", cd)
	}
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{"workloads.csv", "pods.csv", "virtual_machines.csv", "README.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries: got %v want %v", names, want)
	}
}
