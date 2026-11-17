package store

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// triggerPending is the state of a trigger no turn has claimed yet.
const triggerPending = "pending"

// FailTask fails task seq of epic in one write, when its stored build is from: the task is failed, its failure is
// reason cut to TurnReasonMax bytes, its needsFiles are stored as the files a later task may need, and a pending
// trigger `T<seq>-failed` with cause `task T<seq> failed: <reason>` is added. A task that is not open, or whose stored
// build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) FailTask(
	ctx context.Context,
	epic string,
	seq int64,
	from TaskBuild,
	reason string,
	needsFiles []string,
	at time.Time,
) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		stored := capTurnText(reason)
		to := from
		to.Failure = stored
		if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		if err := setTaskStatus(ctx, queries, epic, seq, "failed"); err != nil {
			return err
		}
		if err := setTaskNeedsFiles(ctx, queries, epic, seq, needsFiles); err != nil {
			return err
		}
		if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
			Epic: epic, ID: fmt.Sprintf("T%d-failed", seq), Cause: fmt.Sprintf("task T%d failed: %s", seq, stored),
			State: triggerPending, Turn: 0, Created: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert failure trigger of task %d of epic %s: %w", seq, epic, err)
		}
		return nil
	})
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

// FileOwnedError is GrantTaskFiles's refusal of a file another open task of the epic owns.
type FileOwnedError struct {
	File string
	Task int64
}

// Error names the file and the task that owns it.
func (e FileOwnedError) Error() string {
	return fmt.Sprintf("%s is owned by open task T%d", e.File, e.Task)
}

// GrantTaskFiles adds files, in order, after the files of task seq of epic and stores to as its build, in one write,
// when its stored build is from. A file another open task of the epic owns is a FileOwnedError and writes nothing. A
// task that is not open, or whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) GrantTaskFiles(ctx context.Context, epic string, seq int64, from, to TaskBuild, files []string) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		tasks, err := queries.ListEpicTasks(ctx, epic)
		if err != nil {
			return fmt.Errorf("read tasks of epic %s: %w", epic, err)
		}
		rows, err := queries.ListEpicTaskFiles(ctx, epic)
		if err != nil {
			return fmt.Errorf("read task files of epic %s: %w", epic, err)
		}
		open := map[int64]bool{}
		for _, task := range tasks {
			open[task.Seq] = task.Status == "open"
		}
		var next int64
		for _, row := range rows {
			if row.Task == seq {
				next = max(next, row.Position+1)
				continue
			}
			if open[row.Task] && slices.Contains(files, row.Path) {
				return FileOwnedError{File: row.Path, Task: row.Task}
			}
		}
		if err = moveTask(ctx, queries, epic, seq, from, to); err != nil {
			return err
		}
		for i, file := range files {
			if err = queries.InsertEpicTaskFile(ctx, sqlcgen.InsertEpicTaskFileParams{
				Epic: epic, Task: seq, Position: next + int64(i), Path: file,
			}); err != nil {
				return fmt.Errorf("grant file %s to task %d of epic %s: %w", file, seq, epic, err)
			}
		}
		return nil
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

// setTaskNeedsFiles stores paths as the files task seq of epic names as needed beyond its owned list, as a JSON array,
// and [] for none.
func setTaskNeedsFiles(ctx context.Context, queries *sqlcgen.Queries, epic string, seq int64, paths []string) error {
	encoded, err := encodeList(paths)
	if err != nil {
		return fmt.Errorf("encode needs files of task %d of epic %s: %w", seq, epic, err)
	}
	if err = queries.SetEpicTaskNeedsFiles(ctx, sqlcgen.SetEpicTaskNeedsFilesParams{
		NeedsFiles: encoded, Epic: epic, Seq: seq,
	}); err != nil {
		return fmt.Errorf("set needs files of task %d of epic %s: %w", seq, epic, err)
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
