package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// migratedImage is the bytes of a store file that Open created and fully migrated, made once per test binary.
// Migrating a fresh store costs about 0.4 s under the race detector, and reopening a migrated one costs
// milliseconds, so tests that only need an open store start from a copy of this image.
//
//nolint:gochecknoglobals // one image per test binary, made on first use and read by every test
var migratedImage = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "orrery-store-image-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "orrery.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	if err = s.Close(); err != nil {
		return nil, err
	}
	// Closing the last connection checkpoints the WAL into the file and removes it, so the file alone is the store.
	if _, err = os.Stat(path + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("store image: the WAL outlived Close")
	}
	return os.ReadFile(path)
})

// seedMigrated writes a copy of the migrated store image at path, mode 0600, in a directory it creates 0700, unless a
// file is already there. Open on the path then finds every migration applied and runs none.
func seedMigrated(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
	image, err := migratedImage()
	if err != nil {
		t.Fatalf("make migrated store image: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err = os.WriteFile(path, image, 0o600); err != nil {
		t.Fatalf("write migrated store: %v", err)
	}
}
