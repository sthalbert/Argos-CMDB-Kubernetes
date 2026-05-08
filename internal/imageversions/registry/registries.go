package registry

import (
	"fmt"
	"strings"
)

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
func EffectiveHost(imageRepo string) (string, string, error) {
	slash := strings.Index(imageRepo, "/")
	if slash < 0 {
		return "", "", fmt.Errorf("invalid image_repo (no slash): %q", imageRepo)
	}
	host := imageRepo[:slash]
	repo := imageRepo[slash+1:]
	if mapped, ok := hostTranslate[host]; ok {
		return mapped, repo, nil
	}
	return "https://" + host, repo, nil
}
