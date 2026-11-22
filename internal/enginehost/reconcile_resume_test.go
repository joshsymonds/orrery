package enginehost

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/waittest"
)

// stateTrigger accepts a batch that sets an engine row to state. A crashApplier that refuses it leaves the row at its
// earlier state, after the holder has spooled the output that set it.
func stateTrigger(state string) func([]store.EngineOp) bool {
	return func(ops []store.EngineOp) bool {
		for _, op := range ops {
			set, ok := op.(store.EngineSet)
			if ok && set.Changes.State != nil && *set.Changes.State == state {
				return true
			}
		}
		return false
	}
}

// seedClaudeRow adds the row id of kind, in dir and state, with the Claude session the fake claude reports.
func seedClaudeRow(t *testing.T, s *store.Store, id, dir, state, kind string) {
	t.Helper()
	claude := "session_1"
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: id, Dir: dir, State: state, Kind: kind},
		store.EngineSet{ID: id, Changes: store.EngineChanges{ClaudeSession: &claude}},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed engine row %s: %v", id, err)
	}
}

// stallNextPrompt makes the fake claude in dir play STALL in place of the next prompt it reads. A resumed turn that stalls
// stays working, so a test can check the row while the turn runs.
func stallNextPrompt(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "fakeclaude.next"), []byte("STALL"), 0o600); err != nil {
		t.Fatalf("write fake claude next prompt: %v", err)
	}
}

// fakeStarts returns the argument line of each start of the fake claude logged in dir, in order. A log not yet written
// has none.
func fakeStarts(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fakeclaude.args"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read fake claude args log: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// requireDirPresent fails when dir does not exist.
func requireDirPresent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("dir %s: %v, want the directory of the resumed holder", dir, err)
	}
}

// requireResumed checks that a restart resumed the working turn of session id in dir: the row is working, its last
// prompt is "Resume working.", a fake claude started in dir with --resume session_1 and read that prompt, and the owner
// got exactly one notice, which names the resume. The notice is checked first, so a row that did not resume fails at
// once.
func requireResumed(t *testing.T, s *store.Store, path, id, dir string, sink *fakeSink) {
	t.Helper()
	row, err := s.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession %s: %v", id, err)
	}
	if row.State != "working" {
		t.Fatalf("state of session %s after the restart = %q, want working", id, row.State)
	}
	texts := promptTexts(t, path, id)
	if len(texts) == 0 || texts[len(texts)-1] != "Resume working." {
		t.Fatalf("prompts of session %s = %q, want the last to be Resume working.", id, texts)
	}
	want := Notice{Session: id, Dir: dir, State: "interrupted", Body: "orreryd restarted during the turn; resumed it"}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices after the restart = %+v, want exactly [%+v]", got, want)
	}
	waittest.Until(t, nil, "the fake claude to start with --resume session_1", func() bool {
		starts := fakeStarts(t, dir)
		return len(starts) > 0 && strings.Contains(starts[len(starts)-1], "--resume session_1")
	})
	waittest.Until(t, nil, "the fake claude to read Resume working.", func() bool {
		return stdinLinesWith(t, dir, "Resume working.") == 1
	})
}

func TestReconcileResumesWorkingRowWithClaudeSession(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "working", dir, "working", store.KindEngine)
	stallNextPrompt(t, dir)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireResumed(t, s, path, "working", dir, sink)
}

func TestReconcileDoesNotResumeWaitingRow(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "waiting", dir, "waiting", store.KindEngine)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"waiting": {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: "waiting", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts for a waiting row = %q, want none", starts)
	}
}

func TestReconcileDoesNotResumeTurnResumedAlready(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "working", dir, "working", store.KindEngine)
	// The last prompt is the resume of an earlier restart, so this interruption must not resume the turn again.
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineLaunch{ID: "working"},
		store.EnginePrompt{ID: "working", Text: "Resume working.", WriteID: "resume-1"},
	}, time.Unix(2, 0)); err != nil {
		t.Fatalf("seed resume prompt: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"working": {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: "working", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts for a turn already resumed = %q, want none", starts)
	}
}

