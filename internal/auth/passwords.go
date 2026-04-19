// Package auth owns the cryptographic primitives for human authentication
// per ADR-0007: argon2id password hashing, random secret generation for
// session cookies and API-token plaintexts, and the PHC-encoded format
// helpers the store reads back.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters chosen for interactive login on modest hardware:
// ~150 ms per hash on a 2023-era laptop. Time+memory dominate; threads
// set to 4 for parallelism on a small cloud VM.
//
// If these change, existing hashes keep working — the parameters are
// embedded in the PHC-encoded output and HashPassword reads them on
// verify. New hashes use whatever is set here.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // KiB — 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrPasswordMismatch is returned by VerifyPassword when the supplied
// plaintext doesn't match the hash. Handlers translate it to 401.
var ErrPasswordMismatch = errors.New("password does not match")

// ErrInvalidHashFormat surfaces when a stored hash doesn't parse as PHC —
// an indication of data corruption, not an auth failure.
var ErrInvalidHashFormat = errors.New("invalid password hash format")

// HashPassword produces a PHC-encoded argon2id hash with a fresh random
// salt. Safe to call with an empty password — callers should validate
// length upstream.
func HashPassword(plaintext string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id salt: %w", err)
	}
	hash := argon2.IDKey([]byte(plaintext), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword compares a plaintext against a stored PHC-encoded
// argon2id hash. Returns nil on match, ErrPasswordMismatch on no match,
// ErrInvalidHashFormat when the stored hash is malformed.
func VerifyPassword(plaintext, encoded string) error {
	parts := strings.Split(encoded, "$")
	// $argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
	if len(parts) != 6 || parts[1] != "argon2id" {
		return ErrInvalidHashFormat
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return ErrInvalidHashFormat
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return ErrInvalidHashFormat
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return ErrInvalidHashFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return ErrInvalidHashFormat
	}

	got := argon2.IDKey([]byte(plaintext), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

// RandomSecret returns `n` random bytes, URL-safe base64 encoded and
// stripped of padding. Used for session cookie ids (n=32) and the random
// suffix inside minted API tokens.
func RandomSecret(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
