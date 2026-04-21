package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sthalbert/argos/internal/api"
	"github.com/sthalbert/argos/internal/auth"
	"github.com/sthalbert/argos/internal/collector"
	"github.com/sthalbert/argos/internal/collector/apiclient"
	"github.com/sthalbert/argos/internal/store"
)

// integrationDSN is the DSN for the dedicated integration database.
// Set by TestMain; empty when PGX_TEST_DATABASE is unset.
var integrationDSN string

// TestMain creates a dedicated database so integration tests don't collide
// with internal/store/pg_test.go (which shares PGX_TEST_DATABASE and
// TRUNCATEs concurrently when go test runs packages in parallel).
func TestMain(m *testing.M) {
	baseDSN := os.Getenv("PGX_TEST_DATABASE")
	if baseDSN == "" {
		os.Exit(0) // nothing to run
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration TestMain: connect: %v\n", err)
		os.Exit(1)
	}

	dbName := "argos_integration_test"

	// Drop + recreate to guarantee a clean slate.
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName)
	_, err = conn.Exec(ctx, "CREATE DATABASE "+dbName)
	_ = conn.Close(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration TestMain: create database: %v\n", err)
		os.Exit(1)
	}

	// Build the DSN for the new database.
	// Replace the last path segment (database name) in the base DSN.
	idx := strings.LastIndex(baseDSN, "/")
	qIdx := strings.Index(baseDSN[idx:], "?")
	if qIdx < 0 {
		integrationDSN = baseDSN[:idx+1] + dbName
	} else {
		integrationDSN = baseDSN[:idx+1] + dbName + baseDSN[idx+qIdx:]
	}

	code := m.Run()

	// Teardown: drop the dedicated database.
	conn2, err := pgx.Connect(context.Background(), baseDSN)
	if err == nil {
		_, _ = conn2.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName)
		_ = conn2.Close(context.Background())
	}

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// Test environment
// ---------------------------------------------------------------------------

// testEnv holds the shared test server and auth token.
type testEnv struct {
	srv   *httptest.Server
	token string // bearer token for API calls
	store api.Store
	// admin credentials for login tests
	adminUser string
	adminPass string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	if integrationDSN == "" {
		t.Skip("PGX_TEST_DATABASE not set; skipping integration test")
	}
	dsn := integrationDSN

	ctx := context.Background()
	pg, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := pg.Migrate(ctx); err != nil {
		pg.Close()
		t.Fatalf("migrate: %v", err)
	}

	// Open a raw pool for truncation in cleanup — the store's pool field is
	// unexported, so we need a separate connection.
	rawPool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		pg.Close()
		t.Fatalf("open raw pool for cleanup: %v", err)
	}

	// Wipe all data at setup AND cleanup so the test starts from a blank slate
	// regardless of prior test runs or existing OrbStack data.
	truncate := func() {
		_, _ = rawPool.Exec(context.Background(),
			"TRUNCATE clusters, api_tokens, sessions, user_identities, oidc_auth_states, audit_events, users CASCADE")
	}
	truncate()
	t.Cleanup(func() {
		truncate()
		rawPool.Close()
		pg.Close()
	})

	// Bootstrap admin user.
	adminUser := "integration-admin"
	adminPass := "Sup3r-S3cr3t-P@ss!"
	hash, err := auth.HashPassword(adminPass)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := pg.CreateUser(ctx, api.UserInsert{
		Username:           adminUser,
		PasswordHash:       hash,
		Role:               "admin",
		MustChangePassword: false,
	})
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	var adminID uuid.UUID
	if u.Id != nil {
		adminID = *u.Id
	}

	// Mint a PAT for machine auth.
	minted, err := auth.MintToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	_, err = pg.CreateAPIToken(ctx, api.APITokenInsert{
		ID:              uuid.New(),
		Name:            "integration-test",
		Prefix:          minted.Prefix,
		Hash:            minted.Hash,
		Scopes:          []string{"read", "write", "delete", "admin", "audit"},
		CreatedByUserID: adminID,
		ExpiresAt:       nil,
	})
	if err != nil {
		t.Fatalf("create api token: %v", err)
	}

	// Build the full HTTP handler chain identical to main.go.
	mux := http.NewServeMux()
	strict := api.NewStrictHandlerWithOptions(
		api.NewServer("test", pg, auth.SecureNever, nil),
		[]api.StrictMiddlewareFunc{api.InjectRequestMiddleware},
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusBadRequest)
			},
			ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			},
		},
	)
	api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
		BaseRouter: mux,
		Middlewares: []api.MiddlewareFunc{
			api.AuthMiddleware(pg, auth.SecureNever),
			api.AuditMiddleware(pg),
		},
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &testEnv{
		srv:       srv,
		token:     minted.Plaintext,
		store:     pg,
		adminUser: adminUser,
		adminPass: adminPass,
	}
}

