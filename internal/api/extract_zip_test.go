package api

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
	"time"
)

//nolint:gocyclo // sequential ZIP construction checks; each if-err block is trivially simple
func TestZIPExtract_ContainsFourEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := newExtractZIPWriter(&buf, extractZIPMeta{
		Query:       "log4j",
		GeneratedAt: time.Date(2026, 5, 15, 14, 32, 0, 0, time.UTC),
		Version:     "test",
	})

	w, err := zw.AddCSV("workloads.csv", []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]string{"app1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w, err = zw.AddCSV("pods.csv", []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w, err = zw.AddCSV("virtual_machines.csv", []string{"name"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	zw.SetRowCount("workloads.csv", 1)
	zw.SetRowCount("pods.csv", 0)
	zw.SetRowCount("virtual_machines.csv", 0)

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	wantNames := []string{"workloads.csv", "pods.csv", "virtual_machines.csv", "README.txt"}
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("entries: got %v want %v", names, wantNames)
	}

	readme := findEntry(t, zr, "README.txt")
	for _, fragment := range []string{
		"longue-vue extract",
		"query: log4j",
		"generated: 2026-05-15",
		"workloads.csv: 1 row",
		"pods.csv: 0 row",
		"virtual_machines.csv: 0 row",
		"server: test",
	} {
		if !strings.Contains(readme, fragment) {
			t.Errorf("README missing %q\ngot: %s", fragment, readme)
		}
	}
}

func findEntry(t *testing.T, zr *zip.Reader, name string) string {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		t.Cleanup(func() { _ = rc.Close() })
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return buf.String()
	}
	t.Fatalf("entry %q not found", name)
	return ""
}
