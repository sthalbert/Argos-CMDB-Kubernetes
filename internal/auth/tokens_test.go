package auth

import (
	"strings"
	"testing"
)

func TestMintTokenFormat(t *testing.T) {
	t.Parallel()
	tok, err := MintToken()
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	if !strings.HasPrefix(tok.Plaintext, TokenScheme) {
		t.Errorf("plaintext missing scheme: %q", tok.Plaintext)
	}
	// Shape: argos_pat_<8>_<32>
	parts := strings.Split(strings.TrimPrefix(tok.Plaintext, TokenScheme), "_")
	if len(parts) != 2 {
		t.Fatalf("plaintext wrong segment count: %v", parts)
	}
	if len(parts[0]) != 8 {
		t.Errorf("prefix len=%d, want 8", len(parts[0]))
	}
	if tok.Prefix != parts[0] {
		t.Errorf("stored prefix %q differs from plaintext prefix %q", tok.Prefix, parts[0])
	}
	if !strings.HasPrefix(tok.Hash, "$argon2id$") {
		t.Errorf("hash not argon2id")
	}
	// The stored hash must verify against the plaintext.
	if err := VerifyPassword(tok.Plaintext, tok.Hash); err != nil {
		t.Errorf("minted hash does not verify: %v", err)
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
