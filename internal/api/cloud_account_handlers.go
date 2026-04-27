package api

// Hand-written HTTP handlers for the cloud-accounts endpoints (ADR-0015).
// Mounted on the main mux next to the settings + impact handlers.
//
// Three audiences:
//   - Admin endpoints under /v1/admin/cloud-accounts/* — admin scope.
//   - Collector endpoints under /v1/cloud-accounts/* — vm-collector scope.
//   - The credentials-fetch endpoint specifically returns plaintext SK
//     and is the one place plaintext leaves the database.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sthalbert/argos/internal/auth"
	"github.com/sthalbert/argos/internal/metrics"
	"github.com/sthalbert/argos/internal/secrets"
)

// parseOptTime returns a pointer to time.Time when the optional jsonTime
// carries a non-empty RFC3339 value; nil otherwise.
func parseOptTime(jt *jsonTime) *time.Time {
	if jt == nil || jt.Value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, jt.Value)
	if err != nil {
		return nil
	}
	return &t
}

// The handlers below take the wider api.Store interface so they can be
// mounted alongside the existing handlers without duplicating wiring.

// adminCreateCloudAccountReq is the body shape for POST /v1/admin/cloud-accounts.
type adminCreateCloudAccountReq struct {
	Provider    string  `json:"provider"`
	Name        string  `json:"name"`
	Region      string  `json:"region"`
	AccessKey   string  `json:"access_key,omitempty"`
	SecretKey   string  `json:"secret_key,omitempty"`
	Owner       *string `json:"owner,omitempty"`
	Criticality *string `json:"criticality,omitempty"`
	Notes       *string `json:"notes,omitempty"`
	RunbookURL  *string `json:"runbook_url,omitempty"`
}

// adminPatchCloudAccountReq is the body for PATCH /v1/admin/cloud-accounts/{id}.
// AK/SK are never accepted here — see /credentials.
type adminPatchCloudAccountReq struct {
	Name        *string            `json:"name,omitempty"`
	Region      *string            `json:"region,omitempty"`
	Owner       *string            `json:"owner,omitempty"`
	Criticality *string            `json:"criticality,omitempty"`
	Notes       *string            `json:"notes,omitempty"`
	RunbookURL  *string            `json:"runbook_url,omitempty"`
	Annotations *map[string]string `json:"annotations,omitempty"`
}

// adminCredentialsReq is the body for PATCH /v1/admin/cloud-accounts/{id}/credentials.
type adminCredentialsReq struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
}

// collectorRegisterReq is the body for POST /v1/cloud-accounts.
type collectorRegisterReq struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Region   string `json:"region"`
}

// collectorStatusReq is the body for PATCH /v1/cloud-accounts/{id}/status.
type collectorStatusReq struct {
	Status      *string   `json:"status,omitempty"`
	LastSeenAt  *jsonTime `json:"last_seen_at,omitempty"`
	LastError   *string   `json:"last_error,omitempty"`
	LastErrorAt *jsonTime `json:"last_error_at,omitempty"`
}

// credentialsResp is the response body for GET /v1/cloud-accounts/.../credentials.
type credentialsResp struct {
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Region    string `json:"region"`
	Provider  string `json:"provider"`
}

// jsonTime is a thin wrapper that survives nil parsing in optional
// timestamp fields without forcing every caller to construct a pointer
// to time.Time.
type jsonTime struct {
	Value string
}

// UnmarshalJSON implements json.Unmarshaler for jsonTime, tolerating null.
func (t *jsonTime) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	t.Value = strings.Trim(string(b), `"`)
	return nil
}

