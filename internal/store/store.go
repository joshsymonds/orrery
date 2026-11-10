// Package store owns orreryd's SQLite store: its path, its private file modes and its embedded migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	// The blank import registers the pure-Go "sqlite" driver with database/sql.
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrTooNew reports a database whose recorded migration version is above the newest migration embedded in
// this binary. Open returns it without migrating the database.
var ErrTooNew = errors.New("store schema is newer than this binary")

// Store is an open orrery SQLite store.
type Store struct {
	db *sql.DB
}

// Path resolves the store file: $XDG_STATE_HOME/orrery/orrery.db when XDG_STATE_HOME is set,
// else $HOME/.local/state/orrery/orrery.db. It returns an error when both are unset.
func Path(getenv func(string) string) (string, error) {
	if stateHome := getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "orrery", "orrery.db"), nil
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".local", "state", "orrery", "orrery.db"), nil
	}
	return "", fmt.Errorf("no store path: set XDG_STATE_HOME or HOME")
}

// Open creates the store's directory (0700) and file (0600) when absent, opens the database in WAL mode with a
// 200 ms busy timeout, and applies the embedded migrations. It returns ErrTooNew, without migrating, when the
// database records a newer migration than this binary embeds. A file it cannot use is left untouched.
func Open(ctx context.Context, path string) (*Store, error) {
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	file, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close store file: %w", err)
	}

	dsn := (&url.URL{Scheme: "file", Path: clean, RawQuery: "_busy_timeout=200&_journal_mode=WAL"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = s.migrate(ctx); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return s, nil
}

// Close closes the store.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	migrationsFS, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}
	// The provider does not own s.db: Provider.Close would close the store, so it is never called.
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrationsFS)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	var newest int64
	for _, src := range provider.ListSources() {
		newest = max(newest, src.Version)
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read store version: %w", err)
	}
	if current > newest {
		return fmt.Errorf("%w: store version %d, newest migration %d", ErrTooNew, current, newest)
	}

	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("run store migrations: %w", err)
	}
	return nil
}
