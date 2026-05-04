package mcp

import (
	"sync"
	"testing"
	"time"
)

func TestAuthCache_LRUEvicts(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(2, time.Minute)
	c.Put("aaa", "full-aaa", &MCPCaller{Name: "a"})
	c.Put("bbb", "full-bbb", &MCPCaller{Name: "b"})
	c.Put("ccc", "full-ccc", &MCPCaller{Name: "c"}) // should evict "aaa"
	if c.Len() != 2 {
		t.Fatalf("want len 2, got %d", c.Len())
	}
	if _, ok := c.Get("aaa", "full-aaa"); ok {
		t.Error("aaa should have been evicted")
	}
	if _, ok := c.Get("bbb", "full-bbb"); !ok {
		t.Error("bbb should still be present")
	}
	if _, ok := c.Get("ccc", "full-ccc"); !ok {
		t.Error("ccc should be present")
	}
}

func TestAuthCache_LRURecency(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(2, time.Minute)
	c.Put("aaa", "full-aaa", &MCPCaller{Name: "a"})
	c.Put("bbb", "full-bbb", &MCPCaller{Name: "b"})
	// Access aaa so it becomes most-recently-used.
	c.Get("aaa", "full-aaa")
	// Insert ccc — should evict bbb (oldest), not aaa.
	c.Put("ccc", "full-ccc", &MCPCaller{Name: "c"})
	if _, ok := c.Get("bbb", "full-bbb"); ok {
		t.Error("bbb should have been evicted (least recently used)")
	}
	if _, ok := c.Get("aaa", "full-aaa"); !ok {
		t.Error("aaa should still be present (recently used)")
	}
}

func TestAuthCache_Invalidate(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(10, time.Minute)
	c.Put("aaa", "full-aaa", &MCPCaller{Name: "a"})
	c.Invalidate("aaa")
	if _, ok := c.Get("aaa", "full-aaa"); ok {
		t.Error("invalidated entry should not be findable")
	}
	if c.Len() != 0 {
		t.Errorf("want len 0 after invalidate, got %d", c.Len())
	}
}

func TestAuthCache_TTLExpiry(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(10, 1*time.Millisecond)
	c.Put("aaa", "full-aaa", &MCPCaller{Name: "a"})
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get("aaa", "full-aaa"); ok {
		t.Error("expired entry should not be returned")
	}
}

func TestAuthCache_WrongFullToken(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(10, time.Minute)
	c.Put("aaa", "full-aaa-correct", &MCPCaller{Name: "a"})
	if _, ok := c.Get("aaa", "full-aaa-wrong!!"); ok {
		t.Error("wrong full token should be rejected")
	}
}

func TestAuthCache_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(16, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			prefix := "pref"
			full := "full-token"
			c.Put(prefix, full, &MCPCaller{Name: "concurrent"})
			c.Get(prefix, full)
			c.Invalidate(prefix)
			c.Len()
		}(i)
	}
	wg.Wait()
}

func TestAuthCache_NilCallerSafe(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(10, time.Minute)
	// Must not panic.
	c.Put("aaa", "full-aaa", nil)
	caller, ok := c.Get("aaa", "full-aaa")
	if !ok {
		t.Error("entry should be found")
	}
	if caller != nil {
		t.Error("nil caller should be returned as nil")
	}
}

func TestAuthCache_RevocationSubscriber(t *testing.T) {
	t.Parallel()
	c := NewAuthCache(10, time.Minute)
	c.Put("tok", "full-tok", &MCPCaller{Name: "test"})

	revokeCh := make(chan string, 1)
	// Simulate the subscriber goroutine wired in main.go.
	go func() {
		for prefix := range revokeCh {
			c.Invalidate(prefix)
		}
	}()

	revokeCh <- "tok"
	// Give the goroutine time to process.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, ok := c.Get("tok", "full-tok"); !ok {
			return // success
		}
		time.Sleep(1 * time.Millisecond)
	}
	t.Error("entry should have been invalidated after revocation signal")
}
