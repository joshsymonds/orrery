package store

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ToldMark names the turn, tell or escalation of an epic that a write of a notice row marks told, in the same
// transaction. Seq is the turn's seq, the tell's seq when Tell is set, or the question's seq when Escalation is set.
// Tell and Escalation are not both set.
type ToldMark struct {
	Epic       string
	Seq        int64
	Tell       bool
	Escalation bool
}

// NoticeRow is one pending notice. Exactly one of Pane, Session and Epic names what it is about. Limit names the
// usage limit a notice is about, and is empty for every other notice. Time is the unix second the notice was
// recorded. Due is the unix millisecond its phase timer runs out, 0 for none. Phase and SourceKind are stored as
// given; the store does not interpret them. A non-nil Told is not stored: PutNotice applies it, and the rows Notices
// returns carry none.
type NoticeRow struct {
	ID          int64
	Pane        string
	Session     string
	State       string
	Time        int64
	Phase       string
	Due         int64
	DelayServed bool
	WatchLogged bool
	SourceKind  string
	Source      []byte
	Silent      bool
	Epic        string
	Limit       string
	Told        *ToldMark
}

// PutNotice inserts the row named by row.ID, or replaces every column of the row already stored under that id. It
// raises the id floor to row.ID and marks row.Told, in the same write, so no id ever put is above the floor and a
// notice's row and its told mark land together or not at all.
func (s *Store) PutNotice(ctx context.Context, row NoticeRow) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.UpsertPendingNotice(ctx, sqlcgen.UpsertPendingNoticeParams{
			ID: row.ID, Pane: row.Pane, Session: row.Session, State: row.State, Time: row.Time, Phase: row.Phase,
			Due: row.Due, DelayServed: row.DelayServed, WatchLogged: row.WatchLogged, SourceKind: row.SourceKind,
			Source: row.Source, Silent: row.Silent, Epic: row.Epic, LimitKey: row.Limit,
		}); err != nil {
			return fmt.Errorf("put notice %d: %w", row.ID, err)
		}
		if err := queries.RaiseNoticeIDFloor(ctx, row.ID); err != nil {
			return fmt.Errorf("raise notice id floor to %d: %w", row.ID, err)
		}
		if row.Told != nil {
			return markToldIn(ctx, queries, *row.Told)
		}
		return nil
	})
}

// markToldIn sets the told marker of the turn, tell or escalation told names, inside the write queries belongs to. A
// turn, tell or question that does not exist is an error matching ErrNoTurn, ErrNoTell or ErrNoQuestion, which fails
// that write.
func markToldIn(ctx context.Context, queries *sqlcgen.Queries, told ToldMark) error {
	if told.Escalation {
		rows, err := queries.SetEpicQuestionEscalationTold(ctx, sqlcgen.SetEpicQuestionEscalationToldParams{
			Epic: told.Epic, Seq: told.Seq,
		})
		if err != nil {
			return fmt.Errorf("mark escalation of question %d of epic %s told: %w", told.Seq, told.Epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("question %d of epic %s: %w", told.Seq, told.Epic, ErrNoQuestion)
		}
		return nil
	}
	if told.Tell {
		rows, err := queries.MarkEpicTellTold(ctx, sqlcgen.MarkEpicTellToldParams{Epic: told.Epic, Seq: told.Seq})
		if err != nil {
			return fmt.Errorf("mark tell %d of epic %s told: %w", told.Seq, told.Epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("tell %d of epic %s: %w", told.Seq, told.Epic, ErrNoTell)
		}
		return nil
	}
	rows, err := queries.MarkEpicTurnTold(ctx, sqlcgen.MarkEpicTurnToldParams{Epic: told.Epic, Seq: told.Seq})
	if err != nil {
		return fmt.Errorf("mark turn %d of epic %s told: %w", told.Seq, told.Epic, err)
	}
	if rows == 0 {
		return fmt.Errorf("turn %d of epic %s: %w", told.Seq, told.Epic, ErrNoTurn)
	}
	return nil
}

// DeleteNotice removes the row with id. An id that is not stored is not an error.
func (s *Store) DeleteNotice(ctx context.Context, id int64) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.DeletePendingNotice(ctx, id); err != nil {
			return fmt.Errorf("delete notice %d: %w", id, err)
		}
		return nil
	})
}

// Notices returns every stored notice ordered by id.
func (s *Store) Notices(ctx context.Context) ([]NoticeRow, error) {
	rows, err := sqlcgen.New(s.db).ListPendingNotices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notices: %w", err)
	}
	out := make([]NoticeRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, NoticeRow{
			ID: row.ID, Pane: row.Pane, Session: row.Session, State: row.State, Time: row.Time, Phase: row.Phase,
			Due: row.Due, DelayServed: row.DelayServed, WatchLogged: row.WatchLogged, SourceKind: row.SourceKind,
			Source: row.Source, Silent: row.Silent, Epic: row.Epic, Limit: row.LimitKey,
		})
	}
	return out, nil
}

// NoticeIDFloor returns the highest id ever put, or 0 when none has been. Deletes do not lower it.
func (s *Store) NoticeIDFloor(ctx context.Context) (int64, error) {
	floor, err := sqlcgen.New(s.db).GetNoticeIDFloor(ctx)
	if err != nil {
		return 0, fmt.Errorf("read notice id floor: %w", err)
	}
	return floor, nil
}
