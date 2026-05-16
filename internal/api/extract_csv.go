package api

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
)

var errCSVColumnMismatch = errors.New("csv: column count mismatch")

// extractCSVWriter wraps encoding/csv with the conventions our extracts
// commit to: UTF-8 BOM so Excel auto-detects encoding, CRLF line endings
// (RFC 4180), and a fixed header row written eagerly so even a zero-row
// extract is a valid CSV. Caller must Close() to flush.
type extractCSVWriter struct {
	w       *csv.Writer
	columns int
	closed  bool
}

func newExtractCSVWriter(out io.Writer, header []string) *extractCSVWriter {
	// UTF-8 BOM up front so Excel decodes the file as UTF-8 by default.
	_, _ = out.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(out)
	cw.UseCRLF = true
	_ = cw.Write(header)
	return &extractCSVWriter{w: cw, columns: len(header)}
}

// WriteRow writes one row. Returns an error if the row's column count
// does not match the header's.
func (e *extractCSVWriter) WriteRow(row []string) error {
	if len(row) != e.columns {
		return fmt.Errorf("%w (got %d, want %d)", errCSVColumnMismatch, len(row), e.columns)
	}
	if err := e.w.Write(row); err != nil {
		return fmt.Errorf("csv: write row: %w", err)
	}
	return nil
}

// Close flushes the underlying buffer. Safe to call twice.
func (e *extractCSVWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	e.w.Flush()
	if err := e.w.Error(); err != nil {
		return fmt.Errorf("csv: flush: %w", err)
	}
	return nil
}
