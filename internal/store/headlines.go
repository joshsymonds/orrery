package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HeadlineDueSession is an engine session whose newest engine_turn_ended event is later than its headline_at. It holds
// the session's id, its dir, the text of its oldest engine_prompt event ("" when it has none), the unix seconds of that
// newest turn end, and the unix seconds of its last headline write.
type HeadlineDueSession struct {
	ID, Dir, FirstPrompt string
	TurnAt, HeadlineAt   int64
}

// HeadlineDueSessions returns the engine sessions whose newest engine_turn_ended event is later than their headline_at,
// in id order. A session that has never been headlined has headline_at 0, so each of its turn ends makes it due.
func (s *Store) HeadlineDueSessions(ctx context.Context) ([]HeadlineDueSession, error) {
	rows, err := sqlcgen.New(s.db).ListHeadlineDueSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list headline due sessions: %w", err)
	}
	due := make([]HeadlineDueSession, 0, len(rows))
	for _, row := range rows {
		due = append(due, HeadlineDueSession{
			ID: row.ID, Dir: row.Dir, FirstPrompt: row.FirstPrompt, TurnAt: row.TurnAt, HeadlineAt: row.HeadlineAt,
		})
	}
	return due, nil
}

// HeadlineDueSession returns the engine session id as HeadlineDueSessions lists it, and true, when the session is due. It
// returns false, and no error, when it is not: a session whose newest turn end is not later than its headline_at, a
// step or pane session, and an unknown id.
func (s *Store) HeadlineDueSession(ctx context.Context, id string) (HeadlineDueSession, bool, error) {
	row, err := sqlcgen.New(s.db).GetHeadlineDueSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return HeadlineDueSession{}, false, nil
	}
	if err != nil {
		return HeadlineDueSession{}, false, fmt.Errorf("get headline due session %s: %w", id, err)
	}
	return HeadlineDueSession{
		ID: row.ID, Dir: row.Dir, FirstPrompt: row.FirstPrompt, TurnAt: row.TurnAt, HeadlineAt: row.HeadlineAt,
	}, true, nil
}

// RecentTurn is one of a session's recent turns. Role is "user" for an engine_prompt text the owner sent, and
// "assistant" for a main-conversation text block the session wrote.
type RecentTurn struct {
	Role, Text string
}

// RecentTurns returns the newest items of the engine session id: its engine_prompt texts and its main-conversation
// assistant text blocks, at most items of them, oldest first. A sub-agent's block, and a block of any other kind or
// role, is not a turn.
func (s *Store) RecentTurns(ctx context.Context, id string, items int) ([]RecentTurn, error) {
	rows, err := sqlcgen.New(s.db).ListRecentTurns(ctx, sqlcgen.ListRecentTurnsParams{ID: id, Items: int64(items)})
	if err != nil {
		return nil, fmt.Errorf("list recent turns of session %s: %w", id, err)
	}
	// The query lists the newest first, so the rows are reversed into oldest first.
	turns := make([]RecentTurn, 0, len(rows))
	for _, row := range slices.Backward(rows) {
		turns = append(turns, RecentTurn{Role: row.Role, Text: row.Text})
	}
	return turns, nil
}

// SetSessionHeadline stores headline, and at as the time of the write, as the headline of the engine session id. A pane
// or step session, and an unknown id, is not written and is not an error.
func (s *Store) SetSessionHeadline(ctx context.Context, id, headline string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.SetSessionHeadline(ctx, sqlcgen.SetSessionHeadlineParams{
			Headline: headline, HeadlineAt: at.Unix(), ID: id,
		}); err != nil {
			return fmt.Errorf("set headline of session %s: %w", id, err)
		}
		return nil
	})
}
