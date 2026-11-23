package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// EngineRow is one headless engine session row, copied from its sessions columns. Tracked is set when the row's
// consumed offset is not NULL, that is, when an EngineLaunch has tracked it. Consumed, ReplayFrom, Background, Awaited
// and TurnOutput are the re-adoption state the row holds. Host is the orrery name of the host the session runs on, or
// "" when none is named. Held is set while the row's working state is held by a background task, with no turn running
// (see EngineChanges).
type EngineRow struct {
	ID            string
	Dir           string
	State         string
	Question      string
	ClaudeSession string
	Reason        string
	Kind          string
	Host          string
	Tracked       bool
	Consumed      int64
	ReplayFrom    int64
	Background    bool
	Awaited       bool
	TurnOutput    string
	Held          bool
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
	rows, err := sqlcgen.New(s.read).ListHolderPrompts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list holder prompts of engine session %s: %w", id, err)
	}
	out := make([]HolderPrompt, 0, len(rows))
	for _, row := range rows {
		out = append(out, HolderPrompt{WriteID: row.WriteID, Text: row.Text})
	}
	return out, nil
}

// HolderPromptsUnanswered returns how many prompts were written to an engine session's holder that no later turn end
// answers: the engine_prompt events above the session's prompts_from that carry a write id and that no engine_turn_ended
// with a greater seq answers. A turn end answers a prompt when its answered column lists the prompt's write id, and a
// turn end with a NULL answered column, stored before the column existed, answers every earlier prompt. It returns 0 for
// an id that names no engine session.
func (s *Store) HolderPromptsUnanswered(ctx context.Context, id string) (int, error) {
	count, err := sqlcgen.New(s.read).CountHolderPromptsUnanswered(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count holder prompts no turn end answers of engine session %s: %w", id, err)
	}
	return int(count), nil
}

// EngineSessions returns every engine and step session row, ordered by id.
func (s *Store) EngineSessions(ctx context.Context) ([]EngineRow, error) {
	rows, err := sqlcgen.New(s.read).ListEngineSessions(ctx)
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
	row, err := sqlcgen.New(s.read).GetEngineSession(ctx, id)
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
	rows, err := sqlcgen.New(s.read).ListUnfinishedEngineText(ctx)
	if err != nil {
		return nil, fmt.Errorf("list engine sessions with unfinished text: %w", err)
	}
	out := make(map[string]bool)
	for _, row := range rows {
		out[row.SessionID] = true
	}
	return out, nil
}

// QuietSessions returns the ids of the headless engine sessions that the owner's Interrupt left quiet and that no owner
// Say has cleared since: each whose newest engine_interrupt has a higher seq than its newest engine_prompt sent by the
// owner, or that holds an engine_interrupt and no owner prompt at all. A step session is never listed. The ids are in
// id order.
func (s *Store) QuietSessions(ctx context.Context) ([]string, error) {
	ids, err := sqlcgen.New(s.read).ListQuietSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list quiet engine sessions: %w", err)
	}
	return ids, nil
}

func toEngineRow(row sqlcgen.Session) EngineRow {
	return EngineRow{
		ID: row.ID, Dir: row.Dir, State: row.State, Question: row.Question,
		ClaudeSession: row.ClaudeSession, Reason: row.Reason, Kind: row.Kind, Host: row.Host,
		Tracked: row.Consumed.Valid, Consumed: row.Consumed.Int64, ReplayFrom: row.ReplayFrom,
		Background: row.Background, Awaited: row.Awaited, TurnOutput: row.TurnOutput, Held: row.Held,
	}
}
