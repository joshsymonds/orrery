package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The caps on one recorded send: its text is cut to maxPaneSendText bytes, the cap conversation puts on a prompt item,
// and its client to maxPaneSendClient bytes.
const (
	maxPaneSendText   = 4096
	maxPaneSendClient = 64
)

// ErrEmptyPaneSend reports a send that names an empty pane, text or client. A refused send writes nothing.
var ErrEmptyPaneSend = errors.New("empty pane send")

// PaneSend is one text sent to a pane: the text, the client that sent it, and the time it was sent in unix seconds.
type PaneSend struct {
	Text, Client string
	At           int64
}

// RecordPaneSend stores one text that client sent to pane at at. The text is cut to 4096 bytes and the client to 64
// bytes, each at a UTF-8 rune start. An empty pane, text or client is ErrEmptyPaneSend, and a refused send writes
// nothing. Nothing prunes a send.
func (s *Store) RecordPaneSend(ctx context.Context, pane, text, client string, at time.Time) error {
	if pane == "" || text == "" || client == "" {
		return fmt.Errorf("send to pane %q from client %q: %w", pane, client, ErrEmptyPaneSend)
	}
	text = capText(text, maxPaneSendText)
	client = capText(client, maxPaneSendClient)
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.InsertPaneSend(ctx, sqlcgen.InsertPaneSendParams{
			Pane: pane, Text: text, Client: client, At: at.Unix(),
		}); err != nil {
			return fmt.Errorf("record send to pane %s: %w", pane, err)
		}
		return nil
	})
}

// PaneSends returns the sends to pane whose time is in [from, to], ordered by time and then by the order they were
// recorded in. It does the read in one read transaction on a connection it opens and closes. It returns an error that
// matches ErrUnavailable when the file is missing or the database is busy. It never creates the file and never writes
// the database.
func (r *Reader) PaneSends(ctx context.Context, pane string, from, to int64) ([]PaneSend, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	sends, err := readPaneSends(ctx, db, pane, from, to)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return sends, nil
}

// readPaneSends runs the listing of the sends to pane in one read transaction.
func readPaneSends(ctx context.Context, db *sql.DB, pane string, from, to int64) ([]PaneSend, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListPaneSends(ctx, sqlcgen.ListPaneSendsParams{Pane: pane, FromAt: from, ToAt: to})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list pane sends: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	sends := make([]PaneSend, 0, len(rows))
	for _, row := range rows {
		sends = append(sends, PaneSend{Text: row.Text, Client: row.Client, At: row.At})
	}
	return sends, nil
}
