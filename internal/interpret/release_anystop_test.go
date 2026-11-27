package interpret

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/releasestep"
	"github.com/joshsymonds/orrery/internal/store"
)

// anyStopSetup says how the world of a stop case is set up: the release steps of its epic, whether origin's main moved
// in a commit that conflicts with the epic's branch, so the release integrator merges it, whether the epic has no
// branch, so its candidate cannot be prepared, and whether a release reviewer reviews the candidate before the first
// step.
type anyStopSetup struct {
	steps    []store.EpicReleaseStep
	conflict bool
	noBranch bool
	reviewed bool
}

// anyStopCase is one cause a release stops with. stop drives the release to that stop through the interpreter and the
// store, and returns the release tip the stop's replan starts from, "" for a stop with none. fragment is a part of the
// reason the stop records.
type anyStopCase struct {
	name     string
	setup    anyStopSetup
	stop     func(w replanWorld) string
	fragment string
}

// The parts of the refusals a planner's action of a stopped release is refused with that the tests read: the refusal of
// a retry of a release its owner stopped, and the refusal of a resume of a release its judge stopped with a verdict.
const (
	ownerStopRefusal = "its owner stopped the release; only the owner retries it"
	judgeStopPart    = "its release judge found the release short of its postconditions"
	judgeSteerPart   = "ask the steerer"
)

// undoNoneKindStep is a release step that names the tag kind and whose undo is none, so no later step undoes it.
func undoNoneKindStep() store.EpicReleaseStep {
	return store.EpicReleaseStep{
		Step: "tag the release", Target: "origin's tags", Undo: "none",
		Kind: releasestep.KindTag, Params: releasestep.Params{Prefix: "v"},
	}
}

// stoppedTask is a task a planner plans while its release is stopped, on epic.txt, with no Requirement to claim.
func stoppedTask(title string) store.EpicTaskInput {
	task := runTask(title, "epic.txt")
	task.Requirements = []string{}
	return task
}

// failStepOne starts the first release step of the release run's current attempt, which names the push-main kind, and
// fails it: its check exits 1 with a reason, and the release stops at that step.
func failStepOne(w replanWorld) {
	w.t.Helper()
	w.pass()
	w.endCheck(w.stepID(1), 1, "", "origin refused the push\n")
	w.pass()
}

// stepStop stops the release at its failed first step, the cause the step cases and the resume cases start from.
func stepStop(w replanWorld) string {
	failStepOne(w)
	return ""
}

// judgeStop stops the release at its judge, whose verdict on the release is that it did not release the epic.
func judgeStop(w replanWorld) string {
	w.pass()
	w.finishStep(1, "tagged main")
	w.pass()
	w.endStep(w.judgeID(), "done", "done", map[string]any{
		"status": "done", "released": false, "uncovered": []string{},
	})
	w.pass()
	return ""
}

// integratorBlockedStop stops the release at a round of the release integrator that returned blocked without naming a
// path, so the round has no decision for the planner to settle.
func integratorBlockedStop(w replanWorld) string {
	w.pass()
	w.blockWithoutPaths()
	return w.tip
}

// integratorFailedStop stops the release at a round of the release integrator that ended failed.
func integratorFailedStop(w replanWorld) string {
	w.pass()
	w.endStep(w.integratorID(), "failed", "failed", map[string]any{
		"status": "failed", "evidence": "the merge check crashed before it could judge the merge",
	})
	w.pass()
	return ""
}

// refusedReviewStop stops the release at its candidate, refused by a release reviewer with findings. The release
// interpreter replans such a refusal in a workflow with a plan, so the stop is written as the interpreter writes it for
// a refusal in a workflow with no plan: the refused review's run, then the stop at its attempt with the refusal as
// reason. It returns the candidate the review refused, which the stop's replan starts from.
func refusedReviewStop(w replanWorld) string {
	w.pass()
	candidate := runAt(w.t, w.s, releaseNodePath).Revision
	refusal := "the release reviewer refused the candidate: " + releaseClaim
	ctx := w.t.Context()
	if err := w.s.RecordRelease(ctx, runEpic, store.ReleaseRun{
		Part: store.ReleasePartReview, Position: 1, Attempt: 0, Step: releaseReviewerName, Head: candidate,
		End: "gate_failed", Reason: refusal, At: runClock(),
	}); err != nil {
		w.t.Fatalf("RecordRelease of the refused review: %v", err)
	}
	if err := w.s.StopRelease(ctx, runEpic, releaseNodePath, 0, refusal, runClock()); err != nil {
		w.t.Fatalf("StopRelease at the refused review: %v", err)
	}
	return candidate
}

