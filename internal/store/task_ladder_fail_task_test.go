package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// ladderFailTaskEpic is the epic the fail_task climb tests plan, with task 1 holding the build each case stores.
const ladderFailTaskEpic = "orrery-1a2b3c4/task-fail"

// ladderFailTaskReason is the planner's reason for failing task 1 in the cases that do not cut it.
const ladderFailTaskReason = "the build cannot reach the store"

// ladderFailTaskAt is the second the fail_task climb tests carry their actions out at.
func ladderFailTaskAt() time.Time {
	return time.Unix(1700001200, 0)
}

// ladderFailTaskStore returns a store whose ladderFailTaskEpic has turn 1 planned, with task 1 holding build.
func ladderFailTaskStore(t *testing.T, build TaskBuild) *Store {
	t.Helper()
	ctx := context.Background()
	s := openEpicStore(t)
	createEpic(t, s, fullEpic(ladderFailTaskEpic))
	turn, err := s.ClaimTurn(ctx, ladderFailTaskEpic, claimedAt())
	if err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, ladderFailTaskEpic, turn.Seq, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	if err = s.SetTaskBuild(ctx, ladderFailTaskEpic, 1, TaskBuild{}, build); err != nil {
		t.Fatalf("SetTaskBuild: %v", err)
	}
	return s
}

// ladderFailTaskInput is the fail_task action of task 1 with reason.
func ladderFailTaskInput(reason string) EpicActionInput {
	return EpicActionInput{Kind: ActionFailTask, Task: 1, Reason: reason}
}

// ladderFailTaskCarryOut carries out action at ladderFailTaskAt, and fails the test on error.
func ladderFailTaskCarryOut(t *testing.T, s *Store, action PlannerAction) {
	t.Helper()
	if _, err := s.CarryOutAction(context.Background(), action, ladderFailTaskAt()); err != nil {
		t.Fatalf("CarryOutAction of action %d: %v", action.Position, err)
	}
}

