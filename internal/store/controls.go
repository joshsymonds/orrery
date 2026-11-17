package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrNoTurn reports that the epic has no turn with the seq asked for.
var ErrNoTurn = errors.New("no turn")

// ErrNoDueResume reports that no due row has the id asked for.
var ErrNoDueResume = errors.New("no due resume")

// RefusalError is the error a control returns when the epic is outside the state the control needs. The control
// writes nothing, and the text says why.
type RefusalError string

// Error returns the reason the control was refused.
func (r RefusalError) Error() string {
	return string(r)
}

// PauseEpic pauses epic in one write, so the planner starts no turn for it, and records the owner as its pause source.
// An epic a usage limit paused is accepted and becomes the owner's pause, and its due rows close in the same write, so
// no reset resumes it. An unknown epic is ErrNoEpic, and an epic the owner already paused, or paused with no recorded
// source, is a RefusalError.
func (s *Store) PauseEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Paused != 0 && head.PauseSource != pauseLimit {
			return RefusalError(fmt.Sprintf("epic %s is already paused", epic))
		}
		return pauseAsOwner(ctx, queries, epic)
	})
}

// ResumeEpic clears the pause of epic and closes its due rows in one write and leaves any park in place. An unknown
// epic is ErrNoEpic. An epic that is not paused is a RefusalError, and a parked one names retry, since only retry ends
// a park.
func (s *Store) ResumeEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		if head.Paused == 0 {
			if head.Parked != "" {
				return RefusalError(fmt.Sprintf("epic %s is parked, not paused; retry ends a park", epic))
			}
			return RefusalError(fmt.Sprintf("epic %s is not paused", epic))
		}
		return resumeEpic(ctx, queries, epic)
	})
}

// ResumeDueEpic resumes the epic of the due row id in one write. The row must be a due row of an epic, and an unknown
// row is ErrNoDueResume. When the row is still due and its epic is paused by a usage limit, the write clears the pause
// as ResumeEpic does, closes the epic's due rows, and reports true. Otherwise the write marks the row done and reports
// false.
func (s *Store) ResumeDueEpic(ctx context.Context, id string) (bool, error) {
	var resumed bool
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		row, err := queries.GetDueResume(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("due resume %s: %w", id, ErrNoDueResume)
		}
		if err != nil {
			return fmt.Errorf("get due resume %s: %w", id, err)
		}
		if row.Kind != dueKindEpic {
			return fmt.Errorf("due resume %s is of kind %s, not %s", id, row.Kind, dueKindEpic)
		}
		if row.State != "due" {
			return closeDueResume(ctx, queries, id)
		}
		head, err := epicHead(ctx, queries, row.Target)
		if err != nil {
			return err
		}
		if head.Paused == 0 || head.PauseSource != pauseLimit {
			return closeDueResume(ctx, queries, id)
		}
		resumed = true
		return resumeEpic(ctx, queries, row.Target)
	})
	if err != nil {
		return false, err
	}
	return resumed, nil
}

// CloseDueResume marks the due row id done in one write. It resumes nothing and leaves the pause of its epic as it is.
func (s *Store) CloseDueResume(ctx context.Context, id string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return closeDueResume(ctx, queries, id)
	})
}

// StopEpic pauses epic as its owner's, closes its due rows, and flags its running turn to stop, in one write, and
// returns the turn's step, empty while the turn has none. The turn keeps its claimed triggers. An epic with no running
// turn that holds its repository's release slot frees the slot in one write instead, and returns the id of its release
// step that has no end, or "" when none runs. It neither pauses the epic nor changes its phase. An unknown epic is
// ErrNoEpic, and an epic with no running turn that holds no slot is a RefusalError.
func (s *Store) StopEpic(ctx context.Context, epic string) (string, error) {
	var step string
	err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		running, err := queries.GetRunningEpicTurn(ctx, epic)
		if errors.Is(err, sql.ErrNoRows) {
			step, err = stopReleaseSlot(ctx, queries, head)
			return err
		}
		if err != nil {
			return fmt.Errorf("read running turn of epic %s: %w", epic, err)
		}
		if err = queries.SetEpicTurnStop(ctx, sqlcgen.SetEpicTurnStopParams{Epic: epic, Seq: running.Seq}); err != nil {
			return fmt.Errorf("stop turn %d of epic %s: %w", running.Seq, epic, err)
		}
		step = running.Step
		return pauseAsOwner(ctx, queries, epic)
	})
	if err != nil {
		return "", err
	}
	return step, nil
}

