package api

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func emptyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	t.Parallel()

	handler := SecurityHeadersMiddleware(nil, false)(emptyHandler())

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

	t.Run("no HSTS on plain HTTP", func(t *testing.T) {
		t.Parallel()
		h := SecurityHeadersMiddleware(nil, false)(emptyHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody))
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS should not be set on plain HTTP, got %q", got)
		}
	})

	// AUTH-VULN-04 reproducer: with no trusted proxies, X-Forwarded-Proto
	// from an attacker-controlled peer must NOT trigger HSTS — that header
	// shape should only ever come from a real TLS-terminating ingress.
	t.Run("XFP ignored without trust list", func(t *testing.T) {
		t.Parallel()
		h := SecurityHeadersMiddleware(nil, false)(emptyHandler())
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody)
		req.RemoteAddr = "203.0.113.5:443"
		req.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS leaked from spoofed XFP without trust: %q", got)
		}
	})

	t.Run("XFP honored from trusted peer", func(t *testing.T) {
		t.Parallel()
		_, cidr, err := net.ParseCIDR("10.0.0.0/8")
		if err != nil {
			t.Fatal(err)
		}
		h := SecurityHeadersMiddleware([]*net.IPNet{cidr}, false)(emptyHandler())
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody)
		req.RemoteAddr = "10.0.0.1:443"
		req.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
			t.Error("HSTS missing for XFP from trusted peer")
		}
	})

	t.Run("XFP ignored from untrusted peer", func(t *testing.T) {
		t.Parallel()
		_, cidr, err := net.ParseCIDR("10.0.0.0/8")
		if err != nil {
			t.Fatal(err)
		}
		h := SecurityHeadersMiddleware([]*net.IPNet{cidr}, false)(emptyHandler())
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody)
		req.RemoteAddr = "203.0.113.5:443" // not in 10.0.0.0/8
		req.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
			t.Errorf("HSTS leaked from spoofed XFP from untrusted peer: %q", got)
		}
	})

	// LONGUE_VUE_REQUIRE_HTTPS=true is the operator's promise that this
	// deployment is HTTPS-only. Emit HSTS unconditionally so a browser
	// that ever lands on the public hostname is told never to downgrade.
	t.Run("force-emit HSTS when require-https is set", func(t *testing.T) {
		t.Parallel()
		h := SecurityHeadersMiddleware(nil, true)(emptyHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), "GET", "http://localhost/", http.NoBody))
		if got := rec.Header().Get("Strict-Transport-Security"); got == "" {
			t.Error("force-emit: HSTS must be set even on plain HTTP request shape")
		}
	})
}
