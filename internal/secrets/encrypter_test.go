package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func mustKey(tb testing.TB) []byte {
	tb.Helper()
	key := make([]byte, MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		tb.Fatalf("rand: %v", err)
	}
	return key
}

func TestNewEncrypter_RejectsWrongKeySize(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		_, err := NewEncrypter(make([]byte, n))
		if !errors.Is(err, ErrMasterKeySize) {
			t.Errorf("size=%d: want ErrMasterKeySize, got %v", n, err)
		}
	}
}

func TestEncrypter_RoundTrip(t *testing.T) {
	t.Parallel()
	enc, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}

	plaintext := []byte("super-secret-outscale-SK")
	aad := []byte("cloud_account_id=abcd-1234")

	ct, err := enc.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct.KID != CurrentKID {
		t.Errorf("KID = %q, want %q", ct.KID, CurrentKID)
	}
	if len(ct.Nonce) == 0 {
		t.Error("nonce empty")
	}
	if bytes.Contains(ct.Bytes, plaintext) {
		t.Error("ciphertext contains plaintext bytes")
	}

	got, err := enc.Decrypt(ct, aad)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("got %q, want %q", got, plaintext)
	}
}

func TestEncrypter_AADBinding(t *testing.T) {
	t.Parallel()
	enc, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}

	ct, err := enc.Encrypt([]byte("payload"), []byte("aad-A"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Decrypt with a different AAD must fail — this is the core
	// guarantee that a backup-restore cannot move ciphertext between
	// rows.
	_, err = enc.Decrypt(ct, []byte("aad-B"))
	if err == nil {
		t.Fatal("Decrypt with wrong AAD succeeded")
	}
}

func TestEncrypter_NonceUniqueness(t *testing.T) {
	t.Parallel()
	enc, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	const trials = 100
	seen := make(map[string]struct{}, trials)
	for i := range trials {
		ct, err := enc.Encrypt([]byte("payload"), []byte("aad"))
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		key := string(ct.Nonce)
		if _, dup := seen[key]; dup {
			t.Fatalf("nonce reused at trial %d", i)
		}
		seen[key] = struct{}{}
	}
}

func TestEncrypter_RejectsUnknownKID(t *testing.T) {
	t.Parallel()
	enc, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	ct, err := enc.Encrypt([]byte("x"), []byte("aad"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	ct.KID = "v999"
	_, err = enc.Decrypt(ct, []byte("aad"))
	if !errors.Is(err, ErrUnknownKID) {
		t.Fatalf("got %v, want ErrUnknownKID", err)
	}
}

func TestFingerprint_Deterministic(t *testing.T) {
	t.Parallel()
	key := mustKey(t)
	enc1, err := NewEncrypter(key)
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	enc2, err := NewEncrypter(append([]byte(nil), key...))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	if enc1.Fingerprint() != enc2.Fingerprint() {
		t.Errorf("fingerprints diverge for same key: %s vs %s",
			enc1.Fingerprint(), enc2.Fingerprint())
	}
	if len(enc1.Fingerprint()) != 8 {
		t.Errorf("fingerprint len = %d, want 8", len(enc1.Fingerprint()))
	}
	if !isHex(enc1.Fingerprint()) {
		t.Errorf("fingerprint not hex: %s", enc1.Fingerprint())
	}
}

func TestFingerprint_VariesAcrossKeys(t *testing.T) {
	t.Parallel()
	enc1, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	enc2, err := NewEncrypter(mustKey(t))
	if err != nil {
		t.Fatalf("NewEncrypter: %v", err)
	}
	if enc1.Fingerprint() == enc2.Fingerprint() {
		t.Errorf("two random keys produced the same fingerprint: %s",
			enc1.Fingerprint())
	}
}

func TestNewEncrypterFromEnv_Missing(t *testing.T) {
	t.Setenv(MasterKeyEnvVar, "")
	_, err := NewEncrypterFromEnv()
	if !errors.Is(err, ErrMasterKeyMissing) {
		t.Fatalf("got %v, want ErrMasterKeyMissing", err)
	}
}

func TestNewEncrypterFromEnv_BadBase64(t *testing.T) {
	t.Setenv(MasterKeyEnvVar, "!!!not-base64!!!")
	_, err := NewEncrypterFromEnv()
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("error doesn't mention base64: %v", err)
	}
}

func TestNewEncrypterFromEnv_OK(t *testing.T) {
	key := mustKey(t)
	t.Setenv(MasterKeyEnvVar, base64.StdEncoding.EncodeToString(key))
	enc, err := NewEncrypterFromEnv()
	if err != nil {
		t.Fatalf("NewEncrypterFromEnv: %v", err)
	}
	if len(enc.Fingerprint()) != 8 {
		t.Errorf("fingerprint length wrong: %s", enc.Fingerprint())
	}
}

func isHex(s string) bool {
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
