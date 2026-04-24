package eol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Client fetches product lifecycle data from the endoflife.date API.
// It maintains an in-memory cache per product with a configurable TTL.
type Client struct {
	baseURL    string
	httpClient *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	cycles    []Cycle
	fetchedAt time.Time
}

// NewClient creates a Client pointing at baseURL (typically
// "https://endoflife.date"). The cache TTL should match the enricher
// interval — lifecycle data changes slowly.
func NewClient(baseURL string, ttl time.Duration, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:    baseURL,
		httpClient: httpClient,
		cache:      make(map[string]cacheEntry),
		ttl:        ttl,
	}
}

// GetProduct returns all release cycles for the given product
// (e.g. "kubernetes", "ubuntu"). Results are cached for the
// configured TTL.
func (c *Client) GetProduct(ctx context.Context, product string) ([]Cycle, error) {
	c.mu.Lock()
	if entry, ok := c.cache[product]; ok && time.Since(entry.fetchedAt) < c.ttl {
		c.mu.Unlock()
		return entry.cycles, nil
	}
	c.mu.Unlock()

	url := fmt.Sprintf("%s/api/%s.json", c.baseURL, product)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", product, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", product, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", product, ErrProductNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s status %d: %w", product, resp.StatusCode, ErrUnexpectedStatus)
	}

	var cycles []Cycle
	if err := json.NewDecoder(resp.Body).Decode(&cycles); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", product, err)
	}

	c.mu.Lock()
	c.cache[product] = cacheEntry{cycles: cycles, fetchedAt: time.Now()}
	c.mu.Unlock()

	return cycles, nil
}

// FindCycle looks up the best-matching cycle for the given version
// string (e.g. "1.28"). Returns nil when no matching cycle exists.
func FindCycle(cycles []Cycle, version string) *Cycle {
	for i := range cycles {
		if cycles[i].Cycle == version {
			return &cycles[i]
		}
	}
	return nil
}
