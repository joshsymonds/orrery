package interpret

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/store"
)

// replanFlow is the workflow the replan tests pin: a plan that builds and integrates, then the release. The plan has
// ended when a test's release starts, so a replan runs that plan again under the release.
const replanFlow = "name: replan\nnodes:\n  - id: plan\n    kind: plan\n    body:\n      - id: build\n" +
	"        kind: build\n      - id: integrate\n        kind: integrate\n  - id: release\n    kind: release\n"

// The paths under the release run's first attempt that its replan's plan, its task's build and its batch run at.
const (
	replanPlanPath  = "release/A0/plan"
	replanBuildPath = replanPlanPath + "/build/T1"
	replanBatchPath = replanPlanPath + "/S1/integrate"
)

// replanWorld is a conflict world whose epic runs replanFlow, with its plan ended and its stored base at the branch tip,
// as an epic that ended clean leaves them, and a planner fake that knows the epic.
type replanWorld struct {
	conflictWorld
	planner *fakePlanner
}

// newReplanWorld returns a replan world whose release has not started.
func newReplanWorld(t *testing.T) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	plans := &fakePlanner{known: []string{runEpic}}
	w.runner.planner = plans
	base := nodeGit(t, w.repo, "rev-parse", "main")
	w.createEpicOn(replanFlow, base, runEpic, store.PhaseClean, tagStep())
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "README")
	origin := mergeOriginCommit(t, remote, "README")
	execStoreFile(t, w.path, "UPDATE epics SET stored_base = ? WHERE id = ?", tip, runEpic)
	ctx := t.Context()
	plan := store.NodeRun{Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: kindPlan}
	if err := w.s.StartNodes(ctx, runEpic, []store.NodeRun{plan}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := w.s.EndNodes(ctx, runEpic, []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, runClock()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	return replanWorld{conflictWorld: conflictWorld{releaseWorld: w, tip: tip, origin: origin}, planner: plans}
}

// block ends the first round of the release integrator blocked on README, with its decision on side, and passes.
func (w replanWorld) block(side string) {
	w.t.Helper()
	w.endStep(w.integratorID(1), "returned", "blocked", map[string]any{
		"status": "blocked", "commit": "", "resolutions": []map[string]any{}, "other_changes": []map[string]any{},
		"checks": []map[string]any{},
		"unresolved": []map[string]any{{
			"path": "README", "candidate": "the epic's README says change", "origin": "main's README says origin",
			"decision": "make the candidate's README say origin, as main's does", "side": side,
		}},
		"evidence": "the candidate's README must change before the merge",
	})
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// report is the report the blocked round on side records.
func (w replanWorld) report(side string) string {
	return fmt.Sprintf("the release integrator stopped the merge of origin's commit %s into release tip %s:\n"+
		"- README: side: %s; decision: make the candidate's README say origin, as main's does; candidate: the "+
		"epic's README says change; origin: main's README says origin", w.origin, w.tip, side)
}

// requireReplanned fails unless the release went back to planning with report: the epic is correcting, unparked and
// holds no release slot, the release run still runs at its first attempt, the replan's plan runs under it with the
// epic's highest turn as its mark, a replan of the attempt names the tip and the report, the integrator's worktree is
// gone, the owner is told, and the planner was passed for the epic.
func (w replanWorld) requireReplanned(report string) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != "correcting" || flow.Epic.Parked != "" || flow.Slot != "" {
		w.t.Fatalf("epic phase %q parked %q slot %q, want correcting, unparked and the slot free",
			flow.Epic.Phase, flow.Epic.Parked, flow.Slot)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 0 {
		w.t.Errorf("release run = %+v, want running at attempt 0", run)
	}
	if plan := runAt(w.t, w.s, replanPlanPath); plan.State != store.NodeRunning || plan.Kind != kindPlan ||
		plan.Node != "plan" {
		w.t.Errorf("replan plan = %+v, want the plan running under the release", plan)
	}
	if !slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Attempt == 0 && r.Start == w.tip && r.Reason == report
	}) {
		w.t.Errorf("release runs = %+v, want a replan of attempt 0 from %s with the report", flow.Releases, w.tip)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		w.t.Errorf("integrator directory stat = %v, want it removed", err)
	}
	if tells := nodeTells(w.t, w.path); !slices.Contains(tells, replanCause+report) {
		w.t.Errorf("tells = %q, want %q", tells, replanCause+report)
	}
	if last := w.planner.named[len(w.planner.named)-1]; !slices.Contains(last, runEpic) {
		w.t.Errorf("planner passes named %q, want the last to name %s", w.planner.named, runEpic)
	}
}

