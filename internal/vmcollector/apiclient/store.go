// Package apiclient implements the narrow collector-side store that
// argos-vm-collector uses to talk to argosd over HTTPS (ADR-0015).
// Mirrors the transport setup of internal/collector/apiclient — same
// CA / mTLS / extra-headers shape — but exposes only the methods the
// VM collector needs.
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

	"github.com/sthalbert/argos/internal/vmcollector/provider"
)

// Sentinel errors.
var (
	// ErrAlreadyKubeNode is returned by UpsertVirtualMachine when argosd
	// reports 409 — the VM is already inventoried as a Kubernetes node.
	ErrAlreadyKubeNode = errors.New("already_inventoried_as_kubernetes_node")
	// ErrNotRegistered is returned by FetchCredentialsByName when argosd
	// returns 404 (the cloud_account row does not exist yet).
	ErrNotRegistered = errors.New("cloud_account_not_registered")
	// ErrAccountDisabled is returned when argosd reports 403 — the
	// account has been disabled by an admin.
	ErrAccountDisabled = errors.New("cloud_account_disabled")
	errBadTransport    = errors.New("unexpected default transport type")
	errNoCACerts       = errors.New("CA cert file contains no valid certificates")
	errMaxRetries      = errors.New("max retries exceeded")
)

// Config carries the knobs for building the HTTP-backed store.
type Config struct {
	ServerURL    string
	Token        string
	CACert       string
	ClientCert   string
	ClientKey    string
	ExtraHeaders map[string]string
}

// Store is the HTTP-backed collector store.
type Store struct {
	client       *http.Client
	baseURL      string
	token        string
	extraHeaders map[string]string
}

// NewStore builds a Store from cfg.
//
//nolint:gocritic // hugeParam: stable signature
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

func buildTransport(cfg *Config) (*http.Transport, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errBadTransport
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
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport.TLSClientConfig.Certificates = []tls.Certificate{cert}
	}
	return transport, nil
}

// Credentials is the JSON shape returned by /v1/cloud-accounts/.../credentials.
type Credentials struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Region    string `json:"region"`
	Provider  string `json:"provider"`
}

// CloudAccount mirrors the relevant fields of api.CloudAccount used
// by the collector. Kept as its own type so we don't import the
// argosd internal/api package from the collector binary.
type CloudAccount struct {
	ID       uuid.UUID `json:"id"`
	Provider string    `json:"provider"`
	Name     string    `json:"name"`
	Region   string    `json:"region"`
	Status   string    `json:"status"`
}

// FetchCredentialsByName GETs /v1/cloud-accounts/by-name/{name}/credentials.
func (s *Store) FetchCredentialsByName(ctx context.Context, name string) (Credentials, error) {
	var out Credentials
	path := "/v1/cloud-accounts/by-name/" + url.PathEscape(name) + "/credentials"
	if err := s.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Credentials{}, err
	}
	return out, nil
}

// RegisterCloudAccount POSTs /v1/cloud-accounts to register the
// (provider, name, region) tuple. Idempotent.
func (s *Store) RegisterCloudAccount(ctx context.Context, providerName, name, region string) (CloudAccount, error) {
	body := map[string]string{
		"provider": providerName,
		"name":     name,
		"region":   region,
	}
	var out CloudAccount
	if err := s.doJSON(ctx, http.MethodPost, "/v1/cloud-accounts", body, &out); err != nil {
		return CloudAccount{}, err
	}
	return out, nil
}

// UpdateCloudAccountStatus PATCHes /v1/cloud-accounts/{id}/status.
func (s *Store) UpdateCloudAccountStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastErr *string) error {
	body := map[string]any{}
	if status != "" {
		body["status"] = status
	}
	if lastSeenAt != nil {
		body["last_seen_at"] = lastSeenAt.UTC().Format(time.RFC3339)
		now := time.Now().UTC().Format(time.RFC3339)
		body["last_error_at"] = now
	}
	if lastErr != nil {
		body["last_error"] = *lastErr
	}
	if err := s.doJSON(ctx, http.MethodPatch, "/v1/cloud-accounts/"+id.String()+"/status", body, nil); err != nil {
		return err
	}
	return nil
}

