// Package apiclient implements collector.CmdbStore over the argosd REST API.
// It is the write-path for the push-mode collector (ADR-0009): every store
// method maps to one HTTP call against a remote argosd instance.
package apiclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

	"github.com/sthalbert/argos/internal/api"
)

// Config carries the knobs for building an HTTP-backed store.
type Config struct {
	// ServerURL is the argosd base URL, e.g. "https://argos.internal:8080"
	// or "https://gw:443/argos". A trailing path is prepended to every
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

// Store implements collector.CmdbStore by calling the argosd REST API.
type Store struct {
	client       *http.Client
	baseURL      string // scheme + host + optional path prefix, no trailing slash
	token        string
	extraHeaders map[string]string
}

// NewStore builds an HTTP-backed store from cfg.
func NewStore(cfg Config) (*Store, error) {
	u, err := url.Parse(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("parse server URL: %w", err)
	}
	baseURL := strings.TrimRight(u.String(), "/")

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA cert file contains no valid certificates")
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

	return &Store{
		client:       &http.Client{Transport: transport, Timeout: 30 * time.Second},
		baseURL:      baseURL,
		token:        cfg.Token,
		extraHeaders: cfg.ExtraHeaders,
	}, nil
}

// ── collector.CmdbStore implementation ──────────────────────────────

func (s *Store) GetClusterByName(ctx context.Context, name string) (api.Cluster, error) {
	path := "/v1/clusters?name=" + url.QueryEscape(name) + "&limit=1"
	var list api.ClusterList
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &list); err != nil {
		return api.Cluster{}, fmt.Errorf("get cluster by name: %w", err)
	}
	if len(list.Items) == 0 {
		return api.Cluster{}, api.ErrNotFound
	}
	return list.Items[0], nil
}

func (s *Store) UpdateCluster(ctx context.Context, id uuid.UUID, in api.ClusterUpdate) (api.Cluster, error) {
	var out api.Cluster
	if err := s.doJSON(ctx, http.MethodPatch, "/v1/clusters/"+id.String(), in, &out); err != nil {
		return api.Cluster{}, fmt.Errorf("update cluster: %w", err)
	}
	return out, nil
}

func (s *Store) UpsertNode(ctx context.Context, in api.NodeCreate) (api.Node, error) {
	var out api.Node
	if err := s.doJSON(ctx, http.MethodPost, "/v1/nodes", in, &out); err != nil {
		return api.Node{}, fmt.Errorf("upsert node: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteNodesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/nodes/reconcile", clusterID, keepNames)
}

func (s *Store) UpsertNamespace(ctx context.Context, in api.NamespaceCreate) (api.Namespace, error) {
	var out api.Namespace
	if err := s.doJSON(ctx, http.MethodPost, "/v1/namespaces", in, &out); err != nil {
		return api.Namespace{}, fmt.Errorf("upsert namespace: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteNamespacesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/namespaces/reconcile", clusterID, keepNames)
}

func (s *Store) UpsertPod(ctx context.Context, in api.PodCreate) (api.Pod, error) {
	var out api.Pod
	if err := s.doJSON(ctx, http.MethodPost, "/v1/pods", in, &out); err != nil {
		return api.Pod{}, fmt.Errorf("upsert pod: %w", err)
	}
	return out, nil
}

func (s *Store) DeletePodsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/pods/reconcile", namespaceID, keepNames)
}

func (s *Store) UpsertWorkload(ctx context.Context, in api.WorkloadCreate) (api.Workload, error) {
	var out api.Workload
	if err := s.doJSON(ctx, http.MethodPost, "/v1/workloads", in, &out); err != nil {
		return api.Workload{}, fmt.Errorf("upsert workload: %w", err)
	}
	return out, nil
}

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

func (s *Store) UpsertService(ctx context.Context, in api.ServiceCreate) (api.Service, error) {
	var out api.Service
	if err := s.doJSON(ctx, http.MethodPost, "/v1/services", in, &out); err != nil {
		return api.Service{}, fmt.Errorf("upsert service: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteServicesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/services/reconcile", namespaceID, keepNames)
}

func (s *Store) UpsertIngress(ctx context.Context, in api.IngressCreate) (api.Ingress, error) {
	var out api.Ingress
	if err := s.doJSON(ctx, http.MethodPost, "/v1/ingresses", in, &out); err != nil {
		return api.Ingress{}, fmt.Errorf("upsert ingress: %w", err)
	}
	return out, nil
}

func (s *Store) DeleteIngressesNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/ingresses/reconcile", namespaceID, keepNames)
}

func (s *Store) UpsertPersistentVolume(ctx context.Context, in api.PersistentVolumeCreate) (api.PersistentVolume, error) {
	var out api.PersistentVolume
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumes", in, &out); err != nil {
		return api.PersistentVolume{}, fmt.Errorf("upsert persistent volume: %w", err)
	}
	return out, nil
}

