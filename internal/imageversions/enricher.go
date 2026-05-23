package imageversions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
	"github.com/sthalbert/longue-vue/internal/imageversions/registry"
)

const sourceRegistry = "registry"

// Enricher periodically queries public registries for the latest tag of
// each image used in K8s clusters.
type Enricher struct {
	store    Store
	lister   TagsLister
	resolver mirrorresolve.Resolver
	interval time.Duration
	metrics  *Metrics

	triggerCh chan struct{}
	running   atomic.Bool

	// pendingResolutionKeys accumulates the (mirror_image_repo, variant)
	// pairs persisted during the current tick. Reset at the top of every
	// RunTick; used as the keep-set for reaping image_origin_resolutions.
	// Safe to mutate without locking: only one tick runs at a time
	// (running atomic.Bool guards re-entry).
	pendingResolutionKeys [][2]string
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

// NewEnricherWithMetrics constructs an Enricher with Prometheus instrumentation.
// The metrics argument may be nil to disable instrumentation (equivalent to
// calling NewEnricher).
func NewEnricherWithMetrics(s Store, lister TagsLister, interval time.Duration, m *Metrics) *Enricher {
	return &Enricher{
		store:     s,
		lister:    lister,
		interval:  interval,
		metrics:   m,
		triggerCh: make(chan struct{}, 1),
	}
}

// NewEnricherWithResolver constructs an Enricher with a mirror resolver and
// optional metrics. The resolver may be nil (passthrough).
func NewEnricherWithResolver(s Store, lister TagsLister, resolver mirrorresolve.Resolver, interval time.Duration, m *Metrics) *Enricher {
	return &Enricher{
		store:     s,
		lister:    lister,
		resolver:  resolver,
		interval:  interval,
		metrics:   m,
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
			return fmt.Errorf("imageversions enricher stopped: %w", ctx.Err())
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

	e.pendingResolutionKeys = nil

	start := time.Now()
	defer func() {
		if e.metrics != nil {
			e.metrics.TickDuration.Observe(time.Since(start).Seconds())
			e.metrics.LastTickTimestamp.Set(float64(time.Now().Unix()))
		}
	}()

	enabledRegs, ok := e.loadEnabledRegistries(ctx)
	if !ok {
		return
	}

	refs, err := e.store.DistinctImageRefs(ctx)
	if err != nil {
		slog.Warn("imageversions: distinct refs failed", slog.String("err", err.Error()))
		return
	}
	discovered, repoRegistry := e.discoverWork(ctx, refs, enabledRegs)

	processed := e.dispatchWorkers(ctx, discovered, repoRegistry, enabledRegs)

	reaped, err := e.store.DeleteImageVersionsNotIn(ctx, processed)
	if err != nil {
		slog.Warn("imageversions: reap failed", slog.String("err", err.Error()))
	}
	if _, err := e.store.DeleteImageOriginResolutionsNotIn(ctx, e.pendingResolutionKeys); err != nil {
		slog.Warn("imageversions: reap origin resolutions failed",
			slog.String("err", err.Error()))
	}
	slog.Info(
		"imageversions: tick complete",
		slog.Int("discovered_refs", len(refs)),
		slog.Int("processed_rows", len(processed)),
		slog.Int64("reaped_rows", reaped),
		slog.Duration("duration", time.Since(start)),
	)
	if e.metrics != nil {
		e.metrics.TickTotal.WithLabelValues("success").Inc()
		// KnownTotal is an approximation from the in-memory processed slice.
		// WithErrorTotal is deferred to V2 when a CountImageVersions store
		// method is added (tracking V1 gap: only approximate row counts available).
		e.metrics.KnownTotal.Set(float64(len(processed)))
	}
}

// loadEnabledRegistries reads the settings toggle and the registry allowlist.
// Returns (registries, true) when work should proceed, (nil, false) when the
// tick must short-circuit (feature disabled, store error, or no enabled rows).
func (e *Enricher) loadEnabledRegistries(ctx context.Context) ([]api.ImageRegistry, bool) {
	settings, err := e.store.GetSettings(ctx)
	if err != nil {
		slog.Warn("imageversions: get settings failed", slog.String("err", err.Error()))
		return nil, false
	}
	if !settings.ImageVersionsEnabled {
		return nil, false
	}
	regs, err := e.store.ListImageRegistries(ctx)
	if err != nil {
		slog.Warn("imageversions: list registries failed", slog.String("err", err.Error()))
		return nil, false
	}
	enabled := make([]api.ImageRegistry, 0, len(regs))
	for i := range regs {
		r := &regs[i]
		// Mirror rows participate only in the resolver path (ADR-0026);
		// they must not appear in the public-registry allowlist iteration.
		if r.Enabled && !r.IsMirror {
			enabled = append(enabled, *r)
		}
	}
	if e.metrics != nil {
		e.metrics.RegistriesEnabledTotal.Set(float64(len(enabled)))
	}
	// If the admin has disabled every registry while leaving the feature
	// toggle on, don't run a tick that would reap every existing row.
	if len(enabled) == 0 {
		slog.Info("imageversions: no enabled registries; skipping tick")
		return nil, false
	}
	return enabled, true
}

// discoverWork parses raw image refs, matches each against the allowlist,
// and groups them by (image_repo, variant). Returns the per-repo variant set
// and the per-repo matched registry hostname.
func (e *Enricher) discoverWork(ctx context.Context, refs []string, enabledRegs []api.ImageRegistry) (
	discovered map[string]map[string]struct{},
	repoRegistry map[string]string,
) {
	discovered = map[string]map[string]struct{}{}
	repoRegistry = map[string]string{}
	for _, raw := range refs {
		resolved, skip := e.resolveMirrorRef(ctx, raw)
		if skip {
			continue
		}
		ref, err := ParseImageRef(resolved)
		if err != nil {
			slog.Debug("imageversions: skip ref", slog.String("ref", raw), slog.String("reason", err.Error()))
			continue
		}
		if !matchAnyRegistry(enabledRegs, ref.Registry) {
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
	return discovered, repoRegistry
}

// resolveMirrorRef runs the mirror resolver if configured. Returns the
// resolved ref and skip=true when the caller must drop this ref.
// Persists both successes and classified failures into
// image_origin_resolutions so the GET-time join in
// internal/api/containers_versions_enrich.go can surface origin info.
func (e *Enricher) resolveMirrorRef(ctx context.Context, raw string) (string, bool) {
	if e.resolver == nil {
		return raw, false
	}
	resolved, err := e.resolver.Resolve(ctx, raw)
	if err == nil {
		e.persistResolution(ctx, raw, resolved, "")
		return resolved, false
	}
	classified := classifyResolverErr(err)
	if classified == "" {
		// Unclassified error (e.g. fetch_error during a one-off network blip):
		// log and skip without persisting; next tick retries.
		slog.Warn("imageversions: mirror resolve error",
			slog.String("ref", raw), slog.String("err", err.Error()))
		return "", true
	}
	slog.Debug("imageversions: skip mirror ref",
		slog.String("ref", raw), slog.String("reason", classified))
	e.persistResolution(ctx, raw, "", classified)
	return "", true
}

// classifyResolverErr maps a resolver error to the stable string we
// persist as last_error and emit as origin_error on the API response.
// Returns "" for errors that should be retried next tick without
// persisting a failure row.
func classifyResolverErr(err error) string {
	switch {
	case errors.Is(err, mirrorresolve.ErrNoOriginAnnotation):
		return "missing_annotation"
	case errors.Is(err, mirrorresolve.ErrAmbiguousAnnotation):
		return "ambiguous_annotation"
	case errors.Is(err, mirrorresolve.ErrChainTooDeep):
		return "chain_too_deep"
	case errors.Is(err, mirrorresolve.ErrReplicaTargetMissing):
		return "replica_target_missing"
	}
	return ""
}

// persistResolution writes a success or failure row for the given raw
// pod ref. classifiedErr=="" means success (resolved != "" required);
// non-empty classifiedErr means failure (resolved is ignored). Logs but
// does not surface store errors — the next tick retries.
func (e *Enricher) persistResolution(ctx context.Context, raw, resolved, classifiedErr string) {
	mirrorRef, err := ParseImageRef(raw)
	if err != nil {
		return
	}
	pt, err := ParseTag(mirrorRef.Tag)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	up := api.ImageOriginResolutionUpsert{
		MirrorImageRepo: mirrorRef.ImageRepo,
		Variant:         pt.Variant,
		ResolvedAt:      now,
	}
	if classifiedErr != "" {
		errCopy := classifiedErr
		up.LastError = &errCopy
		up.LastErrorAt = &now
	} else if resolved != "" {
		originRef, err := ParseImageRef(resolved)
		if err != nil {
			return
		}
		up.OriginImageRepo = &originRef.ImageRepo
		// via_hostname = the resolved ref's registry (the annotation mirror
		// at the end of the chain).
		up.ViaHostname = &originRef.Registry
	}
	e.pendingResolutionKeys = append(e.pendingResolutionKeys, [2]string{mirrorRef.ImageRepo, pt.Variant})
	if _, err := e.store.UpsertImageOriginResolution(ctx, up); err != nil {
		slog.Warn("imageversions: persist origin resolution",
			slog.String("mirror_ref", raw), slog.String("err", err.Error()))
	}
}

// matchAnyRegistry reports whether the candidate registry matches any
// enabled allowlist entry.
func matchAnyRegistry(enabledRegs []api.ImageRegistry, candidate string) bool {
	for i := range enabledRegs {
		if registry.Match(enabledRegs[i].Hostname, candidate) {
			return true
		}
	}
	return false
}

// dispatchWorkers fans out one goroutine per image_repo (bounded to 5
// concurrent), upserts every result, and returns the processed key set
// for the reap step.
func (e *Enricher) dispatchWorkers(
	ctx context.Context,
	discovered map[string]map[string]struct{},
	repoRegistry map[string]string,
	enabledRegs []api.ImageRegistry,
) [][2]string {
	limiters := map[string]*rate.Limiter{}
	for i := range enabledRegs {
		r := &enabledRegs[i]
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

	sem := make(chan struct{}, 5)
	results := make(chan tickResult)
	var wg sync.WaitGroup

	for repo, variants := range discovered {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			e.queryRepo(ctx, repo, variants, repoRegistry[repo], pickLimiter, results)
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
	return processed
}

// queryRepo runs the registry call for a single image_repo and emits one
// tickResult per variant onto results. Errors from EffectiveHost or ListTags
// are reported as error-marked results (preserving each variant in the
// upsert set so reap won't drop them).
func (e *Enricher) queryRepo(
	ctx context.Context,
	repo string,
	variants map[string]struct{},
	reg string,
	pickLimiter func(string) *rate.Limiter,
	results chan<- tickResult,
) {
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
		results <- tickResult{
			upsert: api.ImageVersionUpsert{
				ImageRepo:     repo,
				Variant:       v,
				Registry:      reg,
				LatestTag:     strPtrIfNonEmpty(latest),
				Annotation:    ann,
				Source:        sourceRegistry,
				LastCheckedAt: now,
			},
		}
	}
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
func buildAnnotation(latestAvailable, errMsg *string) (json.RawMessage, error) {
	obj := map[string]any{
		"eol_status": "unknown",
	}
	if latestAvailable != nil {
		obj["latest_available"] = *latestAvailable
	}
	if errMsg != nil {
		obj["error"] = *errMsg
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("marshal annotation: %w", err)
	}
	return b, nil
}
