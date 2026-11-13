package holder

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	// childTestCap is small enough that one FLOOD run overruns it several times.
	childTestCap = 4096
	// childTestRead is the buffer the spool is read back through.
	childTestRead = 1024
	// childTestGrace is the grace a test gives the child on the fake clock.
	childTestGrace = 3 * time.Second
	// childSettle bounds how long a capped spool is watched for growth that must not come.
	childSettle = 200 * time.Millisecond
	// childDrainCap is far below the output of one DIE turn.
	childDrainCap = 1024

	childReplyOne    = "fake reply 1"
	childStallingOne = "fake stalling 1"
	childDyingOne    = "fake dying 1"
	childDiedStderr  = "fake died"
	childDiedCode    = 3
	childSignalTerm  = "terminated"
	childSignalKill  = "killed"
)

// fakeClaudePath is the absolute path of the fake claude that the engine host tests share.
func fakeClaudePath(t *testing.T) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "enginehost", "testdata", "fakeclaude.sh"))
	if err != nil {
		t.Fatalf("resolve fakeclaude.sh: %v", err)
	}
	return path
}

// startFakeChild starts the fake claude in a fresh working directory, spooling its stdout into spool.
func startFakeChild(t *testing.T, spool *Spool, cfg ChildConfig) *Child {
	t.Helper()
	cfg.Path = fakeClaudePath(t)
	cfg.Dir = t.TempDir()
	return startChildUnderCleanup(t, spool, cfg)
}

// startChildUnderCleanup starts the child cfg describes, spooling its stdout into spool. The test's cleanup stops it,
// advances a fake clock past any grace, and waits for it to be reaped, so no child outlives the test.
func startChildUnderCleanup(t *testing.T, spool *Spool, cfg ChildConfig) *Child {
	t.Helper()
	child, err := StartChild(cfg, spool)
	if err != nil {
		t.Fatalf("StartChild: %v", err)
	}
	t.Cleanup(func() {
		child.Stop()
		if fake, ok := cfg.Clock.(*fakeClock); ok {
			fake.Advance(time.Hour)
		}
		select {
		case <-child.Done():
		case <-time.After(waitArmedTimeout):
			t.Errorf("child not reaped within %v of Stop", waitArmedTimeout)
		}
	})
	return child
}

// childPrompt sends the fake claude a user prompt carrying word, which selects the fake's turn.
func childPrompt(t *testing.T, child *Child, word string) {
	t.Helper()
	if err := child.Write([]byte(`{"type":"user","text":"` + word + `"}` + "\n")); err != nil {
		t.Fatalf("Write prompt %s: %v", word, err)
	}
}

// childSpooled returns every byte the spool holds, from its first retained offset to its end.
func childSpooled(t *testing.T, spool *Spool) []byte {
	t.Helper()
	var out bytes.Buffer
	buf := make([]byte, childTestRead)
	for off := spool.Start(); off < spool.End(); {
		n, err := spool.ReadAt(buf, off)
		if err != nil {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
		out.Write(buf[:n])
		off += int64(n)
	}
	return out.Bytes()
}

// awaitChildSpool waits until ready reports true, failing the test after waitArmedTimeout. The change channel is taken
// before ready runs, so no change between the two checks is missed.
func awaitChildSpool(t *testing.T, spool *Spool, what string, ready func() bool) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for {
		changed := spool.Changed()
		if ready() {
			return
		}
		select {
		case <-changed:
		case <-timeout:
			t.Fatalf("spool never reached %s within %v; it holds %q", what, waitArmedTimeout, childSpooled(t, spool))
		}
	}
}

// awaitChildDone waits for the child to be reaped and its stdout spooled, failing the test after waitArmedTimeout.
func awaitChildDone(t *testing.T, child *Child) {
	t.Helper()
	select {
	case <-child.Done():
	case <-time.After(waitArmedTimeout):
		t.Fatalf("child not done within %v", waitArmedTimeout)
	}
}

