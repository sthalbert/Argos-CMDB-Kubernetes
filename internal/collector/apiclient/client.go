// Package apiclient implements collector.CmdbStore over the longue-vue REST API.
// It is the write-path for the push-mode collector (ADR-0009): every store
// method maps to one HTTP call against a remote longue-vue instance.
package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/longue-vue/internal/api"
)

// Sentinel errors for the HTTP-backed store.
var (
	errNoCACerts        = errors.New("CA cert file contains no valid certificates")
	errHTTPRequest      = errors.New("HTTP request error")
	errMaxRetries       = errors.New("max retries exceeded")
	errBadTransportType = errors.New("unexpected default transport type")
)

// Config carries the knobs for building an HTTP-backed store.
type Config struct {
	// ServerURL is the longue-vue base URL, e.g. "https://longue-vue.internal:8080"
	// or "https://gw:443/lv". A trailing path is prepended to every
	// request so gateway path-prefix rewrite works transparently.
	ServerURL string

	// Token is the bearer token (PAT) injected into every request.
	Token string

	// CACert is the path to a PEM-encoded CA bundle for server TLS
	// verification. Empty uses the system pool.
	CACert string

	// ClientCert and ClientKey are paths to a PEM-encoded client
	// certificate and key for mTLS. Both must be set or both empty.
	ClientCert string
	ClientKey  string

	// ExtraHeaders are injected into every outbound request. Typical
	// use: gateway routing headers (X-Tenant-Id, X-Route-Key).
	ExtraHeaders map[string]string
}

// Store implements collector.CmdbStore by calling the longue-vue REST API.
type Store struct {
	client       *http.Client
	baseURL      string // scheme + host + optional path prefix, no trailing slash
	token        string
	extraHeaders map[string]string
}

// NewStore builds an HTTP-backed store from cfg.
//
//nolint:gocritic // hugeParam: keeping value receiver for backward compatibility with external callers.
func NewStore(cfg Config) (*Store, error) {
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	baseURL := strings.TrimRight(u.String(), "/")

	transport, err := buildTransport(&cfg)
	if err != nil {
		return nil, err
	}

	return &Store{
		client:       &http.Client{Transport: transport, Timeout: 30 * time.Second},
		baseURL:      baseURL,
		token:        cfg.Token,
		extraHeaders: cfg.ExtraHeaders,
	}, nil
}

// buildTransport constructs an http.Transport with the TLS settings from cfg.
func buildTransport(cfg *Config) (*http.Transport, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errBadTransportType
	}
	transport := defaultTransport.Clone()

	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errNoCACerts
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.RootCAs = pool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}

	return transport, nil
}

// ── collector.CmdbStore implementation ──────────────────────────────

// EnsureCluster registers a cluster in the CMDB if no row with the same name
// exists, or returns the existing row unchanged when one does. The returned
// bool is true when a new row was inserted (server response 201), false when
// an existing row was returned (server response 200).
//
// EnsureCluster is the only cluster-bootstrap entry point on the apiclient
// store: a push-mode collector behind the strict-write-only DMZ ingest
// gateway (ADR-0016) cannot reach GET /v1/clusters, so the previous
// GET-then-POST pattern is replaced by a single idempotent POST.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface
func (s *Store) EnsureCluster(ctx context.Context, in api.ClusterCreate) (api.Cluster, bool, error) {
	var out api.Cluster
	status, err := s.doJSONStatus(ctx, http.MethodPost, "/v1/clusters", in, &out)
	if err != nil {
		return api.Cluster{}, false, fmt.Errorf("ensure cluster: %w", err)
	}
	return out, status == http.StatusCreated, nil
}

// UpdateCluster applies a partial update to the cluster identified by id.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	var out api.Cluster
	if err := s.doJSON(ctx, http.MethodPatch, "/v1/clusters/"+id.String(), in, &out); err != nil {
		return api.Cluster{}, fmt.Errorf("update cluster: %w", err)
	}
	return out, nil
}

// UpsertNode creates or updates a node record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	var out api.Node
	if err := s.doJSON(ctx, http.MethodPost, "/v1/nodes", in, &out); err != nil {
		return api.Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return out, nil
}

// DeleteNodesNotIn removes nodes not in the keepNames list for the given cluster.
func (s *Store) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/nodes/reconcile", clusterID, keepNames)
}

// UpsertNamespace creates or updates a namespace record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	var out api.Namespace
	if err := s.doJSON(ctx, http.MethodPost, "/v1/namespaces", in, &out); err != nil {
		return api.Namespace{}, fmt.Errorf("upsert namespace: %w", err)
	}
	return out, nil
}

// DeleteNamespacesNotIn removes namespaces not in the keepNames list for the given cluster.
func (s *Store) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/namespaces/reconcile", clusterID, keepNames)
}

