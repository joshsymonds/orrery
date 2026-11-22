package ledger

import (
	"context"
	"database/sql"
)

// Reader reads the ledger file at its path without writing it. Each read opens the file, reads it and closes it, so a
// Reader holds no connection between reads.
type Reader struct {
	path string
}

// NewReader returns a Reader for the ledger file at path. It opens nothing.
func NewReader(path string) *Reader {
	return &Reader{path: path}
}

// open opens the ledger file for one read, read-only, and never creates it. It returns ok false, with a nil error,
// when the file is absent. The caller closes the database it returns when ok is true.
func (r *Reader) open(_ context.Context) (*sql.DB, bool, error) {
	return nil, false, errNotImplemented
}
