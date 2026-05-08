package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	maxPages    = 50
	httpTimeout = 30 * time.Second
)

// Sentinel errors returned by ListTags. Callers should compare with errors.Is.
var (
	ErrRepoNotFound     = errors.New("registry: repo not found")
	ErrRateLimited      = errors.New("registry: rate limited")
	ErrRegistryStatus   = errors.New("registry: unexpected status")
	ErrInvalidChallenge = errors.New("registry: invalid bearer challenge")
	ErrMissingRealm     = errors.New("registry: bearer challenge missing realm")
	ErrTokenFetchStatus = errors.New("registry: token fetch returned non-2xx")
)

// QueryObserver receives one record per outbound tag-list call. Used to
// expose Prometheus metrics without creating an import cycle (the metrics
// type lives in the parent imageversions package).
type QueryObserver interface {
	ObserveQuery(registry, status string, durationSeconds float64)
}

// Client is a thin OCI-distribution client supporting anonymous-bearer
// auth and Link-header pagination.
type Client struct {
	http      *http.Client
	userAgent string
	observer  QueryObserver
}

// NewClient returns a Client with sensible defaults: 30s timeout, identifying
// User-Agent, no transport overrides.
func NewClient() *Client {
	return &Client{
		http:      &http.Client{Timeout: httpTimeout},
		userAgent: "longue-vue (image-versions-enricher)",
	}
}

// NewClientWithObserver constructs a Client with the given observer.
// Pass nil to disable metric instrumentation.
func NewClientWithObserver(o QueryObserver) *Client {
	c := NewClient()
	c.observer = o
	return c
}

// ListTags fetches all tags for repo from the given registryURL
// (e.g., "https://registry-1.docker.io"). Paginated responses are
// followed until the Link header is exhausted or maxPages is reached.
// Returns ErrRepoNotFound on 404 and ErrRateLimited on 429.
func (c *Client) ListTags(ctx context.Context, registryURL, repo string) ([]string, error) {
	next := strings.TrimRight(registryURL, "/") + "/v2/" + repo + "/tags/list?n=100"
	var token string
	var allTags []string
	for page := 0; page < maxPages && next != ""; page++ {
		tags, link, newToken, err := c.fetchOnePage(ctx, next, token)
		if err != nil {
			return nil, err
		}
		if newToken != "" {
			// 401 path: retry the same URL with the freshly issued token.
			token = newToken
			continue
		}
		allTags = append(allTags, tags...)
		next = parseNextLink(link, registryURL)
	}
	return allTags, nil
}

// observe records a single registry call to the observer if one is configured.
func (c *Client) observe(host, status string, elapsed float64) {
	if c.observer != nil {
		c.observer.ObserveQuery(host, status, elapsed)
	}
}

// classifyStatus turns an HTTP status into an observer status label and a
// sentinel error. Returns ("success", nil) for 2xx responses.
func classifyStatus(code int, body []byte) (label string, statusErr error) {
	switch {
	case code == http.StatusNotFound:
		return "not_found", ErrRepoNotFound
	case code == http.StatusTooManyRequests:
		return "rate_limited", ErrRateLimited
	case code >= 400:
		return "error", fmt.Errorf("%w: status %d: %s", ErrRegistryStatus, code, string(body))
	}
	return "success", nil
}

// fetchOnePage performs one HTTP request and decodes the body. When the
// registry replies 401 and we don't yet have a token, fetchOnePage fetches
// one and returns it via newToken so the caller can retry — the returned
// tags/link are zero in that case. All other non-2xx statuses are classified
// into a sentinel error.
func (c *Client) fetchOnePage(ctx context.Context, pageURL, token string) (tags []string, link, newToken string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, http.NoBody)
	if err != nil {
		return nil, "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	qStart := time.Now()
	resp, err := c.http.Do(req)
	elapsed := time.Since(qStart).Seconds()
	if err != nil {
		c.observe(req.URL.Host, "error", elapsed)
		return nil, "", "", fmt.Errorf("http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized && token == "" {
		c.observe(req.URL.Host, "error", elapsed)
		t, terr := c.fetchToken(ctx, resp.Header.Get("WWW-Authenticate"))
		if terr != nil {
			return nil, "", "", fmt.Errorf("token: %w", terr)
		}
		return nil, "", t, nil
	}

	if resp.StatusCode >= 400 {
		// Non-2xx: read a bounded body snippet for the error message,
		// classify, observe, return. The body is consumed; do not let
		// the decode path below run.
		label, statusErr := classifyStatus(resp.StatusCode, drainBody(resp))
		c.observe(req.URL.Host, label, elapsed)
		return nil, "", "", statusErr
	}
	c.observe(req.URL.Host, "success", elapsed)

	var body struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, "", "", fmt.Errorf("decode: %w", err)
	}
	return body.Tags, resp.Header.Get("Link"), "", nil
}

// drainBody reads the response body up to a sane cap so error messages can
// echo a snippet without exhausting memory on a hostile/large body.
func drainBody(resp *http.Response) []byte {
	const bodyCap = 4 << 10
	b, _ := io.ReadAll(io.LimitReader(resp.Body, bodyCap))
	return b
}

var bearerRealmRe = regexp.MustCompile(`Bearer\s+(.+)`)

// fetchToken interprets a WWW-Authenticate Bearer challenge and fetches
// an anonymous token from the realm endpoint.
func (c *Client) fetchToken(ctx context.Context, challenge string) (string, error) {
	tokenURL, err := tokenEndpointURL(challenge)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: status %d", ErrTokenFetchStatus, resp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
}

// tokenEndpointURL builds the token endpoint URL from a WWW-Authenticate
// Bearer challenge by extracting realm/service/scope and re-encoding them
// as query parameters on the realm URL.
func tokenEndpointURL(challenge string) (string, error) {
	m := bearerRealmRe.FindStringSubmatch(challenge)
	if m == nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidChallenge, challenge)
	}
	params := parseChallengeParams(m[1])
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("%w: %q", ErrMissingRealm, challenge)
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("parse realm: %w", err)
	}
	q := u.Query()
	if s, ok := params["service"]; ok {
		q.Set("service", s)
	}
	if s, ok := params["scope"]; ok {
		q.Set("scope", s)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// parseChallengeParams parses comma-separated key="value" pairs found in
// a WWW-Authenticate Bearer challenge.
func parseChallengeParams(s string) map[string]string {
	out := map[string]string{}
	for _, part := range splitChallenge(s) {
		eq := strings.Index(part, "=")
		if eq < 0 {
			continue
		}
		k := strings.TrimSpace(part[:eq])
		v := strings.Trim(strings.TrimSpace(part[eq+1:]), `"`)
		out[k] = v
	}
	return out
}

// splitChallenge splits on commas that are not inside quotes.
func splitChallenge(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

var nextLinkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// parseNextLink extracts the next-page URL from a Link header. base is
// used as the absolute prefix when the candidate URL is path-relative.
func parseNextLink(header, base string) string {
	if header == "" {
		return ""
	}
	m := nextLinkRe.FindStringSubmatch(header)
	if m == nil {
		return ""
	}
	candidate := m[1]
	if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
		return candidate
	}
	return strings.TrimRight(base, "/") + candidate
}
