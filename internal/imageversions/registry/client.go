package registry

import (
	"context"
	"encoding/json"
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

// ErrRepoNotFound is returned when the registry responds 404 for a tags/list request.
var ErrRepoNotFound = fmt.Errorf("registry: repo not found")

// ErrRateLimited is returned when the registry responds 429.
var ErrRateLimited = fmt.Errorf("registry: rate limited")

// Client is a thin OCI-distribution client supporting anonymous-bearer
// auth and Link-header pagination.
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient returns a Client with sensible defaults: 30s timeout, identifying
// User-Agent, no transport overrides.
func NewClient() *Client {
	return &Client{
		http:      &http.Client{Timeout: httpTimeout},
		userAgent: "longue-vue (image-versions-enricher)",
	}
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("User-Agent", c.userAgent)
		req.Header.Set("Accept", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("http: %w", err)
		}
		if resp.StatusCode == http.StatusUnauthorized && token == "" {
			chal := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			t, err := c.fetchToken(ctx, chal)
			if err != nil {
				return nil, fmt.Errorf("token: %w", err)
			}
			token = t
			continue // retry same URL with token
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return nil, ErrRepoNotFound
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			return nil, ErrRateLimited
		}
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("registry status %d: %s", resp.StatusCode, string(body))
		}

		var body struct {
			Tags []string `json:"tags"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode: %w", err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()
		allTags = append(allTags, body.Tags...)
		next = parseNextLink(link, registryURL)
	}
	return allTags, nil
}

var bearerRealmRe = regexp.MustCompile(`Bearer\s+(.+)`)

// fetchToken interprets a WWW-Authenticate Bearer challenge and fetches
// an anonymous token from the realm endpoint.
func (c *Client) fetchToken(ctx context.Context, challenge string) (string, error) {
	m := bearerRealmRe.FindStringSubmatch(challenge)
	if m == nil {
		return "", fmt.Errorf("not a bearer challenge: %q", challenge)
	}
	params := parseChallengeParams(m[1])
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("missing realm in challenge: %q", challenge)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("token fetch status %d", resp.StatusCode)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", err
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
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
