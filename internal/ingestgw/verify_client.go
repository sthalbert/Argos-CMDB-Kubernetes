package ingestgw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrVerifyDenied is returned by VerifyClient.Verify when longue-vue
// responds 401 (token invalid / unknown / expired / revoked). The
// gateway uses errors.Is to distinguish a valid-but-rejected token
// from a transport / 5xx failure.
var ErrVerifyDenied = errors.New("verify denied")

// VerifyClient calls longue-vue's POST /v1/auth/verify (ADR-0016 §5).
// Reuses the gateway's mTLS *http.Client so the verify call rides the
// same connection pool as forwarded writes — one keepalive, one cert
// rotation event reaches both code paths.
type VerifyClient struct {
	client      *http.Client
	endpointURL string // e.g. "https://longue-vue-ingest.longue-vue.svc:8443/v1/auth/verify"
}

// NewVerifyClient wires a verify client against the given upstream base
// URL (no trailing slash) and HTTP client.
func NewVerifyClient(client *http.Client, upstreamBaseURL string) *VerifyClient {
	return &VerifyClient{
		client:      client,
		endpointURL: upstreamBaseURL + "/v1/auth/verify",
	}
}

// verifyRequestBody / verifyResponseBody mirror the OpenAPI shapes
// without importing internal/api — keeps the gateway binary lean and
// independent of longue-vue's codegen output.
type verifyRequestBody struct {
	Token string `json:"token"`
}

type verifyResponseBody struct {
	Valid               bool     `json:"valid"`
	CallerID            string   `json:"caller_id,omitempty"`
	Kind                string   `json:"kind"`
	TokenName           string   `json:"token_name,omitempty"`
	Scopes              []string `json:"scopes"`
	BoundCloudAccountID string   `json:"bound_cloud_account_id,omitempty"`
	Exp                 int64    `json:"exp,omitempty"`
}

// Verify calls longue-vue's verify endpoint and returns a decoded result
// plus the token's own expiry (zero when the token does not expire).
//
// Returns:
//   - (entry, exp, nil) on longue-vue 200 — caller stores in the positive cache.
//   - (CachedToken{}, time.Time{}, ErrVerifyDenied) on longue-vue 401 — caller
//     stores in the negative cache.
//   - (CachedToken{}, time.Time{}, transport / 5xx error) — caller does
//     NOT cache and surfaces 503 to the collector.
func (v *VerifyClient) Verify(ctx context.Context, token string) (CachedToken, time.Time, error) {
	body, err := json.Marshal(verifyRequestBody{Token: token}) //nolint:errchkjson // struct is always marshalable; error check kept for safety
	if err != nil {
		return CachedToken{}, time.Time{}, fmt.Errorf("marshal verify body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpointURL, bytes.NewReader(body))
	if err != nil {
		return CachedToken{}, time.Time{}, fmt.Errorf("build verify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := v.client.Do(req)
	if err != nil {
		observeVerify("error")
		return CachedToken{}, time.Time{}, fmt.Errorf("verify call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<14)) // 16 KiB cap on response body
	if readErr != nil {
		observeVerify("error")
		return CachedToken{}, time.Time{}, fmt.Errorf("read verify response: %w", readErr)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var decoded verifyResponseBody
		if err := json.Unmarshal(respBody, &decoded); err != nil {
			observeVerify("error")
			return CachedToken{}, time.Time{}, fmt.Errorf("decode verify response: %w", err)
		}
		observeVerify("valid")
		entry := CachedToken{
			Valid:               true,
			CallerID:            decoded.CallerID,
			TokenName:           decoded.TokenName,
			Scopes:              decoded.Scopes,
			BoundCloudAccountID: decoded.BoundCloudAccountID,
		}
		var exp time.Time
		if decoded.Exp > 0 {
			exp = time.Unix(decoded.Exp, 0)
		}
		_ = start // duration is reported by the calling proxy, not here
		return entry, exp, nil
	case http.StatusUnauthorized:
		observeVerify("invalid")
		return CachedToken{}, time.Time{}, ErrVerifyDenied
	default:
		observeVerify("error")
		//nolint:err113 // dynamic status code, not a comparable sentinel
		return CachedToken{}, time.Time{}, fmt.Errorf("verify upstream status %d", resp.StatusCode)
	}
}