// UpsertPod creates or updates a pod record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, error) {
	var out api.Pod
	if err := s.doJSON(ctx, http.MethodPost, "/v1/pods", in, &out); err != nil {
		return api.Pod{}, fmt.Errorf("upsert pod: %w", err)
	}
	return out, nil
}

// DeletePodsNotIn removes pods not in the keepNames list for the given namespace.
func (s *Store) DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/pods/reconcile", namespaceID, keepNames)
}

// UpsertWorkload creates or updates a workload record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error) {
	var out api.Workload
	if err := s.doJSON(ctx, http.MethodPost, "/v1/workloads", in, &out); err != nil {
		return api.Workload{}, fmt.Errorf("upsert workload: %w", err)
	}
	return out, nil
}

// DeleteWorkloadsNotIn removes workloads not in the keep lists for the given namespace.
func (s *Store) DeleteWorkloadsNotIn(ctx context.Context, namespaceID uuid.UUID, keepKinds, keepNames []string) (int64, error) {
	body := reconcileWorkloadsBody{
		NamespaceID: namespaceID,
		KeepKinds:   keepKinds,
		KeepNames:   keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, "/v1/workloads/reconcile", body, &result); err != nil {
		return 0, fmt.Errorf("reconcile workloads: %w", err)
	}
	return result.Deleted, nil
}

// UpsertService creates or updates a service record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, error) {
	var out api.Service
	if err := s.doJSON(ctx, http.MethodPost, "/v1/services", in, &out); err != nil {
		return api.Service{}, fmt.Errorf("upsert service: %w", err)
	}
	return out, nil
}

// DeleteServicesNotIn removes services not in the keepNames list for the given namespace.
func (s *Store) DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/services/reconcile", namespaceID, keepNames)
}

// UpsertIngress creates or updates an ingress record.
func (s *Store) UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error) {
	var out api.Ingress
	if err := s.doJSON(ctx, http.MethodPost, "/v1/ingresses", in, &out); err != nil {
		return api.Ingress{}, fmt.Errorf("upsert ingress: %w", err)
	}
	return out, nil
}

// DeleteIngressesNotIn removes ingresses not in the keepNames list for the given namespace.
func (s *Store) DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/ingresses/reconcile", namespaceID, keepNames)
}

// UpsertPersistentVolume creates or updates a persistent volume record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error) {
	var out api.PersistentVolume
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumes", in, &out); err != nil {
		return api.PersistentVolume{}, fmt.Errorf("upsert persistent volume: %w", err)
	}
	return out, nil
}

// DeletePersistentVolumesNotIn removes PVs not in the keepNames list for the given cluster.
func (s *Store) DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/persistentvolumes/reconcile", clusterID, keepNames)
}

// UpsertPersistentVolumeClaim creates or updates a PVC record.
//
//nolint:gocritic // hugeParam: signature matches CmdbStore interface.
func (s *Store) UpsertPersistentVolumeClaim(ctx context.Context, in api.PersistentVolumeClaimCreate) (api.PersistentVolumeClaim, error) {
	var out api.PersistentVolumeClaim
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumeclaims", in, &out); err != nil {
		return api.PersistentVolumeClaim{}, fmt.Errorf("upsert persistent volume claim: %w", err)
	}
	return out, nil
}

// DeletePersistentVolumeClaimsNotIn removes PVCs not in the keepNames list for the given namespace.
func (s *Store) DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/persistentvolumeclaims/reconcile", namespaceID, keepNames)
}

// ── HTTP helpers ────────────────────────────────────────────────────

// reconcile body types -- lightweight JSON carriers matching the OpenAPI
// schemas without importing the generated types (avoids a circular dep).

type reconcileClusterScopedBody struct {
	ClusterID uuid.UUID `json:"cluster_id"`
	KeepNames []string  `json:"keep_names"`
}

type reconcileNamespaceScopedBody struct {
	NamespaceID uuid.UUID `json:"namespace_id"`
	KeepNames   []string  `json:"keep_names"`
}

type reconcileWorkloadsBody struct {
	NamespaceID uuid.UUID `json:"namespace_id"`
	KeepKinds   []string  `json:"keep_kinds"`
	KeepNames   []string  `json:"keep_names"`
}

type reconcileResultBody struct {
	Deleted int64 `json:"deleted"`
}

func (s *Store) reconcileClusterScoped(ctx context.Context, path string, clusterID uuid.UUID, keepNames []string) (int64, error) {
	body := reconcileClusterScopedBody{
		ClusterID: clusterID,
		KeepNames: keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return 0, fmt.Errorf("reconcile %s: %w", path, err)
	}
	return result.Deleted, nil
}

