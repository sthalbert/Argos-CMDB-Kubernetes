package imageversions

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/imageversions/registry"
)

const sourceRegistry = "registry"

// Enricher periodically queries public registries for the latest tag of
// each image used in K8s clusters.
type Enricher struct {
	store    Store
	lister   TagsLister
	interval time.Duration

	triggerCh chan struct{}
	running   atomic.Bool
}

// NewEnricher constructs an Enricher. interval controls the periodic tick;
// the trigger channel is buffered (capacity 1) so a manual trigger never
// blocks.
func NewEnricher(s Store, lister TagsLister, interval time.Duration) *Enricher {
	return &Enricher{
		store:     s,
		lister:    lister,
		interval:  interval,
		triggerCh: make(chan struct{}, 1),
	}
}

// Run blocks until ctx is cancelled, executing one tick per interval and
// also responding to manual triggers. Returns ctx.Err() on cancellation.
func (e *Enricher) Run(ctx context.Context) error {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	e.RunTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			e.RunTick(ctx)
		case <-e.triggerCh:
			e.RunTick(ctx)
		}
	}
}

// Trigger queues an immediate tick. Returns true if a tick is already
// running or pending (and the new trigger was a no-op).
func (e *Enricher) Trigger() (alreadyRunning bool) {
	if e.running.Load() {
		return true
	}
	select {
	case e.triggerCh <- struct{}{}:
		return false
	default:
		return true
	}
}

// IsRunning reports whether a tick is currently in progress.
func (e *Enricher) IsRunning() bool {
	return e.running.Load()
}

// tickResult holds a single enrichment result to be upserted into the store.
type tickResult struct {
	upsert api.ImageVersionUpsert
}

