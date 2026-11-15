package enginehost

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// TestStopThenDrainKillsAHolderThatGaveNoAnswerWithoutAskingAgain hands stopThenDrain a holder whose attach failed for
// want of an answer. The holder's socket is replaced by one that takes a connection and never answers, so a stop asked
// over it would wait out its whole bound. stopThenDrain must not dial it: it kills the holder and its child from the
// pids record, then drains the session.
func TestStopThenDrainKillsAHolderThatGaveNoAnswerWithoutAskingAgain(t *testing.T) {
	t.Parallel()
	rig := newCrashRig(t, stalledTextTrigger(), false)
	dir := t.TempDir()
	id, err := rig.manager.Start(t.Context(), StartRequest{Dir: dir, Prompt: "STALL"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, engineOf(rig.manager), "the store refuses the batch of fake stalling 1", rig.crash.isRefusing)
	pid := fakePID(t, dir)
	hdir := holderDir(t, rig.root, id)
	pids := holderPids(t, hdir)
	rig.manager.Close()

	sock, err := holder.SocketPath(hdir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if err = os.Remove(sock); err != nil {
		t.Fatalf("remove the holder's socket: %v", err)
	}
	silent, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen in place of the holder: %v", err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	// A dial while the holder still holds its session lock asks a live holder to stop. Once the lock is free the holder
	// has ended, and a dial is the clear of a gone holder, which a refused connection answers as well as this one.
	asked := make(chan struct{})
	lockPath := filepath.Join(hdir, holder.LockName)
	go func() {
		for {
			conn, acceptErr := silent.Accept()
			if acceptErr != nil {
				return
			}
			lock, lockErr := holder.Lock(lockPath)
			if errors.Is(lockErr, holder.ErrLocked) {
				close(asked)
				t.Cleanup(func() { _ = conn.Close() })
				return
			}
			if lockErr == nil {
				_ = lock.Close()
			}
			_ = conn.Close()
		}
	}()

	rows, err := rig.store.EngineSessions(t.Context())
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	var found bool
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		found = true
		writer := NewWriter(WriterConfig{Applier: rig.store, Tick: 5 * time.Millisecond})
		startRun(t, writer)
		restarted := newStateManager(t, rig.store, writer, &fakeSink{}, fakeSystemdRunPath(t), rig.root, nil)
		noAnswer := fmt.Errorf("%w: %w", holder.ErrUnattachable, holder.ErrNoAnswer)
		drained := make(chan bool, 1)
		go func() { drained <- restarted.stopThenDrain(t.Context(), row, hdir, noAnswer) }()
		select {
		case <-asked:
			t.Fatal("stopThenDrain asked a holder to stop that had already given no answer")
		case ok := <-drained:
			if !ok {
				t.Fatal("stopThenDrain did not settle the row by draining it")
			}
		}
	}
	if !found {
		t.Fatalf("no engine row for session %s", id)
	}
	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
}
