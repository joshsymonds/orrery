package holder

import (
	"bytes"
	"testing"
	"time"
)

// idleSettle is how long a test watches a holder for an idle stop that must not come. An idle stop takes effect within
// it.
const idleSettle = 200 * time.Millisecond

// newIdleConfig returns a holder config whose timers run on a fake clock the test advances.
func newIdleConfig(t *testing.T) (Config, *fakeClock) {
	t.Helper()
	cfg := newHolderConfig(t)
	clock := newFakeClock()
	cfg.Clock = clock
	return cfg, clock
}

// requireIdleHolderRunning fails unless Run is still going and the child's process group pgid is alive once idleSettle
// has passed.
func requireIdleHolderRunning(t *testing.T, run *running, pgid int) {
	t.Helper()
	select {
	case <-run.done:
		t.Fatalf("holder returned %v before its idle stop", run.err)
	case <-time.After(idleSettle):
	}
	requireChildGroupAlive(t, pgid)
}

// requireIdleExit waits for Run to return nil, then checks the exit file records an idle stop of the child whose
// process group was pgid and which ended by signal.
func requireIdleExit(t *testing.T, run *running, cfg Config, pgid int, signal string) {
	t.Helper()
	if err := run.wait(t); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	exit := readExitFile(t, cfg.Dir)
	if exit.Reason != ReasonIdle || exit.Code != -1 || exit.Signal != signal {
		t.Errorf("exit = %+v, want reason %q, code -1, signal %q", exit, ReasonIdle, signal)
	}
	requireChildGroupGone(t, pgid)
	requireNoSocket(t, socketOf(t, cfg.Dir))
	assertDirNames(t, cfg.Dir, ExitName, LockName, segmentName(0))
}

func TestHolderIdleStopsChildAfterTimeoutWithNoController(t *testing.T) {
	t.Parallel()

	cfg, clock := newIdleConfig(t)
	run := startHolder(t.Context(), t, cfg)
	pid := awaitChildPid(t, cfg.Child.Dir)
	// A connection that only says hello is not a controller, so it holds nothing off.
	dialHolder(t, run, socketOf(t, cfg.Dir)).hello(WireVersion)

	clock.Advance(DefaultIdleTimeout - time.Nanosecond)
	requireIdleHolderRunning(t, run, pid)

	clock.Advance(time.Nanosecond)
	requireIdleExit(t, run, cfg, pid, childSignalTerm)
}

func TestHolderIdleIsHeldOffWhileAControllerIsAttached(t *testing.T) {
	t.Parallel()

	cfg, clock := newIdleConfig(t)
	run := startHolder(t.Context(), t, cfg)
	pid := awaitChildPid(t, cfg.Child.Dir)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	clock.Advance(2 * DefaultIdleTimeout)
	requireIdleHolderRunning(t, run, pid)
}

func TestHolderDetachRestartsTheIdleTimeoutInFull(t *testing.T) {
	t.Parallel()

	cfg, clock := newIdleConfig(t)
	run := startHolder(t.Context(), t, cfg)
	pid := awaitChildPid(t, cfg.Child.Dir)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)

	clock.Advance(DefaultIdleTimeout / 2)
	conn.detach()

	// The timeout armed at start falls due at DefaultIdleTimeout, inside the full timeout that starts at the detach. The
	// attach disarmed it, so its expiry must not stop the child.
	clock.Advance(DefaultIdleTimeout - time.Nanosecond)
	requireIdleHolderRunning(t, run, pid)

	clock.Advance(time.Nanosecond)
	requireIdleExit(t, run, cfg, pid, childSignalTerm)
}

func TestHolderIdleKillsATermproofChildOnlyAfterTheGrace(t *testing.T) {
	t.Parallel()

	cfg, clock := newIdleConfig(t)
	cfg.Child.Grace = childTestGrace
	run := startHolder(t.Context(), t, cfg)
	// Cleanups run in reverse order, so this advance runs before startHolder waits for the holder. It ends a child that
	// ignores SIGTERM. A test that fails before the idle stop would otherwise wait out the grace forever.
	t.Cleanup(func() { clock.Advance(time.Hour) })
	pid := awaitChildPid(t, cfg.Child.Dir)
	conn := dialHolder(t, run, socketOf(t, cfg.Dir))
	conn.hello(WireVersion)
	conn.attachAt(0)
	conn.sendWrite("p1", userLine("TERMPROOF"))
	var seen streamSeen
	conn.readUntil(&seen, func() bool { return bytes.Contains(seen.out, []byte(childStallingOne)) })
	conn.detach()

	clock.Advance(DefaultIdleTimeout)
	// The child ignores SIGTERM, so it runs on until the grace passes on the clock.
	clock.waitArmed(t, 1)
	requireIdleHolderRunning(t, run, pid)

	clock.Advance(childTestGrace - time.Nanosecond)
	requireIdleHolderRunning(t, run, pid)

	clock.Advance(time.Nanosecond)
	requireIdleExit(t, run, cfg, pid, childSignalKill)
}
