package imageversions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sthalbert/longue-vue/internal/api"
	"github.com/sthalbert/longue-vue/internal/imageversions/mirrorresolve"
)

// storeLookup adapts a Store into a mirrorresolve.MirrorLookup.
type storeLookup struct{ s Store }

// NewStoreMirrorLookup returns a MirrorLookup backed by the given Store.
func NewStoreMirrorLookup(s Store) mirrorresolve.MirrorLookup { return storeLookup{s: s} }

// FindMirror implements mirrorresolve.MirrorLookup. Returns (zero, false, nil)
// when no mirror row applies (api.ErrNotFound from the store).
func (l storeLookup) FindMirror(ctx context.Context, hostname, imagePath string) (mirrorresolve.MirrorRow, bool, error) {
	row, err := l.s.FindMirrorForRef(ctx, hostname, imagePath)
	if errors.Is(err, api.ErrNotFound) {
		return mirrorresolve.MirrorRow{}, false, nil
	}
	if err != nil {
		return mirrorresolve.MirrorRow{}, false, fmt.Errorf("find mirror: %w", err)
	}
	user := ""
	if row.AuthUsername != nil {
		user = *row.AuthUsername
	}
	return mirrorresolve.MirrorRow{
		Hostname:     row.Hostname,
		PathPrefix:   row.PathPrefix,
		AuthUsername: user,
		TokenSource: func(ctx context.Context) (string, error) {
			return l.s.GetMirrorAuthToken(ctx, row.Hostname, row.PathPrefix)
		},
	}, true, nil
}
