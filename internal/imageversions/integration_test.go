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
	mirrors  map[string]api.ImageRegistry
	upserted []api.ImageVersionUpsert

	originUpserts []api.ImageOriginResolutionUpsert
	originReaped  [][2]string
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
	if m, ok := s.mirrors[hostname]; ok {
		return m, nil
	}
	return api.ImageRegistry{}, api.ErrNotFound
}

func (s *mirrorIntegrationStore) GetMirrorAuthToken(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

//nolint:gocritic // in matches the Store interface signature
func (s *mirrorIntegrationStore) UpsertImageOriginResolution(_ context.Context, in api.ImageOriginResolutionUpsert) (api.ImageOriginResolution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.originUpserts = append(s.originUpserts, in)
	return api.ImageOriginResolution{
		MirrorImageRepo: in.MirrorImageRepo,
		Variant:         in.Variant,
		OriginImageRepo: in.OriginImageRepo,
		ViaHostname:     in.ViaHostname,
		ResolvedAt:      in.ResolvedAt,
		LastError:       in.LastError,
		LastErrorAt:     in.LastErrorAt,
	}, nil
}

func (s *mirrorIntegrationStore) DeleteImageOriginResolutionsNotIn(_ context.Context, keep [][2]string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.originReaped = keep
	return 0, nil
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
		mirrors: map[string]api.ImageRegistry{
			u.Host: {
				Hostname:   u.Host,
				PathPrefix: "container/",
				IsMirror:   true,
				Enabled:    true,
			},
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

// TestImageVersions_Integration_ReplicaChain verifies the three-rung chain:
// a replica-mirror pod ref -> annotation-mirror manifest fetch -> public
// registry tags lookup. Confirms both image_versions and
// image_origin_resolutions rows are written.
func TestImageVersions_Integration_ReplicaChain(t *testing.T) {
	// Annotation mirror: serves manifest with base.name -> ghcr.io/...
	annoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.manifest.v1+json",
			"annotations": map[string]string{
				"org.opencontainers.image.base.name": "ghcr.io/sthalbert/longue-vue-collector:0.26",
			},
		})
	}))
	defer annoSrv.Close()
	annoURL, _ := url.Parse(annoSrv.URL)

	annoHost := annoURL.Host
	store := &mirrorIntegrationStore{
		settings: api.Settings{ImageVersionsEnabled: true},
		regs: []api.ImageRegistry{
			{Hostname: "ghcr.io", Enabled: true, RateLimitPerSec: 5},
		},
		refs: []string{"localreg.example.com/containers/sthalbert/longue-vue-collector:0.26"},
		mirrors: map[string]api.ImageRegistry{
			"localreg.example.com": {
				Hostname:               "localreg.example.com",
				PathPrefix:             "containers/",
				IsMirror:               true,
				Enabled:                true,
				ReplicatesFromHostname: &annoHost,
			},
			annoHost: {
				Hostname:   annoHost,
				PathPrefix: "containers/",
				IsMirror:   true,
				Enabled:    true,
			},
		},
	}

	lister := &stubLister{tags: map[string][]string{
		"sthalbert/longue-vue-collector": {"0.25", "0.26", "0.27"},
	}}

	resolver := &mirrorresolve.HTTPResolver{
		Lookup: imageversions.NewStoreMirrorLookup(store),
		Client: annoSrv.Client(),
		Scheme: "http",
	}
	enricher := imageversions.NewEnricherWithResolver(store, lister, resolver, time.Hour, nil)
	enricher.RunTick(context.Background())

	// Assert image_versions row at the resolved origin.
	if len(store.upserted) != 1 {
		t.Fatalf("expected 1 image_versions upsert, got %d", len(store.upserted))
	}
	iv := store.upserted[0]
	if iv.ImageRepo != "ghcr.io/sthalbert/longue-vue-collector" {
		t.Errorf("image_repo: want ghcr.io/sthalbert/longue-vue-collector, got %q", iv.ImageRepo)
	}
	if iv.LatestTag == nil || *iv.LatestTag != "0.27" {
		t.Errorf("latest_tag: want 0.27, got %v", iv.LatestTag)
	}

	// Assert image_origin_resolutions row keyed on the replica-side repo.
	if len(store.originUpserts) != 1 {
		t.Fatalf("expected 1 origin resolution, got %d", len(store.originUpserts))
	}
	or := store.originUpserts[0]
	if or.MirrorImageRepo != "localreg.example.com/containers/sthalbert/longue-vue-collector" {
		t.Errorf("mirror_image_repo: %q", or.MirrorImageRepo)
	}
	if or.OriginImageRepo == nil || *or.OriginImageRepo != "ghcr.io/sthalbert/longue-vue-collector" {
		t.Errorf("origin_image_repo: want ghcr.io/sthalbert/longue-vue-collector, got %v", or.OriginImageRepo)
	}
	if or.LastError != nil {
		t.Errorf("last_error should be nil on success, got %v", or.LastError)
	}
}
