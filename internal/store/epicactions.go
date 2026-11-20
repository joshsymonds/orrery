package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds of an epic's actions, as an action row names them. A move hands the epic to another steerer; the others are
// the controls the owner or orreryd makes.
const (
	ActionMove    = "move"
	ActionPause   = "pause"
	ActionResume  = "resume"
	ActionStop    = "stop"
	ActionRetry   = "retry"
	ActionRelease = "release"
	ActionClose   = "close"
	ActionAmend   = "amend"
)

// The kinds of the caller events that name an epic action: a move, which the session it leaves and the session it
// reaches each hear of, and any other action, which the session it is owed to hears of.
const (
	CallerKindMoved  = "moved"
	CallerKindAction = "action"
)

// EpicAction is one action of an epic, numbered from 1 in the order it was made. From and To are the steerers before
// and after a move, and "" for any other kind. Recipient is the session the action is owed to, or "" when none is.
type EpicAction struct {
	Seq       int64
	Kind      string
	Maker     Maker
	From, To  string
	Recipient string
	At        time.Time
}

// SteerTarget is the sessions row of a session an epic can be moved to: its id, kind, state, directory, and the server
// and pane of a pane row.
type SteerTarget struct {
	ID, Kind, State, Dir, Server, Pane string
}

// SteerTarget returns the sessions row with id, and true. It returns false, and no error, when no row has the id.
func (s *Store) SteerTarget(ctx context.Context, id string) (SteerTarget, bool, error) {
	row, err := sqlcgen.New(s.db).GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return SteerTarget{}, false, nil
	}
	if err != nil {
		return SteerTarget{}, false, fmt.Errorf("read session %s: %w", id, err)
	}
	return SteerTarget{
		ID: row.ID, Kind: row.Kind, State: row.State, Dir: row.Dir, Server: row.Server, Pane: row.Pane,
	}, true, nil
}

// MoveEpic hands epic to session to in one write: its steerer becomes to, and one move action is appended from the
// steerer it had, made by maker at the time at. An unknown epic is ErrNoEpic. A to that already steers epic is a
// RefusalError, and the write changes nothing.
func (s *Store) MoveEpic(ctx context.Context, epic, to string, maker Maker, at time.Time) (EpicAction, error) {
	var action EpicAction
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Steerer == to {
			return RefusalError(fmt.Sprintf("epic %s is already steered by session %s", epic, to))
		}
		if err = queries.SetEpicSteerer(ctx, sqlcgen.SetEpicSteererParams{Steerer: to, ID: epic}); err != nil {
			return fmt.Errorf("set steerer of epic %s: %w", epic, err)
		}
		action, err = appendEpicAction(ctx, queries, epic, ActionMove, maker, head.Steerer, to, at)
		return err
	})
	return action, err
}

// RecordEpicAction appends one action of kind to epic in one write, made by maker at the time at. Its From and To are
// "" and its recipient is as appendEpicAction sets it. A move is MoveEpic's to make, so kind is not ActionMove. An
// unknown epic is ErrNoEpic.
func (s *Store) RecordEpicAction(ctx context.Context, epic, kind string, maker Maker, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := epicHead(ctx, queries, epic); err != nil {
			return err
		}
		_, err := appendEpicAction(ctx, queries, epic, kind, maker, "", "", at)
		return err
	})
}

// EpicActionEvent is the id of the caller event for action seq of epic.
func EpicActionEvent(epic string, seq int64) string {
	return fmt.Sprintf("epic:%s:A%d", epic, seq)
}

// EpicDecisionEvent is the id of the caller event for decision seq of epic.
func EpicDecisionEvent(epic string, seq int64) string {
	return fmt.Sprintf("epic:%s:D%d", epic, seq)
}

// appendEpicAction appends one action of kind to epic as its next seq, made by maker at the time at, moving the
// steerer from from to to. Its recipient is ownerRecipient, which is "" for a move. The insert runs with withEpicEvent,
// so the committed write wakes the caller-event pass.
func appendEpicAction(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, kind string,
	maker Maker,
	from, to string,
	at time.Time,
) (EpicAction, error) {
	recipient := ""
	if kind != ActionMove {
		owed, err := ownerRecipient(ctx, queries, epic, maker)
		if err != nil {
			return EpicAction{}, err
		}
		recipient = owed
	}
	maxSeq, err := queries.MaxEpicActionSeq(ctx, epic)
	if err != nil {
		return EpicAction{}, fmt.Errorf("read action count of epic %s: %w", epic, err)
	}
	action := EpicAction{
		Seq: maxSeq + 1, Kind: kind, Maker: maker, From: from, To: to, Recipient: recipient,
		At: time.Unix(at.Unix(), 0),
	}
	if err = queries.InsertEpicAction(withEpicEvent(ctx), sqlcgen.InsertEpicActionParams{
		Epic: epic, Seq: action.Seq, Kind: kind, Maker: maker.By, MakerSession: maker.Session,
		FromSession: from, ToSession: to, Recipient: recipient, At: at.Unix(),
	}); err != nil {
		return EpicAction{}, fmt.Errorf("insert action %s of epic %s: %w", kind, epic, err)
	}
	return action, nil
}

// ownerRecipient returns the session an action made by maker on epic is owed to: the epic's steerer when the owner
// made it, and "" otherwise.
func ownerRecipient(ctx context.Context, queries *sqlcgen.Queries, epic string, maker Maker) (string, error) {
	if maker.By != MakerOwner {
		return "", nil
	}
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return "", err
	}
	return head.Steerer, nil
}

// actionOf returns the action a stored row holds.
func actionOf(action sqlcgen.EpicAction) EpicAction {
	return EpicAction{
		Seq:       action.Seq,
		Kind:      action.Kind,
		Maker:     Maker{By: action.Maker, Session: action.MakerSession},
		From:      action.FromSession,
		To:        action.ToSession,
		Recipient: action.Recipient,
		At:        time.Unix(action.At, 0),
	}
}
