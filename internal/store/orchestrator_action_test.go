package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// actionEpic is the epic the action tests stop, resume and close, and actionOther is the epic of the release tests that
// holds no release slot. Both are in the repository of fullEpic, so one of them holds that repository's slot at a time.
const (
	actionEpic  = "orrery-1a2b3c4/epic-actions"
	actionOther = "orrery-1a2b3c4/epic-unslotted"
)

// storeActions stores actions as the pending actions of turn 1 of epic, in the write CompleteTurn stores a return's actions
// in, and returns the pending actions of epic.
func storeActions(t *testing.T, s *Store, epic string, actions ...EpicActionInput) []PlannerAction {
	t.Helper()
	ctx := context.Background()
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return insertActions(ctx, queries, epic, 1, actions)
	}); err != nil {
		t.Fatalf("insert actions of epic %s: %v", epic, err)
	}
	return pendingActionsOf(t, s, epic)
}

// pendingActionsOf returns the pending actions of epic, as PendingActions reads them.
func pendingActionsOf(t *testing.T, s *Store, epic string) []PlannerAction {
	t.Helper()
	all, err := s.PendingActions(context.Background())
	if err != nil {
		t.Fatalf("PendingActions: %v", err)
	}
	var out []PlannerAction
	for _, action := range all {
		if action.Epic == epic {
			out = append(out, action)
		}
	}
	return out
}

// decisionNamed returns the decision of epic with client id id, and fails the test when epic has none.
func decisionNamed(t *testing.T, s *Store, epic, id string) EpicDecision {
	t.Helper()
	for _, decision := range closeRow(t, s, epic).Decisions {
		if decision.ID == id {
			return decision
		}
	}
	t.Fatalf("epic %s has no decision %s", epic, id)
	return EpicDecision{}
}

// triggerRow returns the state and cause of trigger id of epic, and fails the test when no trigger has the id.
func triggerRow(t *testing.T, s *Store, epic, id string) (state, cause string) {
	t.Helper()
	err := s.db.QueryRowContext(context.Background(),
		`SELECT state, cause FROM epic_triggers WHERE epic = ? AND id = ?`, epic, id).Scan(&state, &cause)
	if err != nil {
		t.Fatalf("read trigger %s of epic %s: %v", id, epic, err)
	}
	return state, cause
}

// createStoppedRelease stores epic as a release stopped at its first release step, which failed. epic takes its
// repository's release slot while clean, and its release stops it, so it holds the slot.
func createStoppedRelease(t *testing.T, s *Store, epic string) {
	t.Helper()
	ctx := context.Background()
	at := closeAt()
	row := fullEpic(epic)
	row.ReleaseSteps = []EpicReleaseStep{
		{Step: "push tag", Target: "origin", Undo: "delete tag"},
		{Step: "publish", Target: "registry", Undo: "none"},
	}
	run := NodeRun{Path: "release", ID: "id-" + epic, Node: "release", Kind: "release", Attempt: 1}
	row.Phase = PhaseClean
	createEpic(t, s, row)
	if _, err := s.TakeReleaseSlot(ctx, epic, at); err != nil {
		t.Fatalf("TakeReleaseSlot of epic %s: %v", epic, err)
	}
	if err := s.StartNodes(ctx, epic, []NodeRun{run}, nil, at); err != nil {
		t.Fatalf("StartNodes of epic %s: %v", epic, err)
	}
	if err := s.RecordRelease(ctx, epic, ReleaseRun{
		Part: ReleasePartStep, Position: 1, Attempt: 1, Step: "push tag", Evidence: "run-push-tag",
		Reason: "rejected", At: at,
	}); err != nil {
		t.Fatalf("RecordRelease of epic %s: %v", epic, err)
	}
	if err := s.StopRelease(ctx, epic, run.Path, 1, "rejected", at); err != nil {
		t.Fatalf("StopRelease of epic %s: %v", epic, err)
	}
}