func (s *Store) DeletePersistentVolumesNotIn(ctx context.Context, clusterID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileClusterScoped(ctx, "/v1/persistentvolumes/reconcile", clusterID, keepNames)
}

func (s *Store) UpsertPersistentVolumeClaim(ctx context.Context, in api.PersistentVolumeClaimCreate) (api.PersistentVolumeClaim, error) {
	var out api.PersistentVolumeClaim
	if err := s.doJSON(ctx, http.MethodPost, "/v1/persistentvolumeclaims", in, &out); err != nil {
		return api.PersistentVolumeClaim{}, fmt.Errorf("upsert persistent volume claim: %w", err)
	}
	return out, nil
}

func (s *Store) DeletePersistentVolumeClaimsNotIn(ctx context.Context, namespaceID uuid.UUID, keepNames []string) (int64, error) {
	return s.reconcileNamespaceScoped(ctx, "/v1/persistentvolumeclaims/reconcile", namespaceID, keepNames)
}

// ── HTTP helpers ────────────────────────────────────────────────────

// reconcile body types — lightweight JSON carriers matching the OpenAPI
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
func (s *Store) doJSON(ctx context.Context, method, path string, body any, dst any) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	fullURL := s.baseURL + path

	var lastErr error
	for attempt := range maxRetries {
		// Reset reader for retries.
		if body != nil {
			buf, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(buf)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+s.token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range s.extraHeaders {
			req.Header.Set(k, v)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s %s: %w", method, path, err)
			if ctx.Err() != nil {
				return lastErr
			}
			backoff(ctx, attempt)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s %s: read response: %w", method, path, readErr)
			backoff(ctx, attempt)
			continue
		}

		// 2xx → success.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if dst != nil && len(respBody) > 0 {
				if err := json.Unmarshal(respBody, dst); err != nil {
					return fmt.Errorf("%s %s: decode response: %w", method, path, err)
				}
			}
			return nil
		}

		// 401/403 → auth error, no retry.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			slog.Error("apiclient: auth error (not retrying)", "method", method, "path", path,
				"status", resp.StatusCode, "body", truncate(string(respBody), 500))
			return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, truncate(string(respBody), 200))
		}

		// 404 → map to ErrNotFound for store contract compatibility.
		if resp.StatusCode == http.StatusNotFound {
			return api.ErrNotFound
		}

		// 409 → map to ErrConflict.
		if resp.StatusCode == http.StatusConflict {
			return api.ErrConflict
		}

		// 5xx → transient, retry.
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, truncate(string(respBody), 200))
			slog.Warn("apiclient: transient error, retrying",
				"method", method, "path", path, "status", resp.StatusCode,
				"attempt", attempt+1, "body", truncate(string(respBody), 500))
			backoff(ctx, attempt)
			continue
		}

		// Other 4xx → permanent error.
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, truncate(string(respBody), 200))
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
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
	return s[:n] + "…"
}
