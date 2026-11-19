package store

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The kinds a rung of a task's ladder can be. A rung is the task build's Attempt number: rungs 1 and 2 are RungAttempt,
// 3 is RungRescue, 4 is RungOrchestrator, 5 and 6 are RungApproach, and 7 is RungGap.
const (
	RungAttempt      = "attempt"
	RungRescue       = "rescue"
	RungOrchestrator = "orchestrator"
	RungApproach     = "approach"
	RungGap          = "gap"
)

// The values of a rung's moved_by: the kind of failure that moved the task onto the rung. Rung 1 has none.
const (
	MovedByUnit         = "unit"
	MovedByFinding      = "finding"
	MovedByFailDecision = "fail_decision"
	MovedByCannotMeet   = "cannot_meet"
	MovedByFailTask     = "fail_task"
)

// The outcomes a rung can end with. RungGapped is the outcome 'gap', the same word as the kind RungGap.
const (
	RungFailed   = "failed"
	RungAccepted = "accepted"
	RungGapped   = "gap"
)

// TaskGap is the status of a task that ended as a gap.
const TaskGap = "gap"

// taskOpen is the status of a task that is open, the only status a planner's fail_task acts on.
const taskOpen = "open"

// RescueRung is rung 3, the rung of kind RungRescue.
const RescueRung = 3

// OrchestratorRung is rung 4, the rung of kind RungOrchestrator.
const OrchestratorRung = 4

// ApproachRung is rung 5, the first rung of kind RungApproach: a task that climbs onto it needs a change of approach.
const ApproachRung = 5

// LastRung is rung 6, the highest rung a task builds on: a climb past it ends the task as a gap.
const LastRung = 6

// GapRung is rung 7, the rung of kind RungGap, which a task ends on as a gap.
const GapRung = 7

// RungMove is the failure that moves a task off its rung: MovedBy is the kind of failure, Cause its text, and Step the
// step it came from. To is the rung to move to, and 0 means the next rung. Gate and Assertion are the review gate and
// the claim of the confirmed finding that moved the task, and are empty when no finding did. NeedsFiles are the files
// the failure named as needed beyond the task's owned list, which a climb stores as the task's needs files.
type RungMove struct {
	MovedBy, Cause, Gate, Assertion, Step string
	To                                    int64
	NeedsFiles                            []string
}

// TaskRung is one rung of a task's ladder: its number, its kind, the failure that moved the task onto it, and when the
// rung was written. Outcome and Reason are the rung's outcome, and Ended when it was written. Outcome and Reason are
// empty and Ended is zero while the rung has no outcome. The failure fields are empty for rung 1.
type TaskRung struct {
	Rung      int64
	Kind      string
	MovedBy   string
	Cause     string
	Gate      string
	Assertion string
	Step      string
	Created   time.Time
	Outcome   string
	Reason    string
	Ended     time.Time
}

// taskRungsOf returns the rungs each task holds, keyed by task, each task's in the rung order of rows. A task with no
// rung has no entry.
func taskRungsOf(rows []sqlcgen.ListEpicTaskRungsRow) map[int64][]TaskRung {
	out := map[int64][]TaskRung{}
	for _, row := range rows {
		out[row.Task] = append(out[row.Task], taskRungOf(row))
	}
	return out
}

// taskRungOf returns the rung a stored row holds, with its outcome when it has one.
func taskRungOf(row sqlcgen.ListEpicTaskRungsRow) TaskRung {
	rung := TaskRung{
		Rung: row.Rung, Kind: row.Kind, MovedBy: row.MovedBy, Cause: row.Cause, Gate: row.Gate,
		Assertion: row.Assertion, Step: row.Step, Created: time.Unix(row.Created, 0),
		Outcome: row.Outcome.String, Reason: row.Reason.String,
	}
	if row.Ended.Valid {
		rung.Ended = time.Unix(row.Ended.Int64, 0)
	}
	return rung
}

// RungOf returns the rung build is on: its Attempt, or 1 when Attempt is 0, since a build in red before its first
// attempt is on rung 1.
func RungOf(build TaskBuild) int64 {
	if build.Attempt == 0 {
		return 1
	}
	return build.Attempt
}

// ClimbCause returns the cause a climb records for a failure: cause cut to TaskDecisionMax bytes when decided is true,
// as an orchestrator's decision that failed the task keeps it, and to TurnReasonMax bytes otherwise, as a failure of a
// unit keeps it.
func ClimbCause(cause string, decided bool) string {
	if decided {
		return capText(cause, TaskDecisionMax)
	}
	return capTurnText(cause)
}

// RungKind returns the kind of rung: RungAttempt for rungs 1 and 2, RungRescue for RescueRung, RungOrchestrator for
// OrchestratorRung, RungApproach for the rungs up to LastRung, and RungGap for GapRung and above.
func RungKind(rung int64) string {
	switch {
	case rung < RescueRung:
		return RungAttempt
	case rung == RescueRung:
		return RungRescue
	case rung == OrchestratorRung:
		return RungOrchestrator
	case rung <= LastRung:
		return RungApproach
	default:
		return RungGap
	}
}

