package interpret

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// The paths of the sync runs of the test plan for turns 1, 2 and 3, and the batches of turns 2 and 3: the settle walks of
// tasks 4 and 6.
const (
	syncFirst   = "plan/M1"
	syncSecond  = "plan/M2"
	syncThird   = "plan/M3"
	thirdBatch  = "plan/S6/integrate"
	syncRefRoot = "refs/orrery/epic-runner/sync/"
)

// syncWorld is a sync test's world: an integrate world whose repository has a bare origin as its remote origin, with
// main pushed to it, and the identity the sync's merge commit is written under.
type syncWorld struct {
	*integrateWorld
	origin string
}

// newSyncWorld returns a sync world with the test epic stored at its base and its branch at base.
func newSyncWorld(t *testing.T) *syncWorld {
	t.Helper()
	w := newIntegrateWorld(t)
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.invalid")
	return &syncWorld{integrateWorld: w, origin: mergeRemote(t, w.repo)}
}

// newSyncWorldAhead returns a sync world whose epic's base is a commit on main that origin lacks: main is pushed to
// origin, and then the base is committed on main and not pushed.
func newSyncWorldAhead(t *testing.T) *syncWorld {
	t.Helper()
	var origin string
	w := newIntegrateWorld(t, madeBy(func(t *testing.T) (string, string) {
		t.Helper()
		repo, _ := nodeRepo(t)
		origin = mergeRemote(t, repo)
		nodeWrite(t, repo, "local.txt", "local\n")
		nodeGit(t, repo, "add", "local.txt")
		nodeGit(t, repo, "commit", "-q", "-m", "local, not pushed")
		return repo, nodeGit(t, repo, "rev-parse", "HEAD")
	}))
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.invalid")
	return &syncWorld{integrateWorld: w, origin: origin}
}

// passed carries one pass out on the integrate flow, fails the test when it logged anything, and reports whether the
// plan run was given its turn, which is whether the planner is passed for the epic.
func (w *integrateWorld) passed() bool {
	w.t.Helper()
	planned := w.runner.passFlow(context.Background(), epicOf(w.t, w.s), integrateFlow())
	if w.logs.Len() > 0 {
		w.t.Fatalf("pass logged: %s", w.logs.String())
	}
	return planned
}

// originCommit commits file to origin's main from a clone, as another machine pushes to it, and returns the commit.
func (w *syncWorld) originCommit(file string) string {
	w.t.Helper()
	return mergeOriginCommit(w.t, w.origin, file)
}

// forceOrigin commits file on the epic's base in a clone of origin and pushes it over origin's main, as a force-push
// that rewrote origin's main does, and returns the commit.
func (w *syncWorld) forceOrigin(file string) string {
	w.t.Helper()
	clone := w.t.TempDir()
	nodeGit(w.t, clone, "clone", "-q", w.origin, ".")
	nodeGit(w.t, clone, "reset", "-q", "--hard", w.base)
	nodeWrite(w.t, clone, file, "forced\n")
	nodeGit(w.t, clone, "add", file)
	nodeGit(w.t, clone, "commit", "-q", "-m", "forced "+file)
	nodeGit(w.t, clone, "push", "-q", "--force", "origin", "main")
	return nodeGit(w.t, clone, "rev-parse", "HEAD")
}

// toSyncGate runs the first batch to its end with toCaptured, then moves origin's main as syncGate does. It returns the
// batch's candidate, origin's commit and the sync run.
func (w *syncWorld) toSyncGate() (string, string, store.NodeRun) {
	w.t.Helper()
	candidate := w.toCaptured()
	originHead, sync := w.syncGate()
	return candidate, originHead, sync
}

// syncGate moves origin's main by a commit of origin.txt once the first batch has run to its end, which the pass that
// ends the batch merges into the stored base and starts the sync's Done gate at. It returns origin's commit and the sync
// run. The planner gets no turn in that pass.
func (w *syncWorld) syncGate() (string, store.NodeRun) {
	w.t.Helper()
	originHead := w.originCommit("origin.txt")
	if w.passed() {
		w.t.Error("the pass that started the sync gave the planner a turn")
	}
	return originHead, runAt(w.t, w.s, syncFirst)
}

// syncAdvance runs the sync of syncGate through its Done gate and its capture, both passing, and returns what syncGate
// returns. The pass after it advances the sync.
func (w *syncWorld) syncAdvance() (string, store.NodeRun) {
	w.t.Helper()
	originHead, sync := w.syncGate()
	w.endGate(syncFirst, 0, "")
	w.pass()
	w.endCapture(syncFirst)
	return originHead, sync
}

// claimTurn starts a planner turn on the test epic, as a planner goroutine an earlier orreryd left would, so the epic's
// work is no longer at rest. It returns the turn's seq.
func (w *integrateWorld) claimTurn() int64 {
	w.t.Helper()
	turn, err := w.s.ClaimTurn(context.Background(), runEpic, runClock())
	if err != nil {
		w.t.Fatalf("ClaimTurn: %v", err)
	}
	return turn.Seq
}

// nextTurn plans the next turn's two tasks on base, as files named after their seqs, accepts both and runs the batch of
// the turn's settle walk at path to its end, both its gate and its capture passing. The pass that ends it is left to the
// caller.
func (w *integrateWorld) nextTurn(base string, first, second int64, path string) {
	w.t.Helper()
	one := w.commit(base, fmt.Sprintf("t%d.go", first), "one\n")
	two := w.commit(base, fmt.Sprintf("t%d.go", second), "two\n")
	w.planTurn()
	w.accept(first, base, one)
	w.accept(second, base, two)
	w.pass()
	w.endGate(path, 0, "")
	w.pass()
	w.endCapture(path)
}

// endCaptureFailed ends the capture with id as a judge that finds its conclusion does not hold ends it.
func (w *integrateWorld) endCaptureFailed(id string) {
	w.t.Helper()
	ctx := context.Background()
	if err := w.s.RecordEvidenceCapture(ctx, id, store.EvidenceCapture{At: runClock()}); err != nil {
		w.t.Fatalf("RecordEvidenceCapture %s: %v", id, err)
	}
	verdict := store.EvidenceVerdict{Verdict: evidence.VerdictFails, Reason: "the runner stopped", At: runClock()}
	if err := w.s.RecordEvidenceVerdict(ctx, id, verdict); err != nil {
		w.t.Fatalf("RecordEvidenceVerdict %s: %v", id, err)
	}
}

