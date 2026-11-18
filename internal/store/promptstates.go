package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The lifecycle states of a prompt sent to an engine session, stored as the state of each record of the prompt.
const (
	PromptQueued    = "queued"
	PromptStarted   = "started"
	PromptCompleted = "completed"
	PromptCancelled = "canceled"
	PromptDiscarded = "discarded"
)

// PromptState is a prompt sent to an engine session that has a lifecycle state: its prompt id, its text, its sender,
// and the newest of its states. PromptID and Sender are "" when the prompt carried none.
type PromptState struct {
	PromptID string
	Text     string
	Sender   string
	State    string
}

// EnginePromptState records State as the newest lifecycle state of the prompt that carries WriteID in the engine
// session ID. A prompt's states are kept in the order they are recorded, so the newest is the one recorded last. A
// canceled or discarded state is not recorded for a prompt that already has a started state: claude reports an
// interrupted opener that had started as cancelled after its turn's result, and that report must not make a message
// taken in read as not delivered. It appends no event.
type EnginePromptState struct {
	ID      string
	WriteID string
	State   string
}

// EnginePromptsDiscard records PromptDiscarded for every prompt of the engine session ID above the session's
// prompts_from whose newest state is queued or that has no state, so a prompt the session has started or finished keeps
// its state. It appends no event.
type EnginePromptsDiscard struct {
	ID string
}

func (op EnginePromptState) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.InsertPromptState(ctx, sqlcgen.InsertPromptStateParams{
		SessionID: op.ID, WriteID: op.WriteID, State: op.State,
	}); err != nil {
		return fmt.Errorf("record state %s of prompt %s of session %s: %w", op.State, op.WriteID, op.ID, err)
	}
	return nil
}

func (op EnginePromptsDiscard) applyEngine(ctx context.Context, queries *sqlcgen.Queries, _ time.Time) error {
	if err := queries.DiscardUnstartedPrompts(ctx, op.ID); err != nil {
		return fmt.Errorf("discard unstarted prompts of session %s: %w", op.ID, err)
	}
	return nil
}

// PromptStates returns the prompts of the engine session id that have a lifecycle state, each with its newest state, in
// the order they were sent. A prompt is matched to its engine_prompt event by write id, and a prompt with no state is
// absent. It reads the writer's database, as HeldPrompts does.
func (s *Store) PromptStates(ctx context.Context, id string) ([]PromptState, error) {
	rows, err := sqlcgen.New(s.db).ListPromptStates(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list prompt states of engine session %s: %w", id, err)
	}
	return toPromptStates(rows), nil
}

// PromptStates returns the prompts of the engine session id that have a lifecycle state, as Store.PromptStates does. It
// reads the store file as Snapshot does, and returns an error matching ErrUnavailable when the file is missing or the
// database is busy.
func (r *Reader) PromptStates(ctx context.Context, id string) ([]PromptState, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(r.path))
	if err != nil {
		return nil, fmt.Errorf("open store reader: %w", err)
	}
	rows, err := sqlcgen.New(db).ListPromptStates(ctx, id)
	if closeErr := db.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close store reader: %w", closeErr))
	}
	if err != nil {
		if unavailable(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return nil, fmt.Errorf("list prompt states of engine session %s: %w", id, err)
	}
	return toPromptStates(rows), nil
}

// PromptWrite is an engine prompt as it was sent: the write id it was sent under, its text and its sender, "" when it
// carried none.
type PromptWrite struct {
	WriteID string
	Text    string
	Sender  string
}

// PromptWrite returns the prompt of the engine session id that carries promptID: the first engine_prompt event of the
// session with that prompt id. It reports false with no error when the session has no such prompt. It reads the
// writer's database, as EnginePromptSent does.
func (s *Store) PromptWrite(ctx context.Context, id, promptID string) (PromptWrite, bool, error) {
	row, err := sqlcgen.New(s.db).GetPromptWrite(ctx, sqlcgen.GetPromptWriteParams{SessionID: id, PromptID: promptID})
	if errors.Is(err, sql.ErrNoRows) {
		return PromptWrite{}, false, nil
	}
	if err != nil {
		return PromptWrite{}, false, fmt.Errorf("read prompt %s of engine session %s: %w", promptID, id, err)
	}
	return PromptWrite{WriteID: row.WriteID, Text: row.Text, Sender: row.Sender}, true, nil
}

// toPromptStates converts the rows a query read, in their order.
func toPromptStates(rows []sqlcgen.ListPromptStatesRow) []PromptState {
	states := make([]PromptState, 0, len(rows))
	for _, row := range rows {
		states = append(states, PromptState{
			PromptID: row.PromptID, Text: row.Text, Sender: row.Sender, State: row.State,
		})
	}
	return states
}
