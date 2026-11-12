package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepCtlSessions is stepStartSessions that also records each Stop.
type stepCtlSessions struct {
	*stepStartSessions

	mu      sync.Mutex
	stopped []string
}

func (f *stepCtlSessions) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

// stoppedIDs returns a copy of the ids Stop was called with.
func (f *stepCtlSessions) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// stepCtlFixture holds a store, a writer and fake sessions shared by every Steps the test builds, with worktrees under
// t.TempDir(). Each Steps is closed at cleanup.
type stepCtlFixture struct {
	steps     *Steps
	sessions  *stepCtlSessions
	store     *store.Store
	writer    *Writer
	worktrees string
	locks     []string
}

// newStepCtlFixture returns a fixture with one Steps over a real store and writer.
func newStepCtlFixture(t *testing.T) *stepCtlFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	f := &stepCtlFixture{
		store:     s,
		writer:    writer,
		worktrees: t.TempDir(),
		locks:     []string{filepath.Join(t.TempDir(), "gate.lock")},
		sessions:  &stepCtlSessions{stepStartSessions: &stepStartSessions{writer: writer}},
	}
	f.steps = f.newSteps(t)
	return f
}

// newSteps returns a Steps over the fixture's store, writer and sessions. It is closed at cleanup.
func (f *stepCtlFixture) newSteps(t *testing.T) *Steps {
	t.Helper()
	steps, err := NewSteps(StepsConfig{
		Sessions:  f.sessions,
		Store:     f.store,
		Writer:    f.writer,
		Locks:     f.locks,
		Worktrees: f.worktrees,
		Environ:   []string{"PATH=" + os.Getenv("PATH")},
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return steps
}

// started returns the StepSession StartStep was called with for id. The test fails if there is none.
func (f *stepCtlFixture) started(t *testing.T, id string) StepSession {
	t.Helper()
	f.sessions.stepStartSessions.mu.Lock()
	defer f.sessions.stepStartSessions.mu.Unlock()
	for _, step := range f.sessions.started {
		if step.ID == id {
			return step
		}
	}
	t.Fatalf("StartStep was not called for step %s", id)
	return StepSession{}
}

// startReadOnly starts a read-only step at the head of repo and returns its id.
func (f *stepCtlFixture) startReadOnly(t *testing.T, repo string) string {
	t.Helper()
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id, err := f.steps.Start(t.Context(), stepStartReadOnly(repo, revision))
	if err != nil {
		t.Fatalf("Start read-only step: %v", err)
	}
	return id
}

// startGated starts a writing step in linked that names gate unit from a gates file in t.TempDir(). The gate writes its
// pid to pidFile and sleeps a minute. Its done return names the workspace's HEAD, so every commit check passes and the
// gate runs.
func (f *stepCtlFixture) startGated(t *testing.T, linked, pidFile string) string {
	t.Helper()
	gatesFile := filepath.Join(t.TempDir(), "gates.toml")
	script := fmt.Sprintf(`echo $$ > %q; sleep 60`, pidFile)
	stepGateWriteFile(t, gatesFile, stepGateFileText(stepGateShell(t), script, 300))
	id, err := f.steps.Start(t.Context(), stepGateNamedStart(linked, "unit", gatesFile))
	if err != nil {
		t.Fatalf("Start writing step: %v", err)
	}
	head := stepGitTestRun(t, linked, "rev-parse", "HEAD")
	raw := fmt.Sprintf(`{"status":%q,"commit":%q}`, stepLoopDone, head)
	f.started(t, id).Observer.Return(session.TurnEnded{StructuredOutput: []byte(raw)})
	return id
}

// endStep returns a done return for step id, with the default done status, and waits until its row ends.
func (f *stepCtlFixture) endStep(t *testing.T, id string) {
	t.Helper()
	raw := fmt.Sprintf(`{"status":%q}`, stepDefaultDone)
	f.started(t, id).Observer.Return(session.TurnEnded{StructuredOutput: []byte(raw)})
	stepStartAwait(t, "step "+id+" to end", func() bool {
		row, err := f.steps.Status(t.Context(), id)
		return err == nil && row.End != ""
	})
}

// stepCtlGatePid returns the pid a gate wrote to path, and false until it has written one.
func stepCtlGatePid(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid, err == nil
}

// stepCtlAwaitGatePid waits until a gate writes its pid to path and returns it.
func stepCtlAwaitGatePid(t *testing.T, path string) int {
	t.Helper()
	stepStartAwait(t, "the gate to start", func() bool {
		_, ok := stepCtlGatePid(path)
		return ok
	})
	pid, _ := stepCtlGatePid(path)
	return pid
}

// stepCtlRequireKilled fails the test unless no process has pid.
func stepCtlRequireKilled(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("gate process %d still exists after Stop or Close: kill(0) = %v", pid, err)
	}
}

func TestStepsStopEndsRunningStepStoppedAndRemovesItsWorktree(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	id := f.startReadOnly(t, repo)
	wt := filepath.Join(f.worktrees, id)
	if stepStartGone(wt) {
		t.Fatal("worktree is missing while the step runs")
	}

	if err := f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != "stopped" {
		t.Errorf("end = %q, want stopped", row.End)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 1 || got[0] != id {
		t.Errorf("session Stop calls = %v, want [%s]", got, id)
	}
	if !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after Stop", wt)
	}
	if list := stepWorktreeTestWorktrees(t, repo); strings.Contains(list, id) {
		t.Errorf("git still lists the step worktree after Stop:\n%s", list)
	}
}

func TestStepsStopKillsGateAndReturnsPromptly(t *testing.T) {
	f := newStepCtlFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	pidFile := filepath.Join(t.TempDir(), "gate.pid")
	id := f.startGated(t, linked, pidFile)
	pid := stepCtlAwaitGatePid(t, pidFile)

	began := time.Now()
	if err := f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if elapsed := time.Since(began); elapsed >= waitTimeout {
		t.Errorf("Stop took %s mid-gate, want it to return promptly", elapsed)
	}
	stepCtlRequireKilled(t, pid)

	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != "stopped" {
		t.Errorf("end = %q, want stopped", row.End)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 1 || got[0] != id {
		t.Errorf("session Stop calls = %v, want [%s]", got, id)
	}
}

func TestStepsStopOfEndedStepChangesNothing(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	id := f.startReadOnly(t, repo)
	f.endStep(t, id)
	wt := filepath.Join(f.worktrees, id)
	stepStartAwait(t, "the worktree of the ended step to go", func() bool { return stepStartGone(wt) })
	before, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	if err := f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of an ended step: %v, want nil", err)
	}

	after, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if after.End != before.End || after.Status != before.Status || after.Reason != before.Reason ||
		!after.Ended.Equal(before.Ended) {
		t.Errorf("row changed by Stop: before %+v, after %+v", before, after)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 1 {
		t.Errorf("session Stop calls = %v, want only the one from the step's own end", got)
	}
}

func TestStepsStopOfRowWithoutLoopEndsItStopped(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	id := f.startReadOnly(t, repo)
	wt := filepath.Join(f.worktrees, id)
	f.steps.Close()

	if err := f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop of a row with no loop: %v", err)
	}

	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if row.End != "stopped" {
		t.Errorf("end = %q, want stopped", row.End)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 1 || got[0] != id {
		t.Errorf("session Stop calls = %v, want [%s]", got, id)
	}
	if !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after Stop", wt)
	}
}

func TestStepsStopUnknownIDErrorsWithErrNoStep(t *testing.T) {
	f := newStepCtlFixture(t)

	err := f.steps.Stop(t.Context(), "no-such-step")
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Stop of an unknown id error = %v, want it to match ErrNoStep", err)
	}
}

