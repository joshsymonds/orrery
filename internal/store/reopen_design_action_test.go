package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// The reason and deliverable the reopen_design actions of these tests give.
const (
	reopenReason      = "the index must take a list"
	reopenDeliverable = "the index interface, widened to take a list"
)

// reopenItem is the evidence item of task 1 in the reopen tests.
func reopenItem() evidence.Item {
	return evidence.Item{Capture: []string{"go", "test", "./internal/index"}, Limit: 300, Conclusion: "the index holds"}
}

// seedReopenTask creates climbEpic with task 1 of kind and status, titled Design the index, with the gate review, the
// evidence item reopenItem and the file index.go.
func seedReopenTask(t *testing.T, s *Store, kind, status string) {
	t.Helper()
	ctx := context.Background()
	createEpic(t, s, fullEpic(climbEpic))
	item, err := encodeItem(reopenItem())
	if err != nil {
		t.Fatalf("encode evidence of task 1: %v", err)
	}
	if _, err = s.fixtureExec(ctx, `INSERT INTO epic_tasks (epic, seq, turn, title, deliverable, gate, evidence, status, kind)
		VALUES (?, 1, 1, 'Design the index', 'the index interface', 'review', ?, ?, ?)`,
		climbEpic, item, status, kind); err != nil {
		t.Fatalf("insert task 1: %v", err)
	}
	ladderApproachOwn(t, s, 1, "index.go")
}

// reopenDesignAction stores a reopen_design action of task seq of climbEpic as its pending action, and returns it.
func reopenDesignAction(t *testing.T, s *Store, seq int64) PlannerAction {
	t.Helper()
	return storeActions(t, s, climbEpic, EpicActionInput{
		Kind: ActionReopenDesign, Task: seq, Deliverable: reopenDeliverable, Reason: reopenReason,
	})[0]
}

// reopenTasksOf returns the tasks of climbEpic, as Epic reads them.
func reopenTasksOf(t *testing.T, s *Store) []EpicTask {
	t.Helper()
	epic, err := s.Epic(context.Background(), climbEpic)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	return epic.Tasks
}

// TestReopenDesignActionAddsTheRevision checks that a reopen_design action of a design task adds a revision of it in one
// write: an open design task with the next seq, revising the task, planned on the action's turn, titled after the task,
// with the action's deliverable and the files, gate, test changes and evidence of the task's current version, which here
// is a version that sets the gate and the test changes. The reopened task keeps its rows, and the action is taken with its
// decision.
func TestReopenDesignActionAddsTheRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	seedReopenTask(t, s, TaskKindDesign, taskDone)
	gate, changes := "new-gate", []string{"index_test.go"}
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		return appendTaskVersion(ctx, queries, climbEpic, 1, "", TaskVersionInput{
			Source: SourceOrchestrator, Reason: "the gate moves", Gate: &gate, TestChanges: &changes,
		}, closeAt())
	}); err != nil {
		t.Fatalf("append version of task 1: %v", err)
	}
	before := climbTaskOf(ctx, t, s, 1)
	action := reopenDesignAction(t, s, 1)

	stop, err := s.CarryOutAction(ctx, action, closeAt())
	if err != nil || stop != nil {
		t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
	}

	revision := climbTaskOf(ctx, t, s, 2)
	checks := []struct {
		name      string
		got, want any
	}{
		{"Kind", revision.Kind, TaskKindDesign},
		{"Revises", revision.Revises, int64(1)},
		{"Turn", revision.Turn, action.Turn},
		{"Title", revision.Title, "Revise T1: Design the index"},
		{"Deliverable", revision.Deliverable, reopenDeliverable},
		{"Status", revision.Status, "open"},
		{"Files", revision.Files, []string{"index.go"}},
		{"TestChanges", revision.TestChanges, []string{"index_test.go"}},
		{"Gate", revision.Gate, gate},
		{"Evidence", revision.Evidence, reopenItem()},
		{"DependsOn", revision.DependsOn, []int64(nil)},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Errorf("revision %s = %#v, want %#v", check.name, check.got, check.want)
		}
	}
	if got := climbTaskOf(ctx, t, s, 1); !reflect.DeepEqual(got, before) {
		t.Errorf("task 1 after the reopen = %+v, want it unchanged: %+v", got, before)
	}
	if state, refusal := ladderApproachState(t, s, action); state != actionTaken || refusal != "" {
		t.Errorf("action %d of epic %s = state %q refusal %q, want taken", action.Position, climbEpic, state, refusal)
	}
	if got, want := decisionNamed(t, s, climbEpic, "action-1-1").Text,
		"planner reopen design of task 1: "+reopenReason; got != want {
		t.Errorf("decision of reopen_design = %q, want %q", got, want)
	}
}

// TestReopenDesignActionRefusesANonDesignTask checks that a reopen_design action naming a task that is not a design task
// is refused with its trigger, and writes no revision and changes no task row. It also checks the same for a task that
// does not exist, and for a design task that has failed or ended as a gap.
func TestReopenDesignActionRefusesANonDesignTask(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, kind, status string
		task               int64
		refusal            string
	}{
		{
			name: "a task that is not a design task", kind: "", status: taskDone, task: 1,
			refusal: "task 1 of epic " + climbEpic + " is not a design task",
		},
		{
			name: "a task that does not exist", kind: TaskKindDesign, status: taskDone, task: 2,
			refusal: "task 2 of epic " + climbEpic + " does not exist",
		},
		{
			name: "a design task that has failed", kind: TaskKindDesign, status: taskFailed, task: 1,
			refusal: "task 1 of epic " + climbEpic + " has failed",
		},
		{
			name: "a design task that ended as a gap", kind: TaskKindDesign, status: TaskGap, task: 1,
			refusal: "task 1 of epic " + climbEpic + " ended as a gap",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			seedReopenTask(t, s, c.kind, c.status)
			before := reopenTasksOf(t, s)
			action := reopenDesignAction(t, s, c.task)

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, fmt.Sprintf("reopen design of task %d", c.task), c.refusal)
			if got := reopenTasksOf(t, s); !reflect.DeepEqual(got, before) {
				t.Errorf("tasks of epic %s after the refusal = %+v, want %+v", climbEpic, got, before)
			}
		})
	}
}
