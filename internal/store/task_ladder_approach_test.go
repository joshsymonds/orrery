package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
)

// The reason and deliverable the change_approach actions of the approach tests give.
const (
	ladderApproachReason      = "the first approach cannot reach the store"
	ladderApproachDeliverable = "the change made another way"
)

// ladderApproachOnRung5 creates climbEpic with task 1 climbed from rung 3 onto rung 4, the approach rung, by the
// orchestrator's fail decision. version, when not nil, is stored with the climb as the task's version.
func ladderApproachOnRung5(t *testing.T, s *Store, version *TaskVersionInput) {
	t.Helper()
	ctx := context.Background()
	from := climbFrom()
	from.Attempt = OrchestratorRung
	seedClimbTask(t, s, from)
	move := RungMove{MovedBy: MovedByFailDecision, Cause: climbCause, Step: "step-9"}
	to := ClimbedBuild(from, ApproachRung, move.Cause)
	if err := s.ClimbTask(ctx, climbEpic, 1, from, to, move, version, nil, climbedAt()); err != nil {
		t.Fatalf("ClimbTask onto rung %d: %v", ApproachRung, err)
	}
}

// ladderApproachAction stores a change_approach action of task seq with item as the pending action of climbEpic, and
// returns it.
func ladderApproachAction(t *testing.T, s *Store, seq int64, item evidence.Item) PlannerAction {
	t.Helper()
	return storeActions(t, s, climbEpic, EpicActionInput{
		Kind: ActionChangeApproach, Task: seq, Deliverable: ladderApproachDeliverable, Item: item,
		Reason: ladderApproachReason,
	})[0]
}

// ladderApproachFilesAction stores a change_approach action of task seq that adds files and removes remove, with no
// evidence item, as the pending action of climbEpic, and returns it.
func ladderApproachFilesAction(t *testing.T, s *Store, seq int64, files, remove []string) PlannerAction {
	t.Helper()
	return storeActions(t, s, climbEpic, EpicActionInput{
		Kind: ActionChangeApproach, Task: seq, Deliverable: ladderApproachDeliverable, Files: files, Remove: remove,
		Reason: ladderApproachReason,
	})[0]
}

// ladderApproachOwn gives task seq of climbEpic the files paths, in order.
func ladderApproachOwn(t *testing.T, s *Store, seq int64, paths ...string) {
	t.Helper()
	ctx := context.Background()
	for i, path := range paths {
		if _, err := s.fixtureExec(ctx, `INSERT INTO epic_task_files (epic, task, position, path) VALUES (?, ?, ?, ?)`,
			climbEpic, seq, i, path); err != nil {
			t.Fatalf("give task %d the file %s: %v", seq, path, err)
		}
	}
}

// ladderApproachVersions returns how many task spec versions climbEpic stores, over all its tasks.
func ladderApproachVersions(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM epic_task_versions WHERE epic = ?`, climbEpic).Scan(&n); err != nil {
		t.Fatalf("count versions of epic %s: %v", climbEpic, err)
	}
	return n
}

// ladderApproachState returns the state and refusal action's row holds.
func ladderApproachState(t *testing.T, s *Store, action PlannerAction) (state, refusal string) {
	t.Helper()
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT state, refusal FROM planner_actions WHERE epic = ? AND turn = ? AND position = ?`,
		action.Epic, action.Turn, action.Position).Scan(&state, &refusal); err != nil {
		t.Fatalf("read action %d of epic %s: %v", action.Position, action.Epic, err)
	}
	return state, refusal
}

