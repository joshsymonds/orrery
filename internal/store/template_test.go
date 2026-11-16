package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pressly/goose/v3"
)

// storeImages are the store files the tests start from, each made once per test binary on first use. Migrating a
// fresh store costs about 0.4 s under the race detector, and copying a migrated one costs milliseconds.
//
// migrated is a store file that Open created and fully migrated, for tests that only need an open store. versions are
// the store file at each migration version, from one walk of the migrations from the first, for tests that store rows
// under an older schema and then migrate them: a version a test names costs its copy, not the walk up to it.
//
//nolint:gochecknoglobals // one set of images per test binary, made on first use and read by every test
var storeImages = struct {
	migrated func() ([]byte, error)
	versions func() (map[int64][]byte, error)
}{migrated: sync.OnceValues(migrateImage), versions: sync.OnceValues(versionImages)}

// migrateImage returns the bytes of a store file that Open created and fully migrated.
func migrateImage() ([]byte, error) {
	dir, err := os.MkdirTemp("", "orrery-store-image-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "orrery.db")
	s, err := Open(context.Background(), path, noSync())
	if err != nil {
		return nil, err
	}
	if closeErr := s.Close(); closeErr != nil {
		return nil, closeErr
	}
	// Closing the last connection checkpoints the WAL into the file and removes it, so the file alone is the store.
	if _, err = os.Stat(path + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("store image: the WAL outlived Close")
	}
	return os.ReadFile(path)
}

// versionImages returns, by migration version, the bytes of a store file the embedded migrations have taken to that
// version and no further, as goose leaves a file it migrates one version at a time. The file keeps the default rollback
// journal, so each version's commit is in the file itself when its bytes are read.
func versionImages() (map[int64][]byte, error) {
	dir, err := os.MkdirTemp("", "orrery-store-versions-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "orrery.db")
	db, err := sql.Open("sqlite", path+"?_synchronous=OFF")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsFS)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	images := map[int64][]byte{}
	for {
		result, upErr := provider.UpByOne(ctx)
		if errors.Is(upErr, goose.ErrNoNextVersion) {
			return images, nil
		}
		if upErr != nil {
			return nil, upErr
		}
		image, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		images[result.Source.Version] = image
	}
}

// noSync is the Open option this package's test stores open with: synchronous OFF, so their commits and checkpoints
// skip the fsyncs a throwaway file does not need.
func noSync() Option {
	return WithSynchronous(SynchronousOff)
}

// seedMigrated writes a copy of the migrated store image at path, mode 0600, in a directory it creates 0700, unless a
// file is already there. Open on the path then finds every migration applied and runs none.
func seedMigrated(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
	image, err := storeImages.migrated()
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
