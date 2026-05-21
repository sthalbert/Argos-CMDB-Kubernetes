package mirrorresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sentinel errors returned by Resolve.
var (
	// ErrNoOriginAnnotation indicates the manifest was fetched successfully
	// but no usable annotation was found.
	ErrNoOriginAnnotation = errors.New("mirrorresolve: no usable origin annotation")
	// ErrAmbiguousAnnotation indicates the annotation exists but is not a
	// parseable registry reference (e.g. a GitHub source URL).
	ErrAmbiguousAnnotation = errors.New("mirrorresolve: annotation not a registry ref")
	// ErrInvalidRef indicates an unparseable image reference.
	ErrInvalidRef = errors.New("mirrorresolve: invalid ref")
	// ErrFetchManifest indicates a non-2xx response fetching a manifest.
	ErrFetchManifest = errors.New("mirrorresolve: fetch manifest")
	// ErrFetchConfig indicates a non-2xx response fetching a config blob.
	ErrFetchConfig = errors.New("mirrorresolve: fetch config")
)

const (
	annBaseName = "org.opencontainers.image.base.name"
	annSource   = "org.opencontainers.image.source"

	manifestAccept = "application/vnd.oci.image.manifest.v1+json," +
		"application/vnd.docker.distribution.manifest.v2+json," +
		"application/vnd.oci.image.index.v1+json," +
		"application/vnd.docker.distribution.manifest.list.v2+json"
)

// MirrorRow is the subset of api.ImageRegistry the resolver needs.
type MirrorRow struct {
	Hostname     string
	PathPrefix   string
	AuthUsername string
	// TokenSource returns the plaintext token at call time. May return ""
	// for anonymous mirrors. Called at most once per Resolve.
	TokenSource func(ctx context.Context) (string, error)
}

// MirrorLookup finds the longest-prefix mirror row matching (hostname,
// imagePath). Returns (zero, false, nil) when no mirror row applies.
type MirrorLookup interface {
	FindMirror(ctx context.Context, hostname, imagePath string) (MirrorRow, bool, error)
}

// Resolver resolves a mirrored ref to its public origin. When the ref does
// not match any mirror row, Resolve returns (ref, nil).
type Resolver interface {
	Resolve(ctx context.Context, ref string) (origin string, err error)
}

// Observer receives one event per Resolve call.
type Observer interface {
	ObserveResolve(result string, d time.Duration)
}

// HTTPResolver implements Resolver against an OCI distribution v2 endpoint.
type HTTPResolver struct {
	Lookup  MirrorLookup
	Client  *http.Client
	Metrics Observer
	Scheme  string // "https" by default; "http" for httptest
	Now     func() time.Time
}

type ociManifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Annotations   map[string]string `json:"annotations,omitempty"`
	Config        struct {
		Digest string `json:"digest"`
	} `json:"config"`
	Manifests []struct {
		Digest      string            `json:"digest"`
		Annotations map[string]string `json:"annotations,omitempty"`
	} `json:"manifests,omitempty"`
}

