// Package storetest gives tests a store file that the embedded migrations have already been applied to. Migrating a
// fresh store costs about 0.4 s under the race detector, and reopening a migrated one costs milliseconds, so a test
// that only needs an open store starts from a copy of a migrated image. It is imported only by tests.
package storetest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// images are the store images a test binary starts its stores from, each made once on first use. migrated is the
// bytes of a store file that store.Open created and fully migrated. filled holds, by key, the migrated store after a
// caller's fill has written to it (see SeedFilled).
//
//nolint:gochecknoglobals // one set of images per test binary, made on first use and read by every test
var images = struct {
	migrated func() ([]byte, error)
	mu       sync.Mutex
	filled   map[string]func() ([]byte, error)
}{migrated: sync.OnceValues(makeMigrated), filled: map[string]func() ([]byte, error){}}

// makeMigrated returns the bytes of a store file that store.Open created and fully migrated, made in a directory of
// its own that it removes.
func makeMigrated() ([]byte, error) {
	dir, err := os.MkdirTemp("", "orrery-store-image-")
	if err != nil {
		return nil, fmt.Errorf("make image directory: %w", err)
	}
	raw, err := migrated(filepath.Join(dir, "orrery.db"))
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		err = errors.Join(err, fmt.Errorf("remove image directory: %w", rmErr))
	}
	return raw, err
}

// makeFilled returns the bytes of the migrated store after fill has written to it, made in a directory of its own
// that it removes.
func makeFilled(fill func(context.Context, *store.Store) error) ([]byte, error) {
	raw, err := images.migrated()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "orrery-store-filled-")
	if err != nil {
		return nil, fmt.Errorf("make image directory: %w", err)
	}
	path := filepath.Join(dir, "orrery.db")
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		err = fmt.Errorf("write migrated store: %w", err)
	} else {
		raw, err = filled(path, fill)
	}
	if rmErr := os.RemoveAll(dir); rmErr != nil {
		err = errors.Join(err, fmt.Errorf("remove image directory: %w", rmErr))
	}
	return raw, err
}

// filled opens the migrated store at path, runs fill on it, closes it and returns the file's bytes.
func filled(path string, fill func(context.Context, *store.Store) error) ([]byte, error) {
	ctx := context.Background()
	st, err := store.Open(ctx, path, NoSync())
	if err != nil {
		return nil, fmt.Errorf("open image store: %w", err)
	}
	if err = fill(ctx, st); err != nil {
		return nil, errors.Join(fmt.Errorf("fill image store: %w", err), st.Close())
	}
	return closedImage(st, path)
}

// Prepare makes the migrated store image now, so the first Seed copies it in place of making it. A TestMain calls it
// beside its other setup, so the tests that start at once do not all wait for the image's migrations. It returns the
// error of making the image, which Seed would otherwise report.
func Prepare() error {
	_, err := images.migrated()
	return err
}

// migrated opens a new store at path, which store.Open migrates, closes it and returns the file's bytes.
func migrated(path string) ([]byte, error) {
	s, err := store.Open(context.Background(), path, NoSync())
	if err != nil {
		return nil, fmt.Errorf("open image store: %w", err)
	}
	return closedImage(s, path)
}

// closedImage closes s, the store at path, and returns the file's bytes.
func closedImage(s *store.Store, path string) ([]byte, error) {
	if err := s.Close(); err != nil {
		return nil, fmt.Errorf("close image store: %w", err)
	}
	// Closing the last connection checkpoints the WAL into the file and removes it, so the file alone is the store.
	if _, err := os.Stat(path + "-wal"); !errors.Is(err, fs.ErrNotExist) {
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
	raw, err := images.migrated()
	if err != nil {
		t.Fatalf("make migrated store image: %v", err)
	}
	writeImage(t, path, raw)
}

// SeedFilled writes at path, as Seed does, a copy of the migrated store after fill has written to it, unless a file is
// already there. The filled store is made once per test binary for each key, the first time a test names the key, so
// fill must write the same rows whenever it is called with that key: only the first call's are kept. A test whose
// setup writes many rows before its subject seeds them this way, and pays for the writes once.
func SeedFilled(t *testing.T, path, key string, fill func(context.Context, *store.Store) error) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat %s: %v", path, err)
	}
	images.mu.Lock()
	build, ok := images.filled[key]
	if !ok {
		build = sync.OnceValues(func() ([]byte, error) { return makeFilled(fill) })
		images.filled[key] = build
	}
	images.mu.Unlock()
	raw, err := build()
	if err != nil {
		t.Fatalf("make filled store image %q: %v", key, err)
	}
	writeImage(t, path, raw)
}

// BusyError returns the error a store write gets while another connection holds the store's write lock, as Litestream
// holds it during a checkpoint, so store.Busy reports it. A test's fake returns it to refuse a write as a busy store
// does. The write is made on a store of its own with a busy timeout of 0, which refuses it at once, so the test waits
// out no timeout.
func BusyError(t *testing.T) error {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	Seed(t, path)
	st, err := store.Open(t.Context(), path, NoSync(), store.WithBusyTimeout(0))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open lock connection: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close lock database: %v", closeErr)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatalf("lock connection: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close lock connection: %v", closeErr)
		}
	})
	if _, err = conn.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("take write lock: %v", err)
	}
	busy := st.AddHold(t.Context(), store.HoldRow{Member: "busy-probe", Since: time.Unix(1, 0)})
	if _, err = conn.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatalf("release write lock: %v", err)
	}
	if !store.Busy(busy) {
		t.Fatalf("write under the held lock = %v, want a busy refusal", busy)
	}
	return fmt.Errorf("add hold under the held lock: %w", busy)
}

// writeImage writes raw, a store image, at path, mode 0600, in a directory it creates 0700.
func writeImage(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make store directory: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write store image: %v", err)
	}
}
