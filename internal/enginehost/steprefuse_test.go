package enginehost

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// refusalGuard bounds each call the refusal tests make, so a call that blocks fails the test rather than hanging it.
const refusalGuard = 5 * time.Second

// refusalLog is a log buffer that several goroutines may write and the test may read.
type refusalLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *refusalLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *refusalLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// refusalFixture is a Steps over a real Manager, writer and store. Its worktrees directory is empty until a read-only
// step starts.
type refusalFixture struct {
	steps     *Steps
	writer    *Writer
	manager   *Manager
	log       *refusalLog
	worktrees string
}

// newRefusalFixture returns a fixture whose manager allows stepCap live steps. The step loops are canceled and the
// manager closed at cleanup.
func newRefusalFixture(t *testing.T, stepCap int) *refusalFixture {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	s, _ := openStore(t)
	log := &refusalLog{}
	logger := slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond, Logger: logger})
	startRun(t, writer)
	manager := NewManager(Config{
		Shell: sh, Claude: fakeClaudePath(t), Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap: 2, StepCap: stepCap, Writer: writer, Now: fixedClock, Notifier: &fakeSink{}, Store: s,
	})
	t.Cleanup(manager.Close)
	worktrees := t.TempDir()
	steps, err := NewSteps(StepsConfig{
		Sessions: manager, Store: s, Writer: writer,
		Locks:     []string{filepath.Join(t.TempDir(), "gate.lock")},
		Worktrees: worktrees, Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(func() {
		steps.mu.Lock()
		defer steps.mu.Unlock()
		for _, cancel := range steps.loops {
			cancel()
		}
	})
	return &refusalFixture{steps: steps, writer: writer, manager: manager, log: log, worktrees: worktrees}
}

// refusalStart runs Start for start and returns its id and error. It fails the test if Start has not returned in
// refusalGuard.
func refusalStart(t *testing.T, steps *Steps, start StepStart) (string, error) {
	t.Helper()
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := steps.Start(t.Context(), start)
		done <- result{id, err}
	}()
	select {
	case r := <-done:
		return r.id, r.err
	case <-time.After(refusalGuard):
		t.Fatalf("Start did not return within %v", refusalGuard)
		return "", nil
	}
}

// refusalCommitReturns checks that another session's Writer.Commit returns within refusalGuard.
func refusalCommitReturns(t *testing.T, writer *Writer) {
	t.Helper()
	const other = "cccccccccccccccccccccccccccccccc"
	done := make(chan error, 1)
	go func() {
		done <- writer.Commit(t.Context(), other, store.EngineAdd{ID: other, Dir: "/work", State: "working"})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("other session Commit: %v", err)
		}
	case <-time.After(refusalGuard):
		t.Fatalf("other session Commit did not return within %v", refusalGuard)
	}
}

// refusalWorktrees returns the names of the entries in dir.
func refusalWorktrees(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read worktrees: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// refusalCheck checks the state a refused start must leave: an empty id, no retry warning and another session's commit
// returning.
func refusalCheck(t *testing.T, f *refusalFixture, id string, err error) {
	t.Helper()
	if id != "" {
		t.Errorf("refused Start id = %q, want empty", id)
	}
	refusalCommitReturns(t, f.writer)
	if strings.Contains(f.log.String(), "ops kept for retry") {
		t.Errorf("refused Start led to a retried write:\n%s", f.log.String())
	}
	if err == nil {
		t.Fatal("refused Start returned no error")
	}
}

func TestStepRefusedCapReachedStartsNothing(t *testing.T) {
	f := newRefusalFixture(t, 1)
	repo := stepGitTestRepo(t)
	running := stepStartReadOnly(repo, "HEAD")
	running.Prompt = "HANG"
	firstID, err := refusalStart(t, f.steps, running)
	if err != nil || firstID == "" {
		t.Fatalf("first Start = %q, %v; want a running step", firstID, err)
	}

	id, err := refusalStart(t, f.steps, stepStartReadOnly(repo, "HEAD"))
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("second Start error = %v, want it to match %v", err, ErrCapReached)
	}
	refusalCheck(t, f, id, err)
	if names := refusalWorktrees(t, f.worktrees); len(names) != 1 || names[0] != firstID {
		t.Errorf("worktrees = %v, want only the running step's %s", names, firstID)
	}
}

func TestStepRefusedBadAgentStartsNothing(t *testing.T) {
	f := newRefusalFixture(t, 2)
	repo := stepGitTestRepo(t)
	start := stepStartReadOnly(repo, "HEAD")
	start.Agent = "-x"

	id, err := refusalStart(t, f.steps, start)
	if !errors.Is(err, ErrBadAgent) {
		t.Fatalf("Start error = %v, want it to match %v", err, ErrBadAgent)
	}
	refusalCheck(t, f, id, err)
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}

func TestStepRefusedAfterCloseStartsNothing(t *testing.T) {
	f := newRefusalFixture(t, 2)
	f.manager.Close()
	repo := stepGitTestRepo(t)

	id, err := refusalStart(t, f.steps, stepStartReadOnly(repo, "HEAD"))
	if !errors.Is(err, errManagerClosed) {
		t.Fatalf("Start error = %v, want it to match %v", err, errManagerClosed)
	}
	refusalCheck(t, f, id, err)
	if names := refusalWorktrees(t, f.worktrees); len(names) != 0 {
		t.Errorf("worktrees = %v, want none after a refused start", names)
	}
}
