package store

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// sendBackEpic is the epic the send-back tests plan, with tasks 1 and 2 planned, and sendBackBase is the stored base it
// is created at.
const (
	sendBackEpic   = "orrery-7c8d9e0/task-send-back"
	sendBackBase   = "fedcba9876543210fedcba9876543210fedcba98"
	sendBackBatch  = "plan/integrate"
	sendBackReason = "the task's commit breaks the store's ordering"
)

// sendBackAt is the second the send-back tests carry their actions out at.
func sendBackAt() time.Time {
	return time.Unix(1700003000, 0)
}

// sendBackStore returns a store whose sendBackEpic is created at sendBackBase with turn 1 planned: tasks 1 and 2 are
// open, with no build.
func sendBackStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s := openEpicStore(t)
	row := fullEpic(sendBackEpic)
	row.Base = sendBackBase
	createEpic(t, s, row)
	turn, err := s.ClaimTurn(ctx, sendBackEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, sendBackEpic, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	return s
}

// sendBackDone stores build as the build of task seq of sendBackEpic and accepts the task done.
func sendBackDone(t *testing.T, s *Store, seq int64, build TaskBuild) {
	t.Helper()
	ctx := context.Background()
	if err := s.SetTaskBuild(ctx, sendBackEpic, seq, TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild of task %d: %v", seq, err)
	}
	if err := s.AcceptTask(ctx, sendBackEpic, seq, build, "reviewed"); err != nil {
		t.Fatalf("AcceptTask of task %d: %v", seq, err)
	}
}

// sendBackAction stores input as the pending action of turn of sendBackEpic and returns it.
func sendBackAction(t *testing.T, s *Store, turn int64, input EpicActionInput) PlannerAction {
	t.Helper()
	ctx := context.Background()
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return insertActions(ctx, queries, sendBackEpic, turn, []EpicActionInput{input})
	}); err != nil {
		t.Fatalf("insert action of turn %d: %v", turn, err)
	}
	for _, action := range pendingActionsOf(t, s, sendBackEpic) {
		if action.Turn == turn {
			return action
		}
	}
	t.Fatalf("no pending action of turn %d of epic %s", turn, sendBackEpic)
	return PlannerAction{}
}

// sendBackCarry carries out action at sendBackAt and fails the test on error.
func sendBackCarry(t *testing.T, s *Store, action PlannerAction) {
	t.Helper()
	if _, err := s.CarryOutAction(context.Background(), action, sendBackAt()); err != nil {
		t.Fatalf("CarryOutAction of action %d of turn %d: %v", action.Position, action.Turn, err)
	}
}

// sendBackInput is the send_back action of task 1, with sendBackReason.
func sendBackInput() EpicActionInput {
	return EpicActionInput{Kind: ActionSendBack, Task: 1, Reason: sendBackReason}
}

// sendBackFail carries out a fail_task of task 1 with reason, stored as the action of turn.
func sendBackFail(t *testing.T, s *Store, turn int64, reason string) {
	t.Helper()
	sendBackCarry(t, s, sendBackAction(t, s, turn, EpicActionInput{Kind: ActionFailTask, Task: 1, Reason: reason}))
}

// sendBackTask returns task seq of sendBackEpic as the store reads it.
func sendBackTask(t *testing.T, s *Store, seq int64) EpicTask {
	t.Helper()
	for _, task := range closeRow(t, s, sendBackEpic).Tasks {
		if task.Seq == seq {
			return task
		}
	}
	t.Fatalf("epic %s has no task %d", sendBackEpic, seq)
	return EpicTask{}
}

// sendBackRun starts the integrate run sendBackBatch of sendBackEpic, running.
func sendBackRun(t *testing.T, s *Store) {
	t.Helper()
	run := NodeRun{Path: sendBackBatch, ID: "id-" + sendBackBatch, Node: "integrate", Kind: integrateKind, Attempt: 1}
	if err := s.StartNodes(context.Background(), sendBackEpic, []NodeRun{run}, nil, sendBackAt()); err != nil {
		t.Fatalf("StartNodes of run %s: %v", sendBackBatch, err)
	}
}

// sendBackResult returns the batch result of a run that composed each of seqs into a commit.
func sendBackResult(t *testing.T, seqs ...int64) string {
	t.Helper()
	type commit struct {
		Task   int64  `json:"task"`
		Commit string `json:"commit"`
	}
	composed := make([]commit, 0, len(seqs))
	for _, seq := range seqs {
		composed = append(composed, commit{Task: seq, Commit: fmt.Sprintf("commit-%d", seq)})
	}
	encoded, err := json.Marshal(map[string]any{"composed": composed, "leftOut": []string{}})
	if err != nil {
		t.Fatalf("encode batch result: %v", err)
	}
	return string(encoded)
}