// requireRefusedAction fails unless action is recorded as refused with refusal: its row holds the refusal and is no longer
// pending, a decision holds the refusal and the planner's reason, and a pending trigger names the action and the refusal.
// summary is how the action is named.
func requireRefusedAction(t *testing.T, s *Store, action PlannerAction, summary, refusal string) {
	t.Helper()
	var state, stored string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT state, refusal FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?`,
		action.Epic, action.Turn, action.Position).Scan(&state, &stored); err != nil {
		t.Fatalf("read action %d of epic %s: %v", action.Position, action.Epic, err)
	}
	if state != "refused" || stored != refusal {
		t.Errorf("action %d of epic %s = state %q refusal %q, want refused with %q",
			action.Position, action.Epic, state, stored, refusal)
	}

	id := fmt.Sprintf("action-%d-%d-refused", action.Turn, action.Position)
	if got, want := decisionNamed(t, s, action.Epic, id).Text,
		"planner "+summary+" refused: "+refusal+". Its reason: "+action.Reason; got != want {
		t.Errorf("decision %s text = %q, want %q", id, got, want)
	}
	if gotState, cause := triggerRow(t, s, action.Epic, id); gotState != "pending" ||
		cause != "planner "+summary+" refused: "+refusal {
		t.Errorf("trigger %s = state %q cause %q, want pending with the refusal", id, gotState, cause)
	}
}

// TestOrchestratorActionRetryNodeRerunsTheParkedRun checks that a retry_node action of a parked node reruns its run at the
// next attempt, ends the park of the epic, and is recorded as a decision that holds its reason.
func TestOrchestratorActionRetryNodeRerunsTheParkedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	startIntegrateRuns(t, s,
		NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1})
	if err := s.ParkNode(ctx, integrateEpic, retryLatestBatch, 1, nodeFailedOutcome, "lint failed", ParkClassNode,
		"integrate is red", integrateParkedAt()); err != nil {
		t.Fatalf("ParkNode: %v", err)
	}
	action := storeActions(t, s, integrateEpic, EpicActionInput{
		Kind: ActionRetryNode, Node: "integrate", Reason: "the lint is fixed on the candidate",
	})[0]

	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || stop != nil {
		t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
	}
	if run := integrateRun(t, s, retryLatestBatch); run.Attempt != 2 || run.State != NodeRunning {
		t.Errorf("run %s after retry_node = attempt %d state %q, want attempt 2 running",
			retryLatestBatch, run.Attempt, run.State)
	}
	if parked := integrateEpicRow(t, s).Parked; parked != "" {
		t.Errorf("parked of epic %s after retry_node = %q, want empty", integrateEpic, parked)
	}
	if got, want := decisionNamed(t, s, integrateEpic, "action-1-1").Text,
		"planner retry node integrate: the lint is fixed on the candidate"; got != want {
		t.Errorf("decision of retry_node = %q, want %q", got, want)
	}
}

// TestOrchestratorActionRetryEpicEndsThePark checks that a retry_epic action ends the park of a parked epic, and is
// recorded as a decision that holds its reason.
func TestOrchestratorActionRetryEpicEndsThePark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if err := s.ParkEpic(ctx, claimEpic, ParkClassNode, "credit ran out", completedAt()); err != nil {
		t.Fatalf("ParkEpic of %s: %v", claimEpic, err)
	}
	action := storeActions(t, s, claimEpic, EpicActionInput{Kind: ActionRetryEpic, Reason: "the credit is back"})[0]

	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if parked := closeRow(t, s, claimEpic).Parked; parked != "" {
		t.Errorf("parked of epic %s after retry_epic = %q, want empty", claimEpic, parked)
	}
	if got, want := decisionNamed(t, s, claimEpic, "action-1-1").Text,
		"planner retry epic: the credit is back"; got != want {
		t.Errorf("decision of retry_epic = %q, want %q", got, want)
	}
}

// TestOrchestratorActionRetryOnAnOwnerParkIsRefusedAndKeepsThePark checks that a retry_epic or retry_node action on an epic
// whose park the owner holds is refused and recorded as refused with its trigger, and that the park keeps its reason and
// its owner class, with its park question still open.
func TestOrchestratorActionRetryOnAnOwnerParkIsRefusedAndKeepsThePark(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		epic    string
		setup   func(t *testing.T) *Store
		action  EpicActionInput
		park    string
		summary string
		refusal string
	}{
		{
			name: "retry_epic on an epic parked as branch",
			epic: claimEpic,
			setup: func(t *testing.T) *Store {
				t.Helper()
				s := openEpicStore(t)
				createParkedEpic(t, s, claimEpic)
				return s
			},
			action:  EpicActionInput{Kind: ActionRetryEpic, Reason: "the credit is back"},
			park:    "credit ran out",
			summary: "retry epic",
			refusal: "epic " + claimEpic + " holds the owner's branch park; only the owner retries it",
		},
		{
			name: "retry_node on a node of an epic parked as branch",
			epic: integrateEpic,
			setup: func(t *testing.T) *Store {
				t.Helper()
				ctx := context.Background()
				s := openIntegrateStore(t)
				startIntegrateRuns(t, s,
					NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1})
				if err := s.ParkNode(ctx, integrateEpic, retryLatestBatch, 1, nodeFailedOutcome, "lint failed",
					ParkClassBranch, "integrate is red", integrateParkedAt()); err != nil {
					t.Fatalf("ParkNode: %v", err)
				}
				return s
			},
			action: EpicActionInput{
				Kind: ActionRetryNode, Node: "integrate", Reason: "the lint is fixed on the candidate",
			},
			park:    "integrate is red",
			summary: "retry node integrate",
			refusal: "epic " + integrateEpic + " holds the owner's branch park; only the owner retries it",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := c.setup(t)
			action := storeActions(t, s, c.epic, c.action)[0]

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, c.summary, c.refusal)

			row := closeRow(t, s, c.epic)
			if row.Parked != c.park || row.ParkClass != ParkClassBranch {
				t.Errorf("park of epic %s = %q %q, want %q %q",
					c.epic, row.ParkClass, row.Parked, ParkClassBranch, c.park)
			}
			open, err := s.OpenQuestions(ctx, c.epic)
			if err != nil {
				t.Fatalf("OpenQuestions: %v", err)
			}
			if len(open) != 1 || open[0].Label != ParkLabel || open[0].Text != c.park {
				t.Errorf("open questions of epic %s = %+v, want one park question with text %q", c.epic, open, c.park)
			}
		})
	}
}

// TestOrchestratorActionFailTaskClimbsTheOpenTask checks that a fail_task action climbs the open task its turn planned
// one rung, with the planner's reason as the cause: the task stays open on the rung after the one it was on, the rung it
// left ends failed with the reason, and the rung it climbs to is moved by fail_task. No T1-failed trigger is added. The
// open steps of that task, and no others, are returned to stop.
func TestOrchestratorActionFailTaskClimbsTheOpenTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	const reason = "the build cannot reach the store"
	plan := plannedTurn()
	plan.Actions = []EpicActionInput{{Kind: ActionFailTask, Task: 1, Reason: reason}}
	if err = s.CompleteTurn(ctx, claimEpic, turn.Seq, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	for _, step := range []StepAdd{
		{ID: "build-step", Task: "T1"}, {ID: "other-step", Task: "T2"},
	} {
		step.Agent, step.Prompt, step.Schema, step.Workspace = "builder", "build it", `{"type":"object"}`, "/ws/"+step.ID
		step.Revision, step.Worktree, step.DoneValue, step.ReadOnly = "rev-1", "/wt/"+step.ID, "DONE", true
		step.GateLimit, step.ModelLimit, step.Epic = time.Minute, time.Hour, claimEpic
		if err = s.ApplyEngine(ctx, []EngineOp{step}, closeAt()); err != nil {
			t.Fatalf("ApplyEngine adding step %s: %v", step.ID, err)
		}
	}
	action := pendingActionsOf(t, s, claimEpic)[0]
	before := closeRow(t, s, claimEpic).Tasks[0].Build
	rung := RungOf(before)

	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if want := []string{"build-step"}; !reflect.DeepEqual(stop, want) {
		t.Errorf("steps to stop = %v, want %v", stop, want)
	}
	task := closeRow(t, s, claimEpic).Tasks[0]
	if task.Status != "open" || task.Build.Attempt != rung+1 {
		t.Errorf("task 1 after fail_task = status %q attempt %d, want open at attempt %d",
			task.Status, task.Build.Attempt, rung+1)
	}
	if len(task.Rungs) != 2 {
		t.Fatalf("rungs of task 1 = %+v, want the rung left and the rung climbed to", task.Rungs)
	}
	if left := task.Rungs[0]; left.Rung != rung || left.Outcome != RungFailed || left.Reason != reason {
		t.Errorf("left rung = %+v, want rung %d ended failed with the reason", left, rung)
	}
	if climbed := task.Rungs[1]; climbed.Rung != rung+1 || climbed.MovedBy != MovedByFailTask {
		t.Errorf("climbed rung = %+v, want rung %d moved by fail_task", climbed, rung+1)
	}
	var failed int64
	if err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM epic_triggers WHERE epic = ? AND id = 'T1-failed'`, claimEpic).Scan(&failed); err != nil {
		t.Fatalf("count T1-failed triggers: %v", err)
	}
	if failed != 0 {
		t.Errorf("T1-failed triggers = %d, want none: a climb adds no failure trigger", failed)
	}
	if got, want := decisionNamed(t, s, claimEpic, "action-1-1").Text,
		"planner fail task 1: the build cannot reach the store"; got != want {
		t.Errorf("decision of fail_task = %q, want %q", got, want)
	}
}

