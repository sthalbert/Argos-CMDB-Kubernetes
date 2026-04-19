package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Errorf("hash missing argon2id prefix: %q", h)
	}

	if err := VerifyPassword("correct horse battery staple", h); err != nil {
		t.Errorf("correct password should verify: %v", err)
	}
	if err := VerifyPassword("wrong password", h); err == nil {
		t.Errorf("wrong password should fail")
	} else if err != ErrPasswordMismatch {
		t.Errorf("expected ErrPasswordMismatch, got %v", err)
	}
}

func TestHashPasswordSaltsAreUnique(t *testing.T) {
	t.Parallel()
	// Same plaintext twice → different hashes (random salt). Both must
	// verify against the same password.
	p := "some-password-value"
	a, err := HashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two hashes of the same password are identical — salt broken")
	}
	if err := VerifyPassword(p, a); err != nil {
		t.Errorf("first verify: %v", err)
	}
	if err := VerifyPassword(p, b); err != nil {
		t.Errorf("second verify: %v", err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"",
		"plaintext",
		"$argon2id$",
		"$argon2id$v=19$m=garbage$salt$hash",
		"$bcrypt$...$...",
	} {
		if err := VerifyPassword("whatever", bad); err == nil {
			t.Errorf("%q should not verify", bad)
		}
	}
}

func TestRandomSecretNonEmpty(t *testing.T) {
	t.Parallel()
	s, err := RandomSecret(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(s) == 0 {
		t.Fatal("empty secret")
	}
	// Base64-url unpadded: 16 raw bytes → 22 chars.
	if len(s) != 22 {
		t.Errorf("len=%d, want 22 for n=16", len(s))
	}
}
