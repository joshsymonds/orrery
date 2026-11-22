// Package ledger owns orrery's LLM ledger, the file beside orreryd's store that keeps one record for each LLM request:
// its account, seat and models, its tokens and its cost, with the usage limit claims the request reported. Ledger
// writes the records, and Reader reads them without writing, for the Usage API.
package ledger

import (
	"context"
	"embed"
	"errors"
	"path/filepath"
)

// MigrationFS holds the ledger's embedded goose migrations, from migrations/.
//
//go:embed migrations/*.sql
var MigrationFS embed.FS

// ErrTooNew reports a ledger whose recorded migration version is above the newest migration embedded in this binary.
// Open returns it without migrating the ledger.
var ErrTooNew = errors.New("ledger schema is newer than this binary")

// errNotImplemented is returned by each ledger operation whose implementation is not written yet.
var errNotImplemented = errors.New("ledger: not implemented")

// Path returns the ledger file's path: ledger.db in the directory of storePath, beside orreryd's store.
func Path(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "ledger.db")
}

// Ledger is an open ledger file, which Start and Finish write.
type Ledger struct{}

// Open opens the ledger file at path, creating it and applying the embedded migrations when it is absent. It returns
// ErrTooNew, without migrating, when the ledger records a newer migration than this binary embeds.
func Open(_ context.Context, _ string) (*Ledger, error) {
	return nil, errNotImplemented
}

// Close closes the ledger file.
func (l *Ledger) Close() error {
	return errNotImplemented
}
