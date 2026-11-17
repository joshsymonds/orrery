package interpret

import (
	"bytes"
	"context"
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

// uploadPack writes a shell script whose body is body under a temporary directory and makes it the program the test
// repository's git runs for origin's side of a fetch or ls-remote, which a local origin runs as a child of git.
func (w *integrateWorld) uploadPack(body string) {
	w.t.Helper()
	script := filepath.Join(w.t.TempDir(), "upload-pack")
	exectest.WriteFile(w.t, script, []byte("#!/bin/sh\n"+body), 0o700)
	w.git("config", "remote.origin.uploadpack", script)
}

// toSyncGate runs the first batch to its end with origin's main moved by a commit of origin.txt, which the pass that
// ends the batch merges into the stored base and starts the sync's Done gate at. It returns the batch's candidate,
// origin's commit and the sync run. The planner gets no turn in that pass.
func (w *syncWorld) toSyncGate() (string, string, store.NodeRun) {
	w.t.Helper()
	candidate := w.toCaptured()
	originHead := w.originCommit("origin.txt")
	if w.passed() {
		w.t.Error("the pass that started the sync gave the planner a turn")
	}
	return candidate, originHead, runAt(w.t, w.s, syncFirst)
}

// toSyncAdvance runs the sync of toSyncGate through its Done gate and its capture, both passing, and returns what
// toSyncGate returns. The pass after it advances the sync.
func (w *syncWorld) toSyncAdvance() (string, string, store.NodeRun) {
	w.t.Helper()
	candidate, originHead, sync := w.toSyncGate()
	w.endGate(syncFirst, 0, "")
	w.pass()
	w.endCapture(syncFirst)
	return candidate, originHead, sync
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

// syncTells returns the tells of the test epic that name a sync run.
func (w *integrateWorld) syncTells() []string {
	w.t.Helper()
	return slices.DeleteFunc(nodeTells(w.t, w.path), func(tell string) bool {
		return !strings.Contains(tell, "plan/M")
	})
}

// TestSyncMergesOriginMainAtAQuietPoint: once the first batch passed, with origin's main moved, the pass merges origin's
// commit into the stored base as a two-parent merge, pins it and walks the Done gate and the capture at it while the
// planner waits. Once both pass, the branch and the stored base move to the merge, the epic records origin's commit as
// synced, nothing is told, and the planner gets its turn. The next turn's tasks build on the merge, so its batch composes
// on it.
func TestSyncMergesOriginMainAtAQuietPoint(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
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

	output := fmt.Sprintf("merged origin's commit %s into the stored base %s as %s\nR1 green", originHead, candidate,
		merge)
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

// TestSyncConflictTellsOnceAndIsNotTriedAgain: origin's main adds a path a task added too, so the merge conflicts. The
// sync ends failed and tells the paths once, the branch and stored base stay, nothing is pinned, and the planner gets
// its turn in the same pass. At the next quiet point origin's main has not moved, so the sync is skipped as tried, and
// nothing more is told.
func TestSyncConflictTellsOnceAndIsNotTriedAgain(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate := w.toCaptured()
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
		t.Error("the planner got no turn after a sync skipped as tried")
	}
	w.requireSyncEnded(syncSecond, "skipped", fmt.Sprintf("origin's commit %s was tried by %s", originHead, syncFirst))
	w.requireUnmoved(next)
	if tells := w.syncTells(); !reflect.DeepEqual(tells, want) {
		t.Errorf("tells after the second quiet point = %q, want only the first %q", tells, want)
	}
}

// TestSyncGateFailureTellsAndALaterMainIsTried: a Done gate that fails at the merge ends the sync failed and tells it,
// and the branch does not move. The same origin commit is not tried at the next quiet point: no gate runs for it. Once
// origin's main moves, the next quiet point merges the new commit and advances to it.
func TestSyncGateFailureTellsAndALaterMainIsTried(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
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
	w.requireSyncEnded(syncSecond, "skipped", fmt.Sprintf("origin's commit %s was tried by %s", originHead, syncFirst))
	if got := len(w.fakes.checks.calls); got != checks {
		t.Errorf("%d checks run, want %d: the tried commit runs no gate again", got, checks)
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

// TestSyncCaptureRegressionTellsAndDoesNotAdvance: R1 is green at the stored base, and its capture at the merge fails,
// so the sync ends failed with the regression told, and nothing moves.
func TestSyncCaptureRegressionTellsAndDoesNotAdvance(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
	w.endGate(syncFirst, 0, "")
	w.pass()
	w.endCaptureFailed(workID(t, w.s, syncFirst, "capture R1"))
	w.pass()
	output := fmt.Sprintf(
		"merge %s of origin's commit %s into the stored base %s\nR1 regressed from green at the stored base %s",
		sync.Revision, originHead, candidate, candidate,
	)
	w.requireSyncEnded(syncFirst, outcomeFailed, output)
	w.requireUnmoved(candidate)
	if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 failed: " + output}) {
		t.Errorf("tells = %q, want the regression", tells)
	}
}

// TestSyncSkipsOriginMainTheStoredBaseHolds: origin's main has not moved since the epic's base, so the stored base holds
// it. The sync ends skipped in the pass that started it, runs no gate, tells nothing and moves nothing, and the planner
// gets its turn in that pass.
func TestSyncSkipsOriginMainTheStoredBaseHolds(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate := w.toCaptured()
	checks := len(w.fakes.checks.calls)
	if !w.passed() {
		t.Error("the planner got no turn after a sync with nothing to merge")
	}
	w.requireSyncEnded(syncFirst, "skipped",
		fmt.Sprintf("origin's commit %s is in the stored base %s", w.base, candidate))
	w.requireUnmoved(candidate)
	if got := len(w.fakes.checks.calls); got != checks {
		t.Errorf("%d checks run, want %d: nothing to merge runs no gate", got, checks)
	}
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
}

// TestSyncWithNoOriginSkipsQuietly: a repository with no remote origin skips the sync with a note, logs nothing, tells
// nothing, parks nothing, and the planner gets its turn.
func TestSyncWithNoOriginSkipsQuietly(t *testing.T) {
	t.Parallel()
	w := newIntegrateWorld(t)
	candidate := w.toCaptured()
	if !w.passed() {
		t.Error("the planner got no turn in a repository with no origin")
	}
	w.requireSyncEnded(syncFirst, "skipped", "the repository has no remote origin")
	w.requireUnmoved(candidate)
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
}

// TestSyncFetchFailureSkipsAndLogs: an origin that cannot be fetched skips the sync with the error as its output and a
// logged warning. Nothing is told or parked, and the planner gets its turn.
func TestSyncFetchFailureSkipsAndLogs(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate := w.toCaptured()
	w.git("remote", "set-url", "origin", t.TempDir()+"/gone")
	logs := &bytes.Buffer{}
	w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
		t.Error("the planner got no turn after a fetch failed")
	}
	run := runAt(t, w.s, syncFirst)
	if run.State != store.NodeEnded || run.Outcome != "skipped" ||
		!strings.HasPrefix(run.Output, "the sync did not run: git ls-remote --symref origin HEAD: ") {
		t.Errorf("sync = %+v, want ended skipped on the failed fetch", run)
	}
	if !strings.Contains(logs.String(), "sync of origin's main did not run") {
		t.Errorf("logs = %q, want the failed fetch logged", logs.String())
	}
	w.requireUnmoved(candidate)
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
	}
}

// TestSyncFetchThatStallsIsStoppedAtTheLimit: an origin that never answers holds the sync's prepare only for the
// runner's sync limit. git is stopped then, with the upload-pack it started, and the sync ends skipped with the limit
// named and a logged warning. Nothing is told or moved, and the planner gets its turn.
func TestSyncFetchThatStallsIsStoppedAtTheLimit(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate := w.toCaptured()
	w.originCommit("origin.txt")
	w.uploadPack("exec sleep 60\n")
	w.runner.syncLimit = time.Second
	logs := &bytes.Buffer{}
	w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
	started := time.Now()
	if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
		t.Error("the planner got no turn after the fetch was stopped")
	}
	if took := time.Since(started); took > 30*time.Second {
		t.Errorf("the pass took %s, want it cut at the limit, well before the stalled origin's 60s", took)
	}
	run := runAt(t, w.s, syncFirst)
	if run.State != store.NodeEnded || run.Outcome != "skipped" ||
		!strings.HasPrefix(run.Output, "the sync did not run: git did not finish within 1s: git ") {
		t.Errorf("sync = %+v, want ended skipped on git stopped at the limit", run)
	}
	if !strings.Contains(logs.String(), "sync of origin's main did not run") {
		t.Errorf("logs = %q, want the stopped fetch logged", logs.String())
	}
	w.requireUnmoved(candidate)
	if tells := w.syncTells(); len(tells) != 0 {
		t.Errorf("tells = %q, want none", tells)
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
// tells nothing and moves nothing, and the planner gets its turn. It tried no merge, so the next quiet point reads
// origin's commit again and is not skipped as tried.
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
		t.Error("the planner got no turn after the second sync")
	}
	w.requireSyncEnded(syncSecond, "skipped", output)
	w.requireUnmoved(next)
}

