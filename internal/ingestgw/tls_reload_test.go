package ingestgw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// generateSelfSignedCert creates a self-signed TLS cert+key pair, writes them
// to certPath/keyPath, and returns the cert DER bytes for comparison.
func generateSelfSignedCert(t *testing.T, certPath, keyPath, cn string) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certDER
}

// atomicWrite writes content to a temp file then renames to dst so the write
// looks atomic to fsnotify (matches Vault Agent / cert-manager patterns).
func atomicWrite(t *testing.T, dst string, content []byte) {
	t.Helper()
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}
}

func TestCertReloader_InitialLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	generateSelfSignedCert(t, certPath, keyPath, "initial-cn")

	reloader, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	t.Cleanup(func() { _ = reloader.Close() })

	// GetClientCertificate's contract is (cert, nil) on success or
	// (nil, err) on failure — never (nil, nil) — so once err is nil
	// the cert is non-nil by construction. NewCertReloader's reload()
	// also guarantees Leaf is parsed and stored on the keypair.
	cert, err := reloader.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate: %v", err)
	}
	if cert.Leaf.Subject.CommonName != "initial-cn" {
		t.Errorf("CN = %q; want initial-cn", cert.Leaf.Subject.CommonName)
	}
}

func TestCertReloader_GetCertificate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	generateSelfSignedCert(t, certPath, keyPath, "server-cn")

	reloader, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	t.Cleanup(func() { _ = reloader.Close() })

	cert, err := reloader.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert.Leaf.Subject.CommonName != "server-cn" {
		t.Errorf("CN = %q; want server-cn", cert.Leaf.Subject.CommonName)
	}
}

func TestCertReloader_HotReload(t *testing.T) {
	// Not parallel — fsnotify timing is inherently timing-dependent.
	// Use a generous but bounded wait so CI doesn't hang.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	generateSelfSignedCert(t, certPath, keyPath, "v1-cn")

	reloader, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	t.Cleanup(func() { _ = reloader.Close() })

	// Verify initial cert.
	cert1, err := reloader.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatalf("initial GetClientCertificate: %v", err)
	}
	cn1 := cert1.Leaf.Subject.CommonName
	if cn1 != "v1-cn" {
		t.Fatalf("initial CN = %q; want v1-cn", cn1)
	}

	// Generate new cert/key as new PEM content, write atomically.
	newKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "v2-cn"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	newDER, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &newKey.PublicKey, newKey)
	newCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: newDER})
	newKeyBytes, _ := x509.MarshalECPrivateKey(newKey)
	newKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: newKeyBytes})

	atomicWrite(t, certPath, newCertPEM)
	atomicWrite(t, keyPath, newKeyPEM)

	// Wait up to 5s for the reloader to pick up the new cert.
	// The debounce in watch() is 200ms; macOS kqueue can be slower than
	// inotify — give it generous headroom.
	deadline := time.Now().Add(5 * time.Second)
	var reloaded bool
	for time.Now().Before(deadline) {
		cert2, err := reloader.GetClientCertificate(&tls.CertificateRequestInfo{})
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if cert2.Leaf != nil && cert2.Leaf.Subject.CommonName == "v2-cn" {
			reloaded = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !reloaded {
		t.Error("cert was not hot-reloaded within 5s after atomic rename")
	}
}

func TestCertReloader_CorruptCertRetainsPrevious(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")

	generateSelfSignedCert(t, certPath, keyPath, "good-cn")

	reloader, err := NewCertReloader(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("NewCertReloader: %v", err)
	}
	t.Cleanup(func() { _ = reloader.Close() })

	// Overwrite with corrupt PEM — reload() should fail.
	atomicWrite(t, certPath, []byte("this is not a valid PEM certificate"))

	// Give the watcher a brief moment, then confirm the previous cert is still there.
	time.Sleep(300 * time.Millisecond)

	cert, err := reloader.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("GetClientCertificate after corrupt file: %v", err)
	}
	if cert.Leaf == nil || cert.Leaf.Subject.CommonName != "good-cn" {
		t.Errorf("expected previous cert (good-cn) to be retained; got CN=%q", cert.Leaf.Subject.CommonName)
	}
}

func TestCertReloader_InvalidPathFails(t *testing.T) {
	t.Parallel()
	_, err := NewCertReloader("/nonexistent/path/tls.crt", "/nonexistent/path/tls.key", nil)
	if err == nil {
		t.Error("expected error for nonexistent cert/key paths")
	}
}

func TestRelevantEvent(t *testing.T) {
	t.Parallel()
	certPath := "/certs/tls.crt"
	keyPath := "/certs/tls.key"

	// fsnotify.Op bitmask values:
	// Create=1, Write=2, Remove=4, Rename=8, Chmod=16
	type opCase struct {
		op   fsnotify.Op
		name string
		want bool
	}
	cases := []opCase{
		{fsnotify.Write, "/certs/tls.crt", true},
		{fsnotify.Create, "/certs/tls.key", true},
		{fsnotify.Rename, "/certs/tls.crt", true},
		{fsnotify.Chmod, "/certs/tls.crt", false},   // chmod-only should not trigger
		{fsnotify.Write, "/certs/other.txt", false}, // unrelated file
		{fsnotify.Remove, "/certs/tls.crt", false},  // Remove not in the relevant set
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := fsnotify.Event{Name: tc.name, Op: tc.op}
			got := relevantEvent(ev, certPath, keyPath)
			if got != tc.want {
				t.Errorf("relevantEvent(%v, %q) = %v; want %v", tc.op, tc.name, got, tc.want)
			}
		})
	}
}