// anyStopCases returns a stop of each cause a release stops with: a failed release step, a judge's verdict that the
// release did not release the epic, a round of the release integrator that returned blocked, a round that ended failed,
// a candidate that could not be prepared, and a release reviewer's refusal of the candidate with findings.
func anyStopCases() []anyStopCase {
	kindSteps := []store.EpicReleaseStep{pushMainStep(), tagKindStep()}
	return []anyStopCase{
		{
			name:     "failed release step",
			setup:    anyStopSetup{steps: kindSteps},
			stop:     stepStop,
			fragment: "the step exited 1\nstderr:\norigin refused the push",
		},
		{
			name:     "judge verdict",
			setup:    anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}},
			stop:     judgeStop,
			fragment: "the judge did not release the epic",
		},
		{
			name:     "integrator returned blocked",
			setup:    anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}, conflict: true},
			stop:     integratorBlockedStop,
			fragment: "stopped the merge of origin's commit",
		},
		{
			name:     "integrator ended failed",
			setup:    anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}, conflict: true},
			stop:     integratorFailedStop,
			fragment: "release integrator ended failed",
		},
		{
			name:     "candidate",
			setup:    anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}, noBranch: true},
			stop:     func(w replanWorld) string { w.pass(); return "" },
			fragment: "has no branch",
		},
		{
			name:     "refused review",
			setup:    anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}, reviewed: true},
			stop:     refusedReviewStop,
			fragment: "the release reviewer refused the candidate: " + releaseClaim,
		},
	}
}

// newStopWorld returns a replan world whose epic runs replanFlow with the release steps and setup gives. Its planner
// has planned a design task T1 that is done, the plan has ended, and its release has not started, as an epic that ended
// clean leaves it.
func newStopWorld(t *testing.T, setup anyStopSetup) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
	w.runner.orrery = testOrrery
	plans := &fakePlanner{known: []string{runEpic}}
	w.runner.planner = plans
	if setup.reviewed {
		w.runner.reviewers = &releaseReviewers{}
		w.reviewerList = defaultReleaseReviewer(t, true)
	}
	base := nodeGit(t, w.repo, "rev-parse", "main")
	w.createEpicOn(replanFlow, base, runEpic, runPhase, setup.steps...)
	stored, origin, tip := base, base, ""
	if !setup.noBranch {
		remote := mergeRemote(t, w.repo)
		file := "epic.txt"
		if setup.conflict {
			file = "README"
		}
		tip = nodeEpicBranch(t, w.repo, file)
		if setup.conflict {
			origin = mergeOriginCommit(t, remote, "README")
		}
		stored = tip
	}
	execStoreFile(t, w.path, "UPDATE epics SET stored_base = ? WHERE id = ?", stored, runEpic)
	ctx := t.Context()
	plan := store.NodeRun{Path: "plan", ID: store.NodeRunID(runEpic, "plan", 0, "start"), Node: "plan", Kind: kindPlan}
	if err := w.s.StartNodes(ctx, runEpic, []store.NodeRun{plan}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	world := replanWorld{conflictWorld: conflictWorld{releaseWorld: w, tip: tip, origin: origin}, planner: plans}
	design := runTask("the design the release rests on", "design.md")
	design.Kind = store.TaskKindDesign
	design.Requirements = []string{}
	world.kick()
	turn := world.plannerTurn([]store.EpicTaskInput{design})
	done := tip
	if setup.noBranch {
		done = base
	}
	world.acceptTask(turn.Seq, stored, done)
	if err := w.s.EndNodes(ctx, runEpic, []store.NodeEnd{{Path: "plan", Outcome: "complete"}}, runClock()); err != nil {
		t.Fatalf("EndNodes: %v", err)
	}
	execStoreFile(t, w.path, "UPDATE epics SET phase = ? WHERE id = ?", store.PhaseClean, runEpic)
	return world
}

// kick wakes the planner of runEpic as the owner does, with a word of its own.
func (w replanWorld) kick() {
	w.t.Helper()
	if err := w.s.KickEpic(
		w.t.Context(), runEpic, "the owner has a word", store.Maker{By: store.MakerOwner}, runClock(),
	); err != nil {
		w.t.Fatalf("KickEpic: %v", err)
	}
}