// TestOrchestratorActionResumeReleaseResumesAStoppedReleaseHoldingItsSlot checks that a resume_release action resumes a
// release stopped at a failed step that can be undone, with no undo-none step named, and records it as a decision that
// holds its reason.
func TestOrchestratorActionResumeReleaseResumesAStoppedReleaseHoldingItsSlot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createStoppedRelease(t, s, actionEpic)
	action := storeActions(t, s, actionEpic, EpicActionInput{
		Kind: ActionResumeRelease, Reason: "the push tag can be undone and run again",
	})[0]

	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if phase := closeRow(t, s, actionEpic).Phase; phase != PhaseReleasing {
		t.Errorf("phase of epic %s after resume_release = %q, want %q", actionEpic, phase, PhaseReleasing)
	}
	if got, want := decisionNamed(t, s, actionEpic, "action-1-1").Text,
		"planner resume release: the push tag can be undone and run again"; got != want {
		t.Errorf("decision of resume_release = %q, want %q", got, want)
	}
}

// TestOrchestratorActionCloseEndsTheEpicAsEachKind checks that a close_epic action closes an epic as abandoned, with no
// replacing epic, and as superseded, with the action's by as the epic that replaces it, each closed by the planner and
// recorded as a decision that holds its reason.
func TestOrchestratorActionCloseEndsTheEpicAsEachKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, kind, by, phase, decision string
	}{
		{"abandoned", "abandoned", "", PhaseClosedAbandoned, "planner close epic as abandoned: the work is dropped"},
		{"superseded", "superseded", closeNext, PhaseClosedSuperseded,
			"planner close epic as superseded by " + closeNext + ": the work is dropped"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			createEpic(t, s, fullEpic(claimEpic))
			action := storeActions(t, s, claimEpic, EpicActionInput{
				Kind: ActionCloseEpic, Close: c.kind, By: c.by, Reason: "the work is dropped",
			})[0]

			if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
				t.Fatalf("CarryOutAction: %v", err)
			}
			row := closeRow(t, s, claimEpic)
			if row.Phase != c.phase || row.ClosedBy != CloserPlanner || row.SupersededBy != c.by {
				t.Errorf("epic after close = phase %q closed by %q superseded by %q, "+
					"want %q closed by %q superseded by %q",
					row.Phase, row.ClosedBy, row.SupersededBy, c.phase, CloserPlanner, c.by)
			}
			if got := decisionNamed(t, s, claimEpic, "action-1-1").Text; got != c.decision {
				t.Errorf("decision of close_epic = %q, want %q", got, c.decision)
			}
		})
	}
}

