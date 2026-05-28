package mirrorresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// ErrChainTooDeep indicates a replica mirror points at another replica.
	// Only one hop is supported.
	ErrChainTooDeep = errors.New("mirrorresolve: replica chain too deep")
	// ErrReplicaTargetMissing indicates a replica's upstream hostname has
	// no matching mirror row.
	ErrReplicaTargetMissing = errors.New("mirrorresolve: replica target missing")
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
	Hostname               string
	PathPrefix             string
	ReplicatesFromHostname string // empty = annotation mirror; non-empty = replica
	AuthUsername           string
	// TokenSource returns the plaintext token at call time. May return ""
	// for anonymous mirrors. Called at most once per Resolve.
	TokenSource func(ctx context.Context) (string, error)
}

// MirrorLookup finds the longest-prefix mirror row matching (hostname,
// imagePath). Returns (zero, false, nil) when no mirror row applies.
type MirrorLookup interface {
	FindMirror(ctx context.Context, hostname, imagePath string) (MirrorRow, bool, error)
}

// OriginLookup finds a manually-configured public origin registry for a bare
// image name (path without the mirror's PathPrefix). Returns (registry, true)
// when a mapping exists, ("", false) when none does.
type OriginLookup interface {
	FindOrigin(ctx context.Context, bareName string) (registry string, ok bool, err error)
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
	Origins OriginLookup
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

// Resolve implements Resolver.
//
//nolint:gocyclo // sequential pipeline: split → mirror lookup → replica swap → manual mapping → OCI fetch; flat is clearer than factored
func (h *HTTPResolver) Resolve(ctx context.Context, ref string) (string, error) {
	started := h.now()

	hostname, imagePath, tag, err := splitRef(ref)
	if err != nil {
		// Ref that lacks the host/path shape (bare digest like
		// sha256:abc…, Docker Hub library shortcut like "nginx" or
		// "debian:latest", etc.) is by definition not a mirror ref.
		// Pass it through unchanged so the public-registry enricher
		// path can parse and route it normally — refusing here would
		// drop the image from version tracking entirely.
		h.observe("passthrough", started)
		//nolint:nilerr // intentional: unparseable refs are passed through to downstream parsing
		return ref, nil
	}

	// ADR-0031: the operator-curated mapping table is consulted first.
	// We try FindOrigin on the full imagePath, then strip one leading
	// segment and retry, until a mapping matches or no slash remains.
	// First hit wins and short-circuits the mirror lookup. Store errors
	// are logged and fall through (fail-open) to preserve resolver
	// availability when the mappings table is transiently unreachable.
	if h.Origins != nil {
		if rewritten, matched := h.tryManualMapping(ctx, imagePath, tag); matched {
			h.observe("ok_manual", started)
			return rewritten, nil
		}
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

	// Replica mirror: one-hop hostname rewrite, then re-lookup.
	// The path is identical on both registries (hostname-swap only); we
	// only swap the hostname before fetching annotations from the upstream
	// annotation mirror.
	if row.ReplicatesFromHostname != "" {
		hostname = row.ReplicatesFromHostname
		upstream, upOK, upErr := h.Lookup.FindMirror(ctx, hostname, imagePath)
		if upErr != nil {
			h.observe("lookup_error", started)
			return "", fmt.Errorf("mirror upstream lookup: %w", upErr)
		}
		if !upOK {
			h.observe("replica_target_missing", started)
			return "", ErrReplicaTargetMissing
		}
		if upstream.ReplicatesFromHostname != "" {
			h.observe("chain_too_deep", started)
			return "", ErrChainTooDeep
		}
		row = upstream
	}

	// Manual origin mapping: if a curator has recorded the public registry
	// for this image, return it immediately without an OCI round-trip.
	if h.Origins != nil {
		bareName := strings.TrimPrefix(imagePath, row.PathPrefix)
		if reg, ok, oErr := h.Origins.FindOrigin(ctx, bareName); oErr == nil && ok {
			var ref string
			if tag != "" {
				ref = reg + "/" + bareName + ":" + tag
			} else {
				ref = reg + "/" + bareName
			}
			h.observe("ok_manual", started)
			return ref, nil
		}
		// Miss or error: fall through to OCI annotation path.
	}

	origin, result, err := h.resolveFromMirror(ctx, row, imagePath, tag)
	h.observe(result, started)
	return origin, err
}

// tryManualMapping implements the ADR-0031 pre-mirror lookup. It returns
// the rewritten ref and matched=true on the first FindOrigin hit while
// progressively stripping leading segments from imagePath. On any
// FindOrigin error it logs once and returns matched=false so the caller
// falls through to the existing mirror/OCI flow.
func (h *HTTPResolver) tryManualMapping(ctx context.Context, imagePath, tag string) (rewritten string, matched bool) {
	candidate := imagePath
	for {
		reg, ok, err := h.Origins.FindOrigin(ctx, candidate)
		if err != nil {
			slog.Warn("mirrorresolve: manual mapping lookup failed",
				slog.String("candidate", candidate),
				slog.String("err", err.Error()))
			return "", false
		}
		if ok {
			if tag != "" {
				return reg + "/" + candidate + ":" + tag, true
			}
			return reg + "/" + candidate, true
		}
		// Suffix-match is intentional (ADR-0031): a mapping for "grafana/loki"
		// rewrites any ref ending with that path. Curate the table accordingly.
		i := strings.Index(candidate, "/")
		if i < 0 {
			return "", false
		}
		candidate = candidate[i+1:]
	}
}

// resolveFromMirror fetches the manifest from the mirror, applies the
// annotation-extraction priority (base.name → source → config Labels), and
// returns the resolved public origin together with the metric label to
// record. Splitting this out keeps Resolve's cyclomatic complexity in check
// and makes the fetch/extract pipeline easier to test independently.
func (h *HTTPResolver) resolveFromMirror(ctx context.Context, row MirrorRow, imagePath, tag string) (origin, result string, err error) {
	manifest, err := h.fetchManifest(ctx, row, imagePath, tag)
	if err != nil {
		if isAuthErr(err) {
			return "", "auth_error", err
		}
		return "", "fetch_error", err
	}

	annotations := manifest.Annotations
	if len(annotations) == 0 && len(manifest.Manifests) > 0 {
		annotations = manifest.Manifests[0].Annotations
	}

	if origin, ok := extractOrigin(annotations, tag); ok {
		return origin, "ok", nil
	}

	if manifest.Config.Digest != "" {
		if cfg, cfgErr := h.fetchConfig(ctx, row, imagePath, manifest.Config.Digest); cfgErr == nil {
			if origin, ok := extractOrigin(cfg.Config.Labels, tag); ok {
				return origin, "ok", nil
			}
		}
	}

	if _, present := annotations[annSource]; present {
		return "", "ambiguous_annotation", ErrAmbiguousAnnotation
	}
	return "", "missing_annotation", ErrNoOriginAnnotation
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