// passQuiet carries one pass out and fails the test when it logs anything.
func (w replanWorld) passQuiet() {
	w.t.Helper()
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// plannerTurn claims the planner's turn that the epic's triggers wake and completes it with tasks and actions, as the
// planner returns them, and returns the turn. carryTurn carries out the actions it stored.
func (w replanWorld) plannerTurn(tasks []store.EpicTaskInput, actions ...store.EpicActionInput) store.EpicTurn {
	w.t.Helper()
	ctx := w.t.Context()
	turn, err := w.s.ClaimTurn(ctx, runEpic, runClock())
	if err != nil {
		w.t.Fatalf("ClaimTurn: %v", err)
	}
	plan := store.EpicPlan{
		Decisions: []string{}, Tasks: tasks, Outline: "the release's stop", Outcome: "work", Actions: actions,
	}
	if err = w.s.CompleteTurn(ctx, runEpic, turn.Seq, plan, runClock()); err != nil {
		w.t.Fatalf("CompleteTurn %d: %v", turn.Seq, err)
	}
	return turn
}

// carryTurn carries out each action the planner turn turn stored, and fails the test on an error or on a step the carry
// asks the runner to stop: an action that is refused is carried out too, and is seen through the epic's triggers.
func (w replanWorld) carryTurn(turn store.EpicTurn) {
	w.t.Helper()
	ctx := w.t.Context()
	actions, err := w.s.PendingActions(ctx)
	if err != nil {
		w.t.Fatalf("PendingActions: %v", err)
	}
	for _, action := range actions {
		if action.Epic != runEpic || action.Turn != turn.Seq {
			continue
		}
		stop, carryErr := w.s.CarryOutAction(ctx, action, runClock())
		if carryErr != nil || len(stop) != 0 {
			w.t.Fatalf("CarryOutAction of %s = %v, %v; want no steps stopped and no error", action.Kind, stop, carryErr)
		}
	}
}

// triggerCauses returns the causes of the triggers of runEpic whose ids start with prefix, in the order the store holds
// them.
func triggerCauses(t *testing.T, s *store.Store, prefix string) []string {
	t.Helper()
	history, err := s.EpicHistory(t.Context(), runEpic)
	if err != nil {
		t.Fatalf("EpicHistory: %v", err)
	}
	var causes []string
	for _, trigger := range history.Triggers {
		if strings.HasPrefix(trigger.ID, prefix) {
			causes = append(causes, trigger.Cause)
		}
	}
	return causes
}

// requireNotRefused fails unless no planner action of runEpic was refused.
func (w replanWorld) requireNotRefused() {
	w.t.Helper()
	if causes := triggerCauses(w.t, w.s, ""); slices.ContainsFunc(causes, func(cause string) bool {
		return strings.Contains(cause, " refused: ")
	}) {
		w.t.Errorf("triggers = %q, want no planner action refused", causes)
	}
}

// replanPlanAt returns the path of the plan a replan of the release run at attempt runs.
func replanPlanAt(attempt int64) string {
	return fmt.Sprintf("%s/A%d/plan", releaseNodePath, attempt)
}

// requireStopped fails unless the release of w stopped at attempt, at a release step, with a reason that holds
// fragment: the epic is stopped and parked with that reason, and the release run is parked at attempt with the reason
// as its output, with no replan of attempt stored yet. It returns the reason.
func (w replanWorld) requireStopped(fragment string, attempt int64) string {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	reason := flow.Epic.Parked
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.ParkClass != store.ParkClassReleaseStep ||
		!strings.Contains(reason, fragment) {
		w.t.Fatalf("epic phase %q class %q parked %q, want stopped at a release step with a reason holding %q",
			flow.Epic.Phase, flow.Epic.ParkClass, reason, fragment)
	}
	run := runAt(w.t, w.s, releaseNodePath)
	if run.State != store.NodeParked || run.Attempt != attempt || run.Output != reason {
		w.t.Fatalf("release run = %+v, want parked at attempt %d with the stop's reason", run, attempt)
	}
	if slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Attempt == attempt
	}) {
		w.t.Fatalf("release runs = %+v, want no replan of attempt %d before the action", flow.Releases, attempt)
	}
	return reason
}