// ClimbedBuild returns the build a task holds on rung after it climbs there from from, failed by failure. The build is
// on rung, in stage red when from was in red and in implement otherwise, and its failure is failure. Its rerun,
// no-verdict, held, step, revision, refs, green run and review are cleared, as are its gate and finding positions and
// its question. Base, RedRun, Decisions, After and Guard are kept.
func ClimbedBuild(from TaskBuild, rung int64, failure string) TaskBuild {
	to := from
	to.Attempt = rung
	if from.Stage == "red" {
		to.Stage = "red"
	} else {
		to.Stage = "implement"
	}
	to.Rerun, to.NoVerdict = 0, 0
	to.Held, to.Step, to.Revision, to.Refs, to.GreenRun, to.Review = "", "", "", "", "", ""
	to.GatePos, to.FindingPos, to.Question = 0, 0, 0
	to.Failure = failure
	return to
}

// ClimbTask climbs task seq of epic off its rung in one write, when its stored build is from. The rung the task is on
// is RungOf(from), and the rung it climbs to is move.To, or the rung after that when move.To is 0. The climb ends the
// rung the task is on as failed with move.Cause, adds the rung it climbs to moved by move, and stores to as the task's
// build, which must be on that rung: its Attempt is the rung, or GapRung when the rung is past it. move.NeedsFiles are
// stored as the task's needs files, so a climb with none stores none. A climb onto GapRung also ends that rung as
// gapped, sets the task's status to TaskGap, and adds a pending trigger `T<seq>-gap` with cause
// `task T<seq> ended as a gap: <cause>`. A climb onto ApproachRung adds a pending trigger `T<seq>-approach` with cause
// `task T<seq> needs a change of approach: <cause>`. version, when not nil, is stored as the task's next version,
// applied by decision's id, and decision, when not nil, is appended to the epic's decisions, with its refused files
// stored as refused by its id. A target at or below RungOf(from), a to that is not on the target capped at GapRung,
// and a move whose MovedBy is not one of the MovedBy values are errors that write nothing. A task that is not open, or
// whose stored build is not from, is ErrTaskMoved and writes nothing.
func (s *Store) ClimbTask(
	ctx context.Context,
	epic string,
	seq int64,
	from, to TaskBuild,
	move RungMove,
	version *TaskVersionInput,
	decision *TaskDecision,
	at time.Time,
) error {
	if err := checkClimb(epic, seq, from, to, move); err != nil {
		return err
	}
	return s.write(ctx, func(queries *sqlcgen.Queries) error {
		return climbTask(ctx, queries, epic, seq, from, to, move, version, decision, at)
	})
}

// climbTask writes the climb ClimbTask describes for task seq of epic in the caller's write, so a planner action climbs
// its task in the write that carries it out. It does not run checkClimb, which the caller runs first. The stored build
// moves from from to to before anything else is written, so a task that is not open, or whose stored build is not from,
// is ErrTaskMoved, and the caller's write carries that error out with nothing stored. The rest is as ClimbTask gives
// it: move's needs files, the rungs of the climb, what the climb onto to ends, and version and decision when not nil.
func climbTask(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq int64,
	from, to TaskBuild,
	move RungMove,
	version *TaskVersionInput,
	decision *TaskDecision,
	at time.Time,
) error {
	if err := moveTask(ctx, queries, epic, seq, from, to); err != nil {
		return err
	}
	if err := setTaskNeedsFiles(ctx, queries, epic, seq, move.NeedsFiles); err != nil {
		return err
	}
	if err := insertClimbRungs(ctx, queries, epic, seq, RungOf(from), to.Attempt, move, at); err != nil {
		return err
	}
	if err := insertClimbEnd(ctx, queries, epic, seq, to.Attempt, move.Cause, at); err != nil {
		return err
	}
	decisionID := ""
	if decision != nil {
		decisionID = decision.ID
	}
	var granted []string
	if version != nil {
		if err := appendTaskVersion(ctx, queries, epic, seq, decisionID, *version, at); err != nil {
			return err
		}
		granted = version.Files
	}
	var refusals TaskDecision
	if decision != nil {
		refusals = *decision
	}
	if err := storeRefusals(ctx, queries, epic, seq, refusals, granted, at); err != nil {
		return err
	}
	if decision == nil {
		return nil
	}
	_, err := appendDecision(ctx, queries, epic, decision.ID, decision.Text, 0, at)
	return err
}

// checkClimb returns an error when a climb from from with move cannot land as it names: when its target rung is not
// above from's rung, RungOf(from), when to is not on the target capped at GapRung, or when move's MovedBy is not a
// MovedBy value.
func checkClimb(epic string, seq int64, from, to TaskBuild, move RungMove) error {
	rung := RungOf(from)
	target := rung + 1
	if move.To != 0 {
		target = move.To
	}
	if target <= rung {
		return fmt.Errorf("climb task %d of epic %s to rung %d from rung %d: target not above it",
			seq, epic, target, rung)
	}
	if want := min(target, GapRung); to.Attempt != want {
		return fmt.Errorf("climb task %d of epic %s: build on rung %d, want rung %d", seq, epic, to.Attempt, want)
	}
	if !knownMovedBy(move.MovedBy) {
		return fmt.Errorf("climb task %d of epic %s: unknown moved_by %q", seq, epic, move.MovedBy)
	}
	return nil
}

