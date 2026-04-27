// Package vmcollector implements the polling loop run by the
// argos-vm-collector binary (ADR-0015 §11). One Collector instance =
// one cloud account.
package vmcollector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/vmcollector/apiclient"
	"github.com/sthalbert/argos/internal/vmcollector/filter"
	"github.com/sthalbert/argos/internal/vmcollector/provider"
)

// ErrCredentialsNotProvisioned is returned by ensureCredentials when the
// account is registered but no credentials have been supplied by an admin yet.
var ErrCredentialsNotProvisioned = errors.New("credentials not yet provisioned")

// CollectorStore is the slice of apiclient.Store the collector consumes.
// Declared as an interface so unit tests can swap in a fake.
type CollectorStore interface {
	FetchCredentialsByName(ctx context.Context, name string) (apiclient.Credentials, error)
	RegisterCloudAccount(ctx context.Context, providerName, name, region string) (apiclient.CloudAccount, error)
	UpdateCloudAccountStatus(ctx context.Context, id uuid.UUID, status string, lastSeenAt *time.Time, lastErr *string) error
	UpsertVirtualMachine(ctx context.Context, accountID uuid.UUID, vm provider.VM) error
	ReconcileVirtualMachines(ctx context.Context, accountID uuid.UUID, keep []string) (int64, error)
}

// ProviderFactory builds a Provider from the credentials fetched from
// argosd. Returns a fresh instance so AK/SK rotation produces a fresh
// SDK client.
type ProviderFactory func(creds apiclient.Credentials) (provider.Provider, error)

// Config carries the runtime knobs for one Collector goroutine.
type Config struct {
	Provider          string // "outscale"
	AccountName       string // matches cloud_accounts.name
	Region            string
	Interval          time.Duration
	FetchTimeout      time.Duration
	Reconcile         bool
	CredentialRefresh time.Duration
}

// Collector is the polling loop for one cloud account.
type Collector struct {
	cfg     Config
	store   CollectorStore
	factory ProviderFactory

	mu        sync.Mutex
	provider  provider.Provider
	accountID uuid.UUID
	creds     apiclient.Credentials
	credsAt   time.Time
}

// New builds a Collector. The factory is invoked once on first
// successful credential fetch, and again on each refresh.
//
//nolint:gocritic // hugeParam: Config is the standard constructor signature; callers pass by value intentionally
func New(cfg Config, store CollectorStore, factory ProviderFactory) *Collector {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 30 * time.Second
	}
	if cfg.CredentialRefresh <= 0 {
		cfg.CredentialRefresh = time.Hour
	}
	return &Collector{cfg: cfg, store: store, factory: factory}
}

