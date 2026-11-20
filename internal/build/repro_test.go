package build

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
)

// reproThing is the non-test file of the reproduction fixture. The base holds reproThingBase, and the implementer's
// commit changes it to reproThingChanged. reproTestFile is the test file the commit adds and the task's test item runs,
// with the content reproTestSource.
const (
	reproThing        = "thing.go"
	reproThingBase    = "package thing\n\nconst Name = \"base\"\n"
	reproThingChanged = "package thing\n\nconst Name = \"changed\"\n"
	reproTestFile     = "thing_test.go"
	reproTestSource   = "package thing\n\nimport \"testing\"\n\nfunc TestName(t *testing.T) { t.Fatal(Name) }\n"
)

// reproChecks is a Checks that records each check it is asked to run and stores it as the store does, running. A repeat
// under an id the store holds returns the stored check, as NodeChecks does.
type reproChecks struct {
	store *store.Store
	runs  []store.NodeCheck
}

func (f *reproChecks) Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error) {
	f.runs = append(f.runs, check)
	if err := f.store.AddNodeCheck(ctx, check, passClock()); err != nil {
		return store.NodeCheck{}, err
	}
	return f.store.NodeCheck(ctx, check.ID)
}

// reproPlan is the planner's return for one task, task 1, whose evidence is a test item that runs reproTestFile.
func reproPlan() store.EpicPlan {
	return store.EpicPlan{
		Tasks: []store.EpicTaskInput{{
			Title: "Build the thing", Deliverable: "The thing holds", Files: []string{reproThing, reproTestFile},
			Requirements: []string{"R1"}, Gate: "check",
			Evidence: evidence.Item{
				Capture: []string{"scripts/check"}, Limit: 60, Conclusion: "the thing holds", Judge: "the judge",
				Kind: evidence.KindTest, Tests: []string{reproTestFile},
			},
		}},
		Outline: "One task",
		Outcome: "work",
	}
}

