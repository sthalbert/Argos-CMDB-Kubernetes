package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/secrets"
)

// imageRegistryColumns lists the SELECT columns in canonical order.
const imageRegistryColumns = `hostname, path_prefix, rate_limit_per_sec, enabled, notes,
	is_mirror, replicates_from_hostname, auth_username, auth_token_ciphertext, created_at, updated_at`

// Static errors for the image-registry store. err113 forbids ad-hoc
// fmt.Errorf without %w wrapping.
var (
	errCiphertextTooShort     = errors.New("image registry: ciphertext too short")
	errEncrypterNotConfigured = errors.New("image registry: encrypter not configured")
)

func scanImageRegistry(row pgx.Row) (api.ImageRegistry, error) {
	var r api.ImageRegistry
	var authUser *string
	var authToken []byte
	if err := row.Scan(&r.Hostname, &r.PathPrefix, &r.RateLimitPerSec, &r.Enabled, &r.Notes,
		&r.IsMirror, &r.ReplicatesFromHostname, &authUser, &authToken,
		&r.CreatedAt, &r.UpdatedAt); err != nil {
		return api.ImageRegistry{}, fmt.Errorf("scan image registry: %w", err)
	}
	r.AuthUsername = authUser
	r.AuthConfigured = len(authToken) > 0
	return r, nil
}

// aadForImageRegistry binds the encrypted token to the row's composite PK.
func aadForImageRegistry(hostname, pathPrefix string) []byte {
	return []byte("image_versions_registries|" + hostname + "|" + pathPrefix)
}

// packCiphertext encodes a Ciphertext into a single BYTEA: nonce ‖ sealed.
// The KID is implicit (always CurrentKID for writes); reads accept the
// current KID only.
func packCiphertext(ct secrets.Ciphertext) []byte {
	out := make([]byte, 0, len(ct.Nonce)+len(ct.Bytes))
	out = append(out, ct.Nonce...)
	out = append(out, ct.Bytes...)
	return out
}

// unpackCiphertext splits a stored BYTEA back into a Ciphertext. nonceSize
// must match the encrypter's AEAD nonce size (12 for AES-GCM).
func unpackCiphertext(raw []byte, nonceSize int) (secrets.Ciphertext, error) {
	if len(raw) < nonceSize {
		return secrets.Ciphertext{}, fmt.Errorf("%w: %d bytes", errCiphertextTooShort, len(raw))
	}
	return secrets.Ciphertext{
		Nonce: raw[:nonceSize],
		Bytes: raw[nonceSize:],
		KID:   secrets.CurrentKID,
	}, nil
}

// ListImageRegistries returns all rows ordered by (hostname, path_prefix).
func (p *PG) ListImageRegistries(ctx context.Context) ([]api.ImageRegistry, error) {
	q := `SELECT ` + imageRegistryColumns + `
FROM image_versions_registries
ORDER BY hostname, path_prefix`
	rows, err := p.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list image registries: %w", err)
	}
	defer rows.Close()
	out := make([]api.ImageRegistry, 0)
	for rows.Next() {
		r, err := scanImageRegistry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan image registry: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate image registries: %w", err)
	}
	return out, nil
}

// GetImageRegistry fetches a single registry by (hostname, path_prefix).
func (p *PG) GetImageRegistry(ctx context.Context, hostname, pathPrefix string) (api.ImageRegistry, error) {
	q := `SELECT ` + imageRegistryColumns + `
FROM image_versions_registries WHERE hostname = $1 AND path_prefix = $2`
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname, pathPrefix))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageRegistry{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageRegistry{}, fmt.Errorf("get image registry: %w", err)
	}
	return r, nil
}

// CreateImageRegistry inserts a new registry row.
//
//nolint:gocritic // in matches the api.Store interface signature shared by every CRUD method (hugeParam expected)
func (p *PG) CreateImageRegistry(ctx context.Context, in api.ImageRegistryUpsert) (api.ImageRegistry, error) {
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var tokenCT []byte
	if in.AuthToken != nil && *in.AuthToken != "" {
		if p.encrypter == nil {
			return api.ImageRegistry{}, fmt.Errorf("create image registry: %w", errEncrypterNotConfigured)
		}
		ct, err := p.encrypter.Encrypt([]byte(*in.AuthToken), aadForImageRegistry(in.Hostname, in.PathPrefix))
		if err != nil {
			return api.ImageRegistry{}, fmt.Errorf("encrypt token: %w", err)
		}
		tokenCT = packCiphertext(ct)
	}
	q := `INSERT INTO image_versions_registries
	(hostname, path_prefix, rate_limit_per_sec, enabled, notes,
	 is_mirror, replicates_from_hostname, auth_username, auth_token_ciphertext)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING ` + imageRegistryColumns
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q,
		in.Hostname, in.PathPrefix, in.RateLimitPerSec, enabled, in.Notes,
		in.IsMirror, in.ReplicatesFromHostname, in.AuthUsername, tokenCT))
	if err != nil {
		if isUniqueViolation(err) {
			return api.ImageRegistry{}, api.ErrConflict
		}
		return api.ImageRegistry{}, fmt.Errorf("create image registry: %w", err)
	}
	return r, nil
}

