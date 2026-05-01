package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Token plaintext format: `longue_vue_pat_<prefix>_<suffix>`
//
// New tokens are issued with the `longue_vue_pat_` scheme (TokenScheme).
// Legacy tokens minted before the rename used the `argos_pat_` scheme
// (TokenSchemeLegacy) and are accepted by ParseToken forever — production
// deployments may carry them indefinitely. MintToken only ever emits
// the new scheme.
//
//   - The scheme prefix namespaces the value so it's greppable (GitHub's
//     `ghp_` / `gho_` pattern). If this ever leaks into logs or source
//     control, secret scanners can catch it.
//   - `<prefix>` is 8 URL-safe characters — stored in the clear in
//     the `api_tokens.prefix` column so middleware can locate the row
//     in O(1). Prefix alone is not a credential (collision space is
//     62^8 ≈ 2·10^14, plenty for display+index; the suffix is the
//     actual secret).
//   - `<suffix>` is 32 URL-safe characters, random. Together with the
//     prefix, argon2id-hashed at rest.
const (
	TokenScheme       = "longue_vue_pat_"
	TokenSchemeLegacy = "argos_pat_"
	tokenPrefixLen    = 8
	tokenSuffixBytes  = 24 // 24 raw → 32 chars base64-url-unpadded
)

// ErrInvalidTokenFormat means the Authorization header didn't carry a
// value shaped like either the current scheme (`longue_vue_pat_<prefix>_<suffix>`)
// or the legacy scheme (`argos_pat_<prefix>_<suffix>`). Returned by ParseToken;
// handlers translate to 401 without leaking which bit was wrong.
var ErrInvalidTokenFormat = errors.New("invalid token format")

// MintedToken is what the admin token-creation flow returns: the full
// plaintext (handed to the operator once), the prefix (persisted for
// lookup), and the argon2id hash (persisted for verification). Keep the
// plaintext on the wire only long enough to send it back in the response
// body — never log it, never store it.
type MintedToken struct {
	Plaintext string
	Prefix    string
	Hash      string
}

// MintToken generates a fresh token plaintext and the persistable pieces
// that go with it. Prefix is hex-encoded so it can't collide with the
// underscore separator; the suffix is base64-url from RandomSecret.
func MintToken() (MintedToken, error) {
	prefixBytes := make([]byte, tokenPrefixLen/2) // 4 raw → 8 hex chars
	if _, err := rand.Read(prefixBytes); err != nil {
		return MintedToken{}, fmt.Errorf("token prefix: %w", err)
	}
	prefix := hex.EncodeToString(prefixBytes)
	suffix, err := RandomSecret(tokenSuffixBytes)
	if err != nil {
		return MintedToken{}, err
	}
	plaintext := TokenScheme + prefix + "_" + suffix
	hash, err := HashPassword(plaintext)
	if err != nil {
		return MintedToken{}, err
	}
	return MintedToken{
		Plaintext: plaintext,
		Prefix:    prefix,
		Hash:      hash,
	}, nil
}

// ParseToken splits a presented plaintext into its prefix (for store
// lookup) and full form (for argon2id verify against the stored hash).
// Accepts both the current scheme (TokenScheme = "longue_vue_pat_") and
// the legacy scheme (TokenSchemeLegacy = "argos_pat_") so production
// tokens minted before the rename continue to verify.
// Returns ErrInvalidTokenFormat on anything not shaped like either scheme.
func ParseToken(plaintext string) (prefix, full string, err error) {
	var rest string
	switch {
	case strings.HasPrefix(plaintext, TokenScheme):
		rest = strings.TrimPrefix(plaintext, TokenScheme)
	case strings.HasPrefix(plaintext, TokenSchemeLegacy):
		rest = strings.TrimPrefix(plaintext, TokenSchemeLegacy)
	default:
		return "", "", ErrInvalidTokenFormat
	}
	sep := strings.IndexByte(rest, '_')
	if sep != tokenPrefixLen {
		return "", "", fmt.Errorf("prefix must be %d chars: %w", tokenPrefixLen, ErrInvalidTokenFormat)
	}
	return rest[:tokenPrefixLen], plaintext, nil
}
