package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EngineRow is one headless engine session row, copied from its sessions columns. Tracked is set when the row's
// consumed offset is not NULL, that is, when an EngineLaunch has tracked it. Consumed, ReplayFrom, Background and
// TurnOutput are the re-adoption state the row holds.
type EngineRow struct {
	ID            string
	Dir           string
	State         string
	Question      string
	ClaudeSession string
	Reason        string
	Kind          string
	Tracked       bool
	Consumed      int64
	ReplayFrom    int64
	Background    bool
	TurnOutput    string
}

// HolderPrompt is a prompt sent to an engine session that its holder has not yet consumed: the prompt's write id
// and text.
type HolderPrompt struct {
	WriteID string
	Text    string
}

// HolderPrompts returns the prompts sent to an engine session after its launch, in seq order. Each is an
// engine_prompt event above the session's prompts_from that carries a write id. It returns none for an id that
// names no engine session.
func (s *Store) HolderPrompts(ctx context.Context, id string) ([]HolderPrompt, error) {
	rows, err := sqlcgen.New(s.db).ListHolderPrompts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list holder prompts of engine session %s: %w", id, err)
	}
	out := make([]HolderPrompt, 0, len(rows))
	for _, row := range rows {
		out = append(out, HolderPrompt{WriteID: row.WriteID, Text: row.Text})
	}
	return out, nil
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

// EngineTextSessions returns the ids of the engine sessions that hold at least one unfinished engine text block,
// each id once. A session with no unfinished text is absent from the map.
func (s *Store) EngineTextSessions(ctx context.Context) (map[string]bool, error) {
	rows, err := sqlcgen.New(s.db).ListUnfinishedEngineText(ctx)
	if err != nil {
		return nil, fmt.Errorf("list engine sessions with unfinished text: %w", err)
	}
	out := make(map[string]bool)
	for _, row := range rows {
		out[row.SessionID] = true
	}
	return out, nil
}

func toEngineRow(row sqlcgen.Session) EngineRow {
	return EngineRow{
		ID: row.ID, Dir: row.Dir, State: row.State, Question: row.Question,
		ClaudeSession: row.ClaudeSession, Reason: row.Reason, Kind: row.Kind,
		Tracked: row.Consumed.Valid, Consumed: row.Consumed.Int64, ReplayFrom: row.ReplayFrom,
		Background: row.Background, TurnOutput: row.TurnOutput,
	}
}