// RetryEpic ends the park of epic in one write and leaves its triggers pending. The failed turns up to the epic's
// highest turn seq stop counting toward a park. Each loop of epic parked by ParkLoop turns running with one more
// granted round. Each parked evidence run that an open park question for a red run judged holding at base names turns
// running at its next attempt, and each open park question of epic is answered retry, in the same write. An unknown
// epic is ErrNoEpic, an epic that is not parked is a RefusalError, an epic in phase met_at_base, which the owner
// ended, is a RefusalError that changes nothing, and so is an epic in phase stopped, whose release park retry does not end.
func (s *Store) RetryEpic(ctx context.Context, epic string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return retryEpicIn(ctx, queries, epic)
	})
}

// retryEpicIn ends the park of epic in the caller's write, as RetryEpic does.
func retryEpicIn(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	head, err := epicHead(ctx, queries, epic)
	if err != nil {
		return err
	}
	if head.Phase == phaseMetAtBase {
		return metAtBaseRefusal(epic, head.Parked)
	}
	if head.Phase == PhaseStopped {
		return RefusalError(fmt.Sprintf("epic %s is stopped; retry does not end a stopped release", epic))
	}
	if head.Parked == "" {
		return RefusalError(fmt.Sprintf("epic %s is not parked", epic))
	}
	err = rerunMetAtBaseRuns(ctx, queries, epic)
	if err != nil {
		return err
	}
	err = closeParkQuestions(ctx, queries, epic)
	if err != nil {
		return err
	}
	return endPark(ctx, queries, epic)
}

// rerunMetAtBaseRuns turns each parked evidence run that an open park question of epic names running at its next
// attempt, in the caller's write. A park question that names no run, such as a planner's, changes nothing.
func rerunMetAtBaseRuns(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	open, err := queries.ListOpenEpicParkQuestions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read open park questions of epic %s: %w", epic, err)
	}
	for _, question := range open {
		var met sqlcgen.EpicMetAtBase
		met, err = queries.GetEpicMetAtBase(ctx, sqlcgen.GetEpicMetAtBaseParams{Epic: epic, Question: question.Seq})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			continue
		case err != nil:
			return fmt.Errorf("read park question %d of epic %s: %w", question.Seq, epic, err)
		}
		err = rerunParkedRun(ctx, queries, epic, met.Path)
		if err != nil {
			return err
		}
	}
	return nil
}

// endPark ends the park of epic in the caller's write: the failed turns up to the epic's highest turn seq stop counting
// toward a park, and each loop of epic parked by ParkLoop turns running with one more granted round.
func endPark(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	highest, err := queries.MaxEpicTurnSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read turn count of epic %s: %w", epic, err)
	}
	if err = queries.RetryEpic(ctx, sqlcgen.RetryEpicParams{Retried: highest, ID: epic}); err != nil {
		return fmt.Errorf("retry epic %s: %w", epic, err)
	}
	return grantParkedLoops(ctx, queries, epic)
}

// closeParkQuestions answers each open park question of epic with retry in the caller's write. Ending a park by retry
// takes the retry the question recommends.
func closeParkQuestions(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	open, err := queries.ListOpenEpicParkQuestions(ctx, epic)
	if err != nil {
		return fmt.Errorf("read open park questions of epic %s: %w", epic, err)
	}
	for _, question := range open {
		if err = queries.SetEpicQuestionAnswered(ctx, sqlcgen.SetEpicQuestionAnsweredParams{
			Choice: ChoiceRetry, Epic: epic, Seq: question.Seq,
		}); err != nil {
			return fmt.Errorf("answer park question %d of epic %s: %w", question.Seq, epic, err)
		}
	}
	return nil
}

// endedPhase reports whether phase is one an epic ends in, which a kick is refused for. The store keeps its own list,
// since the kinds package that reads the phases imports the store. The closed phases are ended too (IsClosedPhase).
func endedPhase(phase string) bool {
	switch phase {
	case "met_at_base", "clean", "ended_with_gaps", "released", "done", "stopped":
		return true
	}
	return IsClosedPhase(phase)
}

