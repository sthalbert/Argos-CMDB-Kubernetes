package mirrorresolve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeLookup always returns the configured row.
type fakeLookup struct {
	row MirrorRow
	ok  bool
}

//nolint:gocritic // method-on-map type returns interface tuple; renaming receiver hurts readability // f is a test fixture; receiver copy is acceptable here
func (f fakeLookup) FindMirror(_ context.Context, _, _ string) (MirrorRow, bool, error) {
	return f.row, f.ok, nil
}

type manifestFixture struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

func TestHTTPResolver_Resolve(t *testing.T) {
	cases := []struct {
		name       string
		manifest   manifestFixture
		wantOrigin string
		wantErr    error
	}{
		{
			name: "base.name preferred",
			manifest: manifestFixture{
				SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.opencontainers.image.base.name": "docker.io/library/debian:bookworm",
					annSource:                            "https://github.com/x/y",
				},
			},
			wantOrigin: "docker.io/library/debian:1.25",
		},
		{
			name: "source as registry ref",
			manifest: manifestFixture{
				SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					annSource: "docker.io/library/nginx",
				},
			},
			wantOrigin: "docker.io/library/nginx:1.25",
		},
		{
			name: "source as github URL -> ambiguous",
			manifest: manifestFixture{
				SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					annSource: "https://github.com/x/y",
				},
			},
			wantErr: ErrAmbiguousAnnotation,
		},
		{
			name:     "no annotations",
			manifest: manifestFixture{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json"},
			wantErr:  ErrNoOriginAnnotation,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/v2/container/library/nginx/manifests/") {
					t.Fatalf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				_ = json.NewEncoder(w).Encode(tc.manifest)
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)

			r := &HTTPResolver{
				Lookup: fakeLookup{row: MirrorRow{Hostname: u.Host, PathPrefix: "container/"}, ok: true},
				Client: srv.Client(),
				Scheme: "http",
			}
			origin, err := r.Resolve(context.Background(), u.Host+"/container/library/nginx:1.25")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v want=%v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if origin != tc.wantOrigin {
				t.Fatalf("origin=%q want=%q", origin, tc.wantOrigin)
			}
		})
	}
}

func TestHTTPResolver_Passthrough_NoMirror(t *testing.T) {
	r := &HTTPResolver{Lookup: fakeLookup{ok: false}}
	got, err := r.Resolve(context.Background(), "docker.io/library/nginx:1.25")
	if err != nil {
		t.Fatal(err)
	}
	if got != "docker.io/library/nginx:1.25" {
		t.Fatalf("passthrough=%q", got)
	}
}

// TestHTTPResolver_Passthrough_UnparseableRef covers the regression where
// refs that lack the host/path shape (bare digests, Docker Hub library
// shortcuts) were being WARN-logged and dropped from the enricher entirely
// instead of flowing through to ParseImageRef. See ADR-0026 follow-up.
func TestHTTPResolver_Passthrough_UnparseableRef(t *testing.T) {
	cases := []struct{ name, ref string }{
		{"bare digest", "sha256:e792110c581423bf8ec4d8476bca3658810a24a9437f6f8ff24cdeafd9521dbd"},
		{"library shortcut with tag", "nginx:1.25"},
		{"library shortcut no tag", "busybox"},
		{"namespaced library", "python:3.11-slim"},
	}
	r := &HTTPResolver{Lookup: fakeLookup{ok: false}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.Resolve(context.Background(), tc.ref)
			if err != nil {
				t.Fatalf("expected passthrough, got error: %v", err)
			}
			if got != tc.ref {
				t.Fatalf("got=%q, want unchanged %q", got, tc.ref)
			}
		})
	}
}

func TestHTTPResolver_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	r := &HTTPResolver{
		Lookup: fakeLookup{row: MirrorRow{Hostname: u.Host, PathPrefix: "container/"}, ok: true},
		Client: srv.Client(),
		Scheme: "http",
	}
	_, err := r.Resolve(context.Background(), u.Host+"/container/library/nginx:1.25")
	if err == nil {
		t.Fatal("expected error")
	}
	var auth *httpAuthError
	if !errors.As(err, &auth) {
		t.Fatalf("not auth error: %v", err)
	}
}

func TestHTTPResolver_DefaultTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			t.Fatalf("expected manifests/latest, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(manifestFixture{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			Annotations: map[string]string{
				"org.opencontainers.image.base.name": "docker.io/library/nginx",
			},
		})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	r := &HTTPResolver{
		Lookup: fakeLookup{row: MirrorRow{Hostname: u.Host, PathPrefix: "container/"}, ok: true},
		Client: srv.Client(),
		Scheme: "http",
	}
	got, err := r.Resolve(context.Background(), u.Host+"/container/library/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if got != "docker.io/library/nginx:latest" {
		t.Fatalf("got=%q", got)
	}
}

// stubLookup returns different rows per hostname so we can simulate
// the local-registry → global-mirror rewrite hop.
type stubLookup struct {
	rows map[string]MirrorRow
}

func (s stubLookup) FindMirror(_ context.Context, hostname, _ string) (MirrorRow, bool, error) {
	r, ok := s.rows[hostname]
	return r, ok, nil
}