// sendBackClaim claims tasks of the run sendBackBatch at revision, with a result that composed each of composed.
func sendBackClaim(t *testing.T, s *Store, revision string, composed []int64, tasks ...int64) {
	t.Helper()
	if err := s.ClaimBatch(context.Background(), sendBackEpic, sendBackBatch, 1, revision, "refs",
		sendBackResult(t, composed...), tasks); err != nil {
		t.Fatalf("ClaimBatch of run %s: %v", sendBackBatch, err)
	}
}

// sendBackEnd ends the run sendBackBatch with outcome, and candidate over the stored base over, as a batch ends.
func sendBackEnd(t *testing.T, s *Store, outcome, candidate, over string) {
	t.Helper()
	end := BatchEnd{Path: sendBackBatch, Attempt: 1, Outcome: outcome, Candidate: candidate, Over: over}
	if err := s.EndBatch(context.Background(), sendBackEpic, end, nil, sendBackAt()); err != nil {
		t.Fatalf("EndBatch of run %s: %v", sendBackBatch, err)
	}
}

// sendBackRungs returns the rungs of task 1 that a send_back moved onto rung 1 with reason at sendBackAt.
func sendBackRungs(reason string) []TaskRung {
	return []TaskRung{{
		Rung: 1, Kind: RungAttempt, MovedBy: MovedBySendBack, Cause: reason, Created: sendBackAt(),
	}}
}

// sendBackRequireReopened fails unless task 1 of sendBackEpic is open on implement at attempt 1 with base as its base
// and sendBackReason as its failure, holds no batch, and carries the send_back version and rung for that reason.
func sendBackRequireReopened(t *testing.T, s *Store, base string) {
	t.Helper()
	task := sendBackTask(t, s, 1)
	if task.Status != "open" {
		t.Errorf("status = %q, want open", task.Status)
	}
	reason := sendBackReason
	want := TaskBuild{Stage: "implement", Base: base, Attempt: 1, Failure: reason}
	if !reflect.DeepEqual(task.Build, want) {
		t.Errorf("build = %+v, want %+v", task.Build, want)
	}
	if task.Batch != "" {
		t.Errorf("batch = %q, want none", task.Batch)
	}
	if !reflect.DeepEqual(task.Rungs, sendBackRungs(reason)) {
		t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, sendBackRungs(reason))
	}
	if len(task.Versions) == 0 {
		t.Errorf("versions = none, want a send_back version")
		return
	}
	last := task.Versions[len(task.Versions)-1]
	if last.Source != SourceSendBack || last.Reason != sendBackReason || last.State != VersionApplied ||
		len(last.Changed()) != 0 {
		t.Errorf("last version = %+v, want an applied send_back version that changes no field", last)
	}
}

// TestSendBackDoneTaskWithNoBatchReopens checks that a send_back of a done task no batch claimed reopens it at implement
// on rung 1 of the stored base, with its reason as the failure, a version and a rung moved by send_back, and the action
// taken. The epic's candidate and the other task stay as they were.
func TestSendBackDoneTaskWithNoBatchReopens(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	action := sendBackAction(t, s, 1, sendBackInput())

	sendBackCarry(t, s, action)

	sendBackRequireReopened(t, s, sendBackBase)
	if state := ladderFailTaskState(t, s, action); state != "taken" {
		t.Errorf("action state = %q, want taken", state)
	}
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "" {
		t.Errorf("candidate = %q, want none", got)
	}
}

