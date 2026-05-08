package imageversions

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/api"
)

type fakeStore struct {
	mu         sync.Mutex
	settings   api.Settings
	registries []api.ImageRegistry
	refs       []string
	upserted   []api.ImageVersionUpsert
	reaped     [][][2]string
	upsertErr  error
}

func (s *fakeStore) GetSettings(_ context.Context) (api.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings, nil
}
func (s *fakeStore) ListImageRegistries(_ context.Context) ([]api.ImageRegistry, error) {
	return s.registries, nil
}
func (s *fakeStore) DistinctImageRefs(_ context.Context) ([]string, error) {
	return s.refs, nil
}
func (s *fakeStore) UpsertImageVersion(_ context.Context, in api.ImageVersionUpsert) (api.ImageVersionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upserted = append(s.upserted, in)
	return api.ImageVersionRow{ImageRepo: in.ImageRepo, Variant: in.Variant}, s.upsertErr
}
func (s *fakeStore) DeleteImageVersionsNotIn(_ context.Context, keep [][2]string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reaped = append(s.reaped, keep)
	return 0, nil
}

type fakeLister struct {
	byRepo map[string][]string
	err    error
}

func (l *fakeLister) ListTags(_ context.Context, _ string, repoPath string) ([]string, error) {
	if l.err != nil {
		return nil, l.err
	}
	return l.byRepo[repoPath], nil
}

func TestEnricher_Tick_Disabled(t *testing.T) {
	s := &fakeStore{settings: api.Settings{ImageVersionsEnabled: false}}
	e := NewEnricher(s, &fakeLister{}, time.Hour)
	e.RunTick(context.Background())
	if len(s.upserted) != 0 {
		t.Fatalf("expected no upserts when disabled, got %d", len(s.upserted))
	}
	if len(s.reaped) != 0 {
		t.Fatalf("expected no reap call when disabled, got %d", len(s.reaped))
	}
}

func TestEnricher_Tick_NoEnabledRegistries_DoesNotReap(t *testing.T) {
	// When the admin disables every registry while leaving the feature
	// toggle on, the tick must NOT call DeleteImageVersionsNotIn — that
	// would wipe every existing row when called with an empty keep slice.
	s := &fakeStore{
		settings: api.Settings{ImageVersionsEnabled: true},
		registries: []api.ImageRegistry{
			{Hostname: "docker.io", RateLimitPerSec: 1.0, Enabled: false},
		},
		refs: []string{"nginx:1.25.3"},
	}
	e := NewEnricher(s, &fakeLister{}, time.Hour)
	e.RunTick(context.Background())
	if len(s.reaped) != 0 {
		t.Fatalf("expected no reap call when no registries enabled, got %d", len(s.reaped))
	}
	if len(s.upserted) != 0 {
		t.Fatalf("expected no upserts when no registries enabled, got %d", len(s.upserted))
	}
}

func TestEnricher_Tick_HappyPath(t *testing.T) {
	enabled := true
	s := &fakeStore{
		settings:   api.Settings{ImageVersionsEnabled: enabled},
		registries: []api.ImageRegistry{{Hostname: "docker.io", RateLimitPerSec: 1.0, Enabled: true}},
		refs:       []string{"nginx:1.25.3", "nginx:1.25.3-alpine"},
	}
	l := &fakeLister{byRepo: map[string][]string{
		"library/nginx": {"1.25.3", "1.27.4", "1.27.4-alpine", "1.27.5-rc1"},
	}}
	e := NewEnricher(s, l, time.Hour)
	e.RunTick(context.Background())

	if len(s.upserted) != 2 {
		t.Fatalf("expected 2 upserts (one per variant), got %d: %+v", len(s.upserted), s.upserted)
	}
	seen := map[string]string{}
	for _, u := range s.upserted {
		var lt string
		if u.LatestTag != nil {
			lt = *u.LatestTag
		}
		seen[u.Variant] = lt
	}
	if seen[""] != "1.27.4" {
		t.Errorf("variant=\"\" expected 1.27.4, got %q", seen[""])
	}
	if seen["alpine"] != "1.27.4-alpine" {
		t.Errorf("variant=alpine expected 1.27.4-alpine, got %q", seen["alpine"])
	}
	if len(s.reaped) != 1 {
		t.Fatalf("expected one reap call, got %d", len(s.reaped))
	}
}

func TestEnricher_Tick_RegistryError_DoesNotFailTick(t *testing.T) {
	enabled := true
	s := &fakeStore{
		settings:   api.Settings{ImageVersionsEnabled: enabled},
		registries: []api.ImageRegistry{{Hostname: "docker.io", RateLimitPerSec: 1.0, Enabled: true}},
		refs:       []string{"nginx:1.25.3"},
	}
	l := &fakeLister{err: errors.New("network down")}
	e := NewEnricher(s, l, time.Hour)
	e.RunTick(context.Background())

	if len(s.upserted) != 1 {
		t.Fatalf("expected 1 upsert with error info, got %d", len(s.upserted))
	}
	if s.upserted[0].LastError == nil || *s.upserted[0].LastError == "" {
		t.Fatalf("expected last_error populated")
	}
	if s.upserted[0].LatestTag != nil {
		t.Fatalf("expected latest_tag nil on error")
	}
}

func TestEnricher_Trigger_DedupesWhilePending(t *testing.T) {
	s := &fakeStore{settings: api.Settings{ImageVersionsEnabled: false}}
	e := NewEnricher(s, &fakeLister{}, time.Hour)
	if running := e.Trigger(); running {
		t.Fatalf("first trigger should not report running")
	}
	if running := e.Trigger(); !running {
		t.Fatalf("second back-to-back trigger should report running/pending")
	}
}

func TestEnricher_AnnotationShape(t *testing.T) {
	latest := "1.27.4"
	ann, err := buildAnnotation(&latest, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(ann, &m); err != nil {
		t.Fatalf("annotation must be valid JSON: %v", err)
	}
	if m["latest_available"] != "1.27.4" {
		t.Errorf("expected latest_available, got %v", m)
	}
}