// doReq sends an HTTP request with bearer auth and returns the response.
func (e *testEnv) doReq(t *testing.T, method, path, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+e.token)
	if body != "" {
		ct := "application/json"
		if method == http.MethodPatch {
			ct = "application/merge-patch+json"
		}
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// doJSON sends a request and decodes the JSON response into dst.
// Returns the HTTP status code.
func (e *testEnv) doJSON(t *testing.T, method, path, body string, dst any) int {
	t.Helper()
	resp := e.doReq(t, method, path, body)
	defer func() { _ = resp.Body.Close() }()
	if dst != nil {
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, dst); err != nil {
				t.Fatalf("decode JSON (%s %s): %v\nbody: %s", method, path, err, string(raw))
			}
		}
	}
	return resp.StatusCode
}

// doReqNoAuth sends an unauthenticated HTTP request.
func (e *testEnv) doReqNoAuth(t *testing.T, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }

// ---------------------------------------------------------------------------
// TestHealthProbes
// ---------------------------------------------------------------------------

func TestHealthProbes(t *testing.T) {
	env := newTestEnv(t)

	t.Run("healthz returns 200 ok", func(t *testing.T) {
		resp := env.doReqNoAuth(t, http.MethodGet, "/healthz")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var h api.Health
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if h.Status != api.Ok {
			t.Errorf("expected status ok, got %q", h.Status)
		}
	})

	t.Run("readyz returns 200 ok", func(t *testing.T) {
		resp := env.doReqNoAuth(t, http.MethodGet, "/readyz")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		var h api.Health
		if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if h.Status != api.Ok {
			t.Errorf("expected status ok, got %q", h.Status)
		}
	})
}

// ---------------------------------------------------------------------------
// TestAuthFlow
// ---------------------------------------------------------------------------

func TestAuthFlow(t *testing.T) {
	env := newTestEnv(t)

	t.Run("login with admin creds and use session cookie", func(t *testing.T) {
		// Login.
		loginBody := fmt.Sprintf(`{"username":%q,"password":%q}`, env.adminUser, env.adminPass)
		req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/v1/auth/login", strings.NewReader(loginBody))
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("login request: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204 from login, got %d", resp.StatusCode)
		}

		// Extract session cookie.
		var sessionCookie *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == auth.SessionCookieName {
				sessionCookie = c
				break
			}
		}
		if sessionCookie == nil {
			t.Fatal("no session cookie in login response")
		}

		// GET /v1/auth/me with the cookie.
		meReq, _ := http.NewRequest(http.MethodGet, env.srv.URL+"/v1/auth/me", nil)
		meReq.AddCookie(sessionCookie)
		meResp, err := client.Do(meReq)
		if err != nil {
			t.Fatalf("me request: %v", err)
		}
		defer func() { _ = meResp.Body.Close() }()
		if meResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 from /me, got %d", meResp.StatusCode)
		}
		var me api.Me
		if err := json.NewDecoder(meResp.Body).Decode(&me); err != nil {
			t.Fatalf("decode /me: %v", err)
		}
		if me.Username == nil || *me.Username != env.adminUser {
			t.Errorf("expected username %q, got %v", env.adminUser, me.Username)
		}

		// Logout.
		logoutReq, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/v1/auth/logout", nil)
		logoutReq.AddCookie(sessionCookie)
		logoutResp, err := client.Do(logoutReq)
		if err != nil {
			t.Fatalf("logout request: %v", err)
		}
		_ = logoutResp.Body.Close()
		if logoutResp.StatusCode != http.StatusNoContent {
			t.Fatalf("expected 204 from logout, got %d", logoutResp.StatusCode)
		}

		// Verify cookie cleared (Set-Cookie with empty value or MaxAge=-1).
		found := false
		for _, c := range logoutResp.Cookies() {
			if c.Name == auth.SessionCookieName && c.MaxAge < 0 {
				found = true
			}
		}
		if !found {
			t.Error("expected session cookie to be cleared on logout")
		}
	})

	t.Run("PAT token gets clusters 200", func(t *testing.T) {
		resp := env.doReq(t, http.MethodGet, "/v1/clusters", "")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
	})

	t.Run("request without auth returns 401", func(t *testing.T) {
		resp := env.doReqNoAuth(t, http.MethodGet, "/v1/clusters")
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", resp.StatusCode)
		}
	})
}

// ---------------------------------------------------------------------------
// TestClusterCRUD
// ---------------------------------------------------------------------------

