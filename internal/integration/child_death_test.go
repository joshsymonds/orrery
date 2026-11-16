package integration_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/exectest"
)

// heldSummaryScript is a fake claude whose summary run records its pid in held.pid beside it, then becomes a sleep
// that outlives any test, so only a kill ends it.
const heldSummaryScript = `#!/bin/sh
D=$(dirname "$0")
echo $$ > "$D/held.pid.tmp" && mv "$D/held.pid.tmp" "$D/held.pid"
cat > /dev/null
exec sleep 1000
`

// TestOrrerydKilledTakesItsSummaryRun SIGKILLs an orreryd while its summary run is held, and requires the run gone
// once orreryd is reaped: a killed orreryd runs no cleanup, so only the run's parent-death signal can end it.
func TestOrrerydKilledTakesItsSummaryRun(t *testing.T) {
	t.Parallel()
	client, name := newServer(t)
	pane := startPane(t, client, "sleep 300")
	daemon, socket, dir := startSummaryDaemon(t, name, heldSummaryScript)
	transcript := filepath.Join(dir, "transcript.jsonl")
	writeFile(t, transcript, exampleTranscript, 0o600)

	sendHook(t, socket, pane, dir, map[string]any{
		"hook_event_name": "UserPromptSubmit",
		"transcript_path": transcript,
	})
	run := waitForPID(t, daemon, filepath.Join(dir, "held.pid"))
	t.Cleanup(func() { _ = syscall.Kill(run, syscall.SIGKILL) })

	daemon.asked.Store(true)
	if err := daemon.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("SIGKILL orreryd: %v", err)
	}
	<-daemon.done
	if !exectest.KilledOrGone(t, run) {
		t.Fatalf("summary run %d outlived its SIGKILLed orreryd", run)
	}
}

// waitForPID waits until a fake claude run of daemon has written its pid file at path, and returns the pid it holds.
// It has no deadline of its own, and fails at once when daemon exits first.
func waitForPID(t *testing.T, daemon *orreryProcess, path string) int {
	t.Helper()
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr != nil {
				t.Fatalf("pid file %s holds %q: %v", path, raw, convErr)
			}
			return pid
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("read %s: %v", path, err)
		}
		select {
		case <-daemon.done:
			t.Fatalf("%s exited before its run wrote %s; stderr %q", daemon.name, path, daemon.stderr.String())
		case <-ticker.C:
			stopIfFailed(t)
		}
	}
}
