package holder

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	// gateTestLimit is the gate limit a test gives its child on the fake clock.
	gateTestLimit = time.Minute
	// gateTestTail is the stderr tail a gate keeps, in bytes.
	gateTestTail = 8 << 10
	// gateTestMarker ends the output the trim test writes, so the kept tail is checkable.
	gateTestMarker = "gate-tail-end\n"
)

// startGateChild starts sh running script as a gate child on clock, with gateTestLimit, and returns it with its spool.
func startGateChild(t *testing.T, script string, clock Clock) (*Child, *Spool) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startChildUnderCleanup(t, spool, ChildConfig{
		Path:      sh,
		Args:      []string{"-c", script},
		Dir:       t.TempDir(),
		GateLimit: gateTestLimit,
		Clock:     clock,
	})
	return child, spool
}

func TestGateChildMergesStdoutAndStderrInWriteOrder(t *testing.T) {
	t.Parallel()

	// Both streams share one pipe, so the tail holds the lines in the order the child wrote them.
	child, spool := startGateChild(t, "echo one; echo two >&2; echo three; echo four >&2", newFakeClock())
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	if want := "one\ntwo\nthree\nfour\n"; exit.Stderr != want {
		t.Errorf("Stderr = %q, want %q", exit.Stderr, want)
	}
	if spool.End() != 0 || exit.StdoutEnd != 0 {
		t.Errorf("spool holds %d bytes and StdoutEnd = %d, want nothing spooled", spool.End(), exit.StdoutEnd)
	}
}

func TestGateChildTailKeepsNewestEightKiB(t *testing.T) {
	t.Parallel()

	// 20 KiB of stdout then one stderr line: the tail keeps the newest 8 KiB, which ends with that line.
	child, spool := startGateChild(t, `head -c 20480 /dev/zero | tr '\000' a; echo gate-tail-end >&2`, newFakeClock())
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	want := strings.Repeat("a", gateTestTail-len(gateTestMarker)) + gateTestMarker
	if exit.Stderr != want {
		t.Errorf("Stderr holds %d bytes, want the newest %d: it ends %q", len(exit.Stderr), gateTestTail,
			exit.Stderr[max(0, len(exit.Stderr)-16):])
	}
	if spool.End() != 0 {
		t.Errorf("spool holds %d bytes, want nothing spooled", spool.End())
	}
}

func TestGateChildStdinReadsEOFAndWriteFails(t *testing.T) {
	t.Parallel()

	script := `if read -r line; then echo "stdin: $line"; else echo "stdin: eof"; fi`
	child, _ := startGateChild(t, script, newFakeClock())
	if err := child.Write([]byte("hello\n")); err == nil {
		t.Error("Write to a gate child's stdin succeeded, want an error")
	}
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	if want := "stdin: eof\n"; exit.Stderr != want {
		t.Errorf("Stderr = %q, want %q", exit.Stderr, want)
	}
}

func TestGateChildPastLimitIsStoppedWithReasonLimit(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	child, _ := startGateChild(t, "exec sleep 1000", clock)
	clock.Advance(gateTestLimit - time.Nanosecond)
	assertOpen(t, child.Done(), "gate child before its limit has passed")
	clock.Advance(time.Nanosecond)
	awaitChildDone(t, child)
	exit := child.Exit(ReasonStopped)
	if exit.Reason != ReasonLimit {
		t.Errorf("Reason = %q, want %q whatever reason Exit is given", exit.Reason, ReasonLimit)
	}
	if exit.Signal != childSignalTerm || exit.Code != -1 {
		t.Errorf("Code, Signal = %d, %q; want -1, %q: the limit stops the child as Stop does", exit.Code, exit.Signal,
			childSignalTerm)
	}
}

func TestGateChildExitingBeforeLimitKeepsExitedAndCode(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	child, _ := startGateChild(t, "echo bye >&2; exit 3", clock)
	awaitChildDone(t, child)
	clock.Advance(gateTestLimit)
	exit := child.Exit(ReasonExited)
	if exit.Reason != ReasonExited || exit.Code != 3 {
		t.Errorf("Reason, Code = %q, %d; want %q, 3", exit.Reason, exit.Code, ReasonExited)
	}
	if want := "bye\n"; exit.Stderr != want {
		t.Errorf("Stderr = %q, want %q", exit.Stderr, want)
	}
}
