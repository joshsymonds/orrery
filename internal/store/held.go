package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// HeldPrompt is a message held for an engine session: the id of the prompt it is sent as, and its text.
type HeldPrompt struct {
	PromptID string
	Text     string
}

// EngineHold adds a message to the held set of the engine session ID, after the messages held before it. A prompt id
// the session already holds keeps its stored row, so a repeat changes nothing. It appends no event.
type EngineHold struct {
	ID       string
	PromptID string
	Text     string
}

// EngineHeldRelease removes the message with prompt id PromptID from the held set of the engine session ID, in the write
// that sends it. A prompt id the session does not hold is not an error. It appends no event.
type EngineHeldRelease struct {
	ID       string
	PromptID string
}

func (op EngineHold) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	_, err := queries.GetEngineSession(ctx, op.ID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("engine session %s: %w", op.ID, ErrNoEngineSession)
	case err != nil:
		return fmt.Errorf("read engine session %s: %w", op.ID, err)
	}
	if err = queries.InsertEngineHeld(ctx, sqlcgen.InsertEngineHeldParams{
		SessionID: op.ID, PromptID: op.PromptID, Text: op.Text,
	}); err != nil {
		return fmt.Errorf("hold prompt %s of session %s: %w", op.PromptID, op.ID, err)
	}
	return nil
}

func (op EngineHeldRelease) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.DeleteHeldPrompt(ctx, sqlcgen.DeleteHeldPromptParams{
		SessionID: op.ID, PromptID: op.PromptID,
	}); err != nil {
		return fmt.Errorf("release held prompt %s of session %s: %w", op.PromptID, op.ID, err)
	}
	return nil
}

// HeldPrompts returns the messages held for the engine session id, in the order they were held. It returns none for an
// id that holds none. It reads the writer's database, as EnginePromptSent does.
func (s *Store) HeldPrompts(ctx context.Context, id string) ([]HeldPrompt, error) {
	rows, err := sqlcgen.New(s.db).ListHeldPrompts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list held prompts of engine session %s: %w", id, err)
	}
	return toHeldPrompts(rows), nil
}

// HeldPrompts returns the messages held for the engine session id, in the order they were held. It returns none for an
// id that holds none. It reads the store file as Snapshot does, and returns an error matching ErrUnavailable when the
// file is missing or the database is busy.
func (r *Reader) HeldPrompts(ctx context.Context, id string) ([]HeldPrompt, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	rows, err := sqlcgen.New(db).ListHeldPrompts(ctx, id)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("read held prompts of engine session %s: %w", id, err)
	}
	return toHeldPrompts(rows), nil
}

// toHeldPrompts converts the held rows a query read, in their order.
func toHeldPrompts(rows []sqlcgen.ListHeldPromptsRow) []HeldPrompt {
	held := make([]HeldPrompt, 0, len(rows))
	for _, row := range rows {
		held = append(held, HeldPrompt{PromptID: row.PromptID, Text: row.Text})
	}
	return held
}
