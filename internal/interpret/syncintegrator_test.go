package interpret

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// syncIntegratorRoundLimit is how many rounds a sync's integrator runs before its sync ends failed, as kinds allows.
const syncIntegratorRoundLimit = 3

// syncIntegratorDir returns the directory the integrator worktree of the sync at syncFirst is under, which is removed
// with the worktree once the sync ends.
func syncIntegratorDir(t *testing.T, w *syncWorld) string {
	t.Helper()
	return filepath.Join(w.runner.stateDir, releaseWorktrees, workID(t, w.s, syncFirst, integratorPart))
}

// syncRoundWorktree returns the worktree the sync at syncFirst's integrator rounds run in.
func syncRoundWorktree(t *testing.T, w *syncWorld) string {
	t.Helper()
	return filepath.Join(syncIntegratorDir(t, w), releaseWorktree)
}

// requireIntegratorRemoved fails unless the integrator worktree directory of the sync at syncFirst is gone.
func requireIntegratorRemoved(t *testing.T, w *syncWorld) {
	t.Helper()
	if _, err := os.Stat(syncIntegratorDir(t, w)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("integrator worktree of sync %s: stat = %v, want it removed when the sync ended", syncFirst, err)
	}
}

// commitRound writes text to b.go in the integrator worktree wt and commits the merge of origin's commit into the
// stored base candidate with the sync's message, amending the merge the round before made when amend is set. It
// returns the commit HEAD names.
func commitRound(t *testing.T, wt, candidate, origin, text string, amend bool) string {
	t.Helper()
	nodeWrite(t, wt, "b.go", text)
	nodeGit(t, wt, "add", "b.go")
	message := fmt.Sprintf("Merge origin's commit %s into stored base %s", origin, candidate)
	if amend {
		nodeGit(t, wt, "commit", "-q", "--amend", "-m", message)
	} else {
		nodeGit(t, wt, "commit", "-q", "-m", message)
	}
	return nodeGit(t, wt, "rev-parse", "HEAD")
}

// endRoundGate ends the Done gate check the last pass started, which must be the round's gate at head, with exit and
// stderr. Checks are recorded in the order they start, so the last call is the gate that pass started.
func endRoundGate(t *testing.T, w *syncWorld, head string, exit int, stderr string) {
	t.Helper()
	calls := w.fakes.checks.calls
	if len(calls) == 0 {
		t.Fatal("no check was started, want the round's Done gate")
	}
	gate := calls[len(calls)-1]
	if gate.Revision != head || gate.Path != syncFirst {
		t.Fatalf("last check = %+v, want the round's Done gate of sync %s at %s", gate, syncFirst, head)
	}
	nodeEndCheck(t, w.s, gate.ID, exit, stderr)
}

// endRoundCapture ends the capture of R1 the last pass started, which must be the round's green capture at head, as a
// passing judge holds it.
func endRoundCapture(t *testing.T, w *syncWorld, head string) {
	t.Helper()
	calls := w.fakes.evidence.calls
	if len(calls) == 0 {
		t.Fatal("no capture was started, want the round's green capture")
	}
	capture := calls[len(calls)-1]
	if capture.revision != head || capture.item != "R1" || capture.phase != evidence.PhaseGreen {
		t.Fatalf("last capture = %+v, want R1's green capture at %s", capture, head)
	}
	nodeEndEvidence(t, w.s, capture.id)
}

// hasMarkers reports whether content holds a conflict marker line, as the integrator's commit check does.
func hasMarkers(content string) bool {
	return slices.ContainsFunc(strings.Split(content, "\n"), func(line string) bool {
		return strings.HasPrefix(line, "<<<<<<< ") || line == "=======" || strings.HasPrefix(line, ">>>>>>> ")
	})
}

// roundStep returns the id of the step of round of the sync at syncFirst's integrator.
func roundStep(t *testing.T, w *syncWorld, round int64) string {
	t.Helper()
	return workID(t, w.s, syncFirst, fmt.Sprintf("integrator %d", round))
}

