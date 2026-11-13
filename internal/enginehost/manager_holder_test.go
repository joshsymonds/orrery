package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// fakeSystemdRunPath returns the absolute path of the fake systemd-run the holder tests share.
func fakeSystemdRunPath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "holder", "testdata", "fakesystemdrun.sh"))
	if err != nil {
		t.Fatalf("absolute fake systemd-run path: %v", err)
	}
	return path
}

// scopeArgs returns the arguments that put the holder of session id in its own user scope, before the holder's argv.
func scopeArgs(id string) []string {
	return []string{"--user", "--scope", "--collect", "-p", "OOMPolicy=continue", "--unit", "orrery-hold-" + id}
}

// shortRoot creates a temporary directory whose path is short enough for a socket path. A t.TempDir path carries the
// test name and can exceed sun_path's limit. The caller removes the root.
func shortRoot() (string, error) {
	root, err := os.MkdirTemp("", "hold")
	if err != nil {
		return "", fmt.Errorf("create short root: %w", err)
	}
	return root, nil
}

// holderRoot returns a short temporary state directory for the holders of a test. The root is removed when the test
// ends, after the holders in it are cleared.
func holderRoot(t *testing.T) string {
	t.Helper()
	root, err := shortRoot()
	if err != nil {
		t.Fatalf("%v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove %s: %v", root, err)
		}
	})
	return root
}

// holderDir returns the directory of the holder of session id under the state dir root.
func holderDir(t *testing.T, root, id string) string {
	t.Helper()
	dir, err := holder.SessionDir(filepath.Join(root, "holders"), id)
	if err != nil {
		t.Fatalf("holder dir of session %s: %v", id, err)
	}
	return dir
}

// holderSocket returns the socket of the holder of session id under the state dir root.
func holderSocket(t *testing.T, root, id string) string {
	t.Helper()
	sock, err := holder.SocketPath(holderDir(t, root, id))
	if err != nil {
		t.Fatalf("holder socket of session %s: %v", id, err)
	}
	return sock
}

// closeAndClear closes m, then stops the holder of session id under root if one still runs, and empties its directory.
// Close detaches sessions under a scope, so a test ends the holders it leaves running this way. Closing first means no
// live session's process removes the holder directory while it is cleared.
func closeAndClear(t *testing.T, m *Manager, root, id string) {
	t.Helper()
	m.Close()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), managerGuard)
	defer cancel()
	if err := holder.Clear(ctx, holderDir(t, root, id)); err != nil {
		t.Errorf("clear holder of session %s: %v", id, err)
	}
}

// newHolderManager returns a manager with one slot, like newTestManager, whose sessions run their holders under scope
// with state dir stateDir. Its environment is a PATH, a HOME of its own, and environ.
func newHolderManager(t *testing.T, scope, stateDir string, environ ...string) (*Manager, string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, path := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	m := NewManager(Config{
		Shell:       sh,
		Claude:      fakeClaudePath(t),
		Environ:     append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, environ...),
		Cap:         1,
		Writer:      writer,
		Now:         fixedClock,
		Store:       s,
		HolderScope: scope,
		StateDir:    stateDir,
	})
	t.Cleanup(m.Close)
	return m, path
}

// recordedArgs returns the arguments the fake systemd-run recorded in path, one per line.
func recordedArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the fake systemd-run's record: %v", err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestStartWithHolderScopeRunsSessionUnderScopedHolder(t *testing.T) {
	root := holderRoot(t)
	record := filepath.Join(t.TempDir(), "argv")
	m, path := newHolderManager(t, fakeSystemdRunPath(t), root, "FAKE_SYSTEMD_RUN_ARGS="+record)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	orrery, err := exec.LookPath("orrery")
	if err != nil {
		t.Fatalf("find orrery: %v", err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	child, err := session.Command(session.Config{
		Shell: sh, Claude: fakeClaudePath(t), Dir: dir, PermissionMode: permissionMode, Name: id,
	})
	if err != nil {
		t.Fatalf("session command: %v", err)
	}
	want := append(scopeArgs(id), orrery, "holder", "--session", id, "--state-dir", root, "--", child.Path)
	want = append(want, child.Args...)
	if got := recordedArgs(t, record); !slices.Equal(got, want) {
		t.Fatalf("systemd-run argv %q, want %q", got, want)
	}
}

func TestStartWithHolderScopeFailureFailsWithItsReasonAndRunsNoClaude(t *testing.T) {
	const reason = "fake systemd-run refused the scope"
	root := holderRoot(t)
	m, path := newHolderManager(t, fakeSystemdRunPath(t), root, "FAKE_SYSTEMD_RUN_FAIL="+reason)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, path, id, "the failed state", func(h store.EngineHistory) bool {
		return h.State == "failed"
	})
	failure := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			failure = fields["reason"]
		}
	}
	if !strings.Contains(failure, reason) {
		t.Fatalf("failed reason %q does not carry systemd-run's reason %q", failure, reason)
	}
	if _, err := os.Stat(filepath.Join(dir, "fakeclaude.pid")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("fake claude ran after systemd-run failed: stat = %v", err)
	}
}

func TestCloseWithHolderScopeLeavesSessionRunning(t *testing.T) {
	root := holderRoot(t)
	m, path := newHolderManager(t, fakeSystemdRunPath(t), root)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { closeAndClear(t, m, root, id) })
	waitUntil(t, "the stalled block's unfinished text", func() bool { return countRows(t, path, "engine_text") == 1 })
	pid := fakePID(t, dir)

	m.Close()

	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("fake claude %d is not running after Close: %v", pid, err)
	}
	conn, err := holder.Dial(t.Context(), holderSocket(t, root, id))
	if err != nil {
		t.Fatalf("holder of session %s does not answer hello after Close: %v", id, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close holder connection: %v", err)
	}
	history := historyOf(t, path, id)
	if history.State != "interrupted" {
		t.Fatalf("running session state after Close = %q, want interrupted", history.State)
	}
	reason := ""
	for _, fields := range changedFields(t, history) {
		if fields["reason"] != "" {
			reason = fields["reason"]
		}
	}
	if want := "orreryd stopped during the turn"; reason != want {
		t.Fatalf("running session reason after Close = %q, want %q", reason, want)
	}
}

func TestCloseWithoutHolderScopeEndsSession(t *testing.T) {
	m, path := newHolderManager(t, "", "")
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block and the turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == "done"
	})
	pid := fakePID(t, dir)

	m.Close()

	requireGone(t, pid)
	if got := historyOf(t, path, id).State; got != "done" {
		t.Fatalf("session state after Close = %q, want done", got)
	}
}
