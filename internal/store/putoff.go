package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of a put-off mark: a headless session's question, named by its request_id, or a pane's question, named by
// its PaneQuestionKey.
const (
	PutOffHeadless = "headless"
	PutOffPane     = "pane"
)

// ErrBadPutOffKind reports a put-off call that names a kind other than headless or pane.
var ErrBadPutOffKind = errors.New("bad put-off kind")

// ErrEmptyPutOffID reports a put-off call that names an empty session or an empty question.
var ErrEmptyPutOffID = errors.New("empty put-off id")

// PaneQuestionKey is the question key of a pane's question: its hash and the changed time it was asked at, joined by
// "@".
func PaneQuestionKey(hash string, changed int64) string {
	return hash + "@" + strconv.FormatInt(changed, 10)
}

// PutOffMark is the owner's put-off mark on one question: the kind, the session the question belongs to, the question
// and the time it was put off.
type PutOffMark struct {
	Kind, Session, Question string
	At                      time.Time
}

// PutOffQuestion marks the question of kind in session as put off at at, replacing the time an earlier PutOffQuestion
// stored for it. A kind other than headless or pane is ErrBadPutOffKind, and an empty session or question is
// ErrEmptyPutOffID; a refused call writes nothing. It does not judge whether the question is open: a mark on a closed
// question is inert.
func (s *Store) PutOffQuestion(ctx context.Context, kind, session, question string, at time.Time) error {
	if err := checkPutOffKey(kind, session, question); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertPutOffQuestion(ctx, sqlcgen.UpsertPutOffQuestionParams{
			Kind: kind, SessionID: session, Question: question, PutOffAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("put off %s question of %s: %w", kind, session, err)
		}
		return nil
	})
}

// BringBackQuestion deletes the put-off mark on the question of kind in session. A mark that is absent is not an
// error, so a second BringBackQuestion of the same question returns nil. The refusals are those of PutOffQuestion.
func (s *Store) BringBackQuestion(ctx context.Context, kind, session, question string) error {
	if err := checkPutOffKey(kind, session, question); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePutOffQuestion(ctx, sqlcgen.DeletePutOffQuestionParams{
			Kind: kind, SessionID: session, Question: question,
		}); err != nil {
			return fmt.Errorf("bring back %s question of %s: %w", kind, session, err)
		}
		return nil
	})
}

// checkPutOffKey is ErrBadPutOffKind when kind is neither headless nor pane, and ErrEmptyPutOffID when session or
// question is empty.
func checkPutOffKey(kind, session, question string) error {
	switch kind {
	case PutOffHeadless, PutOffPane:
	default:
		return fmt.Errorf("put-off kind %q: %w", kind, ErrBadPutOffKind)
	}
	if session == "" || question == "" {
		return fmt.Errorf("%s put-off of session %q, question %q: %w", kind, session, question, ErrEmptyPutOffID)
	}
	return nil
}

// PutOffQuestions returns every put-off mark, ordered by kind, session and question. It does the read in one read
// transaction on a connection it opens and closes. It returns an error that matches ErrUnavailable when the file is
// missing or the database is busy. It never creates the file and never writes the database.
func (r *Reader) PutOffQuestions(ctx context.Context) ([]PutOffMark, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return nil, err
	}
	marks, err := readPutOffQuestions(ctx, db)
	if closeErr := closeDB(); closeErr != nil {
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

// readPutOffQuestions runs the listing of the marks in one read transaction.
func readPutOffQuestions(ctx context.Context, db *sql.DB) ([]PutOffMark, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListPutOffQuestions(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list put-off questions: %w", err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	marks := make([]PutOffMark, 0, len(rows))
	for _, row := range rows {
		marks = append(marks, PutOffMark{
			Kind: row.Kind, Session: row.SessionID, Question: row.Question, At: time.Unix(row.PutOffAt, 0),
		})
	}
	return marks, nil
}