func TestHTTPResolver_ReplicaChain_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Annotation mirror serves the manifest with base.name pointing at ghcr.io.
		if !strings.HasPrefix(r.URL.Path, "/v2/containers/sthalbert/longue-vue-collector/manifests/") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_ = json.NewEncoder(w).Encode(manifestFixture{
			SchemaVersion: 2,
			MediaType:     "application/vnd.oci.image.manifest.v1+json",
			Annotations: map[string]string{
				annBaseName: "ghcr.io/sthalbert/longue-vue-collector:0.26",
			},
		})
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	res := &HTTPResolver{
		Lookup: stubLookup{rows: map[string]MirrorRow{
			"local.example.com": {
				Hostname: "local.example.com", PathPrefix: "containers/",
				ReplicatesFromHostname: u.Host,
			},
			u.Host: {Hostname: u.Host, PathPrefix: "containers/"},
		}},
		Scheme: "http",
	}

	got, err := res.Resolve(context.Background(),
		"local.example.com/containers/sthalbert/longue-vue-collector:0.26")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "ghcr.io/sthalbert/longue-vue-collector:0.26"
	if got != want {
		t.Errorf("origin: want %q, got %q", want, got)
	}
}

func TestHTTPResolver_ReplicaChain_TargetMissing(t *testing.T) {
	res := &HTTPResolver{
		Lookup: stubLookup{rows: map[string]MirrorRow{
			"local.example.com": {
				Hostname: "local.example.com", PathPrefix: "containers/",
				ReplicatesFromHostname: "mirror.example.com",
			},
			// note: mirror.example.com NOT in the lookup
		}},
		Scheme: "http",
	}
	_, err := res.Resolve(context.Background(),
		"local.example.com/containers/sthalbert/x:1.0")
	if !errors.Is(err, ErrReplicaTargetMissing) {
		t.Fatalf("want ErrReplicaTargetMissing, got %v", err)
	}
}

// fakeMirrorLookup is a map-keyed MirrorLookup keyed by key(hostname, imagePath).
type fakeMirrorLookup map[string]MirrorRow

func key(hostname, imagePath string) string { return hostname + "/" + imagePath }

func (f fakeMirrorLookup) FindMirror(_ context.Context, hostname, imagePath string) (row MirrorRow, ok bool, err error) {
	r, found := f[key(hostname, imagePath)]
	return r, found, nil
}

// fakeOriginLookup is a map from bare image name to registry.
type fakeOriginLookup map[string]string

func (f fakeOriginLookup) FindOrigin(_ context.Context, bareName string) (publicRegistry string, ok bool, err error) {
	reg, found := f[bareName]
	return reg, found, nil
}

func TestResolve_ManualMappingHit(t *testing.T) {
	lookup := fakeMirrorLookup{
		key("registry.zex-integ.internal", "containers/grafana/alloy"): MirrorRow{
			Hostname:   "registry.zex-integ.internal",
			PathPrefix: "containers/",
		},
	}
	r := &HTTPResolver{
		Lookup:  lookup,
		Origins: fakeOriginLookup{"grafana/alloy": "docker.io"},
	}
	got, err := r.Resolve(context.Background(),
		"registry.zex-integ.internal/containers/grafana/alloy:v1.16.0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := "docker.io/grafana/alloy:v1.16.0"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

// When the mirror lookup misses entirely, the OriginLookup MUST NOT be
// consulted (we only know it's a known image once a mirror row matches).
// This guards against accidentally short-circuiting passthrough refs.
func TestResolve_NoMirrorRow_StillPassthrough(t *testing.T) {
	r := &HTTPResolver{
		Lookup:  fakeMirrorLookup{}, // empty: no mirror row
		Origins: fakeOriginLookup{"grafana/alloy": "docker.io"},
	}
	origin, err := r.Resolve(context.Background(), "nginx:latest")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if origin != "nginx:latest" {
		t.Fatalf("want passthrough nginx:latest, got %q", origin)
	}
}

// Mirror row hits but mapping misses → resolver continues to the OCI
// fetch path. We verify the early-return is NOT taken by passing an empty
// OriginLookup and a nil HTTP client: if the manual branch incorrectly
// returned, the test would pass with the wrong result; if it correctly
// falls through, the OCI fetch will fail (nil client) and surface as a
// fetch_error sentinel.
func TestResolve_ManualMappingMiss_FallsThrough(t *testing.T) {
	lookup := fakeMirrorLookup{
		key("registry.zex-integ.internal", "containers/grafana/alloy"): MirrorRow{
			Hostname:   "registry.zex-integ.internal",
			PathPrefix: "containers/",
		},
	}
	r := &HTTPResolver{
		Lookup:  lookup,
		Origins: fakeOriginLookup{}, // empty: forces fall-through
	}
	_, err := r.Resolve(context.Background(),
		"registry.zex-integ.internal/containers/grafana/alloy:v1.16.0")
	if err == nil {
		t.Fatalf("expected fall-through to OCI path (which fails with nil client), got nil err")
	}
}

func TestHTTPResolver_ReplicaChain_TooDeep(t *testing.T) {
	res := &HTTPResolver{
		Lookup: stubLookup{rows: map[string]MirrorRow{
			"local.example.com": {
				Hostname: "local.example.com", PathPrefix: "containers/",
				ReplicatesFromHostname: "midway.example.com",
			},
			"midway.example.com": {
				Hostname: "midway.example.com", PathPrefix: "containers/",
				ReplicatesFromHostname: "mirror.example.com",
			},
			"mirror.example.com": {Hostname: "mirror.example.com", PathPrefix: "containers/"},
		}},
		Scheme: "http",
	}
	_, err := res.Resolve(context.Background(),
		"local.example.com/containers/sthalbert/x:1.0")
	if !errors.Is(err, ErrChainTooDeep) {
		t.Fatalf("want ErrChainTooDeep, got %v", err)
	}
}