// requireSyncEnded fails unless the sync run at path ended with outcome and output.
func (w *integrateWorld) requireSyncEnded(path, outcome, output string) store.NodeRun {
	w.t.Helper()
	run := runAt(w.t, w.s, path)
	if run.State != store.NodeEnded || run.Outcome != outcome || run.Output != output {
		w.t.Errorf("sync %s = %+v, want ended %s with output %q", path, run, outcome, output)
	}
	return run
}

// requireUnmoved fails unless the epic's branch and stored base are both still at base and the epic synced nothing.
func (w *integrateWorld) requireUnmoved(base string) {
	w.t.Helper()
	epic := epicOf(w.t, w.s)
	if epic.StoredBase != base || epic.Synced != "" || epic.Parked != "" {
		w.t.Errorf("epic stored base %q, synced %q, parked %q, want %q, none and not parked", epic.StoredBase,
			epic.Synced, epic.Parked, base)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != base {
		w.t.Errorf("branch = %s, want it left at %s", tip, base)
	}
}

// requireRead fails unless the test epic's last read of origin's main was made for the stored base base at the
// runner's clock and found commit as state.
func (w *integrateWorld) requireRead(base, commit, state string) {
	w.t.Helper()
	want := store.OriginRead{Base: base, Commit: commit, State: state, At: w.runner.now()}
	if read := epicOf(w.t, w.s).OriginRead; read != want {
		w.t.Errorf("origin read = %+v, want %+v", read, want)
	}
}

// syncTells returns the tells of the test epic that name a sync run.
func (w *integrateWorld) syncTells() []string {
	w.t.Helper()
	return slices.DeleteFunc(nodeTells(w.t, w.path), func(tell string) bool {
		return !strings.Contains(tell, "plan/M")
	})
}

// syncMergesOriginMainAtAQuietPoint: once the first batch passed, with origin's main moved, the pass merges origin's
// commit into the stored base as a two-parent merge, pins it and walks the Done gate and the capture at it while the
// planner waits. Once both pass, the branch and the stored base move to the merge, the epic records origin's commit as
// synced, nothing is told, and the planner gets its turn. The next turn's tasks build on the merge, so its batch composes
// on it.
func syncMergesOriginMainAtAQuietPoint(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncGate()
		pending := w.pending()
		merge := sync.Revision
		if run := runAt(t, w.s, integrateBatch); run.Outcome != outcomePassed {
			t.Fatalf("batch = %+v, want passed before the sync", run)
		}
		if sync.State != store.NodeRunning || sync.Kind != "sync" || sync.Node != "sync" || merge == "" {
			t.Fatalf("sync = %+v, want running at a merge", sync)
		}
		parents := strings.Fields(w.git("rev-list", "--parents", "-n", "1", merge))
		if want := []string{merge, candidate, originHead}; !reflect.DeepEqual(parents, want) {
			t.Errorf("merge parents = %v, want the stored base then origin's commit %v", parents, want)
		}
		if pinned := w.git("rev-parse", syncRefRoot+originHead); pinned != merge {
			t.Errorf("pin of origin's commit = %s, want the merge %s", pinned, merge)
		}
		if fetched := w.git("rev-parse", syncRefRoot+"origin"); fetched != originHead {
			t.Errorf("sync's origin ref = %s, want origin's commit %s", fetched, originHead)
		}
		w.requireUnmoved(candidate)
		gate := w.fakes.checks.calls[len(w.fakes.checks.calls)-1]
		if gate.ID != workID(t, w.s, syncFirst, "done 1") || gate.Revision != merge || gate.Path != syncFirst {
			t.Errorf("last check = %+v, want the sync's Done gate at the merge", gate)
		}

		w.endGate(syncFirst, 0, "")
		if w.passed() {
			t.Error("the planner got a turn while the sync captures")
		}
		capture := w.fakes.evidence.calls[len(w.fakes.evidence.calls)-1]
		wantCapture := evidenceCall{
			id: workID(t, w.s, syncFirst, "capture R1"), epic: runEpic, item: "R1", revision: merge,
			phase: evidence.PhaseGreen,
		}
		if !reflect.DeepEqual(capture, wantCapture) {
			t.Errorf("last capture = %+v, want %+v", capture, wantCapture)
		}
		w.endCapture(syncFirst)
		if !w.passed() {
			t.Error("the planner got no turn once the sync ended")
		}

		output := fmt.Sprintf(
			"merged origin's commit %s into the stored base %s as %s\nR1 green",
			originHead,
			candidate,
			merge,
		)
		w.requireSyncEnded(syncFirst, outcomePassed, output)
		epic := epicOf(t, w.s)
		if epic.StoredBase != merge || epic.Synced != originHead || epic.Candidate != "" {
			t.Errorf("stored base %q, synced %q, candidate %q, want %q, %q and none", epic.StoredBase, epic.Synced,
				epic.Candidate, merge, originHead)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != merge {
			t.Errorf("branch = %s, want the merge %s", tip, merge)
		}
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none for a sync that passed", tells)
		}
		if got := w.pending(); got != pending || pending != 1 {
			t.Errorf("pending triggers = %d, then %d, want 1 and 1: only the batch wakes the planner", pending, got)
		}

		w.nextTurn(merge, 3, 4, integrateNextBatch)
		if got := w.git("rev-parse", w.candidate(integrateNextBatch)+"~2"); got != merge {
			t.Errorf("the next batch builds on %s, want the merge %s", got, merge)
		}
	}
}