// TestOrchestratorActionCloseReturnsTheTurnAndTheEpicsUnendedSteps checks that a close_epic action ends the running turn,
// and returns the step of that turn and each other step of the epic with no end, once each, and no step that has an end
// or belongs to another epic.
func TestOrchestratorActionCloseReturnsTheTurnAndTheEpicsUnendedSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	turn, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	plan := plannedTurn()
	plan.Actions = []EpicActionInput{{
		Kind: ActionCloseEpic, Close: "abandoned", Reason: "the epic is no longer wanted",
	}}
	if err = s.CompleteTurn(ctx, claimEpic, turn.Seq, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	addPendingTrigger(t, s)
	next, err := s.ClaimTurn(ctx, claimEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn of the next turn: %v", err)
	}
	if err = s.SetTurnStep(ctx, claimEpic, next.Seq, "planner-step"); err != nil {
		t.Fatalf("SetTurnStep: %v", err)
	}
	for _, step := range []StepAdd{
		{ID: "planner-step", Epic: claimEpic}, {ID: "open-step", Epic: claimEpic},
		{ID: "ended-step", Epic: claimEpic}, {ID: "other-step", Epic: actionOther},
	} {
		step.Agent, step.Prompt, step.Schema, step.Workspace = "builder", "build it", `{"type":"object"}`, "/ws/"+step.ID
		step.Revision, step.Worktree, step.DoneValue, step.ReadOnly = "rev-1", "/wt/"+step.ID, "DONE", true
		step.GateLimit, step.ModelLimit = time.Minute, time.Hour
		if err = s.ApplyEngine(ctx, []EngineOp{step}, closeAt()); err != nil {
			t.Fatalf("ApplyEngine adding step %s: %v", step.ID, err)
		}
	}
	if err = s.ApplyEngine(ctx, []EngineOp{StepEnd{ID: "ended-step", End: "done", Status: "passed", Reason: "done"}},
		closeAt()); err != nil {
		t.Fatalf("ApplyEngine ending step: %v", err)
	}
	action := pendingActionsOf(t, s, claimEpic)[0]

	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	if want := []string{"planner-step", "open-step"}; !reflect.DeepEqual(stop, want) {
		t.Errorf("steps to stop = %v, want %v", stop, want)
	}
}