// requireStopReplanned fails unless the release of w went back to planning for the stop at attempt with reason: the
// epic is correcting and unparked, the release run runs at attempt, the replan's plan runs under it, a replan of
// attempt holds start and reason, and a release trigger's cause names the stop.
func (w replanWorld) requireStopReplanned(reason, start string, attempt int64) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != "correcting" || flow.Epic.Parked != "" {
		w.t.Fatalf("epic phase %q parked %q, want correcting and unparked", flow.Epic.Phase, flow.Epic.Parked)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != attempt {
		w.t.Errorf("release run = %+v, want running at attempt %d", run, attempt)
	}
	if plan := runAt(w.t, w.s, replanPlanAt(attempt)); plan.State != store.NodeRunning || plan.Kind != kindPlan {
		w.t.Errorf("replan plan = %+v, want the plan running under the release", plan)
	}
	if !slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan && r.Attempt == attempt && r.Start == start && r.Reason == reason
	}) {
		w.t.Errorf("release runs = %+v, want a replan of attempt %d from %q with the stop's reason", flow.Releases,
			attempt, start)
	}
	if causes := triggerCauses(w.t, w.s, replanTriggerPrefix); !slices.Contains(causes, replanCause+reason) {
		w.t.Errorf("release trigger causes = %q, want %q", causes, replanCause+reason)
	}
}

// requireBuildUnder fails unless the build of task seq runs under the replan plan of the release's first attempt.
func (w replanWorld) requireBuildUnder(seq int64) {
	w.t.Helper()
	path := fmt.Sprintf("%s/build/T%d", replanPlanAt(0), seq)
	if build := runAt(w.t, w.s, path); build.State != store.NodeRunning || build.Task != seq {
		w.t.Errorf("build = %+v, want task %d building under the replan plan at %s", build, seq, path)
	}
}

// TestAnyStopReplansOnPlannerRetry: for each cause a release stops with, the planner's retry_epic sends the release
// back to planning. The epic moves to correcting with a release trigger whose cause names the stop, and a replan
// release run holds the stop's reason. A task the planner's turn planned while the release was stopped then builds
// under the replan plan. The retry is not refused for the stop's cause, for the step's undo, or for the replans the
// release had.
func TestAnyStopReplansOnPlannerRetry(t *testing.T) {
	t.Parallel()
	for _, c := range anyStopCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newStopWorld(t, c.setup)
			start := c.stop(w)
			reason := w.requireStopped(c.fragment, 0)
			w.carryTurn(w.plannerTurn(
				[]store.EpicTaskInput{stoppedTask("the fix the stopped release needs")},
				store.EpicActionInput{Kind: store.ActionRetryEpic, Reason: "the release can run again"},
			))
			w.passQuiet()
			w.requireStopReplanned(reason, start, 0)
			w.requireBuildUnder(2)
			w.requireNotRefused()
		})
	}
}

// TestAnyStopReplansOnSendBack: for each cause a release stops with, the planner's send_back of a done task sends the
// release back to planning with the stop's reason, as the retry does, and the task sent back builds under the replan
// plan.
func TestAnyStopReplansOnSendBack(t *testing.T) {
	t.Parallel()
	for _, c := range anyStopCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newStopWorld(t, c.setup)
			start := c.stop(w)
			reason := w.requireStopped(c.fragment, 0)
			w.carryTurn(w.plannerTurn(nil, store.EpicActionInput{
				Kind: store.ActionSendBack, Task: 1, Reason: "the design is wrong for the release",
			}))
			w.passQuiet()
			w.requireStopReplanned(reason, start, 0)
			w.requireBuildUnder(1)
			w.requireNotRefused()
		})
	}
}

// TestAnyStopReplansOnReopenDesign: for each cause a release stops with, the planner's reopen_design of a design task
// sends the release back to planning with the stop's reason, as the retry does, and the design task that revises it
// builds under the replan plan.
func TestAnyStopReplansOnReopenDesign(t *testing.T) {
	t.Parallel()
	for _, c := range anyStopCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newStopWorld(t, c.setup)
			start := c.stop(w)
			reason := w.requireStopped(c.fragment, 0)
			w.carryTurn(w.plannerTurn(nil, store.EpicActionInput{
				Kind: store.ActionReopenDesign, Task: 1, Deliverable: "the design the release needs, revised",
				Reason: "the design must change before the release",
			}))
			w.passQuiet()
			w.requireStopReplanned(reason, start, 0)
			w.requireBuildUnder(2)
			w.requireNotRefused()
		})
	}
}