// syncConflictTellsOnceAndIsNotTriedAgain: origin's main adds a path a task added too, so the merge conflicts. The
// sync ends failed and tells the paths once, the branch and stored base stay, nothing is pinned, and the planner gets
// its turn in the same pass. At the next quiet point origin's main has not moved, but the stored base has: the commit
// the first sync tried at the old stored base is not tried at the new one, so a sync of it is stored at syncSecond,
// merges into the new stored base, conflicts in b.go there and is told once more. The branch and stored base stay at
// the new stored base.
func syncConflictTellsOnceAndIsNotTriedAgain(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead := w.originCommit("b.go")
		if !w.passed() {
			t.Error("the planner got no turn after the sync conflicted")
		}

		output := fmt.Sprintf(
			"merge of origin's commit %s into the stored base %s conflicts in b.go",
			originHead,
			candidate,
		)
		run := w.requireSyncEnded(syncFirst, outcomeFailed, output)
		if run.Revision != "" {
			t.Errorf("sync revision = %q, want none for a merge that conflicts", run.Revision)
		}
		w.requireUnmoved(candidate)
		want := []string{"workflow node plan/M1 failed: " + output}
		if tells := w.syncTells(); !reflect.DeepEqual(tells, want) {
			t.Errorf("tells = %q, want %q", tells, want)
		}
		if refs := w.git("for-each-ref", "--format=%(refname)", syncRefRoot); refs != syncRefRoot+"origin" {
			t.Errorf("sync refs = %q, want only the origin ref", refs)
		}

		w.nextTurn(candidate, 3, 4, integrateNextBatch)
		next := w.candidate(integrateNextBatch)
		if !w.passed() {
			t.Error("the planner got no turn after the second sync conflicted")
		}
		again := fmt.Sprintf(
			"merge of origin's commit %s into the stored base %s conflicts in b.go",
			originHead,
			next,
		)
		w.requireSyncEnded(syncSecond, outcomeFailed, again)
		w.requireUnmoved(next)
		w.requireRead(next, originHead, originTaken)
		both := []string{"workflow node plan/M1 failed: " + output, "workflow node plan/M2 failed: " + again}
		if tells := w.syncTells(); !reflect.DeepEqual(tells, both) {
			t.Errorf("tells after the second quiet point = %q, want the first and the second %q", tells, both)
		}
	}
}

// syncGateFailureTellsAndALaterMainIsTried: a Done gate that fails at the merge ends the sync failed and tells it,
// and the branch does not move. The same origin commit is tried again once the stored base moves: at the next quiet
// point a sync of it is stored at syncSecond, merges into the new stored base and runs its Done gate there, which
// fails too, so that sync ends failed and is told as well. Once origin's main moves, the next quiet point merges the
// new commit and advances to it.
func syncGateFailureTellsAndALaterMainIsTried(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncGate()
		w.endGate(syncFirst, 1, "lint failed")
		if !w.passed() {
			t.Error("the planner got no turn after the sync's gate failed")
		}
		output := fmt.Sprintf(
			"merge %s of origin's commit %s into the stored base %s\nDone gate lint failed",
			sync.Revision,
			originHead,
			candidate,
		)
		w.requireSyncEnded(syncFirst, outcomeFailed, output)
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 failed: " + output}) {
			t.Errorf("tells = %q, want the gate failure", tells)
		}

		checks := len(w.fakes.checks.calls)
		w.nextTurn(candidate, 3, 4, integrateNextBatch)
		second := w.candidate(integrateNextBatch)
		checks++ // The second batch's own Done gate.
		w.pass()
		checks++ // The second sync's Done gate, at the new stored base.
		if !w.hasRunAt(syncSecond) {
			t.Errorf("sync %s not stored for origin's commit at the new stored base", syncSecond)
		}
		if got := len(w.fakes.checks.calls); got != checks {
			t.Errorf("%d checks run, want %d: the sync's Done gate runs at the new stored base", got, checks)
		}
		w.endGate(syncSecond, 1, "lint failed")
		if !w.passed() {
			t.Error("the planner got no turn after the second sync's gate failed")
		}
		retry := runAt(t, w.s, syncSecond)
		again := fmt.Sprintf(
			"merge %s of origin's commit %s into the stored base %s\nDone gate lint failed",
			retry.Revision,
			originHead,
			second,
		)
		w.requireSyncEnded(syncSecond, outcomeFailed, again)
		w.requireUnmoved(second)
		both := []string{"workflow node plan/M1 failed: " + output, "workflow node plan/M2 failed: " + again}
		if tells := w.syncTells(); !reflect.DeepEqual(tells, both) {
			t.Errorf("tells = %q, want the first and the second gate failure %q", tells, both)
		}

		later := w.originCommit("later.txt")
		w.nextTurn(second, 5, 6, thirdBatch)
		third := w.candidate(thirdBatch)
		w.pass()
		merge := runAt(t, w.s, syncThird).Revision
		if parents := w.git("rev-parse", merge+"^1", merge+"^2"); parents != third+"\n"+later {
			t.Errorf("merge parents = %q, want %s and %s", parents, third, later)
		}
		w.endGate(syncThird, 0, "")
		w.pass()
		w.endCapture(syncThird)
		w.pass()
		w.requireSyncEnded(syncThird, outcomePassed, fmt.Sprintf(
			"merged origin's commit %s into the stored base %s as %s\nR1 green", later, third, merge))
		if epic := epicOf(t, w.s); epic.StoredBase != merge || epic.Synced != later {
			t.Errorf("stored base %q and synced %q, want %q and %q", epic.StoredBase, epic.Synced, merge, later)
		}
	}
}

// syncCaptureRegressionTellsAndDoesNotAdvance: R1 is green at the stored base, and its capture at the merge fails,
// so the sync ends failed with the regression told, and nothing moves.
func syncCaptureRegressionTellsAndDoesNotAdvance(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncGate()
		w.endGate(syncFirst, 0, "")
		w.pass()
		w.endCaptureFailed(workID(t, w.s, syncFirst, "capture R1"))
		w.pass()
		output := fmt.Sprintf(
			"merge %s of origin's commit %s into the stored base %s\n"+
				"R1 regressed from green at the stored base %s: its capture exited 0 and was judged fails: "+
				"the runner stopped",
			sync.Revision, originHead, candidate, candidate,
		)
		w.requireSyncEnded(syncFirst, outcomeFailed, output)
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 failed: " + output}) {
			t.Errorf("tells = %q, want the regression", tells)
		}
	}
}

