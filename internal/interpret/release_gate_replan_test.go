package interpret

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// newGateReplanWorld returns a replan world whose epic's branch and origin's main change different files, so the
// release's merge of origin's commit is clean. The epic runs replanFlow, its plan has ended, and its release has not
// started, as an epic that ended clean leaves it.
func newGateReplanWorld(t *testing.T) replanWorld {
	t.Helper()
	w := newReleaseWorld(t, &recordingEnding{})
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

// endGateWith ends the Done gate check of the release run with end, as its holder would, and passes.
func (w replanWorld) endGateWith(end store.NodeCheckEnd) {
	w.t.Helper()
	id := workID(w.t, w.s, releaseNodePath, "done 1")
	if err := w.s.EndNodeCheck(w.t.Context(), id, end, runClock()); err != nil {
		w.t.Fatalf("EndNodeCheck %s: %v", id, err)
	}
	if logged := w.pass(); logged != "" {
		w.t.Fatalf("pass logged: %s", logged)
	}
}

// gateTriggerIDs returns the ids of the triggers epic has stored.
func gateTriggerIDs(t *testing.T, s *store.Store, epic string) []string {
	t.Helper()
	history, err := s.EpicHistory(context.Background(), epic)
	if err != nil {
		t.Fatalf("EpicHistory of %s: %v", epic, err)
	}
	ids := make([]string, 0, len(history.Triggers))
	for _, trigger := range history.Triggers {
		ids = append(ids, trigger.ID)
	}
	return ids
}

// requireGateReplanned fails unless the release went back to planning for the gate's report: the epic is correcting and
// unparked, the release run still runs at its first attempt, the replan's plan runs under it, a replan of that attempt
// holds origin's commit as its head and the report as its reason, a release trigger is stored, the integrator's worktree
// is gone and the owner is told.
func (w replanWorld) requireGateReplanned(report string) {
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
		return r.Part == store.ReleasePartReplan && r.Attempt == 0 && r.Head == w.origin && r.Reason == report
	}) {
		w.t.Errorf("release runs = %+v, want a replan of attempt 0 holding origin %s and the report",
			flow.Releases, w.origin)
	}
	if !slices.ContainsFunc(gateTriggerIDs(w.t, w.s, runEpic), func(id string) bool {
		return strings.HasPrefix(id, replanTriggerPrefix)
	}) {
		w.t.Errorf("trigger ids = %q, want a %s trigger", gateTriggerIDs(w.t, w.s, runEpic), replanTriggerPrefix)
	}
	if _, err := os.Stat(w.integratorDir()); !os.IsNotExist(err) {
		w.t.Errorf("integrator directory stat = %v, want it removed", err)
	}
	if tells := nodeTells(w.t, w.path); !slices.Contains(tells, replanCause+report) {
		w.t.Errorf("tells = %q, want %q", tells, replanCause+report)
	}
}

// requireGateStopped fails unless the release stopped on reason: the epic is stopped and parked with it, the release run
// is parked with it, and the release was sent back to planning no more than replans times, the replans it had before the
// stop. Each of those is a replan release run and a release trigger, and the stop adds neither.
func (w replanWorld) requireGateStopped(reason string, replans int) {
	w.t.Helper()
	flow := w.flowOf(runEpic)
	if flow.Epic.Phase != store.PhaseStopped || flow.Epic.Parked != reason {
		w.t.Errorf(
			"epic phase %q parked %q, want stopped and parked with %q",
			flow.Epic.Phase,
			flow.Epic.Parked,
			reason,
		)
	}
	if run := runAt(w.t, w.s, releaseNodePath); run.State != store.NodeParked || run.Output != reason {
		w.t.Errorf("release run = %+v, want parked with %q", run, reason)
	}
	var replanRuns int
	for _, run := range flow.Releases {
		if run.Part == store.ReleasePartReplan {
			replanRuns++
		}
	}
	if replanRuns != replans {
		w.t.Errorf("replan runs = %d, want %d: %+v", replanRuns, replans, flow.Releases)
	}
	var releaseTriggers int
	for _, id := range gateTriggerIDs(w.t, w.s, runEpic) {
		if strings.HasPrefix(id, replanTriggerPrefix) {
			releaseTriggers++
		}
	}
	if releaseTriggers != replans {
		w.t.Errorf("%s triggers = %d, want %d", replanTriggerPrefix, releaseTriggers, replans)
	}
}

// gateFailureReport is the report a Done gate that exited 1 on commit gives, with no output.
func gateFailureReport(commit string) string {
	return fmt.Sprintf("Done gate %s failed on commit %s: it exited 1.", integrateDoneGate, commit)
}

// TestReleaseGateReplan: a Done gate that ran and failed on the release candidate sends the release back to planning
// once per origin tip, as a release integrator's blocked round does, and a gate that did not run or timed out stops it.
// A second gate failure from the origin tip an earlier replan recorded stops the release.
func TestReleaseGateReplan(t *testing.T) {
	t.Parallel()
	t.Run("gate that ran and failed on a cleanly merged candidate replans", func(t *testing.T) {
		t.Parallel()
		w := newGateReplanWorld(t)
		w.pass()
		candidate := runAt(t, w.s, releaseNodePath).Revision
		w.endGate(1)
		w.pass()
		w.requireGateReplanned(gateFailureReport(candidate))
	})
	t.Run("gate that ran and failed on the last round of the integrator replans", func(t *testing.T) {
		t.Parallel()
		w := newReplanWorld(t)
		w.pass()
		merge := w.commitMerge()
		// The release integrator has three rounds, and the gate of the last one that fails replans.
		for round := 1; round <= 3; round++ {
			w.endIntegrator(round, merge)
			nodeEndCheck(t, w.s, w.gateID(round), 1, "")
			w.pass()
		}
		w.requireGateReplanned(gateFailureReport(merge))
	})
	t.Run("gate that did not run stops the release", func(t *testing.T) {
		t.Parallel()
		w := newGateReplanWorld(t)
		w.pass()
		candidate := runAt(t, w.s, releaseNodePath).Revision
		w.endGateWith(store.NodeCheckEnd{NotRun: "the check did not start"})
		w.requireGateStopped(fmt.Sprintf("done gate %s did not pass at %s", integrateDoneGate, candidate), 0)
	})
	t.Run("gate that timed out stops the release", func(t *testing.T) {
		t.Parallel()
		w := newGateReplanWorld(t)
		w.pass()
		candidate := runAt(t, w.s, releaseNodePath).Revision
		w.endGateWith(store.NodeCheckEnd{TimedOut: true})
		w.requireGateStopped(fmt.Sprintf("done gate %s did not pass at %s", integrateDoneGate, candidate), 0)
	})
	t.Run("second gate failure from the origin tip an earlier replan recorded stops the release", func(t *testing.T) {
		t.Parallel()
		w := newGateReplanWorld(t)
		w.pass()
		w.endGate(1)
		w.pass()
		w.requireGateReplanned(gateFailureReport(runAt(t, w.s, releaseNodePath).Revision))

		turn, _ := w.claimReplanTurn()
		w.completeReplanTurn(turn, "complete")
		again := runAt(t, w.s, releaseNodePath)
		if again.Attempt != 1 || again.Revision == "" {
			t.Fatalf("release run = %+v, want attempt 1 with its candidate prepared again", again)
		}
		w.endGate(1)
		w.pass()
		w.requireGateStopped(fmt.Sprintf("done gate %s did not pass at %s; origin's main has not moved since the last "+
			"replan", integrateDoneGate, again.Revision), 1)
	})
}
