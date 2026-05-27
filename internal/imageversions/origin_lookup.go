package imageversions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
)

// storeOriginLookup adapts a Store into a mirrorresolve.OriginLookup.
// Parallel to storeLookup in mirror_lookup.go (ADR-0030).
type storeOriginLookup struct{ s Store }

// NewStoreOriginLookup returns an OriginLookup backed by the given Store.
func NewStoreOriginLookup(s Store) mirrorresolve.OriginLookup {
	return storeOriginLookup{s: s}
}

// FindOrigin implements mirrorresolve.OriginLookup. Returns ok=false on
// api.ErrNotFound; bubbles up any other error.
func (l storeOriginLookup) FindOrigin(ctx context.Context, imageName string) (string, bool, error) {
	reg, err := l.s.FindImageOrigin(ctx, imageName)
	if errors.Is(err, api.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find image origin: %w", err)
	}
	return reg, true, nil
}