// TestTaskLadderChangeApproachStoresVersion checks that a change_approach action carried out on a task on the approach
// rung stores the task's approach version, applied, or checking when its item sets a field, and is taken with its
// decision. It also checks that the action is refused, with its trigger and no version written, for a task not on the
// approach rung, a task that is not open, a task that does not exist, and a task that already has an approach version.
func TestTaskLadderChangeApproachStoresVersion(t *testing.T) {
	t.Parallel()
	t.Run("stores the version applied", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		ladderApproachOnRung5(t, s, nil)
		action := ladderApproachAction(t, s, 1, evidence.Item{})

		stop, err := s.CarryOutAction(ctx, action, closeAt())
		if err != nil || stop != nil {
			t.Fatalf("CarryOutAction = %v, %v; want no steps and no error", stop, err)
		}
		deliverable := ladderApproachDeliverable
		want := []TaskVersion{{
			Version: 2, Source: SourceApproach, Decision: "action-1-1", Reason: ladderApproachReason,
			State: VersionApplied, Deliverable: &deliverable, Created: closeAt(), Settled: closeAt(),
		}}
		if got := climbTaskOf(ctx, t, s, 1).Versions; !reflect.DeepEqual(got, want) {
			t.Errorf("versions of task 1 = %+v, want %+v", got, want)
		}
		if state, refusal := ladderApproachState(t, s, action); state != actionTaken || refusal != "" {
			t.Errorf("action 1 of epic %s = state %q refusal %q, want taken", climbEpic, state, refusal)
		}
		if got, want := decisionNamed(t, s, climbEpic, "action-1-1").Text,
			"planner change approach of task 1: "+ladderApproachReason; got != want {
			t.Errorf("decision of change_approach = %q, want %q", got, want)
		}
	})

	t.Run("stores the version checking with its item", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		ladderApproachOnRung5(t, s, nil)
		item := evidence.Item{
			Capture: []string{"go", "test", "./internal/approach"}, Limit: 300, Conclusion: "the approach holds",
		}
		action := ladderApproachAction(t, s, 1, item)

		if _, err := s.CarryOutAction(ctx, action, closeAt()); err != nil {
			t.Fatalf("CarryOutAction: %v", err)
		}
		deliverable := ladderApproachDeliverable
		want := []TaskVersion{{
			Version: 2, Source: SourceApproach, Decision: "action-1-1", Reason: ladderApproachReason,
			State: VersionChecking, Deliverable: &deliverable, Evidence: &item, Created: closeAt(),
		}}
		if got := climbTaskOf(ctx, t, s, 1).Versions; !reflect.DeepEqual(got, want) {
			t.Errorf("versions of task 1 = %+v, want %+v", got, want)
		}
		if state, _ := ladderApproachState(t, s, action); state != actionTaken {
			t.Errorf("action 1 of epic %s = state %q, want taken", climbEpic, state)
		}
	})

	earlier := "an earlier change"
	earlierVersion := TaskVersionInput{Source: SourceApproach, Reason: "an earlier approach", Deliverable: &earlier}
	cases := []struct {
		name    string
		seed    func(t *testing.T, s *Store)
		task    int64
		files   []string
		remove  []string
		summary string
		refusal string
	}{
		{
			name: "a task not on the approach rung",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				from := climbFrom()
				from.Attempt = 2
				seedClimbTask(t, s, from)
			},
			task:    1,
			summary: "change approach of task 1",
			refusal: "task 1 of epic " + climbEpic + " is on rung 2, not rung 4",
		},
		{
			name: "a task that is not open",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				if _, err := s.fixtureExec(context.Background(),
					`UPDATE epic_tasks SET status = 'failed' WHERE epic = ? AND seq = 1`, climbEpic); err != nil {
					t.Fatalf("fail task 1: %v", err)
				}
			},
			task:    1,
			summary: "change approach of task 1",
			refusal: "task 1 of epic " + climbEpic + " is not open",
		},
		{
			name: "a task that does not exist",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
			},
			task:    2,
			summary: "change approach of task 2",
			refusal: "task 2 of epic " + climbEpic + " does not exist",
		},
		{
			name: "a task that already has an approach version",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, &earlierVersion)
			},
			task:    1,
			summary: "change approach of task 1",
			refusal: "task 1 of epic " + climbEpic + " already has a change of approach",
		},
		{
			name: "a file the task already owns",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				ladderApproachOwn(t, s, 1, "thing.go")
			},
			task:    1,
			files:   []string{"thing.go"},
			summary: "change approach of task 1",
			refusal: "thing.go is already one of task 1's files",
		},
		{
			name: "a file to remove the task does not own",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				ladderApproachOwn(t, s, 1, "thing.go")
			},
			task:    1,
			remove:  []string{"nope.go"},
			summary: "change approach of task 1",
			refusal: "nope.go is not one of task 1's files",
		},
		{
			name: "a file both added and removed",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				ladderApproachOwn(t, s, 1, "thing.go")
			},
			task:    1,
			files:   []string{"b.go"},
			remove:  []string{"b.go"},
			summary: "change approach of task 1",
			refusal: "file b.go is named twice",
		},
		{
			name: "a file an open task owns",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				ladderApproachOwn(t, s, 1, "thing.go")
				if _, err := s.fixtureExec(context.Background(), `INSERT INTO epic_tasks (epic, seq, turn, title,
					deliverable, status) VALUES (?, 2, 1, 'Other', 'the other change', 'open')`, climbEpic); err != nil {
					t.Fatalf("insert task 2: %v", err)
				}
				ladderApproachOwn(t, s, 2, "held.go")
			},
			task:    1,
			files:   []string{"held.go"},
			summary: "change approach of task 1",
			refusal: "held.go is owned by open T2",
		},
		{
			name: "a change that would leave the task no files",
			seed: func(t *testing.T, s *Store) {
				t.Helper()
				ladderApproachOnRung5(t, s, nil)
				ladderApproachOwn(t, s, 1, "thing.go")
			},
			task:    1,
			remove:  []string{"thing.go"},
			summary: "change approach of task 1",
			refusal: "task 1 of epic " + climbEpic + " would be left no files",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			s := openEpicStore(t)
			c.seed(t, s)
			versions := ladderApproachVersions(t, s)
			action := ladderApproachFilesAction(t, s, c.task, c.files, c.remove)

			stop, err := s.CarryOutAction(ctx, action, closeAt())
			if err != nil || stop != nil {
				t.Fatalf("CarryOutAction = %v, %v; want no steps and no error for a refusal", stop, err)
			}
			requireRefusedAction(t, s, action, c.summary, c.refusal)
			if got := ladderApproachVersions(t, s); got != versions {
				t.Errorf("versions of epic %s = %d after the refusal, want %d", climbEpic, got, versions)
			}
		})
	}
}
