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

// testLimit is the Limit the limit tests run under. A command that finishes on its own finishes well inside it.
const testLimit = 500 * time.Millisecond

// ignoreTermScript is the command the kill tests run under sh. It ignores SIGTERM, prints a ready line once its trap is
// set, then waits on its stdin, so only the limit can end it.
const ignoreTermScript = `trap '' TERM; echo "ready $$"; read -r line`

// rerunScript returns the command the rerun tests run under sh. Under grant-term it prints a ready line, then exits 0
// when SIGTERM stops it. Under any other grant it sleeps for seconds, then exits 3.
func rerunScript(seconds string) string {
	return `case "$ORRERY_GRANT" in
grant-term) trap 'exit 0' TERM; echo "ready $$"; read -r line ;;
*) sleep ` + seconds + `; exit 3 ;;
esac`
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

// stopThenRerun runs rerunScript(seconds) under grant-term through a Pools that stops that run once it has held for
// half of testLimit. The rerun is granted grant-limit-rerun at once. It returns RunWithGrant's status.
func stopThenRerun(t *testing.T, seconds string) int {
	t.Helper()
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-term", ready), rejoinStep("grant-limit-rerun")}}
	socket := serveStopPools(t, pools)
	r, w := stdoutPipe(t)
	cfg := limitConfig(t, socket, rerunScript(seconds), w, io.Discard, stdinPipe(t))
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	time.Sleep(testLimit / 2)
	close(ready)
	return awaitStatus(t, status)
}

// TestRunLimitKillsCommandPastLimit: a sleep that runs past its Limit is SIGKILLed. Its process is gone, RunWithGrant
// returns ExitLimit, and stderr names the limit.
func TestRunLimitKillsCommandPastLimit(t *testing.T) {
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-limit", poolMembers("builds", "m1"))))
	r, w := stdoutPipe(t)
	var stderr bytes.Buffer
	cfg := runConfig(t, socket, "sleep", w, &stderr)
	cfg.Limit = testLimit
	status := runAsync(testContext(t), cfg)
	pid := readyPID(t, awaitReady(t, r))
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Fatalf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
	waitGone(t, pid)
	if got := stderr.String(); got != limitLine() {
		t.Errorf("stderr = %q, want %q", got, limitLine())
	}
}

// TestRunLimitLetsCommandEndWithinLimit: a command that ends inside its Limit returns its own status, not ExitLimit.
func TestRunLimitLetsCommandEndWithinLimit(t *testing.T) {
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-quick", poolMembers("builds", "m1"))))
	var stderr bytes.Buffer
	cfg := limitConfig(t, socket, "exit 3", io.Discard, &stderr, nil)
	if got := awaitStatus(t, runAsync(testContext(t), cfg)); got != 3 {
		t.Errorf("RunWithGrant = %d, want the command's status 3", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the command ended within its limit", stderr.String())
	}
}

// TestRunLimitIgnoresQueueTime: the Limit counts from the start of CMD. A grant that comes after Limit has passed still
// lets a quick command succeed.
func TestRunLimitIgnoresQueueTime(t *testing.T) {
	gate := make(chan struct{})
	time.AfterFunc(2*testLimit, func() { close(gate) })
	granted := grantedWith("grant-late", poolMembers("builds", "m1"))
	socket := runServe(t, newPoolsFakeSteps(acquireStep{
		gate:   gate,
		events: []*v1.AcquireResponse{granted},
		hold:   true,
	}))
	var stderr bytes.Buffer
	cfg := limitConfig(t, socket, "exit 0", io.Discard, &stderr, nil)
	if got := awaitStatus(t, runAsync(testContext(t), cfg)); got != 0 {
		t.Errorf("RunWithGrant = %d, want the command's status 0", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want nothing: the queue time did not count", stderr.String())
	}
}

// TestRunLimitRerunGetsFreshLimit: the rerun after a stop counts from its own start. The first run and the rerun
// together run past testLimit, yet each stays inside it, so the rerun finishes with its own status.
func TestRunLimitRerunGetsFreshLimit(t *testing.T) {
	if got := stopThenRerun(t, "0.35"); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
}

// TestRunLimitRerunIsLimited: the rerun after a stop gets a Limit of its own, so a rerun that runs past it is killed.
func TestRunLimitRerunIsLimited(t *testing.T) {
	if got := stopThenRerun(t, "5"); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}

// TestRunLimitKillsStoppedCommandPastLimit: a stopped command that ignores SIGTERM does not run on past its Limit while
// its stop grace runs. The limit SIGKILLs it, and the run is not rerun.
func TestRunLimitKillsStoppedCommandPastLimit(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{stopStep("grant-stubborn", ready)}}
	socket := serveStopPools(t, pools)
	r, w := stdoutPipe(t)
	var stderr bytes.Buffer
	cfg := stopConfig(t, socket, w, &stderr, stdinPipe(t))
	cfg.Limit = testLimit
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	close(ready)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
	if n := len(pools.received()); n != 1 {
		t.Errorf("Acquire called %d times, want 1: a run the limit killed is not rerun", n)
	}
}

// TestRunLimitKillsCancelledCommandPastLimit: a command that ignores the SIGTERM sent when its context ends is
// SIGKILLed once its Limit passes, and RunWithGrant returns ExitLimit.
func TestRunLimitKillsCancelledCommandPastLimit(t *testing.T) {
	socket := runServe(t, newPoolsFake(nil, true, grantedWith("grant-cancel", poolMembers("builds", "m1"))))
	r, w := stdoutPipe(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := limitConfig(t, socket, ignoreTermScript, w, io.Discard, stdinPipe(t))
	status := runAsync(ctx, cfg)
	awaitReady(t, r)
	cancel()
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}

// TestRunLimitKillsRefusedCommandPastLimit: a command whose grant orreryd refuses to reclaim, and which ignores the
// SIGTERM that follows, is SIGKILLed once its Limit passes, and RunWithGrant returns ExitLimit.
func TestRunLimitKillsRefusedCommandPastLimit(t *testing.T) {
	ready := make(chan struct{})
	pools := newPoolsFakeSteps(grantThenBreak("grant-refused"), refusedAfter(ready, connect.CodeNotFound))
	socket := runServe(t, pools)
	r, w := stdoutPipe(t)
	cfg := limitConfig(t, socket, ignoreTermScript, w, io.Discard, stdinPipe(t))
	status := runAsync(testContext(t), cfg)
	awaitReady(t, r)
	close(ready)
	if got := awaitStatus(t, status); got != cli.ExitLimit {
		t.Errorf("RunWithGrant = %d, want ExitLimit %d", got, cli.ExitLimit)
	}
}
