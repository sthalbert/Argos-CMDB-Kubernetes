package main

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/sthalbert/longue-vue/internal/auth"
	"github.com/sthalbert/longue-vue/internal/httputil"
)

func TestLoadCollectorClustersJSONPrecedence(t *testing.T) {
	// Having both LONGUE_VUE_COLLECTOR_CLUSTERS and the legacy single-cluster vars
	// set, the JSON must win so the operator's migration to multi-cluster
	// doesn't get silently shadowed by stale single-cluster env vars.
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[{"name":"prod","kubeconfig":"/etc/kube/prod.yaml"}]`)
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "legacy")
	t.Setenv("LONGUE_VUE_KUBECONFIG", "/etc/kube/legacy.yaml")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "prod" || got[0].Kubeconfig != "/etc/kube/prod.yaml" {
		t.Errorf("got=%+v, want exactly [{prod, /etc/kube/prod.yaml}]", got)
	}
}

func TestLoadCollectorClustersLegacyFallback(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", "")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "dev")
	t.Setenv("LONGUE_VUE_KUBECONFIG", "/home/me/.kube/config")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "dev" || got[0].Kubeconfig != "/home/me/.kube/config" {
		t.Errorf("got=%+v, want [{dev, /home/me/.kube/config}]", got)
	}
}

func TestLoadCollectorClustersLegacyFallbackEmptyKubeconfig(t *testing.T) {
	// Empty LONGUE_VUE_KUBECONFIG is legal — it means "use in-cluster config".
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", "")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "in-cluster")
	t.Setenv("LONGUE_VUE_KUBECONFIG", "")

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 1 || got[0].Name != "in-cluster" || got[0].Kubeconfig != "" {
		t.Errorf("got=%+v", got)
	}
}

func TestLoadCollectorClustersNoEnv(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", "")
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "")
	t.Setenv("LONGUE_VUE_KUBECONFIG", "")

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected an error when no cluster env is set")
	}
}

func TestLoadCollectorClustersMultiCluster(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[
		{"name":"prod","kubeconfig":"/etc/kube/prod.yaml"},
		{"name":"staging","kubeconfig":"/etc/kube/staging.yaml"},
		{"name":"in-cluster","kubeconfig":""}
	]`)

	got, err := loadCollectorClusters()
	if err != nil {
		t.Fatalf("loadCollectorClusters: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	if got[2].Name != "in-cluster" || got[2].Kubeconfig != "" {
		t.Errorf("in-cluster entry = %+v", got[2])
	}
}

func TestLoadCollectorClustersMalformedJSON(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[{"name":"prod"`) // truncated

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadCollectorClustersEmptyJSONArray(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[]`)
	t.Setenv("LONGUE_VUE_CLUSTER_NAME", "legacy") // legacy must not be used: JSON presence wins even if empty.

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on empty JSON array")
	}
}

