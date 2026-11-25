package interpret

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/planner"
	"github.com/joshsymonds/orrery/internal/reviewers"
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

// newReplanWorld returns a replan world whose release has not started, and whose epic declares requirements.
func newReplanWorld(t *testing.T, requirements ...store.EpicRequirement) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.requirements = requirements
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

// block ends the release integrator's round blocked on README, with its decision on side, and passes.
func (w replanWorld) block(side string) {
	w.t.Helper()
	w.endStep(w.integratorID(), "returned", "blocked", map[string]any{
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

// pathlessEvidence is the evidence of a blocked return that names no path.
const pathlessEvidence = "the worktree is not as the role text describes it"

// blockWithoutPaths ends the release integrator's round blocked with evidence and no unresolved path, which stops the
// release, and passes.
func (w replanWorld) blockWithoutPaths() {
	w.t.Helper()
	w.endStep(w.integratorID(), "returned", "blocked", map[string]any{
		"status": "blocked", "commit": "", "resolutions": []map[string]any{}, "other_changes": []map[string]any{},
		"checks": []map[string]any{}, "unresolved": []map[string]any{}, "evidence": pathlessEvidence,
	})
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// newCleanReplanWorld returns a replan world whose epic's branch and origin's main change different files, so git
// merges origin's commit into the tip with no conflict. The epic runs replanFlow, its plan has ended, and its release
// has not started, as an epic that ended clean leaves it. Its epic's merged reviewers are reviewerList.
func newCleanReplanWorld(t *testing.T, reviewerList ...reviewers.Reviewer) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.reviewerList = reviewerList
	plans := &fakePlanner{known: []string{runEpic}}
	w.runner.planner = plans
	base := nodeGit(t, w.repo, "rev-parse", "main")
	w.createEpicOn(replanFlow, base, runEpic, store.PhaseClean, tagStep())
	remote := mergeRemote(t, w.repo)
	tip := nodeEpicBranch(t, w.repo, "epic.txt")
	origin := mergeOriginCommit(t, remote, "origin.txt")
	execStoreFile(t, w.path, "UPDATE epics SET stored_base = ? WHERE id = ?", tip, runEpic)
	plan := store.NodeRun{Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: kindPlan}
	if err := w.s.StartNodes(t.Context(), runEpic, []store.NodeRun{plan}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	ended := []store.NodeEnd{{Path: "plan", Outcome: "complete"}}
	if err := w.s.EndNodes(t.Context(), runEpic, ended, runClock()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	return replanWorld{conflictWorld: conflictWorld{releaseWorld: w, tip: tip, origin: origin}, planner: plans}
}

// releaseTriggerIDs returns the ids of the release triggers epic has stored, the ones a replan writes.
func releaseTriggerIDs(t *testing.T, s *store.Store, epic string) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicHistory of %s: %v", epic, err)
	}
	var ids []string
	for _, trigger := range history.Triggers {
		if strings.HasPrefix(trigger.ID, replanTriggerPrefix) {
			ids = append(ids, trigger.ID)
		}
	}
	return ids
}

// requireReplannedAt fails unless the release went back to planning for report: the epic is correcting and unparked, the
// release run still runs at its first attempt, the replan's plan runs under it, a replan of that attempt holds head and
// the report as its reason, a release trigger is stored, the integrator's worktree is gone and the owner is told.
func (w replanWorld) requireReplannedAt(head, report string) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != "correcting" || flow.Epic.Parked != "" {
		w.t.Fatalf("epic phase %q parked %q, want correcting and unparked", flow.Epic.Phase, flow.Epic.Parked)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 0 {
		w.t.Errorf("release run = %+v, want running at attempt 0", run)
	}
	if plan := runAt(w.t, w.s, replanPlanPath); plan.State != store.NodeRunning || plan.Kind != kindPlan {
		w.t.Errorf("replan plan = %+v, want the plan running under the release", plan)
	}
	if !slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Attempt == 0 && r.Head == head && r.Reason == report
	}) {
		w.t.Errorf("release runs = %+v, want a replan of attempt 0 holding %s and the report", flow.Releases, head)
	}
	if ids := releaseTriggerIDs(w.t, w.s, runEpic); len(ids) == 0 {
		w.t.Errorf("release trigger ids = %q, want a %s trigger", ids, replanTriggerPrefix)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		w.t.Errorf("integrator directory stat = %v, want it removed", err)
	}
	if tells := nodeTells(w.t, w.path); !slices.Contains(tells, replanCause+report) {
		w.t.Errorf("tells = %q, want %q", tells, replanCause+report)
	}
}

// requireReleaseStopped fails unless the release stopped on reason: the epic is stopped and parked with it, the release
// run is parked with it, and the release was sent back to planning no more than replans times, the replans it had
// before the stop. Each of those is a replan release run and a release trigger, and the stop adds neither.
func (w replanWorld) requireReleaseStopped(reason string, replans int) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.Parked != reason {
		w.t.Errorf("epic phase %q parked %q, want stopped and parked with %q", flow.Epic.Phase, flow.Epic.Parked,
			reason)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeParked || run.Output != reason {
		w.t.Errorf("release run = %+v, want parked with %q", run, reason)
	}
	if runs := releaseRunsOf(flow.Releases, store.ReleasePartReplan); len(runs) != replans {
		w.t.Errorf("replan runs = %d, want %d: %+v", len(runs), replans, flow.Releases)
	}
	if ids := releaseTriggerIDs(w.t, w.s, runEpic); len(ids) != replans {
		w.t.Errorf("%s triggers = %d, want %d", replanTriggerPrefix, len(ids), replans)
	}
}

// report is the report the blocked round on side records.
func (w replanWorld) report(side string) string {
	return fmt.Sprintf("the release integrator stopped the merge of origin's commit %s into release tip %s:\n"+
		"- README: side: %s; decision: make the candidate's README say origin, as main's does; candidate: the "+
		"epic's README says change; origin: main's README says origin", w.origin, w.tip, side)
}

// requireReplanned fails unless the release went back to planning with report: the epic is correcting and unparked, the
// release run still runs at its first attempt, the replan's plan runs under it with the
// epic's highest turn as its mark, a replan of the attempt names the tip and the report, the integrator's worktree is
// gone, the owner is told, and the planner was passed for the epic.
func (w replanWorld) requireReplanned(report string) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != "correcting" || flow.Epic.Parked != "" {
		w.t.Fatalf("epic phase %q parked %q, want correcting and unparked", flow.Epic.Phase, flow.Epic.Parked)
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
// blocked on a decision on the candidate's side does not stop the release. The epic goes back to planning: its plan
// runs again under the release with a trigger that carries the integrator's report, which the planner's next turn
// reads, and the owner is told. The task that turn plans is built and integrated onto the epic's branch, and once the
// planner returns complete the release moves to its next attempt and begins again. Origin's main merges into the new
// branch tip with no conflict, and the release integrator of the new attempt makes that merge: the prepare records
// origin's commit and no conflict, and no candidate.
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
	if flow.Epic.Phase != store.PhaseReleasing {
		t.Fatalf("epic phase %q, want releasing again", flow.Epic.Phase)
	}
	run := runAt(t, w.s, releaseNodePath)
	merge := decodeMerge(t, run.Result)
	if run.Attempt != 1 || merge.Tip != newTip || merge.Origin != w.origin || run.Revision != "" ||
		strings.Contains(run.Result, `"conflicts"`) {
		t.Errorf("release run = %+v, want attempt 1 merging origin's commit %s into the new tip %s with no conflict",
			run, w.origin, newTip)
	}
	starts := w.integratorStarts()
	if len(starts) != 2 || starts[1].ID != w.integratorID() || starts[1].Revision != newTip ||
		!strings.Contains(starts[1].Prompt, "## Conflicted paths\nNone.\n") {
		t.Errorf("integrator starts = %+v, want the integrator of attempt 1 started at the new tip %s with no "+
			"conflicted path", starts, newTip)
	}
}

// TestReleaseIntegratorMergeSideReplans: a round of the release integrator that returns blocked on a decision on the
// merge's side, such as which side's behavior holds, does not stop the release either. It goes back to planning with
// the integrator's report, as a round blocked on the candidate's side does, so the epic's planner settles the decision
// as tasks.
func TestReleaseIntegratorMergeSideReplans(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	w.block("merge")
	w.requireReplanned(w.report("merge"))
}

// TestRetryOfIntegratorStoppedReleaseReplans: a release its integrator stopped, as one blocked without naming a path
// stops, is moved on by the owner's retry: the next pass sends it back to planning with the stopped round's report,
// exactly as a blocked return that names a path does.
func TestRetryOfIntegratorStoppedReleaseReplans(t *testing.T) {
	t.Parallel()
	w := newReplanWorld(t)
	w.pass()
	w.blockWithoutPaths()
	if phase := w.flowOf(runEpic).Epic.Phase; phase != store.PhaseStopped {
		t.Fatalf("epic phase = %q, want stopped", phase)
	}
	if err := w.s.RetryEpic(
		context.Background(),
		runEpic,
		store.Maker{By: store.MakerOwner},
		time.Unix(1700000000, 0),
	); err != nil {
		t.Fatalf("RetryEpic: %v", err)
	}
	if logged := w.pass(); logged != "" {
		t.Fatalf("pass logged: %s", logged)
	}
	w.requireReplanned(fmt.Sprintf(
		"the release integrator stopped the merge of origin's commit %s into release tip %s: %s",
		w.origin, w.tip, pathlessEvidence,
	))
}
