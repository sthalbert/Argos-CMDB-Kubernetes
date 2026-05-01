package ingestgw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newProxyServer builds a gateway Server pointing at the given upstream URL.
func newProxyServer(t *testing.T, upstream string) *Server {
	t.Helper()
	s, err := NewServer(Config{
		UpstreamBaseURL: upstream,
		UpstreamClient:  &http.Client{},
		MaxBodyBytes:    1 << 20,
		CacheConfig:     DefaultCacheConfig(),
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func TestCopyForwardableHeaders_HopByHop(t *testing.T) {
	t.Parallel()
	hopHeaders := []string{
		"Connection",
		"Keep-Alive",
		"Proxy-Connection",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	}
	src := http.Header{}
	for _, h := range hopHeaders {
		src.Set(h, "should-be-stripped")
	}
	src.Set("Authorization", "Bearer argos_pat_aabbccdd_token")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyForwardableHeaders(dst, src)

	for _, h := range hopHeaders {
		if dst.Get(h) != "" {
			t.Errorf("hop-by-hop header %q should have been stripped; got %q", h, dst.Get(h))
		}
	}
	if dst.Get("Authorization") == "" {
		t.Error("Authorization header should be forwarded")
	}
	if dst.Get("Content-Type") == "" {
		t.Error("Content-Type header should be forwarded")
	}
}

func TestCopyForwardableHeaders_StripIngressHeaders(t *testing.T) {
	t.Parallel()
	ingressOnly := []string{
		"X-Real-Ip",
		"X-Longue-Vue-Verified-Caller",
		"X-Longue-Vue-Verified-Scope",
		"X-Longue-Vue-Verified-User",
	}
	src := http.Header{}
	for _, h := range ingressOnly {
		src.Set(h, "attacker-value")
	}
	src.Set("Authorization", "Bearer tok")

	dst := http.Header{}
	copyForwardableHeaders(dst, src)

	for _, h := range ingressOnly {
		if dst.Get(h) != "" {
			t.Errorf("ingest-internal header %q should be stripped; got %q", h, dst.Get(h))
		}
	}
}

func TestCopyForwardableHeaders_AuthForwarded(t *testing.T) {
	t.Parallel()
	const bearerVal = "Bearer argos_pat_aabbccdd_mytoken"
	src := http.Header{}
	src.Set("Authorization", bearerVal)

	dst := http.Header{}
	copyForwardableHeaders(dst, src)

	if got := dst.Get("Authorization"); got != bearerVal {
		t.Errorf("Authorization = %q; want %q", got, bearerVal)
	}
}

func TestProxyRequest_BodyCapRejects413(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Should never be reached.
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	s := newProxyServer(t, upstream.URL)

	// Build a request whose body exceeds MaxBodyBytes.
	hugeBody := strings.Repeat("x", int(s.maxBodyBytes+1))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/clusters", strings.NewReader(hugeBody))
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	rr := httptest.NewRecorder()
	// handleProxy is the handler that enforces the cap; we call it directly
	// to bypass auth for this unit test.
	s.handleProxy(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d; want 413", rr.Code)
	}
}

func TestBuildUpstreamURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		base       string
		requestURI string
		want       string
	}{
		{"https://longue-vue:8443", "/v1/clusters", "https://longue-vue:8443/v1/clusters"},
		{"https://longue-vue:8443/", "/v1/pods", "https://longue-vue:8443/v1/pods"},
		{"https://longue-vue:8443", "/v1/clusters?foo=bar", "https://longue-vue:8443/v1/clusters?foo=bar"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got, err := buildUpstreamURL(tc.base, tc.requestURI)
			if err != nil {
				t.Fatalf("buildUpstreamURL error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		xff    string
		realIP string
		remote string
		wantIP string
	}{
		{"xff single", "1.2.3.4", "", "", "1.2.3.4"},
		{"xff list", "1.2.3.4, 5.6.7.8", "", "", "1.2.3.4"},
		{"x-real-ip", "", "10.0.0.1", "", "10.0.0.1"},
		{"remoteaddr", "", "", "192.168.1.1:12345", "192.168.1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.realIP != "" {
				r.Header.Set("X-Real-Ip", tc.realIP)
			}
			if tc.remote != "" {
				r.RemoteAddr = tc.remote
			}
			got := clientIP(r)
			if got != tc.wantIP {
				t.Errorf("clientIP() = %q; want %q", got, tc.wantIP)
			}
		})
	}
}
