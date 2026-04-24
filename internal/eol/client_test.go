package eol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const kubeEndpoint = "/api/kubernetes.json"

func serveCycles(w http.ResponseWriter, cycles []Cycle) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(cycles); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func TestClientGetProduct(t *testing.T) {
	t.Parallel()

	cycles := []Cycle{
		{Cycle: "1.30", EOL: "2025-06-28", Support: "2025-04-28", Latest: "1.30.14"},
		{Cycle: "1.28", EOL: "2025-01-28", Support: false, Latest: "1.28.15"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != kubeEndpoint {
			http.NotFound(w, r)
			return
		}
		serveCycles(w, cycles)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, 1*time.Hour, srv.Client())

	t.Run("successful fetch", func(t *testing.T) {
		got, err := client.GetProduct(context.Background(), "kubernetes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 cycles, got %d", len(got))
		}
		if got[0].Cycle != "1.30" {
			t.Errorf("cycle[0] = %q, want 1.30", got[0].Cycle)
		}
	})

	t.Run("cached on second call", func(t *testing.T) {
		got, err := client.GetProduct(context.Background(), "kubernetes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 cycles from cache, got %d", len(got))
		}
	})

	t.Run("unknown product returns error", func(t *testing.T) {
		_, err := client.GetProduct(context.Background(), "nonexistent")
		if err == nil {
			t.Fatal("expected error for unknown product")
		}
	})
}

func TestFindCycle(t *testing.T) {
	t.Parallel()

	cycles := []Cycle{
		{Cycle: "1.30", Latest: "1.30.14"},
		{Cycle: "1.28", Latest: "1.28.15"},
	}

	t.Run("found", func(t *testing.T) {
		c := FindCycle(cycles, "1.28")
		if c == nil {
			t.Fatal("expected cycle 1.28 to be found")
		}
		if c.Latest != "1.28.15" {
			t.Errorf("latest = %q, want 1.28.15", c.Latest)
		}
	})

	t.Run("not found", func(t *testing.T) {
		c := FindCycle(cycles, "1.26")
		if c != nil {
			t.Fatal("expected nil for unknown cycle")
		}
	})
}

func TestCycleEOLDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		eol     any
		wantOK  bool
		wantStr string
	}{
		{"string date", "2025-06-28", true, "2025-06-28"},
		{"bool false", false, false, ""},
		{"bool true", true, false, ""},
		{"nil", nil, false, ""},
		{"bad string", "not-a-date", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := Cycle{EOL: tt.eol}
			d, ok := c.EOLDate()
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if ok && d.Format("2006-01-02") != tt.wantStr {
				t.Errorf("date=%s, want %s", d.Format("2006-01-02"), tt.wantStr)
			}
		})
	}
}