// knownMovedBy reports whether movedBy is one of the MovedBy values.
func knownMovedBy(movedBy string) bool {
	switch movedBy {
	case MovedByUnit, MovedByFinding, MovedByFailDecision, MovedByCannotMeet, MovedByFailTask:
		return true
	default:
		return false
	}
}

// insertClimbRungs writes, in the caller's write, the row of rung from of task seq with its outcome failed for move's
// cause, and the row of rung to that move moved the task onto. A row or outcome already stored is left alone.
func insertClimbRungs(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq, from, to int64,
	move RungMove,
	at time.Time,
) error {
	if _, err := queries.InsertEpicTaskRung(ctx, sqlcgen.InsertEpicTaskRungParams{
		Epic: epic, Task: seq, Rung: from, Kind: RungKind(from), Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert rung %d of task %d of epic %s: %w", from, seq, epic, err)
	}
	if _, err := queries.InsertEpicTaskRungOutcome(ctx, sqlcgen.InsertEpicTaskRungOutcomeParams{
		Epic: epic, Task: seq, Rung: from, Outcome: RungFailed, Reason: move.Cause, Ended: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert outcome of rung %d of task %d of epic %s: %w", from, seq, epic, err)
	}
	if _, err := queries.InsertEpicTaskRung(ctx, sqlcgen.InsertEpicTaskRungParams{
		Epic: epic, Task: seq, Rung: to, Kind: RungKind(to), MovedBy: move.MovedBy, Cause: move.Cause,
		Gate: move.Gate, Assertion: move.Assertion, Step: move.Step, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert rung %d of task %d of epic %s: %w", to, seq, epic, err)
	}
	return nil
}

// insertClimbEnd writes, in the caller's write, what a climb onto rung ends the task with: a climb onto GapRung ends
// that rung as gapped, the task as a gap, and adds the gap trigger, and a climb onto ApproachRung adds the approach
// trigger. Any other rung ends nothing.
func insertClimbEnd(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic string,
	seq, rung int64,
	cause string,
	at time.Time,
) error {
	switch rung {
	case GapRung:
		if _, err := queries.InsertEpicTaskRungOutcome(ctx, sqlcgen.InsertEpicTaskRungOutcomeParams{
			Epic: epic, Task: seq, Rung: rung, Outcome: RungGapped, Reason: cause, Ended: at.Unix(),
		}); err != nil {
			return fmt.Errorf("insert outcome of rung %d of task %d of epic %s: %w", rung, seq, epic, err)
		}
		if err := setTaskStatus(ctx, queries, epic, seq, TaskGap); err != nil {
			return err
		}
		return insertClimbTrigger(ctx, queries, epic, fmt.Sprintf("T%d-gap", seq),
			fmt.Sprintf("task T%d ended as a gap: %s", seq, cause), at)
	case ApproachRung:
		return insertClimbTrigger(ctx, queries, epic, fmt.Sprintf("T%d-approach", seq),
			fmt.Sprintf("task T%d needs a change of approach: %s", seq, cause), at)
	default:
		return nil
	}
}

// insertClimbTrigger adds a pending trigger with id and cause to epic, in the caller's write.
func insertClimbTrigger(
	ctx context.Context,
	queries *sqlcgen.Queries,
	epic, id, cause string,
	at time.Time,
) error {
	if err := queries.InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: epic, ID: id, Cause: cause, State: triggerPending, Turn: 0, Created: at.Unix(),
	}); err != nil {
		return fmt.Errorf("insert trigger %s of epic %s: %w", id, epic, err)
	}
	return nil
}

// acceptRung ends rung of task seq of epic as accepted, in the caller's write, when the task has a row for that rung.
// The outcome's ended time is the row's own created time. A task with no row for the rung writes nothing.
func acceptRung(ctx context.Context, queries *sqlcgen.Queries, epic string, seq, rung int64) error {
	rows, err := queries.ListEpicTaskRungs(ctx, epic)
	if err != nil {
		return fmt.Errorf("read rungs of epic %s: %w", epic, err)
	}
	for _, row := range rows {
		if row.Task != seq || row.Rung != rung {
			continue
		}
		if _, err = queries.InsertEpicTaskRungOutcome(ctx, sqlcgen.InsertEpicTaskRungOutcomeParams{
			Epic: epic, Task: seq, Rung: rung, Outcome: RungAccepted, Ended: row.Created,
		}); err != nil {
			return fmt.Errorf("insert accepted outcome of rung %d of task %d of epic %s: %w", rung, seq, epic, err)
		}
	}
	return nil
}