func TestStepsWaitReturnsEndedStepAtOnceWithRowsInOrder(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	running := f.startReadOnly(t, repo)
	ended := f.startReadOnly(t, repo)
	f.endStep(t, ended)

	began := time.Now()
	got, rows, err := f.steps.Wait(t.Context(), []string{running, ended}, waitTimeout)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(began); elapsed >= waitTimeout/2 {
		t.Errorf("Wait took %s with an ended step listed, want it at once", elapsed)
	}
	if got == nil || got.ID != ended || got.End != stepLoopEndDone {
		t.Fatalf("Wait ended = %+v, want step %s ended done", got, ended)
	}
	if len(rows) != 2 || rows[0].ID != running || rows[1].ID != ended {
		t.Fatalf("Wait rows = %v, want [%s %s] in order", stepCtlRowIDs(rows), running, ended)
	}
	if rows[0].End != "" {
		t.Errorf("running row end = %q, want open", rows[0].End)
	}
	if rows[1].End != stepLoopEndDone {
		t.Errorf("ended row end = %q, want done", rows[1].End)
	}
}

func TestStepsWaitBlocksUntilAStepEnds(t *testing.T) {
	f := newStepCtlFixture(t)
	id := f.startReadOnly(t, stepGitTestRepo(t))

	type waited struct {
		ended *store.StepRow
		rows  []store.StepRow
		err   error
	}
	result := make(chan waited, 1)
	go func() {
		ended, rows, err := f.steps.Wait(t.Context(), []string{id}, waitTimeout)
		result <- waited{ended, rows, err}
	}()

	select {
	case w := <-result:
		t.Fatalf("Wait returned before the step ended: ended %+v err %v", w.ended, w.err)
	case <-time.After(50 * time.Millisecond):
	}

	f.endStep(t, id)
	var w waited
	select {
	case w = <-result:
	case <-time.After(waitTimeout):
		t.Fatal("Wait did not return after the step ended")
	}
	if w.err != nil {
		t.Fatalf("Wait: %v", w.err)
	}
	if w.ended == nil || w.ended.ID != id || w.ended.End != stepLoopEndDone {
		t.Fatalf("Wait ended = %+v, want step %s ended done", w.ended, id)
	}
	if len(w.rows) != 1 || w.rows[0].ID != id || w.rows[0].End != stepLoopEndDone {
		t.Errorf("Wait rows = %+v, want the ended step", w.rows)
	}
}

