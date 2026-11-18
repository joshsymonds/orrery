package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrBadRowKind reports an archive call that names a row kind other than epic, session or headless.
var ErrBadRowKind = errors.New("bad row kind")

// ErrEmptyRowID reports an archive call that names a row by an empty id.
var ErrEmptyRowID = errors.New("empty row id")

// ArchiveMark is the owner's archive mark on one list row. ArchivedAt is set while the row is archived, and
// UnarchivedAt is set once the owner has unarchived it; the other time is zero.
type ArchiveMark struct {
	Kind         string
	ID           string
	ArchivedAt   time.Time
	UnarchivedAt time.Time
}

// ArchiveRow marks the row of kind with id archived at at, replacing the mark an earlier ArchiveRow or UnarchiveRow
// stored. A kind other than epic, session or headless is ErrBadRowKind, and an empty id is ErrEmptyRowID. It does not
// require the row to exist in any table, so an epic with only a record can be archived. Archiving a headless row also
// deletes that session's cron jobs, in the same write, so an archived headless session keeps none.
func (s *Store) ArchiveRow(ctx context.Context, kind, id string, at time.Time) error {
	if err := checkRowKey(kind, id); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertArchivedRow(ctx, sqlcgen.UpsertArchivedRowParams{
			Kind: kind, ID: id, ArchivedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("archive %s %s: %w", kind, id, err)
		}
		if kind == "headless" {
			if _, err := queries.DeleteSessionCronJobs(ctx, id); err != nil {
				return fmt.Errorf("delete cron jobs of archived %s: %w", id, err)
			}
		}
		return nil
	})
}

// UnarchiveRow marks the row of kind with id unarchived at at, replacing the mark an earlier ArchiveRow or
// UnarchiveRow stored, and keeps at so the owner's unarchive is remembered. The refusals are those of ArchiveRow.
func (s *Store) UnarchiveRow(ctx context.Context, kind, id string, at time.Time) error {
	if err := checkRowKey(kind, id); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertUnarchivedRow(ctx, sqlcgen.UpsertUnarchivedRowParams{
			Kind: kind, ID: id, UnarchivedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("unarchive %s %s: %w", kind, id, err)
		}
		return nil
	})
}

// checkRowKey is ErrBadRowKind when kind is not a list row kind, and ErrEmptyRowID when id is empty.
func checkRowKey(kind, id string) error {
	switch kind {
	case "epic", "session", "headless":
	default:
		return fmt.Errorf("row kind %q: %w", kind, ErrBadRowKind)
	}
	if id == "" {
		return fmt.Errorf("%s row: %w", kind, ErrEmptyRowID)
	}
	return nil
}

// ArchivedRows returns every archive mark, ordered by kind and then id. It does the read in one read transaction on a
// connection it opens and closes. It returns an error that matches ErrUnavailable when the file is missing or the
// database is busy. It never creates the file and never writes the database.
func (r *Reader) ArchivedRows(ctx context.Context) ([]ArchiveMark, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	marks, err := readArchivedRows(ctx, db)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return marks, nil
}

// readArchivedRows runs the listing of the marks in one read transaction.
func readArchivedRows(ctx context.Context, db *sql.DB) ([]ArchiveMark, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListArchivedRows(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list archived rows: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	marks := make([]ArchiveMark, 0, len(rows))
	for _, row := range rows {
		mark := ArchiveMark{Kind: row.Kind, ID: row.ID}
		if row.ArchivedAt != 0 {
			mark.ArchivedAt = time.Unix(row.ArchivedAt, 0)
		}
		if row.UnarchivedAt != 0 {
			mark.UnarchivedAt = time.Unix(row.UnarchivedAt, 0)
		}
		marks = append(marks, mark)
	}
	return marks, nil
}