// RunTick executes one full enrichment cycle. Exposed for tests; the long
// loop in Run() calls it.
func (e *Enricher) RunTick(ctx context.Context) {
	if !e.running.CompareAndSwap(false, true) {
		return
	}
	defer e.running.Store(false)

	settings, err := e.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("imageversions: get settings failed", slog.String("err", err.Error()))
		return
	}
	if !settings.ImageVersionsEnabled {
		return
	}

	tickStart := time.Now()
	regs, err := e.store.ListImageRegistries(ctx)
	if err != nil {
		slog.Warn("imageversions: list registries failed", slog.String("err", err.Error()))
		return
	}
	enabledRegs := make([]api.ImageRegistry, 0, len(regs))
	for _, r := range regs {
		if r.Enabled {
			enabledRegs = append(enabledRegs, r)
		}
	}
	// If the admin has disabled every registry while leaving the feature
	// toggle on, don't run a tick that would reap every existing row.
	// Preserve the data and wait for the operator to re-enable a registry.
	if len(enabledRegs) == 0 {
		slog.Info("imageversions: no enabled registries; skipping tick")
		return
	}

	refs, err := e.store.DistinctImageRefs(ctx)
	if err != nil {
		slog.Warn("imageversions: distinct refs failed", slog.String("err", err.Error()))
		return
	}

	// discovered: repo -> set of variants observed in the cluster.
	discovered := map[string]map[string]struct{}{}
	// repoRegistry: repo -> matched registry hostname (for rate-limiter selection and upsert).
	repoRegistry := map[string]string{}

	for _, raw := range refs {
		ref, err := ParseImageRef(raw)
		if err != nil {
			slog.Debug("imageversions: skip ref", slog.String("ref", raw), slog.String("reason", err.Error()))
			continue
		}
		// Match against enabled allowlist.
		var matched *api.ImageRegistry
		for i := range enabledRegs {
			if registry.Match(enabledRegs[i].Hostname, ref.Registry) {
				matched = &enabledRegs[i]
				break
			}
		}
		if matched == nil {
			slog.Debug("imageversions: registry not allowlisted",
				slog.String("registry", ref.Registry), slog.String("ref", raw))
			continue
		}
		pt, err := ParseTag(ref.Tag)
		if err != nil {
			slog.Debug("imageversions: skip tag", slog.String("ref", raw), slog.String("reason", err.Error()))
			continue
		}
		if _, ok := discovered[ref.ImageRepo]; !ok {
			discovered[ref.ImageRepo] = map[string]struct{}{}
		}
		discovered[ref.ImageRepo][pt.Variant] = struct{}{}
		repoRegistry[ref.ImageRepo] = ref.Registry
	}

	// Per-registry rate limiters keyed by allowlist hostname.
	limiters := map[string]*rate.Limiter{}
	for _, r := range enabledRegs {
		limiters[r.Hostname] = rate.NewLimiter(rate.Limit(r.RateLimitPerSec), 1)
	}
	pickLimiter := func(reg string) *rate.Limiter {
		for h, l := range limiters {
			if registry.Match(h, reg) {
				return l
			}
		}
		return nil
	}

	sem := make(chan struct{}, 5) // bounded worker pool
	results := make(chan tickResult)
	var wg sync.WaitGroup

	for repo, variants := range discovered {
		repo, variants := repo, variants
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			reg := repoRegistry[repo]
			url, repoPath, err := registry.EffectiveHost(repo)
			if err != nil {
				emitError(results, repo, variants, reg, err)
				return
			}
			if l := pickLimiter(reg); l != nil {
				if err := l.Wait(ctx); err != nil {
					return
				}
			}
			tags, err := e.lister.ListTags(ctx, url, repoPath)
			if err != nil {
				emitError(results, repo, variants, reg, err)
				return
			}
			now := time.Now().UTC()
			for v := range variants {
				latest, _ := ComputeLatest(v, tags)
				ann, _ := buildAnnotation(strPtrIfNonEmpty(latest), nil)
				up := api.ImageVersionUpsert{
					ImageRepo:     repo,
					Variant:       v,
					Registry:      reg,
					LatestTag:     strPtrIfNonEmpty(latest),
					Annotation:    ann,
					Source:        sourceRegistry,
					LastCheckedAt: now,
				}
				results <- tickResult{upsert: up}
			}
		}()
	}

	go func() { wg.Wait(); close(results) }()

	var processed [][2]string
	for r := range results {
		if _, err := e.store.UpsertImageVersion(ctx, r.upsert); err != nil {
			slog.Warn("imageversions: upsert failed",
				slog.String("repo", r.upsert.ImageRepo),
				slog.String("err", err.Error()))
		}
		processed = append(processed, [2]string{r.upsert.ImageRepo, r.upsert.Variant})
	}

	reaped, err := e.store.DeleteImageVersionsNotIn(ctx, processed)
	if err != nil {
		slog.Warn("imageversions: reap failed", slog.String("err", err.Error()))
	}
	slog.Info("imageversions: tick complete",
		slog.Int("discovered_refs", len(refs)),
		slog.Int("processed_rows", len(processed)),
		slog.Int64("reaped_rows", reaped),
		slog.Duration("duration", time.Since(tickStart)),
	)
}

// emitError pushes one error-marked upsert per known variant for the given repo.
func emitError(results chan<- tickResult, repo string, variants map[string]struct{}, reg string, err error) {
	now := time.Now().UTC()
	msg := err.Error()
	var classified string
	switch {
	case errors.Is(err, registry.ErrRepoNotFound):
		classified = "repo not found"
	case errors.Is(err, registry.ErrRateLimited):
		classified = "rate limited"
	default:
		classified = msg
	}
	ann, _ := buildAnnotation(nil, &classified)
	for v := range variants {
		results <- tickResult{
			upsert: api.ImageVersionUpsert{
				ImageRepo:     repo,
				Variant:       v,
				Registry:      reg,
				LatestTag:     nil,
				Annotation:    ann,
				Source:        sourceRegistry,
				LastCheckedAt: now,
				LastError:     &classified,
				LastErrorAt:   &now,
			},
		}
	}
}

func strPtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// buildAnnotation produces an annotation JSONB with latest_available and
// eol_status fields. In V1 we only fill the latest_available field (and a
// sentinel eol_status="unknown"). The schema is forward-compatible with the
// richer fields planned for V3.
func buildAnnotation(latestAvailable *string, errMsg *string) (json.RawMessage, error) {
	obj := map[string]any{
		"eol_status": "unknown",
	}
	if latestAvailable != nil {
		obj["latest_available"] = *latestAvailable
	}
	if errMsg != nil {
		obj["error"] = *errMsg
	}
	return json.Marshal(obj)
}
