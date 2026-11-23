package interpret

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// newSyncStepWorld returns a sync world whose first batch has passed, with origin's main holding a commit on b.go that the
// stored base also adds, so the sync's merge conflicts in b.go. Its runner gets a state directory, and the roles of
// integratorRoles when withRole is set. It returns the world, the stored base and origin's commit.
func newSyncStepWorld(t *testing.T, withRole bool) (*syncWorld, string, string) {
	t.Helper()
	w := newSyncWorld(t)
	candidate := w.toCaptured()
	if withRole {
		w.runner.getenv = integratorRoles(t)
	}
	w.runner.stateDir = t.TempDir()
	originHead := w.originCommit("b.go")
	return w, candidate, originHead
}

// syncIntegratorStarts returns the starts of the steps of the sync's integrator, in order.
func syncIntegratorStarts(w *syncWorld) []enginehost.StepStart {
	var starts []enginehost.StepStart
	for _, start := range w.fakes.steps.calls {
		if start.Schema == IntegratorSchema {
			starts = append(starts, start)
		}
	}
	return starts
}

// syncWorktree returns the worktree the sync run at path's integrator runs its rounds in.
func syncWorktree(w *syncWorld, path string) string {
	dir := filepath.Join(w.runner.stateDir, releaseWorktrees, workID(w.t, w.s, path, integratorPart))
	return filepath.Join(dir, releaseWorktree)
}

// requireSyncRounds fails unless the integrate rounds of the test epic are want, in every field but Created, which is
// when each was recorded.
func requireSyncRounds(t *testing.T, s *store.Store, want []store.IntegrateRound) {
	t.Helper()
	flow, err := s.EpicFlow(t.Context(), runEpic)
	if err != nil {
		t.Fatalf("EpicFlow: %v", err)
	}
	got := flow.Rounds
	for i := range got {
		got[i].Created = time.Time{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("integrate rounds = %+v, want %+v", got, want)
	}
}

// endSyncStep ends the step with id as the step service does: its return is stored as result when result is not nil,
// and the step ends with end and status.
func endSyncStep(t *testing.T, s *store.Store, id, end, status string, result map[string]any) {
	t.Helper()
	var ops []store.EngineOp
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode return of step %s: %v", id, err)
		}
		ops = append(ops, store.StepReturn{ID: id, Result: string(encoded)})
	}
	ops = append(ops, store.StepEnd{ID: id, End: end, Status: status})
	if err := s.ApplyEngine(t.Context(), ops, runClock()); err != nil {
		t.Fatalf("end step %s: %v", id, err)
	}
}

// syncDoneReturn is a done return of the sync's integrator that names commit and resolves b.go by keeping both sides.
func syncDoneReturn(commit string) map[string]any {
	return map[string]any{
		"status": "done", "commit": commit,
		"resolutions":   []map[string]any{{"path": "b.go", "took": "both", "kept": "both lines stay"}},
		"other_changes": []map[string]any{}, "checks": []map[string]any{}, "unresolved": []map[string]any{},
		"evidence": "b.go keeps both lines",
	}
}

// syncBlockedReturn is a blocked return of the sync's integrator that stops on b.go, on a decision that is how to
// resolve the merge.
func syncBlockedReturn() map[string]any {
	return map[string]any{
		"status": "blocked", "commit": "",
		"resolutions": []map[string]any{}, "other_changes": []map[string]any{}, "checks": []map[string]any{},
		"unresolved": []map[string]any{{
			"path": "b.go", "candidate": "two", "origin": "origin", "decision": "keep the candidate's file",
			"side": "merge",
		}},
		"evidence": "b.go holds two versions of one file",
	}
}

// TestSyncIntegratorStepStartsRoundOneInItsWorktree: a sync whose merge conflicts starts round 1 of its integrator as an
// unowned writing step, labeled with the epic and the sync run's path, in a worktree of its own where the merge of origin's
// commit is in progress over the stored base.
func TestSyncIntegratorStepStartsRoundOneInItsWorktree(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()

	starts := syncIntegratorStarts(w)
	if len(starts) != 1 {
		t.Fatalf("integrator starts = %+v, want round 1 alone", starts)
	}
	start := starts[0]
	wt := syncWorktree(w, syncFirst)
	if start.ID != workID(t, w.s, syncFirst, "integrator 1") || start.Agent != integratorAgent ||
		start.Workspace != wt || start.Revision != candidate || !start.Unowned || start.DoneValue != "done" ||
		start.Epic != runEpic || start.Task != syncFirst || start.ReturnCheck == nil {
		t.Errorf("integrator start = %+v, want round 1 unowned under %s in %s at %s, labeled %s and %s",
			start, integratorAgent, wt, candidate, runEpic, syncFirst)
	}
	if head := nodeGit(t, wt, "rev-parse", "HEAD"); head != candidate {
		t.Errorf("worktree HEAD = %s, want the stored base %s", head, candidate)
	}
	if mergeHead := nodeGit(t, wt, "rev-parse", "MERGE_HEAD"); mergeHead != originHead {
		t.Errorf("worktree MERGE_HEAD = %s, want origin's commit %s", mergeHead, originHead)
	}
	if unmerged := nodeGit(t, wt, "diff", "--name-only", "--diff-filter=U"); unmerged != "b.go" {
		t.Errorf("unmerged paths = %q, want b.go", unmerged)
	}
	if !strings.HasPrefix(start.Prompt, "You are the sync integrator for one epic.") {
		t.Errorf("prompt starts %q, want the sync role text", start.Prompt[:min(len(start.Prompt), 80)])
	}
	for _, want := range []string{
		"Stored base: " + candidate,
		"Origin's commit: " + originHead + " from origin's refs/heads/main",
		"Commit message: Merge origin's commit " + originHead + " into stored base " + candidate,
		"- AA b.go\n",
		"## Previous attempt\nNone.\n",
	} {
		if !strings.Contains(start.Prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, start.Prompt)
		}
	}
}