// KickEpic records text on epic as the owner's decision and adds a pending trigger whose cause carries text, in one
// write. The decision's id is `owner-kick:<seq>` for the decision's seq, which the store assigns, and the trigger has
// the same id, so the planner claims it at the epic's next plan run and its turn prompt shows text. An unknown epic is
// ErrNoEpic. An epic in an ended phase, a paused epic and a parked epic are refused and write nothing; the refusal for
// a parked epic names orrery epic retry, which ends the park.
func (s *Store) KickEpic(ctx context.Context, epic, text string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		head, err := epicHead(ctx, queries, epic)
		if err != nil {
			return err
		}
		switch {
		case endedPhase(head.Phase):
			return RefusalError(fmt.Sprintf("epic %s is %s, an ended phase, and takes no kick", epic, head.Phase))
		case head.Paused != 0:
			return RefusalError(fmt.Sprintf("epic %s is paused; resume it before kicking it", epic))
		case head.Parked != "":
			return RefusalError(fmt.Sprintf(
				"epic %s is parked; orrery epic retry %s ends the park before a kick", epic, epic,
			))
		}
		next, err := queries.MaxEpicDecisionSeq(ctx, epic)
		if err != nil {
			return fmt.Errorf("read decision count of epic %s: %w", epic, err)
		}
		id := fmt.Sprintf("owner-kick:%d", next+1)
		if _, err = appendDecision(ctx, queries, epic, id, text, 0, at); err != nil {
			return err
		}
		if err = queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
			Epic: epic, ID: id, Cause: "owner kick: " + text, State: triggerPending, Turn: 0, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert trigger of kick %s on epic %s: %w", id, epic, err)
		}
		return nil
	})
}

// TurnStopped reports whether turn seq of epic was flagged to stop. A turn that does not exist is an error matching
// ErrNoTurn.
func (s *Store) TurnStopped(ctx context.Context, epic string, seq int64) (bool, error) {
	stop, err := sqlcgen.New(s.db).GetEpicTurnStop(ctx, sqlcgen.GetEpicTurnStopParams{Epic: epic, Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("turn %d of epic %s: %w", seq, epic, ErrNoTurn)
	}
	if err != nil {
		return false, fmt.Errorf("read stop of turn %d of epic %s: %w", seq, epic, err)
	}
	return stop != 0, nil
}

// epicHead returns the stored head of epic. An unknown epic is ErrNoEpic.
func epicHead(ctx context.Context, queries *sqlcgen.Queries, epic string) (sqlcgen.Epic, error) {
	head, err := queries.GetEpic(ctx, epic)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.Epic{}, fmt.Errorf("epic %s: %w", epic, ErrNoEpic)
	}
	if err != nil {
		return sqlcgen.Epic{}, fmt.Errorf("get epic %s: %w", epic, err)
	}
	return head, nil
}

// pauseAsOwner pauses epic with the owner as its pause source and closes its due rows in the caller's write, so no
// reset that is still due resumes it.
func pauseAsOwner(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	if err := pauseEpicAs(ctx, queries, epic, pauseOwner); err != nil {
		return err
	}
	return closeEpicDueResumes(ctx, queries, epic)
}

// resumeEpic clears the pause of epic and closes its due rows in the caller's write, so no reset that is still due
// resumes it later.
func resumeEpic(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	if err := queries.ResumeEpic(ctx, epic); err != nil {
		return fmt.Errorf("resume epic %s: %w", epic, err)
	}
	return closeEpicDueResumes(ctx, queries, epic)
}

// closeEpicDueResumes marks every due row of epic done in the caller's write.
func closeEpicDueResumes(ctx context.Context, queries *sqlcgen.Queries, epic string) error {
	closed := sqlcgen.CloseDueResumesOfParams{Kind: dueKindEpic, Target: epic}
	if err := queries.CloseDueResumesOf(ctx, closed); err != nil {
		return fmt.Errorf("close due resumes of epic %s: %w", epic, err)
	}
	return nil
}

// closeDueResume marks the due row id done in the caller's write.
func closeDueResume(ctx context.Context, queries *sqlcgen.Queries, id string) error {
	if err := queries.CloseDueResume(ctx, id); err != nil {
		return fmt.Errorf("close due resume %s: %w", id, err)
	}
	return nil
}
