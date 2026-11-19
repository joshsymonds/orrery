package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// guardTestFile is the test file the guard tests change: R1's test item runs it, and a task may list it as a test change.
const guardTestFile = "thing_test.go"

// guardTwoAsserts is guardTestFile at the base, where TestThing makes two assertions. guardOneAssert is the same file at
// the implementer's commit, which lowers TestThing to one.
const (
	guardTwoAsserts = `package thing

import "testing"

func TestThing(t *testing.T) { t.Fatal("a"); t.Error("b") }
`
	guardOneAssert = `package thing

import "testing"

func TestThing(t *testing.T) { t.Fatal("a") }
`
)

// guardRefusal is the refusal the commit of guardOneAssert earns against the base.
const guardRefusal = "guard refused: thing_test.go: TestThing: assertions lowered from 2 to 1"

// newGuardFixture returns a fixture whose epic has one Requirement, R1, whose test item runs guardTestFile. Its base
// commit holds guardTestFile with TestThing making two assertions, and task 1 lists testChanges as its test changes.
func newGuardFixture(t *testing.T, testChanges ...string) *passFixture {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"))
	base := guardCommitIn(t, setup.epic.Repository, guardTwoAsserts)
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
		ID: passEpic, Repository: setup.epic.Repository, Base: base, Title: "Build the thing",
		Phase: "accepted", Created: passClock(), Changed: passClock(), Gates: setup.epic.Gates,
		Requirements: []store.EpicRequirement{{
			ID: "R1", Text: "TestThing holds", Evidence: "TestThing", Kind: "test", Item: guardRequirementItem(),
		}},
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if _, err = s.ClaimTurn(ctx, passEpic, passClock()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, passEpic, 1, guardPlan(testChanges), passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	steps := &passSteps{rows: make(map[string]store.StepRow)}
	fakeEvidence := &passEvidence{store: s}
	return &passFixture{
		t: t, ctx: ctx, store: s, dbPath: path, repo: setup.epic.Repository, base: base, stateDir: setup.stateDir,
		rolesPath: setup.rolesPath, getenv: setup.getenv, steps: steps, evidence: fakeEvidence,
		runner: New(Config{
			Store: s, Steps: steps, Evidence: fakeEvidence, Getenv: setup.getenv, StateDir: setup.stateDir,
			Now: passClock,
		}),
	}
}

// guardRequirementItem is the test item of R1: a test that runs guardTestFile.
func guardRequirementItem() evidence.Item {
	return evidence.Item{
		Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "TestThing holds", Judge: "the judge",
		Kind: evidence.KindTest, Tests: []string{guardTestFile},
	}
}

// guardPlan is the planner's return for task 1, which touches guardTestFile and lists testChanges as its test changes.
func guardPlan(testChanges []string) store.EpicPlan {
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Build the thing", Deliverable: "The thing holds", Files: []string{"thing.go", guardTestFile},
			Requirements: []string{"R1"}, TestChanges: testChanges, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the thing holds", Judge: "the judge",
			},
		}},
		Outline: "One task",
		Outcome: "work",
	}
}

// guardCommitIn writes src as guardTestFile in the checkout dir, commits it on dir's HEAD, and returns the commit. An
// implementer commits in its own worktree, so a step's commit is made there and the primary checkout's HEAD never moves
// under the step, which would be a ref move the build parks on.
func guardCommitIn(t *testing.T, dir, src string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, guardTestFile), []byte(src), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	buildRun(t, dir, "add", guardTestFile)
	buildRun(t, dir, "commit", "-q", "-m", "change")
	return buildHead(t, dir)
}

// guardDone is the kept return of an implementer that finished with commit.
func guardDone(commit string) string {
	return `{"summary":"done","commit":"` + commit + `"}`
}

// guardStartImplementer runs task 1 through red, which fails at the base, so its implementer starts at attempt 1.
func guardStartImplementer(f *passFixture) {
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
}

// guardGreenRuns returns how many green evidence runs the runner started.
func guardGreenRuns(f *passFixture) int {
	n := 0
	for _, run := range f.evidence.runs {
		if run.phase == StageGreen {
			n++
		}
	}
	return n
}

// TestGuardRefusesLoweredAssertionAndMovesFirstAttemptToSecond covers R1: an implementer whose commit lowers an assertion
// of a guarded test file is refused before green. The attempt is spent, the second attempt's failure and the build's
// guard are the refusal, and no green run starts.
func TestGuardRefusesLoweredAssertionAndMovesFirstAttemptToSecond(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	lowered := guardCommitIn(t, worktree, guardOneAssert)
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(lowered))
	f.pass()
	build := f.task().Build
	refused := build.Failure == guardRefusal && build.Guard == guardRefusal
	if build.Stage != StageImplement || build.Attempt != 2 || !refused {
		t.Errorf("build stage %q attempt %d failure %q guard %q, want implement at attempt 2 with the refusal as both",
			build.Stage, build.Attempt, build.Failure, build.Guard)
	}
	if got := guardGreenRuns(f); got != 0 {
		t.Errorf("green runs started = %d, want none", got)
	}
}

// TestGuardRefusalOnSecondAttemptClimbsTask covers R1: a refusal of the second attempt's commit climbs the task with
// the refusal as its failure, and no green run starts.
func TestGuardRefusalOnSecondAttemptClimbsTask(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	lowered := guardCommitIn(t, worktree, guardOneAssert)
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(lowered))
	f.pass()
	before := ladderClimbBefore(f, 2)
	f.setStep(passStep(StageImplement, 2, 0), "done", guardDone(lowered))
	f.pass()
	if task := f.task(); task.Build.Failure != guardRefusal {
		t.Errorf("task failure %q, want %q", task.Build.Failure, guardRefusal)
	}
	ladderClimbCheck(f, store.RungOf(before), taskOpen, store.MovedByUnit)
	if got := guardGreenRuns(f); got != 0 {
		t.Errorf("green runs started = %d, want none", got)
	}
}

// TestGuardTestChangesLiftTheGuardAndMoveToGreen covers R2: a commit that lowers an assertion of a file the task lists
// as a test change is not refused, and the build moves to green at the commit.
func TestGuardTestChangesLiftTheGuardAndMoveToGreen(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t, guardTestFile)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	lowered := guardCommitIn(t, worktree, guardOneAssert)
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(lowered))
	f.pass()
	f.expectGreenAt(lowered)
}
