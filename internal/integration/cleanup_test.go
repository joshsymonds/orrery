package integration_test

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/joshsymonds/orrery/internal/exectest"
)

// lateRunScript is a fake claude whose summary run starts a sleep of half a second, records the sleep's pid in
// late.pid beside it, and waits for the sleep before it replies. The sleep is the run's own child, so a signal that
// reaches only the run leaves it running.
const lateRunScript = `#!/bin/sh
D=$(dirname "$0")
cat > /dev/null
sleep 0.5 &
echo $! > "$D/late.pid.tmp" && mv "$D/late.pid.tmp" "$D/late.pid"
wait
printf 'summary: example summary\n'
`

// TestOrrerydLeftRunningEndsItsRunsAtCleanup ends with its orreryd running and a summary run in flight, as a test that
// never stops its daemon ends. The cleanup of the daemon stops orreryd, which ends its runs before it exits, so once
// the cleanup has run, the run's sleep is gone or dying (exectest.KilledOrGone): nothing orreryd started can write into
// the test's directories while they are removed. A cleanup that SIGKILLed orreryd would leave the sleep running. The check is the test's first cleanup, so it runs last, after the
// daemon's.
func TestOrrerydLeftRunningEndsItsRunsAtCleanup(t *testing.T) {
	t.Parallel()
	var sleep int
	t.Cleanup(func() {
		if sleep == 0 {
			return
		}
		if !exectest.KilledOrGone(t, sleep) {
			_ = syscall.Kill(sleep, syscall.SIGKILL)
			t.Errorf("the summary run's sleep %d outlived the daemon's cleanup", sleep)
		}
	})
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, _, dir := startSummaryDaemon(t, name, lateRunScript)
	session := startClaudeSession(t, client, dir, pane, dir)
	session.submitPrompt(t)
	sleep = waitForPID(t, daemon, filepath.Join(dir, "late.pid"))
}