// TestAnyStopReplansOnOwnerRetry: for each cause a release stops with, the owner's RetryEpic sends the release back to
// planning with the stop's reason, as the planner's retry does, and a task the planner planned while the release was
// stopped builds under the replan plan.
func TestAnyStopReplansOnOwnerRetry(t *testing.T) {
	t.Parallel()
	for _, c := range anyStopCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newStopWorld(t, c.setup)
			start := c.stop(w)
			reason := w.requireStopped(c.fragment, 0)
			w.plannerTurn([]store.EpicTaskInput{stoppedTask("the fix the stopped release needs")})
			if err := w.s.RetryEpic(
				w.t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock(),
			); err != nil {
				w.t.Fatalf("RetryEpic: %v", err)
			}
			w.passQuiet()
			w.requireStopReplanned(reason, start, 0)
			w.requireBuildUnder(2)
		})
	}
}

// TestAnyStopReplansResumeRerunsStep: a release stopped at a failed release step is resumed by the planner's
// resume_release at the next attempt, which runs the stopped step again: its check starts under the new attempt. The
// step's undo changes nothing, so a step whose undo is none is resumed too, by the planner and by the owner's
// ReleaseEpic.
func TestAnyStopReplansResumeRerunsStep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		steps []store.EpicReleaseStep
		owner bool
	}{
		{name: "undo is a revert, by the planner", steps: []store.EpicReleaseStep{pushMainStep(), tagKindStep()}},
		{
			name:  "undo none, by the planner",
			steps: []store.EpicReleaseStep{undoNoneKindStep(), tagKindStep()},
		},
		{
			name:  "undo none, by the owner's ReleaseEpic",
			steps: []store.EpicReleaseStep{undoNoneKindStep(), tagKindStep()},
			owner: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newStopWorld(t, anyStopSetup{steps: c.steps})
			stepStop(w)
			w.requireStopped("the step exited 1\nstderr:\norigin refused the push", 0)
			checks := len(w.fakes.checks.calls)
			if c.owner {
				if err := w.s.ReleaseEpic(
					w.t.Context(),
					runEpic,
					store.Maker{By: store.MakerOwner},
					runClock(),
				); err != nil {
					t.Fatalf("ReleaseEpic: %v", err)
				}
			} else {
				w.carryTurn(w.plannerTurn(nil, store.EpicActionInput{
					Kind: store.ActionResumeRelease, Reason: "the step can run again",
				}))
			}
			w.passQuiet()
			flow := w.flowOf(runEpic)
			if flow.Epic.Phase != store.PhaseReleasing || flow.Epic.Parked != "" {
				t.Fatalf("epic phase %q parked %q, want releasing and unparked", flow.Epic.Phase, flow.Epic.Parked)
			}
			if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 1 {
				t.Errorf("release run = %+v, want running at attempt 1", run)
			}
			if len(w.fakes.checks.calls) <= checks ||
				w.fakes.checks.calls[len(w.fakes.checks.calls)-1].ID != w.stepID(1) {
				t.Errorf("check starts = %+v, want the stopped step started again at attempt 1", w.fakes.checks.calls)
			}
			if !c.owner {
				w.requireNotRefused()
			}
		})
	}
}

// TestAnyStopReplansRefusesRetryOfOwnerStop: a release its owner stopped stays with the owner. The planner's
// retry_epic, returned before the owner stopped the release, is carried out after the stop and refused, with a refusal
// that names the owner. The release stays stopped, with no replan stored.
func TestAnyStopReplansRefusesRetryOfOwnerStop(t *testing.T) {
	t.Parallel()
	w := newStopWorld(t, anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}})
	w.pass()
	w.kick()
	turn := w.plannerTurn(nil, store.EpicActionInput{Kind: store.ActionRetryEpic, Reason: "the release can run again"})

	open, err := w.s.StopEpic(w.t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock())
	if err != nil {
		t.Fatalf("StopEpic: %v", err)
	}
	if open != w.stepID(1) {
		t.Fatalf("StopEpic returned step %q, want the open step %q", open, w.stepID(1))
	}
	if err = w.s.ApplyEngine(w.t.Context(), []store.EngineOp{
		store.StepEnd{ID: open, End: "stopped", Status: "stopped"},
	}, runClock()); err != nil {
		t.Fatalf("end step %s: %v", open, err)
	}
	w.passQuiet()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.ParkClass != store.ParkClassReleaseOwnerStop {
		t.Fatalf("epic phase %q class %q, want stopped by the owner", flow.Epic.Phase, flow.Epic.ParkClass)
	}

	w.carryTurn(turn)
	refusal := "epic " + runEpic + ": " + ownerStopRefusal
	if causes := triggerCauses(t, w.s, ""); !slices.ContainsFunc(causes, func(cause string) bool {
		return strings.Contains(cause, refusal)
	}) {
		t.Errorf("triggers = %q, want the refusal %q recorded", causes, refusal)
	}
	flow = w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped || slices.ContainsFunc(flow.Releases, func(r store.ReleaseRun) bool {
		return r.Part == store.ReleasePartReplan
	}) {
		t.Errorf("epic phase %q releases %+v, want the release still stopped with no replan",
			flow.Epic.Phase, flow.Releases)
	}
}

