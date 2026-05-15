package api_test

import (
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
