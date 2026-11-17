package interpret

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// integrateBranch is the ref of the test epic's branch, which the batches advance.
const integrateBranch = "refs/heads/" + nodeBranchName

// integrateBatch is the path of the integrate run that batches the two tasks of the first turn: the settle walk of task 2.
const integrateBatch = "plan/S2/integrate"

// integrateNextBatch is the path of the integrate run that batches the two tasks of a second turn: the settle walk of
// task 4.
const integrateNextBatch = "plan/S4/integrate"

// integrateDoneGate is the Done gate of the test epic. nodeGates defines it.
const integrateDoneGate = "lint"

// integrateFlow is the flow the integrate tests run: a plan whose body builds each task and then integrates the batch of
// accepted tasks. Integrate sits in a plan body, so it is walked under the plan's settle walks.
func integrateFlow() workflow.Workflow {
	return workflow.Workflow{Name: "integrate", Nodes: []workflow.Node{{
		ID: "plan", Kind: kindPlan, Body: []workflow.Node{
			{ID: "build", Kind: kindBuild},
			{ID: "integrate", Kind: kindIntegrate},
		},
	}}}
}

// integrateOptions are the choices a test world makes beyond its defaults.
type integrateOptions struct {
	pin       store.EpicWorkflow
	viaRunner bool
}

// integrateOption sets one of integrateOptions before the test epic is stored.
type integrateOption func(*integrateOptions)

// pinnedTo pins the test epic to pin, the workflow it runs.
func pinnedTo(pin store.EpicWorkflow) integrateOption {
	return func(o *integrateOptions) { o.pin = pin }
}

// viaRunnerPass makes each pass of the world go through Runner.Pass, which loads the epic's pinned workflow, rather than
// carrying integrateFlow out.
func viaRunnerPass() integrateOption {
	return func(o *integrateOptions) { o.viaRunner = true }
}

// integrateWorld is one integrate test's world: a scratch repository with the epic's branch at base, the epic stored on
// it with its Done gate and Requirement, and a runner over the check and evidence fakes. Its logger keeps what a pass
// logs, so a pass that logs an error fails the test.
type integrateWorld struct {
	t         *testing.T
	s         *store.Store
	path      string
	repo      string
	base      string
	fakes     nodeFakes
	runner    *Runner
	logs      *bytes.Buffer
	viaRunner bool
}

// newIntegrateWorld returns a world with the test epic stored at its base, its branch at base and no task. The options
// apply to the epic and its passes.
func newIntegrateWorld(t *testing.T, opts ...integrateOption) *integrateWorld {
	t.Helper()
	var o integrateOptions
	for _, opt := range opts {
		opt(&o)
	}
	s, path := openNodeStore(t)
	repo, base := nodeRepo(t)
	nodeGit(t, repo, "update-ref", integrateBranch, base)
	if err := s.CreateEpic(context.Background(), store.EpicRow{
		ID: runEpic, Repository: repo, Base: base, Title: "Integrate the batches", Phase: runPhase,
		Created: runClock(), Changed: runClock(), Gates: nodeGates(), DoneGates: []string{integrateDoneGate},
		Requirements: nodeRequirements(), Workflow: o.pin,
	}); err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	fakes := newNodeFakes(s)
	logs := &bytes.Buffer{}
	runner := runNodes(s, fakes)
	runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	return &integrateWorld{
		t: t, s: s, path: path, repo: repo, base: base, fakes: fakes, runner: runner, logs: logs,
		viaRunner: o.viaRunner,
	}
}

// pass carries one pass out on the test epic: through Runner.Pass when the world is viaRunner, otherwise by carrying the
// integrate flow out.
func (w *integrateWorld) pass() {
	w.t.Helper()
	if w.viaRunner {
		w.runner.Pass(context.Background())
	} else {
		w.runner.passFlow(context.Background(), epicOf(w.t, w.s), integrateFlow())
	}
	if w.logs.Len() > 0 {
		w.t.Fatalf("pass logged an error: %s", w.logs.String())
	}
}

// git runs git in the test repository and returns its trimmed output.
func (w *integrateWorld) git(args ...string) string {
	w.t.Helper()
	return nodeGit(w.t, w.repo, args...)
}

