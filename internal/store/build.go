package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
// FindingPos are the review gate and the finding the build is on. Guard is the text of the guard's last refusal of the
// task's build, empty when it refused nothing. Decisions counts the orchestrator decisions carried out on the task.
// After is the seqs of the tasks the build waits for an integrate batch to take, joined with commas, while it is held
// waiting, and the tasks it last waited for once that hold ends; it is empty for a build that never waited. Question
// is the seq of the owner question the build waits on, 0 while it waits on none. ReBriefs holds the first line of each
// failure an orchestrator's re-brief answered on the task, each followed by a newline, which a climb keeps.
type TaskBuild struct {
	Stage, Base                            string
	Attempt, Rerun, NoVerdict              int64
	Held, Step, RedRun, GreenRun, Revision string
	Failure, Review, Refs                  string
	GatePos, FindingPos                    int64
	Guard                                  string
	Decisions                              int64
	After                                  string
	Question                               int64
	ReBriefs                               string
}

// ReBriefed reports whether the task's build has answered a re-brief on a failure whose first line is line.
func (b TaskBuild) ReBriefed(line string) bool {
	return slices.Contains(rebriefLines(b), line)
}

// WithReBrief returns b with line added to the first lines its re-briefs answered. line is the first line of a failure,
// so it holds no newline.
func (b TaskBuild) WithReBrief(line string) TaskBuild {
	b.ReBriefs += line + "\n"
	return b
}

// rebriefLines returns the first lines b's re-briefs answered, in the order they were added, or none.
func rebriefLines(b TaskBuild) []string {
	if b.ReBriefs == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(b.ReBriefs, "\n"), "\n")
}

// GateChange is one gate-needed change a task's implementer declared: a file it changed beyond what the Deliverable
// names because the task's own gate or evidence could not pass without it, and the reason the gate needs it.
type GateChange struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// TaskTestRun is one run of a task's test command: its position among the runs it belongs to, the command it ran, the
// revision it ran at, and its step and output. Step is empty while the run has no result.
type TaskTestRun struct {
	Position                int64
	Command, Revision, Step string
	Output                  string
}

// TaskFinding is one finding a review raised: its position among the review's findings, its id, the claim it makes,
// where it is cited and located, its evidence and how to verify it. Step, Result and Reason are its verification: the
// step that checked it, whether it is confirmed, dropped or undecided, and why. Unverified is whether it was settled
// as confirmed without a verification. TestRuns are the test runs that checked it, in position order.
type TaskFinding struct {
	Position                                          int64
	ID, Claim, Citation, Location, Evidence, VerifyBy string
	Step, Result, Reason                              string
	Unverified                                        bool
	TestRuns                                          []TaskTestRun
}

// TaskReview is one review of a task: its attempt, its position among the review gates, the gate it ran, its step,
// its verdict, the reason it gave no holds or fails verdict when its verdict is undetermined, the findings it raised
// in position order, and the test runs it holds in position order.
type TaskReview struct {
	Attempt, Position   int64
	Gate, Step, Verdict string
	Reason              string
	Findings            []TaskFinding
	TestRuns            []TaskTestRun
}

// TaskFindingResult is the verification of one finding: the attempt, the position of its review and its own
// position, with the step that verified it, its result and its reason.
type TaskFindingResult struct {
	Attempt, Review, Position int64
	Step, Result, Reason      string
}

// TaskTestRunResult is the result of one test run: the attempt, the position of its review and its own position, with
// the step that ran it and its output.
type TaskTestRunResult struct {
	Attempt, Review, Position int64
	Step, Output              string
}

// EpicTell is one tell an epic holds for its owner: its seq, its text, when it was written, whether the owner has been
// told it, and the question it belongs to, nil when it belongs to none.
type EpicTell struct {
	Seq      int64
	Text     string
	At       time.Time
	Told     bool
	Question *EpicQuestion
}

// buildOf returns the build state a stored task row holds.
func buildOf(stored sqlcgen.EpicTask) TaskBuild {
	return TaskBuild{
		Stage: stored.Stage, Base: stored.Base, Attempt: stored.Attempt, Rerun: stored.Rerun,
		NoVerdict: stored.NoVerdict, Held: stored.Held, Step: stored.Step, RedRun: stored.RedRun,
		GreenRun: stored.GreenRun, Revision: stored.Revision, Failure: stored.Failure, Review: stored.Review,
		Refs: stored.Refs, GatePos: stored.GatePos, FindingPos: stored.FindingPos, Guard: stored.Guard,
		Decisions: stored.Decisions, After: stored.AfterTasks, Question: stored.Question, ReBriefs: stored.Rebriefs,
	}
}

// findingsKey names the review a finding or a test run belongs to: its task, its attempt and its review's position.
type findingsKey struct {
	task, attempt, review int64
}

