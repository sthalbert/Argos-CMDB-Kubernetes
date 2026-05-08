package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRegistry serves a /v2/<repo>/tags/list endpoint with optional bearer-auth
// challenge and Link-header pagination.
func newFakeRegistry(t *testing.T, requireAuth bool, pages [][]string) *httptest.Server {
	var tokenIssuer *httptest.Server
	if requireAuth {
		tokenIssuer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"token": "fake-token", "expires_in": 300})
		}))
		t.Cleanup(tokenIssuer.Close)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v2/") || !strings.HasSuffix(r.URL.Path, "/tags/list") {
			http.NotFound(w, r)
			return
		}
		if requireAuth && r.Header.Get("Authorization") != "Bearer fake-token" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+tokenIssuer.URL+`",service="fake",scope="repository:foo:pull"`)
			http.Error(w, "auth required", http.StatusUnauthorized)
			return
		}
		page := 0
		if p := r.URL.Query().Get("page"); p == "1" {
			page = 1
		}
		if page >= len(pages) {
			json.NewEncoder(w).Encode(map[string]any{"name": "foo", "tags": []string{}})
			return
		}
		if page+1 < len(pages) {
			w.Header().Set("Link", `<`+srv.URL+`/v2/foo/tags/list?page=1>; rel="next"`)
		}
		json.NewEncoder(w).Encode(map[string]any{"name": "foo", "tags": pages[page]})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListTags_NoAuth(t *testing.T) {
	srv := newFakeRegistry(t, false, [][]string{{"1.0.0", "1.1.0"}})
	c := NewClient()
	tags, err := c.ListTags(context.Background(), srv.URL, "foo")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tags) != 2 || tags[0] != "1.0.0" || tags[1] != "1.1.0" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestListTags_BearerAuth(t *testing.T) {
	srv := newFakeRegistry(t, true, [][]string{{"1.0.0"}})
	c := NewClient()
	tags, err := c.ListTags(context.Background(), srv.URL, "foo")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tags) != 1 || tags[0] != "1.0.0" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestListTags_Pagination(t *testing.T) {
	srv := newFakeRegistry(t, false, [][]string{
		{"1.0.0", "1.1.0"},
		{"1.2.0"},
	})
	c := NewClient()
	tags, err := c.ListTags(context.Background(), srv.URL, "foo")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tags) != 3 {
		t.Fatalf("expected 3 tags after pagination, got %d", len(tags))
	}
}

func TestListTags_RepoNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	_, err := c.ListTags(context.Background(), srv.URL, "missing")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrRepoNotFound) {
		t.Errorf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestListTags_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	_, err := c.ListTags(context.Background(), srv.URL, "foo")
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}