func TestClusterCRUD(t *testing.T) {
	env := newTestEnv(t)

	// CREATE
	var created api.Cluster
	body := `{"name":"test-cluster","environment":"staging"}`
	resp := env.doReq(t, http.MethodPost, "/v1/clusters", body)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("create: expected 201, got %d: %s", resp.StatusCode, raw)
	}
	locHeader := resp.Header.Get("Location")
	if locHeader == "" {
		t.Error("expected Location header on 201")
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode created cluster: %v", err)
	}
	_ = resp.Body.Close()
	if created.Id == nil {
		t.Fatal("created cluster has nil id")
	}
	if created.Name != "test-cluster" {
		t.Errorf("expected name test-cluster, got %q", created.Name)
	}

	clusterID := created.Id.String()

	// GET by id
	var fetched api.Cluster
	status := env.doJSON(t, http.MethodGet, "/v1/clusters/"+clusterID, "", &fetched)
	if status != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", status)
	}
	if fetched.Name != "test-cluster" {
		t.Errorf("get: name mismatch: %q", fetched.Name)
	}

	// LIST with name filter
	var listed api.ClusterList
	status = env.doJSON(t, http.MethodGet, "/v1/clusters?name=test-cluster", "", &listed)
	if status != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", status)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("list: expected 1 item, got %d", len(listed.Items))
	}

	// PATCH
	var patched api.Cluster
	patchResp := env.doReq(t, http.MethodPatch, "/v1/clusters/"+clusterID, `{"kubernetes_version":"v1.29.1"}`)
	if patchResp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(patchResp.Body)
		_ = patchResp.Body.Close()
		t.Fatalf("patch: expected 200, got %d: %s", patchResp.StatusCode, raw)
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patched); err != nil {
		_ = patchResp.Body.Close()
		t.Fatalf("decode patched: %v", err)
	}
	_ = patchResp.Body.Close()
	if patched.KubernetesVersion == nil || *patched.KubernetesVersion != "v1.29.1" {
		t.Errorf("patch: expected kubernetes_version v1.29.1, got %v", patched.KubernetesVersion)
	}

	// DELETE
	delResp := env.doReq(t, http.MethodDelete, "/v1/clusters/"+clusterID, "")
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", delResp.StatusCode)
	}

	// GET after delete -> 404
	getResp := env.doReq(t, http.MethodGet, "/v1/clusters/"+clusterID, "")
	_ = getResp.Body.Close()
	if getResp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d", getResp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// TestFullResourceHierarchy
// ---------------------------------------------------------------------------