// commit writes text to file on top of parent and returns the commit. The checkout is left on main.
func (w *integrateWorld) commit(parent, file, text string) string {
	w.t.Helper()
	w.git("checkout", "-q", "--detach", parent)
	nodeWrite(w.t, w.repo, file, text)
	w.git("add", file)
	w.git("commit", "-q", "-m", "work on "+file)
	head := w.git("rev-parse", "HEAD")
	w.git("checkout", "-q", "main")
	return head
}

// planTurn completes a turn that plans tasks 1 and 2, or 3 and 4 after a first turn, and carries their builds out.
func (w *integrateWorld) planTurn() {
	w.t.Helper()
	completeTurn(w.t, w.s, runPlan("complete"))
	w.pass()
}

// accept stores the build of task seq as built on base at revision, and accepts the task.
func (w *integrateWorld) accept(seq int64, base, revision string) {
	w.t.Helper()
	ctx := context.Background()
	built := store.TaskBuild{Stage: "green", Base: base, Revision: revision}
	if err := w.s.SetTaskBuild(ctx, runEpic, seq, store.TaskBuild{}, built); err != nil {
		w.t.Fatalf("SetTaskBuild %d: %v", seq, err)
	}
	if err := w.s.AcceptTask(ctx, runEpic, seq, built, "reviewed"); err != nil {
		w.t.Fatalf("AcceptTask %d: %v", seq, err)
	}
}

// candidate returns the revision the batch at path was claimed at, which is its candidate.
func (w *integrateWorld) candidate(path string) string {
	w.t.Helper()
	run := runAt(w.t, w.s, path)
	if run.Revision == "" {
		w.t.Fatalf("batch %s was not claimed: %+v", path, run)
	}
	return run.Revision
}

// endGate ends the Done gate check of the batch at path with exit and stderr.
func (w *integrateWorld) endGate(path string, exit int, stderr string) {
	w.t.Helper()
	nodeEndCheck(w.t, w.s, workID(w.t, w.s, path, "done 1"), exit, stderr)
}

// endCapture ends the capture of R1 of the batch at path as a passing judge holds it.
func (w *integrateWorld) endCapture(path string) {
	w.t.Helper()
	nodeEndEvidence(w.t, w.s, workID(w.t, w.s, path, "capture R1"))
}