// TestSendBackTaskOfAFailedBatchDropsItsClaimAndTheCandidate checks that a send_back of a task whose commit a failed
// batch composed, and the stored base does not hold, clears the task's batch claim, drops the epic's candidate that
// holds the commit, and reopens the task on the stored base. The other task the failed batch composed keeps its claim,
// so the next batch composes it again from the stored base.
func TestSendBackTaskOfAFailedBatchDropsItsClaimAndTheCandidate(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	sendBackDone(t, s, 2, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-2"})
	sendBackRun(t, s)
	sendBackClaim(t, s, "rev-cand", []int64{1, 2}, 1, 2)
	sendBackEnd(t, s, "failed", "cand-1", sendBackBase)
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "cand-1" {
		t.Fatalf("candidate before the send_back = %q, want cand-1", got)
	}

	sendBackCarry(t, s, sendBackAction(t, s, 1, sendBackInput()))

	sendBackRequireReopened(t, s, sendBackBase)
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "" {
		t.Errorf("candidate = %q, want none: the candidate held the sent-back commit", got)
	}
	other := sendBackTask(t, s, 2)
	if other.Status != "done" || other.Batch != "plan/integrate" {
		t.Errorf("task 2 = status %q batch %q, want done and still claimed by the failed batch",
			other.Status, other.Batch)
	}
}

// TestSendBackTaskInTheStoredBaseCorrectsOnTheNewBase checks that a send_back of a task its claiming batch passed with
// the task in it reopens the task on the stored base the batch moved to, not the base it was built on, and clears its
// batch claim, so the task's correction is claimed by the next batch. The epic's candidate is kept, since the base holds
// the task's commit.
func TestSendBackTaskInTheStoredBaseCorrectsOnTheNewBase(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	sendBackRun(t, s)
	sendBackClaim(t, s, "rev-passed", []int64{1}, 1)
	sendBackEnd(t, s, "passed", "cand-2", "rev-passed")
	base := closeRow(t, s, sendBackEpic).StoredBase
	if base != "rev-passed" {
		t.Fatalf("stored base after the passed batch = %q, want rev-passed", base)
	}
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "cand-2" {
		t.Fatalf("candidate after the passed batch = %q, want cand-2", got)
	}

	sendBackCarry(t, s, sendBackAction(t, s, 1, sendBackInput()))

	sendBackRequireReopened(t, s, base)
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "cand-2" {
		t.Errorf("candidate = %q, want cand-2 kept: the stored base holds the sent-back commit", got)
	}
}

// TestSendBackLeftOutOfAPassedBatchIsNotInTheBase checks that a task its claiming batch passed without composing, since
// the batch left it out, is not in the stored base the batch moved to: its send_back clears the epic's candidate as a
// task whose commit the base lacks has it cleared.
func TestSendBackLeftOutOfAPassedBatchIsNotInTheBase(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	sendBackRun(t, s)
	sendBackClaim(t, s, "rev-passed", nil, 1)
	sendBackEnd(t, s, "passed", "cand-2", "rev-passed")
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "cand-2" {
		t.Fatalf("candidate after the passed batch = %q, want cand-2", got)
	}

	sendBackCarry(t, s, sendBackAction(t, s, 1, sendBackInput()))

	sendBackRequireReopened(t, s, "rev-passed")
	if got := closeRow(t, s, sendBackEpic).Candidate; got != "" {
		t.Errorf("candidate = %q, want none: the task's commit is not in the stored base", got)
	}
}

// TestSendBackRefusedWhileAClaimingRunRuns checks that a send_back is refused, and writes nothing, while a running
// integrate run claims the task or composes it into its candidate, since that run would carry the task's commit onward.
func TestSendBackRefusedWhileAClaimingRunRuns(t *testing.T) {
	t.Parallel()
	t.Run("claimed by a running batch", func(t *testing.T) {
		t.Parallel()
		s := sendBackStore(t)
		sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
		sendBackRun(t, s)
		sendBackClaim(t, s, "rev-cand", []int64{1}, 1)
		action := sendBackAction(t, s, 1, sendBackInput())

		sendBackCarry(t, s, action)

		requireRefusedAction(t, s, action, "send back task 1", "task 1 of epic "+sendBackEpic+
			" is in running integrate run plan/integrate")
		if task := sendBackTask(t, s, 1); task.Status != "done" || task.Batch != "plan/integrate" {
			t.Errorf("task 1 = status %q batch %q, want done and claimed by the running batch", task.Status, task.Batch)
		}
	})
	t.Run("composed by a running batch without claiming it", func(t *testing.T) {
		t.Parallel()
		s := sendBackStore(t)
		sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
		sendBackRun(t, s)
		sendBackClaim(t, s, "rev-cand", []int64{1})
		action := sendBackAction(t, s, 1, sendBackInput())

		sendBackCarry(t, s, action)

		requireRefusedAction(t, s, action, "send back task 1", "task 1 of epic "+sendBackEpic+
			" is in running integrate run plan/integrate")
	})
}

// TestSendBackRefusedForAnEndedEpic checks that a send_back on an epic in an ended phase is refused and writes nothing.
func TestSendBackRefusedForAnEndedEpic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	if _, err := s.fixtureExec(ctx, `UPDATE epics SET phase = 'done' WHERE id = ?`, sendBackEpic); err != nil {
		t.Fatalf("end epic %s: %v", sendBackEpic, err)
	}
	action := sendBackAction(t, s, 1, sendBackInput())

	sendBackCarry(t, s, action)

	requireRefusedAction(t, s, action, "send back task 1", "epic "+sendBackEpic+" is done, an ended phase")
	if task := sendBackTask(t, s, 1); task.Status != "done" || len(task.Rungs) != 0 {
		t.Errorf("task 1 = status %q rungs %+v, want done with no rung", task.Status, task.Rungs)
	}
}

// sendBackOpen stores build as the build of open task seq of sendBackEpic.
func sendBackOpen(t *testing.T, s *Store, seq int64, build TaskBuild) {
	t.Helper()
	if err := s.SetTaskBuild(context.Background(), sendBackEpic, seq, TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild of task %d: %v", seq, err)
	}
}

