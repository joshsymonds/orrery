// Package ledger owns orrery's LLM ledger, the file beside orreryd's store that keeps one record for each LLM request:
// its account, seat and models, its tokens and its cost, with the usage limit claims the request reported. Ledger
// writes the records, and Reader reads them without writing, for the Usage API.
package ledger

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
	"strconv"
	"time"

	"github.com/pressly/goose/v3"

	// The blank import registers the "sqlite3" driver that Open and Reader open the ledger with.
	_ "github.com/mattn/go-sqlite3"
)

// MigrationFS holds the ledger's embedded goose migrations, from migrations/.
//
//go:embed migrations/*.sql
var MigrationFS embed.FS

// ErrTooNew reports a ledger whose recorded migration version is above the newest migration embedded in this binary.
// Open returns it without migrating the ledger.
var ErrTooNew = errors.New("ledger schema is newer than this binary")

// busyTimeout is how long a ledger statement waits for a lock another connection holds before it fails. It is the
// store's BusyTimeout, for the same writer: Litestream holds the write lock for its checkpoints.
const busyTimeout = 5 * time.Second

// Path returns the ledger file's path: ledger.db in the directory of storePath, beside orreryd's store.
func Path(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "ledger.db")
}

// Ledger is an open ledger file, which Start and Finish write.
type Ledger struct {
	db *sql.DB
}

// Open opens the ledger file at path, creating its directory (0700) and the file (0600) when absent. It opens the
// file in WAL mode with a busy timeout, on one connection, and applies the embedded migrations. It returns ErrTooNew,
// without migrating and leaving the file as it was, when the ledger records a newer migration than this binary embeds.
func Open(ctx context.Context, path string) (*Ledger, error) {
	clean := filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(clean), 0o700); err != nil {
		return nil, fmt.Errorf("create ledger directory: %w", err)
	}
	file, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create ledger file: %w", err)
	}
	if err = file.Close(); err != nil {
		return nil, fmt.Errorf("close ledger file: %w", err)
	}

	query := "_busy_timeout=" + strconv.FormatInt(busyTimeout.Milliseconds(), 10)
	dsn := (&url.URL{Scheme: "file", Path: clean, RawQuery: query}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ledger: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err = migrate(ctx, db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Ledger{db: db}, nil
}

// migrate checks the version the ledger records, then switches the file to WAL mode and applies the embedded
// migrations. The check comes first, so a ledger that ErrTooNew refuses keeps its journal mode and its bytes.
func migrate(ctx context.Context, db *sql.DB) error {
	migrationsFS, err := fs.Sub(MigrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}
	// The provider does not own db: Provider.Close would close the ledger, so it is never called.
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, migrationsFS)
	if err != nil {
		return fmt.Errorf("create goose provider: %w", err)
	}

	var newest int64
	for _, src := range provider.ListSources() {
		newest = max(newest, src.Version)
	}
	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("read ledger version: %w", err)
	}
	if current > newest {
		return fmt.Errorf("%w: ledger version %d, newest migration %d", ErrTooNew, current, newest)
	}

	var journal string
	if err = db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&journal); err != nil {
		return fmt.Errorf("set ledger journal mode: %w", err)
	}
	if journal != "wal" {
		return fmt.Errorf("set ledger journal mode: got %q, want wal", journal)
	}
	if _, err = provider.Up(ctx); err != nil {
		return fmt.Errorf("run ledger migrations: %w", err)
	}
	return nil
}

// Close closes the ledger file.
func (l *Ledger) Close() error {
	if err := l.db.Close(); err != nil {
		return fmt.Errorf("close ledger: %w", err)
	}
	return nil
}
