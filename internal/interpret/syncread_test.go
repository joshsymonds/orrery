package interpret

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/store"
)

// countedUploadPack makes origin's side of each fetch and ls-remote the test repository runs a script that counts
// itself and then runs git upload-pack, or, for each run whose number stalls lists, sleeps a minute instead, as an
// origin that stops answering does. It returns how many times origin has been read.
func (w *integrateWorld) countedUploadPack(stalls ...int) func() int {
	w.t.Helper()
	dir := w.t.TempDir()
	count := filepath.Join(dir, "count")
	listed := make([]string, 0, len(stalls))
	for _, n := range stalls {
		listed = append(listed, fmt.Sprint(n))
	}
	body := fmt.Sprintf("#!/bin/sh\necho read >> '%s'\nn=$(wc -l < '%s' | tr -d ' ')\n", count, count) +
		fmt.Sprintf("case ' %s ' in *\" $n \"*) exec sleep 60;; esac\n", strings.Join(listed, " ")) +
		"exec git upload-pack \"$@\"\n"
	script := filepath.Join(dir, "upload-pack")
	exectest.WriteFile(w.t, script, []byte(body), 0o700)
	w.git("config", "remote.origin.uploadpack", script)
	return func() int {
		w.t.Helper()
		data, err := os.ReadFile(count)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			w.t.Fatalf("read the upload-pack count: %v", err)
		}
		return strings.Count(string(data), "\n")
	}
}

// freeTurn starts the test epic's next planner turn and ends it free, as a turn a restart cut ends, and returns its
// seq.
func (w *integrateWorld) freeTurn() int64 {
	w.t.Helper()
	seq := w.claimTurn()
	free := store.TurnFree{Reason: "engine_restarted"}
	if err := w.s.FreeTurn(context.Background(), runEpic, seq, free, w.runner.now()); err != nil {
		w.t.Fatalf("FreeTurn %d: %v", seq, err)
	}
	return seq
}

// quietTurn completes the test epic's next planner turn with no task and outcome work, so the epic is at a quiet point
// of a new turn with its stored base where it was.
func (w *integrateWorld) quietTurn() {
	w.t.Helper()
	completeTurn(w.t, w.s, store.EpicPlan{Decisions: []string{}, Outline: "nothing to plan", Outcome: "work"})
}

// TestSyncKeyIgnoresFreedTurns: a sync whose Done gate failed tried origin's commit. Turns the planner then could not
// start, which end free, are no turns a sync is keyed by: however long passes between them, no sync is started for
// them, no run is stored, and origin is not read again. The next turn the planner runs is keyed, and its quiet point
// reads origin's main again.
func TestSyncKeyIgnoresFreedTurns(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	reads := w.countedUploadPack()
	_, _, sync := w.toSyncGate()
	if sync.Revision == "" || reads() != 3 {
		t.Fatalf("sync = %+v after %d reads of origin, want it merged after the read, ls-remote and fetch", sync,
			reads())
	}
	w.endGate(syncFirst, 1, "lint failed")
	if !w.passed() {
		t.Fatal("the planner got no turn after the sync failed")
	}
	for i := range 3 {
		w.laterBy(time.Duration(i+1) * syncInterval)
		seq := w.freeTurn()
		if !w.passed() {
			t.Errorf("the plan got no run after free turn %d", seq)
		}
		if path := syncPath("plan", seq); w.hasRunAt(path) {
			t.Errorf("sync %s stored for free turn %d", path, seq)
		}
	}
	if got := reads(); got != 3 {
		t.Errorf("origin read %d times after the free turns, want the sync's 3 only", got)
	}

	w.quietTurn()
	if !w.passed() {
		t.Error("the planner got no turn at the quiet point of a turn it ran")
	}
	if got := reads(); got != 4 {
		t.Errorf("origin read %d times after a turn the planner ran, want one more read, 4", got)
	}
	if w.hasRunAt("plan/M5") {
		t.Error("sync plan/M5 stored for origin's commit the first sync tried")
	}
}

// TestSyncReadsOriginAtMostOncePerIntervalForAStoredBase: the quiet point after the first batch reads origin's main
// once and finds the stored base holds it. Origin's main then moves, and the epic stays at that quiet point: within
// the sync interval no pass reads origin again or starts a sync, and the planner gets its turn on each. Once the
// interval has passed by the runner's clock, the next pass reads origin again, which a restart keeps too since the read
// is stored, finds its commit ahead of the stored base, and starts the sync, holding the planner.
func TestSyncReadsOriginAtMostOncePerIntervalForAStoredBase(t *testing.T) {
	t.Parallel()
	w := newSyncWorld(t)
	reads := w.countedUploadPack()
	candidate := w.toCaptured()
	if !w.passed() || reads() != 1 {
		t.Fatalf("after the first batch: origin read %d times, want once, and the planner given its turn", reads())
	}
	w.requireRead(candidate, w.base, originHeld)
	originHead := w.originCommit("origin.txt")
	for _, by := range []time.Duration{0, time.Minute, syncInterval - time.Second} {
		w.laterBy(by)
		if !w.passed() {
			t.Errorf("the planner got no turn %s after the read", by)
		}
	}
	if got := reads(); got != 1 || w.hasRunAt(syncFirst) {
		t.Errorf("origin read %d times and sync stored %v within the interval, want 1 and none", got,
			w.hasRunAt(syncFirst))
	}

	w.runner = runNodes(w.s, w.fakes)
	w.runner.logger = slog.New(slog.NewTextHandler(w.logs, nil))
	w.laterBy(syncInterval)
	if w.passed() {
		t.Error("the planner got a turn while the sync after the interval runs")
	}
	sync := runAt(t, w.s, syncFirst)
	if sync.State != store.NodeRunning || sync.Revision == "" || reads() != 4 {
		t.Fatalf("sync = %+v after %d reads of origin, want it merging after the read, ls-remote and fetch", sync,
			reads())
	}
	if parents := w.git("rev-parse", sync.Revision+"^1", sync.Revision+"^2"); parents != candidate+"\n"+originHead {
		t.Errorf("merge parents = %q, want %s and %s", parents, candidate, originHead)
	}
	w.requireRead(candidate, originHead, originTaken)
}