// syncStoresNoRunWhenTheStoredBaseHoldsOriginMain: origin's main has not moved since the epic's base, so the stored
// base holds it. The quiet point reads origin's main, records that the stored base holds it, starts no sync and stores
// no run, runs no gate, tells nothing and moves nothing, and the planner gets its turn in that pass.
func syncStoresNoRunWhenTheStoredBaseHoldsOriginMain(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		checks := len(w.fakes.checks.calls)
		if !w.passed() {
			t.Error("the planner got no turn when origin's main had nothing to merge")
		}
		if w.hasRunAt(syncFirst) {
			t.Errorf("sync %s stored with nothing to merge", syncFirst)
		}
		w.requireRead(candidate, w.base, originHeld)
		w.requireUnmoved(candidate)
		if got := len(w.fakes.checks.calls); got != checks {
			t.Errorf("%d checks run, want %d: nothing to merge runs no gate", got, checks)
		}
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}

// syncWithNoOriginStoresNoRun: a repository with no remote origin records that it has none, starts no sync, logs
// nothing, tells nothing, parks nothing, and the planner gets its turn.
func syncWithNoOriginStoresNoRun(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.integrateWorld(t, atCaptured)
		if !w.passed() {
			t.Error("the planner got no turn in a repository with no origin")
		}
		if w.hasRunAt(syncFirst) {
			t.Errorf("sync %s stored in a repository with no origin", syncFirst)
		}
		w.requireRead(candidate, "", originNone)
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}

// syncReadThatFailsStoresNoRunAndLogs: an origin that cannot be read records a failed read and a logged warning,
// and starts no sync. Nothing is told or parked, and the planner gets its turn.
func syncReadThatFailsStoresNoRunAndLogs(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		w.git("remote", "set-url", "origin", t.TempDir()+"/gone")
		logs := &bytes.Buffer{}
		w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
		if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
			t.Error("the planner got no turn after the read of origin's main failed")
		}
		if w.hasRunAt(syncFirst) {
			t.Errorf("sync %s stored when origin's main could not be read", syncFirst)
		}
		w.requireRead(candidate, "", originFailed)
		if !strings.Contains(logs.String(), "origin's main was not read") ||
			!strings.Contains(logs.String(), "git ls-remote --symref origin HEAD") {
			t.Errorf("logs = %q, want the failed read logged", logs.String())
		}
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}

// TestFetchOriginNeverPrompts: git that reads origin runs with GIT_TERMINAL_PROMPT=0 whatever the daemon's environment
// says, so an origin that asks for credentials fails the read rather than waiting on a prompt no one answers. The
// upload-pack origin runs for the ls-remote and then the fetch records the variable each time git passes it on.
func TestFetchOriginNeverPrompts(t *testing.T) {
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	repo := mergeRepo(t)
	mergeRemote(t, repo)
	seen := filepath.Join(t.TempDir(), "prompt")
	script := filepath.Join(t.TempDir(), "upload-pack")
	body := fmt.Sprintf(
		"#!/bin/sh\nprintf '%%s' \"${GIT_TERMINAL_PROMPT-unset}\" >> '%s'\nexec git upload-pack \"$@\"\n", seen,
	)
	exectest.WriteFile(t, script, []byte(body), 0o700)
	nodeGit(t, repo, "config", "remote.origin.uploadpack", script)
	epic := store.EpicRow{ID: runEpic, Repository: repo}

	branch, commit, err := fetchOrigin(context.Background(), epic, syncRef(epic, "origin"))
	if err != nil {
		t.Fatalf("fetchOrigin: %v", err)
	}
	if main := nodeGit(t, repo, "rev-parse", "main"); branch != "refs/heads/main" || commit != main {
		t.Errorf("fetchOrigin = %s at %s, want refs/heads/main at %s", branch, commit, main)
	}
	got, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("read what upload-pack saw: %v", err)
	}
	if string(got) != "00" {
		t.Errorf("GIT_TERMINAL_PROMPT upload-pack saw = %q, want 0 for the ls-remote and 0 for the fetch", got)
	}
}

// TestSyncSkipsOriginMainWithoutTheChangeBase: the epic's base is a commit on main that origin lacks, and origin's main
// moves on from the commit the base was made on. The change from origin's commit would hold the base's own commit
// beside the epic's, so the sync ends skipped in the pass that started it: it merges and pins nothing, runs no gate,
// tells nothing and moves nothing, and the planner gets its turn. Its fetch brought origin's commit, so the next quiet
// point's read finds that commit lacks the change base and starts no sync.
func TestSyncSkipsOriginMainWithoutTheChangeBase(t *testing.T) {
	t.Parallel()
	w := newSyncWorldAhead(t)
	candidate := w.toCaptured()
	originHead := w.originCommit("origin.txt")
	checks := len(w.fakes.checks.calls)
	if !w.passed() {
		t.Error("the planner got no turn after a sync that merged nothing")
	}
	output := fmt.Sprintf("the epic's change base %s is not in origin's commit %s", w.base, originHead)
	if run := w.requireSyncEnded(syncFirst, "skipped", output); run.Revision != "" {
		t.Errorf("sync revision = %q, want none", run.Revision)
	}
	w.requireUnmoved(candidate)
	if got := len(w.fakes.checks.calls); got != checks {
		t.Errorf("%d checks run, want %d: a sync that merged nothing runs no gate", got, checks)
	}
	if refs := w.git("for-each-ref", "--format=%(refname)", syncRefRoot); refs != syncRefRoot+"origin" {
		t.Errorf("sync refs = %q, want only the origin ref", refs)
	}
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}

	w.nextTurn(candidate, 3, 4, integrateNextBatch)
	next := w.candidate(integrateNextBatch)
	if !w.passed() {
		t.Error("the planner got no turn at the second quiet point")
	}
	if w.hasRunAt(syncSecond) {
		t.Errorf("sync %s stored for origin's commit that lacks the change base", syncSecond)
	}
	w.requireRead(next, originHead, originApart)
	w.requireUnmoved(next)
}