func TestReconcileDoesNotResumeStepRow(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	dir := t.TempDir()
	seedClaudeRow(t, s, "step", dir, "working", store.KindStep)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireEngineRows(t, s, map[string]restartRow{
		"step": {state: "interrupted", reason: restartedReason},
	})
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices for a step row = %+v, want none", got)
	}
	if starts := fakeStarts(t, dir); len(starts) != 0 {
		t.Fatalf("fake claude starts for a step row = %q, want none", starts)
	}
}

func TestReconcileNotifiesOnlyFailureWhenResumeFails(t *testing.T) {
	t.Parallel()
	s, _ := openStore(t)
	// The row's directory does not exist, so the resume fails before claude starts.
	dir := filepath.Join(t.TempDir(), "missing")
	seedClaudeRow(t, s, "working", dir, "working", store.KindEngine)
	writer := NewWriter(WriterConfig{Applier: s})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newReconcileManager(t, s, writer, sink)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	row, err := s.EngineSession(t.Context(), "working")
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "failed" || row.Reason == "" {
		t.Fatalf("row after the failed resume = state %q reason %q, want failed with a reason", row.State, row.Reason)
	}
	want := Notice{Session: "working", Dir: dir, State: "failed", Body: row.Reason}
	if got := sink.got(); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}

func TestDrainOfTurnEndInSpoolIsNotResumed(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, stateTrigger("done"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The store refuses the turn's end, so the holder spools it unrecorded and the row stays working.
	waitUntil(t, engineOf(rig.manager), "the store refuses the turn's end", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	rig.manager.Close()
	if err := syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
		t.Fatalf("kill holder %d: %v", pids.Holder, err)
	}
	waitHolderEnded(t, pids)

	sink := restartOver(t, rig, &textSpy{inner: rig.store})

	gateWaitGone(t, pid)
	row, err := rig.store.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "done" {
		t.Fatalf("state after the restart = %q, want done from the spooled turn end", row.State)
	}
	for _, got := range sink.got() {
		if got.State == "interrupted" {
			t.Fatalf("notices after the restart = %+v, want no interruption", sink.got())
		}
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts after the restart = %q, want only the first", starts)
	}
	if texts := promptTexts(t, rig.path, id); !slices.Equal(texts, []string{"hi"}) {
		t.Fatalf("prompts after the restart = %q, want only hi", texts)
	}
}

func TestDrainOfOpenQuestionInSpoolIsNotResumed(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, stateTrigger("waiting"), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The store refuses the open question, so the holder spools it unrecorded and the row stays working.
	waitUntil(t, engineOf(rig.manager), "the store refuses the open question", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, rig.root, id))
	rig.manager.Close()
	if err := syscall.Kill(pids.Holder, syscall.SIGKILL); err != nil {
		t.Fatalf("kill holder %d: %v", pids.Holder, err)
	}
	waitHolderEnded(t, pids)

	sink := restartOver(t, rig, &textSpy{inner: rig.store})

	gateWaitGone(t, pid)
	requireEngineRows(t, rig.store, map[string]restartRow{
		id: {state: "interrupted", reason: restartedReason},
	})
	got := sink.got()
	want := Notice{Session: id, Dir: dir, State: "interrupted", Body: restartedReason}
	if len(got) == 0 || !reflect.DeepEqual(got[len(got)-1], want) {
		t.Fatalf("notices after the restart = %+v, want the last to be [%+v]", got, want)
	}
	if starts := fakeStarts(t, dir); len(starts) != 1 {
		t.Fatalf("fake claude starts after the restart = %q, want only the first", starts)
	}
	if texts := promptTexts(t, rig.path, id); !slices.Equal(texts, []string{"ASK"}) {
		t.Fatalf("prompts after the restart = %q, want only ASK", texts)
	}
}
