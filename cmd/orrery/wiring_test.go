package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestUsageTextListsRunStatus lists run status on its own line in the usage text, with what it prints.
func TestUsageTextListsRunStatus(t *testing.T) {
	for _, line := range strings.Split(usageText, "\n") {
		if strings.HasPrefix(line, "  run status") && strings.HasSuffix(line, "print the pools orreryd runs") {
			return
		}
	}
	t.Errorf("usage text does not list run status with its description:\n%s", usageText)
}

// treePoolsUnavailable is the line the pools view shows when no orreryd Pools resolves.
const treePoolsUnavailable = "pools unavailable"

// treeTestTmux names a tmux server that is never started. Listing its panes fails, and the tree still runs its pools
// view, so the test reaches the Pools wiring without a live tmux.
const treeTestTmux = "orrery-tree-test-unstarted"

// syncBuffer is the tree's output. The program writes it while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the output.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns the output so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// treeResult is the exit code and stderr of one `orrery tree` run.
type treeResult struct {
	code   int
	stderr string
}

// startTree runs `orrery args... tree --client` in a goroutine, with getenv as its environment and keys read from a
// pipe. It returns the pipe's writer, the program's output, and the channel that receives the run's result. The pipe is
// closed at cleanup, which ends the program if the test fails first.
func startTree(t *testing.T, args []string, getenv func(string) string) (*os.File, *syncBuffer, <-chan treeResult) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Errorf("close key pipe: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Errorf("close key reader: %v", err)
		}
	})
	out := &syncBuffer{}
	result := make(chan treeResult, 1)
	go func() {
		var stderr bytes.Buffer
		code := run(append(args, "tree", "--client", "orrery-tree-test-tty"), reader, out, &stderr, getenv)
		result <- treeResult{code: code, stderr: stderr.String()}
	}()
	return writer, out, result
}

// treeKey writes key to the tree's input.
func treeKey(t *testing.T, writer *os.File, key string) {
	t.Helper()
	if _, err := writer.WriteString(key); err != nil {
		t.Fatalf("write key %q: %v", key, err)
	}
}

// treeAwait returns the result of the tree run, and fails the test when none arrives within serveTestWait.
func treeAwait(t *testing.T, result <-chan treeResult) treeResult {
	t.Helper()
	select {
	case res := <-result:
		return res
	case <-time.After(serveTestWait):
		t.Fatalf("orrery tree did not return within %v", serveTestWait)
		return treeResult{}
	}
}

// treeTestEnv returns an environment with a fresh home and state directory, and no socket variable.
func treeTestEnv(t *testing.T) func(string) string {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "XDG_STATE_HOME": t.TempDir()}
	return func(key string) string { return env[key] }
}

// TestTreePoolsReachDaemonSocket runs the tree with --socket naming a daemon socket whose session socket serves a fake
// orreryd Pools. Pressing p shows the pools view, which reads Pools.Status from that fake, and q ends the tree.
func TestTreePoolsReachDaemonSocket(t *testing.T) {
	fake := &servePools{status: &enginev1.PoolsStatusResponse{}}
	daemon := serveFakePools(t, fake)
	writer, _, result := startTree(t, []string{"--socket", daemon, "-L", treeTestTmux}, treeTestEnv(t))

	treeKey(t, writer, "p")
	deadline := time.After(serveTestWait)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !slices.Contains(fake.received(), "Status") {
		select {
		case <-deadline:
			t.Fatalf("the tree's pools view did not reach orreryd's Pools within %v; calls %q", serveTestWait,
				fake.received())
		case <-ticker.C:
		}
	}
	treeKey(t, writer, "q")
	if res := treeAwait(t, result); res.code != exitOK {
		t.Fatalf("orrery tree exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
}

// TestTreePoolsUnavailableWithoutSocket runs the tree with no socket named and none in the environment. Pressing p
// shows the pools view as unavailable, since no orreryd Pools resolves.
func TestTreePoolsUnavailableWithoutSocket(t *testing.T) {
	writer, out, result := startTree(t, []string{"-L", treeTestTmux}, treeTestEnv(t))

	treeKey(t, writer, "p")
	deadline := time.After(serveTestWait)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !strings.Contains(out.String(), treePoolsUnavailable) {
		select {
		case <-deadline:
			t.Fatalf("the tree did not show %q within %v", treePoolsUnavailable, serveTestWait)
		case <-ticker.C:
		}
	}
	treeKey(t, writer, "q")
	if res := treeAwait(t, result); res.code != exitOK {
		t.Fatalf("orrery tree exit status %d, want %d; stderr %q", res.code, exitOK, res.stderr)
	}
}