func TestStepsWaitTimeoutReturnsStatusesAndNoEndedStep(t *testing.T) {
	f := newStepCtlFixture(t)
	id := f.startReadOnly(t, stepGitTestRepo(t))

	got, rows, err := f.steps.Wait(t.Context(), []string{id}, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got != nil {
		t.Errorf("Wait ended = %+v at timeout, want nil", got)
	}
	if len(rows) != 1 || rows[0].ID != id || rows[0].End != "" {
		t.Errorf("Wait rows = %+v, want the open step", rows)
	}
}

func TestStepsWaitCtxEndingReturnsItsError(t *testing.T) {
	f := newStepCtlFixture(t)
	id := f.startReadOnly(t, stepGitTestRepo(t))
	ctx, cancel := context.WithCancel(t.Context())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()

	got, _, err := f.steps.Wait(ctx, []string{id}, waitTimeout)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want it to match context.Canceled", err)
	}
	if got != nil {
		t.Errorf("Wait ended = %+v when its ctx ended, want nil", got)
	}
}

func TestStepsWaitUnknownIDErrorsWithErrNoStep(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	ended := f.startReadOnly(t, repo)
	f.endStep(t, ended)

	_, _, err := f.steps.Wait(t.Context(), []string{ended, "no-such-step"}, waitTimeout)
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Wait error = %v, want it to match ErrNoStep", err)
	}
}

func TestStepsReconcileEndsOpenRowsEngineRestartedAndRemovesWorktrees(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	first := f.startReadOnly(t, repo)
	second := f.startReadOnly(t, repo)
	f.steps.Close()
	for _, id := range []string{first, second} {
		if stepStartGone(filepath.Join(f.worktrees, id)) {
			t.Fatalf("worktree of %s is missing before the restart", id)
		}
	}

	restarted := f.newSteps(t)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	for _, id := range []string{first, second} {
		row, err := restarted.Status(t.Context(), id)
		if err != nil {
			t.Fatalf("Status %s: %v", id, err)
		}
		if row.End != "engine_restarted" {
			t.Errorf("step %s end = %q, want engine_restarted", id, row.End)
		}
		if wt := filepath.Join(f.worktrees, id); !stepStartGone(wt) {
			t.Errorf("worktree %s still exists after Reconcile", wt)
		}
	}
	if list := stepWorktreeTestWorktrees(t, repo); strings.Contains(list, first) || strings.Contains(list, second) {
		t.Errorf("git still lists step worktrees after Reconcile:\n%s", list)
	}
	if got := f.sessions.stoppedIDs(); len(got) != 0 {
		t.Errorf("Reconcile stopped sessions %v, want none", got)
	}
	open, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(open) != 0 {
		t.Errorf("open step rows after Reconcile = %v, want none", stepCtlRowIDs(open))
	}
}

func TestStepsCloseKillsGateAndLeavesRowsOpen(t *testing.T) {
	f := newStepCtlFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	pidFile := filepath.Join(t.TempDir(), "gate.pid")
	id := f.startGated(t, linked, pidFile)
	pid := stepCtlAwaitGatePid(t, pidFile)

	f.steps.Close()

	f.steps.mu.Lock()
	live := len(f.steps.loops)
	f.steps.mu.Unlock()
	if live != 0 {
		t.Errorf("%d loops still running after Close, want none", live)
	}
	stepCtlRequireKilled(t, pid)
	open, err := f.store.OpenSteps(t.Context())
	if err != nil {
		t.Fatalf("OpenSteps: %v", err)
	}
	if len(open) != 1 || open[0].ID != id {
		t.Errorf("open step rows after Close = %v, want [%s]", stepCtlRowIDs(open), id)
	}
}

// stepCtlRowIDs returns the ids of rows in order.
func stepCtlRowIDs(rows []store.StepRow) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func TestStepsReconcileEndsEveryRowWhenAWorktreeCannotBeRemoved(t *testing.T) {
	f := newStepCtlFixture(t)
	repo := stepGitTestRepo(t)
	first := f.startReadOnly(t, repo)
	second := f.startReadOnly(t, repo)
	f.steps.Close()
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("remove the steps' repository: %v", err)
	}

	restarted := f.newSteps(t)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, id := range []string{first, second} {
		row, err := restarted.Status(t.Context(), id)
		if err != nil {
			t.Fatalf("Status %s: %v", id, err)
		}
		if row.End != "engine_restarted" {
			t.Errorf("step %s end = %q, want engine_restarted although its worktree could not be removed", id, row.End)
		}
	}
}