// syncSkipsOriginMainForcedPastTheSyncedCommit: once a sync passed, origin's main is forced to a commit on the epic's
// base that lacks the origin commit the epic synced. The change from the forced commit would hold the synced commit's
// change beside the epic's, so the next quiet point's sync ends skipped, and the stored base and synced commit stay.
func syncSkipsOriginMainForcedPastTheSyncedCommit(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, _ := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncAdvance()
		w.pass()
		if epic := epicOf(t, w.s); epic.Synced != originHead {
			t.Fatalf("synced = %q, want %q after the first sync", epic.Synced, originHead)
		}
		forced := w.forceOrigin("forced.txt")

		w.nextTurn(sync.Revision, 3, 4, integrateNextBatch)
		next := w.candidate(integrateNextBatch)
		if !w.passed() {
			t.Error("the planner got no turn after the second sync")
		}
		w.requireSyncEnded(syncSecond, "skipped",
			fmt.Sprintf("the epic's change base %s is not in origin's commit %s", originHead, forced))
		if epic := epicOf(t, w.s); epic.StoredBase != next || epic.Synced != originHead {
			t.Errorf("stored base %q and synced %q, want %q and %q", epic.StoredBase, epic.Synced, next, originHead)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != next {
			t.Errorf("branch = %s, want it left at %s", tip, next)
		}
	}
}

// syncRestartFinishesTheWalk: a restart that loses the sync's Done gate check reruns it under a runner that starts
// afresh, which finds the running sync and walks it on. A restart that cut the advance after it moved the branch ends
// the sync passed once, with the stored base following the branch.
func syncRestartFinishesTheWalk(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncGate()
		lost := store.NodeCheckEnd{NotRun: store.LostAtRestart}
		if err := w.s.EndNodeCheck(
			context.Background(),
			workID(t, w.s, syncFirst, "done 1"),
			lost,
			runClock(),
		); err != nil {
			t.Fatalf("EndNodeCheck: %v", err)
		}
		w.fakes = newNodeFakes(w.s)
		w.runner = runNodes(w.s, w.fakes)
		w.runner.logger = slog.New(slog.NewTextHandler(w.logs, nil))
		if w.passed() {
			t.Error("the planner got a turn while the sync reruns its gate")
		}
		rerun := workID(t, w.s, syncFirst, "done 1 rerun")
		if len(w.fakes.checks.calls) != 1 || w.fakes.checks.calls[0].ID != rerun {
			t.Fatalf("checks after the restart = %+v, want the gate's rerun %s", w.fakes.checks.calls, rerun)
		}
		nodeEndCheck(t, w.s, rerun, 0, "")
		w.pass()
		w.endCapture(syncFirst)
		w.git("update-ref", integrateBranch, sync.Revision, candidate)
		if !w.passed() {
			t.Error("the planner got no turn once the cut sync ended")
		}
		w.requireSyncEnded(syncFirst, outcomePassed, fmt.Sprintf(
			"merged origin's commit %s into the stored base %s as %s\nR1 green", originHead, candidate, sync.Revision))
		if epic := epicOf(t, w.s); epic.StoredBase != sync.Revision || epic.Synced != originHead {
			t.Errorf("stored base %q and synced %q, want %q and %q", epic.StoredBase, epic.Synced, sync.Revision,
				originHead)
		}
	}
}

// syncAtTheMergeEndsPassedWhileTheEpicIsBusy: a restart cut the sync's advance after it moved the branch to the
// merge and before the end was written, and by the next pass a planner turn runs. The sync ends passed, its stored base
// following the branch, rather than skipped with the branch ahead of the stored base, where the next batch's advance
// would park the epic. Nothing is told.
func syncAtTheMergeEndsPassedWhileTheEpicIsBusy(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncAdvance()
		w.git("update-ref", integrateBranch, sync.Revision, candidate)
		w.claimTurn()
		w.pass()
		w.requireSyncEnded(syncFirst, outcomePassed, fmt.Sprintf(
			"merged origin's commit %s into the stored base %s as %s\nR1 green", originHead, candidate, sync.Revision))
		epic := epicOf(t, w.s)
		if epic.StoredBase != sync.Revision || epic.Synced != originHead || epic.Parked != "" {
			t.Errorf("stored base %q, synced %q, parked %q, want %q, %q and not parked", epic.StoredBase, epic.Synced,
				epic.Parked, sync.Revision, originHead)
		}
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}

// syncDoesNotAdvanceABusyEpic: a planner turn that started while the sync's checks ran keeps the sync from moving
// the branch, which is still at the stored base: the sync ends skipped with why, tells nothing, and the branch and stored
// base stay.
func syncDoesNotAdvanceABusyEpic(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncAdvance()
		turn := w.claimTurn()
		w.pass()
		w.requireSyncEnded(syncFirst, "skipped", fmt.Sprintf(
			"merge %s of origin's commit %s into the stored base %s\nnot advanced: planner turn %d is running",
			sync.Revision, originHead, candidate, turn))
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}

// syncDoesNotAdvanceAMovedBranch: a branch another commit moved while the sync ran is not moved. The sync ends
// skipped and tells why, the epic is not parked, and origin's commit is not counted as tried.
func syncDoesNotAdvanceAMovedBranch(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead, sync := w.syncGate()
		w.endGate(syncFirst, 0, "")
		w.pass()
		w.endCapture(syncFirst)
		moved := w.commit(candidate, "moved.go", "moved\n")
		w.git("update-ref", integrateBranch, moved)
		w.pass()
		output := fmt.Sprintf(
			"merge %s of origin's commit %s into the stored base %s\nnot advanced: epic/epic-runner moved: at %s, not "+
				"the stored base %s", sync.Revision, originHead, candidate, moved, candidate,
		)
		w.requireSyncEnded(syncFirst, "skipped", output)
		if epic := epicOf(t, w.s); epic.StoredBase != candidate || epic.Parked != "" || epic.Synced != "" {
			t.Errorf("epic = stored base %q, parked %q, synced %q, want %q, not parked, none", epic.StoredBase,
				epic.Parked, epic.Synced, candidate)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != moved {
			t.Errorf("branch = %s, want it left at %s", tip, moved)
		}
		if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 skipped: " + output}) {
			t.Errorf("tells = %q, want the refused advance", tells)
		}
	}
}

