package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// ErrTaskMoved reports that a write to a task's build found the task not open, or its stored build not equal to the
// build the write expected, and so wrote nothing.
var ErrTaskMoved = errors.New("task moved")

// TaskBuild is the build state of one task. Stage is the unit the build is on, empty before its first, and Base is
// the revision the task builds from. Attempt is the attempt the build is on, Rerun counts the restarts of the current
// unit, and NoVerdict counts the units in a row that gave no verdict. Held is why the build stopped, empty while it
// runs. Step is the step of the current unit, RedRun and GreenRun name its red and green evidence runs, and Revision
// is the revision the current unit runs at. Failure is the reason of the last failed unit. Review is whether the
// review gates have run, and Refs is the refs snapshot the implementer's step is checked against. GatePos and
// FindingPos are the review gate and the finding the build is on.
type TaskBuild struct {
	Stage, Base                            string
	Attempt, Rerun, NoVerdict              int64
	Held, Step, RedRun, GreenRun, Revision string
	Failure, Review, Refs                  string
	GatePos, FindingPos                    int64
}

// TaskFinding is one finding a review raised: its position among the review's findings, its id, the claim it makes,
// where it is cited and located, its evidence and how to verify it. Step, Result and Reason are its verification: the
// step that checked it, whether it is confirmed, dropped or undecided, and why.
type TaskFinding struct {
	Position                                          int64
	ID, Claim, Citation, Location, Evidence, VerifyBy string
	Step, Result, Reason                              string
}

// TaskReview is one review of a task: its attempt, its position among the review gates, the gate it ran, its step,
// its verdict, and the findings it raised in position order.
type TaskReview struct {
	Attempt, Position   int64
	Gate, Step, Verdict string
	Findings            []TaskFinding
}

// TaskFindingResult is the verification of one finding: the attempt, the position of its review and its own
// position, with the step that verified it, its result and its reason.
type TaskFindingResult struct {
	Attempt, Review, Position int64
	Step, Result, Reason      string
}

// EpicTell is one tell an epic holds for its owner: its seq, its text, when it was written, and whether the owner
// has been told it.
type EpicTell struct {
	Seq  int64
	Text string
	At   time.Time
	Told bool
}

// buildOf returns the build state a stored task row holds.
func buildOf(stored sqlcgen.EpicTask) TaskBuild {
	return TaskBuild{
		Stage: stored.Stage, Base: stored.Base, Attempt: stored.Attempt, Rerun: stored.Rerun,
		NoVerdict: stored.NoVerdict, Held: stored.Held, Step: stored.Step, RedRun: stored.RedRun,
		GreenRun: stored.GreenRun, Revision: stored.Revision, Failure: stored.Failure, Review: stored.Review,
		Refs: stored.Refs, GatePos: stored.GatePos, FindingPos: stored.FindingPos,
	}
}

// findingsKey names the review a finding belongs to: its task, its attempt and its review's position.
type findingsKey struct {
	task, attempt, review int64
}

// taskReviewsOf returns the reviews of every task keyed by task seq, each task's in attempt then position order, and
// each review's findings in position order. The rows come in the order the review and finding queries give.
func taskReviewsOf(reviews []sqlcgen.EpicTaskReview, findings []sqlcgen.EpicTaskFinding) map[int64][]TaskReview {
	findingsOf := make(map[findingsKey][]TaskFinding)
	for _, f := range findings {
		key := findingsKey{task: f.Task, attempt: f.Attempt, review: f.Review}
		findingsOf[key] = append(findingsOf[key], TaskFinding{
			Position: f.Position, ID: f.ID, Claim: f.Claim, Citation: f.Citation, Location: f.Location,
			Evidence: f.Evidence, VerifyBy: f.VerifyBy, Step: f.Step, Result: f.Result, Reason: f.Reason,
		})
	}
	out := make(map[int64][]TaskReview)
	for _, r := range reviews {
		out[r.Task] = append(out[r.Task], TaskReview{
			Attempt: r.Attempt, Position: r.Position, Gate: r.Gate, Step: r.Step, Verdict: r.Verdict,
			Findings: findingsOf[findingsKey{task: r.Task, attempt: r.Attempt, review: r.Position}],
		})
	}
	return out
}

// setEpicTaskBuildParams returns the parameters of the write that stores to as the build of task seq of epic, when
// the stored build is from.
func setEpicTaskBuildParams(epic string, seq int64, from, to TaskBuild) sqlcgen.SetEpicTaskBuildParams {
	return sqlcgen.SetEpicTaskBuildParams{
		ToStage: to.Stage, ToBase: to.Base, ToAttempt: to.Attempt, ToRerun: to.Rerun,
		ToNoVerdict: to.NoVerdict, ToHeld: to.Held, ToStep: to.Step, ToRedRun: to.RedRun,
		ToGreenRun: to.GreenRun, ToRevision: to.Revision, ToFailure: to.Failure, ToReview: to.Review,
		ToRefs: to.Refs, ToGatePos: to.GatePos, ToFindingPos: to.FindingPos,
		Epic: epic, Seq: seq,
		FromStage: from.Stage, FromBase: from.Base, FromAttempt: from.Attempt, FromRerun: from.Rerun,
		FromNoVerdict: from.NoVerdict, FromHeld: from.Held, FromStep: from.Step, FromRedRun: from.RedRun,
		FromGreenRun: from.GreenRun, FromRevision: from.Revision, FromFailure: from.Failure,
		FromReview: from.Review, FromRefs: from.Refs, FromGatePos: from.GatePos, FromFindingPos: from.FindingPos,
	}
}

// SetTaskBuild stores to as the build of task seq of epic in one compare-and-set write: it writes only when the task
// is open and its stored build equals from. A task that is not stored, is not open, or whose build differs from from
// is ErrTaskMoved and writes nothing.
func (s *Store) SetTaskBuild(ctx context.Context, epic string, seq int64, from, to TaskBuild) error {
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		rows, err := queries.SetEpicTaskBuild(ctx, setEpicTaskBuildParams(epic, seq, from, to))
		if err != nil {
			return fmt.Errorf("set build of task %d of epic %s: %w", seq, epic, err)
		}
		if rows == 0 {
			return fmt.Errorf("task %d of epic %s: %w", seq, epic, ErrTaskMoved)
		}
		return nil
	})
}

// BuildEpics returns the ids, ordered by id, of the epics that have a base, are not in an ended phase (met_at_base,
// clean, ended_with_gaps, released, done or stopped), and hold at least one open task.
func (s *Store) BuildEpics(ctx context.Context) ([]string, error) {
	ids, err := sqlcgen.New(s.db).ListBuildEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list build epics: %w", err)
	}
	return ids, nil
}
