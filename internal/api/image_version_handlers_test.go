package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newFakeStoreForTest returns a fresh in-memory store suitable for handler unit tests.
func newFakeStoreForTest(t *testing.T) *memStore {
	t.Helper()
	return newMemStore()
}

// enableImageVersions sets the ImageVersionsEnabled flag on the fake store.
func enableImageVersions(t *testing.T, s *memStore, enabled bool) {
	t.Helper()
	s.mu.Lock()
	s.settings.ImageVersionsEnabled = enabled
	s.mu.Unlock()
}

func TestHandleListImageVersions_Empty(t *testing.T) {
	s := newFakeStoreForTest(t)
	h := HandleListImageVersions(s)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/image-versions", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["items"]; !ok {
		t.Fatalf("missing items field in %v", body)
	}
}

func TestHandleRefresh_FeatureDisabled(t *testing.T) {
	s := newFakeStoreForTest(t)
	enr := &fakeRefresher{enabled: false}
	h := HandleRefreshImageVersions(s, enr)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/image-versions/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d", w.Code)
	}
}

func TestHandleRefresh_Triggered(t *testing.T) {
	s := newFakeStoreForTest(t)
	enr := &fakeRefresher{enabled: true}
	enableImageVersions(t, s, true)

	h := HandleRefreshImageVersions(s, enr)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/image-versions/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d", w.Code)
	}
	if !enr.triggered {
		t.Fatalf("expected enricher.Trigger to be called")
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["already_running"] != false {
		t.Errorf("expected already_running=false, got %v", body["already_running"])
	}
	if body["queued"] != true {
		t.Errorf("expected queued=true, got %v", body["queued"])
	}
}

func TestHandleRefresh_AlreadyRunning(t *testing.T) {
	s := newFakeStoreForTest(t)
	enr := &fakeRefresher{enabled: true, isRunning: true}
	enableImageVersions(t, s, true)

	h := HandleRefreshImageVersions(s, enr)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/image-versions/refresh", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body) //nolint:errcheck // test helper; failure visible via body check below
	if body["already_running"] != true {
		t.Errorf("expected already_running=true, got %v", body["already_running"])
	}
}

type fakeRefresher struct {
	enabled   bool
	triggered bool
	isRunning bool
}

func (f *fakeRefresher) Trigger() bool   { f.triggered = true; return f.isRunning }
func (f *fakeRefresher) IsRunning() bool { return f.isRunning }
