package imageversions

import (
	"context"
	"errors"
	"testing"

	"github.com/sthalbert/longue-vue/internal/api"
)

// fakeStoreForOrigin implements the Store interface subset our adapter
// needs. Only FindImageOrigin is exercised.
type fakeStoreForOrigin struct {
	Store // embed to satisfy unused methods
	rows  map[string]string
	err   error
}

func (f *fakeStoreForOrigin) FindImageOrigin(_ context.Context, name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.rows[name]
	if !ok {
		return "", api.ErrNotFound
	}
	return v, nil
}

func TestStoreOriginLookup_FindOrigin(t *testing.T) {
	s := &fakeStoreForOrigin{rows: map[string]string{"grafana/alloy": "docker.io"}}
	l := NewStoreOriginLookup(s)

	t.Run("hit", func(t *testing.T) {
		reg, ok, err := l.FindOrigin(context.Background(), "grafana/alloy")
		if err != nil || !ok || reg != "docker.io" {
			t.Fatalf("want (docker.io,true,nil), got (%q,%v,%v)", reg, ok, err)
		}
	})
	t.Run("miss → ok=false, err=nil", func(t *testing.T) {
		_, ok, err := l.FindOrigin(context.Background(), "missing/image")
		if ok || err != nil {
			t.Fatalf("want (false,nil), got (ok=%v,err=%v)", ok, err)
		}
	})
	t.Run("store error", func(t *testing.T) {
		s.err = errors.New("boom")
		_, ok, err := l.FindOrigin(context.Background(), "grafana/alloy")
		if ok || err == nil {
			t.Fatalf("want (false, non-nil err), got (ok=%v,err=%v)", ok, err)
		}
	})
}
