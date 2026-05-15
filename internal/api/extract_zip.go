package api

import (
	"archive/zip"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// extractZIPMeta is the human-readable metadata embedded in README.txt.
type extractZIPMeta struct {
	Query       string
	GeneratedAt time.Time
	Version     string
}

// extractZIPWriter bundles per-entity CSV writers and a README into a
// single ZIP. Order of entries in the archive matches the order CSVs
// were added; README.txt is always last.
type extractZIPWriter struct {
	zw        *zip.Writer
	meta      extractZIPMeta
	files     []string       // insertion order
	rowCounts map[string]int // name → rows
}

func newExtractZIPWriter(out io.Writer, meta extractZIPMeta) *extractZIPWriter {
	return &extractZIPWriter{
		zw:        zip.NewWriter(out),
		meta:      meta,
		rowCounts: make(map[string]int),
	}
}

// AddCSV opens a new entry inside the ZIP and returns a CSV writer for
// it. The caller must Close() the returned writer before calling AddCSV
// again or Close() on the ZIP itself — concurrent zip entries are not
// supported by archive/zip.
func (z *extractZIPWriter) AddCSV(name string, header []string) (*extractCSVWriter, error) {
	w, err := z.zw.Create(name)
	if err != nil {
		return nil, fmt.Errorf("zip: create %s: %w", name, err)
	}
	z.files = append(z.files, name)
	return newExtractCSVWriter(w, header), nil
}

// SetRowCount records the number of rows for the given CSV (for the
// README). Must be called before Close().
func (z *extractZIPWriter) SetRowCount(name string, n int) {
	z.rowCounts[name] = n
}

// Close writes README.txt and finalises the archive.
func (z *extractZIPWriter) Close() error {
	w, err := z.zw.Create("README.txt")
	if err != nil {
		return fmt.Errorf("zip: create README: %w", err)
	}
	if _, err := w.Write([]byte(z.renderREADME())); err != nil {
		return fmt.Errorf("zip: write README: %w", err)
	}
	if err := z.zw.Close(); err != nil {
		return fmt.Errorf("zip: close: %w", err)
	}
	return nil
}

func (z *extractZIPWriter) renderREADME() string {
	var b strings.Builder
	b.WriteString("longue-vue extract\n")
	b.WriteString("==================\n\n")
	b.WriteString(fmt.Sprintf("query: %s\n", z.meta.Query))
	b.WriteString(fmt.Sprintf("generated: %s\n", z.meta.GeneratedAt.UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("server: %s\n\n", z.meta.Version))
	b.WriteString("Files:\n")
	names := append([]string(nil), z.files...)
	sort.Strings(names)
	for _, name := range names {
		n := z.rowCounts[name]
		unit := "rows"
		if n == 1 {
			unit = "row"
		}
		b.WriteString(fmt.Sprintf("  %s: %d %s\n", name, n, unit))
	}
	b.WriteString("\nThis is a point-in-time snapshot. Re-run the same extract on\n")
	b.WriteString("the UI to refresh.\n")
	return b.String()
}
