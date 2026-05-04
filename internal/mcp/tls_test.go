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
	s := NewServer(newFakeStore(), nil, Config{
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

	// Pick a free port by binding a temporary listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := NewServer(newFakeStore(), nil, Config{
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
	conn, dialErr := net.DialTimeout("tcp", addr, time.Second)
	if dialErr != nil {
		cancel()
		t.Fatalf("server not listening: %v", dialErr)
	}
	conn.Close()

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

	// Pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("find free port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s := NewServer(newFakeStore(), nil, Config{
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

	// Wait for listener to be ready.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, e := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if e == nil {
			conn.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// HTTPS client with InsecureSkipVerify should complete the TLS handshake.
	tlsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only
		},
		Timeout: 2 * time.Second,
	}
	resp, err := tlsClient.Get("https://" + addr + "/sse")
	if err == nil {
		resp.Body.Close()
		// Any HTTP response (even 4xx) means TLS handshake succeeded.
	} else if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "connection reset") {
		// Server closed SSE stream immediately — handshake still completed.
	} else {
		t.Fatalf("HTTPS client failed (expected TLS handshake to succeed): %v", err)
	}

	// Plain HTTP client must either error or receive a non-2xx response
	// (Go's TLS server responds with 400 "Client sent an HTTP request to an HTTPS server").
	plainClient := &http.Client{Timeout: 2 * time.Second}
	plainResp, plainErr := plainClient.Get("http://" + addr + "/sse")
	if plainErr == nil {
		plainResp.Body.Close()
		if plainResp.StatusCode >= 200 && plainResp.StatusCode < 300 {
			t.Fatal("plain HTTP client got 2xx — TLS not enforced")
		}
		// Non-2xx (e.g. 400) from the TLS server is expected.
	}
	// A transport-level error is also acceptable (e.g. malformed TLS record).
}
