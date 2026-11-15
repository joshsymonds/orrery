package build

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// errSyntheticCrash is the error a store that crashes at one write returns from it.
var errSyntheticCrash = errors.New("synthetic crash")

// crashBeforeMarkStore is a store whose SetEpicBranch fails when it is asked for state, as a crash between the write
// that creates an epic's branch and the write that marks the branch created leaves it.
type crashBeforeMarkStore struct {
	*store.Store
	state string
}

func (s crashBeforeMarkStore) SetEpicBranch(ctx context.Context, epic, state string) error {
	if state == s.state {
		return errSyntheticCrash
	}
	return s.Store.SetEpicBranch(ctx, epic, state)
}

// crashBeforeAcceptStore is a store whose AcceptTask fails, as a crash between a task's pin and its accept leaves it.
type crashBeforeAcceptStore struct {
	*store.Store
}

func (crashBeforeAcceptStore) AcceptTask(context.Context, string, int64, store.TaskBuild, string) error {
	return errSyntheticCrash
}

// keyedState is what a repeated write or pass must leave unchanged: the epic as stored, with its tasks and their
// reviews, and the tells the owner was given.
type keyedState struct {
	epic  store.EpicRow
	tells []string
}

// keyedStateOf returns the state of the fixture's epic that a repeat must not change.
func (f *passFixture) keyedStateOf() keyedState {
	return keyedState{epic: f.epic(), tells: f.tells()}
}

// runnerOver returns a Runner over s, logging to logger, that shares the fixture's fakes, so the starts and runs it
// makes are counted with the first runner's. A runner over the fixture's store after the first is the runner a restart
// of orreryd leaves.
func (f *passFixture) runnerOver(s Store, logger *slog.Logger) *Runner {
	return New(Config{
		Store: s, Steps: f.steps, Evidence: f.evidence, Getenv: f.getenv, StateDir: f.stateDir, Now: passClock,
		Logger: logger,
	})
}

// startCounts returns how many times the fixture's steps were started, under each id.
func (f *passFixture) startCounts() map[string]int {
	counts := make(map[string]int)
	for _, start := range f.steps.starts {
		counts[start.ID]++
	}
	return counts
}

// runCounts returns how many times the fixture's evidence was asked to start each run, under each id.
func (f *passFixture) runCounts() map[string]int {
	counts := make(map[string]int)
	for _, run := range f.evidence.runs {
		counts[run.id]++
	}
	return counts
}

// TestKeyedRedRunFoundByItsIDAfterRestartIsNotStartedAgain covers R2: a red run started under its derived id is found
// by that id on a runner rebuilt from the store, and the pass starts no second run.
func TestKeyedRedRunFoundByItsIDAfterRestartIsNotStartedAgain(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.pass()
	red := passRunID(StageRed, 0)
	want := map[string]int{red: 1}
	if got := f.runCounts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RunTask counts after the first pass = %v, want %v", got, want)
	}
	runBefore, err := f.store.EvidenceRun(f.ctx, red)
	if err != nil {
		t.Fatalf("EvidenceRun: %v", err)
	}
	before := f.keyedStateOf()

	f.runnerOver(f.store, nil).pass(f.ctx)

	if got := f.runCounts(); !reflect.DeepEqual(got, want) {
		t.Errorf("RunTask counts after the rebuilt pass = %v, want %v", got, want)
	}
	runAfter, err := f.store.EvidenceRun(f.ctx, red)
	if err != nil || !reflect.DeepEqual(runAfter, runBefore) {
		t.Errorf("evidence run after the rebuilt pass = %+v, %v, want %+v", runAfter, err, runBefore)
	}
	if got := f.keyedStateOf(); !reflect.DeepEqual(got, before) {
		t.Errorf("state after the rebuilt pass = %+v, want %+v", got, before)
	}
}