// syncReadThatStallsIsStoppedAtTheReadLimitAndBacksOff: an origin that never answers holds the read of origin's
// main only for the runner's read limit. git is stopped then, with the upload-pack it started, the read is recorded
// failed and logged with the limit named, and no sync starts, so the planner gets its turn. Within the sync interval
// the failed read stands: no pass reads origin again or logs. Once it has passed, the next pass reads origin again.
func syncReadThatStallsIsStoppedAtTheReadLimitAndBacksOff(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		w.originCommit("origin.txt")
		reads := w.countedUploadPack(1, 2)
		w.runner.readLimit = time.Second
		logs := &bytes.Buffer{}
		w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
		started := time.Now()
		if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
			t.Error("the planner got no turn after the read was stopped")
		}
		if took := time.Since(started); took > 30*time.Second {
			t.Errorf("the pass took %s, want it cut at the limit, well before the stalled origin's 60s", took)
		}
		if w.hasRunAt(syncFirst) {
			t.Errorf("sync %s stored after the read was stopped", syncFirst)
		}
		w.requireRead(candidate, "", originFailed)
		if !strings.Contains(logs.String(), "origin's main was not read") ||
			!strings.Contains(logs.String(), "git did not finish within 1s: git ls-remote") {
			t.Errorf("logs = %q, want the stopped read logged with the limit", logs.String())
		}

		logs.Reset()
		w.laterBy(syncInterval - time.Second)
		if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
			t.Error("the planner got no turn while the failed read stands")
		}
		if logs.Len() > 0 || reads() != 1 {
			t.Errorf("origin read %d times, logs %q, within the interval, want 1 and nothing", reads(), logs.String())
		}
		w.laterBy(syncInterval)
		w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow())
		if got := reads(); got != 2 || !strings.Contains(logs.String(), "origin's main was not read") {
			t.Errorf("origin read %d times, logs %q, once the interval passed, want 2 and the read logged", got,
				logs.String())
		}
	}
}

// syncFetchThatStallsIsStoppedAtTheFetchLimitAndBacksOff: origin answers the read and the prepare's ls-remote and
// then stalls in the fetch, which is stopped at the runner's fetch limit. The sync ends skipped with the limit named,
// logged and untold, and the planner gets its turn. Its failed read stands for the sync interval, so the quiet point of
// the planner's next turn, at the same stored base, reads nothing and starts no sync. Once the interval has passed, the
// quiet point reads origin again and the sync of the same origin commit merges it, since a skipped sync tried nothing.
func syncFetchThatStallsIsStoppedAtTheFetchLimitAndBacksOff(images *worldImages) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		w, candidate := images.syncWorld(t, atCaptured)
		originHead := w.originCommit("origin.txt")
		reads := w.countedUploadPack(3)
		w.runner.fetchLimit = time.Second
		logs := &bytes.Buffer{}
		w.runner.logger = slog.New(slog.NewTextHandler(logs, nil))
		if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
			t.Error("the planner got no turn after the fetch was stopped")
		}
		run := runAt(t, w.s, syncFirst)
		if run.State != store.NodeEnded || run.Outcome != "skipped" ||
			!strings.HasPrefix(run.Output, "the sync did not run: git did not finish within 1s: git fetch ") {
			t.Errorf("sync = %+v, want ended skipped on the fetch stopped at the limit", run)
		}
		if !strings.Contains(logs.String(), "sync of origin's main did not run") {
			t.Errorf("logs = %q, want the stopped fetch logged", logs.String())
		}
		w.requireRead(candidate, "", originFailed)
		w.requireUnmoved(candidate)
		if tells := w.syncTells(); len(tells) != 0 {
			t.Errorf("tells = %q, want none", tells)
		}

		logs.Reset()
		w.quietTurn()
		if !w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) || w.hasRunAt(syncSecond) {
			t.Errorf("sync stored %v at the next quiet point within the interval, want none and the planner's turn",
				w.hasRunAt(syncSecond))
		}
		if got := reads(); got != 3 || logs.Len() > 0 {
			t.Errorf(
				"origin read %d times, logs %q, within the interval, want the first 3 only and nothing logged",
				got,
				logs.String(),
			)
		}
		w.laterBy(syncInterval)
		if w.runner.passFlow(context.Background(), epicOf(t, w.s), integrateFlow()) {
			t.Error("the planner got a turn while the sync after the interval runs")
		}
		sync := runAt(t, w.s, syncSecond)
		if parents := w.git("rev-parse", sync.Revision+"^1", sync.Revision+"^2"); parents != candidate+"\n"+originHead {
			t.Errorf("merge parents = %q, want %s and the skipped sync's origin commit %s", parents, candidate,
				originHead)
		}
	}
}