// UpdateImageRegistry applies a merge-patch.
//
// AuthToken semantics:
//   - nil   → leave ciphertext unchanged
//   - ""    → clear ciphertext (set to NULL); also clears auth_username
//   - other → re-encrypt with PK-bound AAD
//
//nolint:gocyclo // straight-line merge-patch branches per nullable field; flat structure is clearer than factored helpers
func (p *PG) UpdateImageRegistry(ctx context.Context, hostname, pathPrefix string, patch api.ImageRegistryPatch) (api.ImageRegistry, error) {
	var (
		tokenProvided bool
		tokenCT       []byte
		clearToken    bool
	)
	if patch.AuthToken != nil {
		tokenProvided = true
		if *patch.AuthToken == "" {
			clearToken = true
		} else {
			if p.encrypter == nil {
				return api.ImageRegistry{}, fmt.Errorf("update image registry: %w", errEncrypterNotConfigured)
			}
			ct, err := p.encrypter.Encrypt([]byte(*patch.AuthToken), aadForImageRegistry(hostname, pathPrefix))
			if err != nil {
				return api.ImageRegistry{}, fmt.Errorf("encrypt token: %w", err)
			}
			tokenCT = packCiphertext(ct)
		}
	}

	var (
		replicaProvided bool
		clearReplica    bool
		replicaTarget   string
	)
	if patch.ReplicatesFromHostname != nil {
		replicaProvided = true
		if *patch.ReplicatesFromHostname == "" {
			clearReplica = true
		} else {
			replicaTarget = *patch.ReplicatesFromHostname
		}
	}

	notesProvided := patch.Notes != nil
	userProvided := patch.AuthUsername != nil

	q := `UPDATE image_versions_registries SET
		rate_limit_per_sec       = COALESCE($3, rate_limit_per_sec),
		enabled                  = COALESCE($4, enabled),
		notes                    = CASE WHEN $5::bool THEN $6 ELSE notes END,
		is_mirror                = COALESCE($7, is_mirror),
		replicates_from_hostname = CASE
		                              WHEN $8::bool THEN NULL
		                              WHEN $9::bool THEN $10
		                              ELSE replicates_from_hostname
		                           END,
		auth_username            = CASE WHEN $11::bool THEN $12 ELSE auth_username END,
		auth_token_ciphertext    = CASE
		                              WHEN $13::bool THEN NULL
		                              WHEN $14::bool THEN $15
		                              ELSE auth_token_ciphertext
		                           END,
		updated_at               = now()
	WHERE hostname = $1 AND path_prefix = $2
	RETURNING ` + imageRegistryColumns

	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q,
		hostname, pathPrefix,
		patch.RateLimitPerSec, patch.Enabled,
		notesProvided, patch.Notes,
		patch.IsMirror,
		clearReplica, replicaProvided && !clearReplica, replicaTarget,
		userProvided, patch.AuthUsername,
		clearToken,
		tokenProvided && !clearToken, tokenCT,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageRegistry{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageRegistry{}, fmt.Errorf("update image registry: %w", err)
	}
	return r, nil
}

// DeleteImageRegistry removes a registry by (hostname, path_prefix).
func (p *PG) DeleteImageRegistry(ctx context.Context, hostname, pathPrefix string) error {
	tag, err := p.pool.Exec(ctx,
		`DELETE FROM image_versions_registries WHERE hostname = $1 AND path_prefix = $2`,
		hostname, pathPrefix)
	if err != nil {
		return fmt.Errorf("delete image registry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return api.ErrNotFound
	}
	return nil
}

// FindMirrorForRef returns the longest-prefix mirror row whose hostname
// matches and whose path_prefix is a prefix of imagePath.
func (p *PG) FindMirrorForRef(ctx context.Context, hostname, imagePath string) (api.ImageRegistry, error) {
	q := `SELECT ` + imageRegistryColumns + `
FROM image_versions_registries
WHERE hostname = $1
  AND $2 LIKE path_prefix || '%'
  AND is_mirror = TRUE
  AND enabled = TRUE
ORDER BY length(path_prefix) DESC
LIMIT 1`
	r, err := scanImageRegistry(p.pool.QueryRow(ctx, q, hostname, imagePath))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ImageRegistry{}, api.ErrNotFound
	}
	if err != nil {
		return api.ImageRegistry{}, fmt.Errorf("find mirror for ref: %w", err)
	}
	return r, nil
}

// GetMirrorAuthToken decrypts and returns the robot-account token for the
// row keyed by (hostname, pathPrefix). Empty string when no token is set.
// Callers MUST NOT log the return value.
func (p *PG) GetMirrorAuthToken(ctx context.Context, hostname, pathPrefix string) (string, error) {
	const q = `SELECT auth_token_ciphertext FROM image_versions_registries
		WHERE hostname=$1 AND path_prefix=$2`
	var ct []byte
	err := p.pool.QueryRow(ctx, q, hostname, pathPrefix).Scan(&ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", api.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get mirror token: %w", err)
	}
	if len(ct) == 0 {
		return "", nil
	}
	if p.encrypter == nil {
		return "", fmt.Errorf("get mirror token: %w", errEncrypterNotConfigured)
	}
	packed, err := unpackCiphertext(ct, p.encrypter.NonceSize())
	if err != nil {
		return "", fmt.Errorf("unpack mirror token: %w", err)
	}
	pt, err := p.encrypter.Decrypt(packed, aadForImageRegistry(hostname, pathPrefix))
	if err != nil {
		return "", fmt.Errorf("decrypt mirror token: %w", err)
	}
	return string(pt), nil
}