// endRed ends the red capture of R1 at the node run at path as its judge finds the conclusion fails. That is the
// Requirement not met at base, which a red run passes.
func (w *integrateWorld) endRed(path string) {
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

// pending returns how many triggers of the test epic are pending.
func (w *integrateWorld) pending() int {
	w.t.Helper()
	flow, err := w.s.EpicFlow(context.Background(), runEpic)
	if err != nil {
		w.t.Fatalf("EpicFlow: %v", err)
	}
	return flow.Pending
}

// toGate runs the first batch up to its Done gate. The plan starts, two tasks are planned and accepted on the base, and
// the pass that prepares the batch starts the gate. It returns the candidate the batch was claimed at.
func (w *integrateWorld) toGate() string {
	w.t.Helper()
	first := w.commit(w.base, "internal/a/x.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	return w.candidate(integrateBatch)
}

// toCaptured runs the first batch through its Done gate and the capture of R1, both passing, and returns its candidate.
// The pass after it checks the refs and advances the branch.
func (w *integrateWorld) toCaptured() string {
	w.t.Helper()
	candidate := w.toGate()
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	return candidate
}

// requireParked fails unless the batch at integrateBatch is parked, the epic is parked with reason and told it, and the
// pending triggers are still pending as pending counted them before the pass.
func (w *integrateWorld) requireParked(reason string, pending int) {
	w.t.Helper()
	if run := runAt(w.t, w.s, integrateBatch); run.State != store.NodeParked {
		w.t.Errorf("batch = %+v, want parked", run)
	}
	if got := epicOf(w.t, w.s).Parked; got != reason {
		w.t.Errorf("epic parked = %q, want %q", got, reason)
	}
	if tells := nodeTells(w.t, w.path); !slices.Contains(tells, reason) {
		w.t.Errorf("tells = %q, want %q", tells, reason)
	}
	if got := w.pending(); got != pending {
		w.t.Errorf("pending triggers = %d, want %d: a park wakes no planner", got, pending)
	}
}

// TestIntegrateBatchGreenAdvancesBranchAndStoredBase: two accepted tasks compose to two commits on the stored base. The
// Done gate, then the capture of the Requirement, run at the candidate. Once both are green the epic's branch moves to
// the candidate, the stored base follows it, and the planner's trigger is pending.
func TestIntegrateBatchGreenAdvancesBranchAndStoredBase(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	candidate := w.toCaptured()

	wantChecks := []store.NodeCheck{{
		ID: workID(t, w.s, integrateBatch, "done 1"), Epic: runEpic, Path: integrateBatch, Revision: candidate,
		Argv: []string{"./bin/lint"}, Limit: 60, Needs: []store.EpicGateNeed{{Pool: "lint", Count: 2}},
	}}
	if !reflect.DeepEqual(w.fakes.checks.calls, wantChecks) {
		t.Errorf("checks run = %+v, want %+v", w.fakes.checks.calls, wantChecks)
	}
	wantEvidence := []evidenceCall{{
		id: workID(t, w.s, integrateBatch, "capture R1"), epic: runEpic, item: "R1", revision: candidate,
		phase: evidence.PhaseGreen,
	}}
	if !reflect.DeepEqual(w.fakes.evidence.calls, wantEvidence) {
		t.Errorf("evidence runs = %+v, want %+v", w.fakes.evidence.calls, wantEvidence)
	}

	pending := w.pending()
	w.pass()
	run := runAt(t, w.s, integrateBatch)
	if run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("batch = %+v, want ended passed", run)
	}
	if epic := epicOf(t, w.s); epic.StoredBase != candidate || epic.Candidate != "" {
		t.Errorf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want the candidate %s", tip, candidate)
	}
	if got := w.pending(); got != pending+1 {
		t.Errorf("pending triggers = %d, want %d: green wakes the planner once", got, pending+1)
	}
	if got := w.git("rev-list", "--count", w.base+".."+candidate); got != "2" {
		t.Errorf("commits on the stored base = %s, want 2", got)
	}
	if got := w.git("diff", "--name-only", w.base, candidate); got != "b.go\ninternal/a/x.go" {
		t.Errorf("paths the candidate changes over the stored base = %q, want both tasks' paths", got)
	}
	pin := fmt.Sprintf("refs/orrery/epic-runner/candidate/%s-%d", run.ID, run.Attempt)
	if got := w.git("rev-parse", "--verify", pin); got != candidate {
		t.Errorf("pinned candidate = %s, want %s", got, candidate)
	}
}

// TestIntegrateFailedGateKeepsBranchAndNextBatchComposesOnCandidate: a failed Done gate leaves the branch at the stored
// base, stores the candidate as the epic's candidate and wakes the planner. The next batch composes on that candidate.
func TestIntegrateFailedGateKeepsBranchAndNextBatchComposesOnCandidate(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	candidate := w.toGate()
	pending := w.pending()
	w.endGate(integrateBatch, 1, "lint failed")
	w.pass()

	run := runAt(t, w.s, integrateBatch)
	if run.State != store.NodeEnded || run.Outcome != "failed" || run.Output != "Done gate lint failed" {
		t.Errorf("batch = %+v, want ended failed on the failed gate", run)
	}
	if epic := epicOf(t, w.s); epic.StoredBase != w.base || epic.Candidate != candidate {
		t.Errorf("stored base %q and candidate %q, want %q and %q", epic.StoredBase, epic.Candidate, w.base, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != w.base {
		t.Errorf("branch = %s, want the stored base %s", tip, w.base)
	}
	if got := w.pending(); got != pending+1 {
		t.Errorf("pending triggers = %d, want %d: red wakes the planner once", got, pending+1)
	}
	if got := len(w.fakes.evidence.calls); got != 0 {
		t.Errorf("%d evidence runs after a failed gate, want none", got)
	}

	first := w.commit(candidate, "c.go", "three\n")
	second := w.commit(candidate, "d.go", "four\n")
	w.planTurn()
	w.accept(3, candidate, first)
	w.accept(4, candidate, second)
	w.pass()
	next := w.candidate(integrateNextBatch)
	w.endGate(integrateNextBatch, 0, "")
	w.pass()
	w.endCapture(integrateNextBatch)
	w.pass()

	if epic := epicOf(t, w.s); epic.StoredBase != next || epic.Candidate != "" {
		t.Errorf("stored base %q and candidate %q, want %q and none", epic.StoredBase, epic.Candidate, next)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != next {
		t.Errorf("branch = %s, want the second candidate %s", tip, next)
	}
	if got := w.git("rev-list", "--count", candidate+".."+next); got != "2" {
		t.Errorf("commits of the second batch on the first candidate = %s, want 2", got)
	}
	if got := w.git("rev-parse", next+"~2"); got != candidate {
		t.Errorf("second candidate builds on %s, want the first candidate %s", got, candidate)
	}
}

// TestIntegrateMovedOrCheckedOutBranchParksBatch: a branch another commit moved, or one checked out in a worktree, parks
// the batch and the epic with a tell, and the branch does not move. No other ref moves either.
func TestIntegrateMovedOrCheckedOutBranchParksBatch(t *testing.T) {
	t.Parallel()
	t.Run("moved", func(t *testing.T) {
		t.Parallel()
		w := newIntegrateWorld(t)
		w.toCaptured()
		moved := w.commit(w.base, "e.go", "moved\n")
		w.git("update-ref", integrateBranch, moved)
		refs := w.git("for-each-ref")
		pending := w.pending()
		w.pass()

		w.requireParked(fmt.Sprintf("epic/epic-runner moved: at %s, not the stored base %s", moved, w.base), pending)
		if tip := w.git("rev-parse", integrateBranch); tip != moved {
			t.Errorf("branch = %s, want it left at %s", tip, moved)
		}
		if got := w.git("for-each-ref"); got != refs {
			t.Errorf("refs = %q, want them unchanged as %q", got, refs)
		}
	})
	t.Run("checked out in a worktree", func(t *testing.T) {
		t.Parallel()
		w := newIntegrateWorld(t)
		w.toCaptured()
		worktree := filepath.Join(t.TempDir(), "epic")
		w.git("worktree", "add", "-q", worktree, nodeBranchName)
		refs := w.git("for-each-ref")
		pending := w.pending()
		w.pass()

		w.requireParked("epic/epic-runner is checked out in a worktree, so it was not moved", pending)
		if tip := w.git("rev-parse", integrateBranch); tip != w.base {
			t.Errorf("branch = %s, want it left at the stored base %s", tip, w.base)
		}
		if got := w.git("for-each-ref"); got != refs {
			t.Errorf("refs = %q, want them unchanged as %q", got, refs)
		}
	})
}

// TestIntegrateCutAdvanceEndsPassedOnce: a branch already at the candidate, as a restart leaves it when it cut the
// advance, ends the batch passed once and moves nothing. A later pass changes nothing.
func TestIntegrateCutAdvanceEndsPassedOnce(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	candidate := w.toCaptured()
	w.git("update-ref", integrateBranch, candidate, w.base)
	pending := w.pending()
	w.pass()

	if run := runAt(t, w.s, integrateBatch); run.State != store.NodeEnded || run.Outcome != "passed" {
		t.Errorf("batch = %+v, want ended passed", run)
	}
	if got := epicOf(t, w.s).StoredBase; got != candidate {
		t.Errorf("stored base = %q, want the candidate %q", got, candidate)
	}
	if got := w.pending(); got != pending+1 {
		t.Errorf("pending triggers = %d, want %d: the cut batch wakes the planner once", got, pending+1)
	}
	ended := runsOf(t, w.s)
	w.pass()
	if got := runsOf(t, w.s); !reflect.DeepEqual(got, ended) {
		t.Errorf("node runs changed on a later pass: %+v, want %+v", got, ended)
	}
	if got := w.pending(); got != pending+1 {
		t.Errorf("pending triggers = %d after a later pass, want %d", got, pending+1)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != candidate {
		t.Errorf("branch = %s, want it left at the candidate %s", tip, candidate)
	}
}

// TestIntegrateOverlapLeavesTaskOutAndTellsThePlanner: two accepted tasks that change the same path compose to one
// commit. The later task is left out, and the overlap line is the batch's output and the cause of the trigger that wakes
// the planner.
func TestIntegrateOverlapLeavesTaskOutAndTellsThePlanner(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	first := w.commit(w.base, "b.go", "one\n")
	second := w.commit(w.base, "b.go", "two\n")
	w.pass()
	w.planTurn()
	w.accept(1, w.base, first)
	w.accept(2, w.base, second)
	w.pass()
	candidate := w.candidate(integrateBatch)
	w.endGate(integrateBatch, 0, "")
	w.pass()
	w.endCapture(integrateBatch)
	w.pass()

	if got := w.git("rev-list", "--count", w.base+".."+candidate); got != "1" {
		t.Errorf("commits on the stored base = %s, want only T1's", got)
	}
	const overlap = "T2 overlaps T1 at b.go"
	run := runAt(t, w.s, integrateBatch)
	if run.State != store.NodeEnded || run.Outcome != "passed" || run.Output != "R1 green\n"+overlap {
		t.Errorf("batch = %+v, want ended passed with output %q", run, "R1 green\n"+overlap)
	}
	triggers, err := w.s.TurnTriggers(context.Background(), runEpic, 0)
	if err != nil {
		t.Fatalf("TurnTriggers: %v", err)
	}
	wantCause := fmt.Sprintf("workflow node %s passed: R1 green\n%s", integrateBatch, overlap)
	if !slices.ContainsFunc(triggers, func(trigger store.EpicTrigger) bool { return trigger.Cause == wantCause }) {
		t.Errorf("pending triggers = %+v, want one with cause %q", triggers, wantCause)
	}
}

// TestBuiltInEpicReachesBuiltThenWaitsAtReview: a database epic pinned to the built-in epic, with one Requirement naming
// an evidence item, runs red at base first: its capture is judged not met, so red passes and the plan starts. The one
// task is planned, built and accepted, and its batch integrates green, so the plan ends built and review starts. Review
// waits: after further passes it is still running, the epic is not parked, and no node after it has started.
func TestBuiltInEpicReachesBuiltThenWaitsAtReview(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
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

	completeTurn(t, s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("only", "b.go")}, Outline: "one task",
		Outcome: "complete",
	})
	w.pass()
	if task := runAt(t, s, "plan/build/T1"); task.State != store.NodeRunning || task.Task != 1 {
		t.Fatalf("build of task 1 = %+v, want running", task)
	}

	first := w.commit(w.base, "b.go", "one\n")
	w.accept(1, w.base, first)
	w.pass()
	const batch = "plan/S1/integrate"
	candidate := w.candidate(batch)
	w.endGate(batch, 0, "")
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

// TestIntegrateBatchOnAnOlderBaseNeverMovesTheBranchBack: a batch composed on the base, whose branch a newer batch has
// since advanced, with the stored base, to a commit the batch does not build on, parks instead of moving the branch
// back. The branch keeps the newer batch's work. This is the batch of a late walk that a settle walk overtook.
func TestIntegrateBatchOnAnOlderBaseNeverMovesTheBranchBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	w := newIntegrateWorld(t)
	candidate := w.toCaptured()
	newer := w.commit(w.base, "newer.go", "newer\n")
	const overtaking = "plan/S7/integrate"
	if err := w.s.StartNodes(ctx, runEpic, []store.NodeRun{{
		Path: overtaking, ID: "overtaking", Node: "integrate", Kind: kindIntegrate, Attempt: 1,
	}}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := w.s.PrepareNode(ctx, runEpic, overtaking, 1, newer, ""); err != nil {
		t.Fatalf("PrepareNode: %v", err)
	}
	if err := w.s.EndBatch(ctx, runEpic, overtaking, 1, outcomePassed, "", nil, runClock()); err != nil {
		t.Fatalf("EndBatch: %v", err)
	}
	w.git("update-ref", integrateBranch, newer)
	pending := w.pending()
	w.pass()
	w.requireParked(fmt.Sprintf(
		"epic/epic-runner is at %s, which the candidate %s does not build on, so it was not moved", newer, candidate,
	), pending)
	if tip := w.git("rev-parse", integrateBranch); tip != newer {
		t.Errorf("branch = %s, want it left at the newer batch's %s", tip, newer)
	}
}
