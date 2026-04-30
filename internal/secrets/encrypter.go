// Package secrets implements envelope encryption of long-lived secrets
// stored in the CMDB (cloud-provider AK/SK pairs per ADR-0015).
//
// Current scheme: AES-256-GCM with a single master key delivered through
// the LONGUE_VUE_SECRETS_MASTER_KEY env var (base64-encoded 32 bytes). The
// AAD (Additional Authenticated Data) is bound to the row's primary key
// so a database restore cannot move a ciphertext between rows.
//
// Each ciphertext is stored alongside its nonce and a key id (`kid`).
// Today the only kid is "v1"; the column lets a future rotation ADR
// introduce a multi-key scheme without a schema change.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
)

// MasterKeyEnvVar is the env var the operator sets on argosd's
// deployment to deliver the 32-byte AES-256 master key (base64-encoded).
const MasterKeyEnvVar = "LONGUE_VUE_SECRETS_MASTER_KEY"

// CurrentKID is the key id stamped on every fresh ciphertext. A future
// rotation ADR will introduce a registry of {kid → key} pairs.
const CurrentKID = "v1"

// MasterKeySize is the required raw byte length of the AES-256 master key.
const MasterKeySize = 32

// Errors returned by the package. Callers map these to HTTP problem
// responses or log lines as appropriate.
var (
	// ErrMasterKeyMissing is returned by NewEncrypterFromEnv when
	// LONGUE_VUE_SECRETS_MASTER_KEY is unset or empty.
	ErrMasterKeyMissing = errors.New("master key not configured")
	// ErrMasterKeySize is returned when the decoded master key is not
	// exactly 32 bytes long.
	ErrMasterKeySize = errors.New("master key must decode to 32 bytes")
	// ErrUnknownKID is returned when a ciphertext carries a kid this
	// build does not recognise.
	ErrUnknownKID = errors.New("unknown key id")
)

// Encrypter encrypts and decrypts byte payloads under a single master
// key. Safe for concurrent use — cipher.AEAD is itself goroutine-safe
// and we never mutate the master key after construction.
type Encrypter struct {
	aead cipher.AEAD
	// keyHash is the SHA-256 of the raw master key. The leading 8
	// hex chars are reported by Fingerprint() so an operator can
	// confirm the right key is loaded at startup.
	keyHash [sha256.Size]byte
}

// NewEncrypter builds an Encrypter from a raw 32-byte master key. The
// key is consumed by the AES cipher and never retained verbatim by
// the Encrypter.
func NewEncrypter(masterKey []byte) (*Encrypter, error) {
	if len(masterKey) != MasterKeySize {
		return nil, fmt.Errorf("got %d bytes: %w", len(masterKey), ErrMasterKeySize)
	}
	block, err := aes.NewCipher(masterKey)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher new gcm: %w", err)
	}
	enc := &Encrypter{
		aead:    aead,
		keyHash: sha256.Sum256(masterKey),
	}
	return enc, nil
}

// NewEncrypterFromEnv builds an Encrypter from LONGUE_VUE_SECRETS_MASTER_KEY.
// Returns ErrMasterKeyMissing if unset, ErrMasterKeySize if the decoded
// length is wrong.
func NewEncrypterFromEnv() (*Encrypter, error) {
	raw := os.Getenv(MasterKeyEnvVar)
	if raw == "" {
		return nil, ErrMasterKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("base64 decode %s: %w", MasterKeyEnvVar, err)
	}
	return NewEncrypter(key)
}

// Fingerprint returns the first 8 hex chars of SHA-256(masterKey).
// argosd logs this at startup so operators can confirm the right key
// is loaded without ever exposing the key itself.
func (e *Encrypter) Fingerprint() string {
	const hex = "0123456789abcdef"
	out := make([]byte, 8)
	for i := range 4 {
		out[i*2] = hex[e.keyHash[i]>>4]
		out[i*2+1] = hex[e.keyHash[i]&0x0f]
	}
	return string(out)
}

// Ciphertext is the persisted form of an encrypted secret. All three
// fields must travel together — the nonce is required for decryption,
// the kid disambiguates which master key was used.
type Ciphertext struct {
	Bytes []byte
	Nonce []byte
	KID   string
}

// Encrypt seals plaintext under the master key with the given AAD. The
// AAD typically contains the row's primary key (e.g. the cloud_account
// UUID's bytes) so a backup-restore cannot move the ciphertext between
// rows. Returns a fresh random nonce on every call — never reuse a
// nonce with the same key (GCM security depends on it).
func (e *Encrypter) Encrypt(plaintext, aad []byte) (Ciphertext, error) {
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Ciphertext{}, fmt.Errorf("read nonce: %w", err)
	}
	sealed := e.aead.Seal(nil, nonce, plaintext, aad)
	return Ciphertext{Bytes: sealed, Nonce: nonce, KID: CurrentKID}, nil
}

// Decrypt opens a Ciphertext under the master key with the same AAD
// that was used at encryption time. Returns a non-nil error when the
// AAD doesn't match, the ciphertext is tampered, or the kid is unknown.
func (e *Encrypter) Decrypt(ct Ciphertext, aad []byte) ([]byte, error) {
	if ct.KID != CurrentKID {
		return nil, fmt.Errorf("kid %q: %w", ct.KID, ErrUnknownKID)
	}
	plaintext, err := e.aead.Open(nil, ct.Nonce, ct.Bytes, aad)
	if err != nil {
		return nil, fmt.Errorf("aead open: %w", err)
	}
	return plaintext, nil
}