// requireChildGroupGone fails the test when any process of process group pgid is still alive.
func requireChildGroupGone(t *testing.T, pgid int) {
	t.Helper()
	if err := syscall.Kill(-pgid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d: kill(0) = %v, want ESRCH", pgid, err)
	}
}

// readChildPid returns the pid that a test's shell wrote to the file name.
func readChildPid(t *testing.T, name string) int {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parse the pid in %s: %v", name, err)
	}
	return pid
}

// awaitPidGone waits until process pid no longer exists, failing the test after waitArmedTimeout.
func awaitPidGone(t *testing.T, pid int, what string) {
	t.Helper()
	timeout := time.After(waitArmedTimeout)
	for syscall.Kill(pid, 0) == nil {
		select {
		case <-timeout:
			t.Fatalf("%s: process %d still alive %v after Stop", what, pid, waitArmedTimeout)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// floodToCap sends FLOOD and waits until the spool holds exactly its cap. Then it watches the spool for a settle
// window, so the child has time to fill its pipe, and fails if the spool grows or the child finishes meanwhile.
func floodToCap(t *testing.T, spool *Spool, child *Child) {
	t.Helper()
	childPrompt(t, child, "FLOOD")
	awaitChildSpool(t, spool, "its cap", func() bool { return spool.End() == childTestCap })
	select {
	case <-spool.Changed():
		t.Fatalf("spool grew past its cap of %d bytes", childTestCap)
	case <-time.After(childSettle):
	}
	assertOpen(t, child.Done(), "child that is capped")
}

func TestChildPromptReplyLandsInSpool(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startFakeChild(t, spool, ChildConfig{})
	childPrompt(t, child, "hello")
	awaitChildSpool(t, spool, "the reply", func() bool {
		return bytes.Contains(childSpooled(t, spool), []byte(childReplyOne))
	})
}

func TestChildDeathGivesExitRecordWithStdoutEnd(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startFakeChild(t, spool, ChildConfig{})
	childPrompt(t, child, "DIE")
	awaitChildDone(t, child)
	exit := child.Exit(ReasonExited)
	if exit.Reason != ReasonExited {
		t.Errorf("Reason = %q, want %q", exit.Reason, ReasonExited)
	}
	if exit.Code != childDiedCode || exit.Signal != "" {
		t.Errorf("Code, Signal = %d, %q; want %d, empty", exit.Code, exit.Signal, childDiedCode)
	}
	if exit.Stderr != childDiedStderr {
		t.Errorf("Stderr = %q, want %q", exit.Stderr, childDiedStderr)
	}
	if exit.StdoutEnd != spool.End() {
		t.Errorf("StdoutEnd = %d, want the spool's End %d", exit.StdoutEnd, spool.End())
	}
	if !bytes.Contains(childSpooled(t, spool), []byte(childDyingOne)) {
		t.Errorf("spool does not hold the output the child wrote before it died")
	}
}

func TestChildStopEndsItsProcessGroup(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startFakeChild(t, spool, ChildConfig{Clock: newFakeClock()})
	childPrompt(t, child, "STALL")
	awaitChildSpool(t, spool, "the stalled block", func() bool {
		return bytes.Contains(childSpooled(t, spool), []byte(childStallingOne))
	})
	child.Stop()
	child.Stop()
	awaitChildDone(t, child)
	exit := child.Exit(ReasonStopped)
	if exit.Signal != childSignalTerm || exit.Code != -1 {
		t.Errorf("Code, Signal = %d, %q; want -1, %q", exit.Code, exit.Signal, childSignalTerm)
	}
	requireChildGroupGone(t, child.Pid())
}

func TestChildStopSignalsWholeGroup(t *testing.T) {
	t.Parallel()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	dir := t.TempDir()
	spool, _ := newTestSpool(t, SpoolConfig{})
	clock := newFakeClock()
	// The shell backgrounds two sleeps in the child's group. The first keeps the default SIGTERM, so SIGTERM to the
	// group ends it. The second is forked after the shell ignores SIGTERM, so only SIGKILL to the group ends it.
	// The fake then ignores SIGTERM too, so the leader survives until the grace passes.
	script := `sleep 1000 & echo $! > sleep.pid; trap '' TERM; (exec sleep 1000) & echo $! > proof.pid; exec "$0"`
	child := startChildUnderCleanup(t, spool, ChildConfig{
		Path:  sh,
		Args:  []string{"-c", script, fakeClaudePath(t)},
		Dir:   dir,
		Grace: childTestGrace,
		Clock: clock,
	})
	childPrompt(t, child, "TERMPROOF")
	awaitChildSpool(t, spool, "the stalled block", func() bool {
		return bytes.Contains(childSpooled(t, spool), []byte(childStallingOne))
	})
	sleepPid := readChildPid(t, filepath.Join(dir, "sleep.pid"))
	proofPid := readChildPid(t, filepath.Join(dir, "proof.pid"))
	child.Stop()
	awaitPidGone(t, sleepPid, "background sleep in the child's group")
	assertOpen(t, child.Done(), "child whose SIGTERM-proof leader is still running")
	if err := syscall.Kill(proofPid, 0); err != nil {
		t.Fatalf("SIGTERM-proof sleep %d gone before the grace passed: %v", proofPid, err)
	}
	clock.waitArmed(t, 1)
	clock.Advance(childTestGrace)
	awaitChildDone(t, child)
	exit := child.Exit(ReasonStopped)
	if exit.Signal != childSignalKill || exit.Code != -1 {
		t.Errorf("Code, Signal = %d, %q; want -1, %q", exit.Code, exit.Signal, childSignalKill)
	}
}

func TestChildStopSurvivesSIGTERMUntilGraceThenKills(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{})
	clock := newFakeClock()
	child := startFakeChild(t, spool, ChildConfig{Grace: childTestGrace, Clock: clock})
	childPrompt(t, child, "TERMPROOF")
	awaitChildSpool(t, spool, "the stalled block", func() bool {
		return bytes.Contains(childSpooled(t, spool), []byte(childStallingOne))
	})
	child.Stop()
	clock.waitArmed(t, 1)
	clock.Advance(childTestGrace - time.Nanosecond)
	assertOpen(t, child.Done(), "child that ignores SIGTERM before its grace passes")
	clock.Advance(time.Nanosecond)
	awaitChildDone(t, child)
	exit := child.Exit(ReasonStopped)
	if exit.Signal != childSignalKill || exit.Code != -1 {
		t.Errorf("Code, Signal = %d, %q; want -1, %q", exit.Code, exit.Signal, childSignalKill)
	}
}

func TestChildDefaultGraceAndClockKillAfterDefaultGrace(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{})
	child := startFakeChild(t, spool, ChildConfig{})
	childPrompt(t, child, "TERMPROOF")
	awaitChildSpool(t, spool, "the stalled block", func() bool {
		return bytes.Contains(childSpooled(t, spool), []byte(childStallingOne))
	})
	started := time.Now()
	child.Stop()
	select {
	case <-child.Done():
	case <-time.After(DefaultGrace + waitArmedTimeout):
		t.Fatalf("child not done within DefaultGrace %v plus %v", DefaultGrace, waitArmedTimeout)
	}
	if elapsed := time.Since(started); elapsed < DefaultGrace {
		t.Errorf("child killed after %v, before DefaultGrace %v", elapsed, DefaultGrace)
	}
	exit := child.Exit(ReasonStopped)
	if exit.Signal != childSignalKill {
		t.Errorf("Signal = %q, want %q", exit.Signal, childSignalKill)
	}
}

func TestChildFloodStopsAtCapUntilAck(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{CapBytes: childTestCap})
	child := startFakeChild(t, spool, ChildConfig{})
	floodToCap(t, spool, child)
	mustAck(t, spool, childTestCap, 0)
	awaitChildSpool(t, spool, "more output after the ack", func() bool {
		return spool.End() > childTestCap
	})
	child.Stop()
	awaitChildDone(t, child)
	exit := child.Exit(ReasonStopped)
	if exit.StdoutEnd != spool.End() {
		t.Errorf("StdoutEnd = %d, want the spool's End %d", exit.StdoutEnd, spool.End())
	}
}