// TestSyncSkipsOriginMainForcedPastTheSyncedCommit: once a sync passed, origin's main is forced to a commit on the epic's
// base that lacks the origin commit the epic synced. The change from the forced commit would hold the synced commit's
// change beside the epic's, so the next quiet point's sync ends skipped, and the stored base and synced commit stay.
func TestSyncSkipsOriginMainForcedPastTheSyncedCommit(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	_, originHead, sync := w.toSyncAdvance()
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

// TestSyncRestartFinishesTheWalk: a restart that loses the sync's Done gate check reruns it under a runner that starts
// afresh, which finds the running sync and walks it on. A restart that cut the advance after it moved the branch ends
// the sync passed once, with the stored base following the branch.
func TestSyncRestartFinishesTheWalk(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
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

// TestSyncAtTheMergeEndsPassedWhileTheEpicIsBusy: a restart cut the sync's advance after it moved the branch to the
// merge and before the end was written, and by the next pass a planner turn runs. The sync ends passed, its stored base
// following the branch, rather than skipped with the branch ahead of the stored base, where the next batch's advance
// would park the epic. Nothing is told.
func TestSyncAtTheMergeEndsPassedWhileTheEpicIsBusy(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncAdvance()
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

// TestSyncDoesNotAdvanceABusyEpic: a planner turn that started while the sync's checks ran keeps the sync from moving
// the branch, which is still at the stored base: the sync ends skipped with why, tells nothing, and the branch and stored
// base stay.
func TestSyncDoesNotAdvanceABusyEpic(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncAdvance()
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

// TestSyncDoesNotAdvanceAMovedBranch: a branch another commit moved while the sync ran is not moved. The sync ends
// failed and tells why, and the epic is not parked.
func TestSyncDoesNotAdvanceAMovedBranch(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	candidate, originHead, sync := w.toSyncGate()
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
	w.requireSyncEnded(syncFirst, outcomeFailed, output)
	if epic := epicOf(t, w.s); epic.StoredBase != candidate || epic.Parked != "" || epic.Synced != "" {
		t.Errorf("epic = stored base %q, parked %q, synced %q, want %q, not parked, none", epic.StoredBase,
			epic.Parked, epic.Synced, candidate)
	}
	if tip := w.git("rev-parse", integrateBranch); tip != moved {
		t.Errorf("branch = %s, want it left at %s", tip, moved)
	}
	if tells := w.syncTells(); !reflect.DeepEqual(tells, []string{"workflow node plan/M1 failed: " + output}) {
		t.Errorf("tells = %q, want the refused advance", tells)
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
	w.endRed("red")
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
	w.requireSyncEnded(syncSecond, "skipped", fmt.Sprintf("origin's commit %s is in the stored base %s", originHead,
		merge))
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
	if err := s.PrepareSync(ctx, runEpic, syncFirst, 0, head, `{"base":"`+base+`"}`); err != nil {
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
	ctx context.Context, epic, path string, attempt int64, revision, result string,
) error {
	if f.failing {
		return errSyncWrite
	}
	return f.Store.PrepareSync(ctx, epic, path, attempt, revision, result)
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
