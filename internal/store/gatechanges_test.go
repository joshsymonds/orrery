package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// TestSetTaskBuildGateChangesStoresTheBuildAndTheChanges: the write moves the build as SetTaskBuild does and stores the
// whole list of declared gate-needed changes, which the task reads back in order. A planned task reads back none.
func TestSetTaskBuildGateChangesStoresTheBuildAndTheChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	if got := buildTaskOf(t, s, 1).GateChanges; got != nil {
		t.Fatalf("gate changes of a planned task = %v, want none", got)
	}

	from := buildTaskOf(t, s, 1).Build
	to := TaskBuild{Stage: "green", Base: buildBase, Attempt: 1, Revision: "abc1234"}
	changes := []GateChange{
		{Path: "internal/enginehost/record.go", Reason: "check_changed fails on a -race report in stoppedTasks"},
		{Path: "cmd/orreryd/callerevent_wiring_test.go", Reason: "the wake moves the delivery this test expects"},
	}
	if err := s.SetTaskBuildGateChanges(ctx, buildEpic, 1, from, to, changes); err != nil {
		t.Fatalf("SetTaskBuildGateChanges: %v", err)
	}
	task := buildTaskOf(t, s, 1)
	if task.Build != to {
		t.Errorf("build after SetTaskBuildGateChanges = %+v, want %+v", task.Build, to)
	}
	if !reflect.DeepEqual(task.GateChanges, changes) {
		t.Errorf("gate changes = %v, want %v", task.GateChanges, changes)
	}
	if got := buildTaskOf(t, s, 2).GateChanges; got != nil {
		t.Errorf("gate changes of task 2 = %v, want none", got)
	}
}

// TestSetTaskBuildGateChangesFromAMovedBuildWritesNothing: a write from a build that is not the stored one is
// ErrTaskMoved, and neither the build nor the changes are stored.
func TestSetTaskBuildGateChangesFromAMovedBuildWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := plannedBuildEpic(t)
	before := rowsOf(t, s.db.DB, "epic_tasks")
	err := s.SetTaskBuildGateChanges(ctx, buildEpic, 1, TaskBuild{Stage: "implement"},
		TaskBuild{Stage: "green", Base: buildBase}, []GateChange{{Path: "a.go", Reason: "the gate needs it"}})
	if !errors.Is(err, ErrTaskMoved) {
		t.Fatalf("SetTaskBuildGateChanges from a moved build: err = %v, want ErrTaskMoved", err)
	}
	if got := rowsOf(t, s.db.DB, "epic_tasks"); !reflect.DeepEqual(got, before) {
		t.Errorf("epic_tasks after the refused write = %v, want %v", got, before)
	}
}