// newReproFixture returns a fixture whose epic has task 1 planned as reproPlan at a base that holds reproThing, with the
// checks the runner runs its reproductions through.
func newReproFixture(t *testing.T) (*passFixture, *reproChecks) {
	t.Helper()
	ctx := context.Background()
	setup := newStartSetup(t, readoptCommandGate("check"))
	if err := os.WriteFile(
		filepath.Join(setup.epic.Repository, reproThing),
		[]byte(reproThingBase),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	buildRun(t, setup.epic.Repository, "add", reproThing)
	buildRun(t, setup.epic.Repository, "commit", "-q", "-m", "base")
	base := buildHead(t, setup.epic.Repository)
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
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if _, err = s.ClaimTurn(ctx, passEpic, passClock()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
	if err = s.CompleteTurn(ctx, passEpic, 1, reproPlan(), passClock()); err != nil {
		t.Fatalf("CompleteTurn: %v", err)
	}
	steps := &passSteps{rows: make(map[string]store.StepRow)}
	fakeEvidence := &passEvidence{store: s}
	checks := &reproChecks{store: s}
	return &passFixture{
		t: t, ctx: ctx, store: s, dbPath: path, repo: setup.epic.Repository, base: base, stateDir: setup.stateDir,
		rolesPath: setup.rolesPath, getenv: setup.getenv, steps: steps, evidence: fakeEvidence,
		runner: New(Config{
			Store: s, Steps: steps, Evidence: fakeEvidence, Checks: checks, Getenv: setup.getenv,
			StateDir: setup.stateDir, Now: passClock,
		}),
	}, checks
}

// reproCommit writes reproTestFile and changes reproThing in worktree, commits both, and returns the commit. It is the
// implementer's commit of the task.
func reproCommit(t *testing.T, worktree string) string {
	t.Helper()
	for name, src := range map[string]string{reproTestFile: reproTestSource, reproThing: reproThingChanged} {
		if err := os.WriteFile(filepath.Join(worktree, name), []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	buildRun(t, worktree, "add", reproTestFile, reproThing)
	buildRun(t, worktree, "commit", "-q", "-m", "change")
	return buildHead(t, worktree)
}

// toReproGreen runs f's task through red, which fails at the base, and through its implementer, whose commit it returns.
// The guard passes the commit, so the build is at green at the commit, and the pass that moved it has also asked for the
// reproduction.
func toReproGreen(f *passFixture) string {
	f.pass()
	f.addRun(f.lastRun().id, evidence.PhaseRed, f.base, 1, evidence.VerdictFails)
	f.pass()
	worktree := f.addTaskWorktree(passStep(StageImplement, 1, 0))
	commit := reproCommit(f.t, worktree)
	f.setStep(passStep(StageImplement, 1, 0), "done", guardDone(commit))
	f.pass()
	return commit
}

// endRepro ends the reproduction check the runner started with exit, as the engine does when the capture exits. It fails
// the test when the runner started no reproduction.
func endRepro(t *testing.T, f *passFixture, checks *reproChecks, exit int) {
	t.Helper()
	if len(checks.runs) != 1 {
		t.Fatalf("reproduction checks started = %d, want the one reproduction to end", len(checks.runs))
	}
	if err := f.store.EndNodeCheck(f.ctx, checks.runs[0].ID, store.NodeCheckEnd{Exit: exit}, passClock()); err != nil {
		t.Fatalf("EndNodeCheck: %v", err)
	}
}

// TestGreenReproductionStartsCheckAtOverlayOfBase covers R3: a test item's task at green starts one reproduction check
// under the repro id, at a commit whose parent is the task base. The commit's tree holds its test file and the base's
// other files, and the repro ref names it. A pass that finds the check running starts nothing more.
func TestGreenReproductionStartsCheckAtOverlayOfBase(t *testing.T) {
	t.Parallel()
	f, checks := newReproFixture(t)
	commit := toReproGreen(f)
	if len(checks.runs) != 1 {
		t.Fatalf("reproduction checks started = %d, want one", len(checks.runs))
	}
	id := RunID(passEpic, 1, unitRepro, 1, 0)
	check := checks.runs[0]
	want := store.NodeCheck{
		ID: id, Epic: passEpic, Path: "T1", Revision: check.Revision, Argv: []string{"scripts/check"}, Limit: 60,
	}
	if !reflect.DeepEqual(check, want) {
		t.Errorf("reproduction check = %+v, want %+v", check, want)
	}
	overlay := check.Revision
	if parent := buildRun(t, f.repo, "rev-parse", overlay+"^"); parent != f.base {
		t.Errorf("overlay parent = %s, want the task base %s", parent, f.base)
	}
	if files := buildRun(t, f.repo, "ls-tree", "-r", "--name-only", overlay); files != reproThing+"\n"+reproTestFile {
		t.Errorf("overlay files = %q, want the base's file and the commit's test file only", files)
	}
	if got, want := buildRun(t, f.repo, "rev-parse", overlay+":"+reproTestFile),
		buildRun(t, f.repo, "rev-parse", commit+":"+reproTestFile); got != want {
		t.Errorf("overlay's %s = %s, want the commit's %s", reproTestFile, got, want)
	}
	if got, want := buildRun(t, f.repo, "rev-parse", overlay+":"+reproThing),
		buildRun(t, f.repo, "rev-parse", f.base+":"+reproThing); got != want {
		t.Errorf("overlay's %s = %s, want the base's %s", reproThing, got, want)
	}
	ref := "refs/orrery/epic/repro/" + id
	if got := buildRun(t, f.repo, "rev-parse", "--verify", ref); got != overlay {
		t.Errorf("%s = %s, want the overlay %s", ref, got, overlay)
	}
	f.pass()
	if len(checks.runs) != 1 {
		t.Errorf("reproduction checks started after a second pass = %d, want the one still running", len(checks.runs))
	}
}

// TestGreenReproductionExitZeroIsANoteAndTheGreenRunStarts: a reproduction that exits 0 is a note on the task's build,
// not a spent attempt. The note is kept once, and the green run starts once: a later pass records no second note and
// starts no second run.
func TestGreenReproductionExitZeroIsANoteAndTheGreenRunStarts(t *testing.T) {
	t.Parallel()
	f, checks := newReproFixture(t)
	toReproGreen(f)
	endRepro(t, f, checks, 0)
	f.pass()
	task := f.task()
	label := taskLabel(task.Seq)
	note := label + " attempt 1 reproduction: exited 0 on the base with the commit's test files, so they " +
		"pass without the rest of the commit"
	if task.Build.Stage != StageGreen || task.Build.Attempt != 1 || !reflect.DeepEqual(task.Notes, []string{note}) {
		t.Fatalf("build stage %q attempt %d notes %q, want green on attempt 1 with the note %q",
			task.Build.Stage, task.Build.Attempt, task.Notes, note)
	}
	if got := guardGreenRuns(f); got != 1 {
		t.Fatalf("green runs started = %d, want 1 once the note is kept", got)
	}
	f.pass()
	if got := f.task().Notes; !reflect.DeepEqual(got, []string{note}) {
		t.Errorf("notes after a second pass = %q, want the one note", got)
	}
	if got := guardGreenRuns(f); got != 1 {
		t.Errorf("green runs started after a second pass = %d, want 1", got)
	}
}

// TestGreenReproductionExitOneStartsGreenRunAtCommit covers R3 and R4: a reproduction that exits 1 at the base holds, so
// the green run starts at the commit.
func TestGreenReproductionExitOneStartsGreenRunAtCommit(t *testing.T) {
	t.Parallel()
	f, checks := newReproFixture(t)
	commit := toReproGreen(f)
	endRepro(t, f, checks, 1)
	f.pass()
	f.expectGreenAt(commit)
}

// TestGreenReproductionWithoutChecksStartsNothing covers R3: a runner with no checks reports that the reproduction cannot
// run, and the task waits at green with its attempt unspent and no green run started.
func TestGreenReproductionWithoutChecksStartsNothing(t *testing.T) {
	t.Parallel()
	f, _ := newReproFixture(t)
	f.runner.checks = nil
	commit := toReproGreen(f)
	build := f.task().Build
	if build.Stage != StageGreen || build.Revision != commit || build.Attempt != 1 || build.Failure != "" {
		t.Errorf("build = %+v, want green at %s on attempt 1 with no failure", build, commit)
	}
	if got := guardGreenRuns(f); got != 0 {
		t.Errorf("green runs started = %d, want none", got)
	}
}
