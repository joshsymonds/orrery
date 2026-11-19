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
// guardCapture is the capture file R1's item runs, which the guard tests change to refuse a commit.
const (
	guardTestFile = "thing_test.go"
	guardCapture  = "scripts/check"
)

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

// guardCaptureSrc is guardCapture at the base, and guardCaptureChanged is the same file at the implementer's commit.
const (
	guardCaptureSrc     = "#!/bin/sh\nexit 0\n"
	guardCaptureChanged = "#!/bin/sh\nexit 1\n"
)

// guardRefusal is the refusal the commit of guardCaptureChanged earns against the base.
const guardRefusal = "guard refused: scripts/check: capture file changed"

// newGuardFixture returns a fixture whose epic has one Requirement, R1, whose test item runs guardCapture and
// guardTestFile. Its base commit holds both, with TestThing making two assertions, and task 1 lists testChanges as its
// test changes.
func newGuardFixture(t *testing.T, testChanges ...string) *passFixture {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"))
	base := guardCommitFiles(t, setup.epic.Repository, map[string]string{
		guardTestFile: guardTwoAsserts, guardCapture: guardCaptureSrc,
	})
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

// guardRequirementItem is the test item of R1: a test that runs guardCapture and guardTestFile.
func guardRequirementItem() evidence.Item {
	return evidence.Item{
		Capture: []string{guardCapture}, Limit: 60, Conclusion: "TestThing holds", Judge: "the judge",
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
				Capture: []string{guardCapture}, Limit: 60, Conclusion: "the thing holds", Judge: "the judge",
			},
		}},
		Outline: "One task",
		Outcome: "work",
	}
}

// guardCommitFiles writes each of files, a map from path to content, in the checkout dir, commits them on dir's HEAD,
// and returns the commit. An implementer commits in its own worktree, so a step's commit is made there and the primary
// checkout's HEAD never moves under the step, which would be a ref move the build parks on.
func guardCommitFiles(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for path, src := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		buildRun(t, dir, "add", path)
	}
	buildRun(t, dir, "commit", "-q", "-m", "change")
	return buildHead(t, dir)
}

// guardCommitIn writes src as guardTestFile in the checkout dir and commits it, as guardCommitFiles does.
func guardCommitIn(t *testing.T, dir, src string) string {
	t.Helper()
	return guardCommitFiles(t, dir, map[string]string{guardTestFile: src})
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

// TestGuardRefusesChangedCaptureAndMovesFirstAttemptToSecond covers R1: an implementer whose commit changes the capture
// file of the Requirement is refused before green. The attempt is spent, the second attempt's failure and the build's
// guard are the refusal, and no green run starts.
func TestGuardRefusesChangedCaptureAndMovesFirstAttemptToSecond(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	changed := guardCommitFiles(t, worktree, map[string]string{guardCapture: guardCaptureChanged})
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(changed))
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
	changed := guardCommitFiles(t, worktree, map[string]string{guardCapture: guardCaptureChanged})
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(changed))
	f.pass()
	before := ladderClimbBefore(f, 2)
	f.setStep(passStep(StageImplement, 2, 0), "done", guardDone(changed))
	f.pass()
	if task := f.task(); task.Build.Failure != guardRefusal {
		t.Errorf("task failure %q, want %q", task.Build.Failure, guardRefusal)
	}
	ladderClimbCheck(f, store.RungOf(before), store.MovedByUnit)
	if got := guardGreenRuns(f); got != 0 {
		t.Errorf("green runs started = %d, want none", got)
	}
}

// TestGuardTestChangesLiftTheGuardAndMoveToGreen covers R2: a commit that changes the capture file of a task that lists
// it as a test change is not refused, and the build moves to green at the commit.
func TestGuardTestChangesLiftTheGuardAndMoveToGreen(t *testing.T) {
	t.Parallel()
	f := newGuardFixture(t, guardCapture)
	guardStartImplementer(f)
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	changed := guardCommitFiles(t, worktree, map[string]string{guardCapture: guardCaptureChanged})
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(changed))
	f.pass()
	f.expectGreenAt(changed)
}

// TestGuardFactsCommitsPassTheGuard covers G18: a commit that removes a test, lowers an assertion, adds a skip, deletes a
// guarded test file or leaves one unreadable passes the guard. The build moves to green at the commit, and the facts are
// left for the task's floor reviewer, so the build stores no guard refusal.
func TestGuardFactsCommitsPassTheGuard(t *testing.T) {
	t.Parallel()
	const (
		otherTestOnly = "package thing\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) { t.Fatal(\"a\") }\n"
		skipped       = "package thing\n\nimport \"testing\"\n\nfunc TestThing(t *testing.T) { t.Fatal(\"a\"); t.Error(\"b\"); t.Skip(\"slow\") }\n"
		unreadable    = "package thing\n\nfunc ("
	)
	tests := []struct {
		name   string
		commit func(t *testing.T, dir string) string
	}{
		{name: "assertion lowered", commit: func(t *testing.T, dir string) string {
			return guardCommitIn(t, dir, guardOneAssert)
		}},
		{name: "test removed", commit: func(t *testing.T, dir string) string {
			return guardCommitIn(t, dir, otherTestOnly)
		}},
		{name: "skip added", commit: func(t *testing.T, dir string) string {
			return guardCommitIn(t, dir, skipped)
		}},
		{name: "guarded file deleted", commit: func(t *testing.T, dir string) string {
			buildRun(t, dir, "rm", "-q", guardTestFile)
			buildRun(t, dir, "commit", "-q", "-m", "change")
			return buildHead(t, dir)
		}},
		{name: "test file unreadable", commit: func(t *testing.T, dir string) string {
			return guardCommitIn(t, dir, unreadable)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newGuardFixture(t)
			guardStartImplementer(f)
			worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
			commit := tc.commit(t, worktree)
			f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(commit))
			f.pass()
			f.expectGreenAt(commit)
			if text := f.task().Build.Guard; text != "" {
				t.Errorf("build guard = %q, want none", text)
			}
		})
	}
}