// TestSyncOnTheBuiltInEpicHoldsThePlannerAndEndsBuilt: an epic pinned to the built-in epic, run through Runner.Pass,
// syncs origin's main after its batch passes. The planner is not passed for the epic while the sync runs, and is once it
// ended. The plan then ends built at the merge, since the sync's capture is the Requirement's green at the stored base.
func TestSyncOnTheBuiltInEpicHoldsThePlannerAndEndsBuilt(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t, pinnedTo(builtinPin(t)), viaRunnerPass())
	w.git("config", "user.name", "Test")
	w.git("config", "user.email", "test@example.invalid")
	origin := mergeRemote(t, w.repo)
	planner := &fakePlanner{known: []string{runEpic}}
	w.runner.planner = planner
	s := w.s

	w.pass()
	w.endRed()
	w.pass()
	endCost(t, s)
	w.pass()
	completeTurn(t, s, store.EpicPlan{
		Decisions: []string{}, Tasks: []store.EpicTaskInput{runTask("only", "b.go")}, Outline: "one task",
		Outcome: "complete",
	})
	w.pass()
	w.accept(1, w.base, w.commit(w.base, "b.go", "one\n"))
	w.pass()
	const batch = "plan/S1/integrate"
	w.endGate(batch, 0, "")
	w.pass()
	w.endCapture(batch)
	originHead := mergeOriginCommit(t, origin, "origin.txt")

	w.pass()
	merge := runAt(t, s, syncFirst).Revision
	if named := planner.named[len(planner.named)-1]; len(named) != 0 {
		t.Errorf("planner passed for %v while the sync runs, want none", named)
	}
	w.endGate(syncFirst, 0, "")
	w.pass()
	w.endCapture(syncFirst)
	w.pass()
	if named := planner.named[len(planner.named)-1]; !reflect.DeepEqual(named, []string{runEpic}) {
		t.Errorf("planner passed for %v once the sync ended, want the epic", named)
	}
	if epic := epicOf(t, s); epic.StoredBase != merge || epic.Synced != originHead {
		t.Fatalf("stored base %q and synced %q, want the merge %q and %q", epic.StoredBase, epic.Synced, merge,
			originHead)
	}

	completeTurn(t, s, store.EpicPlan{Decisions: []string{}, Outline: "the batch is green", Outcome: "complete"})
	w.pass()
	if plan := runAt(t, s, "plan"); plan.State != store.NodeEnded || plan.Outcome != "complete" {
		t.Errorf("plan = %+v, want ended complete", plan)
	}
	if epic := epicOf(t, s); epic.Phase != "built" || epic.Parked != "" {
		t.Errorf("epic phase %q, parked %q, want built and not parked", epic.Phase, epic.Parked)
	}
	if w.hasRunAt(syncSecond) {
		t.Errorf("sync %s stored at the plan's end, when the stored base holds origin's main", syncSecond)
	}
	w.requireRead(merge, originHead, originHeld)
}

// TestOwnedReadsTheChangeFromTheSyncedCommit: an owned node of an epic whose stored base merged origin's main in a sync
// lists the paths its branch changes over origin's commit it synced, so origin's own change is not taken for the
// epic's, and the node passes.
func TestOwnedReadsTheChangeFromTheSyncedCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo, base := nodeRepo(t)
	nodeWrite(t, repo, "origin.go", "origin\n")
	nodeGit(t, repo, "add", "origin.go")
	nodeGit(t, repo, "commit", "-q", "-m", "origin's change")
	originHead := nodeGit(t, repo, "rev-parse", "HEAD")
	head := nodeEpicBranch(t, repo, "b.go")
	s := openRunStore(t)
	createNodeEpic(t, s, repo, base, nodeOwnedFlow)
	sync := store.NodeRun{Path: syncFirst, ID: "sync-run", Node: "sync", Kind: "sync"}
	if err := s.StartNodes(ctx, runEpic, []store.NodeRun{sync}, nil, runClock()); err != nil {
		t.Fatalf("StartNodes: %v", err)
	}
	if err := s.PrepareSync(ctx, runEpic, syncFirst, 0, head, `{"base":"`+base+`"}`, store.OriginRead{}); err != nil {
		t.Fatalf("PrepareSync: %v", err)
	}
	end := store.SyncEnd{Path: syncFirst, Outcome: outcomePassed, Synced: originHead}
	if err := s.EndSync(ctx, runEpic, end, runClock()); err != nil {
		t.Fatalf("EndSync: %v", err)
	}
	runNodes(s, newNodeFakes(s)).Pass(ctx)

	run := runAt(t, s, "owned")
	if run.Result != `["b.go"]` || run.State != store.NodeEnded || run.Outcome != outcomePassed {
		t.Errorf("owned = %+v, want ended passed with only b.go changed", run)
	}
}

// failingSyncStore is the store a test wraps so its sync writes fail while failing is set.
type failingSyncStore struct {
	Store
	failing bool
}

// errSyncWrite is the error a failing sync write returns.
var errSyncWrite = errors.New("disk full")

// PrepareSync fails while failing is set, and writes otherwise.
func (f *failingSyncStore) PrepareSync(
	ctx context.Context, epic, path string, attempt int64, revision, result string, read store.OriginRead,
) error {
	if f.failing {
		return errSyncWrite
	}
	return f.Store.PrepareSync(ctx, epic, path, attempt, revision, result, read)
}

// EndSync fails while failing is set, and writes otherwise.
func (f *failingSyncStore) EndSync(ctx context.Context, epic string, end store.SyncEnd, at time.Time) error {
	if f.failing {
		return errSyncWrite
	}
	return f.Store.EndSync(ctx, epic, end, at)
}

