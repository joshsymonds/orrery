package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EpicCaller names the session that made a call of an epic. A non-empty Session names the sessions row with that id.
// Otherwise the caller is a tmux pane, and Server and Pane name its row.
type EpicCaller struct{ Session, Server, Pane string }

// epicCreateCall is the call that creates an epic. The session that makes it is the epic's creator.
const epicCreateCall = "epic_create"

// RecordEpicCall records call, made at at, as the newest call of epic by the session that made it, in one write. The
// caller resolves to its sessions row, and that session's row for epic takes the new call and time, so a later call by
// the same session replaces the earlier one. A call of epic_create marks that session the epic's creator, and no later
// call clears the mark. It reports false and writes nothing when no row resolves.
func (s *Store) RecordEpicCall(
	ctx context.Context, epic string, caller EpicCaller, call string, at time.Time,
) (bool, error) {
	var recorded bool
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		session, err := resolveEpicCaller(ctx, queries, caller)
		if err != nil {
			return err
		}
		if session == "" {
			return nil
		}
		err = queries.UpsertEpicSession(ctx, sqlcgen.UpsertEpicSessionParams{
			Epic: epic, Session: session, Call: call, At: at.Unix(), Creator: boolInt(call == epicCreateCall),
		})
		if err != nil {
			return fmt.Errorf("record call of epic %s by session %s: %w", epic, session, err)
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}

// resolveEpicCaller returns the id of the sessions row caller names, or "" when none does. A caller with a session id
// names the row with that id, whatever its kind. Any other caller names the pane row with its server and pane.
func resolveEpicCaller(ctx context.Context, queries *sqlcgen.Queries, caller EpicCaller) (string, error) {
	if caller.Session != "" {
		row, err := queries.GetSession(ctx, caller.Session)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("read session %s: %w", caller.Session, err)
		}
		return row.ID, nil
	}
	id, err := queries.GetPaneSession(ctx, sqlcgen.GetPaneSessionParams{Server: caller.Server, Pane: caller.Pane})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read pane %s of server %q: %w", caller.Pane, caller.Server, err)
	}
	return id, nil
}

// CallerSession returns the id of the sessions row caller names, and true. It returns "" and false, and no error,
// when no row matches caller. It reads the store without writing it.
func (s *Store) CallerSession(ctx context.Context, caller EpicCaller) (string, bool, error) {
	session, err := resolveEpicCaller(ctx, sqlcgen.New(s.read), caller)
	if err != nil {
		return "", false, err
	}
	return session, session != "", nil
}

// The makers of an epic call: the owner, which is the user and has no session; a conversation, which is a session
// that is a conversation; and orreryd, which made a step's end.
const (
	MakerOwner        = "owner"
	MakerConversation = "conversation"
	MakerOrreryd      = "orreryd"
)

// Maker names who made an epic call. By is one of the Maker constants. Session is the id of the conversation's
// sessions row when By is MakerConversation, and "" otherwise.
type Maker struct{ By, Session string }

// Phrase names the maker in the text of an epic call: the owner, conversation with its session id, or orreryd. A maker
// with no By is unknown.
func (m Maker) Phrase() string {
	switch m.By {
	case MakerOwner:
		return "the owner"
	case MakerConversation:
		return "conversation " + m.Session
	case MakerOrreryd:
		return "orreryd"
	}
	return "unknown"
}

// CallerMaker returns who made an epic call as caller names it. A zero caller has no session, server or pane, so it is
// the owner and no read is made. Any other caller names a sessions row as resolveEpicCaller does. The caller is the
// owner when no row matches; an orreryd step when its row is of KindStep; and a conversation when its row is of
// KindEngine. A pane row, of kind "", is a conversation when its state is not empty and the owner when it is empty.
// It reads the store without writing it.
func (s *Store) CallerMaker(ctx context.Context, caller EpicCaller) (Maker, error) {
	owner := Maker{By: MakerOwner}
	if caller == (EpicCaller{}) {
		return owner, nil
	}
	queries := sqlcgen.New(s.read)
	id := caller.Session
	if id == "" {
		pane, err := queries.GetPaneSession(ctx, sqlcgen.GetPaneSessionParams{Server: caller.Server, Pane: caller.Pane})
		if errors.Is(err, sql.ErrNoRows) {
			return owner, nil
		}
		if err != nil {
			return Maker{}, fmt.Errorf("read pane %s of server %q: %w", caller.Pane, caller.Server, err)
		}
		id = pane
	}
	row, err := queries.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, nil
	}
	if err != nil {
		return Maker{}, fmt.Errorf("read session %s: %w", id, err)
	}
	switch row.Kind {
	case KindStep:
		return Maker{By: MakerOrreryd}, nil
	case KindEngine:
		return Maker{By: MakerConversation, Session: row.ID}, nil
	}
	if row.State == "" {
		return owner, nil
	}
	return Maker{By: MakerConversation, Session: row.ID}, nil
}

// EpicSteerer returns the sessions row of the session that steers epic, and true. It returns false, and no error, when
// epic has no steerer or when the steerer's sessions row is gone.
func (s *Store) EpicSteerer(ctx context.Context, epic string) (CallerSession, bool, error) {
	row, err := sqlcgen.New(s.read).GetEpicSteerer(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return CallerSession{}, false, nil
	}
	if err != nil {
		return CallerSession{}, false, fmt.Errorf("read steerer of epic %s: %w", epic, err)
	}
	return CallerSession{ID: row.ID, Kind: row.Kind, Server: row.Server, Pane: row.Pane, State: row.State}, true, nil
}

// EpicSessionRow is one session that made a call of an epic: its newest call of the epic, the unix second it was made
// at, whether the session created the epic, and the session's kind, server, pane, label, dir, title and first prompt
// from its sessions row. Title is "" until a title is stored, and FirstPrompt is the text of the session's oldest
// engine_prompt event, or "" when it has none.
type EpicSessionRow struct {
	Session     string
	Kind        string
	Server      string
	Pane        string
	Label       string
	Dir         string
	Title       string
	FirstPrompt string
	Call        string
	At          int64
	Creator     bool
}

// EpicSessions returns the sessions that made a call of epic and that the sessions table still holds, newest call
// first and ties by session id. It does the read in one read transaction on a connection it opens and closes. It
// returns an error that matches ErrUnavailable when the file is missing or the database is busy. It never creates the
// file and never writes the database.
func (r *Reader) EpicSessions(ctx context.Context, epic string) ([]EpicSessionRow, error) {
	db, closeDB, err := r.database()
	if err != nil {
		return nil, err
	}
	rows, err := readEpicSessions(ctx, db, epic)
	if closeErr := closeDB(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, err
	}
	return rows, nil
}

// readEpicSessions runs the listing of epic in one read transaction.
func readEpicSessions(ctx context.Context, db *sql.DB, epic string) ([]EpicSessionRow, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin store read: %w", err)
	}
	rows, err := sqlcgen.New(tx).ListEpicSessions(ctx, epic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list sessions of epic %s: %w", epic, err), tx.Rollback())
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("end store read: %w", err)
	}
	listed := make([]EpicSessionRow, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, EpicSessionRow{
			Session: row.Session, Kind: row.Kind, Server: row.Server, Pane: row.Pane, Label: row.Label,
			Dir: row.Dir, Title: row.Title, FirstPrompt: row.FirstPrompt, Call: row.Call, At: row.At,
			Creator: row.Creator != 0,
		})
	}
	return listed, nil
}
