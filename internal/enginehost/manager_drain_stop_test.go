package enginehost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// stubbornClaudePath returns the path of a claude that ignores SIGTERM and then runs the fake claude. The ignored signal
// survives the exec, so the holder that stops this claude waits out the child's grace before it kills it, and the stop
// of the session takes that long. The script is written into a directory of the test.
func stubbornClaudePath(t *testing.T) string {
	t.Helper()
	quoted := "'" + strings.ReplaceAll(fakeClaudePath(t), "'", `'\''`) + "'"
	script := fmt.Sprintf("#!/bin/sh\ntrap '' TERM\nexec %s \"$@\"\n", quoted)
	path := filepath.Join(t.TempDir(), "claude")
	exectest.WriteFile(t, path, []byte(script), 0o700)
	return path
}

// newClaudeManager returns a manager with one slot whose controller speaks holder wire version version, over writer and
// s, that runs claude for its sessions. Its sessions run their holders under the fake systemd-run in root. Close runs at
// the end of the test. The caller runs Reconcile.
func newClaudeManager(
	t *testing.T, s Store, writer *Writer, sink NoticeSink, root, claude string, version int,
) *Manager {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	m := NewManager(Config{
		Shell:         sh,
		Claude:        claude,
		Environ:       []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Cap:           1,
		Writer:        writer,
		Now:           fixedClock,
		Notifier:      sink,
		Store:         s,
		HolderScope:   fakeSystemdRunPath(t),
		StateDir:      root,
		HolderVersion: version,
	})
	t.Cleanup(m.Close)
	return m
}

// TestAwaitStoppingWaitsForRetiredHolderRemoval holds the turn end of a one-version-older holder in the store, so its
// session is retired at the turn end and its stop starts. The stubborn claude ignores that stop's SIGTERM, so the holder
// runs on for the child's grace. The hold then opens, so the pump ends at once. awaitStopping must still wait for the
// stop: when it returns, the old holder has ended and its directory is gone. A Say that waits then starts a new holder
// in the same directory, which stays and answers it.
func TestAwaitStoppingWaitsForRetiredHolderRemoval(t *testing.T) {
	t.Parallel()
	s, path := openStore(t)
	root := holderRoot(t)
	t.Cleanup(func() { clearHolderDirs(t, root) })
	claude := stubbornClaudePath(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	first := newClaudeManager(t, s, writer, &fakeSink{}, root, claude, holder.WireVersion)
	dir := t.TempDir()
	id, err := first.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	pid := fakePID(t, dir)
	pids := holderPids(t, holderDir(t, root, id))
	first.Close()

	// The hold opens in the test body's defer, which runs before the cleanups that stop the writer it blocks.
	hold := newTurnEndHold(s)
	defer hold.open()
	restartedWriter := NewWriter(WriterConfig{Applier: hold, Tick: 5 * time.Millisecond})
	startRun(t, restartedWriter)
	sink := &fakeSink{}
	restarted := newClaudeManager(t, s, restartedWriter, sink, root, claude, holder.WireVersion+1)
	if err := restarted.Reconcile(t.Context()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !slices.Contains(restarted.Live(), id) {
		t.Fatalf("session %s is not live after the restart", id)
	}

	releaseHeldTurn(t, dir)
	waitUntil(t, "the turn end to retire session "+id, func() bool {
		return !slices.Contains(restarted.Live(), id)
	})
	hold.open()
	if err := restarted.awaitStopping(t.Context(), id); err != nil {
		t.Fatalf("awaitStopping: %v", err)
	}

	oldDir := holderDir(t, root, id)
	_, err = os.Lstat(oldDir)
	if err == nil {
		t.Fatalf("holder dir %s is still present after awaitStopping, want it removed", oldDir)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat holder dir %s: %v", oldDir, err)
	}
	// The holder gives up its session lock as it exits, so its process may still be ending; the directory, which a new
	// holder takes, is gone already.
	waitHolderEnded(t, pids)
	if !gateProcessGone(pid) {
		t.Fatalf("fake claude %d is still running after awaitStopping, want it ended", pid)
	}

	if err := restarted.Say(t.Context(), SayRequest{ID: id, Text: "again"}); err != nil {
		t.Fatalf("Say after the stop: %v", err)
	}
	waitHistory(t, path, id, "the reply to the Say that waited", func(h store.EngineHistory) bool {
		return h.State == "done" && hasBlock(t, h, "fake reply 2")
	})
	requireDirPresent(t, oldDir)
	if starts := fakeStarts(t, dir); len(starts) != 2 || !strings.Contains(starts[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second with --resume session_1", starts)
	}
}
