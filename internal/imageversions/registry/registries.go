package registry

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidImageRepo is returned by EffectiveHost when the image_repo string
// is missing a registry prefix (no '/' separator).
var ErrInvalidImageRepo = errors.New("registry: invalid image_repo")

// hostTranslate maps a canonical registry hostname (as stored in image_repo
// or in the image_versions_registries table) to the effective HTTPS URL
// the OCI client should call. Most registries are identity; Docker Hub is
// the well-known exception.
var hostTranslate = map[string]string{
	"docker.io": "https://registry-1.docker.io",
}

// EffectiveHost takes a fully-qualified image_repo (e.g. "docker.io/library/nginx")
// and returns (registryURL, repoPath) such that the OCI client can call
// <registryURL>/v2/<repoPath>/tags/list.
func EffectiveHost(imageRepo string) (registryURL, repoPath string, err error) {
	slash := strings.Index(imageRepo, "/")
	if slash < 0 {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidImageRepo, imageRepo)
	}
	host := imageRepo[:slash]
	repo := imageRepo[slash+1:]
	if mapped, ok := hostTranslate[host]; ok {
		return mapped, repo, nil
	}
	return "https://" + host, repo, nil
}