// Run executes the polling loop until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	slog.Info("vm-collector starting",
		slog.String("provider", c.cfg.Provider),
		slog.String("account_name", c.cfg.AccountName),
		slog.String("region", c.cfg.Region),
		slog.String("interval", c.cfg.Interval.String()),
	)

	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()

	for {
		c.runOnce(ctx)
		select {
		case <-ctx.Done():
			slog.Info("vm-collector stopping")
			return fmt.Errorf("vm-collector: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// runOnce performs one tick: ensure credentials, ensure provider,
// list VMs, filter, upsert each, reconcile, update status.
//
//nolint:gocyclo // tick logic is inherently branchy
func (c *Collector) runOnce(ctx context.Context) {
	tickStart := time.Now()
	tickCtx, cancel := context.WithTimeout(ctx, c.cfg.FetchTimeout)
	defer cancel()

	if err := c.ensureCredentials(tickCtx); err != nil {
		slog.Warn("vm-collector: credentials unavailable",
			slog.Any("error", err),
			slog.String("account_name", c.cfg.AccountName))
		ObserveTick("error", time.Since(tickStart))
		return
	}

	prov := c.getProvider()
	accountID := c.getAccountID()
	if prov == nil || accountID == uuid.Nil {
		// Should not happen — ensureCredentials populates these on success.
		ObserveTick("error", time.Since(tickStart))
		return
	}

	vms, err := prov.ListVMs(tickCtx)
	if err != nil {
		c.reportTickError(ctx, err)
		ObserveTick("error", time.Since(tickStart))
		return
	}
	kept := filter.Apply(vms)
	// Pre-filter dropped count: VMs the collector knew were kube-owned
	// before sending. Server-side 409s are counted separately below.
	IncSkippedKubernetes(len(vms) - len(kept))
	SetVMsObserved(len(kept))
	slog.Info("vm-collector tick: provider list",
		slog.Int("listed", len(vms)),
		slog.Int("kept", len(kept)),
	)
	keep := make([]string, 0, len(kept))
	for i := range kept {
		if err := c.store.UpsertVirtualMachine(tickCtx, accountID, kept[i]); err != nil {
			if errors.Is(err, apiclient.ErrAlreadyKubeNode) {
				IncSkippedKubernetes(1)
				slog.Info("vm-collector: skipping kube node",
					slog.String("provider_vm_id", kept[i].ProviderVMID))
				continue
			}
			c.reportTickError(ctx, fmt.Errorf("upsert %s: %w", kept[i].ProviderVMID, err))
			ObserveTick("error", time.Since(tickStart))
			return
		}
		keep = append(keep, kept[i].ProviderVMID)
	}

	if c.cfg.Reconcile {
		if n, err := c.store.ReconcileVirtualMachines(tickCtx, accountID, keep); err != nil {
			c.reportTickError(ctx, fmt.Errorf("reconcile: %w", err))
			ObserveTick("error", time.Since(tickStart))
			return
		} else if n > 0 {
			slog.Info("vm-collector: reconciled tombstones", slog.Int64("tombstoned", n))
		}
	}

	now := time.Now().UTC()
	if err := c.store.UpdateCloudAccountStatus(tickCtx, accountID, "active", &now, nil); err != nil {
		slog.Warn("vm-collector: status update failed", slog.Any("error", err))
	}
	ObserveTick("success", time.Since(tickStart))
}

// ensureCredentials fetches credentials from argosd; on the first
// attempt it auto-registers the account if missing. Refreshes
// credentials when the cache age exceeds CredentialRefresh.
//
//nolint:gocyclo // bootstrap branching logic is intentionally inline for readability
func (c *Collector) ensureCredentials(ctx context.Context) error {
	c.mu.Lock()
	cachedAccountID := c.accountID
	cachedAt := c.credsAt
	cachedCreds := c.creds
	cachedProvider := c.provider
	c.mu.Unlock()

	if cachedAccountID != uuid.Nil && cachedProvider != nil &&
		!cachedAt.IsZero() && time.Since(cachedAt) < c.cfg.CredentialRefresh {
		return nil
	}

	creds, err := c.store.FetchCredentialsByName(ctx, c.cfg.AccountName)
	if err != nil {
		if !errors.Is(err, apiclient.ErrNotRegistered) {
			ObserveCredentialRefresh("error")
			return fmt.Errorf("fetch credentials: %w", err)
		}
		slog.Info("vm-collector: account not registered yet, registering",
			slog.String("account_name", c.cfg.AccountName))
		acct, regErr := c.store.RegisterCloudAccount(ctx, c.cfg.Provider, c.cfg.AccountName, c.cfg.Region)
		if regErr != nil {
			return fmt.Errorf("register cloud account: %w", regErr)
		}
		slog.Info("vm-collector: registered, awaiting admin to provide credentials",
			slog.String("account_id", acct.ID.String()))
		c.mu.Lock()
		c.accountID = acct.ID
		c.mu.Unlock()
		return ErrCredentialsNotProvisioned
	}

	// We need the account id. If we don't have one yet, register
	// idempotently to recover it from the response.
	if cachedAccountID == uuid.Nil {
		acct, err := c.store.RegisterCloudAccount(ctx, c.cfg.Provider, c.cfg.AccountName, c.cfg.Region)
		if err != nil {
			return fmt.Errorf("register cloud account: %w", err)
		}
		cachedAccountID = acct.ID
	}

	prov, err := c.factory(creds)
	if err != nil {
		return fmt.Errorf("build provider: %w", err)
	}

	c.mu.Lock()
	c.accountID = cachedAccountID
	c.creds = creds
	c.credsAt = time.Now().UTC()
	c.provider = prov
	c.mu.Unlock()

	ObserveCredentialRefresh("success")
	if cachedCreds.AccessKey != creds.AccessKey || cachedCreds.SecretKey != creds.SecretKey {
		slog.Info("vm-collector: credentials refreshed",
			slog.String("account_id", cachedAccountID.String()))
	}
	return nil
}

func (c *Collector) getProvider() provider.Provider {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.provider
}

// getAccountID returns the cached cloud_account UUID under the mutex.
// Today only one goroutine drives the loop, but reading c.accountID
// unsynchronised would trip the race detector the moment ticks are
// parallelised (e.g. per-region) or any future code starts reading
// state from another goroutine.
func (c *Collector) getAccountID() uuid.UUID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accountID
}

func (c *Collector) reportTickError(ctx context.Context, err error) {
	slog.Error("vm-collector tick failed", slog.Any("error", err))
	c.mu.Lock()
	id := c.accountID
	c.mu.Unlock()
	if id == uuid.Nil {
		return
	}
	msg := err.Error()
	statusCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if uerr := c.store.UpdateCloudAccountStatus(statusCtx, id, "error", nil, &msg); uerr != nil {
		slog.Warn("vm-collector: report-error status update failed", slog.Any("error", uerr))
	}
}