// taskReviewsOf returns the reviews of every task keyed by task seq, each task's in attempt then position order, each
// review's findings in position order, and each review's test runs in position order. Every finding of a review holds
// the review's test runs. The rows come in the order the review, finding and test run queries give.
func taskReviewsOf(
	reviews []sqlcgen.EpicTaskReview,
	findings []sqlcgen.EpicTaskFinding,
	runs []sqlcgen.EpicTaskTestRun,
) map[int64][]TaskReview {
	findingsOf := make(map[findingsKey][]TaskFinding)
	for _, f := range findings {
		key := findingsKey{task: f.Task, attempt: f.Attempt, review: f.Review}
		findingsOf[key] = append(findingsOf[key], TaskFinding{
			Position: f.Position, ID: f.ID, Claim: f.Claim, Citation: f.Citation, Location: f.Location,
			Evidence: f.Evidence, VerifyBy: f.VerifyBy, Step: f.Step, Result: f.Result, Reason: f.Reason,
			Unverified: f.Unverified != 0,
		})
	}
	runsOf := make(map[findingsKey][]TaskTestRun)
	for _, run := range runs {
		key := findingsKey{task: run.Task, attempt: run.Attempt, review: run.Review}
		runsOf[key] = append(runsOf[key], TaskTestRun{
			Position: run.Position, Command: run.Command, Revision: run.Revision, Step: run.Step, Output: run.Output,
		})
	}
	out := make(map[int64][]TaskReview)
	for _, r := range reviews {
		key := findingsKey{task: r.Task, attempt: r.Attempt, review: r.Position}
		testRuns := runsOf[key]
		reviewFindings := findingsOf[key]
		for i := range reviewFindings {
			reviewFindings[i].TestRuns = testRuns
		}
		out[r.Task] = append(out[r.Task], TaskReview{
			Attempt: r.Attempt, Position: r.Position, Gate: r.Gate, Step: r.Step, Verdict: r.Verdict,
			Reason:   r.Reason,
			Findings: reviewFindings,
			TestRuns: testRuns,
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
		ToRefs: to.Refs, ToGatePos: to.GatePos, ToFindingPos: to.FindingPos, ToGuard: to.Guard,
		ToDecisions: to.Decisions, ToAfterTasks: to.After, ToQuestion: to.Question, ToRebriefs: to.ReBriefs,
		Epic: epic, Seq: seq,
		FromStage: from.Stage, FromBase: from.Base, FromAttempt: from.Attempt, FromRerun: from.Rerun,
		FromNoVerdict: from.NoVerdict, FromHeld: from.Held, FromStep: from.Step, FromRedRun: from.RedRun,
		FromGreenRun: from.GreenRun, FromRevision: from.Revision, FromFailure: from.Failure,
		FromReview: from.Review, FromRefs: from.Refs, FromGatePos: from.GatePos, FromFindingPos: from.FindingPos,
		FromGuard: from.Guard, FromDecisions: from.Decisions, FromAfterTasks: from.After, FromQuestion: from.Question,
		FromRebriefs: from.ReBriefs,
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

// SetTaskBuildGateChanges stores to as the build of task seq of epic and changes as its whole list of declared
// gate-needed changes, in one compare-and-set write: it writes only when the task is open and its stored build equals
// from. A task that is not stored, is not open, or whose build differs from from is ErrTaskMoved and writes nothing.
func (s *Store) SetTaskBuildGateChanges(
	ctx context.Context, epic string, seq int64, from, to TaskBuild, changes []GateChange,
) error {
	encoded, err := encodeList(changes)
	if err != nil {
		return fmt.Errorf("encode gate changes of task %d of epic %s: %w", seq, epic, err)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if moveErr := moveTask(ctx, queries, epic, seq, from, to); moveErr != nil {
			return moveErr
		}
		if setErr := queries.SetEpicTaskGateChanges(ctx, sqlcgen.SetEpicTaskGateChangesParams{
			GateChanges: encoded, Epic: epic, Seq: seq,
		}); setErr != nil {
			return fmt.Errorf("set gate changes of task %d of epic %s: %w", seq, epic, setErr)
		}
		return nil
	})
}

// SetTaskBuildNotes stores to as the build of task seq of epic, notes as its whole list of notes and each of tells as a
// tell of epic, in one write, when the stored build is from. A task that is not open, or whose build differs from from,
// is ErrTaskMoved and writes nothing.
func (s *Store) SetTaskBuildNotes(
	ctx context.Context, epic string, seq int64, from, to TaskBuild, notes, tells []string, at time.Time,
) error {
	encoded, err := encodeList(notes)
	if err != nil {
		return fmt.Errorf("encode notes of task %d of epic %s: %w", seq, epic, err)
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		if moveErr := moveTask(ctx, queries, epic, seq, from, to); moveErr != nil {
			return moveErr
		}
		if setErr := queries.SetEpicTaskNotes(ctx, sqlcgen.SetEpicTaskNotesParams{
			Notes: encoded, Epic: epic, Seq: seq,
		}); setErr != nil {
			return fmt.Errorf("set notes of task %d of epic %s: %w", seq, epic, setErr)
		}
		return insertTells(ctx, queries, epic, tells, at)
	})
}

// BuildEpics returns the ids, ordered by id, of the epics that have a base, are not in an ended phase (met_at_base,
// clean, ended_with_gaps, released, done, stopped or one of the closed phases), and hold at least one open task.
func (s *Store) BuildEpics(ctx context.Context) ([]string, error) {
	ids, err := sqlcgen.New(s.read).ListBuildEpics(ctx)
	if err != nil {
		return nil, fmt.Errorf("list build epics: %w", err)
	}
	return ids, nil
}
