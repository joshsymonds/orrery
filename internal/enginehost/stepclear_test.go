package enginehost

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// serveStopSpy replaces the socket of the live holder in dir with one that answers each hello as the holder would and
// then answers nothing, so an attach and a stop over it both go unanswered. It returns a channel that is closed when a
// stop frame arrives while the holder still holds its session lock: that stop asks a live holder that gave no answer.
// A stop that arrives once the lock is free is the clear of a gone holder; its connection is closed, as a gone holder's
// refused connection would end it.
func serveStopSpy(t *testing.T, dir string) <-chan struct{} {
	t.Helper()
	sock, err := holder.SocketPath(dir)
	if err != nil {
		t.Fatalf("SocketPath: %v", err)
	}
	if err = os.Remove(sock); err != nil {
		t.Fatalf("remove the holder's socket: %v", err)
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen in place of the holder: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	asked := make(chan struct{})
	lockPath := filepath.Join(dir, holder.LockName)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go spyOnStop(conn, lockPath, asked)
		}
	}()
	return asked
}

// spyOnStop answers the hello on conn, then closes asked when the next frame is a stop and the lock at lockPath is held.
func spyOnStop(conn net.Conn, lockPath string, asked chan struct{}) {
	frames := holder.NewFrameReader(conn)
	if hello, err := frames.Read(); err != nil || hello.Type != holder.TypeHello {
		return
	}
	answer := holder.Frame{Type: holder.TypeHello, Version: holder.WireVersion, Session: stepA}
	if err := holder.WriteFrame(conn, answer); err != nil {
		return
	}
	next, err := frames.Read()
	if err != nil || next.Type != holder.TypeStop {
		return
	}
	lock, lockErr := holder.Lock(lockPath)
	if errors.Is(lockErr, holder.ErrLocked) {
		close(asked)
		return
	}
	if lockErr == nil {
		_ = lock.Close()
	}
	_ = conn.Close()
}

// TestReadoptStepThatGaveNoAnswerIsKilledThenClearedWithoutAsking restarts over a live step holder that answers the
// hello and then nothing, so its re-adoption fails for want of an answer. Reconcile must kill it before the clear, so
// the clear asks no live holder to stop and wait out the same silence.
func TestReadoptStepThatGaveNoAnswerIsKilledThenClearedWithoutAsking(t *testing.T) {
	t.Parallel()
	rig := newStepCrashRig(t, turnEndTrigger)
	dir := t.TempDir()
	startReplyStep(t, rig, dir)
	pid := fakePID(t, dir)
	hdir := holderDir(t, rig.root, stepA)
	pids := holderPids(t, hdir)

	restarted, sink := restartBeforeReconcile(t, rig)
	// The spy answers no attach, so the attach runs out of time however short its bound is.
	restarted.attachTimeout = time.Millisecond
	asked := serveStopSpy(t, hdir)
	obs := &stepObserver{}
	restarted.ExpectStep(stepA, obs)
	reconciled := make(chan error, 1)
	go func() { reconciled <- restarted.Reconcile(t.Context()) }()
	select {
	case <-asked:
		t.Fatal("Reconcile asked a step holder to stop that had already given no answer")
	case err := <-reconciled:
		if err != nil {
			t.Fatalf("Reconcile after the crash: %v", err)
		}
	}

	waitHolderEnded(t, pids)
	gateWaitGone(t, pid)
	requireDirGone(t, hdir)
	if restarted.StepLive(stepA) {
		t.Fatalf("step %s is live after the restart, though its holder gave no answer", stepA)
	}
	requireStepSlots(t, restarted, 0)
	requireUndrained(t, rig, sink, obs)
}