// TestKeyedImplementerStepFoundByItsIDAfterRecordLostIsNotStartedAgain covers R2: an implementer step whose record the
// build lost is found by its derived id on a runner rebuilt from the store, and no second step starts.
func TestKeyedImplementerStepFoundByItsIDAfterRecordLostIsNotStartedAgain(t *testing.T) {
	t.Parallel()
	f := newImplementerFixture(t)
	id := passStep(StageImplement, 1, 0)
	build := f.task().Build
	build.Step = ""
	f.setBuild(build)
	want := map[string]int{id: 1}
	if got := f.startCounts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Start counts before the rebuilt pass = %v, want %v", got, want)
	}
	before := f.keyedStateOf()

	f.runnerOver(f.store, nil).pass(f.ctx)

	if got := f.startCounts(); !reflect.DeepEqual(got, want) {
		t.Errorf("Start counts after the rebuilt pass = %v, want %v", got, want)
	}
	if got := f.keyedStateOf(); !reflect.DeepEqual(got, before) {
		t.Errorf("state after the rebuilt pass = %+v, want %+v", got, before)
	}
}

// TestKeyedBranchCreatedBeforeItsCreatedMarkIsAdoptedNotMadeAgain covers R3: an epic branch created before the write
// that marks it created is adopted by a runner rebuilt from the store, which neither parks the epic nor makes the
// branch again.
func TestKeyedBranchCreatedBeforeItsCreatedMarkIsAdoptedNotMadeAgain(t *testing.T) {
	t.Parallel()
	f := newPassFixture(t)
	f.runnerOver(crashBeforeMarkStore{Store: f.store, state: branchCreated}, nil).pass(f.ctx)
	if got := f.epic().Branch; got != branchCreating {
		t.Fatalf("epic Branch after the crash = %q, want %q", got, branchCreating)
	}

	f.runnerOver(f.store, nil).pass(f.ctx)

	if got := f.epic(); got.Branch != branchCreated || got.Parked != "" {
		t.Errorf("epic Branch = %q, Parked = %q, want created and not parked", got.Branch, got.Parked)
	}
	if got := buildRun(t, f.repo, "rev-parse", BranchRef(passEpic)); got != f.base {
		t.Errorf("epic branch = %s, want the base %s", got, f.base)
	}
	want := map[string]int{passRunID(StageRed, 0): 1}
	if got := f.runCounts(); !reflect.DeepEqual(got, want) {
		t.Errorf("RunTask counts = %v, want %v", got, want)
	}
}

// reviewStageFixture returns a fixture whose task is at review, after its green run passed at the base, with
// reviewGates as the epic's review gates.
func reviewStageFixture(t *testing.T, reviewGates ...string) *passFixture {
	t.Helper()
	f := newPassFixture(t, reviewGates...)
	f.setGreen(1, evidence.VerdictHolds)
	f.setBuild(store.TaskBuild{
		Stage: StageReview, Base: f.base, Attempt: 1, Revision: f.base, GreenRun: passRunID(StageGreen, 1),
	})
	return f
}

// redHoldsFixture returns a fixture whose task's red run ended at the base with its conclusion holding.
func redHoldsFixture(t *testing.T) *passFixture {
	t.Helper()
	f := newPassFixture(t)
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictHolds)
	return f
}

// reviewDoneFixture returns a fixture whose task is at review of gate review, whose review step ended done.
func reviewDoneFixture(t *testing.T) *passFixture {
	t.Helper()
	f := reviewStageFixture(t, "review")
	f.setStep(passStep(StageReview, 1, 0), "done", "")
	return f
}

// findingDroppedFixture returns a fixture whose task is verifying its first finding, whose verifier ended done with the
// finding dropped.
func findingDroppedFixture(t *testing.T) *passFixture {
	t.Helper()
	f := newVerifyingFixture(t)
	f.setStep(passStep(StageVerify, 1, 0), "done",
		`{"results":[{"evidence":"the claim does not hold","result":"dropped"}]}`)
	return f
}

