package imageversions

import (
	"context"

	"github.com/sthalbert/longue-vue/internal/api"
)

// Store is the subset of api.Store used by the enricher. Defining a narrow
// interface here keeps tests trivial and the dependency direction clean.
type Store interface {
	GetSettings(ctx context.Context) (api.Settings, error)
	ListImageRegistries(ctx context.Context) ([]api.ImageRegistry, error)
	DistinctImageRefs(ctx context.Context) ([]string, error)
	UpsertImageVersion(ctx context.Context, in api.ImageVersionUpsert) (api.ImageVersion, error)
	DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
}

// TagsLister abstracts the OCI client for testing.
type TagsLister interface {
	ListTags(ctx context.Context, registryURL, repoPath string) ([]string, error)
}