func TestLoadCollectorClustersEmptyName(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[{"name":"","kubeconfig":"/x"}]`)

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on empty cluster name")
	}
}

func TestLoadCollectorClustersDuplicateName(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_CLUSTERS", `[
		{"name":"prod","kubeconfig":"/a"},
		{"name":"prod","kubeconfig":"/b"}
	]`)

	if _, err := loadCollectorClusters(); err == nil {
		t.Fatal("expected error on duplicate cluster name")
	}
}

// ---------------------------------------------------------------------------
// loadRunConfig
// ---------------------------------------------------------------------------

func setMinimalRunEnv(t *testing.T) {
	t.Helper()
	t.Setenv("LONGUE_VUE_DATABASE_URL", "postgres://lv:lv@localhost:5432/lv")
	t.Setenv("LONGUE_VUE_API_TOKEN", "")
	t.Setenv("LONGUE_VUE_API_TOKENS", "")
	t.Setenv("LONGUE_VUE_SESSION_SECURE_COOKIE", "")
	t.Setenv("LONGUE_VUE_SHUTDOWN_TIMEOUT", "")
	t.Setenv("LONGUE_VUE_AUTO_MIGRATE", "")
	t.Setenv("LONGUE_VUE_ADDR", "")
	t.Setenv("LONGUE_VUE_OIDC_ISSUER", "")
	// ADR-0017 — public-listener TLS posture and proxy trust.
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT", "")
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY", "")
	t.Setenv("LONGUE_VUE_TRUSTED_PROXIES", "")
	t.Setenv("LONGUE_VUE_REQUIRE_HTTPS", "")
}

func TestLoadRunConfig_Defaults(t *testing.T) {
	setMinimalRunEnv(t)

	cfg, err := loadRunConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.addr != ":8080" {
		t.Errorf("addr: want :8080, got %q", cfg.addr)
	}
	if cfg.dsn != "postgres://lv:lv@localhost:5432/lv" {
		t.Errorf("dsn: want the test DSN, got %q", cfg.dsn)
	}
	if cfg.cookiePolicy != auth.SecureAuto {
		t.Errorf("cookiePolicy: want SecureAuto, got %d", cfg.cookiePolicy)
	}
	if cfg.shutdownTimeout != 15*time.Second {
		t.Errorf("shutdownTimeout: want 15s, got %v", cfg.shutdownTimeout)
	}
	if !cfg.autoMigrate {
		t.Error("autoMigrate: want true")
	}
}

func TestLoadRunConfig_MissingDSN(t *testing.T) {
	t.Setenv("LONGUE_VUE_DATABASE_URL", "")

	_, err := loadRunConfig()
	if !errors.Is(err, errDatabaseURLRequired) {
		t.Errorf("want errDatabaseURLRequired, got %v", err)
	}
}

func TestLoadRunConfig_LegacyTokenRejected(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_API_TOKEN", "old-token")

	_, err := loadRunConfig()
	if !errors.Is(err, errLegacyTokensUnsupported) {
		t.Errorf("want errLegacyTokensUnsupported, got %v", err)
	}
}

func TestLoadRunConfig_LegacyTokensRejected(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_API_TOKENS", "tok1,tok2")

	_, err := loadRunConfig()
	if !errors.Is(err, errLegacyTokensUnsupported) {
		t.Errorf("want errLegacyTokensUnsupported, got %v", err)
	}
}

func TestLoadRunConfig_CustomValues(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_ADDR", ":9090")
	t.Setenv("LONGUE_VUE_SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("LONGUE_VUE_AUTO_MIGRATE", "false")
	t.Setenv("LONGUE_VUE_SESSION_SECURE_COOKIE", "always")

	cfg, err := loadRunConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.addr != ":9090" {
		t.Errorf("addr: want :9090, got %q", cfg.addr)
	}
	if cfg.shutdownTimeout != 30*time.Second {
		t.Errorf("shutdownTimeout: want 30s, got %v", cfg.shutdownTimeout)
	}
	if cfg.autoMigrate {
		t.Error("autoMigrate: want false")
	}
	if cfg.cookiePolicy != auth.SecureAlways {
		t.Errorf("cookiePolicy: want SecureAlways, got %d", cfg.cookiePolicy)
	}
}

func TestLoadRunConfig_InvalidShutdownTimeout(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_SHUTDOWN_TIMEOUT", "nope")

	_, err := loadRunConfig()
	if err == nil {
		t.Fatal("expected error for invalid shutdown timeout")
	}
}

func TestLoadRunConfig_InvalidAutoMigrate(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_AUTO_MIGRATE", "nope")

	_, err := loadRunConfig()
	if err == nil {
		t.Fatal("expected error for invalid auto_migrate")
	}
}

// ---------------------------------------------------------------------------
// parseCookiePolicy
// ---------------------------------------------------------------------------

func TestParseCookiePolicy(t *testing.T) {
	tests := []struct {
		env  string
		want auth.SecureCookiePolicy
	}{
		{"", auth.SecureAuto},
		{"auto", auth.SecureAuto},
		{"AUTO", auth.SecureAuto},
		{"always", auth.SecureAlways},
		{"true", auth.SecureAlways},
		{"yes", auth.SecureAlways},
		{"never", auth.SecureNever},
		{"false", auth.SecureNever},
		{"no", auth.SecureNever},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Setenv("LONGUE_VUE_SESSION_SECURE_COOKIE", tt.env)
			got, err := parseCookiePolicy()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("want %d, got %d", tt.want, got)
			}
		})
	}
}

func TestParseCookiePolicy_Invalid(t *testing.T) {
	t.Setenv("LONGUE_VUE_SESSION_SECURE_COOKIE", "maybe")
	_, err := parseCookiePolicy()
	if !errors.Is(err, errInvalidCookiePolicy) {
		t.Errorf("want errInvalidCookiePolicy, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadOIDCConfig
// ---------------------------------------------------------------------------

func TestLoadOIDCConfig_Empty(t *testing.T) {
	t.Setenv("LONGUE_VUE_OIDC_ISSUER", "")
	cfg := loadOIDCConfig()
	if cfg.Issuer != "" {
		t.Errorf("want empty issuer, got %q", cfg.Issuer)
	}
}

func TestLoadOIDCConfig_Full(t *testing.T) {
	t.Setenv("LONGUE_VUE_OIDC_ISSUER", "https://idp.example.com")
	t.Setenv("LONGUE_VUE_OIDC_CLIENT_ID", "longue-vue")
	t.Setenv("LONGUE_VUE_OIDC_CLIENT_SECRET", "s3cret")
	t.Setenv("LONGUE_VUE_OIDC_REDIRECT_URL", "https://longue-vue.example.com/v1/auth/oidc/callback")
	t.Setenv("LONGUE_VUE_OIDC_SCOPES", "openid, email , profile")
	t.Setenv("LONGUE_VUE_OIDC_LABEL", "Corp SSO")

	cfg := loadOIDCConfig()
	if cfg.Issuer != "https://idp.example.com" {
		t.Errorf("issuer: got %q", cfg.Issuer)
	}
	if cfg.ClientID != "longue-vue" {
		t.Errorf("clientId: got %q", cfg.ClientID)
	}
	if cfg.ClientSecret != "s3cret" {
		t.Errorf("clientSecret: got %q", cfg.ClientSecret)
	}
	if cfg.RedirectURL != "https://longue-vue.example.com/v1/auth/oidc/callback" {
		t.Errorf("redirectUrl: got %q", cfg.RedirectURL)
	}
	if len(cfg.Scopes) != 3 || cfg.Scopes[0] != "openid" || cfg.Scopes[1] != "email" || cfg.Scopes[2] != "profile" {
		t.Errorf("scopes: got %v", cfg.Scopes)
	}
	if cfg.Label != "Corp SSO" {
		t.Errorf("label: got %q", cfg.Label)
	}
}

// ---------------------------------------------------------------------------
// loadCollectorEnvConfig
// ---------------------------------------------------------------------------

func TestLoadCollectorEnvConfig_Defaults(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_INTERVAL", "")
	t.Setenv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", "")
	t.Setenv("LONGUE_VUE_COLLECTOR_RECONCILE", "")

	cfg, err := loadCollectorEnvConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.interval != 5*time.Minute {
		t.Errorf("interval: want 5m, got %v", cfg.interval)
	}
	if cfg.fetchTimeout != 10*time.Second {
		t.Errorf("fetchTimeout: want 10s, got %v", cfg.fetchTimeout)
	}
	if !cfg.reconcile {
		t.Error("reconcile: want true")
	}
}

func TestLoadCollectorEnvConfig_Custom(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_INTERVAL", "30s")
	t.Setenv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", "1m")
	t.Setenv("LONGUE_VUE_COLLECTOR_RECONCILE", "false")

	cfg, err := loadCollectorEnvConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.interval != 30*time.Second {
		t.Errorf("interval: want 30s, got %v", cfg.interval)
	}
	if cfg.fetchTimeout != 1*time.Minute {
		t.Errorf("fetchTimeout: want 1m, got %v", cfg.fetchTimeout)
	}
	if cfg.reconcile {
		t.Error("reconcile: want false")
	}
}

func TestLoadCollectorEnvConfig_InvalidInterval(t *testing.T) {
	t.Setenv("LONGUE_VUE_COLLECTOR_INTERVAL", "bad")
	t.Setenv("LONGUE_VUE_COLLECTOR_FETCH_TIMEOUT", "")
	t.Setenv("LONGUE_VUE_COLLECTOR_RECONCILE", "")

	_, err := loadCollectorEnvConfig()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// envOr / parseDurationEnv / parseBoolEnv
// ---------------------------------------------------------------------------

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_KEY", "")
	if got := envOr("TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("want fallback, got %q", got)
	}
	t.Setenv("TEST_KEY", "value")
	if got := envOr("TEST_KEY", "fallback"); got != "value" {
		t.Errorf("want value, got %q", got)
	}
}

func TestParseDurationEnv(t *testing.T) {
	t.Setenv("D", "")
	d, err := parseDurationEnv("D", 42*time.Second)
	if err != nil || d != 42*time.Second {
		t.Errorf("default: want 42s, got %v (err=%v)", d, err)
	}

	t.Setenv("D", "10m")
	d, err = parseDurationEnv("D", 0)
	if err != nil || d != 10*time.Minute {
		t.Errorf("set: want 10m, got %v (err=%v)", d, err)
	}

	t.Setenv("D", "xyz")
	_, err = parseDurationEnv("D", 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseBoolEnv(t *testing.T) {
	t.Setenv("B", "")
	b, err := parseBoolEnv("B", true)
	if err != nil || !b {
		t.Errorf("default: want true, got %v (err=%v)", b, err)
	}

	t.Setenv("B", "false")
	b, err = parseBoolEnv("B", true)
	if err != nil || b {
		t.Errorf("set: want false, got %v (err=%v)", b, err)
	}

	t.Setenv("B", "nope")
	_, err = parseBoolEnv("B", false)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// ADR-0017 — public listener TLS posture and proxy trust
// ---------------------------------------------------------------------------

// mustParseProxies is a test helper that turns a comma-separated CIDR list
// into the parsed slice the runConfig carries; on parse failure it fails
// the test rather than returning an error.
//
//nolint:unparam // generic helper; callers pass the same value today but new tests may not
func mustParseProxies(t *testing.T, csv string) []*net.IPNet {
	t.Helper()
	out, err := httputil.ParseTrustedProxies(csv)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", csv, err)
	}
	return out
}

func TestCheckTransportPosture_DisabledByDefault(t *testing.T) {
	// requireHTTPS=false is the default; nothing should be enforced.
	cfg := runConfig{
		requireHTTPS:   false,
		publicTLSCert:  "",
		publicTLSKey:   "",
		trustedProxies: nil,
		cookiePolicy:   auth.SecureAuto,
	}
	if err := checkTransportPosture(&cfg); err != nil {
		t.Fatalf("requireHTTPS=false must accept any transport posture, got %v", err)
	}
}

func TestCheckTransportPosture_NativeTLSAccepted(t *testing.T) {
	// Native TLS configured — branch (a) of ADR-0017 §3.
	cfg := runConfig{
		requireHTTPS:  true,
		publicTLSCert: "/tls/tls.crt",
		publicTLSKey:  "/tls/tls.key",
		cookiePolicy:  auth.SecureAuto,
	}
	if err := checkTransportPosture(&cfg); err != nil {
		t.Fatalf("native TLS must satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_TrustedProxiesPlusSecureAlwaysAccepted(t *testing.T) {
	// Branch (b): operator runs longue-vue behind a TLS-terminating ingress
	// in the trust list AND has set the cookie policy to always-secure.
	cfg := runConfig{
		requireHTTPS:   true,
		trustedProxies: mustParseProxies(t, "10.0.0.0/8"),
		cookiePolicy:   auth.SecureAlways,
	}
	if err := checkTransportPosture(&cfg); err != nil {
		t.Fatalf("trusted proxies + SecureAlways must satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_NoTLSNoProxiesRefused(t *testing.T) {
	// The pentest topology: requireHTTPS asked for, nothing configured.
	cfg := runConfig{
		requireHTTPS: true,
		cookiePolicy: auth.SecureAuto,
	}
	err := checkTransportPosture(&cfg)
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("want errTransportPostureRefused, got %v", err)
	}
}

func TestCheckTransportPosture_TrustedProxiesButCookieAutoRefused(t *testing.T) {
	// Trusted proxies alone are not enough — without SecureAlways, a
	// downgrade attack on XFP could strip the Secure flag from cookies.
	cfg := runConfig{
		requireHTTPS:   true,
		trustedProxies: mustParseProxies(t, "10.0.0.0/8"),
		cookiePolicy:   auth.SecureAuto,
	}
	err := checkTransportPosture(&cfg)
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("trusted proxies + SecureAuto must NOT satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_TrustedProxiesButCookieNeverRefused(t *testing.T) {
	cfg := runConfig{
		requireHTTPS:   true,
		trustedProxies: mustParseProxies(t, "10.0.0.0/8"),
		cookiePolicy:   auth.SecureNever,
	}
	err := checkTransportPosture(&cfg)
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("trusted proxies + SecureNever must NOT satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_OnlyCertNoKeyRefused(t *testing.T) {
	cfg := runConfig{
		requireHTTPS:  true,
		publicTLSCert: "/tls/tls.crt",
		publicTLSKey:  "",
		cookiePolicy:  auth.SecureAuto,
	}
	err := checkTransportPosture(&cfg)
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("cert without key must NOT satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_OnlyKeyNoCertRefused(t *testing.T) {
	cfg := runConfig{
		requireHTTPS:  true,
		publicTLSCert: "",
		publicTLSKey:  "/tls/tls.key",
		cookiePolicy:  auth.SecureAuto,
	}
	err := checkTransportPosture(&cfg)
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("key without cert must NOT satisfy the guard, got %v", err)
	}
}

func TestCheckTransportPosture_NativeTLSWinsOverProxyBranch(t *testing.T) {
	// When both branches are partially set, native TLS alone is enough —
	// no need for the cookie-policy check on this branch.
	cfg := runConfig{
		requireHTTPS:   true,
		publicTLSCert:  "/tls/tls.crt",
		publicTLSKey:   "/tls/tls.key",
		trustedProxies: mustParseProxies(t, "10.0.0.0/8"),
		cookiePolicy:   auth.SecureAuto,
	}
	if err := checkTransportPosture(&cfg); err != nil {
		t.Fatalf("native TLS branch is independently sufficient, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadRunConfig parsing of ADR-0017 env vars
// ---------------------------------------------------------------------------

func TestLoadRunConfig_PublicTLSPaths(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT", "/tls/tls.crt")
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY", "/tls/tls.key")

	cfg, err := loadRunConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.publicTLSCert != "/tls/tls.crt" {
		t.Errorf("publicTLSCert: got %q", cfg.publicTLSCert)
	}
	if cfg.publicTLSKey != "/tls/tls.key" {
		t.Errorf("publicTLSKey: got %q", cfg.publicTLSKey)
	}
}

func TestLoadRunConfig_TrustedProxiesParsed(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.0.0/16")

	cfg, err := loadRunConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.trustedProxies) != 2 {
		t.Fatalf("trustedProxies: want 2, got %d", len(cfg.trustedProxies))
	}
}

func TestLoadRunConfig_TrustedProxiesInvalidCIDR(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_TRUSTED_PROXIES", "not-a-cidr")

	if _, err := loadRunConfig(); err == nil {
		t.Fatal("expected error on invalid CIDR")
	}
}

func TestLoadRunConfig_RequireHTTPSDefaultFalse(t *testing.T) {
	setMinimalRunEnv(t)

	cfg, err := loadRunConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.requireHTTPS {
		t.Error("requireHTTPS: want default false")
	}
}

func TestLoadRunConfig_RequireHTTPSGuardRefuses(t *testing.T) {
	// requireHTTPS=true with no other transport config — the pentest
	// reproducer; loadRunConfig must refuse before the daemon starts.
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_REQUIRE_HTTPS", "true")

	_, err := loadRunConfig()
	if !errors.Is(err, errTransportPostureRefused) {
		t.Fatalf("want errTransportPostureRefused, got %v", err)
	}
}

func TestLoadRunConfig_RequireHTTPSAcceptsNativeTLS(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_REQUIRE_HTTPS", "true")
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_CERT", "/tls/tls.crt")
	t.Setenv("LONGUE_VUE_PUBLIC_LISTEN_TLS_KEY", "/tls/tls.key")

	if _, err := loadRunConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRunConfig_RequireHTTPSAcceptsProxiesPlusSecureAlways(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_REQUIRE_HTTPS", "true")
	t.Setenv("LONGUE_VUE_TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("LONGUE_VUE_SESSION_SECURE_COOKIE", "always")

	if _, err := loadRunConfig(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRunConfig_InvalidRequireHTTPS(t *testing.T) {
	setMinimalRunEnv(t)
	t.Setenv("LONGUE_VUE_REQUIRE_HTTPS", "nope")

	if _, err := loadRunConfig(); err == nil {
		t.Fatal("expected error on invalid LONGUE_VUE_REQUIRE_HTTPS")
	}
}