func TestFullResourceHierarchy(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	_ = ctx

	// Create cluster.
	var cluster api.Cluster
	status := env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"hierarchy-cluster","environment":"test"}`, &cluster)
	if status != http.StatusCreated {
		t.Fatalf("create cluster: %d", status)
	}
	clusterID := cluster.Id.String()

	// Create 2 nodes.
	for _, name := range []string{"node-1", "node-2"} {
		body := fmt.Sprintf(`{"cluster_id":"%s","name":"%s"}`, clusterID, name)
		var node api.Node
		s := env.doJSON(t, http.MethodPost, "/v1/nodes", body, &node)
		if s != http.StatusCreated {
			t.Fatalf("create node %s: %d", name, s)
		}
	}

	// Create 2 namespaces.
	nsIDs := make(map[string]string)
	for _, name := range []string{"ns-alpha", "ns-beta"} {
		body := fmt.Sprintf(`{"cluster_id":"%s","name":"%s"}`, clusterID, name)
		var ns api.Namespace
		s := env.doJSON(t, http.MethodPost, "/v1/namespaces", body, &ns)
		if s != http.StatusCreated {
			t.Fatalf("create namespace %s: %d", name, s)
		}
		nsIDs[name] = ns.Id.String()
	}

	// Create workloads in ns-alpha.
	wlIDs := make(map[string]string)
	for _, name := range []string{"deploy-web", "deploy-api"} {
		body := fmt.Sprintf(`{"namespace_id":"%s","kind":"Deployment","name":"%s","replicas":2}`, nsIDs["ns-alpha"], name)
		var wl api.Workload
		s := env.doJSON(t, http.MethodPost, "/v1/workloads", body, &wl)
		if s != http.StatusCreated {
			t.Fatalf("create workload %s: %d", name, s)
		}
		wlIDs[name] = wl.Id.String()
	}

	// Create pods with workload_id FK.
	for _, name := range []string{"pod-web-1", "pod-web-2"} {
		body := fmt.Sprintf(`{"namespace_id":"%s","name":"%s","workload_id":"%s"}`,
			nsIDs["ns-alpha"], name, wlIDs["deploy-web"])
		var pod api.Pod
		s := env.doJSON(t, http.MethodPost, "/v1/pods", body, &pod)
		if s != http.StatusCreated {
			t.Fatalf("create pod %s: %d", name, s)
		}
	}

	// Create a service.
	{
		body := fmt.Sprintf(`{"namespace_id":"%s","name":"svc-web","type":"ClusterIP","cluster_ip":"10.0.0.1"}`, nsIDs["ns-alpha"])
		var svc api.Service
		s := env.doJSON(t, http.MethodPost, "/v1/services", body, &svc)
		if s != http.StatusCreated {
			t.Fatalf("create service: %d", s)
		}
	}

	// Create an ingress.
	{
		body := fmt.Sprintf(`{"namespace_id":"%s","name":"ing-web","ingress_class_name":"nginx"}`, nsIDs["ns-alpha"])
		var ing api.Ingress
		s := env.doJSON(t, http.MethodPost, "/v1/ingresses", body, &ing)
		if s != http.StatusCreated {
			t.Fatalf("create ingress: %d", s)
		}
	}

	// Create PV + PVC with bound_volume_id linkage.
	var pvID string
	{
		body := fmt.Sprintf(`{"cluster_id":"%s","name":"pv-data","capacity":"10Gi","phase":"Bound"}`, clusterID)
		var pv api.PersistentVolume
		s := env.doJSON(t, http.MethodPost, "/v1/persistentvolumes", body, &pv)
		if s != http.StatusCreated {
			t.Fatalf("create PV: %d", s)
		}
		pvID = pv.Id.String()
	}
	{
		body := fmt.Sprintf(`{"namespace_id":"%s","name":"pvc-data","bound_volume_id":"%s","phase":"Bound"}`, nsIDs["ns-alpha"], pvID)
		var pvc api.PersistentVolumeClaim
		s := env.doJSON(t, http.MethodPost, "/v1/persistentvolumeclaims", body, &pvc)
		if s != http.StatusCreated {
			t.Fatalf("create PVC: %d", s)
		}
		if pvc.BoundVolumeId == nil || pvc.BoundVolumeId.String() != pvID {
			t.Errorf("PVC bound_volume_id mismatch: expected %s, got %v", pvID, pvc.BoundVolumeId)
		}
	}

	// List each resource type and verify counts.
	var nodeList api.NodeList
	env.doJSON(t, http.MethodGet, "/v1/nodes?cluster_id="+clusterID, "", &nodeList)
	if len(nodeList.Items) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodeList.Items))
	}

	var nsList api.NamespaceList
	env.doJSON(t, http.MethodGet, "/v1/namespaces?cluster_id="+clusterID, "", &nsList)
	if len(nsList.Items) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(nsList.Items))
	}

	var wlList api.WorkloadList
	env.doJSON(t, http.MethodGet, "/v1/workloads?namespace_id="+nsIDs["ns-alpha"], "", &wlList)
	if len(wlList.Items) != 2 {
		t.Errorf("expected 2 workloads, got %d", len(wlList.Items))
	}

	var podList api.PodList
	env.doJSON(t, http.MethodGet, "/v1/pods?namespace_id="+nsIDs["ns-alpha"], "", &podList)
	if len(podList.Items) != 2 {
		t.Errorf("expected 2 pods, got %d", len(podList.Items))
	}

	var svcList api.ServiceList
	env.doJSON(t, http.MethodGet, "/v1/services?namespace_id="+nsIDs["ns-alpha"], "", &svcList)
	if len(svcList.Items) != 1 {
		t.Errorf("expected 1 service, got %d", len(svcList.Items))
	}

	var ingList api.IngressList
	env.doJSON(t, http.MethodGet, "/v1/ingresses?namespace_id="+nsIDs["ns-alpha"], "", &ingList)
	if len(ingList.Items) != 1 {
		t.Errorf("expected 1 ingress, got %d", len(ingList.Items))
	}

	var pvList api.PersistentVolumeList
	env.doJSON(t, http.MethodGet, "/v1/persistentvolumes?cluster_id="+clusterID, "", &pvList)
	if len(pvList.Items) != 1 {
		t.Errorf("expected 1 PV, got %d", len(pvList.Items))
	}

	var pvcList api.PersistentVolumeClaimList
	env.doJSON(t, http.MethodGet, "/v1/persistentvolumeclaims?namespace_id="+nsIDs["ns-alpha"], "", &pvcList)
	if len(pvcList.Items) != 1 {
		t.Errorf("expected 1 PVC, got %d", len(pvcList.Items))
	}

	// Delete cluster -> verify cascade.
	delResp := env.doReq(t, http.MethodDelete, "/v1/clusters/"+clusterID, "")
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete cluster: expected 204, got %d", delResp.StatusCode)
	}

	// After cascade, nodes should be gone.
	var emptyNodes api.NodeList
	env.doJSON(t, http.MethodGet, "/v1/nodes?cluster_id="+clusterID, "", &emptyNodes)
	if len(emptyNodes.Items) != 0 {
		t.Errorf("expected 0 nodes after cascade delete, got %d", len(emptyNodes.Items))
	}

	// Namespaces should be gone.
	var emptyNS api.NamespaceList
	env.doJSON(t, http.MethodGet, "/v1/namespaces?cluster_id="+clusterID, "", &emptyNS)
	if len(emptyNS.Items) != 0 {
		t.Errorf("expected 0 namespaces after cascade delete, got %d", len(emptyNS.Items))
	}
}

// ---------------------------------------------------------------------------
// TestReconcileEndpoints
// ---------------------------------------------------------------------------

func TestReconcileEndpoints(t *testing.T) {
	env := newTestEnv(t)

	// Create cluster.
	var cluster api.Cluster
	env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"reconcile-cluster"}`, &cluster)
	clusterID := cluster.Id.String()

	// --- Nodes ---
	t.Run("reconcile nodes", func(t *testing.T) {
		for _, name := range []string{"node-a", "node-b", "node-c"} {
			body := fmt.Sprintf(`{"cluster_id":"%s","name":"%s"}`, clusterID, name)
			resp := env.doReq(t, http.MethodPost, "/v1/nodes", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"cluster_id":"%s","keep_names":["node-a"]}`, clusterID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/nodes/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile nodes: expected 200, got %d", s)
		}
		if result.Deleted != 2 {
			t.Errorf("expected 2 deleted, got %d", result.Deleted)
		}
		var nodes api.NodeList
		env.doJSON(t, http.MethodGet, "/v1/nodes?cluster_id="+clusterID, "", &nodes)
		if len(nodes.Items) != 1 {
			t.Errorf("expected 1 remaining node, got %d", len(nodes.Items))
		}
		if len(nodes.Items) > 0 && nodes.Items[0].Name != "node-a" {
			t.Errorf("expected node-a, got %q", nodes.Items[0].Name)
		}
	})

	// --- Namespaces ---
	t.Run("reconcile namespaces", func(t *testing.T) {
		for _, name := range []string{"ns-1", "ns-2", "ns-3"} {
			body := fmt.Sprintf(`{"cluster_id":"%s","name":"%s"}`, clusterID, name)
			resp := env.doReq(t, http.MethodPost, "/v1/namespaces", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"cluster_id":"%s","keep_names":["ns-1"]}`, clusterID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/namespaces/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile namespaces: expected 200, got %d", s)
		}
		if result.Deleted != 2 {
			t.Errorf("expected 2 deleted, got %d", result.Deleted)
		}
	})

	// --- Pods (namespace-scoped) ---
	t.Run("reconcile pods", func(t *testing.T) {
		// Create a namespace for pods.
		var ns api.Namespace
		env.doJSON(t, http.MethodPost, "/v1/namespaces",
			fmt.Sprintf(`{"cluster_id":"%s","name":"ns-pods"}`, clusterID), &ns)
		nsID := ns.Id.String()

		for _, name := range []string{"pod-x", "pod-y", "pod-z"} {
			body := fmt.Sprintf(`{"namespace_id":"%s","name":"%s"}`, nsID, name)
			resp := env.doReq(t, http.MethodPost, "/v1/pods", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"namespace_id":"%s","keep_names":["pod-x"]}`, nsID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/pods/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile pods: expected 200, got %d", s)
		}
		if result.Deleted != 2 {
			t.Errorf("expected 2 deleted, got %d", result.Deleted)
		}
	})

	// --- Workloads (with keep_kinds+keep_names) ---
	t.Run("reconcile workloads", func(t *testing.T) {
		var ns api.Namespace
		env.doJSON(t, http.MethodPost, "/v1/namespaces",
			fmt.Sprintf(`{"cluster_id":"%s","name":"ns-wl-reconcile"}`, clusterID), &ns)
		nsID := ns.Id.String()

		for _, wl := range []struct{ kind, name string }{
			{"Deployment", "web"},
			{"Deployment", "api"},
			{"StatefulSet", "db"},
		} {
			body := fmt.Sprintf(`{"namespace_id":"%s","kind":"%s","name":"%s"}`, nsID, wl.kind, wl.name)
			resp := env.doReq(t, http.MethodPost, "/v1/workloads", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"namespace_id":"%s","keep_kinds":["Deployment"],"keep_names":["web"]}`, nsID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/workloads/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile workloads: expected 200, got %d", s)
		}
		if result.Deleted != 2 {
			t.Errorf("expected 2 deleted, got %d", result.Deleted)
		}
	})

	// --- Services ---
	t.Run("reconcile services", func(t *testing.T) {
		var ns api.Namespace
		env.doJSON(t, http.MethodPost, "/v1/namespaces",
			fmt.Sprintf(`{"cluster_id":"%s","name":"ns-svc-reconcile"}`, clusterID), &ns)
		nsID := ns.Id.String()

		for _, name := range []string{"svc-a", "svc-b"} {
			body := fmt.Sprintf(`{"namespace_id":"%s","name":"%s"}`, nsID, name)
			resp := env.doReq(t, http.MethodPost, "/v1/services", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"namespace_id":"%s","keep_names":["svc-a"]}`, nsID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/services/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile services: expected 200, got %d", s)
		}
		if result.Deleted != 1 {
			t.Errorf("expected 1 deleted, got %d", result.Deleted)
		}
	})

	// --- PVCs ---
	t.Run("reconcile pvcs", func(t *testing.T) {
		var ns api.Namespace
		env.doJSON(t, http.MethodPost, "/v1/namespaces",
			fmt.Sprintf(`{"cluster_id":"%s","name":"ns-pvc-reconcile"}`, clusterID), &ns)
		nsID := ns.Id.String()

		for _, name := range []string{"pvc-1", "pvc-2"} {
			body := fmt.Sprintf(`{"namespace_id":"%s","name":"%s"}`, nsID, name)
			resp := env.doReq(t, http.MethodPost, "/v1/persistentvolumeclaims", body); _ = resp.Body.Close()
		}
		reconcileBody := fmt.Sprintf(`{"namespace_id":"%s","keep_names":["pvc-1"]}`, nsID)
		var result struct{ Deleted int64 `json:"deleted"` }
		s := env.doJSON(t, http.MethodPost, "/v1/persistentvolumeclaims/reconcile", reconcileBody, &result)
		if s != http.StatusOK {
			t.Fatalf("reconcile pvcs: expected 200, got %d", s)
		}
		if result.Deleted != 1 {
			t.Errorf("expected 1 deleted, got %d", result.Deleted)
		}
	})
}

// ---------------------------------------------------------------------------
// TestPushCollectorEndToEnd
// ---------------------------------------------------------------------------

// fakeKubeSource implements collector.KubeSource with settable fields.
type fakeKubeSource struct {
	version    string
	nodes      []collector.NodeInfo
	namespaces []collector.NamespaceInfo
	pods       []collector.PodInfo
	workloads  []collector.WorkloadInfo
	services   []collector.ServiceInfo
	ingresses  []collector.IngressInfo
	pvs        []collector.PVInfo
	pvcs       []collector.PVCInfo
	rsOwners   []collector.ReplicaSetOwner
}

func (f *fakeKubeSource) ServerVersion(_ context.Context) (string, error) {
	return f.version, nil
}
func (f *fakeKubeSource) ListNodes(_ context.Context) ([]collector.NodeInfo, error) {
	return f.nodes, nil
}
func (f *fakeKubeSource) ListNamespaces(_ context.Context) ([]collector.NamespaceInfo, error) {
	return f.namespaces, nil
}
func (f *fakeKubeSource) ListPods(_ context.Context) ([]collector.PodInfo, error) {
	return f.pods, nil
}
func (f *fakeKubeSource) ListWorkloads(_ context.Context) ([]collector.WorkloadInfo, error) {
	return f.workloads, nil
}
func (f *fakeKubeSource) ListServices(_ context.Context) ([]collector.ServiceInfo, error) {
	return f.services, nil
}
func (f *fakeKubeSource) ListIngresses(_ context.Context) ([]collector.IngressInfo, error) {
	return f.ingresses, nil
}
func (f *fakeKubeSource) ListReplicaSetOwners(_ context.Context) ([]collector.ReplicaSetOwner, error) {
	return f.rsOwners, nil
}
func (f *fakeKubeSource) ListPersistentVolumes(_ context.Context) ([]collector.PVInfo, error) {
	return f.pvs, nil
}
func (f *fakeKubeSource) ListPersistentVolumeClaims(_ context.Context) ([]collector.PVCInfo, error) {
	return f.pvcs, nil
}

func TestPushCollectorEndToEnd(t *testing.T) {
	env := newTestEnv(t)

	// Create the cluster record via API (the collector looks it up by name).
	var cluster api.Cluster
	s := env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"push-test"}`, &cluster)
	if s != http.StatusCreated {
		t.Fatalf("create cluster: %d", s)
	}

	// Build an apiclient.Store pointing at the httptest server.
	apiStore, err := apiclient.NewStore(apiclient.Config{
		ServerURL: env.srv.URL,
		Token:     env.token,
	})
	if err != nil {
		t.Fatalf("new api store: %v", err)
	}

	source := &fakeKubeSource{
		version: "v1.30.0",
		nodes: []collector.NodeInfo{
			{Name: "push-node-1", Ready: true, KubeletVersion: "v1.30.0"},
			{Name: "push-node-2", Ready: true, KubeletVersion: "v1.30.0"},
		},
		namespaces: []collector.NamespaceInfo{
			{Name: "push-ns-default", Phase: "Active"},
			{Name: "push-ns-kube-system", Phase: "Active"},
		},
		workloads: []collector.WorkloadInfo{
			{Name: "push-deploy-web", Namespace: "push-ns-default", Kind: api.Deployment, Replicas: ptr(2)},
		},
		pods: []collector.PodInfo{
			{Name: "push-pod-1", Namespace: "push-ns-default", Phase: "Running", NodeName: "push-node-1",
				OwnerKind: "Deployment", OwnerName: "push-deploy-web"},
		},
		services: []collector.ServiceInfo{
			{Name: "push-svc", Namespace: "push-ns-default", Type: "ClusterIP", ClusterIP: "10.0.0.100"},
		},
		ingresses: []collector.IngressInfo{
			{Name: "push-ing", Namespace: "push-ns-default", IngressClassName: "nginx"},
		},
		pvs: []collector.PVInfo{
			{Name: "push-pv-1", Capacity: "50Gi", Phase: "Bound"},
		},
		pvcs: []collector.PVCInfo{
			{Name: "push-pvc-1", Namespace: "push-ns-default", Phase: "Bound", VolumeName: "push-pv-1"},
		},
	}

	// Create and run the collector. It polls immediately on Run(), then waits
	// for the ticker. We cancel the context after the first poll completes.
	coll := collector.New(apiStore, source, "push-test", 1*time.Hour, 30*time.Second, true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go coll.Run(ctx) //nolint:errcheck
	time.Sleep(3 * time.Second)
	cancel()

	// Verify cluster version was updated.
	var updated api.Cluster
	env.doJSON(t, http.MethodGet, "/v1/clusters/"+cluster.Id.String(), "", &updated)
	if updated.KubernetesVersion == nil || *updated.KubernetesVersion != "v1.30.0" {
		t.Errorf("expected kubernetes_version v1.30.0, got %v", updated.KubernetesVersion)
	}

	// Verify nodes.
	var nodes api.NodeList
	env.doJSON(t, http.MethodGet, "/v1/nodes?cluster_id="+cluster.Id.String(), "", &nodes)
	if len(nodes.Items) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(nodes.Items))
	}

	// Verify namespaces.
	var nsList api.NamespaceList
	env.doJSON(t, http.MethodGet, "/v1/namespaces?cluster_id="+cluster.Id.String(), "", &nsList)
	if len(nsList.Items) != 2 {
		t.Errorf("expected 2 namespaces, got %d", len(nsList.Items))
	}

	// Verify workloads.
	var wlList api.WorkloadList
	env.doJSON(t, http.MethodGet, "/v1/workloads", "", &wlList)
	found := false
	for _, w := range wlList.Items {
		if w.Name == "push-deploy-web" {
			found = true
		}
	}
	if !found {
		t.Error("expected workload push-deploy-web to exist")
	}

	// Verify pods.
	var podList api.PodList
	env.doJSON(t, http.MethodGet, "/v1/pods", "", &podList)
	found = false
	for _, p := range podList.Items {
		if p.Name == "push-pod-1" {
			found = true
		}
	}
	if !found {
		t.Error("expected pod push-pod-1 to exist")
	}

	// Verify services.
	var svcList api.ServiceList
	env.doJSON(t, http.MethodGet, "/v1/services", "", &svcList)
	found = false
	for _, svc := range svcList.Items {
		if svc.Name == "push-svc" {
			found = true
		}
	}
	if !found {
		t.Error("expected service push-svc to exist")
	}

	// Verify PVs.
	var pvList api.PersistentVolumeList
	env.doJSON(t, http.MethodGet, "/v1/persistentvolumes?cluster_id="+cluster.Id.String(), "", &pvList)
	if len(pvList.Items) != 1 {
		t.Errorf("expected 1 PV, got %d", len(pvList.Items))
	}

	// Verify PVCs.
	var pvcList api.PersistentVolumeClaimList
	env.doJSON(t, http.MethodGet, "/v1/persistentvolumeclaims", "", &pvcList)
	found = false
	for _, pvc := range pvcList.Items {
		if pvc.Name == "push-pvc-1" {
			found = true
			if pvc.BoundVolumeId == nil {
				t.Error("expected PVC push-pvc-1 to have bound_volume_id set")
			}
		}
	}
	if !found {
		t.Error("expected PVC push-pvc-1 to exist")
	}

	// --- Second poll with fewer resources: remove a node, verify reconciliation. ---
	source.nodes = []collector.NodeInfo{
		{Name: "push-node-1", Ready: true, KubeletVersion: "v1.30.0"},
		// push-node-2 removed
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	coll2 := collector.New(apiStore, source, "push-test", 1*time.Hour, 30*time.Second, true)
	go coll2.Run(ctx2) //nolint:errcheck
	time.Sleep(3 * time.Second)
	cancel2()

	// Verify node was reconciled away.
	var nodesAfter api.NodeList
	env.doJSON(t, http.MethodGet, "/v1/nodes?cluster_id="+cluster.Id.String(), "", &nodesAfter)
	if len(nodesAfter.Items) != 1 {
		t.Errorf("expected 1 node after reconciliation, got %d", len(nodesAfter.Items))
	}
	if len(nodesAfter.Items) > 0 && nodesAfter.Items[0].Name != "push-node-1" {
		t.Errorf("expected push-node-1, got %q", nodesAfter.Items[0].Name)
	}
}

// ---------------------------------------------------------------------------
// TestAuditLog
// ---------------------------------------------------------------------------

func TestAuditLog(t *testing.T) {
	env := newTestEnv(t)

	// Create a cluster.
	var cluster api.Cluster
	env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"audit-cluster"}`, &cluster)
	clusterID := cluster.Id.String()

	// Update it.
	resp := env.doReq(t, http.MethodPatch, "/v1/clusters/"+clusterID, `{"kubernetes_version":"v1.28.0"}`); _ = resp.Body.Close()

	// Delete it.
	resp = env.doReq(t, http.MethodDelete, "/v1/clusters/"+clusterID, ""); _ = resp.Body.Close()

	// Fetch audit events.
	var auditList api.AuditEventList
	s := env.doJSON(t, http.MethodGet, "/v1/admin/audit", "", &auditList)
	if s != http.StatusOK {
		t.Fatalf("audit list: expected 200, got %d", s)
	}

	// We expect at least 3 events for the cluster: create (resource_id is
	// empty in the path, so we match on resource_type), update and delete
	// (resource_id is the cluster UUID from the path).
	clusterEvents := 0
	for _, evt := range auditList.Items {
		if evt.ResourceType != nil && *evt.ResourceType == "cluster" {
			// POST /v1/clusters has no ID in the path, so resource_id is empty.
			// PATCH/DELETE /v1/clusters/{id} carry the UUID.
			if evt.ResourceId == nil || *evt.ResourceId == "" || *evt.ResourceId == clusterID {
				clusterEvents++
			}
		}
	}
	if clusterEvents < 3 {
		t.Errorf("expected at least 3 audit events for cluster operations, got %d", clusterEvents)
	}
}

// ---------------------------------------------------------------------------
// TestErrorPaths
// ---------------------------------------------------------------------------

func TestErrorPaths(t *testing.T) {
	env := newTestEnv(t)

	t.Run("GET nonexistent cluster returns 404 Problem JSON", func(t *testing.T) {
		randomID := uuid.New().String()
		var problem api.Problem
		s := env.doJSON(t, http.MethodGet, "/v1/clusters/"+randomID, "", &problem)
		if s != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", s)
		}
		if problem.Status != 404 {
			t.Errorf("expected problem status 404, got %d", problem.Status)
		}
	})

	t.Run("POST cluster with empty name returns 400", func(t *testing.T) {
		var problem api.Problem
		s := env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":""}`, &problem)
		if s != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", s)
		}
	})

	t.Run("POST cluster with duplicate name returns 409", func(t *testing.T) {
		resp := env.doReq(t, http.MethodPost, "/v1/clusters", `{"name":"dup-cluster"}`); _ = resp.Body.Close()
		var problem api.Problem
		s := env.doJSON(t, http.MethodPost, "/v1/clusters", `{"name":"dup-cluster"}`, &problem)
		if s != http.StatusConflict {
			t.Fatalf("expected 409, got %d", s)
		}
		if problem.Status != 409 {
			t.Errorf("expected problem status 409, got %d", problem.Status)
		}
	})

	t.Run("POST node with missing cluster_id returns 400", func(t *testing.T) {
		s := env.doJSON(t, http.MethodPost, "/v1/nodes", `{"name":"orphan"}`, nil)
		if s != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", s)
		}
	})
}
