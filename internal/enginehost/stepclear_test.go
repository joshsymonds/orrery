package enginehost

import (
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
)

// serveStopSpy replaces the socket of the live holder in dir, whose processes pids records, with one that answers each
// hello as the holder would and then answers nothing, so an attach and a stop over it both go unanswered. It returns a
// channel that is closed when a stop frame arrives while the holder's process still runs and has not begun to exit
// (holderLive): that stop asks a live holder that gave no answer. A stop that arrives once the holder's process was
// killed is the clear of a gone holder; its connection is closed, as a gone holder's refused connection would end it.
func serveStopSpy(t *testing.T, dir string, pids holder.Pids) <-chan struct{} {
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
	spy := stopSpy{
		lockPath: filepath.Join(dir, holder.LockName),
		pids:     pids,
		ask:      sync.OnceFunc(func() { close(asked) }),
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
			go spy.serve(conn)
		}
	}()
	return asked
}

// stopSpy judges each stop frame that reaches serveStopSpy's socket. lockPath is the holder's session lock, pids the
// holder's pids record, and ask is called for a stop that reaches a holder whose process is live (holderLive).
type stopSpy struct {
	lockPath string
	pids     holder.Pids
	ask      func()
}

// serve answers the hello on conn, then judges the next frame when it is a stop. The holder's process decides it, not
// the session lock: a fork anywhere in this test binary copies whatever lock file is open at that moment, and the copy
// keeps the lock held until the forked child execs, after the holder has ended and the file it copied is closed. So the
// spy holds a lock of its own across the judgment whenever the lock is free, as such a copy would, and the judgment still
// finds the holder gone. A holder killed just before the stop has freed its lock while it exits, before /proc shows it a
// zombie, so the judgment counts an exiting holder gone as well.
func (s stopSpy) serve(conn net.Conn) {
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
	copied, lockErr := holder.Lock(s.lockPath)
	live, liveErr := holderLive(s.pids)
	if liveErr != nil || live {
		s.ask()
	}
	if lockErr == nil {
		_ = copied.Close()
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
	asked := serveStopSpy(t, hdir, pids)
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
	requireStepsHeld(t, restarted, 0)
	requireUndrained(t, rig, sink, obs)
}
