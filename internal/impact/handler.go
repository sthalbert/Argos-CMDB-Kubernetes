package impact

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
	"github.com/sthalbert/argos/internal/metrics"
)

const (
	defaultDepth = 2
	maxDepth     = 3
)

// HandleImpact returns the impact graph for an entity.
// Route: GET /v1/impact/{entity_type}/{id}?depth=2
func HandleImpact(store TraverserStore) http.HandlerFunc {
	traverser := NewTraverser(store)

	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		entityType := r.PathValue("entity_type")
		if !ValidEntityType(entityType) {
			http.Error(w, "invalid entity type", http.StatusBadRequest)
			return
		}

		rawID := r.PathValue("id")
		id, err := uuid.Parse(rawID)
		if err != nil {
			http.Error(w, "invalid id", http.StatusBadRequest)
			return
		}

		depth := defaultDepth
		if d := r.URL.Query().Get("depth"); d != "" {
			parsed, err := strconv.Atoi(d)
			if err != nil || parsed < 1 || parsed > maxDepth {
				http.Error(w, "depth must be 1-3", http.StatusBadRequest)
				return
			}
			depth = parsed
		}

		start := time.Now()
		graph, err := traverser.Traverse(r.Context(), EntityType(entityType), id, depth)
		duration := time.Since(start)
		metrics.ObserveImpactQuery(entityType, duration)

		if err != nil {
			http.Error(w, "traversal failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(graph) //nolint:errcheck // response write to HTTP client
	}
}
