// Package storetest gives tests a store file that the embedded migrations have already been applied to. Migrating a
// fresh store costs about 0.4 s under the race detector, and reopening a migrated one costs milliseconds, so a test
// that only needs an open store starts from a copy of a migrated image. It is imported only by tests.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// image is the bytes of a store file that store.Open created and fully migrated, made once per test binary.
//
//nolint:gochecknoglobals // one image per test binary, made on first use and read by every test
var image = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "orrery-store-image-")
	if err != nil {
		return nil, fmt.Errorf("make image directory: %w", err)
	}
	raw, err := migrated(filepath.Join(dir, "orrery.db"))
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		err = errors.Join(err, fmt.Errorf("remove image directory: %w", rmErr))
	}
	return raw, err
})

// Prepare makes the migrated store image now, so the first Seed copies it in place of making it. A TestMain calls it
// beside its other setup, so the tests that start at once do not all wait for the image's migrations. It returns the
// error of making the image, which Seed would otherwise report.
func Prepare() error {
	_, err := image()
	return err
}

// migrated opens a new store at path, which store.Open migrates, closes it and returns the file's bytes.
func migrated(path string) ([]byte, error) {
	s, err := store.Open(context.Background(), path, NoSync())
	if err != nil {
		return nil, fmt.Errorf("open image store: %w", err)
	}
	if err = s.Close(); err != nil {
		return nil, fmt.Errorf("close image store: %w", err)
	}
	// Closing the last connection checkpoints the WAL into the file and removes it, so the file alone is the store.
	if _, err = os.Stat(path + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("the image store's WAL outlived Close")
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read image store: %w", err)
	}
	return raw, nil
}

// NoSync is the Open option a test store opens with: synchronous OFF, so its commits and checkpoints skip the fsyncs
// a throwaway file does not need. orreryd opens its store without it.
func NoSync() store.Option {
	return store.WithSynchronous(store.SynchronousOff)
}

// Seed writes a copy of the migrated store image at path, mode 0600, in a directory it creates 0700, unless a file is
// already there. store.Open on the path then finds every migration applied and runs none.
func Seed(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
	raw, err := image()
	if err != nil {
		t.Fatalf("make migrated store image: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write migrated store: %v", err)
	}
}