// TestKeyedRepeatedWritesFromStateBeforeChangeNoRow covers R3: a move, fail, accept, review or finding written from the
// state its task was in before it, repeated on a runner rebuilt from the store, is refused and changes no row.
func TestKeyedRepeatedWritesFromStateBeforeChangeNoRow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		setup  func(t *testing.T) *passFixture
		moveOn func(f *passFixture)
		want   ActionKind
	}{
		{
			name: "move",
			setup: func(t *testing.T) *passFixture {
				t.Helper()
				return newPassFixture(t)
			},
			// The build moves on to implement, so a repeat of the move from fresh would undo it if it landed.
			moveOn: func(f *passFixture) {
				f.setBuild(store.TaskBuild{Stage: StageImplement, Base: f.base, Attempt: 1, Revision: f.base})
			},
			want: ActMove,
		},
		{name: "fail", setup: redHoldsFixture, want: ActFail},
		{
			name: "accept",
			setup: func(t *testing.T) *passFixture {
				t.Helper()
				return reviewStageFixture(t)
			},
			want: ActAccept,
		},
		{name: "review", setup: reviewDoneFixture, want: ActReview},
		{name: "finding", setup: findingDroppedFixture, want: ActFinding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.setup(t)
			view, err := f.runner.view(f.ctx, f.epic(), f.task(), f.base)
			if err != nil {
				t.Fatalf("view: %v", err)
			}
			action := Decide(view)
			if action.Kind != tc.want {
				t.Fatalf("Decide = %s, want %s", action.Kind, tc.want)
			}
			initial := f.keyedStateOf()
			if !f.runner.execute(f.ctx, f.epic(), view, action) {
				t.Fatalf("%s from the state before it was refused, want it to land", action.Kind)
			}
			if reflect.DeepEqual(f.keyedStateOf(), initial) {
				t.Fatalf("%s changed no row, so a repeat of it proves nothing", action.Kind)
			}
			if tc.moveOn != nil {
				tc.moveOn(f)
			}
			before := f.keyedStateOf()
			logs := new(bytes.Buffer)
			repeat := f.runnerOver(f.store, slog.New(slog.NewTextHandler(logs, nil)))

			if repeat.execute(f.ctx, f.epic(), view, action) {
				t.Errorf("repeat of %s from the state before it landed, want it refused", action.Kind)
			}
			if !strings.Contains(logs.String(), store.ErrTaskMoved.Error()) {
				t.Errorf("repeat of %s logged %q, want the refusal %q", action.Kind, logs.String(), store.ErrTaskMoved)
			}
			if got := f.keyedStateOf(); !reflect.DeepEqual(got, before) {
				t.Errorf("repeat of %s changed rows: state %+v, want %+v", action.Kind, got, before)
			}
		})
	}
}

// TestKeyedPinLeftBeforeAcceptIsRepinnedAndTaskAcceptedOnce covers R3: a pin a crash left before its task's accept is
// written again by a runner rebuilt from the store, the task is accepted once, and a later pass changes no row.
func TestKeyedPinLeftBeforeAcceptIsRepinnedAndTaskAcceptedOnce(t *testing.T) {
	t.Parallel()
	f := reviewStageFixture(t)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	const ref = "refs/orrery/epic/T1"

	f.runnerOver(crashBeforeAcceptStore{Store: f.store}, nil).pass(f.ctx)
	if got := buildRun(t, f.repo, "rev-parse", ref); got != f.base {
		t.Fatalf("%s after the crash = %s, want the pin %s", ref, got, f.base)
	}
	if got := f.task().Status; got != taskOpen {
		t.Fatalf("task status after the crash = %q, want %q", got, taskOpen)
	}

	f.runnerOver(f.store, nil).pass(f.ctx)

	task := f.task()
	if task.Status != "done" || task.Build.Review != ReviewUnreviewed {
		t.Errorf("task status %q review %q, want done and unreviewed", task.Status, task.Build.Review)
	}
	if got := buildRun(t, f.repo, "rev-parse", ref); got != f.base {
		t.Errorf("%s = %s, want the pin %s", ref, got, f.base)
	}
	f.expectNoWorktrees(worktree)
	before := f.keyedStateOf()

	f.runnerOver(f.store, nil).pass(f.ctx)

	if got := f.keyedStateOf(); !reflect.DeepEqual(got, before) {
		t.Errorf("a pass after the accept changed rows: state %+v, want %+v", got, before)
	}
}
