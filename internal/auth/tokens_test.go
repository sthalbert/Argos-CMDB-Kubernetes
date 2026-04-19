package auth

import (
	"strings"
	"testing"
)

func TestMintTokenFormat(t *testing.T) {
	t.Parallel()
	// Run the mint a few times because the suffix is base64url, which
	// can legitimately contain `_`. Earlier revisions of this test
	// split the plaintext on every `_` and failed intermittently
	// whenever the random suffix happened to carry one; ParseToken
	// itself uses IndexByte to find the first `_`, so match that
	// (the first `_` after the scheme is always the separator because
	// the prefix is hex-only).
	for i := 0; i < 50; i++ {
		tok, err := MintToken()
		if err != nil {
			t.Fatalf("MintToken: %v", err)
		}

		if !strings.HasPrefix(tok.Plaintext, TokenScheme) {
			t.Errorf("plaintext missing scheme: %q", tok.Plaintext)
		}

		rest := strings.TrimPrefix(tok.Plaintext, TokenScheme)
		sep := strings.IndexByte(rest, '_')
		if sep != 8 {
			t.Fatalf("first `_` should be at position 8 (after 8-hex prefix), got %d in %q", sep, rest)
		}

		prefix := rest[:sep]
		// Prefix is hex — must not contain `_` itself.
		if strings.ContainsRune(prefix, '_') {
			t.Errorf("prefix contains underscore: %q", prefix)
		}
		if tok.Prefix != prefix {
			t.Errorf("stored prefix %q differs from plaintext prefix %q", tok.Prefix, prefix)
		}

		if !strings.HasPrefix(tok.Hash, "$argon2id$") {
			t.Errorf("hash not argon2id")
		}
		if err := VerifyPassword(tok.Plaintext, tok.Hash); err != nil {
			t.Errorf("minted hash does not verify: %v", err)
		}

		// ParseToken should split identically.
		gotPrefix, gotFull, err := ParseToken(tok.Plaintext)
		if err != nil {
			t.Errorf("ParseToken on a freshly minted plaintext failed: %v", err)
		}
		if gotPrefix != prefix || gotFull != tok.Plaintext {
			t.Errorf("ParseToken roundtrip: prefix=%q full=%q", gotPrefix, gotFull)
		}
	}
}

func TestParseToken(t *testing.T) {
	t.Parallel()

	// Valid.
	p := "argos_pat_abc12345_restofrandom"
	prefix, full, err := ParseToken(p)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if prefix != "abc12345" {
		t.Errorf("prefix=%q, want abc12345", prefix)
	}
	if full != p {
		t.Errorf("full=%q, want %q", full, p)
	}

	// Bad.
	for _, bad := range []string{
		"",
		"not_the_scheme",
		"argos_pat_",           // no prefix
		"argos_pat_abc_suffix", // prefix too short
		"argos_pat_abcdefghij_toolong",
	} {
		if _, _, err := ParseToken(bad); err == nil {
			t.Errorf("%q should fail to parse", bad)
		}
	}
}

func TestScopesForRole(t *testing.T) {
	t.Parallel()
	cases := []struct {
		role string
		has  []string
		miss []string
	}{
		{RoleAdmin, []string{ScopeRead, ScopeWrite, ScopeDelete, ScopeAdmin, ScopeAudit}, nil},
		{RoleEditor, []string{ScopeRead, ScopeWrite}, []string{ScopeDelete, ScopeAdmin, ScopeAudit}},
		{RoleAuditor, []string{ScopeRead, ScopeAudit}, []string{ScopeWrite, ScopeDelete, ScopeAdmin}},
		{RoleViewer, []string{ScopeRead}, []string{ScopeWrite, ScopeDelete, ScopeAdmin, ScopeAudit}},
		{"bogus", nil, []string{ScopeRead, ScopeWrite}},
	}
	for _, c := range cases {
		got := ScopesForRole(c.role)
		caller := Caller{Scopes: got}
		for _, s := range c.has {
			if !caller.HasScope(s) {
				t.Errorf("role=%s missing expected scope %q; got %v", c.role, s, got)
			}
		}
		for _, s := range c.miss {
			// admin implies everything, so don't fail for viewer→admin.
			if caller.HasScope(s) && c.role != RoleAdmin {
				t.Errorf("role=%s unexpectedly has scope %q; got %v", c.role, s, got)
			}
		}
	}
}
