package store

import (
	"context"
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/secrets"
)

func secretsNewEncrypter(key []byte) (*secrets.Encrypter, error) {
	return secrets.NewEncrypter(key)
}

func TestImageRegistries_SeedDefaults(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	list, err := pg.ListImageRegistries(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 7 {
		t.Fatalf("expected 7 default rows, got %d", len(list))
	}

	seen := map[string]bool{}
	for _, r := range list {
		seen[r.Hostname] = true
		if !r.Enabled {
			t.Errorf("default %q should be enabled", r.Hostname)
		}
	}
	for _, want := range []string{
		"docker.io", "ghcr.io", "quay.io", "gcr.io",
		"*-docker.pkg.dev", "registry.k8s.io", "public.ecr.aws",
	} {
		if !seen[want] {
			t.Errorf("missing default registry %q", want)
		}
	}
}

//nolint:gocyclo // exercises the full CRUD lifecycle in one test for clarity
func TestImageRegistries_CreateGetUpdateDelete(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Cleanup in case assertions fail before the explicit delete below.
	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(),
			"DELETE FROM image_versions_registries WHERE hostname = 'mirror.example.com'")
	})

	notes := "internal mirror"
	created, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname:        "mirror.example.com",
		RateLimitPerSec: 2.5,
		Notes:           &notes,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.RateLimitPerSec != 2.5 || !created.Enabled {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := pg.GetImageRegistry(ctx, "mirror.example.com", "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Notes == nil || *got.Notes != "internal mirror" {
		t.Fatalf("notes mismatch: %+v", got.Notes)
	}

	off := false
	upd, err := pg.UpdateImageRegistry(ctx, "mirror.example.com", "",
		api.ImageRegistryPatch{Enabled: &off})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Enabled {
		t.Fatalf("expected disabled after patch")
	}

	if err := pg.DeleteImageRegistry(ctx, "mirror.example.com", ""); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := pg.GetImageRegistry(ctx, "mirror.example.com", ""); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestPG_ImageRegistries_FindMirrorForRef(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(),
			"DELETE FROM image_versions_registries WHERE hostname = 'ma-registry.io'")
	})

	_, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname: "ma-registry.io", PathPrefix: "container/", RateLimitPerSec: 2.0,
		IsMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname: "ma-registry.io", PathPrefix: "container/special/", RateLimitPerSec: 2.0,
		IsMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, hostname, imagePath, wantPrefix string
		wantErr                               error
	}{
		{"longest-prefix wins", "ma-registry.io", "container/special/x/nginx", "container/special/", nil},
		{"parent prefix", "ma-registry.io", "container/library/nginx", "container/", nil},
		{"no match (host)", "other.io", "container/library/nginx", "", api.ErrNotFound},
		{"no match (prefix)", "ma-registry.io", "external/library/nginx", "", api.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pg.FindMirrorForRef(ctx, tc.hostname, tc.imagePath)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want=%v", err, tc.wantErr)
			}
			if err == nil && got.PathPrefix != tc.wantPrefix {
				t.Fatalf("prefix=%q want=%q", got.PathPrefix, tc.wantPrefix)
			}
		})
	}
}

func TestPG_ImageRegistries_TokenRoundTrip(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()

	// Wire an encrypter for this test.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := secretsNewEncrypter(key)
	if err != nil {
		t.Fatal(err)
	}
	pg.SetEncrypter(enc)

	t.Cleanup(func() {
		_, _ = pg.pool.Exec(context.Background(),
			"DELETE FROM image_versions_registries WHERE hostname = 'token.example.com'")
	})

	user := "robot$lv"
	secret := "s3cr3t-token"
	_, err = pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname: "token.example.com", PathPrefix: "container/", RateLimitPerSec: 2.0,
		IsMirror: true, AuthUsername: &user, AuthToken: &secret,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := pg.GetMirrorAuthToken(ctx, "token.example.com", "container/")
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("token=%q want=%q", got, secret)
	}

	// AAD binding: replaying ciphertext under a different prefix MUST fail.
	var ct []byte
	if err := pg.Pool().QueryRow(ctx,
		`SELECT auth_token_ciphertext FROM image_versions_registries
		 WHERE hostname=$1 AND path_prefix=$2`,
		"token.example.com", "container/").Scan(&ct); err != nil {
		t.Fatal(err)
	}
	packed, err := unpackCiphertext(ct, pg.Encrypter().NonceSize())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.Encrypter().Decrypt(packed, aadForImageRegistry("token.example.com", "other/")); err == nil {
		t.Fatal("expected decrypt to fail with mismatched AAD")
	}
}

func TestImageRegistries_CreateConflict(t *testing.T) {
	pg := newTestPG(t)
	ctx := context.Background()
	_, err := pg.CreateImageRegistry(ctx, api.ImageRegistryUpsert{
		Hostname:        "docker.io",
		RateLimitPerSec: 1.0,
	})
	if !errors.Is(err, api.ErrConflict) {
		t.Fatalf("expected ErrConflict for existing hostname, got %v", err)
	}
}
