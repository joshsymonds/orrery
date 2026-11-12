package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// lostGrantScript is the command the lost-grant tests run. Under grant-term it is stopScript's stoppable command, which
// prints a ready line once its trap is set and exits on SIGTERM. Under any other grant it prints the grant and the
// builds members it was granted, and exits 3.
const lostGrantScript = `case "$ORRERY_GRANT" in
grant-term) trap 'echo term; exit 0' TERM; echo "ready $$"; read -r line ;;
*) echo "rerun $ORRERY_GRANT $ORRERY_POOL_BUILDS"; exit 3 ;;
esac`

// lostHolderStep is the first Acquire of a lost-grant test: it grants grant-term to members of the builds pool, waits
// until ready closes, then ends the stream with no stop, as a stream cut does.
func lostHolderStep(ready <-chan struct{}, members ...string) scriptedStep {
	return scriptedStep{
		before: []*v1.AcquireResponse{grantedWith("grant-term", poolMembers("builds", members...))},
		after:  ready,
	}
}

// lostConfig returns the RunConfig that runs lostGrantScript through the Pools at socket.
func lostConfig(t *testing.T, socket string, stdout, stderr io.Writer, stdin io.Reader) cli.RunConfig {
	t.Helper()
	cfg := stopConfig(t, socket, stdout, stderr, stdin)
	cfg.Argv = []string{"sh", "-c", lostGrantScript}
	return cfg
}

// stderrFile returns a file for a run's stderr, as in production. A writer that is not a file makes exec copy CMD's
// stderr on a goroutine, which would write the same buffer as the run's own messages. The returned func closes the write
// end, waits for the copy to finish, and returns what was written.
func stderrFile(t *testing.T) (*os.File, func() string) {
	t.Helper()
	r, w := stdoutPipe(t)
	var buf bytes.Buffer
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(copied)
	}()
	return w, func() string {
		_ = w.Close()
		<-copied
		return buf.String()
	}
}

func TestRunLostGrantStopsCommandWhileReclaimWaits(t *testing.T) {
	ready := make(chan struct{})
	gate := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{
		lostHolderStep(ready, "m1"),
		{
			before: []*v1.AcquireResponse{queuedAt(1)},
			after:  gate,
			then:   []*v1.AcquireResponse{grantedWith("grant-rerun", poolMembers("builds", "m2"))},
			hold:   true,
		},
	}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	errW, stderr := stderrFile(t)
	status := runAsync(context.Background(), lostConfig(t, socket, w, errW, stdinR))
	pid := readyPID(t, nextLine(t, lines))
	close(ready)

	// The stream ends with no stop, and the reclaim queues at position 1. CMD must exit on SIGTERM while the reclaim
	// still waits, so the gate stays closed until CMD is gone.
	awaitStopRequests(t, pools, 2)
	waitGone(t, pid)
	close(gate)
	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	_ = w.Close()
	if got, want := remainingLines(lines), []string{"term", "rerun grant-rerun m2"}; !slices.Equal(got, want) {
		t.Errorf("output after the ready line = %q, want %q", got, want)
	}
	text := stderr()
	if !strings.Contains(text, "queued at position 1") {
		t.Errorf("stderr = %q, want the reclaim to write its queued position", text)
	}
	if !strings.Contains(text, "stopped CMD") {
		t.Errorf("stderr = %q, want it to say CMD was stopped", text)
	}
	got := pools.received()
	if len(got) != 2 || !proto.Equal(got[0], testRequest()) || !proto.Equal(got[1], reclaimOf("grant-term")) {
		t.Errorf("Acquire requests = %v, want the request then its reclaim by grant_id grant-term", got)
	}
}

func TestRunLostGrantRerunsUnderNewGrant(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{
		lostHolderStep(ready, "m1", "m2"),
		{
			before: []*v1.AcquireResponse{grantedWith("grant-rerun", poolMembers("builds", "m3", "m4"))},
			hold:   true,
		},
	}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	errW, stderr := stderrFile(t)
	status := runAsync(context.Background(), lostConfig(t, socket, w, errW, stdinR))
	nextLine(t, lines)
	close(ready)

	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	_ = w.Close()
	if got, want := remainingLines(lines), []string{"term", "rerun grant-rerun m3,m4"}; !slices.Equal(got, want) {
		t.Errorf("output after the ready line = %q, want %q: CMD stopped, then rerun under the new grant", got, want)
	}
	if text := stderr(); !strings.Contains(text, "stopped CMD") {
		t.Errorf("stderr = %q, want it to say CMD was stopped", text)
	}
	got := pools.received()
	if len(got) != 2 || !proto.Equal(got[1], reclaimOf("grant-term")) {
		t.Errorf("Acquire requests = %v, want the request then its reclaim by grant_id grant-term", got)
	}
}

func TestRunLostGrantReclaimsAgainWhenQueuedStreamEnds(t *testing.T) {
	ready := make(chan struct{})
	pools := &stopPools{steps: []scriptedStep{
		lostHolderStep(ready, "m1"),
		// The reclaim queues, then its stream ends before a grant.
		{before: []*v1.AcquireResponse{queuedAt(1)}},
		{
			before: []*v1.AcquireResponse{grantedWith("grant-rerun", poolMembers("builds", "m2"))},
			hold:   true,
		},
	}}
	socket := serveStopPools(t, pools)
	stdinR := stdinPipe(t)
	r, w := stdoutPipe(t)
	lines := outputLines(r)
	errW, _ := stderrFile(t)
	status := runAsync(context.Background(), lostConfig(t, socket, w, errW, stdinR))
	nextLine(t, lines)
	close(ready)

	if got := awaitStatus(t, status); got != 3 {
		t.Errorf("RunWithGrant = %d, want the rerun's status 3", got)
	}
	_ = w.Close()
	if got, want := remainingLines(lines), []string{"term", "rerun grant-rerun m2"}; !slices.Equal(got, want) {
		t.Errorf("output after the ready line = %q, want %q", got, want)
	}
	got := pools.received()
	if len(got) != 3 || !proto.Equal(got[1], reclaimOf("grant-term")) || !proto.Equal(got[2], reclaimOf("grant-term")) {
		t.Errorf("Acquire requests = %v, want the request then two reclaims by grant_id grant-term", got)
	}
}
