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
	UpsertImageVersion(ctx context.Context, in api.ImageVersionUpsert) (api.ImageVersionRow, error)
	DeleteImageVersionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
	FindMirrorForRef(ctx context.Context, hostname, imagePath string) (api.ImageRegistry, error)
	GetMirrorAuthToken(ctx context.Context, hostname, pathPrefix string) (string, error)
	UpsertImageOriginResolution(ctx context.Context, in api.ImageOriginResolutionUpsert) (api.ImageOriginResolution, error)
	DeleteImageOriginResolutionsNotIn(ctx context.Context, keep [][2]string) (int64, error)
	FindImageOrigin(ctx context.Context, imageName string) (string, error)
}

// TagsLister abstracts the OCI client for testing.
type TagsLister interface {
	ListTags(ctx context.Context, registryURL, repoPath string) ([]string, error)
}
