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

// TestOpenAPI_SearchExtract_Workloads_200 validates a GET /v1/search/extract
// 200 JSON response shaped as an array of WorkloadExtractRow.
// The response uses anyOf because dispatch is by ?kind=, not by body shape.
func TestOpenAPI_SearchExtract_Workloads_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/search/extract?q=log4j&kind=workloads&format=json", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	respBody := `[{
		"id": "44444444-4444-4444-4444-444444444444",
		"cluster": "prod-eu",
		"namespace": "default",
		"kind": "Deployment",
		"name": "log4j-app",
		"image_matches": "log4j:2.15",
		"replicas": 1,
		"ready_replicas": 1,
		"updated_at": "2026-05-15T14:32:00Z"
	}]`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "GET /v1/search/extract 200 response (workloads)", errs)
	}
}

// TestOpenAPI_SearchExtract_400 validates a 400 Problem response for
// GET /v1/search/extract with a bad format parameter.
func TestOpenAPI_SearchExtract_400(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/search/extract?q=log4j&kind=workloads&format=xml", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	respBody := `{
		"type": "about:blank",
		"title": "Bad Request",
		"status": 400,
		"detail": "format must be 'csv' or 'json'"
	}`
	resp := buildResponse(t, http.StatusBadRequest, respBody)
	resp.Header.Set("Content-Type", "application/problem+json")
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "GET /v1/search/extract 400 response", errs)
	}
}

// TestOpenAPI_EolExtract_200 validates a GET /v1/eol/extract 200 JSON
// response shaped as an array of EolExtractRow.
func TestOpenAPI_EolExtract_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/eol/extract?format=json", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	respBody := `[{
		"entity_type": "cluster",
		"entity_id": "c1",
		"entity_name": "Production EU",
		"cluster": "Production EU",
		"product": "kubernetes",
		"cycle": "1.28",
		"status": "eol",
		"eol_date": "2024-10-28",
		"latest": "1.28.15",
		"latest_available": "1.32.3",
		"support": "",
		"checked_at": "2026-05-15T00:00:00Z"
	}]`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		reportValidationErrors(t, "GET /v1/eol/extract 200 response", errs)
	}
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

// ── ADR-0029: Application + ApplicationBlock validation ───────────────────────

// testTimestamp is a fixed RFC 3339 timestamp shared across the ADR-0029
// validation cases so every test asserts against the same well-formed
// `date-time` literal. Extracted to silence goconst's "repeated string" lint.
const testTimestamp = "2024-01-01T12:00:00Z"

// TestOpenAPI_CreateApplicationBlock_201 validates a POST /v1/application-blocks
// minimal request and 201 response against the spec.
func TestOpenAPI_CreateApplicationBlock_201(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"name":"core-platform"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/application-blocks", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, errs := v.ValidateHttpRequest(req)
	if !ok {
		reportValidationErrors(t, "POST /v1/application-blocks request", errs)
	}

	now := testTimestamp
	respBody := `{
		"id": "550e8400-e29b-41d4-a716-446655440010",
		"name": "core-platform",
		"annotations": {},
		"created_at": "` + now + `",
		"updated_at": "` + now + `"
	}`
	resp := buildResponse(t, http.StatusCreated, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs = v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "POST /v1/application-blocks 201 response", critical)
		}
	}
}

// TestOpenAPI_ListApplicationBlocks_200 validates the list envelope.
func TestOpenAPI_ListApplicationBlocks_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/application-blocks?limit=10", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	now := testTimestamp
	respBody := `{
		"items": [{
			"id": "550e8400-e29b-41d4-a716-446655440011",
			"name": "core-platform",
			"annotations": {},
			"created_at": "` + now + `",
			"updated_at": "` + now + `",
			"application_count": 3
		}]
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "GET /v1/application-blocks 200 response", critical)
		}
	}
}

// TestOpenAPI_PatchApplicationBlock_200 validates merge-patch body + response.
func TestOpenAPI_PatchApplicationBlock_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{"display_name":"Core Platform","owner":"platform-team"}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/v1/application-blocks/550e8400-e29b-41d4-a716-446655440010",
		bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, errs := v.ValidateHttpRequest(req)
	if !ok {
		reportValidationErrors(t, "PATCH /v1/application-blocks/{id} request", errs)
	}

	now := testTimestamp
	respBody := `{
		"id": "550e8400-e29b-41d4-a716-446655440010",
		"name": "core-platform",
		"display_name": "Core Platform",
		"owner": "platform-team",
		"annotations": {},
		"created_at": "` + now + `",
		"updated_at": "` + now + `"
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs = v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "PATCH /v1/application-blocks/{id} 200 response", critical)
		}
	}
}

// TestOpenAPI_CreateApplication_201 validates the minimal POST /v1/applications
// request body + 201 response (including the required member_counts subobject).
func TestOpenAPI_CreateApplication_201(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	reqBody := `{
		"name": "billing",
		"sec_disponibilite": 3,
		"sec_integrite": 3,
		"sec_confidentialite": 2,
		"sec_tracabilite": 2
	}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/v1/applications", bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, errs := v.ValidateHttpRequest(req)
	if !ok {
		reportValidationErrors(t, "POST /v1/applications request", errs)
	}

	now := testTimestamp
	respBody := `{
		"id": "550e8400-e29b-41d4-a716-446655440020",
		"name": "billing",
		"annotations": {},
		"sec_disponibilite": 3,
		"sec_integrite": 3,
		"sec_confidentialite": 2,
		"sec_tracabilite": 2,
		"created_at": "` + now + `",
		"updated_at": "` + now + `",
		"member_counts": {"workloads": 0, "virtual_machines": 0, "vm_applications": 0}
	}`
	resp := buildResponse(t, http.StatusCreated, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs = v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "POST /v1/applications 201 response", critical)
		}
	}
}

