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
	"github.com/joshsymonds/orrery/internal/waittest"
)

// stubbornTermed and stubbornRelease are the files, in a stubborn claude's working directory, that it makes when the
// stop's SIGTERM reaches it and that it waits for before it exits.
const (
	stubbornTermed  = "stubborn.termed"
	stubbornRelease = "stubborn.release"
)

// stubbornClaudePath returns the path of a claude that runs the fake claude as its child and outlives a stop until the
// test releases it: on SIGTERM it makes stubbornTermed and exits only once stubbornRelease exists. The holder that stops
// it waits for it to exit, so the stop of the session lasts until the release. The script is written into a directory
// of the test.
func stubbornClaudePath(t *testing.T) string {
	t.Helper()
	quoted := "'" + strings.ReplaceAll(fakeClaudePath(t), "'", `'\''`) + "'"
	script := fmt.Sprintf("#!/bin/sh\n"+
		"trap ': > %[2]s; while [ ! -e %[3]s ]; do sleep 0.01; done; exit 143' TERM\n"+
		"exec 3<&0\n"+
		"%[1]s \"$@\" <&3 3<&- &\n"+
		"wait $!\n", quoted, stubbornTermed, stubbornRelease)
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
// session is retired at the turn end and its stop starts. The stubborn claude outlives that stop's SIGTERM until the test
// releases it, so the holder runs on. The hold then opens, so the pump ends at once. awaitStopping must still wait for
// the stop: it has not returned while the claude runs on, and when it returns, the old holder has ended and its
// directory is gone. A Say that waits then starts a new holder in the same directory, which stays and answers it.
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
	waitUntil(t, engineOf(first, restarted), "the turn end to retire session "+id, func() bool {
		return !slices.Contains(restarted.Live(), id)
	})
	hold.open()
	stopped := make(chan error, 1)
	go func() { stopped <- restarted.awaitStopping(t.Context(), id) }()
	ended, stopResult := waittest.Closed(stopped)
	termed := filepath.Join(dir, stubbornTermed)
	waittest.Until(t, ended, "the stop's SIGTERM to the old holder's claude", func() bool {
		_, statErr := os.Stat(termed)
		return statErr == nil
	})
	select {
	case <-ended:
		t.Fatalf("awaitStopping returned %v while the old holder's claude still ran", <-stopResult)
	default:
	}
	if err := os.WriteFile(filepath.Join(dir, stubbornRelease), nil, 0o600); err != nil {
		t.Fatalf("release the stubborn claude: %v", err)
	}
	if err := <-stopResult; err != nil {
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
	waitHistory(
		t,
		engineOf(first, restarted),
		path,
		id,
		"the reply to the Say that waited",
		func(h store.EngineHistory) bool {
			return h.State == "done" && hasBlock(t, h, "fake reply 2")
		},
	)
	requireDirPresent(t, oldDir)
	if starts := fakeStarts(t, dir); len(starts) != 2 || !strings.Contains(starts[1], "--resume session_1") {
		t.Fatalf("fake claude starts = %q, want a second with --resume session_1", starts)
	}
}
