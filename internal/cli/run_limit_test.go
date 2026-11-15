package cli_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// testLimit is the Limit the limit tests run under. It runs on the test's timers, so it never passes in real time.
const testLimit = 500 * time.Millisecond

// ignoreTermScript is the command the kill tests run under sh. It ignores SIGTERM, prints a ready line once its trap is
// set, then waits on its stdin, so only the limit can end it.
const ignoreTermScript = `trap '' TERM; echo "ready $$"; read -r line`

// termNoticeScript is the command the cancel test runs under sh. It prints a ready line, then prints term for each
// SIGTERM and runs on, waiting on its stdin, so only the limit can end it and the test sees when SIGTERM came.
const termNoticeScript = `trap 'echo term' TERM; echo "ready $$"; while :; do read -r line; done`

// rerunScript returns the command the rerun tests run under sh. Under grant-term it prints a ready line, then exits 0
// when SIGTERM stops it. Under any other grant it runs rerun.
func rerunScript(rerun string) string {
	return `case "$ORRERY_GRANT" in
grant-term) trap 'exit 0' TERM; echo "ready $$"; read -r line ;;
*) ` + rerun + ` ;;
esac`
}

// armedTimer is one timer a run armed through RunConfig.After: its duration, and the channel the test closes to fire it.
type armedTimer struct {
	d    time.Duration
	fire chan time.Time
}

// runTimers records each timer a run arms, so the test fires the limits and graces it means to, and no real time
// passes for any of them.
type runTimers struct {
	armed chan armedTimer
}

// newRunTimers makes cfg arm its timers on a runTimers it returns.
func newRunTimers(cfg *cli.RunConfig) *runTimers {
	timers := &runTimers{armed: make(chan armedTimer, 16)}
	cfg.After = func(d time.Duration) <-chan time.Time {
		timer := armedTimer{d: d, fire: make(chan time.Time)}
		timers.armed <- timer
		return timer.fire
	}
	return timers
}

// next returns the next timer of duration d the run arms. Timers of other durations it passes are left unfired.
func (timers *runTimers) next(d time.Duration) armedTimer {
	for {
		if timer := <-timers.armed; timer.d == d {
			return timer
		}
	}
}

// limitConfig returns the RunConfig that runs script under sh through the Pools at socket, with testLimit as its Limit.
func limitConfig(t *testing.T, socket, script string, stdout, stderr io.Writer, stdin io.Reader) cli.RunConfig {
	t.Helper()
	cfg := stopConfig(t, socket, stdout, stderr, stdin)
	cfg.Argv = []string{"sh", "-c", script}
	cfg.Limit = testLimit
	return cfg
}

// limitLine is the stderr line a run writes when its Limit kills CMD.
func limitLine() string {
	return "orrery run: CMD ran past its limit of " + testLimit.String() + " and was killed\n"
}

// testContext returns a context that ends when the test does.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// stopThenRerun runs rerunScript(rerun) under grant-term through a Pools that stops that run, and reruns it under
// grant-limit-rerun. Once the rerun has armed its own limit, it fires the first run's limit, which must no longer
// govern anything, and then the rerun's limit when fireRerun is set. It returns RunWithGrant's status.
func stopThenRerun(t *testing.T, rerun string, fireRerun bool) int {
	t.Helper()
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-term", ready), rejoinStep("grant-limit-rerun")}}
	socket := serveStopPools(t, pools)
	r, w := stdoutPipe(t)
	cfg := limitConfig(t, socket, rerunScript(rerun), w, io.Discard, stdinPipe(t))
	timers := newRunTimers(&cfg)
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	first := timers.next(testLimit)
	close(ready)
	second := timers.next(testLimit)
	close(first.fire)
	if fireRerun {
		close(second.fire)
	}
	return awaitStatus(t, status)
}

// TestRunLimitKillsCommandPastLimit: a sleep that runs past its Limit is SIGKILLed. Its process is gone, RunWithGrant
// returns ExitLimit, and stderr names the limit.
func TestRunLimitKillsCommandPastLimit(t *testing.T) {
	t.Parallel()
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-limit", poolMembers("builds", "m1"))))
	r, w := stdoutPipe(t)
	var stderr bytes.Buffer
	cfg := runConfig(t, socket, "sleep", w, &stderr)
	cfg.Limit = testLimit
	timers := newRunTimers(&cfg)
	status := runAsync(testContext(t), cfg)
	pid := readyPID(t, awaitReady(t, r))
	close(timers.next(testLimit).fire)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Fatalf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
	waitGone(t, pid)
	if got := stderr.String(); got != limitLine() {
		t.Errorf("stderr = %q, want %q", got, limitLine())
	}
}

