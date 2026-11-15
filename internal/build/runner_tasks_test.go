package build

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// passTwoTaskPlan is the planner's return for two tasks, each built at check with the evidence of passPlan.
func passTwoTaskPlan() store.EpicPlan {
	plan := passPlan()
	second := plan.Tasks[0]
	second.Title = "Build the other thing"
	plan.Tasks = append(plan.Tasks, second)
	return plan
}

// newTwoTaskFixture returns a fixture like newPassFixture whose epic has tasks 1 and 2 planned at its base, both open.
func newTwoTaskFixture(t *testing.T) *passFixture {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"), readoptReviewGate())
	path := filepath.Join(t.TempDir(), "orrery.db")
	storetest.Seed(t, path)
	s, err := store.Open(ctx, path, storetest.NoSync())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if err = s.CreateEpic(ctx, store.EpicRow{
		ID: passEpic, Repository: setup.epic.Repository, Base: setup.base, Title: "Build the thing",
		Phase: "accepted", Created: passClock(), Changed: passClock(), Gates: setup.epic.Gates,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if _, err = s.ClaimTurn(ctx, passEpic, passClock()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, passEpic, 1, passTwoTaskPlan(), passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	buildRun(t, setup.epic.Repository, "update-ref", "refs/heads/main", setup.base)
	steps := &passSteps{rows: make(map[string]store.StepRow)}
	fakeEvidence := &passEvidence{store: s}
	return &passFixture{
		t: t, ctx: ctx, store: s, repo: setup.epic.Repository, base: setup.base, stateDir: setup.stateDir,
		rolesPath: setup.rolesPath, getenv: setup.getenv, steps: steps, evidence: fakeEvidence,
		runner: New(Config{
			Store: s, Steps: steps, Evidence: fakeEvidence, Getenv: setup.getenv, StateDir: setup.stateDir,
			Now: passClock,
		}),
	}
}

// TestPassTasksCreatesBranchAndMovesOnlyNamedTask: PassTasks naming open task 2 creates the epic's branch and starts
// task 2's red run under its derived id, and leaves open task 1 unmoved.
func TestPassTasksCreatesBranchAndMovesOnlyNamedTask(t *testing.T) {
	t.Parallel()
	f := newTwoTaskFixture(t)
	f.runner.PassTasks(f.ctx, passEpic, []int64{2})
	if got := f.epic().Branch; got != branchCreated {
		t.Errorf("epic Branch = %q, want %q", got, branchCreated)
	}
	want := passRun{id: RunID(passEpic, 2, StageRed, 0, 0), epic: passEpic, seq: 2, revision: f.base, phase: StageRed}
	if got := f.evidence.runs; !slices.Equal(got, []passRun{want}) {
		t.Fatalf("RunTask calls = %+v, want [%+v]", got, want)
	}
	epic := f.epic()
	if task, _ := taskOf(epic, 1); task.Build != (store.TaskBuild{}) {
		t.Errorf("task 1 build = %+v, want unmoved", task.Build)
	}
	if task, _ := taskOf(epic, 2); task.Build.Stage != StageRed || task.Build.Base != f.base {
		t.Errorf("task 2 build = %+v, want red at the base %s", task.Build, f.base)
	}
}

// TestPassTasksNamingNoOpenTaskMovesNothing: PassTasks naming a seq that is not an open task of the epic starts no run
// and moves no task.
func TestPassTasksNamingNoOpenTaskMovesNothing(t *testing.T) {
	t.Parallel()
	f := newTwoTaskFixture(t)
	f.runner.PassTasks(f.ctx, passEpic, []int64{9})
	if got := len(f.evidence.runs); got != 0 {
		t.Errorf("RunTask calls = %d, want none for a seq that is no open task", got)
	}
	for _, task := range f.epic().Tasks {
		if task.Build != (store.TaskBuild{}) {
			t.Errorf("task %d build = %+v, want unmoved", task.Seq, task.Build)
		}
	}
}
