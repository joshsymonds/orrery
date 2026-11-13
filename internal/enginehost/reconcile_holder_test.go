package enginehost

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// newStateManager returns a manager that reads rows from s, writes through writer and sends its notices to sink. Its
// sessions run their holders under scope in the state directory stateDir, and it logs to logger, or discards its logs
// when logger is nil. Close runs at the end of the test.
func newStateManager(
	t *testing.T, s Store, writer *Writer, sink NoticeSink, scope, stateDir string, logger *slog.Logger,
) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:         1,
		Writer:      writer,
		Now:         fixedClock,
		Notifier:    sink,
		Store:       s,
		HolderScope: scope,
		StateDir:    stateDir,
		Logger:      logger,
	})
	t.Cleanup(m.Close)
	return m
}

// seedWorkingRow adds the engine row id, working in dir, with one unfinished text block.
func seedWorkingRow(t *testing.T, s *store.Store, id, dir string) {
	t.Helper()
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: id, Dir: dir, State: "working"},
		store.EngineText{ID: id, MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("seed engine row %s: %v", id, err)
	}
}

// startLeftHolder starts the holder of session id under root directly, as `orrery holder` runs it, with the fake claude
// as its child in dir. The holder is stopped and its directory cleared at the end of the test.
func startLeftHolder(t *testing.T, root, id, dir string) {
	t.Helper()
	orrery, err := exec.LookPath("orrery")
	if err != nil {
		t.Fatalf("find orrery: %v", err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	cmd := exec.Command(orrery, "holder", "--session", id, "--state-dir", root, "--", sh, fakeClaudePath(t))
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder of session %s: %v", id, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
		defer cancel()
		if err := holder.Clear(ctx, holderDir(t, root, id)); err != nil {
			t.Errorf("clear holder of session %s: %v", id, err)
		}
		if err := cmd.Wait(); err != nil {
			t.Errorf("holder of session %s exit: %v", id, err)
		}
	})
}

// requireDirGone fails when dir still exists.
func requireDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dir %s: err %v, want it removed", dir, err)
	}
}

func TestReconcileStopsHolderLeftRunningForWorkingRow(t *testing.T) {
	root := holderRoot(t)
	s, path := openStore(t)
	dir := t.TempDir()
	seedWorkingRow(t, s, "working", dir)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	startLeftHolder(t, root, "working", dir)
	pid := fakePID(t, dir)
	sink := &fakeSink{}
	m := newStateManager(t, s, writer, sink, "", root, nil)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireGone(t, pid)
	requireDirGone(t, holderDir(t, root, "working"))
	requireEngineRows(t, s, map[string]restartRow{
		"working": {state: "interrupted", reason: restartedReason},
	})
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}
	want := Notice{Session: "working", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}

func TestReconcileAfterScopedCloseStopsHeldFake(t *testing.T) {
	root := holderRoot(t)
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	first := newStateManager(t, s, writer, &fakeSink{}, fakeSystemdRunPath(t), root, nil)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, first, root, id) })
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })
	pid := fakePID(t, dir)

	first.Close()

	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("fake claude %d is not running after Close: %v", pid, err)
	}
	sink := &fakeSink{}
	second := newStateManager(t, s, writer, sink, "", root, nil)
	if err := second.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	requireGone(t, pid)
	requireDirGone(t, holderDir(t, root, id))
	requireEngineRows(t, s, map[string]restartRow{
		id: {state: "interrupted", reason: "orreryd stopped during the turn"},
	})
	if n := countRows(t, path, "engine_text"); n != 0 {
		t.Fatalf("unfinished text rows after Reconcile = %d, want 0", n)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after Reconcile of a closed session = %+v, want none", got)
	}
}

func TestReconcileLeavesLiveSessionHolderRunning(t *testing.T) {
	root := holderRoot(t)
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sink := &fakeSink{}
	m := newStateManager(t, s, writer, sink, fakeSystemdRunPath(t), root, nil)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })
	pid := fakePID(t, dir)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("live session's fake claude %d is not running after Reconcile: %v", pid, err)
	}
	conn, err := holder.Dial(t.Context(), holderSocket(t, root, id))
	if err != nil {
		t.Fatalf("holder of live session %s does not answer hello after Reconcile: %v", id, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close holder connection: %v", err)
	}
	if n := countRows(t, path, "engine_text"); n != 1 {
		t.Fatalf("unfinished text rows of the live session after Reconcile = %d, want 1", n)
	}
	row, err := s.EngineSession(t.Context(), id)
	if err != nil {
		t.Fatalf("EngineSession: %v", err)
	}
	if row.State != "working" || row.Reason != "" {
		t.Fatalf("live session after Reconcile = state %q reason %q, want working with no reason",
			row.State, row.Reason)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices after Reconcile of a live session = %+v, want none", got)
	}
}

func TestReconcileWithoutHoldersDirSettlesRows(t *testing.T) {
	root := holderRoot(t)
	s, _ := openStore(t)
	seedWorkingRow(t, s, "working", t.TempDir())
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := newStateManager(t, s, writer, &fakeSink{}, "", root, nil)

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile with no holders dir = %v, want nil", err)
	}
	requireEngineRows(t, s, map[string]restartRow{
		"working": {state: "interrupted", reason: restartedReason},
	})
}

func TestReconcileLogsFailedClearAndSettlesRows(t *testing.T) {
	root := holderRoot(t)
	s, _ := openStore(t)
	dir := t.TempDir()
	seedWorkingRow(t, s, "working", dir)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	// An entry that is a file, not a session directory, cannot be cleared: its lock file cannot be opened inside it.
	const broken = "broken"
	holders := filepath.Join(root, "holders")
	if err := os.Mkdir(holders, 0o700); err != nil {
		t.Fatalf("make holders dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(holders, broken), nil, 0o600); err != nil {
		t.Fatalf("write broken holder entry: %v", err)
	}
	var logs bytes.Buffer
	sink := &fakeSink{}
	m := newStateManager(t, s, writer, sink, "", root, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := m.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile = %v, want nil: a failed clear must not stop the rows from settling", err)
	}
	if !strings.Contains(logs.String(), broken) {
		t.Fatalf("log %q does not name the session %s whose clear failed", logs.String(), broken)
	}
	requireEngineRows(t, s, map[string]restartRow{
		"working": {state: "interrupted", reason: restartedReason},
	})
	want := Notice{Session: "working", Dir: dir, State: "interrupted", Body: restartedReason}
	if got := sink.got(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %+v, want exactly [%+v]", got, want)
	}
}