// TestRunLimitLetsCommandEndWithinLimit: a command that ends before its Limit passes returns its own status, not
// ExitLimit.
func TestRunLimitLetsCommandEndWithinLimit(t *testing.T) {
	t.Parallel()
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-quick", poolMembers("builds", "m1"))))
	var stderr bytes.Buffer
	cfg := limitConfig(t, socket, "exit 3", io.Discard, &stderr, nil)
	newRunTimers(&cfg)
	if got := awaitStatus(t, runAsync(testContext(t), cfg)); got != 3 {
		t.Errorf("RunWithGrant = %d, want the command's status 3", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the command ended within its limit", stderr.String())
	}
}

// TestRunLimitIgnoresQueueTime: the Limit counts from the start of CMD, so time queued for a grant cannot count toward
// it. While the grant is held queued, no limit is armed; once it is granted, a quick command succeeds.
func TestRunLimitIgnoresQueueTime(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	granted := grantedWith("grant-late", poolMembers("builds", "m1"))
	pools := newPoolsFakeSteps(acquireStep{
		gate:   gate,
		events: []*v1.AcquireResponse{granted},
		hold:   true,
	})
	socket := runServe(t, pools)
	var stderr bytes.Buffer
	cfg := limitConfig(t, socket, "exit 0", io.Discard, &stderr, nil)
	timers := newRunTimers(&cfg)
	status := runAsync(testContext(t), cfg)
	for len(pools.received()) == 0 {
		time.Sleep(testRetry)
	}
	select {
	case timer := <-timers.armed:
		t.Fatalf("a %v timer was armed while the grant was queued", timer.d)
	default:
	}
	close(gate)
	if got := awaitStatus(t, status); got != 0 {
		t.Errorf("RunWithGrant = %d, want the command's status 0", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the queue time did not count", stderr.String())
	}
}

// TestRunLimitRerunGetsFreshLimit: the rerun after a stop counts from its own start. The first run's limit, fired once
// the rerun has armed its own, ends nothing, so the rerun finishes with its own status.
func TestRunLimitRerunGetsFreshLimit(t *testing.T) {
	t.Parallel()
	if got := stopThenRerun(t, "exit 3", false); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
}

// TestRunLimitRerunIsLimited: the rerun after a stop gets a Limit of its own, so a rerun that runs past it is killed.
func TestRunLimitRerunIsLimited(t *testing.T) {
	t.Parallel()
	if got := stopThenRerun(t, "read -r line", true); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}

// TestRunLimitKillsStoppedCommandPastLimit: a stopped command that ignores SIGTERM does not run on past its Limit while
// its stop grace runs. The limit SIGKILLs it, and the run is not rerun.
func TestRunLimitKillsStoppedCommandPastLimit(t *testing.T) {
	t.Parallel()
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-stubborn", ready)}}
	socket := serveStopPools(t, pools)
	r, w := stdoutPipe(t)
	var stderr bytes.Buffer
	cfg := stopConfig(t, socket, w, &stderr, stdinPipe(t))
	cfg.Limit = testLimit
	timers := newRunTimers(&cfg)
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	limit := timers.next(testLimit)
	close(ready)
	timers.next(stopGrace)
	close(limit.fire)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
	if n := len(pools.received()); n != 1 {
		t.Errorf("Acquire called %d times, want 1: a run the limit killed is not rerun", n)
	}
}

// TestRunLimitKillsCancelledCommandPastLimit: a command that runs on after the SIGTERM sent when its context ends is
// SIGKILLed once its Limit passes, and RunWithGrant returns ExitLimit.
func TestRunLimitKillsCancelledCommandPastLimit(t *testing.T) {
	t.Parallel()
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-cancel", poolMembers("builds", "m1"))))
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := limitConfig(t, socket, termNoticeScript, w, io.Discard, stdinPipe(t))
	timers := newRunTimers(&cfg)
	status := runAsync(ctx, cfg)
	nextLine(t, lines)
	limit := timers.next(testLimit)
	cancel()
	// The cancel's SIGTERM has reached the command, which runs on, before the limit passes.
	if got := nextLine(t, lines); got != "term" {
		t.Fatalf("line after the cancel = %q, want term", got)
	}
	close(limit.fire)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}

// TestRunLimitKillsRefusedCommandPastLimit: a command whose grant orreryd refuses to reclaim, and which ignores the
// SIGTERM that follows, is SIGKILLed once its Limit passes during the stop grace, and RunWithGrant returns ExitLimit.
func TestRunLimitKillsRefusedCommandPastLimit(t *testing.T) {
	t.Parallel()
	ready := make(chan struct{})
	pools := newPoolsFakeSteps(grantThenBreak("grant-refused"), refusedAfter(ready, connect.CodeNotFound))
	socket := runServe(t, pools)
	r, w := stdoutPipe(t)
	cfg := limitConfig(t, socket, ignoreTermScript, w, io.Discard, stdinPipe(t))
	timers := newRunTimers(&cfg)
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	limit := timers.next(testLimit)
	close(ready)
	timers.next(stopGrace)
	close(limit.fire)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}
