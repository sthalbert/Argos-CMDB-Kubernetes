package ingestgw

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// CertReloader watches a cert/key pair on disk and exposes a hot-reloading
// callback suitable for tls.Config.GetClientCertificate (gateway → longue-vue
// mTLS) or tls.Config.GetCertificate (server side).
//
// The watcher uses fsnotify to react to file writes, renames, and creates
// in the cert directory. Vault Agent and cert-manager both atomic-rename
// when rotating, so the natural fsnotify event there is RENAME +
// CREATE — both paths trigger a reload.
//
// On reload failure (corrupted cert, key mismatch) the previous keypair
// is kept; the next valid file write triggers another reload. A
// Prometheus counter tracks success/failure so renewal regressions are
// visible before the cert actually expires.
type CertReloader struct {
	certPath string
	keyPath  string
	cert     atomic.Pointer[tls.Certificate]
	logger   *slog.Logger
	stop     chan struct{}
	done     chan struct{}
}

// NewCertReloader loads the cert/key pair at startup and starts an
// fsnotify watcher on the cert's directory. The returned reloader's
// GetClientCertificate / GetCertificate methods can be plugged directly
// into a tls.Config.
//
// Call Close to stop the watcher goroutine. Production code that runs
// for the process lifetime can leave it running until exit; tests MUST
// Close to avoid leaking goroutines that continue logging after the
// test directory is removed.
func NewCertReloader(certPath, keyPath string, logger *slog.Logger) (*CertReloader, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &CertReloader{
		certPath: certPath,
		keyPath:  keyPath,
		logger:   logger,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	if err := r.reload(); err != nil {
		return nil, fmt.Errorf("initial keypair load: %w", err)
	}
	go r.watch()
	return r, nil
}

// Close stops the fsnotify watcher goroutine and blocks until it exits.
// Idempotent: a second Close is a no-op.
func (r *CertReloader) Close() error {
	select {
	case <-r.stop:
		// already closed
	default:
		close(r.stop)
	}
	<-r.done
	return nil
}

// GetClientCertificate satisfies tls.Config.GetClientCertificate. The
// callback shape — accepting a *tls.CertificateRequestInfo — lets the
// stdlib pick our keypair on every handshake.
func (r *CertReloader) GetClientCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, fmt.Errorf("ingest gateway: no client certificate loaded") //nolint:err113 // local sentinel, never compared by callers
	}
	return cert, nil
}

// GetCertificate satisfies tls.Config.GetCertificate (server-side use).
// The gateway's inbound TLS listener is fronted by Envoy in production
// so this isn't strictly required, but exposing it keeps the API
// symmetric and tests easier.
func (r *CertReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := r.cert.Load()
	if cert == nil {
		return nil, fmt.Errorf("ingest gateway: no server certificate loaded") //nolint:err113 // local sentinel, never compared by callers
	}
	return cert, nil
}

// reload loads the current cert/key from disk and atomically swaps the
// in-memory pointer. Records the cert's NotAfter as a gauge so alerts
// fire before expiry.
func (r *CertReloader) reload() error {
	keypair, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		observeCertReload("failure")
		return fmt.Errorf("load X509 keypair: %w", err)
	}
	// LoadX509KeyPair leaves Leaf nil; parse it once so callers can
	// inspect NotAfter without re-parsing on every handshake.
	if len(keypair.Certificate) == 0 {
		observeCertReload("failure")
		return fmt.Errorf("loaded keypair has no certificates") //nolint:err113 // local sentinel, never compared by callers
	}
	leaf, err := x509.ParseCertificate(keypair.Certificate[0])
	if err != nil {
		observeCertReload("failure")
		return fmt.Errorf("parse leaf certificate: %w", err)
	}
	keypair.Leaf = leaf
	r.cert.Store(&keypair)
	observeCertNotAfter(leaf.NotAfter)
	observeCertReload("success")
	r.logger.Info("ingest gateway cert reloaded",
		slog.String("path", r.certPath),
		slog.Time("not_after", leaf.NotAfter),
		slog.String("subject_cn", leaf.Subject.CommonName),
	)
	return nil
}

// watch runs in a background goroutine until the process exits. Reacts
// to fsnotify writes / creates / renames in the cert's parent directory
// and re-runs reload(). Failures are logged but never fatal — the
// running cert keeps serving until the next successful reload.
func (r *CertReloader) watch() { //nolint:gocyclo // fsnotify event loop; flat select is clearer than factored sub-handlers
	defer close(r.done)
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		r.logger.Error("fsnotify init failed; cert hot-reload disabled",
			slog.Any("error", err))
		return
	}
	defer func() { _ = watcher.Close() }()

	dir := filepath.Dir(r.certPath)
	if err := watcher.Add(dir); err != nil {
		r.logger.Error("fsnotify add failed; cert hot-reload disabled",
			slog.String("dir", dir), slog.Any("error", err))
		return
	}

	// Coalesce bursts: Vault Agent emits multiple events on a single
	// rotation (CREATE + WRITE on the temp file, RENAME, CHMOD).
	// Reloading once per burst is sufficient.
	var debounce <-chan time.Time
	for {
		select {
		case <-r.stop:
			return
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !relevantEvent(ev, r.certPath, r.keyPath) {
				continue
			}
			if debounce == nil {
				debounce = time.After(200 * time.Millisecond)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			r.logger.Warn("fsnotify error", slog.Any("error", err))
		case <-debounce:
			debounce = nil
			if err := r.reload(); err != nil {
				r.logger.Error("cert reload failed; previous keypair retained",
					slog.Any("error", err))
			}
		}
	}
}

// relevantEvent filters fsnotify events to those that could change the
// cert or key file. Some editors emit lots of incidental events
// (Chmod-only, Remove on a temp swap file) we don't need to react to.
func relevantEvent(ev fsnotify.Event, certPath, keyPath string) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		return false
	}
	// Match by basename so atomic-renames into the watched dir trigger
	// a reload regardless of the temp filename used.
	name := filepath.Base(ev.Name)
	want := filepath.Base(certPath)
	wantKey := filepath.Base(keyPath)
	return name == want || name == wantKey
}
