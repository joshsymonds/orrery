package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// UntitledSession is an engine session that has no stored title and has at least one prompt: its id, its dir, and the
// text of its oldest engine_prompt event.
type UntitledSession struct {
	ID, Dir, FirstPrompt string
}

// UntitledSession returns the engine session id as UntitledSessions lists it, and true, when the session is untitled
// and has a prompt. It returns false, and no error, when it is not: a titled session, a session with no prompt, a step
// or pane session, and an unknown id.
func (s *Store) UntitledSession(ctx context.Context, id string) (UntitledSession, bool, error) {
	row, err := sqlcgen.New(s.db).GetUntitledEngineSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return UntitledSession{}, false, nil
	}
	if err != nil {
		return UntitledSession{}, false, fmt.Errorf("get untitled engine session %s: %w", id, err)
	}
	return UntitledSession{ID: row.ID, Dir: row.Dir, FirstPrompt: row.FirstPrompt}, true, nil
}

// UntitledSessions returns the engine sessions with no stored title that have an engine_prompt event, in id order.
func (s *Store) UntitledSessions(ctx context.Context) ([]UntitledSession, error) {
	rows, err := sqlcgen.New(s.db).ListUntitledEngineSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list untitled engine sessions: %w", err)
	}
	untitled := make([]UntitledSession, 0, len(rows))
	for _, row := range rows {
		untitled = append(untitled, UntitledSession{ID: row.ID, Dir: row.Dir, FirstPrompt: row.FirstPrompt})
	}
	return untitled, nil
}

// SetSessionTitle stores title as the title of the engine session id when the session has no stored title, and leaves a
// stored title as it is, so a title is written once. A pane or step session, and an unknown id, is not written and is
// not an error.
func (s *Store) SetSessionTitle(ctx context.Context, id, title string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := queries.SetSessionTitle(ctx, sqlcgen.SetSessionTitleParams{Title: title, ID: id}); err != nil {
			return fmt.Errorf("set title of session %s: %w", id, err)
		}
		return nil
	})
}