// upsertVMBody mirrors the argosd handler's vmUpsertReq so we don't
// import that type.
type upsertVMBody struct {
	CloudAccountID       uuid.UUID         `json:"cloud_account_id"`
	ProviderVMID         string            `json:"provider_vm_id"`
	Name                 string            `json:"name"`
	Role                 *string           `json:"role,omitempty"`
	PrivateIP            *string           `json:"private_ip,omitempty"`
	PublicIP             *string           `json:"public_ip,omitempty"`
	PrivateDNSName       *string           `json:"private_dns_name,omitempty"`
	VPCID                *string           `json:"vpc_id,omitempty"`
	SubnetID             *string           `json:"subnet_id,omitempty"`
	NICs                 json.RawMessage   `json:"nics,omitempty"`
	SecurityGroups       json.RawMessage   `json:"security_groups,omitempty"`
	InstanceType         *string           `json:"instance_type,omitempty"`
	Architecture         *string           `json:"architecture,omitempty"`
	Zone                 *string           `json:"zone,omitempty"`
	Region               *string           `json:"region,omitempty"`
	ImageID              *string           `json:"image_id,omitempty"`
	ImageName            *string           `json:"image_name,omitempty"`
	KeypairName          *string           `json:"keypair_name,omitempty"`
	BootMode             *string           `json:"boot_mode,omitempty"`
	ProviderAccountID    *string           `json:"provider_account_id,omitempty"`
	ProviderCreationDate *string           `json:"provider_creation_date,omitempty"`
	PowerState           string            `json:"power_state"`
	StateReason          *string           `json:"state_reason,omitempty"`
	Ready                bool              `json:"ready"`
	DeletionProtection   bool              `json:"deletion_protection"`
	CapacityCPU          *string           `json:"capacity_cpu,omitempty"`
	CapacityMemory       *string           `json:"capacity_memory,omitempty"`
	BlockDevices         json.RawMessage   `json:"block_devices,omitempty"`
	RootDeviceType       *string           `json:"root_device_type,omitempty"`
	RootDeviceName       *string           `json:"root_device_name,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
}

// UpsertVirtualMachine POSTs /v1/virtual-machines.
//
//nolint:gocyclo // straight-line mapping
func (s *Store) UpsertVirtualMachine(ctx context.Context, accountID uuid.UUID, vm provider.VM) error {
	body := upsertVMBody{
		CloudAccountID:     accountID,
		ProviderVMID:       vm.ProviderVMID,
		Name:               vm.Name,
		PowerState:         vm.PowerState,
		Ready:              vm.PowerState == "running",
		DeletionProtection: vm.DeletionProtection,
		NICs:               vm.NICs,
		SecurityGroups:     vm.SecurityGroups,
		BlockDevices:       vm.BlockDevices,
		Tags:               vm.Tags,
	}
	body.Role = stringPtrOrNil(vm.Role)
	body.PrivateIP = stringPtrOrNil(vm.PrivateIP)
	body.PublicIP = stringPtrOrNil(vm.PublicIP)
	body.PrivateDNSName = stringPtrOrNil(vm.PrivateDNSName)
	body.VPCID = stringPtrOrNil(vm.VPCID)
	body.SubnetID = stringPtrOrNil(vm.SubnetID)
	body.InstanceType = stringPtrOrNil(vm.InstanceType)
	body.Architecture = stringPtrOrNil(vm.Architecture)
	body.Zone = stringPtrOrNil(vm.Zone)
	body.Region = stringPtrOrNil(vm.Region)
	body.ImageID = stringPtrOrNil(vm.ImageID)
	body.ImageName = stringPtrOrNil(vm.ImageName)
	body.KeypairName = stringPtrOrNil(vm.KeypairName)
	body.BootMode = stringPtrOrNil(vm.BootMode)
	body.ProviderAccountID = stringPtrOrNil(vm.ProviderAccountID)
	body.StateReason = stringPtrOrNil(vm.StateReason)
	body.CapacityCPU = stringPtrOrNil(vm.CapacityCPU)
	body.CapacityMemory = stringPtrOrNil(vm.CapacityMemory)
	body.RootDeviceType = stringPtrOrNil(vm.RootDeviceType)
	body.RootDeviceName = stringPtrOrNil(vm.RootDeviceName)
	if !vm.ProviderCreationDate.IsZero() {
		s := vm.ProviderCreationDate.UTC().Format(time.RFC3339)
		body.ProviderCreationDate = &s
	}
	if err := s.doJSON(ctx, http.MethodPost, "/v1/virtual-machines", body, nil); err != nil {
		return err
	}
	return nil
}

// ReconcileVirtualMachines POSTs /v1/virtual-machines/reconcile.
func (s *Store) ReconcileVirtualMachines(ctx context.Context, accountID uuid.UUID, keep []string) (int64, error) {
	body := map[string]any{
		"cloud_account_id":     accountID,
		"keep_provider_vm_ids": keep,
	}
	var out struct {
		Tombstoned int64 `json:"tombstoned"`
	}
	if err := s.doJSON(ctx, http.MethodPost, "/v1/virtual-machines/reconcile", body, &out); err != nil {
		return 0, err
	}
	return out.Tombstoned, nil
}

func stringPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// --- HTTP plumbing -------------------------------------------------------

const (
	maxRetries    = 3
	retryBaseWait = 1 * time.Second
)

func (s *Store) doJSON(ctx context.Context, method, path string, body, dst any) error {
	var marshalled []byte
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		marshalled = buf
	}
	fullURL := s.baseURL + path

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		stop, err := s.doOnce(ctx, method, fullURL, marshalled, dst)
		if err != nil {
			lastErr = err
			if stop || ctx.Err() != nil {
				return lastErr
			}
			backoff(ctx, attempt)
			continue
		}
		return nil
	}
	return fmt.Errorf("%w: %w", errMaxRetries, lastErr)
}

//nolint:gocyclo // status-code switch
func (s *Store) doOnce(ctx context.Context, method, fullURL string, marshalled []byte, dst any) (stop bool, err error) {
	var bodyReader io.Reader
	if marshalled != nil {
		bodyReader = bytes.NewReader(marshalled)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return true, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	if marshalled != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range s.extraHeaders {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("%s %s: %w", method, req.URL.Path, err)
	}
	respBody, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return false, fmt.Errorf("%s %s: read response: %w", method, req.URL.Path, readErr)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if dst != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, dst); err != nil {
				return true, fmt.Errorf("decode response: %w", err)
			}
		}
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		return true, ErrNotRegistered
	case resp.StatusCode == http.StatusForbidden:
		// Could be account disabled or token mis-bound. Both are
		// terminal — return distinct sentinels so the collector logs
		// useful error text.
		if bytes.Contains(respBody, []byte("Account Disabled")) {
			return true, ErrAccountDisabled
		}
		return true, fmt.Errorf("%s %s: 403: %s", method, req.URL.Path, truncate(string(respBody), 200))
	case resp.StatusCode == http.StatusConflict:
		// On POST /virtual-machines this means already-a-kube-node;
		// surface the dedicated sentinel so the collector can log and continue.
		if strings.HasPrefix(req.URL.Path, "/v1/virtual-machines") && bytes.Contains(respBody, []byte("already_inventoried_as_kubernetes_node")) {
			return true, ErrAlreadyKubeNode
		}
		return true, fmt.Errorf("%s %s: 409: %s", method, req.URL.Path, truncate(string(respBody), 200))
	case resp.StatusCode == http.StatusUnauthorized:
		slog.Error("apiclient: unauthorised, not retrying",
			slog.String("method", method), slog.String("path", req.URL.Path))
		return true, fmt.Errorf("%s %s: 401", method, req.URL.Path)
	case resp.StatusCode >= 500:
		slog.Warn("apiclient: transient 5xx, retrying",
			slog.String("method", method), slog.String("path", req.URL.Path),
			slog.Int("status", resp.StatusCode))
		return false, fmt.Errorf("%s %s: %d %s", method, req.URL.Path, resp.StatusCode, truncate(string(respBody), 200))
	default:
		return true, fmt.Errorf("%s %s: %d %s", method, req.URL.Path, resp.StatusCode, truncate(string(respBody), 200))
	}
}

func backoff(ctx context.Context, attempt int) {
	wait := time.Duration(math.Pow(2, float64(attempt))) * retryBaseWait
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