// TestSyncWriteThatFailsIsMadeOnTheNextPass: a prepare or an end of a sync whose write fails is logged and leaves the
// sync running with the planner held, and the next pass makes it again from the rows as they are.
func TestSyncWriteThatFailsIsMadeOnTheNextPass(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	failing := &failingSyncStore{Store: w.s, failing: true}
	w.runner = runNodes(failing, w.fakes)
	logs := &bytes.Buffer{}
	w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	candidate := w.toCaptured()
	originHead := w.originCommit("origin.txt")
	if w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
		t.Error("the planner got a turn while the sync's prepare failed")
	}
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeRunning || run.Result != "" {
		t.Errorf("sync = %+v, want running and unprepared", run)
	}
	if !strings.Contains(logs.String(), errSyncWrite.Error()) {
		t.Errorf("logs = %q, want the failed prepare", logs.String())
	}

	failing.failing = false
	w.pass()
	merge := runAt(t, w.s, syncFirst).Revision
	w.endGate(syncFirst, 0, "")
	w.pass()
	w.endCapture(syncFirst)
	failing.failing = true
	logs.Reset()
	if w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
		t.Error("the planner got a turn while the sync's end failed")
	}
	if !strings.Contains(logs.String(), errSyncWrite.Error()) {
		t.Errorf("logs = %q, want the failed end", logs.String())
	}
	if run := runAt(t, w.s, syncFirst); run.State != store.NodeRunning {
		t.Errorf("sync = %+v, want running after its end failed", run)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != merge {
		t.Errorf("branch = %s, want the merge %s the advance moved it to", tip, merge)
	}

	failing.failing = false
	if !w.passed() {
		t.Error("the planner got no turn once the sync ended")
	}
	w.requireSyncEnded(syncFirst, outcomePassed, fmt.Sprintf(
		"merged origin's commit %s into the stored base %s as %s\nR1 green", originHead, candidate, merge))
}

// requireNoPark fails unless the test epic is not parked, holds no park of either class, has no open question and has
// stored no planner park, so nothing of a sync went to the owner or the planner as a park.
func (w *integrateWorld) requireNoPark() {
	w.t.Helper()
	epic := epicOf(w.t, w.s)
	if epic.Parked != "" || epic.ParkClass != "" || len(epic.Questions) != 0 {
		w.t.Errorf("epic parked %q as %q with questions %+v, want no park", epic.Parked, epic.ParkClass,
			epic.Questions)
	}
	db, err := sql.Open("sqlite3", w.path)
	if err != nil {
		w.t.Fatalf("open store file: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			w.t.Errorf("close store file: %v", err)
		}
	}()
	var parks int
	if err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM epic_parks WHERE epic = ?",
		runEpic).Scan(&parks); err != nil {
		w.t.Fatalf("count planner parks: %v", err)
	}
	if parks != 0 {
		w.t.Errorf("planner parks stored = %d, want none", parks)
	}
}

// hasRunAt reports whether the test epic has a node run at path.
func (w *integrateWorld) hasRunAt(path string) bool {
	w.t.Helper()
	return slices.ContainsFunc(runsOf(w.t, w.s), func(run store.NodeRun) bool { return run.Path == path })
}

// actionTurn completes the next planner turn of the test epic with no task and action as its one action, as a turn the
// planner returns an action from does, and returns the action as the store keeps it pending.
func (w *integrateWorld) actionTurn(action store.EpicActionInput) store.PlannerAction {
	w.t.Helper()
	completeTurn(w.t, w.s, store.EpicPlan{
		Decisions: []string{}, Outline: "act on the epic", Outcome: "work", Actions: []store.EpicActionInput{action},
	})
	pending, err := w.s.PendingActions(context.Background())
	if err != nil {
		w.t.Fatalf("PendingActions: %v", err)
	}
	if len(pending) != 1 {
		w.t.Fatalf("pending actions = %+v, want the turn's one action", pending)
	}
	return pending[0]
}

// carryOut carries out action as the planner's pass does at the end of the interpreter's pass.
func (w *integrateWorld) carryOut(action store.PlannerAction) {
	w.t.Helper()
	if _, err := w.s.CarryOutAction(context.Background(), action, runClock()); err != nil {
		w.t.Fatalf("CarryOutAction: %v", err)
	}
}

// failedSyncRoutesNoParkAndNoSyncRunsWhileAnActionIsPending: a sync whose merge conflicts ends failed with its one
// tell, and parks nothing: no park of either class, no question and no planner park. Its trigger wakes the planner,
// as the batch does. The planner's next turn returns a retry_node of the node sync. While that action
// is pending, the quiet point that turn opens starts no sync, and the plan keeps its run. The action is refused, since
// a sync run never parks and is no integrate run, and the failed sync is kept as it ended. Once the action is carried
// out the quiet point starts no sync either, since origin's commit is the one the first sync tried.
func failedSyncRoutesNoParkAndNoSyncRunsWhileAnActionIsPending(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		pending := w.pending()
		originHead := w.originCommit("b.go")
		if !w.passed() {
			t.Error("the planner got no turn after the sync failed")
		}
		output := fmt.Sprintf("merge of origin's commit %s into the stored base %s conflicts in b.go", originHead,
			candidate)
		failed := w.requireSyncEnded(syncFirst, outcomeFailed, output)
		w.requireNoPark()
		if got := w.pending(); got != pending+2 {
			t.Errorf("pending triggers = %d, want %d: the batch and the failed sync each wake the planner",
				got, pending+2)
		}
		for _, cause := range triggerCausesOf(t, w.s, runEpic) {
			if strings.Contains(cause, syncFirst) {
				t.Errorf("trigger cause %q names the sync, want no trigger of it", cause)
			}
		}
		tells := []string{"workflow node plan/M1 failed: " + output}
		if got := w.syncTells(); !reflect.DeepEqual(got, tells) {
			t.Errorf("tells = %q, want %q", got, tells)
		}

		action := w.actionTurn(store.EpicActionInput{
			Kind: store.ActionRetryNode, Node: "sync", Reason: "the merge of origin's main conflicted",
		})
		if !w.passed() {
			t.Error("the plan got no run while a planner action is pending, want no sync to hold it")
		}
		if w.hasRunAt(syncSecond) {
			t.Errorf("sync %s started while a planner action is pending", syncSecond)
		}

		w.carryOut(action)
		if got := runAt(t, w.s, syncFirst); !reflect.DeepEqual(got, failed) {
			t.Errorf("sync after the refused retry = %+v, want it kept as %+v", got, failed)
		}
		w.requireNoPark()
		decisions := epicOf(t, w.s).Decisions
		refused := fmt.Sprintf("planner retry node sync refused: node sync of epic %s is not parked. Its reason: %s",
			runEpic, action.Reason)
		if !slices.ContainsFunc(decisions, func(d store.EpicDecision) bool { return d.Text == refused }) {
			t.Errorf("decisions = %+v, want the refusal %q", decisions, refused)
		}

		if !w.passed() {
			t.Error("the planner got no turn once the action was carried out")
		}
		if w.hasRunAt(syncSecond) {
			t.Errorf("sync %s stored for origin's commit the first sync tried", syncSecond)
		}
		w.requireUnmoved(candidate)
		if got := w.syncTells(); !reflect.DeepEqual(got, tells) {
			t.Errorf("tells after the skipped sync = %q, want only the first %q", got, tells)
		}
	}
}