// TestOrchestratorActionRerunsFailedIntegrateBehindAnEmptyPass checks that a retry_node action reruns a failed integrate
// run that is behind a later integrate run which passed with nothing to integrate, keeping the run's revision and result.
func TestOrchestratorActionRerunsFailedIntegrateBehindAnEmptyPass(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openIntegrateStore(t)
	failIntegrateRun(t, s, retryEarlierBatch, "id-s1", "rev-1", "lint failed")
	startIntegrateRuns(t, s,
		NodeRun{Path: retryLatestBatch, ID: "id-s2", Node: "integrate", Kind: "integrate", Attempt: 1})
	if err := s.EndBatch(ctx, integrateEpic, BatchEnd{Path: retryLatestBatch, Attempt: 1, Outcome: "passed"}, nil,
		integrateEndedAt()); err != nil {
		t.Fatalf("EndBatch passed: %v", err)
	}
	parkIntegrateEpic(t, s)
	action := storeActions(t, s, integrateEpic, EpicActionInput{
		Kind: ActionRetryNode, Node: "integrate", Reason: "the candidate can be integrated again",
	})[0]

	if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
		t.Fatalf("CarryOutAction: %v", err)
	}
	requireIntegrateRun(t, s, retryEarlierBatch, NodeRun{
		Path: retryEarlierBatch, ID: "id-s1", Node: "integrate", Kind: "integrate", Attempt: 2,
		Revision: "rev-1", Refs: "refs-1", Result: retryComposed, State: NodeRunning, Started: integrateStartedAt(),
	})
	if parked := integrateEpicRow(t, s).Parked; parked != "" {
		t.Errorf("parked of epic %s after retry_node = %q, want empty", integrateEpic, parked)
	}
}

// TestOrchestratorActionRefusalIsRecordedWithItsTrigger checks that an action the engine refuses writes none of the
// control, and is recorded as refused with the refusal, in a decision that holds the refusal and the reason, and a pending
// trigger that names the action and the refusal.
func TestOrchestratorActionRefusalIsRecordedWithItsTrigger(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		epic    string
		setup   func(t *testing.T, s *Store)
		action  EpicActionInput
		summary string
		refusal string
	}{
		{
			name: "retry_epic on a stopped epic",
			epic: actionEpic,
			setup: func(t *testing.T, s *Store) {
				t.Helper()
				createStoppedRelease(t, s, actionEpic)
			},
			action: EpicActionInput{
				Kind: ActionRetryEpic, Reason: "the release can go on",
			},
			summary: "retry epic",
			refusal: "epic " + actionEpic + " is stopped; retry ends a stopped release only when its release integrator stopped it",
		},
		{
			name: "resume that names an undo-none step",
			epic: actionEpic,
			setup: func(t *testing.T, s *Store) {
				t.Helper()
				createStoppedRelease(t, s, actionEpic)
			},
			action: EpicActionInput{
				Kind: ActionResumeRelease, UndoNone: []int64{2}, Reason: "the publish step must run again",
			},
			summary: "resume release",
			refusal: "epic " + actionEpic + ": a planner resume names no undo-none step; only the owner names one",
		},
		{
			name: "resume of a stopped epic whose owner stop freed its release slot",
			epic: actionOther,
			setup: func(t *testing.T, s *Store) {
				t.Helper()
				createStoppedRelease(t, s, actionOther)
				if _, err := s.StopEpic(context.Background(), actionOther); err != nil {
					t.Fatalf("StopEpic of epic %s: %v", actionOther, err)
				}
			},
			action: EpicActionInput{
				Kind: ActionResumeRelease, Reason: "the push tag can be undone and run again",
			},
			summary: "resume release",
			refusal: "epic " + actionOther + " does not hold the release slot of /work/synthetic-repo",
		},
		{
			name: "close on a closed epic",
			epic: actionEpic,
			setup: func(t *testing.T, s *Store) {
				t.Helper()
				createEpic(t, s, fullEpic(actionEpic))
				if _, err := s.CloseEpic(context.Background(), actionEpic, "abandoned", "", CloserOwner,
					closeAt()); err != nil {
					t.Fatalf("CloseEpic: %v", err)
				}
			},
			action: EpicActionInput{
				Kind: ActionCloseEpic, Close: "abandoned", Reason: "the work is dropped",
			},
			summary: "close epic as abandoned",
			refusal: "epic " + actionEpic + " is closed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			c.setup(t, s)
			action := storeActions(t, s, c.epic, c.action)[0]

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, c.summary, c.refusal)
		})
	}
}