// TestAnyStopReplansRefusesResumeAfterJudgeVerdict: a release its judge stopped with a verdict is not resumed by the
// planner's resume_release. The action is refused with the judge's verdict and a word to ask the steerer, the owner's
// own conversation on the epic, and the release stays stopped at its judge's attempt. The owner's ReleaseEpic then
// resumes the release at its next attempt. The planner's retry_epic of that release replans it, as
// TestAnyStopReplansOnPlannerRetry shows.
func TestAnyStopReplansRefusesResumeAfterJudgeVerdict(t *testing.T) {
	t.Parallel()
	w := newStopWorld(t, anyStopSetup{steps: []store.EpicReleaseStep{tagStep()}})
	judgeStop(w)
	reason := w.requireStopped("the judge did not release the epic", 0)
	w.carryTurn(w.plannerTurn(nil, store.EpicActionInput{
		Kind: store.ActionResumeRelease, Reason: "the judge read the promise too narrowly",
	}))
	w.passQuiet()

	if causes := triggerCauses(t, w.s, ""); !slices.ContainsFunc(causes, func(cause string) bool {
		return strings.Contains(cause, judgeStopPart) && strings.Contains(cause, judgeSteerPart)
	}) {
		t.Errorf("triggers = %q, want the judge's verdict and the word to ask the steerer in the refusal", causes)
	}
	if still := w.requireStopped("the judge did not release the epic", 0); still != reason {
		t.Errorf("stop reason = %q after the refusal, want the judge's %q", still, reason)
	}

	if err := w.s.ReleaseEpic(
		w.t.Context(), runEpic, store.Maker{By: store.MakerOwner}, runClock(),
	); err != nil {
		w.t.Fatalf("ReleaseEpic by the owner: %v", err)
	}
	w.passQuiet()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseReleasing || flow.Epic.Parked != "" {
		w.t.Fatalf("epic phase %q parked %q after the owner's release, want releasing and unparked",
			flow.Epic.Phase, flow.Epic.Parked)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != 1 {
		w.t.Errorf("release run = %+v after the owner's release, want running at attempt 1", run)
	}
}

// TestAnyStopReplansThreeTimesInARow: a release replanned three times in a row is not refused a fourth. Each time the
// first step fails, the planner's retry_epic sends the release back to planning, its plan ends and the release resumes
// at the next attempt. The fourth stop's retry replans it as the first did.
func TestAnyStopReplansThreeTimesInARow(t *testing.T) {
	t.Parallel()
	w := newStopWorld(t, anyStopSetup{steps: []store.EpicReleaseStep{pushMainStep(), tagKindStep()}})
	for attempt := range int64(4) {
		if attempt > 0 {
			turn, _ := w.claimReplanTurn()
			w.completeReplanTurn(turn, "complete")
			if run := runAt(t, w.s, releaseNodePath); run.State != store.NodeRunning || run.Attempt != attempt {
				t.Fatalf("release run = %+v, want running at attempt %d after the replan's plan ended", run, attempt)
			}
		}
		failStepOne(w)
		reason := w.requireStopped("the step exited 1\nstderr:\norigin refused the push", attempt)
		w.carryTurn(w.plannerTurn(nil, store.EpicActionInput{
			Kind: store.ActionRetryEpic, Reason: "the release can run again",
		}))
		w.passQuiet()
		w.requireStopReplanned(reason, "", attempt)
	}
	w.requireNotRefused()
}
