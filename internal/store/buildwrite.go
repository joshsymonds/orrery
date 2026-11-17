package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// triggerPending is the state of a trigger no turn has claimed yet.
const triggerPending = "pending"

// FailTask fails task seq of epic in one write, when its stored build is from: the task is failed, its failure is
// reason cut to TurnReasonMax bytes, and a pending trigger `T<seq>-failed` with cause `task T<seq> failed: <reason>` is
// added. A task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) FailTask(
	ctx context.Context,
	epic string,
	seq int64,
	from TaskBuild,
	reason string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return failTaskIn(ctx, queries, epic, seq, from, reason, at)
	})
}

// failTaskIn fails task seq of epic in the caller's write, as FailTask does.
func failTaskIn(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	from TaskBuild,
	reason string,
	at time.Time,
) error {
	stored := capTurnText(reason)
	to := from
	to.Failure = stored
	if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
		return err
	}
	if err := setTaskStatus(ctx, queries, epic, seq, "failed"); err != nil {
		return err
	}
	if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: fmt.Sprintf("T%d-failed", seq), Cause: fmt.Sprintf("task T%d failed: %s", seq, stored),
		State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert failure trigger of task %d of epic %s: %w", seq, epic, err)
	}
	return nil
}

// AcceptTask marks task seq of epic done with review in one write, when its stored build is from. A task that is not
// open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) AcceptTask(ctx context.Context, epic string, seq int64, from TaskBuild, review string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		to := from
		to.Review = review
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		return setTaskStatus(ctx, queries, epic, seq, "done")
	})
}

// SetTaskBuildTelling stores to as the build of task seq of epic and adds each of tells as a tell of epic, in one
// write, when the stored build is from, as SetTaskBuild does. A task that is not open, or whose stored build is not
// from, is ErrTaskMoved and adds no tell.
func (s *Store) SetTaskBuildTelling(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	tells []string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		return insertTells(ctx, queries, epic, tells, at)
	})
}

// StopTask stores to as the build of task seq of epic and pauses the epic with the owner as its pause source, closing
// its due rows, in one write, when the stored build is from. A task that is not open, or whose stored build is not
// from, is ErrTaskMoved and pauses nothing.
func (s *Store) StopTask(ctx context.Context, epic string, seq int64, from, to TaskBuild) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		return pauseAsOwner(ctx, queries, epic)
	})
}

// ParkTask stores to as the build of task seq of epic, parks the epic with reason, adds reason as a tell, and adds each
// of tells after it, in one write, when the stored build is from. Reason is cut to TurnReasonMax bytes. A task that is
// not open, or whose stored build is not from, is ErrTaskMoved and parks nothing.
func (s *Store) ParkTask(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	reason string,
	at time.Time,
	tells ...string,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		if err := parkWithTell(ctx, queries, epic, capTurnText(reason), at); err != nil {
			return err
		}
		return insertTells(ctx, queries, epic, tells, at)
	})
}

// ParkEpic parks epic with reason and adds reason as a tell, in one write. Reason is cut to TurnReasonMax bytes. An
// unknown epic is ErrNoEpic and writes nothing.
func (s *Store) ParkEpic(ctx context.Context, epic, reason string, at time.Time) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return parkWithTell(ctx, queries, epic, capTurnText(reason), at)
	})
}

// SetEpicBranch stores state as the branch state of epic. An unknown epic is ErrNoEpic.
func (s *Store) SetEpicBranch(ctx context.Context, epic, state string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if _, err := epicHead(ctx, queries, epic); err != nil {
			return err
		}
		if err := queries.SetEpicBranch(ctx, sqlcgen.SetEpicBranchParams{Branch: state, ID: epic}); err != nil {
			return fmt.Errorf("set branch of epic %s: %w", epic, err)
		}
		return nil
	})
}

// moveTask stores to as the build of task seq of epic when the stored build is from, as SetTaskBuild does, and is
// ErrTaskMoved when the task is not open or its build differs from from.
func moveTask(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, from, to TaskBuild) error {
	rows, err := queries.SetEpicTaskBuild(ctx, setEpicTaskBuildParams(epic, seq, from, to))
	if err != nil {
		return fmt.Errorf("set build of task %d of epic %s: %w", seq, epic, err)
	}
	if rows == 0 {
		return fmt.Errorf("task %d of epic %s: %w", seq, epic, ErrTaskMoved)
	}
	return nil
}

// setTaskStatus stores status as the status of task seq of epic.
func setTaskStatus(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, status string) error {
	if err := queries.SetEpicTaskStatus(ctx, sqlcgen.SetEpicTaskStatusParams{
		Status: status, Epic: epic, Seq: seq,
	}); err != nil {
		return fmt.Errorf("set status of task %d of epic %s: %w", seq, epic, err)
	}
	return nil
}

// parkWithTell parks epic with reason, opens its park question, and adds reason as a tell that carries the seq of that
// question, in the write the caller runs.
func parkWithTell(ctx context.Context, queries *sqlcgen.Queries, epic, reason string, at time.Time) error {
	question, err := setParked(ctx, queries, epic, reason)
	if err != nil {
		return err
	}
	return insertQuestionTell(ctx, queries, epic, reason, question, at)
}

// insertTells adds each of texts to epic as tells, in order, as insertTell does.
func insertTells(ctx context.Context, queries *sqlcgen.Queries, epic string, texts []string, at time.Time) error {
	for _, text := range texts {
		if err := insertTell(ctx, queries, epic, text, at); err != nil {
			return err
		}
	}
	return nil
}

// insertTell adds text to epic as its next tell, numbered one past its highest, and not yet told.
func insertTell(ctx context.Context, queries *sqlcgen.Queries, epic, text string, at time.Time) error {
	highest, err := queries.MaxEpicTellSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read tells of epic %s: %w", epic, err)
	}
	if err = queries.InsertEpicTell(ctx, sqlcgen.InsertEpicTellParams{
		Epic: epic, Seq: highest + 1, Text: text, At: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert tell of epic %s: %w", epic, err)
	}
	return nil
}

// insertQuestionTell adds text to epic as its next tell, as insertTell does, carrying question, the seq of the park
// question the tell belongs to.
func insertQuestionTell(
	ctx context.Context, queries *sqlcgen.Queries, epic, text string, question int64, at time.Time,
) error {
	highest, err := queries.MaxEpicTellSeq(ctx, epic)
	if err != nil {
		return fmt.Errorf("read tells of epic %s: %w", epic, err)
	}
	if err = queries.InsertEpicQuestionTell(ctx, sqlcgen.InsertEpicQuestionTellParams{
		Epic: epic, Seq: highest + 1, Text: text, At: at.Unix(), Question: question,
	}); err != nil {
		return fmt.Errorf("insert tell of epic %s: %w", epic, err)
	}
	return nil
}
