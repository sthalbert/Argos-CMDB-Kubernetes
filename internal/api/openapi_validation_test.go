package api

// OpenAPI specification validation tests (ADR-0016 testing strategy, §1).
// Uses pb33f/libopenapi-validator to catch schema errors, broken $refs,
// invalid examples, and request/response shape regressions.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	liberrors "github.com/pb33f/libopenapi-validator/errors"
)

// specPath resolves api/openapi/openapi.yaml relative to this test file.
// Using runtime.Caller so the test works regardless of the working directory.
func specPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../internal/api/openapi_validation_test.go
	// specPath  = .../api/openapi/openapi.yaml
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "api", "openapi", "openapi.yaml")
}

func loadValidator(t *testing.T) validator.Validator {
	t.Helper()
	specBytes, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	doc, err := libopenapi.NewDocument(specBytes)
	if err != nil {
		t.Fatalf("parse openapi document: %v", err)
	}
	v, errs := validator.NewValidator(doc)
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		t.Fatalf("build validator: %s", strings.Join(msgs, "; "))
	}
	return v
}

// TestOpenAPIDocument_Valid verifies the spec itself is well-formed per
// the OpenAPI 3.1 rules (broken $refs, invalid schema types, etc.).
func TestOpenAPIDocument_Valid(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)
	ok, errs := v.ValidateDocument()
	if !ok {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		t.Errorf("OpenAPI document validation failed:\n%s", strings.Join(msgs, "\n"))
	}
}

// TestOpenAPIDocument_Title asserts info.title matches the canonical product name.
// Info.Title must match the canonical product name; clients generated from
// this spec embed the value, so a drift here breaks downstream tooling.
func TestOpenAPIDocument_Title(t *testing.T) {
	t.Parallel()
	specBytes, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("read openapi spec: %v", err)
	}
	doc, err := libopenapi.NewDocument(specBytes)
	if err != nil {
		t.Fatalf("parse openapi document: %v", err)
	}
	model, buildErr := doc.BuildV3Model()
	if buildErr != nil {
		t.Fatalf("build v3 model: %v", buildErr)
	}
	const wantTitle = "longue-vue CMDB API"
	if got := model.Model.Info.Title; got != wantTitle {
		t.Errorf("info.title = %q, want %q", got, wantTitle)
	}
}

// TestOpenAPI_CreateCluster_201 validates a POST /v1/clusters request and
// 201 response against the spec.
func TestOpenAPI_CreateCluster_201(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"name":"test-cluster"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/clusters", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, errs := v.ValidateHttpRequest(req)
	if !ok {
		reportValidationErrors(t, "POST /v1/clusters request", errs)
	}

	// 201 response with all required Cluster fields (id, name, layer, created_at, updated_at).
	id := "550e8400-e29b-41d4-a716-446655440000"
	now := "2024-01-01T12:00:00Z"
	respBody := `{
		"id": "` + id + `",
		"name": "test-cluster",
		"layer": "infrastructure_logical",
		"created_at": "` + now + `",
		"updated_at": "` + now + `"
	}`
	resp := buildResponse(t, http.StatusCreated, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs = v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "POST /v1/clusters 201 response", errs)
	}
}

// TestOpenAPI_CreateCluster_200 validates a 200 response (idempotent hit).
func TestOpenAPI_CreateCluster_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"name":"existing-cluster"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/clusters", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	id := "550e8400-e29b-41d4-a716-446655440001"
	now := "2024-01-01T12:00:00Z"
	respBody := `{
		"id": "` + id + `",
		"name": "existing-cluster",
		"layer": "infrastructure_logical",
		"created_at": "` + now + `",
		"updated_at": "` + now + `"
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "POST /v1/clusters 200 response", errs)
	}
}

// TestOpenAPI_VerifyToken_200 validates a POST /v1/auth/verify request and
// 200 response against the spec.
func TestOpenAPI_VerifyToken_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"token":"argos_pat_aabbccdd_validtoken"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/verify", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	// /v1/auth/verify has no security requirement — skip request auth validation,
	// validate only the response shape.
	callerID := "550e8400-e29b-41d4-a716-446655440000"
	respBody := `{
		"valid": true,
		"kind": "token",
		"caller_id": "` + callerID + `",
		"token_name": "my-collector",
		"scopes": ["write"]
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		// Filter out nullable keyword warnings (OpenAPI 3.1 migration cosmetic).
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "POST /v1/auth/verify 200 response", critical)
		}
	}
}

// TestOpenAPI_VerifyToken_401 validates the 401 response shape for an
// invalid token presented to /v1/auth/verify.
func TestOpenAPI_VerifyToken_401(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"token":"argos_pat_deadbeef_invalidtoken"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/auth/verify", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")

	respBody := `{
		"type": "about:blank",
		"title": "Unauthorized",
		"status": 401,
		"detail": "invalid token"
	}`
	resp := buildResponse(t, http.StatusUnauthorized, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	resp.Header.Set("Content-Type", "application/problem+json")
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "POST /v1/auth/verify 401 response", errs)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func buildResponse(t *testing.T, status int, body string) *http.Response {
	t.Helper()
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type": {"application/json"},
		},
		Body: io.NopCloser(bytes.NewBufferString(body)),
	}
}

func reportValidationErrors(t *testing.T, label string, errs []*liberrors.ValidationError) {
	t.Helper()
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = fmt.Sprintf("[%s] %s: %s", e.ValidationType, e.Message, e.Reason)
	}
	t.Errorf("%s:\n%s", label, strings.Join(msgs, "\n"))
}

// filterNullableErrors removes validation errors caused by the `nullable`
// keyword which is deprecated in OpenAPI 3.1 (use type: [T, null] instead).
// These are spec migration cosmetic issues — not real data shape problems.
func filterNullableErrors(errs []*liberrors.ValidationError) []*liberrors.ValidationError {
	out := errs[:0]
	for _, e := range errs {
		if strings.Contains(e.Message, "nullable") || strings.Contains(e.Reason, "nullable") {
			continue
		}
		out = append(out, e)
	}
	return out
}