func TestChildCappedDeathSpoolsWholeStdout(t *testing.T) {
	t.Parallel()

	// The cap is far below one DIE turn, so the child exits with most of its output still in the pipe.
	capped, _ := newTestSpool(t, SpoolConfig{CapBytes: childDrainCap})
	dying := startFakeChild(t, capped, ChildConfig{})
	childPrompt(t, dying, "DIE")
	awaitChildDone(t, dying)

	uncapped, _ := newTestSpool(t, SpoolConfig{})
	free := startFakeChild(t, uncapped, ChildConfig{})
	childPrompt(t, free, "DIE")
	awaitChildDone(t, free)

	got := childSpooled(t, capped)
	trimmed := bytes.TrimSuffix(got, []byte("\n"))
	lastLine := trimmed[bytes.LastIndexByte(trimmed, '\n')+1:]
	if !bytes.Contains(lastLine, []byte(childDyingOne)) {
		t.Fatalf("capped spool ends with %q, want the DIE turn's last stdout line holding %q", lastLine, childDyingOne)
	}
	if want := childSpooled(t, uncapped); !bytes.Equal(got, want) {
		t.Fatalf("capped spool holds %d bytes, want the %d bytes an uncapped run spools", len(got), len(want))
	}
}

func TestChildStdoutHolderOutlivingChildIsReadToEOF(t *testing.T) {
	t.Parallel()

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	// The leader writes 4 KiB past a 1 KiB cap and exits. A setsid grandchild outside the group keeps the stdout pipe
	// open and writes once more after the leader is reaped. Only a read on to EOF spools that last write. The leader
	// writes only once the grandchild has made the marker file, which it does after setsid has moved it out of the
	// group, so the group's SIGKILL at the leader's reaping cannot end the grandchild before its write.
	spool, _ := newTestSpool(t, SpoolConfig{CapBytes: childDrainCap})
	script := "setsid sh -c ': > out-of-group; sleep 0.5; echo late' 2>/dev/null & " +
		"while [ ! -e out-of-group ]; do sleep 0.01; done; head -c 4096 /dev/zero"
	child := startChildUnderCleanup(t, spool, ChildConfig{
		Path: sh,
		Args: []string{"-c", script},
		Dir:  t.TempDir(),
	})
	awaitChildDone(t, child)
	const late = "late\n"
	if end, want := spool.End(), int64(4096+len(late)); end != want {
		t.Fatalf("spool holds %d bytes once Done closes, want %d: the stdout holder's last write was lost", end, want)
	}
	if got := childSpooled(t, spool); !bytes.HasSuffix(got, []byte(late)) {
		t.Fatalf("spool ends with %q, want %q", got[max(0, len(got)-16):], late)
	}
}

func TestChildStopWhileCappedClosesDoneWithRestSpooled(t *testing.T) {
	t.Parallel()

	spool, _ := newTestSpool(t, SpoolConfig{CapBytes: childTestCap})
	child := startFakeChild(t, spool, ChildConfig{})
	floodToCap(t, spool, child)
	child.Stop()
	awaitChildDone(t, child)
	if end := spool.End(); end <= childTestCap {
		t.Errorf("spool holds %d bytes after Stop, want more than the cap %d: the pipe's rest was not spooled",
			end, childTestCap)
	}
	exit := child.Exit(ReasonStopped)
	if exit.Signal != childSignalTerm {
		t.Errorf("Signal = %q, want %q", exit.Signal, childSignalTerm)
	}
	if exit.StdoutEnd != spool.End() {
		t.Errorf("StdoutEnd = %d, want the spool's End %d", exit.StdoutEnd, spool.End())
	}
}
