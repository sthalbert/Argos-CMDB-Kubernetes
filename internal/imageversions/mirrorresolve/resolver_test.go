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
			manifest: manifestFixture{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.opencontainers.image.base.name": "docker.io/library/debian:bookworm",
					"org.opencontainers.image.source":    "https://github.com/x/y",
				}},
			wantOrigin: "docker.io/library/debian:1.25",
		},
		{
			name: "source as registry ref",
			manifest: manifestFixture{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.opencontainers.image.source": "docker.io/library/nginx",
				}},
			wantOrigin: "docker.io/library/nginx:1.25",
		},
		{
			name: "source as github URL -> ambiguous",
			manifest: manifestFixture{SchemaVersion: 2, MediaType: "application/vnd.oci.image.manifest.v1+json",
				Annotations: map[string]string{
					"org.opencontainers.image.source": "https://github.com/x/y",
				}},
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
	var auth *httpAuthErr
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