// TestSyncIntegratorEndToEnd walks a conflicting sync's integrator from round 1 to the end of the sync, with its steps
// faked: each subtest ends the steps the way the step service does, and each pass is the runner's own.
func TestSyncIntegratorEndToEnd(t *testing.T) {
	t.Parallel()
	t.Run("resolved", syncResolvedEndsPassedAtItsHead)
	t.Run("gate_failure", syncGateFailureStartsRoundTwo)
	t.Run("blocked", syncBlockedEndsFailedAtOnce)
	t.Run("third_round", syncThirdRoundGateFailureEndsFailed)
	t.Run("restart", syncRestartEndsSkipped)
}

// syncResolvedEndsPassedAtItsHead: round 1 starts in its own worktree on the merge of origin's commit into the stored
// base. Its resolved commit has the stored base and then origin's commit as its parents and no conflict marker, passes
// its Done gate and its capture at that head, and then the stored base and the branch move to it.
func syncResolvedEndsPassedAtItsHead(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	wt := syncRoundWorktree(t, w)
	if starts := syncIntegratorStarts(w); len(starts) != 1 || starts[0].Workspace != wt ||
		starts[0].Revision != candidate {
		t.Fatalf("integrator starts = %+v, want round 1 alone in %s at %s", starts, wt, candidate)
	}
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != originHead {
		t.Fatalf("worktree MERGE_HEAD = %s, want origin's commit %s", mergeHead, originHead)
	}

	head := commitRound(t, wt, candidate, originHead, "two\norigin\n", false)
	if parents := strings.Fields(w.git("rev-list", "--parents", "-n", "1", head)); !reflect.DeepEqual(
		parents, []string{head, candidate, originHead},
	) {
		t.Fatalf("commit parents = %v, want the stored base %s then origin's commit %s", parents, candidate, originHead)
	}
	if content := w.git("show", head+":b.go"); hasMarkers(content) {
		t.Fatalf("b.go = %q, want no conflict marker", content)
	}
	endSyncStep(t, w.s, roundStep(t, w, 1), "done", "done", syncDoneReturn(head))
	w.pass()
	endRoundGate(t, w, head, 0, "")
	w.pass()
	endRoundCapture(t, w, head)
	w.pass()

	w.requireSyncEnded(syncFirst, outcomePassed, fmt.Sprintf(
		"merged origin's commit %s into the stored base %s as %s", originHead, candidate, head,
	))
	epic := epicOf(t, w.s)
	if epic.StoredBase != head || epic.Synced != originHead || epic.Candidate != "" {
		t.Errorf("stored base %q, synced %q, candidate %q, want %q, %q and none", epic.StoredBase, epic.Synced,
			epic.Candidate, head, originHead)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != head {
		t.Errorf("branch = %s, want the round's head %s", tip, head)
	}
	requireIntegratorRemoved(t, w)
}

// syncGateFailureStartsRoundTwo: a round whose head fails its Done gate leaves the stored base unmoved, and starts
// round 2 in the same worktree with that failure in its prompt, while the sync and its worktree are still running.
func syncGateFailureStartsRoundTwo(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	wt := syncRoundWorktree(t, w)
	head := commitRound(t, wt, candidate, originHead, "two\norigin\n", false)
	endSyncStep(t, w.s, roundStep(t, w, 1), "done", "done", syncDoneReturn(head))
	w.pass()
	endRoundGate(t, w, head, 1, "lint failed")
	w.pass()

	w.requireUnmoved(candidate)
	starts := syncIntegratorStarts(w)
	if len(starts) != 2 {
		t.Fatalf("integrator starts = %+v, want round 2 after the failed gate", starts)
	}
	round2 := starts[1]
	failure := fmt.Sprintf("Done gate lint failed on commit %s: it exited 1.", head)
	if round2.ID != roundStep(t, w, 2) || round2.Workspace != wt || !strings.Contains(round2.Prompt, failure) {
		t.Errorf("round 2 = %+v, want it in %s with %q in its prompt", round2, wt, failure)
	}
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeRunning {
		t.Errorf("sync run = %+v, want it still running while round 2 runs", run)
	}
	if _, err := os.Stat(syncIntegratorDir(t, w)); err != nil {
		t.Errorf("integrator worktree of a running sync: %v, want it kept", err)
	}
}

// syncBlockedEndsFailedAtOnce: a blocked return ends the sync failed in the pass that records it, with the integrator's
// report in its sync trigger, and starts no further round.
func syncBlockedEndsFailedAtOnce(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	endSyncStep(t, w.s, roundStep(t, w, 1), "returned", "blocked", syncBlockedReturn())
	w.pass()

	if starts := syncIntegratorStarts(w); len(starts) != 1 {
		t.Errorf("integrator starts = %+v, want round 1 alone: a blocked round starts no next", starts)
	}
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("sync run = %+v, want ended failed at once", run)
	}
	report := fmt.Sprintf(
		"the release integrator stopped the merge of origin's commit %s into stored base %s:\n"+
			"- b.go: side: merge; decision: keep the candidate's file; candidate: two; origin: origin",
		originHead, candidate,
	)
	triggers := syncTriggers(t, w)
	if len(triggers) != 1 || !strings.Contains(triggers[0].cause, report) {
		t.Errorf("sync triggers = %+v, want one holding the report %q", triggers, report)
	}
	requireIntegratorRemoved(t, w)
}