// TestSyncIntegratorStepPassedRoundRecordsItsHead: a done return whose commit has the stored base and then origin's commit
// as its parents, and no conflict marker, records round 1 passed with that commit as its head, and pins the commit under
// the sync's ref for origin's commit.
func TestSyncIntegratorStepPassedRoundRecordsItsHead(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	id := workID(t, w.s, syncFirst, "integrator 1")
	wt := syncWorktree(w, syncFirst)
	nodeWrite(t, wt, "b.go", "two\norigin\n")
	nodeGit(t, wt, "add", "b.go")
	message := fmt.Sprintf("Merge origin's commit %s into stored base %s", originHead, candidate)
	nodeGit(t, wt, "commit", "-q", "-m", message)
	commit := nodeGit(t, wt, "rev-parse", "HEAD")
	if parents := strings.Fields(w.git("rev-list", "--parents", "-n", "1", commit)); !reflect.DeepEqual(
		parents, []string{commit, candidate, originHead},
	) {
		t.Fatalf("commit parents = %v, want the stored base %s then origin's commit %s", parents, candidate, originHead)
	}
	endSyncStep(t, w.s, id, "done", "done", syncDoneReturn(commit))
	w.pass()

	requireSyncRounds(t, w.s, []store.IntegrateRound{{
		Path: syncFirst, Attempt: runAt(t, w.s, syncFirst).Attempt, Round: 1, Step: id, Verdict: "merged", Head: commit,
		Explanation: "b.go keeps both lines", Outcome: "passed",
	}})
	if pinned := w.git("rev-parse", syncRefRoot+originHead); pinned != commit {
		t.Errorf("pin of origin's commit = %s, want the commit %s", pinned, commit)
	}
}

// TestSyncIntegratorStepBlockedRoundRecordsItsReport: a blocked return records round 1 failed with the verdict blocked,
// and its report names the stored base and each path it stopped on, and the sync ends failed.
func TestSyncIntegratorStepBlockedRoundRecordsItsReport(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, true)
	w.pass()
	id := workID(t, w.s, syncFirst, "integrator 1")
	endSyncStep(t, w.s, id, "returned", "blocked", syncBlockedReturn())
	w.pass()

	requireSyncRounds(t, w.s, []store.IntegrateRound{{
		Path: syncFirst, Attempt: runAt(t, w.s, syncFirst).Attempt, Round: 1, Step: id, Verdict: "blocked",
		Outcome: "failed",
		Explanation: fmt.Sprintf(
			"the release integrator stopped the merge of origin's commit %s into stored base %s:\n"+
				"- b.go: side: merge; decision: keep the candidate's file; candidate: two; origin: origin",
			originHead, candidate),
	}})
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeEnded || run.Outcome != outcomeFailed {
		t.Errorf("sync run = %+v, want ended failed", run)
	}
}

// TestSyncIntegratorStepRestartedRoundIsSkipped: a step that ended engine_restarted records round 1 skipped, and the sync
// ends skipped, so origin's commit is left untried.
func TestSyncIntegratorStepRestartedRoundIsSkipped(t *testing.T) {
	t.Parallel()
	w, _, _ := newSyncStepWorld(t, true)
	w.pass()
	id := workID(t, w.s, syncFirst, "integrator 1")
	endSyncStep(t, w.s, id, "engine_restarted", "", nil)
	w.pass()

	requireSyncRounds(t, w.s, []store.IntegrateRound{{
		Path: syncFirst, Attempt: runAt(t, w.s, syncFirst).Attempt, Round: 1, Step: id, Verdict: "ended",
		Outcome: "skipped",
	}})
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeEnded || run.Outcome != outcomeSkipped {
		t.Errorf("sync run = %+v, want ended skipped", run)
	}
}

// TestSyncIntegratorStepWithoutRoleRecordsRoundOneFailed: a world that names no integrator role starts no step, and
// records round 1 failed with the conflict and why the role did not resolve, so the sync ends failed.
func TestSyncIntegratorStepWithoutRoleRecordsRoundOneFailed(t *testing.T) {
	t.Parallel()
	w, candidate, originHead := newSyncStepWorld(t, false)
	w.pass()

	if starts := syncIntegratorStarts(w); len(starts) != 0 {
		t.Errorf("integrator starts = %+v, want none", starts)
	}
	requireSyncRounds(t, w.s, []store.IntegrateRound{{
		Path: syncFirst, Attempt: runAt(t, w.s, syncFirst).Attempt, Round: 1,
		Step: workID(t, w.s, syncFirst, "integrator 1"), Verdict: "ended", Outcome: "failed",
		Failure: fmt.Sprintf(
			"merge of origin's commit %s into the stored base %s conflicts in b.go: "+
				"neither XDG_CONFIG_HOME nor HOME is set",
			originHead, candidate),
	}})
}
