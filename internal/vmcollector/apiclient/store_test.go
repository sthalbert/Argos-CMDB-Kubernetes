package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/vmcollector/provider"
)

//nolint:unparam // token varies across test scenarios for future extensibility
func newClient(t *testing.T, srv *httptest.Server, token string) *Store {
	t.Helper()
	store, err := NewStore(Config{ServerURL: srv.URL, Token: token})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func TestFetchCredentialsByName(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/cloud-accounts/by-name/acme-prod/credentials" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer t-1" {
			t.Errorf("auth header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(Credentials{
			AccessKey: "ak", SecretKey: "sk", Region: "eu-west-2", Provider: "outscale",
		})
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	got, err := c.FetchCredentialsByName(context.Background(), "acme-prod")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.SecretKey != "sk" {
		t.Errorf("got %+v", got)
	}
}

func TestFetchCredentials404IsErrNotRegistered(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"cloud_account_not_registered"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	_, err := c.FetchCredentialsByName(context.Background(), "nope")
	if !errors.Is(err, ErrNotRegistered) {
		t.Errorf("err = %v, want ErrNotRegistered", err)
	}
}

func TestFetchCredentials403DisabledIsSentinel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"title":"Account Disabled"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	_, err := c.FetchCredentialsByName(context.Background(), "x")
	if !errors.Is(err, ErrAccountDisabled) {
		t.Errorf("err = %v, want ErrAccountDisabled", err)
	}
}

func TestUpsertVMConflictIsKubeNode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"already_inventoried_as_kubernetes_node"}`))
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	err := c.UpsertVirtualMachine(context.Background(), uuid.New(), provider.VM{
		ProviderVMID: "i-1", Name: "x", PowerState: "running",
	})
	if !errors.Is(err, ErrAlreadyKubeNode) {
		t.Errorf("err = %v, want ErrAlreadyKubeNode", err)
	}
}

func TestRegisterCloudAccount(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/cloud-accounts" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(CloudAccount{
			ID: uuid.New(), Provider: "outscale", Name: "x", Region: "eu-west-2",
			Status: "pending_credentials",
		})
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	got, err := c.RegisterCloudAccount(context.Background(), "outscale", "x", "eu-west-2")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got.Status != "pending_credentials" {
		t.Errorf("got %+v", got)
	}
}

func TestUpdateStatusSendsHeartbeat(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s", r.Method)
		}
		body := make(map[string]any)
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["status"] != "active" {
			t.Errorf("status = %v", body["status"])
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	now := time.Now()
	if err := c.UpdateCloudAccountStatus(context.Background(), uuid.New(), "active", &now, nil); err != nil {
		t.Errorf("update: %v", err)
	}
}

func TestReconcileReturnsTombstoned(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"tombstoned": 7})
	}))
	defer srv.Close()
	c := newClient(t, srv, "t-1")
	n, err := c.ReconcileVirtualMachines(context.Background(), uuid.New(), []string{"i-1"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if n != 7 {
		t.Errorf("tombstoned = %d", n)
	}
}

func TestExtraHeadersForwarded(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Tenant-Id"); got != "abc" {
			t.Errorf("X-Tenant-Id = %q", got)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	store, err := NewStore(Config{
		ServerURL:    srv.URL,
		Token:        "t",
		ExtraHeaders: map[string]string{"X-Tenant-Id": "abc"},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, err = store.FetchCredentialsByName(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error from 404 server")
	}
	if !strings.Contains(err.Error(), "cloud_account_not_registered") &&
		!errors.Is(err, ErrNotRegistered) {
		t.Errorf("err = %v", err)
	}
}
