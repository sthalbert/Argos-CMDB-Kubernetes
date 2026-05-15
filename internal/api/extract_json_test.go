package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONStreamWriter_Empty(t *testing.T) {
	var buf bytes.Buffer
	w := newExtractJSONWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if buf.String() != "[]\n" {
		t.Fatalf("empty stream: got %q want %q", buf.String(), "[]\n")
	}
}

func TestJSONStreamWriter_TwoRows(t *testing.T) {
	var buf bytes.Buffer
	w := newExtractJSONWriter(&buf)
	if err := w.WriteRow(map[string]string{"a": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow(map[string]string{"a": "2"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var got []map[string]string
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if len(got) != 2 || got[0]["a"] != "1" || got[1]["a"] != "2" {
		t.Fatalf("unexpected rows: %v", got)
	}
}
