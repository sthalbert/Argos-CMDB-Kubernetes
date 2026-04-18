package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON writes body as JSON with the given status code.
// Errors encoding the body are logged; the response status is already written.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write json response", "error", err)
	}
}

// writeProblem writes an RFC 7807 problem+json response.
func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  title,
		Status: status,
	}
	if detail != "" {
		p.Detail = &detail
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Error("write problem response", "error", err)
	}
}
