package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Parallel()

	handler := SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "strict-origin-when-cross-origin"},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody))
			if got := rec.Header().Get(tt.header); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestSecurityHeadersHSTS(t *testing.T) {
	t.Parallel()

	handler := SecurityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("no HSTS on plain HTTP", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody))
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS should not be set on plain HTTP, got %q", got)
		}
	})

	t.Run("HSTS on X-Forwarded-Proto https", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody)
		req.Header.Set("X-Forwarded-Proto", "https")
		handler.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
			t.Error("HSTS should be set when X-Forwarded-Proto is https")
		}
	})
}
