package holder

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	// gateTestLimit is the gate limit a test gives its child on the fake clock.
	gateTestLimit = time.Minute
	// gateTestTail is the tail each stream of a gate keeps by default, in bytes.
	gateTestTail = 8 << 10
	// gateTestStdoutEnd and gateTestStderrEnd end the output a trim test writes to each stream, so the kept tails are
	// checkable.
	gateTestStdoutEnd = "stdout-end\n"
	gateTestStderrEnd = "stderr-end\n"
)

// startGateChild starts sh running script as a gate child on clock, with gateTestLimit and the default tail, and
// returns it with its spool.
func startGateChild(t *testing.T, script string, clock Clock) (*Child, *Spool) {
	t.Helper()
	return startGateChildTail(t, script, clock, 0)
}

// startGateChildTail is startGateChild with GateTail set to tail, where zero takes the default.
func startGateChildTail(t *testing.T, script string, clock Clock, tail int) (*Child, *Spool) {
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
		GateTail:  tail,
		Clock:     clock,
	})
	return child, spool
}

func TestGateChildKeepsStdoutAndStderrApart(t *testing.T) {
	t.Parallel()

	child, spool := startGateChild(t, "echo one; echo two >&2; echo three; echo four >&2", newFakeClock())
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	if want := "one\nthree\n"; exit.Stdout != want {
		t.Errorf("Stdout = %q, want %q", exit.Stdout, want)
	}
	if want := "two\nfour\n"; exit.Stderr != want {
		t.Errorf("Stderr = %q, want %q", exit.Stderr, want)
	}
	if spool.End() != 0 || exit.StdoutEnd != 0 {
		t.Errorf("spool holds %d bytes and StdoutEnd = %d, want nothing spooled", spool.End(), exit.StdoutEnd)
	}
}

func TestGateChildTailsKeepNewestEightKiBOfEachStream(t *testing.T) {
	t.Parallel()

	// 20 KiB of each stream, then a marker: each tail keeps the newest 8 KiB, which ends with its own marker.
	script := `head -c 20480 /dev/zero | tr '\000' a; echo stdout-end; ` +
		`head -c 20480 /dev/zero | tr '\000' b >&2; echo stderr-end >&2`
	child, spool := startGateChild(t, script, newFakeClock())
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	if want := strings.Repeat("a", gateTestTail-len(gateTestStdoutEnd)) + gateTestStdoutEnd; exit.Stdout != want {
		t.Errorf("Stdout holds %d bytes, want the newest %d: it ends %q", len(exit.Stdout), gateTestTail,
			exit.Stdout[max(0, len(exit.Stdout)-16):])
	}
	if want := strings.Repeat("b", gateTestTail-len(gateTestStderrEnd)) + gateTestStderrEnd; exit.Stderr != want {
		t.Errorf("Stderr holds %d bytes, want the newest %d: it ends %q", len(exit.Stderr), gateTestTail,
			exit.Stderr[max(0, len(exit.Stderr)-16):])
	}
	if spool.End() != 0 {
		t.Errorf("spool holds %d bytes, want nothing spooled", spool.End())
	}
}

func TestGateChildGateTailHoldsThatManyBytesOfEachStream(t *testing.T) {
	t.Parallel()

	const tail = 100
	script := `head -c 300 /dev/zero | tr '\000' a; echo stdout-end; ` +
		`head -c 300 /dev/zero | tr '\000' b >&2; echo stderr-end >&2`
	child, _ := startGateChildTail(t, script, newFakeClock(), tail)
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	stdout := strings.Repeat("a", 300) + gateTestStdoutEnd
	if want := stdout[len(stdout)-tail:]; exit.Stdout != want {
		t.Errorf("Stdout = %q, want the newest %d bytes %q", exit.Stdout, tail, want)
	}
	stderr := strings.Repeat("b", 300) + gateTestStderrEnd
	if want := stderr[len(stderr)-tail:]; exit.Stderr != want {
		t.Errorf("Stderr = %q, want the newest %d bytes %q", exit.Stderr, tail, want)
	}
}

// TestGateChildKeepsTheKeyLinesOfTheWholeOutput: a gate child's exit record carries the key lines of its stdout and its
// stderr, read from the whole of each stream, the line stdout ends on without a newline included, although each tail
// keeps only its newest 8 KiB and has cut the early lines off.
func TestGateChildKeepsTheKeyLinesOfTheWholeOutput(t *testing.T) {
	t.Parallel()

	const remote = "gate-remote: running just check here (no box; load 9.00 8.00 7.00, nproc 4)"
	script := `echo '` + remote + `' >&2; head -c 20480 /dev/zero | tr '\000' b >&2; echo >&2; ` +
		`echo stderr-end >&2; echo '--- FAIL: TestX (0.10s)'; head -c 20480 /dev/zero | tr '\000' a; echo; ` +
		`printf 'FAIL\tgithub.com/x/y'`
	child, _ := startGateChild(t, script, newFakeClock())
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	for _, want := range []string{remote, "--- FAIL: TestX (0.10s)", "FAIL\tgithub.com/x/y"} {
		if !slices.Contains(exit.KeyLines, want) {
			t.Errorf("KeyLines = %q, want it to hold %q", exit.KeyLines, want)
		}
	}
	if len(exit.KeyLines) != 3 {
		t.Errorf("KeyLines = %q, want only the three key lines", exit.KeyLines)
	}
	if strings.Contains(exit.Stderr, "gate-remote") || strings.Contains(exit.Stdout, "--- FAIL") {
		t.Errorf("tails hold the early key lines, want them cut off: stderr starts %q, stdout starts %q",
			exit.Stderr[:16], exit.Stdout[:16])
	}
}

// TestChildThatIsNoGateKeepsNoKeyLines: a child that is no gate keeps no key lines in its exit record, whatever it
// writes.
func TestChildThatIsNoGateKeepsNoKeyLines(t *testing.T) {
	t.Parallel()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startChildUnderCleanup(t, spool, ChildConfig{
		Path: sh, Args: []string{"-c", "echo '--- FAIL: TestX (0.10s)'; echo FAIL >&2"}, Dir: t.TempDir(),
		Clock: newFakeClock(),
	})
	awaitChildDone(t, child)
	if exit := child.Exit(ReasonExited); exit.KeyLines != nil {
		t.Errorf("KeyLines = %q, want none for a child that is no gate", exit.KeyLines)
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
	if want := "stdin: eof\n"; exit.Stdout != want {
		t.Errorf("Stdout = %q, want %q", exit.Stdout, want)
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