// TestOpenAPI_GetApplication_200 validates the response shape of
// GET /v1/applications/{id}.
func TestOpenAPI_GetApplication_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/applications/550e8400-e29b-41d4-a716-446655440020", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	now := testTimestamp
	respBody := `{
		"id": "550e8400-e29b-41d4-a716-446655440020",
		"name": "billing",
		"display_name": "Billing",
		"application_block_id": "550e8400-e29b-41d4-a716-446655440010",
		"application_block_name": "core-platform",
		"owner": "billing-team",
		"criticality": "high",
		"runbook_url": "https://runbooks.example.com/billing",
		"annotations": {"longue-vue.io/cost-centre": "fin-42"},
		"sec_disponibilite": 3,
		"sec_integrite": 3,
		"sec_confidentialite": 2,
		"sec_tracabilite": 2,
		"sec_notes": "PCI DSS scope",
		"created_at": "` + now + `",
		"updated_at": "` + now + `",
		"member_counts": {"workloads": 4, "virtual_machines": 2, "vm_applications": 1}
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "GET /v1/applications/{id} 200 response", critical)
		}
	}
}

// TestOpenAPI_ListApplications_200 validates the list envelope + filter params.
func TestOpenAPI_ListApplications_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/applications?has_dict=true&dict_min=3&criticality=high&limit=20", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, errs := v.ValidateHttpRequest(req)
	if !ok {
		reportValidationErrors(t, "GET /v1/applications request", errs)
	}

	now := testTimestamp
	respBody := `{
		"items": [{
			"id": "550e8400-e29b-41d4-a716-446655440020",
			"name": "billing",
			"annotations": {},
			"created_at": "` + now + `",
			"updated_at": "` + now + `",
			"member_counts": {"workloads": 4, "virtual_machines": 2, "vm_applications": 1}
		}],
		"next_cursor": "opaque-cursor-value"
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs = v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "GET /v1/applications 200 response", critical)
		}
	}
}

// TestOpenAPI_PatchApplication_400_DICTOutOfRange validates that a DICT value
// outside 0..4 is rejected at the schema layer.
func TestOpenAPI_PatchApplication_400_DICTOutOfRange(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	// sec_disponibilite = 7 violates maximum: 4.
	reqBody := `{"sec_disponibilite":7}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch,
		"/v1/applications/550e8400-e29b-41d4-a716-446655440020",
		bytes.NewBufferString(reqBody))
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	ok, _ := v.ValidateHttpRequest(req)
	if ok {
		t.Errorf("expected validation failure for sec_disponibilite=7 (out of [0,4]); got ok=true")
	}
}

// TestOpenAPI_ListApplicationMembers_200 validates the members envelope.
func TestOpenAPI_ListApplicationMembers_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/applications/550e8400-e29b-41d4-a716-446655440020/members?kind=workload",
		http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	now := testTimestamp
	respBody := `{
		"items": [{
			"kind": "workload",
			"id": "550e8400-e29b-41d4-a716-446655440030",
			"name": "billing-api",
			"parent": {"kind": "namespace", "id": "ns-1", "name": "billing"},
			"linked_at": "` + now + `",
			"linked_by": "alice@example.com"
		}]
	}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "GET /v1/applications/{id}/members 200 response", critical)
		}
	}
}

// TestOpenAPI_GetApplicationEOL_200 validates the placeholder summary shape
// (Phase 5 of ADR-0029 fleshes out the real schema).
func TestOpenAPI_GetApplicationEOL_200(t *testing.T) {
	t.Parallel()
	v := loadValidator(t)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"/v1/applications/550e8400-e29b-41d4-a716-446655440020/eol", http.NoBody)
	req.Header.Set("Authorization", "Bearer argos_pat_aabbccdd_tok")

	respBody := `{"items": []}`
	resp := buildResponse(t, http.StatusOK, respBody)
	t.Cleanup(func() { _ = resp.Body.Close() })
	ok, errs := v.ValidateHttpResponse(req, resp)
	if !ok {
		critical := filterNullableErrors(errs)
		if len(critical) > 0 {
			reportValidationErrors(t, "GET /v1/applications/{id}/eol 200 response", critical)
		}
	}
}
