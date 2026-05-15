package api

import (
	"encoding/json"
	"fmt"
	"io"
)

// extractJSONWriter emits a top-level JSON array of objects, streaming
// row-by-row. Zero rows produces "[]\n" so the file is always a valid
// JSON document. Caller must Close() to write the trailing "]".
type extractJSONWriter struct {
	w       io.Writer
	enc     *json.Encoder
	started bool
	closed  bool
}

func newExtractJSONWriter(out io.Writer) *extractJSONWriter {
	return &extractJSONWriter{
		w:   out,
		enc: json.NewEncoder(out),
	}
}

// WriteRow encodes one row. The first call writes "[", subsequent calls
// write ",". encoding/json's Encoder appends a trailing newline after
// each value, which JSON parsers tolerate inside an array.
func (e *extractJSONWriter) WriteRow(row any) error {
	if e.closed {
		return fmt.Errorf("json: write after close")
	}
	if !e.started {
		if _, err := e.w.Write([]byte("[")); err != nil {
			return fmt.Errorf("json: open array: %w", err)
		}
		e.started = true
	} else {
		if _, err := e.w.Write([]byte(",")); err != nil {
			return fmt.Errorf("json: separator: %w", err)
		}
	}
	if err := e.enc.Encode(row); err != nil {
		return fmt.Errorf("json: encode row: %w", err)
	}
	return nil
}

// Close writes "]\n" (or "[]\n" if no rows were written).
func (e *extractJSONWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if !e.started {
		_, err := e.w.Write([]byte("[]\n"))
		if err != nil {
			return fmt.Errorf("json: write empty array: %w", err)
		}
		return nil
	}
	if _, err := e.w.Write([]byte("]\n")); err != nil {
		return fmt.Errorf("json: close array: %w", err)
	}
	return nil
}
