package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/auth"
)

func newLowCapServer(t *testing.T, store *extractStubStore, maxRows int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	wrap := func(h http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := &auth.Caller{
				Kind:   auth.CallerKindUser,
				Role:   "viewer",
				Scopes: []string{auth.ScopeRead},
			}
			ctx := auth.WithCaller(r.Context(), caller)
			r = r.WithContext(ctx)
			h(w, r)
		})
	}
	mux.Handle("GET /v1/eol/extract", wrap(api.HandleEolExtract(store, maxRows)))
	mux.Handle("GET /v1/search/extract", wrap(api.HandleSearchExtract(store, maxRows)))
	return httptest.NewServer(mux)
}

func makeBigCluster(id uuid.UUID, displayName string, anns map[string]string) api.Cluster {
	uid := openapi_types.UUID(id)
	return api.Cluster{
		Id:          &uid,
		Name:        "big-cluster",
		DisplayName: &displayName,
		Annotations: &anns,
	}
}

func TestEolExtract_CapTruncates(t *testing.T) {
	store := newExtractStubStore()
	clusterID := uuid.MustParse("88888888-8888-8888-8888-888888888888")
	displayName := "big"
	manyAnnotations := make(map[string]string, 50)
	for i := 0; i < 50; i++ {
		k := fmt.Sprintf("longue-vue.io/eol.fake%02d", i)
		manyAnnotations[k] = `{"product":"fake","cycle":"1","eol_status":"unknown","checked_at":"2026-05-15T00:00:00Z"}`
	}
	store.clusters = append(store.clusters, makeBigCluster(clusterID, displayName, manyAnnotations))

	srv := newLowCapServer(t, store, 10)
	defer srv.Close()

	resp, body := getWithAuth(t, srv.URL+"/v1/eol/extract?format=csv")
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Longue-Vue-Truncated"); got != "true" {
		t.Errorf("X-Longue-Vue-Truncated: got %q want %q", got, "true")
	}
	bodyAfterBOM := strings.TrimPrefix(body, "\xEF\xBB\xBF")
	// Count CRLF separators after stripping trailing newlines.
	// With maxRows=10: 1 header + 10 data rows = 11 CRLF-terminated lines.
	// After TrimRight the trailing CRLF of the last row is stripped → 10.
	totalLines := strings.Count(strings.TrimRight(bodyAfterBOM, "\r\n"), "\r\n")
	if totalLines != 10 {
		t.Errorf("expected 10 CRLF separators (1 header + 10 rows = 11 lines, last stripped), got %d", totalLines)
	}
}