type imageConfig struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"config"`
}

type httpAuthError struct{ status int }

func (e *httpAuthError) Error() string { return fmt.Sprintf("auth error: HTTP %d", e.status) }

func isAuthErr(err error) bool {
	var a *httpAuthError
	return errors.As(err, &a)
}

func (h *HTTPResolver) httpClient() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (h *HTTPResolver) scheme() string {
	if h.Scheme != "" {
		return h.Scheme
	}
	return "https"
}

func (h *HTTPResolver) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *HTTPResolver) observe(result string, started time.Time) {
	if h.Metrics != nil {
		h.Metrics.ObserveResolve(result, h.now().Sub(started))
	}
}

// Resolve implements Resolver.
func (h *HTTPResolver) Resolve(ctx context.Context, ref string) (string, error) {
	started := h.now()

	hostname, imagePath, tag, err := splitRef(ref)
	if err != nil {
		h.observe("parse_error", started)
		return "", err
	}

	row, ok, err := h.Lookup.FindMirror(ctx, hostname, imagePath)
	if err != nil {
		h.observe("lookup_error", started)
		return "", fmt.Errorf("mirror lookup: %w", err)
	}
	if !ok {
		h.observe("passthrough", started)
		return ref, nil
	}

	manifest, err := h.fetchManifest(ctx, row, imagePath, tag)
	if err != nil {
		if isAuthErr(err) {
			h.observe("auth_error", started)
		} else {
			h.observe("fetch_error", started)
		}
		return "", err
	}

	annotations := manifest.Annotations
	if len(annotations) == 0 && len(manifest.Manifests) > 0 {
		annotations = manifest.Manifests[0].Annotations
	}

	if origin, ok := extractOrigin(annotations, tag); ok {
		h.observe("ok", started)
		return origin, nil
	}

	// Fallback: config blob Labels.
	if manifest.Config.Digest != "" {
		cfg, err := h.fetchConfig(ctx, row, imagePath, manifest.Config.Digest)
		if err == nil {
			if origin, ok := extractOrigin(cfg.Config.Labels, tag); ok {
				h.observe("ok", started)
				return origin, nil
			}
		}
	}

	if _, present := annotations[annSource]; present {
		h.observe("ambiguous_annotation", started)
		return "", ErrAmbiguousAnnotation
	}
	h.observe("missing_annotation", started)
	return "", ErrNoOriginAnnotation
}

func extractOrigin(m map[string]string, originalTag string) (string, bool) {
	if v := strings.TrimSpace(m[annBaseName]); v != "" {
		return stripAndRetagToOriginal(v, originalTag), true
	}
	if v := strings.TrimSpace(m[annSource]); v != "" {
		if looksLikeRegistryRef(v) {
			return stripAndRetagToOriginal(v, originalTag), true
		}
	}
	return "", false
}

// stripAndRetagToOriginal removes any tag/digest on v and re-attaches the
// caller's tag so the enricher computes freshness for the right lineage.
func stripAndRetagToOriginal(v, originalTag string) string {
	if i := strings.Index(v, "@"); i > 0 {
		v = v[:i]
	}
	if slash := strings.LastIndex(v, "/"); slash >= 0 {
		if colon := strings.LastIndex(v[slash:], ":"); colon > 0 {
			v = v[:slash+colon]
		}
	}
	if originalTag != "" {
		return v + ":" + originalTag
	}
	return v
}

// looksLikeRegistryRef rejects schemes and bare-domain code-host URLs.
func looksLikeRegistryRef(v string) bool {
	if strings.Contains(v, "://") {
		return false
	}
	if !strings.Contains(v, "/") {
		return false
	}
	for _, bad := range []string{"github.com/", "gitlab.com/", "bitbucket.org/"} {
		if strings.HasPrefix(v, bad) {
			return false
		}
	}
	return true
}

// splitRef parses "host[:port]/path[:tag|@digest]" into its components.
// The tag colon search is scoped to the segment after the last '/' so that
// host:port colons are not mistaken for tag separators.
func splitRef(ref string) (hostname, imagePath, tag string, err error) {
	slash := strings.Index(ref, "/")
	if slash < 0 {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	hostname = ref[:slash]
	rest := ref[slash+1:]
	if at := strings.Index(rest, "@"); at >= 0 {
		return hostname, rest[:at], "", nil
	}
	lastSlash := strings.LastIndex(rest, "/")
	tail := rest
	tailStart := 0
	if lastSlash >= 0 {
		tail = rest[lastSlash+1:]
		tailStart = lastSlash + 1
	}
	if colon := strings.LastIndex(tail, ":"); colon >= 0 {
		return hostname, rest[:tailStart+colon], rest[tailStart+colon+1:], nil
	}
	return hostname, rest, "latest", nil
}

func (h *HTTPResolver) fetchManifest(ctx context.Context, row MirrorRow, imagePath, tag string) (*ociManifest, error) {
	if tag == "" {
		tag = "latest"
	}
	url := h.scheme() + "://" + row.Hostname + "/v2/" + imagePath + "/manifests/" + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build manifest request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	if err := h.applyAuth(ctx, req, row); err != nil {
		return nil, err
	}
	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &httpAuthError{status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("%w: HTTP %d", ErrFetchManifest, resp.StatusCode)
	}
	var m ociManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &m, nil
}

func (h *HTTPResolver) fetchConfig(ctx context.Context, row MirrorRow, imagePath, digest string) (*imageConfig, error) {
	url := h.scheme() + "://" + row.Hostname + "/v2/" + imagePath + "/blobs/" + digest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build config request: %w", err)
	}
	if err := h.applyAuth(ctx, req, row); err != nil {
		return nil, err
	}
	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("%w: HTTP %d", ErrFetchConfig, resp.StatusCode)
	}
	var c imageConfig
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	return &c, nil
}

func (h *HTTPResolver) applyAuth(ctx context.Context, req *http.Request, row MirrorRow) error {
	if row.TokenSource == nil {
		return nil
	}
	tok, err := row.TokenSource(ctx)
	if err != nil {
		return err
	}
	if tok == "" {
		return nil
	}
	req.SetBasicAuth(row.AuthUsername, tok)
	return nil
}
