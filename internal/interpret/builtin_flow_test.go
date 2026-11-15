package interpret

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
)

// builtinWorld is one test's world for the built-in epic: a scratch repository with the epic's branch at base, the epic
// stored pinned to the built-in epic's text, and a runner over the check and evidence fakes. Its logger keeps what a pass
// logs, so a pass that logs an error fails the test.
type builtinWorld struct {
	t      *testing.T
	s      *store.Store
	repo   string
	base   string
	fakes  nodeFakes
	runner *Runner
	logs   *bytes.Buffer
}

// newBuiltinWorld returns a world with the test epic stored at its base and pinned to the built-in epic, its branch at
// base and no task.
func newBuiltinWorld(t *testing.T) *builtinWorld {
	t.Helper()
	s, _ := openNodeStore(t)
	repo, base := nodeRepo(t)
	nodeGit(t, repo, "update-ref", integrateBranch, base)
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Build the built-in epic", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Gates: nodeGates(), DoneGates: []string{integrateDoneGate},
		Requirements: nodeRequirements(), Workflow: builtinPin(t),
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	fakes := newNodeFakes(s)
	logs := &bytes.Buffer{}
	runner := runNodes(s, fakes)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	return &builtinWorld{t: t, s: s, repo: repo, base: base, fakes: fakes, runner: runner, logs: logs}
}

// pass runs one pass of the runner, which loads the test epic's pinned flow, and fails the test when it logs an error.
func (w *builtinWorld) pass() {
	w.t.Helper()
	w.runner.Pass(context.Background())
	if w.logs.Len() > 0 {
		w.t.Fatalf("pass logged an error: %s", w.logs.String())
	}
}

// commit writes text to file on top of parent and returns the commit. The checkout is left on main.
func (w *builtinWorld) commit(parent, file, text string) string {
	w.t.Helper()
	nodeGit(w.t, w.repo, "checkout", "-q", "--detach", parent)
	nodeWrite(w.t, w.repo, file, text)
	nodeGit(w.t, w.repo, "add", file)
	nodeGit(w.t, w.repo, "commit", "-q", "-m", "work on "+file)
	head := nodeGit(w.t, w.repo, "rev-parse", "HEAD")
	nodeGit(w.t, w.repo, "checkout", "-q", "main")
	return head
}

// accept stores the build of task seq as built on base at revision, and accepts the task.
func (w *builtinWorld) accept(seq int64, revision string) {
	w.t.Helper()
	ctx := context.Background()
	built := store.TaskBuild{Stage: "green", Base: w.base, Revision: revision}
	if err := w.s.SetTaskBuild(ctx, runEpic, seq, store.TaskBuild{}, built); err != nil {
		w.t.Fatalf("SetTaskBuild %d: %v", seq, err)
	}
	if err := w.s.AcceptTask(ctx, runEpic, seq, built, "reviewed"); err != nil {
		w.t.Fatalf("AcceptTask %d: %v", seq, err)
	}
}

// endRed ends the red capture of R1 at the node run at path as its judge finds the conclusion fails. That is the
// Requirement not met at base, which a red run passes.
func (w *builtinWorld) endRed(path string) {
	w.t.Helper()
	ctx := context.Background()
	id := workID(w.t, w.s, path, "R1")
	if err := w.s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{At: runClock()}); err != nil {
		w.t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	verdict := store.EvidenceVerdict{
		Verdict: evidence.VerdictFails, Passed: evidence.Passed(evidence.PhaseRed, "", 0, evidence.VerdictFails),
		At: runClock(),
	}
	if err := w.s.RecordEvidenceVerdict(ctx, id, verdict); err != nil {
		w.t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// candidate returns the revision the batch at path was claimed at, which is its candidate.
func (w *builtinWorld) candidate(path string) string {
	w.t.Helper()
	run := runAt(w.t, w.s, path)
	if run.Revision == "" {
		w.t.Fatalf("batch %s was not claimed: %+v", path, run)
	}
	return run.Revision
}

// endGate ends the Done gate check of the batch at path with exit 0.
func (w *builtinWorld) endGate(path string) {
	w.t.Helper()
	nodeEndCheck(w.t, w.s, workID(w.t, w.s, path, "done 1"), 0, "")
}

// endCapture ends the capture of R1 of the batch at path as a passing judge holds it.
func (w *builtinWorld) endCapture(path string) {
	w.t.Helper()
	nodeEndEvidence(w.t, w.s, workID(w.t, w.s, path, "capture R1"))
}

// builtinOnePlan returns a planner return of one task on b.go, with outcome.
func builtinOnePlan(outcome string) store.EpicPlan {
	return store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("only", "b.go")}, Outline: "one task",
		Outcome: outcome,
	}
}

