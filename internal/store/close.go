package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The phases an epic closed by hand ends in, one per close kind. A closed epic is not live, and no node, build or
// planner turn starts on it.
const (
	PhaseClosedSuperseded = "closed_superseded"
	PhaseClosedShipped    = "closed_shipped"
	PhaseClosedAbandoned  = "closed_abandoned"
)

// The closers of an epic close: the owner, or the planner, whose close routes the ending report to the owner.
const (
	CloserOwner   = "owner"
	CloserPlanner = "planner"
)

// closedPrefix starts the phase of every close kind: a kind's phase is closedPrefix followed by the kind.
const closedPrefix = "closed_"

// The close kinds the owner names: an epic closed superseded names the epic that replaces it.
const (
	kindSuperseded = "superseded"
	kindShipped    = "shipped"
	kindAbandoned  = "abandoned"
)

// IsClosedPhase reports whether phase is one of the phases a close ends an epic in.
func IsClosedPhase(phase string) bool {
	switch phase {
	case PhaseClosedSuperseded, PhaseClosedShipped, PhaseClosedAbandoned:
		return true
	}
	return false
}

// closeEnd is one epic close the write carries out: the epic, the kind it closes as and the phase that kind ends in,
// the epic that replaces it, the closer and the time.
type closeEnd struct {
	epic, kind, phase, by, closer string
	at                            time.Time
}

// CloseEpic closes epic as kind in one write, and returns the step of the turn it ended, "" when no turn runs. kind is
// superseded, shipped or abandoned. by names the epic that replaces epic, which a superseded close requires; it is
// recorded as named. closer is CloserOwner or CloserPlanner.
//
// The write sets the epic's phase to the closed phase of kind, records closer and by, and clears its park and pause.
// Each open question is answered with the closed phase, so the epic has none open. Its running turn ends free, which
// never counts toward a park, and the triggers it claimed return to pending. Every pending trigger of the epic is then
// consumed, so the planner has nothing to claim. Its running node runs end, its due resumes close, and a release slot it
// holds is freed. A planner close also adds one tell, which says the epic is closed and is the ending the owner is told;
// an owner close adds none.
//
// An unknown epic is ErrNoEpic. A close the store refuses is a RefusalError and writes nothing: an unknown kind, and a
// superseded close without by. An unknown closer is an error and writes nothing.
func (s *Store) CloseEpic(ctx context.Context, epic, kind, by, closer string, at time.Time) (string, error) {
	var step string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := epicHead(ctx, queries, epic); err != nil {
			return err
		}
		end, err := closeEndOf(epic, kind, by, closer, at)
		if err != nil {
			return err
		}
		step, err = endClosedTurn(ctx, queries, end)
		if err != nil {
			return err
		}
		return closeEpicRows(ctx, queries, end)
	})
	if err != nil {
		return "", err
	}
	return step, nil
}

// closeEndOf returns the close of epic as kind, by, closer at at, or a RefusalError when the close is refused: kind is
// unknown, or superseded names no by. An unknown closer is an error.
func closeEndOf(epic, kind, by, closer string, at time.Time) (closeEnd, error) {
	if kind != kindSuperseded && kind != kindShipped && kind != kindAbandoned {
		return closeEnd{}, RefusalError(
			fmt.Sprintf("close epic %s: kind %q is not superseded, shipped or abandoned", epic, kind),
		)
	}
	if kind == kindSuperseded && by == "" {
		return closeEnd{}, RefusalError(fmt.Sprintf("epic %s is superseded and names no epic that replaces it", epic))
	}
	if closer != CloserOwner && closer != CloserPlanner {
		return closeEnd{}, fmt.Errorf("close epic %s by %q: closer is neither owner nor planner", epic, closer)
	}
	return closeEnd{epic: epic, kind: kind, phase: closedPrefix + kind, by: by, closer: closer, at: at}, nil
}

// endClosedTurn ends the running turn of epic free, in the caller's write, and returns its step. It returns "" when no
// turn runs. The turn's claimed triggers return to pending, which closeEpicRows consumes with the rest.
func endClosedTurn(ctx context.Context, queries *sqlcgen.Queries, end closeEnd) (string, error) {
	running, err := queries.GetRunningEpicTurn(ctx, end.epic)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read running turn of epic %s: %w", end.epic, err)
	}
	err = endTurn(ctx, queries, end.epic, running.Seq, "free", "epic closed as "+end.kind, end.at)
	if err != nil {
		return "", err
	}
	return running.Step, nil
}

// closeEpicRows writes end in the caller's write: the answers, the node runs, the head, the phase, the release slot, the
// triggers and the due resumes, and for a planner close the ending tell.
func closeEpicRows(ctx context.Context, queries *sqlcgen.Queries, end closeEnd) error {
	if err := answerOpenQuestions(ctx, queries, end.epic, end.phase); err != nil {
		return err
	}
	if err := queries.EndRunningEpicNodeRuns(ctx, sqlcgen.EndRunningEpicNodeRunsParams{
		Ended: end.at.Unix(), Epic: end.epic,
	}); err != nil {
		return fmt.Errorf("end node runs of epic %s: %w", end.epic, err)
	}
	if err := queries.SetEpicClosed(ctx, sqlcgen.SetEpicClosedParams{
		ClosedBy: end.closer, SupersededBy: end.by, ID: end.epic,
	}); err != nil {
		return fmt.Errorf("record close of epic %s: %w", end.epic, err)
	}
	if err := setEpicPhase(ctx, queries, end.epic, end.phase, end.at); err != nil {
		return err
	}
	if err := queries.FreeEpicReleaseSlot(ctx, end.epic); err != nil {
		return fmt.Errorf("free release slot of epic %s: %w", end.epic, err)
	}
	if err := queries.ConsumePendingEpicTriggers(ctx, end.epic); err != nil {
		return fmt.Errorf("consume triggers of epic %s: %w", end.epic, err)
	}
	if err := resumeEpic(ctx, queries, end.epic); err != nil {
		return err
	}
	if end.closer == CloserPlanner {
		return insertTell(ctx, queries, end.epic, closeTell(end), end.at)
	}
	return nil
}

// answerOpenQuestions answers each open question of epic with choice, the phase the close ends the epic in, in the
// caller's write.
func answerOpenQuestions(ctx context.Context, queries *sqlcgen.Queries, epic, choice string) error {
	open, err := queries.ListOpenEpicQuestions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read open questions of epic %s: %w", epic, err)
	}
	for _, question := range open {
		if err = queries.SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
			Choice: choice, Epic: epic, Seq: question.Seq,
		}); err != nil {
			return fmt.Errorf("answer question %d of epic %s: %w", question.Seq, epic, err)
		}
	}
	return nil
}

// closeTell is the tell a planner close adds: the kind it closed as, and the epic that replaces it when superseded.
func closeTell(end closeEnd) string {
	if end.by == "" {
		return "Closed as " + end.kind + "."
	}
	return "Closed as " + end.kind + " by " + end.by + "."
}