// syncWaitsForThePlannersRerunOfAFailedBatch: a batch whose Done gate fails leaves its candidate, so no sync starts
// while origin's main has moved. The planner's retry_node of the integrate node reruns the failed batch on that
// candidate, which a sync would have left behind the stored base, and no sync starts while the action is pending. Once
// the rerun passes and advances, the pass syncs from the stored base it moved to, and the planner waits for that sync.
func syncWaitsForThePlannersRerunOfAFailedBatch(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atGate)
		originHead := w.originCommit("origin.txt")
		w.endGate(integrateBatch, 1, "lint failed")
		if !w.passed() {
			t.Error("the planner got no turn after the batch failed")
		}
		if epic := epicOf(t, w.s); epic.Candidate != candidate || w.hasRunAt(syncFirst) {
			t.Fatalf("candidate %q and sync stored %v after the failed batch, want %q and no sync", epic.Candidate,
				w.hasRunAt(syncFirst), candidate)
		}

		action := w.actionTurn(store.EpicActionInput{
			Kind: store.ActionRetryNode, Node: "integrate", Reason: "the lint gate is flaky",
		})
		if !w.passed() || w.hasRunAt(syncSecond) {
			t.Fatalf("plan run given %v, sync stored %v, want the plan's run and no sync while the action is pending",
				true, w.hasRunAt(syncSecond))
		}
		w.carryOut(action)
		if batch := runAt(t, w.s, integrateBatch); batch.State != store.NodeRunning || batch.Revision != candidate {
			t.Fatalf("batch = %+v after the retry, want running again at the candidate %s", batch, candidate)
		}
		w.pass()
		w.endGate(integrateBatch, 0, "")
		w.pass()
		w.endCapture(integrateBatch)
		if w.passed() {
			t.Error("the planner got a turn while the sync after the rerun runs")
		}

		if batch := runAt(t, w.s, integrateBatch); batch.Outcome != outcomePassed {
			t.Errorf("batch = %+v, want the rerun passed", batch)
		}
		sync := runAt(t, w.s, syncSecond)
		if sync.State != store.NodeRunning || sync.Revision == "" {
			t.Fatalf("sync = %+v, want it running at its merge after the rerun passed", sync)
		}
		parents := strings.Fields(w.git("rev-list", "--parents", "-n", "1", sync.Revision))
		if want := []string{sync.Revision, candidate, originHead}; !reflect.DeepEqual(parents, want) {
			t.Errorf("merge parents = %v, want the rerun's candidate then origin's commit %v", parents, want)
		}
		w.requireUnmoved(candidate)
		w.endGate(syncSecond, 0, "")
		w.pass()
		w.endCapture(syncSecond)
		if !w.passed() {
			t.Error("the planner got no turn once the sync ended")
		}
		if epic := epicOf(t, w.s); epic.StoredBase != sync.Revision || epic.Synced != originHead {
			t.Errorf(
				"stored base %q and synced %q, want the merge %q and %q",
				epic.StoredBase,
				epic.Synced,
				sync.Revision,
				originHead,
			)
		}
	}
}

// closingSyncStore is the store a test wraps so that the owner's close of the epic lands while a sync's prepare runs:
// its PrepareSync closes the epic before it writes.
type closingSyncStore struct {
	Store
	closer *store.Store
}

// PrepareSync closes the epic as abandoned by its owner, then writes the prepare.
func (c *closingSyncStore) PrepareSync(
	ctx context.Context, epic, path string, attempt int64, revision, result string, read store.OriginRead,
) error {
	if _, err := c.closer.CloseEpic(
		ctx,
		epic,
		"abandoned",
		"",
		store.CloserOwner,
		store.Maker{By: store.MakerOwner},
		runClock(),
	); err != nil {
		return fmt.Errorf("close epic %s: %w", epic, err)
	}
	return c.Store.PrepareSync(ctx, epic, path, attempt, revision, result, read)
}

// syncCloseWhileItPreparesEndsItAndStartsNothingMore: the owner closes the epic while its sync prepares. The close
// ends the sync run with the plan run, and the prepare written after it stores nothing. The pass then carries out
// nothing more on the closed epic: the gate after the plan, which the walk would read as next once the plan run ended,
// does not start, the branch and stored base stay, nothing is told, and the planner is not passed for the epic.
func syncCloseWhileItPreparesEndsItAndStartsNothingMore(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		w.originCommit("origin.txt")
		w.runner = runNodes(&closingSyncStore{Store: w.s, closer: w.s}, w.fakes)
		w.runner.logger = slog.New(slog.NewTextHandler(w.logs, nil))
		flow := integrateFlow()
		flow.Nodes = append(flow.Nodes, workflow.Node{
			ID: "after", Kind: kindGate, Settings: &workflow.Settings{Gate: integrateDoneGate, At: "head"},
		})
		if w.runner.passFlow(context.Background(), epicOf(t, w.s), flow) {
			t.Error("the planner was passed for a closed epic")
		}
		if w.logs.Len() > 0 {
			t.Errorf("pass logged: %s", w.logs.String())
		}

		sync := runAt(t, w.s, syncFirst)
		if sync.State != store.NodeEnded || sync.Outcome != "" || sync.Result != "" || sync.Revision != "" {
			t.Errorf("sync = %+v, want ended by the close with nothing prepared", sync)
		}
		if w.hasRunAt("after") {
			t.Error("the node after the plan started on the closed epic")
		}
		epic := epicOf(t, w.s)
		if epic.Phase != store.PhaseClosedAbandoned || epic.StoredBase != candidate || epic.Synced != "" {
			t.Errorf("epic phase %q, stored base %q, synced %q, want closed at %q with none synced", epic.Phase,
				epic.StoredBase, epic.Synced, candidate)
		}
		if tip := w.git("rev-parse", integrateBranch); tip != candidate {
			t.Errorf("branch = %s, want it left at %s", tip, candidate)
		}
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}
	}
}
