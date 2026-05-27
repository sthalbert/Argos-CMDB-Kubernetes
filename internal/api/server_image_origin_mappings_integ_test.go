package api

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
)

// storeMirrorLookupForTest adapts an api.Store into mirrorresolve.MirrorLookup.
// Defined here to avoid the api→imageversions→api import cycle.
type storeMirrorLookupForTest struct{ s Store }

func (l storeMirrorLookupForTest) FindMirror(ctx context.Context, hostname, imagePath string) (mirrorresolve.MirrorRow, bool, error) {
	row, err := l.s.FindMirrorForRef(ctx, hostname, imagePath)
	if errors.Is(err, ErrNotFound) {
		return mirrorresolve.MirrorRow{}, false, nil
	}
	if err != nil {
		return mirrorresolve.MirrorRow{}, false, fmt.Errorf("find mirror: %w", err)
	}
	user := ""
	if row.AuthUsername != nil {
		user = *row.AuthUsername
	}
	return mirrorresolve.MirrorRow{
		Hostname:     row.Hostname,
		PathPrefix:   row.PathPrefix,
		AuthUsername: user,
		TokenSource: func(ctx context.Context) (string, error) {
			return l.s.GetMirrorAuthToken(ctx, row.Hostname, row.PathPrefix)
		},
	}, true, nil
}

// storeOriginLookupForTest adapts an api.Store into mirrorresolve.OriginLookup.
type storeOriginLookupForTest struct{ s Store }

func (l storeOriginLookupForTest) FindOrigin(ctx context.Context, bareName string) (registry string, ok bool, err error) {
	reg, findErr := l.s.FindImageOrigin(ctx, bareName)
	if errors.Is(findErr, ErrNotFound) {
		return "", false, nil
	}
	if findErr != nil {
		return "", false, fmt.Errorf("find origin: %w", findErr)
	}
	return reg, true, nil
}

// TestResolver_ManualMapping_EndToEnd verifies that, given a memStore
// with a mirror row and an origin mapping, the resolver returns the
// expected public ref WITHOUT hitting any HTTP server (Client is nil).
func TestResolver_ManualMapping_EndToEnd(t *testing.T) {
	resetMemOriginMappings()
	srv := newTestServer(t)
	store := srv.store

	// Seed mirror row.
	_, err := store.CreateImageRegistry(context.Background(), ImageRegistryUpsert{
		Hostname:        "registry.zex-integ.internal",
		PathPrefix:      "containers/",
		RateLimitPerSec: 5,
		IsMirror:        true,
	})
	if err != nil {
		t.Fatalf("seed mirror: %v", err)
	}

	// Seed mapping.
	_, err = store.CreateImageOriginMapping(context.Background(),
		ImageOriginMappingCreate{
			ImageName:      "grafana/alloy",
			PublicRegistry: "docker.io",
		}, "test")
	if err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	// Build a resolver wired to the same store; no HTTP client.
	r := &mirrorresolve.HTTPResolver{
		Lookup:  storeMirrorLookupForTest{s: store},
		Origins: storeOriginLookupForTest{s: store},
	}
	got, err := r.Resolve(context.Background(),
		"registry.zex-integ.internal/containers/grafana/alloy:v1.16.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "docker.io/grafana/alloy:v1.16.0" {
		t.Fatalf("want docker.io/grafana/alloy:v1.16.0, got %q", got)
	}
}
