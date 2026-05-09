package registry

import "testing"

func TestEffectiveHost(t *testing.T) {
	cases := map[string]struct {
		wantURL  string
		wantRepo string
	}{
		"docker.io/library/nginx":           {"https://registry-1.docker.io", "library/nginx"},
		"ghcr.io/foo/bar":                   {"https://ghcr.io", "foo/bar"},
		"quay.io/prometheus/prometheus":     {"https://quay.io", "prometheus/prometheus"},
		"gcr.io/google-containers/etcd":     {"https://gcr.io", "google-containers/etcd"},
		"registry.k8s.io/kube-apiserver":    {"https://registry.k8s.io", "kube-apiserver"},
		"public.ecr.aws/amazonlinux/foo":    {"https://public.ecr.aws", "amazonlinux/foo"},
		"europe-west1-docker.pkg.dev/p/r/i": {"https://europe-west1-docker.pkg.dev", "p/r/i"},
	}
	for repo, exp := range cases {
		t.Run(repo, func(t *testing.T) {
			url, path, err := EffectiveHost(repo)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if url != exp.wantURL || path != exp.wantRepo {
				t.Errorf("got (%q, %q), want (%q, %q)", url, path, exp.wantURL, exp.wantRepo)
			}
		})
	}
}

func TestEffectiveHost_Invalid(t *testing.T) {
	if _, _, err := EffectiveHost("nopath"); err == nil {
		t.Fatalf("expected error for image_repo without slash")
	}
}
