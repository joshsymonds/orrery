package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// dependsOnTask is a task of a planner return named title that depends on the entries depends names, with the files,
// Requirement, gate and evidence every dependency test gives its tasks.
func dependsOnTask(title string, depends ...string) EpicTaskInput {
	return EpicTaskInput{
		Title: title, Deliverable: title + " as planned", Files: []string{"dep.go"}, Requirements: []string{"R11"},
		Gate: "check", Evidence: evidence.Item{
			Capture: []string{"scripts/check"}, Limit: 1800, Conclusion: "the task holds", Judge: "the task judge",
		},
		DependsOn: depends,
	}
}

// TestDependsOnStoredAsSeqs checks that a turn on an epic holding T1 stores each task's dependencies as seqs, in the
// order the planner named them: a task that names T1 and the title of a task of the same turn, and a task that names
// nothing, which reads back with no dependencies.
func TestDependsOnStoredAsSeqs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	if err := s.CompleteTurn(ctx, id, 1, plannedTurn(), completedAt()); err != nil {
		t.Fatalf("first CompleteTurn: %v", err)
	}
	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "later trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3: %v", err)
	}
	turn, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}
	plan := EpicPlan{
		Outline: "Index the store, then expose it",
		Tasks: []EpicTaskInput{
			dependsOnTask("Index the store", "T1"),
			dependsOnTask("Expose the index", "Index the store", "T2"),
			dependsOnTask("Document the index"),
		},
		Outcome: "work",
	}
	if err = s.CompleteTurn(ctx, id, turn.Seq, plan, completedAt().Add(time.Minute)); err != nil {
		t.Fatalf("second CompleteTurn: %v", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT seq, depends_on FROM epic_tasks WHERE epic = ? ORDER BY seq`, id)
	if err != nil {
		t.Fatalf("read depends_on: %v", err)
	}
	defer rows.Close()
	var stored []string
	for rows.Next() {
		var seq int64
		var dependsOn string
		if err = rows.Scan(&seq, &dependsOn); err != nil {
			t.Fatalf("scan depends_on: %v", err)
		}
		stored = append(stored, dependsOn)
	}
	if err = rows.Err(); err != nil {
		t.Fatalf("iterate depends_on: %v", err)
	}
	wantStored := []string{"", "", "1", "3,2", ""}
	if !reflect.DeepEqual(stored, wantStored) {
		t.Errorf("depends_on stored = %q, want %q", stored, wantStored)
	}

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	wantDependsOn := map[int64][]int64{1: nil, 2: nil, 3: {1}, 4: {3, 2}, 5: nil}
	if len(row.Tasks) != len(wantDependsOn) {
		t.Fatalf("epic has %d tasks, want %d", len(row.Tasks), len(wantDependsOn))
	}
	for _, task := range row.Tasks {
		if !reflect.DeepEqual(task.DependsOn, wantDependsOn[task.Seq]) {
			t.Errorf("task %d DependsOn = %v, want %v", task.Seq, task.DependsOn, wantDependsOn[task.Seq])
		}
	}
}

// TestDependsOnRefusesBadNames checks that CompleteTurn refuses a task whose dependency names no task of the epic or of
// the return, a title two tasks of the return share, or the task itself, with ErrBadDependency, naming the task's
// position and the entry. Each refusal leaves the turn running and stores no task.
func TestDependsOnRefusesBadNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		tasks []EpicTaskInput
		named string
	}{
		{
			name:  "unknown seq",
			tasks: []EpicTaskInput{dependsOnTask("Reads the store"), dependsOnTask("Writes the store", "T4")},
			named: `task at position 2 depends on "T4"`,
		},
		{
			name:  "unknown title",
			tasks: []EpicTaskInput{dependsOnTask("Reads the store", "Writes the store")},
			named: `task at position 1 depends on "Writes the store"`,
		},
		{
			name: "shared title",
			tasks: []EpicTaskInput{
				dependsOnTask("Twin task"), dependsOnTask("Twin task"), dependsOnTask("Reads the twin", "Twin task"),
			},
			named: `task at position 3 depends on "Twin task"`,
		},
		{
			name:  "self reference",
			tasks: []EpicTaskInput{dependsOnTask("Loops back", "Loops back")},
			named: `task at position 1 depends on "Loops back"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			const id = claimEpic
			createEpic(t, s, fullEpic(id))
			addPendingTrigger(t, s)
			turn, err := s.ClaimTurn(ctx, id, claimedAt())
			if err != nil {
				t.Fatalf("ClaimTurn: %v", err)
			}

			err = s.CompleteTurn(ctx, id, turn.Seq, EpicPlan{Tasks: tc.tasks, Outcome: "work"}, completedAt())
			if !errors.Is(err, ErrBadDependency) {
				t.Fatalf("CompleteTurn err = %v, want ErrBadDependency", err)
			}
			if !strings.Contains(err.Error(), tc.named) {
				t.Errorf("CompleteTurn err = %q, want it to name %q", err, tc.named)
			}

			row, err := s.Epic(ctx, id)
			if err != nil {
				t.Fatalf("Epic: %v", err)
			}
			if len(row.Turns) != 1 || row.Turns[0].State != "running" {
				t.Errorf("turns = %+v, want the one turn still running", row.Turns)
			}
			if len(row.Tasks) != 0 {
				t.Errorf("tasks = %+v, want none stored", row.Tasks)
			}
		})
	}
}