func (s *Store) reconcileNamespaceScoped(ctx context.Context, path string, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	body := reconcileNamespaceScopedBody{
		NamespaceID: namespaceID,
		KeepNames:   keepNames,
	}
	var result reconcileResultBody
	if err := s.doJSON(ctx, http.MethodPost, path, body, &result); err != nil {
		return 0, fmt.Errorf("reconcile %s: %w", path, err)
	}
	return result.Deleted, nil
}

const (
	maxRetries    = 3
	retryBaseWait = 1 * time.Second
)

// doJSON sends an HTTP request with optional JSON body and decodes the
// JSON response into dst. Retries transient 5xx errors with exponential
// backoff; returns immediately on 401/403.
func (s *Store) doJSON(ctx context.Context, method, path string, body, dst any) error {
	_, err := s.doJSONStatus(ctx, method, path, body, dst)
	return err
}

// doJSONStatus is doJSON, but additionally returns the HTTP status code of
// the final (successful or terminal) response. Callers that need to
// distinguish between 200 and 201 (e.g. EnsureCluster) use this; everyone
// else uses doJSON.
func (s *Store) doJSONStatus(ctx context.Context, method, path string, body, dst any) (int, error) {
	var marshaledBody []byte
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		marshaledBody = buf
	}

	fullURL := s.baseURL + path

	var lastErr error
	var lastStatus int
	for attempt := range maxRetries {
		status, result, err := s.doOnce(ctx, method, fullURL, marshaledBody, dst)
		if err != nil {
			lastErr = err
			lastStatus = status
			if result == attemptDone || ctx.Err() != nil {
				return lastStatus, lastErr
			}
			backoff(ctx, attempt)
			continue
		}
		return status, nil
	}

	return lastStatus, fmt.Errorf("%w: %w", errMaxRetries, lastErr)
}

type attemptResult int

const (
	attemptDone  attemptResult = iota
	attemptRetry               // transient failure, retry
)

// doOnce performs a single HTTP round-trip for doJSON. The first return value
// is the HTTP status code of the response (0 when the request never reached
// the server, e.g. on a transport error).
func (s *Store) doOnce(
	ctx context.Context, method, fullURL string, marshaledBody []byte, dst any,
) (int, attemptResult, error) {
	var bodyReader io.Reader
	if marshaledBody != nil {
		bodyReader = bytes.NewReader(marshaledBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return 0, attemptDone, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if marshaledBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range s.extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, attemptRetry, fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}

	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return resp.StatusCode, attemptRetry, fmt.Errorf("%s %s: read response: %w", method, req.URL.Path, readErr)
	}

	result, err := s.handleResponse(method, req.URL.Path, resp.StatusCode, respBody, dst)
	return resp.StatusCode, result, err
}

// handleResponse interprets the HTTP status code and body returned by a
// single request attempt inside doJSON.
func (s *Store) handleResponse(
	method, path string, statusCode int, respBody []byte, dst any,
) (attemptResult, error) {
	if statusCode >= 200 && statusCode < 300 {
		return s.handleSuccess(method, path, respBody, dst)
	}
	return s.handleError(method, path, statusCode, respBody)
}

// handleSuccess decodes a 2xx response body into dst.
func (s *Store) handleSuccess(method, path string, respBody []byte, dst any) (attemptResult, error) {
	if dst != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, dst); err != nil {
			return attemptDone, fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
	}
	return attemptDone, nil
}

// handleError maps non-2xx HTTP statuses to the appropriate error and retry signal.
func (s *Store) handleError(method, path string, statusCode int, respBody []byte) (attemptResult, error) {
	httpErr := func() error {
		return fmt.Errorf("%s %s: %w: %d %s", method, path, errHTTPRequest, statusCode, truncate(string(respBody), 200))
	}

	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		slog.Error("apiclient: auth error (not retrying)",
			slog.String("method", method), slog.String("path", path),
			slog.Int("status", statusCode), slog.String("body", truncate(string(respBody), 500)))
		return attemptDone, httpErr()
	case statusCode == http.StatusNotFound:
		return attemptDone, api.ErrNotFound
	case statusCode == http.StatusConflict:
		return attemptDone, api.ErrConflict
	case statusCode >= 500:
		slog.Warn("apiclient: transient error, retrying",
			slog.String("method", method), slog.String("path", path),
			slog.Int("status", statusCode), slog.String("body", truncate(string(respBody), 500)))
		return attemptRetry, httpErr()
	default:
		return attemptDone, httpErr()
	}
}

// backoff sleeps with exponential delay, respecting context cancellation.
func backoff(ctx context.Context, attempt int) {
	wait := time.Duration(math.Pow(2, float64(attempt))) * retryBaseWait
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// truncate limits s to n bytes for log messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