// TestSendBackRestartsTheLadderAtRungOne checks that a send_back of a task on rung 3 replaces its ladder with a rung 1
// moved by send_back, and that the ladder then climbs normally: the next fail_task writes the outcome of rung 1 and the
// rung 2 it climbs to.
func TestSendBackRestartsTheLadderAtRungOne(t *testing.T) {
	t.Parallel()
	const first, second = "the first failure", "the second failure"
	s := sendBackStore(t)
	sendBackOpen(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Step: "step-1"})
	sendBackFail(t, s, 1, first)
	sendBackFail(t, s, 2, first)
	if rung := sendBackTask(t, s, 1).Build.Attempt; rung != 3 {
		t.Fatalf("attempt before the send_back = %d, want rung 3", rung)
	}

	sendBackCarry(t, s, sendBackAction(t, s, 3, sendBackInput()))

	sendBackRequireReopened(t, s, sendBackBase)
	sendBackFail(t, s, 4, second)
	at := sendBackAt()
	want := []TaskRung{
		{
			Rung: 1, Kind: RungAttempt, MovedBy: MovedBySendBack, Cause: sendBackReason, Created: at,
			Outcome: RungFailed, Reason: second, Ended: at,
		},
		{Rung: 2, Kind: RungAttempt, MovedBy: MovedByFailTask, Cause: second, Created: at},
	}
	if got := sendBackTask(t, s, 1).Rungs; !reflect.DeepEqual(got, want) {
		t.Errorf("rungs after the fail_task:\n got %+v\nwant %+v", got, want)
	}
}

// TestSendBackFromAGapClimbsAgainAndWakesThePlannerAgain checks that a send_back of a task that ended as a gap reopens
// it, and that its next gap, after a climb from rung 1 again, adds the gap trigger under a new id, since the trigger of
// its first gap is stored.
func TestSendBackFromAGapClimbsAgainAndWakesThePlannerAgain(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackOpen(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: LastRung, Step: "step-6"})
	sendBackFail(t, s, 1, "the first gap")
	if task := sendBackTask(t, s, 1); task.Status != TaskGap {
		t.Fatalf("status after the gap = %q, want %q", task.Status, TaskGap)
	}
	sendBackCarry(t, s, sendBackAction(t, s, 2, sendBackInput()))
	for turn := int64(3); turn <= 7; turn++ {
		sendBackFail(t, s, turn, "climb again")
	}

	if task := sendBackTask(t, s, 1); task.Status != TaskGap {
		t.Fatalf("status after the second gap = %q, want %q", task.Status, TaskGap)
	}
	state, cause := triggerRow(t, s, sendBackEpic, "T1-gap-2")
	if state != triggerPending || cause != "task T1 ended as a gap: climb again" {
		t.Errorf("trigger T1-gap-2 = state %q cause %q, want pending with the second gap's reason", state, cause)
	}
	if state, _ := triggerRow(t, s, sendBackEpic, "T1-gap"); state != triggerPending {
		t.Errorf("trigger T1-gap = state %q, want the first gap's trigger kept", state)
	}
}

// TestSendBackStandingApproachEndsAtTheReopen checks that a change of approach stored before a send_back does not stand
// after it: when the reopened task climbs onto rung 4 again, the planner writes a new change of approach, which is taken.
func TestSendBackStandingApproachEndsAtTheReopen(t *testing.T) {
	t.Parallel()
	s := sendBackStore(t)
	sendBackDone(t, s, 1, TaskBuild{Stage: "implement", Base: sendBackBase, Attempt: 1, Revision: "rev-1"})
	sendBackCarry(t, s, sendBackAction(t, s, 2, sendBackInput()))
	for turn := int64(3); turn <= 5; turn++ {
		sendBackFail(t, s, turn, "climb to the approach")
	}
	approach := sendBackAction(t, s, 6, EpicActionInput{
		Kind: ActionChangeApproach, Task: 1, Deliverable: "the first recarve", Reason: "the first approach",
	})
	sendBackCarry(t, s, approach)
	if state := ladderFailTaskState(t, s, approach); state != "taken" {
		t.Fatalf("first change of approach = %q, want taken", state)
	}
	sendBackCarry(t, s, sendBackAction(t, s, 7, sendBackInput()))
	for turn := int64(8); turn <= 10; turn++ {
		sendBackFail(t, s, turn, "climb to the approach again")
	}

	again := sendBackAction(t, s, 11, EpicActionInput{
		Kind: ActionChangeApproach, Task: 1, Deliverable: "the second recarve", Reason: "the second approach",
	})
	sendBackCarry(t, s, again)

	if state := ladderFailTaskState(t, s, again); state != "taken" {
		t.Errorf("second change of approach = %q, want taken after the send_back", state)
	}
}