// TestDependsOnClimbToGap checks that a climb of an open task at attempt 0 onto GapRung for a dependency ends the task
// as a gap: rung 1 ends failed, rung 6 is stored moved by the dependency and ends gapped, the status is gap, and the
// gap trigger T1-gap is pending.
func TestDependsOnClimbToGap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	from := climbFrom()
	from.Attempt = 0
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByDependency, Cause: climbCause, To: GapRung}
	to := ClimbedBuild(from, GapRung, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, nil, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask: %v", err)
	}

	got := climbTaskOf(ctx, t, s, 1)
	if got.Status != TaskGap {
		t.Errorf("status = %q, want %q", got.Status, TaskGap)
	}
	wantRungs := []TaskRung{
		{Rung: 1, Kind: RungAttempt, Created: climbedAt(), Outcome: RungFailed, Reason: climbCause, Ended: climbedAt()},
		{
			Rung: GapRung, Kind: RungGap, MovedBy: MovedByDependency, Cause: climbCause, Created: climbedAt(),
			Outcome: RungGapped, Reason: climbCause, Ended: climbedAt(),
		},
	}
	if !reflect.DeepEqual(got.Rungs, wantRungs) {
		t.Errorf("rungs:\n got %+v\nwant %+v", got.Rungs, wantRungs)
	}

	history, err := s.EpicHistory(ctx, climbEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	wantTriggers := []EpicTrigger{
		{ID: "created", Cause: "epic created", State: triggerPending, Created: epicCreated()},
		{ID: "T1-gap", Cause: "task T1 ended as a gap: " + climbCause, State: triggerPending, Created: climbedAt()},
	}
	if !reflect.DeepEqual(history.Triggers, wantTriggers) {
		t.Errorf("triggers:\n got %+v\nwant %+v", history.Triggers, wantTriggers)
	}
}

// TestDependsOnRefusesSplitTask checks that CompleteTurn refuses a task whose dependency names a task a split action
// split, with ErrBadDependency naming the task's position and the entry. The refused turn stores no task and stays
// running.
func TestDependsOnRefusesSplitTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openEpicStore(t)
	const id = claimEpic
	createEpic(t, s, fullEpic(id))
	addPendingTrigger(t, s)
	if _, err := s.ClaimTurn(ctx, id, claimedAt()); err != nil {
		t.Fatalf("first ClaimTurn: %v", err)
	}
	first := EpicPlan{Tasks: []EpicTaskInput{dependsOnTask("Index the store")}, Outcome: "work"}
	if err := s.CompleteTurn(ctx, id, 1, first, completedAt()); err != nil {
		t.Fatalf("first CompleteTurn: %v", err)
	}
	if _, err := s.fixtureExec(ctx, `UPDATE epic_tasks SET status = ? WHERE epic = ? AND seq = 1`,
		TaskSplit, id); err != nil {
		t.Fatalf("split task 1: %v", err)
	}
	if err := s.fixtureQueries().InsertEpicTrigger(ctx, sqlcgen.InsertEpicTriggerParams{
		Epic: id, ID: "t3", Cause: "later trigger", State: "pending", Turn: 0, Created: epicCreated().Unix(),
	}); err != nil {
		t.Fatalf("insert trigger t3: %v", err)
	}
	turn, err := s.ClaimTurn(ctx, id, claimedAt().Add(time.Minute))
	if err != nil {
		t.Fatalf("second ClaimTurn: %v", err)
	}

	second := EpicPlan{Tasks: []EpicTaskInput{dependsOnTask("Expose the index", "T1")}, Outcome: "work"}
	err = s.CompleteTurn(ctx, id, turn.Seq, second, completedAt().Add(time.Minute))
	if !errors.Is(err, ErrBadDependency) {
		t.Fatalf("second CompleteTurn err = %v, want ErrBadDependency", err)
	}
	const named = `task at position 1 depends on "T1"`
	if !strings.Contains(err.Error(), named) || !strings.Contains(err.Error(), "names a split task") {
		t.Errorf("second CompleteTurn err = %q, want it to name %q as a split task", err, named)
	}

	row, err := s.Epic(ctx, id)
	if err != nil {
		t.Fatalf("Epic: %v", err)
	}
	if len(row.Turns) != 2 || row.Turns[1].State != "running" {
		t.Errorf("turns = %+v, want the second turn still running", row.Turns)
	}
	if len(row.Tasks) != 1 || row.Tasks[0].Seq != 1 {
		t.Errorf("tasks = %+v, want only task 1 stored", row.Tasks)
	}
}
