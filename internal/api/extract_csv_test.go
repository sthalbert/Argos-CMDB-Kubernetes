package api

import (
	"bytes"
	"strings"
	"testing"
)

func TestCSVWriter_BOMAndHeader(t *testing.T) {
	var buf bytes.Buffer
	w := newExtractCSVWriter(&buf, []string{"a", "b"})
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := buf.Bytes()
	if !bytes.HasPrefix(got, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("missing UTF-8 BOM, got %q", got)
	}
	rest := bytes.TrimPrefix(got, []byte{0xEF, 0xBB, 0xBF})
	if string(rest) != "a,b\r\n" {
		t.Fatalf("header mismatch, got %q", rest)
	}
}

func TestCSVWriter_EscapesSpecialChars(t *testing.T) {
	var buf bytes.Buffer
	w := newExtractCSVWriter(&buf, []string{"col"})
	if err := w.WriteRow([]string{`has,comma`}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]string{"has\"quote"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteRow([]string{"has\nnewline"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out := string(bytes.TrimPrefix(buf.Bytes(), []byte{0xEF, 0xBB, 0xBF}))
	want := "col\r\n" +
		`"has,comma"` + "\r\n" +
		`"has""quote"` + "\r\n" +
		`"has` + "\r\n" + `newline"` + "\r\n"
	if out != want {
		t.Fatalf("escape mismatch\n got: %q\nwant: %q", out, want)
	}
}

func TestCSVWriter_RejectsRowLengthMismatch(t *testing.T) {
	var buf bytes.Buffer
	w := newExtractCSVWriter(&buf, []string{"a", "b"})
	if err := w.WriteRow([]string{"only-one"}); err == nil {
		t.Fatal("expected error on column-count mismatch")
	} else if !strings.Contains(err.Error(), "column count") {
		t.Fatalf("unexpected error: %v", err)
	}
}
