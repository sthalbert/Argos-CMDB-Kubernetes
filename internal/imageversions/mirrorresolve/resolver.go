package mirrorresolve

import (
	"context"
	"errors"
	"net/http"
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
)

// MirrorRow is the subset of api.ImageRegistry the resolver needs.
// Keeping this narrow makes the package easy to test in isolation.
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

// Resolver resolves a mirrored ref to its public origin.
//
// Passthrough contract: if the ref's hostname does not match any configured
// mirror row, Resolve returns (ref, nil) — the caller can treat the value
// as the ordinary public ref.
type Resolver interface {
	Resolve(ctx context.Context, ref string) (origin string, err error)
}

// Observer receives one event per Resolve call. Implementations should be
// goroutine-safe. A nil Observer is allowed; the resolver no-ops then.
type Observer interface {
	ObserveResolve(result string, d time.Duration)
}

// HTTPResolver implements Resolver against an OCI distribution v2 endpoint.
// Zero value is not usable — Lookup must be set.
type HTTPResolver struct {
	Lookup  MirrorLookup
	Client  *http.Client     // defaults to a 10s-timeout client when nil
	Metrics Observer         // optional
	Scheme  string           // "https" by default; "http" for httptest
	Now     func() time.Time // optional; defaults to time.Now
}

// Resolve is the entry point. Implemented in resolve.go (added in Task 7).
// The skeleton commit only declares the type and dependencies.
//
// (No body yet; T6 writes failing tests; T7 implements.)
func (h *HTTPResolver) Resolve(ctx context.Context, ref string) (string, error) {
	return "", errors.New("mirrorresolve: not implemented")
}