// TestBuiltInEpicReachesBuiltThenWaitsAtReview: a database epic pinned to the built-in epic, with one Requirement naming
// an evidence item, runs red at base first: its capture is judged not met, so red passes and the plan starts. The one
// task is planned, built and accepted, and its batch integrates green, so the plan ends built and review starts. Review
// waits: after further passes it is still running, the epic is not parked, and no node after it has started.
func TestBuiltInEpicReachesBuiltThenWaitsAtReview(t *testing.T) {
	t.Parallel()
	w := newBuiltinWorld(t)
	s := w.s

	w.pass()
	w.endRed("red")
	w.pass()
	if red := runAt(t, s, "red"); red.State != store.NodeEnded || red.Outcome != "passed" {
		t.Errorf("red = %+v, want ended passed: the Requirement is not met at base", red)
	}
	wantEvidence := []evidenceCall{{
		id: workID(t, s, "red", "R1"), epic: runEpic, item: "R1", revision: w.base, phase: evidence.PhaseRed,
	}}
	if !reflect.DeepEqual(w.fakes.evidence.calls, wantEvidence) {
		t.Fatalf("evidence runs = %+v, want %+v", w.fakes.evidence.calls, wantEvidence)
	}
	if plan := runAt(t, s, "plan"); plan.State != store.NodeRunning {
		t.Fatalf("plan = %+v, want running once red passed", plan)
	}

	completeTurn(t, s, builtinOnePlan("complete"))
	w.pass()
	if task := runAt(t, s, "plan/build/T1"); task.State != store.NodeRunning || task.Task != 1 {
		t.Fatalf("build of task 1 = %+v, want running", task)
	}

	first := w.commit(w.base, "b.go", "one\n")
	w.accept(1, first)
	w.pass()
	const batch = "plan/S1/integrate"
	candidate := w.candidate(batch)
	w.endGate(batch)
	w.pass()
	w.endCapture(batch)
	w.pass()
	if run := runAt(t, s, batch); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Fatalf("batch = %+v, want ended passed", run)
	}
	if epic := epicOf(t, s); epic.StoredBase != candidate || epic.Candidate != "" {
		t.Fatalf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, candidate)
	}

	completeTurn(t, s, store.EpicPlan{Decisions: []string{}, Outline: "the batch is green", Outcome: "complete"})
	w.pass()
	plan := runAt(t, s, "plan")
	if plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Errorf("plan = %+v, want ended complete", plan)
	}
	if got := epicOf(t, s).Phase; got != "built" {
		t.Errorf("epic phase = %q, want built", got)
	}
	review := runAt(t, s, "review")
	if review.Kind != "review" || review.State != store.NodeRunning {
		t.Errorf("review = %+v, want running", review)
	}

	settled := runsOf(t, s)
	w.pass()
	w.pass()
	if got := runsOf(t, s); !reflect.DeepEqual(got, settled) {
		t.Errorf("node runs changed after review started: %+v, want %+v", got, settled)
	}
	if run := runAt(t, s, "review"); run.State != store.NodeRunning {
		t.Errorf("review = %+v after further passes, want still running", run)
	}
	if parked := epicOf(t, s).Parked; parked != "" {
		t.Errorf("epic parked = %q, want not parked", parked)
	}
	for _, run := range runsOf(t, s) {
		for _, later := range []string{"correct", "conclude", "release"} {
			if run.Path == later || strings.HasPrefix(run.Path, later+"@") {
				t.Errorf("node run %s started after review, want none", run.Path)
			}
		}
	}
}
