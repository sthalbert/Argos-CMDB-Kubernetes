package api_test

import (
	"archive/zip"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestEolExtract_CSV_HappyPath(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, header, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=csv")
	if status != 200 {
		t.Fatalf("status: got %d, body: %s", status, body)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type: %q", ct)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, `attachment; filename="longue-vue-eol-`) {
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, header, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=json")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=json&status=eol")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, header, _ := getWithAuth(t, srv.URL+"/v1/eol/extract?format=xml")
	if status != 400 {
		t.Fatalf("status: want 400, got %d", status)
	}
	if ct := header.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Errorf("Content-Type on 400: %q", ct)
	}
}

func TestSearchExtract_Workloads_CSV(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, header, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, "longue-vue-search-workloads-log4j-") {
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=log4j&kind=pods&format=csv")
	if status != 200 {
		t.Fatalf("status: %d", status)
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t, srv.URL+"/v1/search/extract?q=vault&kind=virtual_machines&format=csv")
	if status != 200 {
		t.Fatalf("status: %d body: %s", status, body)
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
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, _ := getWithAuth(t, srv.URL+"/v1/search/extract?q=x&kind=nodes&format=csv")
	if status != 400 {
		t.Fatalf("status: want 400, got %d", status)
	}
}

func TestSearchExtract_QueryTooLong(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	q := strings.Repeat("x", 257)
	status, _, _ := getWithAuth(t, srv.URL+"/v1/search/extract?q="+q+"&kind=workloads&format=csv")
	if status != 400 {
		t.Fatalf("status: want 400, got %d", status)
	}
}

// ----------------------------------------------------------------------
// ADR-0029 §2.4 — `?application=<substring>` filter on the Search extract.
// The stub store links the seeded workload to "log4j-platform" and the
// seeded VM to "vault-platform" (see newExtractStubStore), so the test
// matrix below exercises hit, miss, cross-kind, and absence cases.
// ----------------------------------------------------------------------

func TestSearchExtract_ApplicationFilter_MatchesLinkedWorkload(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv&application=log4j-platform")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 data row, got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "log4j-app") {
		t.Errorf("expected linked workload in row: %q", lines[1])
	}
}

func TestSearchExtract_ApplicationFilter_SubstringMatchCaseInsensitive(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	// Mixed-case partial substring "PlatForm" must hit the lowercase
	// "log4j-platform" index entry seeded above.
	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv&application=PlatForm")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 data row, got %d lines: %v", len(lines), lines)
	}
}

func TestSearchExtract_ApplicationFilter_NotMatchingExcluded(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	// The workload is linked to "log4j-platform"; an `application=billing`
	// filter must exclude it even though `q=log4j` matches its image.
	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv&application=billing")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if len(lines) != 1 {
		t.Fatalf("want header only (no data rows), got %d lines: %v", len(lines), lines)
	}
}

func TestSearchExtract_ApplicationFilter_MatchesLinkedVM(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=vault&kind=virtual_machines&format=csv&application=vault-platform")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 data row, got %d lines: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "bastion-prod") {
		t.Errorf("expected linked VM in row: %q", lines[1])
	}
}

func TestSearchExtract_ApplicationFilter_VMNotMatchingExcluded(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=vault&kind=virtual_machines&format=csv&application=no-such-app")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	lines := strings.Split(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if len(lines) != 1 {
		t.Fatalf("want header only, got %d lines: %v", len(lines), lines)
	}
}

func TestSearchExtract_ApplicationFilter_PodsViaOwningWorkload(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	// The seeded pod is owned by the workload linked to "log4j-platform";
	// the transitive link must include it.
	status, _, bodyHit := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=pods&format=csv&application=log4j-platform")
	if status != 200 {
		t.Fatalf("hit status: %d, body: %s", status, bodyHit)
	}
	linesHit := strings.Split(strings.TrimRight(strings.TrimPrefix(bodyHit, "\xEF\xBB\xBF"), "\r\n"), "\r\n")
	if len(linesHit) != 2 {
		t.Fatalf("transitive link should include the pod: got %d lines: %v", len(linesHit), linesHit)
	}
	// Conversely, an unrelated application name must exclude the pod.
	status, _, bodyMiss := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=pods&format=csv&application=billing")
	if status != 200 {
		t.Fatalf("miss status: %d, body: %s", status, bodyMiss)
	}
	linesMiss := strings.Split(strings.TrimRight(strings.TrimPrefix(bodyMiss, "\xEF\xBB\xBF"), "\r\n"), "\r\n")
	if len(linesMiss) != 1 {
		t.Fatalf("non-matching app should exclude pod: got %d lines: %v", len(linesMiss), linesMiss)
	}
}

func TestSearchExtract_NoApplicationParam_Unchanged(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	// Sanity: omitting `application` must yield the same result set the
	// pre-ADR-0029 happy-path test asserts (1 header + 1 data row).
	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv")
	if status != 200 {
		t.Fatalf("status: %d, body: %s", status, body)
	}
	lines := strings.Split(strings.TrimRight(strings.TrimPrefix(body, "\xEF\xBB\xBF"), "\r\n"), "\r\n")
	if len(lines) != 2 {
		t.Fatalf("baseline (no filter) should yield 1+1 lines, got %d: %v", len(lines), lines)
	}
}

func TestSearchExtract_ApplicationFilter_TooLong(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	app := strings.Repeat("x", 257)
	status, _, _ := getWithAuth(t,
		srv.URL+"/v1/search/extract?q=log4j&kind=workloads&format=csv&application="+app)
	if status != 400 {
		t.Fatalf("status: want 400, got %d", status)
	}
}

func TestSearchExtractZip_ApplicationFilter_NarrowsAllThreeCSVs(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	// q=platform is a generic substring; the application filter narrows
	// workloads.csv to the log4j-platform-linked row, virtual_machines.csv
	// is empty (vault VM not linked to log4j-platform), and pods.csv has
	// the transitive pod.
	status, _, body := getWithAuth(t,
		srv.URL+"/v1/search/extract.zip?q=log4j&application=log4j-platform")
	if status != 200 {
		t.Fatalf("status: %d", status)
	}
	zr, err := zip.NewReader(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	counts := map[string]int{}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".csv") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		// 1 header row + N data rows, all CRLF-terminated; subtract the BOM.
		body := strings.TrimRight(strings.TrimPrefix(string(raw), "\xEF\xBB\xBF"), "\r\n")
		counts[f.Name] = len(strings.Split(body, "\r\n")) - 1 // minus header
	}
	if counts["workloads.csv"] != 1 {
		t.Errorf("workloads.csv: want 1 data row, got %d", counts["workloads.csv"])
	}
	if counts["pods.csv"] != 1 {
		t.Errorf("pods.csv: want 1 data row (transitive), got %d", counts["pods.csv"])
	}
	if counts["virtual_machines.csv"] != 0 {
		t.Errorf("virtual_machines.csv: want 0 data rows (vault VM not linked to log4j-platform), got %d", counts["virtual_machines.csv"])
	}
}

func TestSearchExtractZip_HasThreeCSVsAndREADME(t *testing.T) {
	srv := newExtractTestServer(t)
	defer srv.Close()

	status, header, body := getWithAuth(t, srv.URL+"/v1/search/extract.zip?q=log4j")
	if status != 200 {
		t.Fatalf("status: %d", status)
	}
	if ct := header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type: %q", ct)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
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
