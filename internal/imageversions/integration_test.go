//go:build integration

package imageversions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/imageversions"
	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
)

// mirrorIntegrationStore is an in-memory Store wired only with the methods
// the enricher needs for this test.
type mirrorIntegrationStore struct {
	mu       sync.Mutex
	settings api.Settings
	regs     []api.ImageRegistry
	refs     []string
	mirror   api.ImageRegistry
	upserted []api.ImageVersionUpsert
}

func (s *mirrorIntegrationStore) GetSettings(_ context.Context) (api.Settings, error) {
	return s.settings, nil
}
func (s *mirrorIntegrationStore) ListImageRegistries(_ context.Context) ([]api.ImageRegistry, error) {
	return s.regs, nil
}
func (s *mirrorIntegrationStore) DistinctImageRefs(_ context.Context) ([]string, error) {
	return s.refs, nil
}

//nolint:gocritic
func (s *mirrorIntegrationStore) UpsertImageVersion(_ context.Context, in api.ImageVersionUpsert) (api.ImageVersionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserted = append(s.upserted, in)
	return api.ImageVersionRow{ImageRepo: in.ImageRepo, Variant: in.Variant}, nil
}
func (s *mirrorIntegrationStore) DeleteImageVersionsNotIn(_ context.Context, _ [][2]string) (int64, error) {
	return 0, nil
}
func (s *mirrorIntegrationStore) FindMirrorForRef(_ context.Context, hostname, _ string) (api.ImageRegistry, error) {
	if hostname == s.mirror.Hostname {
		return s.mirror, nil
	}
	return api.ImageRegistry{}, api.ErrNotFound
}
func (s *mirrorIntegrationStore) GetMirrorAuthToken(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

// stubLister satisfies imageversions.TagsLister.
type stubLister struct {
	tags map[string][]string
}

func (l *stubLister) ListTags(_ context.Context, _, repoPath string) ([]string, error) {
	return l.tags[repoPath], nil
}

// TestImageVersions_Integration verifies that a mirror ref is resolved to
// its public origin via the OCI annotation path, then enriched against the
// expected origin registry.
func TestImageVersions_Integration(t *testing.T) {
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"annotations": map[string]string{
				"org.opencontainers.image.base.name": "docker.io/library/nginx:1.25",
			},
		})
	}))
	defer mirror.Close()
	u, _ := url.Parse(mirror.URL)

	store := &mirrorIntegrationStore{
		settings: api.Settings{ImageVersionsEnabled: true},
		regs: []api.ImageRegistry{
			{Hostname: "docker.io", Enabled: true, RateLimitPerSec: 5},
		},
		refs: []string{u.Host + "/container/library/nginx:1.25"},
		mirror: api.ImageRegistry{
			Hostname:   u.Host,
			PathPrefix: "container/",
			IsMirror:   true,
			Enabled:    true,
		},
	}

	lister := &stubLister{tags: map[string][]string{
		"library/nginx": {"1.25", "1.26"},
	}}

	resolver := &mirrorresolve.HTTPResolver{
		Lookup: imageversions.NewStoreMirrorLookup(store),
		Client: mirror.Client(),
		Scheme: "http",
	}
	enricher := imageversions.NewEnricherWithResolver(store, lister, resolver, time.Hour, nil)
	enricher.RunTick(context.Background())

	if len(store.upserted) != 1 {
		t.Fatalf("expected 1 upsert, got %d", len(store.upserted))
	}
	got := store.upserted[0]
	if got.ImageRepo != "docker.io/library/nginx" {
		t.Fatalf("image_repo=%q want docker.io/library/nginx", got.ImageRepo)
	}
	if got.LatestTag == nil || *got.LatestTag != "1.26" {
		t.Fatalf("latest_tag=%v want 1.26", got.LatestTag)
	}
}