// HandleListCloudAccounts — admin scope. Paginated list.
func HandleListCloudAccounts(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		cursor := r.URL.Query().Get("cursor")
		items, next, err := store.ListCloudAccounts(r.Context(), limit, cursor)
		if err != nil {
			slog.Error("list cloud accounts", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	}
}

// HandleCreateCloudAccount — admin scope. POST /v1/admin/cloud-accounts.
//
//nolint:gocyclo // multi-step onboarding flow: upsert + optional patch + optional credential set
func HandleCreateCloudAccount(store Store, enc *secrets.Encrypter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		var req adminCreateCloudAccountReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if req.Provider == "" || req.Name == "" || req.Region == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "provider, name, region required")
			return
		}
		acct, err := store.UpsertCloudAccount(r.Context(), CloudAccountUpsert{
			Provider: req.Provider, Name: req.Name, Region: req.Region,
		})
		if err != nil {
			slog.Error("create cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		// Apply optional curated metadata if present.
		patch := CloudAccountPatch{
			Owner: req.Owner, Criticality: req.Criticality,
			Notes: req.Notes, RunbookURL: req.RunbookURL,
		}
		if patch.Owner != nil || patch.Criticality != nil || patch.Notes != nil || patch.RunbookURL != nil {
			acct, err = store.UpdateCloudAccount(r.Context(), acct.ID, patch)
			if err != nil {
				slog.Error("create cloud account: apply curated metadata", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		}
		// Set credentials in a single shot if supplied.
		if req.AccessKey != "" && req.SecretKey != "" {
			if enc == nil {
				writeProblem(w, http.StatusServiceUnavailable, "Encryption Disabled",
					"argosd has no master key; cannot store credentials")
				return
			}
			ct, err := enc.Encrypt([]byte(req.SecretKey), acct.ID[:])
			if err != nil {
				slog.Error("encrypt secret key", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
			acct, err = store.SetCloudAccountCredentials(r.Context(), acct.ID, req.AccessKey, ct)
			if err != nil {
				slog.Error("set credentials", slog.Any("error", err))
				writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
				return
			}
		}
		writeJSON(w, http.StatusCreated, acct)
	}
}

// HandleGetCloudAccount — admin scope. GET /v1/admin/cloud-accounts/{id}.
func HandleGetCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		acct, err := store.GetCloudAccount(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("get cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, acct)
	}
}

// HandlePatchCloudAccount — admin scope. PATCH /v1/admin/cloud-accounts/{id}.
func HandlePatchCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var req adminPatchCloudAccountReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		acct, err := store.UpdateCloudAccount(r.Context(), id, CloudAccountPatch{
			Name: req.Name, Region: req.Region,
			Owner: req.Owner, Criticality: req.Criticality,
			Notes: req.Notes, RunbookURL: req.RunbookURL,
			Annotations: req.Annotations,
		})
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			if errors.Is(err, ErrConflict) {
				writeProblem(w, http.StatusConflict, "Conflict", err.Error())
				return
			}
			slog.Error("patch cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, acct)
	}
}

// HandlePatchCloudAccountCredentials — admin scope.
// PATCH /v1/admin/cloud-accounts/{id}/credentials.
// SK is encrypted with AES-256-GCM, AAD = row UUID.
func HandlePatchCloudAccountCredentials(store Store, enc *secrets.Encrypter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		var req adminCredentialsReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if req.AccessKey == "" || req.SecretKey == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "access_key and secret_key required")
			return
		}
		if enc == nil {
			writeProblem(w, http.StatusServiceUnavailable, "Encryption Disabled",
				"argosd has no master key; cannot store credentials")
			return
		}
		ct, err := enc.Encrypt([]byte(req.SecretKey), id[:])
		if err != nil {
			slog.Error("encrypt secret key", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		acct, err := store.SetCloudAccountCredentials(r.Context(), id, req.AccessKey, ct)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("set credentials", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		writeJSON(w, http.StatusOK, acct)
	}
}

// HandleDisableCloudAccount — admin scope. POST .../{id}/disable.
func HandleDisableCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DisableCloudAccount(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("disable cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleEnableCloudAccount — admin scope. POST .../{id}/enable.
func HandleEnableCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.EnableCloudAccount(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("enable cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleDeleteCloudAccount — admin scope. DELETE .../{id}.
func HandleDeleteCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := store.DeleteCloudAccount(r.Context(), id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			slog.Error("delete cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleCollectorRegisterCloudAccount — vm-collector scope.
// POST /v1/cloud-accounts. Idempotent first-contact registration.
func HandleCollectorRegisterCloudAccount(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		var req collectorRegisterReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		if req.Provider == "" || req.Name == "" || req.Region == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "provider, name, region required")
			return
		}
		acct, err := store.UpsertCloudAccount(r.Context(), CloudAccountUpsert(req))
		if err != nil {
			slog.Error("collector register cloud account", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		// Enforce binding now that the row exists. Reject the request when
		// the calling token is bound to a different account.
		if err := caller.EnforceCloudAccountBinding(acct.ID); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token is bound to a different cloud account")
			return
		}
		writeJSON(w, http.StatusOK, acct)
	}
}

// HandleCollectorPatchCloudAccountStatus — vm-collector scope.
// PATCH /v1/cloud-accounts/{id}/status.
//
//nolint:gocyclo // status patch validates multiple optional fields; branching is unavoidable
func HandleCollectorPatchCloudAccountStatus(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := caller.EnforceCloudAccountBinding(id); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token not bound to this cloud account")
			return
		}
		var req collectorStatusReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON body")
			return
		}
		var status string
		if req.Status != nil {
			status = *req.Status
		}
		lastSeenAt := parseOptTime(req.LastSeenAt)
		var lastErr *string
		if req.LastError != nil {
			lastErr = req.LastError
		}
		if err := store.UpdateCloudAccountStatus(r.Context(), id, status, lastSeenAt, lastErr); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeProblem(w, http.StatusNotFound, "Not Found", "")
				return
			}
			if errors.Is(err, ErrConflict) {
				writeProblem(w, http.StatusConflict, "Conflict", err.Error())
				return
			}
			slog.Error("collector patch status", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleCollectorGetCredentialsByName — vm-collector scope.
// GET /v1/cloud-accounts/by-name/{name}/credentials. Returns plaintext SK.
func HandleCollectorGetCredentialsByName(store Store, enc *secrets.Encrypter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		name := r.PathValue("name")
		if name == "" {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "name required")
			return
		}
		// We accept any provider here because a vm-collector PAT is bound
		// by id, not by (provider, name). Lookup may succeed for an
		// account this caller has no business reading; in that case we
		// MUST return the same 404 the not-found path returns, otherwise
		// a bound caller could enumerate cloud-account names by
		// distinguishing 403 (exists, wrong binding) from 404 (no row).
		// ADR-0015 §7 calls this side channel out explicitly.
		acct, err := lookupAccountByName(r, store, name)
		if err != nil {
			handleAccountLookupErr(w, err)
			return
		}
		if err := caller.EnforceCloudAccountBinding(acct.ID); err != nil {
			writeProblem(w, http.StatusNotFound, "Cloud Account Not Registered", "")
			return
		}
		respondCredentials(w, r, store, enc, &acct)
	}
}

// HandleCollectorGetCredentialsByID — vm-collector scope.
// GET /v1/cloud-accounts/{id}/credentials.
func HandleCollectorGetCredentialsByID(store Store, enc *secrets.Encrypter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil || !caller.HasScope(auth.ScopeVMCollector) {
			writeProblem(w, http.StatusForbidden, "Forbidden", "vm-collector scope required")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		if err := caller.EnforceCloudAccountBinding(id); err != nil {
			writeProblem(w, http.StatusForbidden, "Forbidden",
				"token not bound to this cloud account")
			return
		}
		acct, err := store.GetCloudAccount(r.Context(), id)
		if err != nil {
			handleAccountLookupErr(w, err)
			return
		}
		respondCredentials(w, r, store, enc, &acct)
	}
}

func respondCredentials(w http.ResponseWriter, r *http.Request, store Store, enc *secrets.Encrypter, acct *CloudAccount) {
	if enc == nil {
		writeProblem(w, http.StatusServiceUnavailable, "Encryption Disabled",
			"argosd has no master key; cannot decrypt credentials")
		return
	}
	ak, ct, err := store.GetCloudAccountCredentials(r.Context(), acct.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Either the row is gone or the credentials haven't been
			// supplied by the admin yet — same response shape per
			// ADR-0015 §6.
			writeProblem(w, http.StatusNotFound, "Cloud Account Not Registered",
				"credentials not yet provisioned")
			return
		}
		if errors.Is(err, ErrConflict) {
			writeProblem(w, http.StatusForbidden, "Account Disabled",
				"account is disabled; ask an admin to re-enable")
			return
		}
		slog.Error("get credentials", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
		return
	}
	plaintext, err := enc.Decrypt(ct, acct.ID[:])
	if err != nil {
		slog.Error("decrypt credentials", slog.Any("error", err))
		writeProblem(w, http.StatusInternalServerError, "Decryption Failed",
			"the master key may be incorrect")
		return
	}
	// IMPORTANT: never log the plaintext SK. The audit middleware logs
	// request bodies, not response bodies, so this never reaches the
	// audit table — but we leave the comment as a guardrail for future
	// changes.
	metrics.ObserveCredentialsRead(acct.Name)
	writeJSON(w, http.StatusOK, credentialsResp{
		AccessKey: ak,
		SecretKey: string(plaintext),
		Region:    acct.Region,
		Provider:  acct.Provider,
	})
}

// lookupAccountByName is a thin wrapper kept so existing call sites
// don't change. The store now does a single-query lookup across
// providers (ADR-0015 M3) — no more per-provider fan-out.
func lookupAccountByName(r *http.Request, store Store, name string) (CloudAccount, error) {
	acct, err := store.GetCloudAccountByNameAny(r.Context(), name)
	if err == nil {
		return acct, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return CloudAccount{}, fmt.Errorf("get cloud account by name: %w", err)
	}
	return CloudAccount{}, ErrNotFound
}

// legacyLookupAccountByName is the previous fan-out implementation.
// Retained but unreferenced in case a future provider needs an
// allow-list-style lookup. Compiled-out by the unused-symbol linter
// if it survives a future cleanup pass.
//
//nolint:unused // kept as a reference impl
func legacyLookupAccountByName(r *http.Request, store Store, name string) (CloudAccount, error) {
	// We only ship one provider in v1, but the lookup signature still
	// takes (provider, name) so future providers slot in cleanly. Try
	// every known provider until one matches; for v1 this is just
	// "outscale". Iterating over a small whitelist beats an extra
	// "list-by-name-only" store method.
	candidates := []string{"outscale", "aws", "ovh", "scaleway", "azure"}
	for _, p := range candidates {
		acct, err := store.GetCloudAccountByName(r.Context(), p, name)
		if err == nil {
			return acct, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return CloudAccount{}, fmt.Errorf("get cloud account by name: %w", err)
		}
	}
	return CloudAccount{}, ErrNotFound
}

func handleAccountLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeProblem(w, http.StatusNotFound, "Cloud Account Not Registered", "")
		return
	}
	slog.Error("lookup cloud account", slog.Any("error", err))
	writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
}

// pathUUID extracts a UUID from a router path value. Returns (uuid, true)
// on success or writes a 400 and returns (_, false).
//
//nolint:unparam // name is kept for error message clarity; future routes may use different parameter names
func pathUUID(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := r.PathValue(name)
	if raw == "" {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "missing path parameter "+name)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// adminCreateCloudAccountTokenReq is the body for
// POST /v1/admin/cloud-accounts/{id}/tokens — admin mints a vm-collector
// PAT bound to this account.
type adminCreateCloudAccountTokenReq struct {
	Name      string    `json:"name"`
	ExpiresAt *jsonTime `json:"expires_at,omitempty"`
}

// adminCreateCloudAccountTokenResp returns the minted plaintext once.
// Mirrors ApiTokenMint from the codegen but inlined here so this hand-
// written endpoint isn't coupled to the OpenAPI types.
type adminCreateCloudAccountTokenResp struct {
	ID                  uuid.UUID  `json:"id"`
	Name                string     `json:"name"`
	Prefix              string     `json:"prefix"`
	Scopes              []string   `json:"scopes"`
	BoundCloudAccountID uuid.UUID  `json:"bound_cloud_account_id"`
	CreatedByUserID     uuid.UUID  `json:"created_by_user_id"`
	CreatedAt           time.Time  `json:"created_at"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	Token               string     `json:"token"`
}

// HandleCreateCloudAccountToken — admin scope. Mints a vm-collector PAT
// bound to the cloud account in the URL path. Body: {name, expires_at?}.
// The plaintext is returned exactly once. Audit middleware scrubs the
// response body (responses are not logged) — this endpoint is safe to
// audit by request only.
func HandleCreateCloudAccountToken(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireAdmin(w, r) {
			return
		}
		caller := auth.CallerFromContext(r.Context())
		if caller == nil {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "")
			return
		}
		id, ok := pathUUID(w, r, "id")
		if !ok {
			return
		}
		// Verify the account exists before issuing — issuing a bound
		// token against a non-existent account would fail at FK time
		// with an opaque error.
		if _, err := store.GetCloudAccount(r.Context(), id); err != nil {
			handleAccountLookupErr(w, err)
			return
		}

		var req adminCreateCloudAccountTokenReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeProblem(w, http.StatusBadRequest, "Bad Request", "invalid JSON")
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeProblem(w, http.StatusBadRequest, "Missing field", "name is required")
			return
		}

		minted, err := auth.MintToken()
		if err != nil {
			slog.Error("mint token", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}
		tokenID := uuid.New()
		boundID := id
		expiresAt := parseOptTime(req.ExpiresAt)
		_, err = store.CreateAPIToken(r.Context(), APITokenInsert{
			ID:                  tokenID,
			Name:                req.Name,
			Prefix:              minted.Prefix,
			Hash:                minted.Hash,
			Scopes:              []string{auth.ScopeVMCollector},
			CreatedByUserID:     caller.UserID,
			ExpiresAt:           expiresAt,
			BoundCloudAccountID: &boundID,
		})
		if err != nil {
			slog.Error("create vm-collector token", slog.Any("error", err))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "")
			return
		}

		resp := adminCreateCloudAccountTokenResp{
			ID:                  tokenID,
			Name:                req.Name,
			Prefix:              minted.Prefix,
			Scopes:              []string{auth.ScopeVMCollector},
			BoundCloudAccountID: id,
			CreatedByUserID:     caller.UserID,
			CreatedAt:           time.Now().UTC(),
			ExpiresAt:           expiresAt,
			Token:               minted.Plaintext,
		}
		writeJSON(w, http.StatusCreated, resp)
	}
}

// writeJSON writes an application/json response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("writeJSON: encode error", slog.Any("error", err))
	}
}

// writeProblem writes a minimal RFC 7807 application/problem+json response.
func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	body := map[string]any{"type": "about:blank", "title": title, "status": status}
	if detail != "" {
		body["detail"] = detail
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("writeProblem: encode error", slog.Any("error", err))
	}
}
