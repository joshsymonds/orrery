package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EngineRow is one headless engine session row, copied from its sessions columns.
type EngineRow struct {
	ID            string
	Dir           string
	State         string
	Question      string
	ClaudeSession string
	Reason        string
	Kind          string
}

// EngineSessions returns every engine and step session row, ordered by id.
func (s *Store) EngineSessions(ctx context.Context) ([]EngineRow, error) {
	rows, err := sqlcgen.New(s.db).ListEngineSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list engine sessions: %w", err)
	}
	out := make([]EngineRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, toEngineRow(row))
	}
	return out, nil
}

// EngineSession returns the engine session row with id. It returns an error matching ErrNoEngineSession when no
// engine row has that id.
func (s *Store) EngineSession(ctx context.Context, id string) (EngineRow, error) {
	row, err := sqlcgen.New(s.db).GetEngineSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return EngineRow{}, fmt.Errorf("engine session %s: %w", id, ErrNoEngineSession)
	}
	if err != nil {
		return EngineRow{}, fmt.Errorf("get engine session %s: %w", id, err)
	}
	return toEngineRow(row), nil
}

func toEngineRow(row sqlcgen.Session) EngineRow {
	return EngineRow{
		ID: row.ID, Dir: row.Dir, State: row.State, Question: row.Question,
		ClaudeSession: row.ClaudeSession, Reason: row.Reason, Kind: row.Kind,
	}
}