// ladderFailTaskState returns the state action is stored in.
func ladderFailTaskState(t *testing.T, s *Store, action PlannerAction) string {
	t.Helper()
	var state string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT state FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?`,
		action.Epic, action.Turn, action.Position).Scan(&state); err != nil {
		t.Fatalf("read action %d of epic %s: %v", action.Position, action.Epic, err)
	}
	return state
}

// ladderFailTaskHasTrigger reports whether ladderFailTaskEpic has a trigger with id.
func ladderFailTaskHasTrigger(t *testing.T, s *Store, id string) bool {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), ladderFailTaskEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	for _, trigger := range history.Triggers {
		if trigger.ID == id {
			return true
		}
	}
	return false
}

// ladderFailTaskClimbs checks that a fail_task of reason on task 1 holding from climbs the task one rung, with cause as
// the cause: the task is open on the rung after from's, the rung from was on ends failed with cause, the rung climbed
// to is moved by fail_task with from's step, the stored build is the climbed build, the action is taken, and no
// T1-failed trigger exists.
func ladderFailTaskClimbs(t *testing.T, from TaskBuild, reason, cause string) {
	t.Helper()
	s := ladderFailTaskStore(t, from)
	action := storeActions(t, s, ladderFailTaskEpic, ladderFailTaskInput(reason))[0]
	ladderFailTaskCarryOut(t, s, action)

	at := ladderFailTaskAt()
	rung := RungOf(from)
	task := closeRow(t, s, ladderFailTaskEpic).Tasks[0]
	if task.Status != "open" {
		t.Errorf("status = %q, want open", task.Status)
	}
	if want := ClimbedBuild(from, rung+1, cause); !reflect.DeepEqual(task.Build, want) {
		t.Errorf("build = %+v, want %+v", task.Build, want)
	}
	wantRungs := []TaskRung{
		{Rung: rung, Kind: RungKind(rung), Created: at, Outcome: RungFailed, Reason: cause, Ended: at},
		{
			Rung: rung + 1, Kind: RungKind(rung + 1), MovedBy: MovedByFailTask, Cause: cause, Step: from.Step,
			Created: at,
		},
	}
	if !reflect.DeepEqual(task.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, wantRungs)
	}
	if state := ladderFailTaskState(t, s, action); state != "taken" {
		t.Errorf("action state = %q, want taken", state)
	}
	if ladderFailTaskHasTrigger(t, s, "T1-failed") {
		t.Errorf("trigger T1-failed exists, want none: a climb adds no failure trigger")
	}
}

// ladderFailTaskGap checks that a fail_task on task 1 at attempt 5 ends the task as a gap: rung 5 ends failed, rung 6
// is a gap rung moved by fail_task that ends gapped, the status is gap, the gap trigger T1-gap is pending with the
// reason, and no T1-failed trigger exists.
func ladderFailTaskGap(t *testing.T) {
	t.Helper()
	from := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: LastRung, Step: "step-6"}
	s := ladderFailTaskStore(t, from)
	action := storeActions(t, s, ladderFailTaskEpic, ladderFailTaskInput(ladderFailTaskReason))[0]
	ladderFailTaskCarryOut(t, s, action)

	at := ladderFailTaskAt()
	task := closeRow(t, s, ladderFailTaskEpic).Tasks[0]
	if task.Status != TaskGap {
		t.Errorf("status = %q, want %q", task.Status, TaskGap)
	}
	if want := ClimbedBuild(from, GapRung, ladderFailTaskReason); !reflect.DeepEqual(task.Build, want) {
		t.Errorf("build = %+v, want %+v", task.Build, want)
	}
	wantRungs := []TaskRung{
		{Rung: LastRung, Kind: RungApproach, Created: at, Outcome: RungFailed, Reason: ladderFailTaskReason, Ended: at},
		{
			Rung: GapRung, Kind: RungGap, MovedBy: MovedByFailTask, Cause: ladderFailTaskReason, Step: "step-6",
			Created: at, Outcome: RungGapped, Reason: ladderFailTaskReason, Ended: at,
		},
	}
	if !reflect.DeepEqual(task.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", task.Rungs, wantRungs)
	}
	if state, cause := triggerRow(t, s, ladderFailTaskEpic, "T1-gap"); state != triggerPending ||
		cause != "task T1 ended as a gap: "+ladderFailTaskReason {
		t.Errorf("trigger T1-gap = state %q cause %q, want pending with the reason", state, cause)
	}
	if ladderFailTaskHasTrigger(t, s, "T1-failed") {
		t.Errorf("trigger T1-failed exists, want none: a climb adds no failure trigger")
	}
	if state := ladderFailTaskState(t, s, action); state != "taken" {
		t.Errorf("action state = %q, want taken", state)
	}
}

// ladderFailTaskRefused checks that a fail_task on task 1 once it is a gap is refused as not open: the action is
// recorded as refused with its refusal and refusal trigger, and the rungs the first fail_task wrote are the only ones.
func ladderFailTaskRefused(t *testing.T) {
	t.Helper()
	s := ladderFailTaskStore(t, TaskBuild{Stage: "implement", Base: "base-rev", Attempt: LastRung, Step: "step-6"})
	actions := storeActions(t, s, ladderFailTaskEpic,
		ladderFailTaskInput(ladderFailTaskReason), ladderFailTaskInput(ladderFailTaskReason))
	ladderFailTaskCarryOut(t, s, actions[0])
	rungs := closeRow(t, s, ladderFailTaskEpic).Tasks[0].Rungs

	ladderFailTaskCarryOut(t, s, actions[1])
	requireRefusedAction(t, s, actions[1], "fail task 1", "task 1 of epic "+ladderFailTaskEpic+" is not open")
	if got := closeRow(t, s, ladderFailTaskEpic).Tasks[0].Rungs; !reflect.DeepEqual(got, rungs) {
		t.Errorf("rungs after the refused fail_task:\n got %+v\nwant %+v", got, rungs)
	}
}

// TestTaskLadderFailTaskClimbsOneRung checks that a fail_task action climbs the open task it names one rung, moved by
// fail_task, with the planner's reason as the cause, and that from the last rung it ends the task as a gap. A
// fail_task on a task that is a gap is refused.
func TestTaskLadderFailTaskClimbsOneRung(t *testing.T) {
	t.Parallel()
	t.Run("implement at attempt 1 climbs to attempt 2", func(t *testing.T) {
		t.Parallel()
		from := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1"}
		ladderFailTaskClimbs(t, from, ladderFailTaskReason, ladderFailTaskReason)
	})
	t.Run("red at attempt 0 climbs to attempt 2 in red", func(t *testing.T) {
		t.Parallel()
		ladderFailTaskClimbs(t, TaskBuild{Stage: "red", Base: "base-rev"}, ladderFailTaskReason, ladderFailTaskReason)
	})
	t.Run("a held build climbs with no hold", func(t *testing.T) {
		t.Parallel()
		from := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1", Held: "waiting"}
		ladderFailTaskClimbs(t, from, ladderFailTaskReason, ladderFailTaskReason)
	})
	t.Run("a reason longer than TurnReasonMax is cut to it", func(t *testing.T) {
		t.Parallel()
		reason := strings.Repeat("r", TurnReasonMax+10)
		from := TaskBuild{Stage: "implement", Base: "base-rev", Attempt: 1, Step: "step-1"}
		ladderFailTaskClimbs(t, from, reason, reason[:TurnReasonMax])
	})
	t.Run("from attempt 6 ends the task as a gap", func(t *testing.T) {
		t.Parallel()
		ladderFailTaskGap(t)
	})
	t.Run("a fail_task on a gap task is refused", func(t *testing.T) {
		t.Parallel()
		ladderFailTaskRefused(t)
	})
}