// syncThirdRoundGateFailureEndsFailed: each round's head fails its Done gate, so the sync runs its three rounds and
// ends failed once the third's gate fails. Its trigger holds that failure and the last report, and the stored base
// stays.
func syncThirdRoundGateFailureEndsFailed(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	wt := syncRoundWorktree(t, w)
	var head string
	for round := int64(1); round <= syncIntegratorRoundLimit; round++ {
		head = commitRound(t, wt, candidate, originHead, fmt.Sprintf("two\norigin %d\n", round), round > 1)
		endSyncStep(t, w.s, roundStep(t, w, round), "done", "done", syncDoneReturn(head))
		w.pass()
		endRoundGate(t, w, head, 1, "lint failed")
		w.pass()
	}

	if starts := syncIntegratorStarts(w); len(starts) != syncIntegratorRoundLimit {
		t.Errorf("integrator starts = %d, want one for each of the %d rounds", len(starts), syncIntegratorRoundLimit)
	}
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("sync run = %+v, want ended failed after the third round", run)
	}
	triggers := syncTriggers(t, w)
	failure := fmt.Sprintf("Done gate lint failed on commit %s: it exited 1.", head)
	if len(triggers) != 1 || !strings.Contains(triggers[0].cause, failure) ||
		!strings.Contains(triggers[0].cause, "last report: b.go keeps both lines") {
		t.Errorf("sync triggers = %+v, want one holding %q and the last report", triggers, failure)
	}
	w.requireUnmoved(candidate)
	requireIntegratorRemoved(t, w)
}

// syncRestartEndsSkipped: a round whose step ended engine_restarted ends the sync skipped and untold, with the stored
// base unmoved, and origin's commit is not counted as tried.
func syncRestartEndsSkipped(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	endSyncStep(t, w.s, roundStep(t, w, 1), "engine_restarted", "", nil)
	w.pass()

	if run := runAt(t, w.s, syncFirst); run.State != store.NodeEnded || run.Outcome != outcomeSkipped {
		t.Errorf("sync run = %+v, want ended skipped", run)
	}
	flow, err := w.s.EpicFlow(t.Context(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	if tried := kinds.SyncTriedBy(flowRows(flow), originHead); tried != "" {
		t.Errorf("SyncTriedBy(%s) = %q, want no try of origin's commit", originHead, tried)
	}
	w.requireUnmoved(candidate)
	requireIntegratorRemoved(t, w)
}

// TestSyncFailWakesOnAnUnresolvedConflict: a conflicted sync whose integrator returns blocked leaves one pending sync
// trigger, whose cause names origin's commit and the stored base and holds the integrator's report. The planner claims
// a turn for that trigger.
func TestSyncFailWakesOnAnUnresolvedConflict(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	endSyncStep(t, w.s, roundStep(t, w, 1), "returned", "blocked", syncBlockedReturn())
	w.pass()

	gap := fmt.Sprintf("sync:%s@%s", originHead, candidate)
	conflict := fmt.Sprintf(
		"merge of origin's commit %s into the stored base %s conflicts in b.go", originHead, candidate,
	)
	report := fmt.Sprintf(
		"the release integrator stopped the merge of origin's commit %s into stored base %s:\n"+
			"- b.go: side: merge; decision: keep the candidate's file; candidate: two; origin: origin",
		originHead, candidate,
	)
	requireSyncWake(t, w, fmt.Sprintf(
		"sync %s: the sync of origin's commit %s into the stored base %s failed\n%s\n\n%s",
		gap, originHead, candidate, conflict, report,
	))
}
