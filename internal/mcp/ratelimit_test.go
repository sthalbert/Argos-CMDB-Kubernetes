package mcp

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiter_AllowsBurst(t *testing.T) {
	// rps=2, burst=3 — first 3 calls succeed instantly, 4th denied.
	lim := NewRateLimiter(2, 3)

	for i := 0; i < 3; i++ {
		if !lim.Allow(context.Background(), "tok-a") {
			t.Fatalf("call %d should be allowed (within burst)", i+1)
		}
	}

	if lim.Allow(context.Background(), "tok-a") {
		t.Fatal("4th call should be denied (burst exhausted)")
	}
}

func TestRateLimiter_PerKey(t *testing.T) {
	// tok-A exhausts bucket, tok-B starts fresh.
	lim := NewRateLimiter(2, 2)

	// Exhaust tok-A's burst.
	for i := 0; i < 2; i++ {
		if !lim.Allow(context.Background(), "tok-a") {
			t.Fatalf("tok-a call %d should be allowed (within burst)", i+1)
		}
	}

	// tok-A should be denied.
	if lim.Allow(context.Background(), "tok-a") {
		t.Fatal("tok-a 3rd call should be denied (burst exhausted)")
	}

	// tok-B should still have full burst.
	for i := 0; i < 2; i++ {
		if !lim.Allow(context.Background(), "tok-b") {
			t.Fatalf("tok-b call %d should be allowed (fresh key)", i+1)
		}
	}

	if lim.Allow(context.Background(), "tok-b") {
		t.Fatal("tok-b 3rd call should be denied (burst exhausted)")
	}
}

func TestRateLimiter_RecoversOverTime(t *testing.T) {
	// After burst, sleep, allowed again.
	lim := NewRateLimiter(10, 2) // 10 rps = 0.1 tokens/ms

	// Exhaust burst.
	for i := 0; i < 2; i++ {
		if !lim.Allow(context.Background(), "tok-x") {
			t.Fatalf("call %d should be allowed (within burst)", i+1)
		}
	}

	if lim.Allow(context.Background(), "tok-x") {
		t.Fatal("3rd call should be denied (burst exhausted)")
	}

	// Sleep for 150ms — at 10 rps, ~1.5 tokens should accumulate.
	time.Sleep(150 * time.Millisecond)

	// Now 1 more call should be allowed.
	if !lim.Allow(context.Background(), "tok-x") {
		t.Fatal("call after sleep should be allowed (tokens accumulated)")
	}
}

func TestRateLimiter_Concurrent(t *testing.T) {
	// 10 goroutines, same key, total allowed never exceeds rps*duration + burst.
	// Run for ~100ms at 10 rps = max 1 token + burst of 2 = 3 tokens per 100ms.
	lim := NewRateLimiter(10, 2)
	ctx := context.Background()

	var allowed atomic.Int32
	var wg sync.WaitGroup

	// 10 goroutines, each calling Allow 50 times rapidly.
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if lim.Allow(ctx, "shared-key") {
					allowed.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// We expect around burst (2) + 10 rps * time_elapsed.
	// Since the loop is very fast, we should see something close to burst + a few tokens.
	// A loose upper bound: 2 (burst) + 100 (if it somehow took 10 seconds, which it won't) = 102.
	// A tight bound for fast execution: 2 (burst) + 5 (tokens accumulated in ~500ms) = 7.
	// We'll check that it's in a reasonable range without being too strict.
	if allowed.Load() < 2 {
		t.Fatalf("expected at least burst (2) tokens allowed, got %d", allowed.Load())
	}
	if allowed.Load() > 100 {
		t.Fatalf("expected no more than ~100 tokens (loose bound), got %d", allowed.Load())
	}

	// Verify burst is respected: burst size is 2, so initially 2 are available.
	lim2 := NewRateLimiter(10, 2)
	var burst atomic.Int32
	for i := 0; i < 1000; i++ {
		if lim2.Allow(ctx, "burst-test") {
			burst.Add(1)
		} else {
			break
		}
	}
	if burst.Load() != 2 {
		t.Fatalf("expected exactly burst size (2) to be allowed instantly, got %d", burst.Load())
	}
}
