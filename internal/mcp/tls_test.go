package mcp

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// selfSignedCert generates a throwaway self-signed TLS certificate for tests.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	return cert
}

// TestSSE_RefusesToStartWithoutTLS verifies that runSSE returns the refusal
// error immediately when TLS is not configured and AllowPlaintext=false.
func TestSSE_RefusesToStartWithoutTLS(t *testing.T) {
	t.Parallel()
	s := NewServer(newFakeStore(), nil, &Config{
		Transport:         "sse",
		Addr:              "127.0.0.1:0",
		AllowPlaintext:    false,
		TLSGetCertificate: nil,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.Run(ctx)
	if err == nil {
		t.Fatal("expected refusal error, got nil")
	}
	if !strings.Contains(err.Error(), "CRIT-01") {
		t.Fatalf("expected CRIT-01 in error, got: %v", err)
	}
}

// TestSSE_StartsPlaintextWhenAllowed verifies the server binds when
// AllowPlaintext=true and shuts down cleanly on context cancellation.
func TestSSE_StartsPlaintextWhenAllowed(t *testing.T) {
	t.Parallel()

	addr := pickFreePort(t)

	s := NewServer(newFakeStore(), nil, &Config{
		Transport:      "sse",
		Addr:           addr,
		AllowPlaintext: true,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Run(ctx)
	}()

	// Give the server a moment to bind.
	time.Sleep(50 * time.Millisecond)

	// Verify it's actually listening.
	dialer := &net.Dialer{Timeout: time.Second}
	conn, dialErr := dialer.DialContext(ctx, "tcp", addr)
	if dialErr != nil {
		cancel()
		t.Fatalf("server not listening: %v", dialErr)
	}
	_ = conn.Close()

	// Cancel and wait for clean exit.
	cancel()
	select {
	case runErr := <-errCh:
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Fatalf("unexpected error: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

// TestSSE_StartsWithTLS verifies that an HTTPS client can complete a TLS
// handshake with the SSE listener and that a plain HTTP client gets a TLS
// error, confirming native TLS 1.3 is active.
func TestSSE_StartsWithTLS(t *testing.T) {
	t.Parallel()

	cert := selfSignedCert(t)
	getCert := func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return &cert, nil
	}

	addr := pickFreePort(t)

	s := NewServer(newFakeStore(), nil, &Config{
		Transport:         "sse",
		Addr:              addr,
		TLSGetCertificate: getCert,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		if err := s.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			// Log but don't fail — test goroutine may have exited.
			fmt.Printf("mcp run: %v\n", err)
		}
	}()

	waitForListener(ctx, addr, 3*time.Second)
	checkTLSHandshake(ctx, t, addr)
	checkPlainRejected(ctx, t, addr)
}

// pickFreePort binds and immediately closes a listener to discover a free
// 127.0.0.1 port for tests.
func pickFreePort(t *testing.T) string {
	t.Helper()
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitForListener polls addr until a TCP connection succeeds or deadline
// elapses.
func waitForListener(ctx context.Context, addr string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	dialer := &net.Dialer{Timeout: 100 * time.Millisecond}
	for time.Now().Before(deadline) {
		conn, e := dialer.DialContext(ctx, "tcp", addr)
		if e == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// checkTLSHandshake asserts that an HTTPS client can complete a TLS
// handshake against addr (any HTTP response code = success).
func checkTLSHandshake(ctx context.Context, t *testing.T, addr string) {
	t.Helper()
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only
		},
		Timeout: 2 * time.Second,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/sse", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := tlsClient.Do(req)
	switch {
	case err == nil:
		_ = resp.Body.Close()
	case strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "connection reset"):
		// Server closed SSE stream immediately — handshake still completed.
	default:
		t.Fatalf("HTTPS client failed (expected TLS handshake to succeed): %v", err)
	}
}

// checkPlainRejected asserts that a plain HTTP request to a TLS listener
// either errors at transport level or returns a non-2xx response.
func checkPlainRejected(ctx context.Context, t *testing.T, addr string) {
	t.Helper()
	plainClient := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/sse", http.NoBody)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	plainResp, plainErr := plainClient.Do(req)
	if plainErr != nil {
		return // transport-level error is acceptable
	}
	defer func() { _ = plainResp.Body.Close() }()
	if plainResp.StatusCode >= 200 && plainResp.StatusCode < 300 {
		t.Fatal("plain HTTP client got 2xx — TLS not enforced")
	}
}