// claimReplanTurn claims the planner turn the replan's trigger wakes and returns the prompt the planner gets for it.
func (w replanWorld) claimReplanTurn() (store.EpicTurn, string) {
	w.t.Helper()
	ctx := w.t.Context()
	turn, err := w.s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		w.t.Fatalf("ClaimTurn: %v", err)
	}
	triggers, err := w.s.TurnTriggers(ctx, runEpic, turn.Seq)
	if err != nil {
		w.t.Fatalf("TurnTriggers: %v", err)
	}
	epic, err := w.s.Epic(ctx, runEpic)
	if err != nil {
		w.t.Fatalf("Epic: %v", err)
	}
	prompt, err := planner.Prompt(epic, turn.Seq, triggers, nil, nil)
	if err != nil {
		w.t.Fatalf("Prompt: %v", err)
	}
	return turn, prompt
}

// completeReplanTurn completes turn with outcome and tasks, and passes.
func (w replanWorld) completeReplanTurn(turn store.EpicTurn, outcome string, tasks ...store.EpicTaskInput) {
	w.t.Helper()
	plan := store.EpicPlan{Decisions: []string{}, Tasks: tasks, Outline: "the candidate's README", Outcome: outcome}
	if err := w.s.CompleteTurn(w.t.Context(), runEpic, turn.Seq, plan, runClock()); err != nil {
		w.t.Fatalf("CompleteTurn %d: %v", turn.Seq, err)
	}
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// fixCommit commits README saying origin on top of the tip, as the replan's task's implementer would, and returns it.
func (w replanWorld) fixCommit() string {
	w.t.Helper()
	nodeGit(w.t, w.repo, "checkout", "-q", "--detach", w.tip)
	nodeWrite(w.t, w.repo, "README", "origin\n")
	nodeGit(w.t, w.repo, "add", "README")
	nodeGit(w.t, w.repo, "commit", "-q", "-m", "the candidate's README says origin")
	fix := nodeGit(w.t, w.repo, "rev-parse", "HEAD")
	nodeGit(w.t, w.repo, "checkout", "-q", "main")
	return fix
}

// acceptTask stores the build of task seq as built on base at revision, and accepts it.
func (w replanWorld) acceptTask(seq int64, base, revision string) {
	w.t.Helper()
	ctx := w.t.Context()
	built := store.TaskBuild{Stage: "green", Base: base, Revision: revision}
	if err := w.s.SetTaskBuild(ctx, runEpic, seq, store.TaskBuild{}, built); err != nil {
		w.t.Fatalf("SetTaskBuild %d: %v", seq, err)
	}
	if err := w.s.AcceptTask(ctx, runEpic, seq, built, "reviewed"); err != nil {
		w.t.Fatalf("AcceptTask %d: %v", seq, err)
	}
}

// TestReleaseIntegratorCandidateSideReplansThenReleasesFromNewCandidate: a round of the release integrator that returns
// blocked on a decision on the candidate's side does not stop the release. The epic goes back to planning: it frees the
// release slot, its plan runs again under the release with a trigger that carries the integrator's report, which the
// planner's next turn reads, and the owner is told. The task that turn plans is built and integrated onto the epic's
// branch, and once the planner returns complete the release moves to its next attempt, takes the slot again and merges
// origin's main into the new branch tip, which no longer conflicts, so no second integrator round starts.
func TestReleaseIntegratorCandidateSideReplansThenReleasesFromNewCandidate(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	if starts := w.integratorStarts(); len(starts) != 1 {
		t.Fatalf("integrator starts = %d, want the first round", len(starts))
	}
	w.block("candidate")
	report := w.report("candidate")
	w.requireReplanned(report)

	turn, prompt := w.claimReplanTurn()
	for _, want := range []string{"- " + replanTriggerPrefix, replanCause + report} {
		if !strings.Contains(prompt, want) {
			t.Errorf("planner prompt holds no %q:\n%s", want, prompt)
		}
	}
	task := runTask("the candidate's README says origin", "README")
	task.Requirements = []string{}
	w.completeReplanTurn(turn, "work", task)
	if build := runAt(t, w.s, replanBuildPath); build.State != store.NodeRunning || build.Task != 1 {
		t.Fatalf("build = %+v, want the task's build running under the replan's plan", build)
	}

	fix := w.fixCommit()
	w.acceptTask(1, w.tip, fix)
	w.pass()
	nodeEndCheck(t, w.s, workID(t, w.s, replanBatchPath, "done 1"), 0, "")
	w.pass()
	if batch := runAt(t, w.s, replanBatchPath); batch.State != store.NodeEnded || batch.Outcome != "passed" {
		t.Fatalf("batch = %+v, want it passed", batch)
	}
	newTip := nodeGit(t, w.repo, "rev-parse", "refs/heads/"+nodeBranchName)
	if newTip == w.tip {
		t.Fatalf("branch tip = %s, want it moved by the replan's batch", newTip)
	}

	turn, _ = w.claimReplanTurn()
	w.completeReplanTurn(turn, "complete")
	if plan := runAt(t, w.s, replanPlanPath); plan.State != store.NodeEnded {
		t.Errorf("replan plan = %+v, want it ended", plan)
	}
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseReleasing || flow.Slot != runEpic {
		t.Fatalf("epic phase %q slot %q, want releasing and holding the slot again", flow.Epic.Phase, flow.Slot)
	}
	run := runAt(t, w.s, releaseNodePath)
	merge := decodeMerge(t, run.Result)
	if run.Attempt != 1 || merge.Tip != newTip || run.Revision == "" {
		t.Errorf("release run = %+v, want attempt 1 with a merged candidate from the new tip %s", run, newTip)
	}
	if starts := w.integratorStarts(); len(starts) != 1 {
		t.Errorf("integrator starts = %d, want no second round: the new tip merges origin cleanly", len(starts))
	}
}

// TestReleaseIntegratorMergeSideStopsAsBefore: a round that returns blocked on a decision on the merge's side stops the
// release with its report as it did before replans, even with a plan the release could run again: the epic parks
// stopped with the report, keeps the release slot, and no replan or plan run is recorded.
func TestReleaseIntegratorMergeSideStopsAsBefore(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	w.block("merge")

	flow := w.flowOf(runEpic)
	report := w.report("merge")
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.Parked != report ||
		flow.Epic.ParkClass != store.ParkClassReleaseStep || flow.Slot != runEpic {
		t.Errorf("epic phase %q parked %q class %q slot %q, want stopped on the report, a release_step park and the "+
			"slot held", flow.Epic.Phase, flow.Epic.Parked, flow.Epic.ParkClass, flow.Slot)
	}
	if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeParked || run.Output != report {
		t.Errorf("release run = %+v, want parked with the report", run)
	}
	if slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool { return r.Part == store.ReleasePartReplan }) {
		t.Errorf("release runs = %+v, want no replan", flow.Releases)
	}
	if slices.ContainsFunc(flow.Runs, func(r store.NodeRun) bool { return strings.HasPrefix(r.Path, "release/") }) {
		t.Errorf("runs = %+v, want none under the release", flow.Runs)
	}
}

// TestRetryOfIntegratorStoppedReleaseReplans: a release its integrator stopped, as one stopped before the integrator
// named a side, is moved on by the owner's retry: the next pass sends it back to planning with the stopped round's
// report, exactly as a candidate-side block does.
func TestRetryOfIntegratorStoppedReleaseReplans(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	w.block("")
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		t.Fatalf("epic phase = %q, want stopped", phase)
	}
	if err := w.s.RetryEpic(context.Background(), runEpic); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}
	w.requireReplanned(w.report(""))
}
