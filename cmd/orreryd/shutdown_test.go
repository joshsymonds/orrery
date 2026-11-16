package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/waittest"
)

// logBuffer is a log sink that a test reads while the code under test still writes to it.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns what has been written so far.
func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestShutdownPhaseLogsStartBeforeItsWait checks that a shutdown phase logs its start before it waits, and its end only
// once the wait returns. The phase is held until the test has read its start line, so a wait that never returns leaves
// that line, naming the phase, as the last line of the shutdown.
func TestShutdownPhaseLogsStartBeforeItsWait(t *testing.T) {
	var logs logBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		shutdownPhase(logger, "held phase", func() { <-release })
	}()
	waittest.Until(t, done, "the held phase's start line", func() bool {
		return strings.Contains(logs.String(), `phase="held phase" step=start`)
	})
	if strings.Contains(logs.String(), "step=done") {
		t.Fatalf("logs = %q, want no done line while the phase is held", logs.String())
	}
	close(release)
	<-done
	if !strings.Contains(logs.String(), `phase="held phase" step=done`) {
		t.Errorf("logs = %q, want the held phase's done line once it returned", logs.String())
	}
}

// heldShutdownWait closes entered, then blocks until release closes. It stands for a shutdown wait whose producer never
// ends, so the stacks a slow shutdown writes must name it.
func heldShutdownWait(entered chan<- struct{}, release <-chan struct{}) {
	close(entered)
	<-release
}

// TestWatchShutdownWritesStacksOfHeldWait checks that a shutdown still running when the watch's delay passes writes
// every goroutine's stack to a file in the directory, and logs its path. A shutdown phase is held in heldShutdownWait for
// the whole test, entered before the shutdown starts, so the stacks must name the wait, and the log must show the
// phase started and never finished. The delay is zero, so the write follows the shutdown's start at once.
func TestWatchShutdownWritesStacksOfHeldWait(t *testing.T) {
	var logs logBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	dir := filepath.Join(t.TempDir(), "traces")
	entered := make(chan struct{})
	release := make(chan struct{})
	held := make(chan struct{})
	go func() {
		defer close(held)
		shutdownPhase(logger, "held phase", func() { heldShutdownWait(entered, release) })
	}()
	defer func() {
		close(release)
		<-held
	}()
	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	end := watchShutdown(ctx, logger, dir, 0)
	defer end()
	cancel()
	waittest.Until(t, nil, "the stacks line", func() bool { return strings.Contains(logs.String(), shutdownStacksMsg) })
	if !strings.Contains(logs.String(), shutdownStartedMsg) {
		t.Errorf("logs = %q, want the shutdown's start line", logs.String())
	}
	files, err := filepath.Glob(filepath.Join(dir, shutdownStacksPrefix+"*.txt"))
	if err != nil || len(files) != 1 {
		t.Fatalf("stacks files = %v (%v), want one", files, err)
	}
	if !strings.Contains(logs.String(), "path="+files[0]) {
		t.Errorf("logs = %q, want the stacks file's path %s", logs.String(), files[0])
	}
	stacks, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read stacks: %v", err)
	}
	if !strings.Contains(string(stacks), "heldShutdownWait") {
		t.Errorf("stacks do not name the held wait heldShutdownWait:\n%s", stacks)
	}
	if !strings.Contains(logs.String(), `phase="held phase" step=start`) ||
		strings.Contains(logs.String(), `phase="held phase" step=done`) {
		t.Errorf("logs = %q, want the held phase started and not done", logs.String())
	}
}

// TestWatchShutdownEndedFirstWritesNoStacks checks that a shutdown that ends before the watch's delay passes writes no
// stacks: the watch logs the start, and ending it stops the write. The delay is an hour, far past the test's end.
func TestWatchShutdownEndedFirstWritesNoStacks(t *testing.T) {
	var logs logBuffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	end := watchShutdown(ctx, logger, dir, time.Hour)
	cancel()
	waittest.Until(t, nil, "the shutdown's start line", func() bool {
		return strings.Contains(logs.String(), shutdownStartedMsg)
	})
	end()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Errorf("stacks directory holds %v (%v), want nothing", entries, err)
	}
	if strings.Contains(logs.String(), shutdownStacksMsg) {
		t.Errorf("logs = %q, want no stacks line", logs.String())
	}
}
