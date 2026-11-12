package cli_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	"github.com/joshsymonds/orrery/internal/cli"
)

// reclaimHold is how long a test watches a held grant for a second reclaim: several retries long.
const reclaimHold = 5 * testRetry

func TestRunReclaimRequestCarriesGrantID(t *testing.T) {
	fake := newPoolsFakeSteps(grantThenBreak("grant-id-1"), grantedHeld("grant-id-1"))
	socket := runServe(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, runConfig(t, socket, "sleep", io.Discard, io.Discard))
	waitClosed(t, fake.held, "the reclaim was never held")
	cancel()
	if got := awaitStatus(t, status); got != stoppedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGTERM status %d", got, stoppedStatus)
	}

	got := fake.received()
	if len(got) != 2 {
		t.Fatalf("Acquire called %d times, want 2: the request and its reclaim", len(got))
	}
	if !proto.Equal(got[0], testRequest()) {
		t.Errorf("first request = %v, want the request the run was given", got[0])
	}
	if !proto.Equal(got[1], reclaimOf("grant-id-1")) {
		t.Errorf("reclaim = %v, want the same request with grant_id grant-id-1", got[1])
	}
}

func TestRunReclaimResumesHolding(t *testing.T) {
	fake := newPoolsFakeSteps(grantThenBreak("grant-hold"), grantedHeld("grant-hold"))
	socket := runServe(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, runConfig(t, socket, "sleep", io.Discard, io.Discard))
	waitClosed(t, fake.held, "the reclaim was never held")

	// A reclaimed grant that is granted and held is not reclaimed again, so the command keeps running.
	select {
	case got := <-status:
		t.Fatalf("RunWithGrant = %d while the reclaimed grant was held", got)
	case <-time.After(reclaimHold):
	}
	if n := len(fake.received()); n != 2 {
		t.Errorf("Acquire called %d times, want 2: a held grant is not reclaimed again", n)
	}
	cancel()
	if got := awaitStatus(t, status); got != stoppedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGTERM status %d", got, stoppedStatus)
	}
}

func TestRunReclaimRetriesUntilGranted(t *testing.T) {
	fake := newPoolsFakeSteps(
		grantThenBreak("grant-retry"),
		refusedWith(connect.CodeUnavailable),
		grantedHeld("grant-retry"),
	)
	socket := runServe(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, runConfig(t, socket, "sleep", io.Discard, io.Discard))
	waitClosed(t, fake.held, "the reclaim was never granted")

	// The command runs through the refused reclaim: RunWithGrant has not returned.
	select {
	case got := <-status:
		t.Fatalf("RunWithGrant = %d while the grant was being reclaimed", got)
	default:
	}
	got := fake.received()
	if len(got) != 3 {
		t.Fatalf("Acquire called %d times, want 3: the request, a refused reclaim, and a granted one", len(got))
	}
	for i, req := range got[1:] {
		if !proto.Equal(req, reclaimOf("grant-retry")) {
			t.Errorf("reclaim %d = %v, want the same request with grant_id grant-retry", i+1, req)
		}
	}
	cancel()
	if stopped := awaitStatus(t, status); stopped != stoppedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGTERM status %d", stopped, stoppedStatus)
	}
}

func TestRunReclaimRefusedStopsCommand(t *testing.T) {
	tests := []struct {
		name string
		code connect.Code
	}{
		{name: "not found", code: connect.CodeNotFound},
		{name: "failed precondition", code: connect.CodeFailedPrecondition},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gate := make(chan struct{})
			fake := newPoolsFakeSteps(grantThenBreak("grant-refused"), refusedAfter(gate, tc.code))
			socket := runServe(t, fake)
			ready, w := stdoutPipe(t)
			var stderr bytes.Buffer
			status := runAsync(context.Background(), runConfig(t, socket, "sleep", w, &stderr))
			pid := readyPID(t, awaitReady(t, ready))
			close(gate)
			if got := awaitStatus(t, status); got != cli.ExitNoGrant {
				t.Errorf("RunWithGrant = %d, want ExitNoGrant %d", got, cli.ExitNoGrant)
			}
			waitGone(t, pid)
			if !strings.Contains(stderr.String(), "refused") {
				t.Errorf("stderr = %q, want it to say orreryd refused the reclaim", stderr.String())
			}
		})
	}
}

func TestRunReclaimCommandExitsWhileReconnecting(t *testing.T) {
	fake := newPoolsFakeSteps(grantThenBreak("grant-exit"), refusedWith(connect.CodeUnavailable))
	socket := runServe(t, fake)
	ready, w := stdoutPipe(t)
	var stderr bytes.Buffer
	status := runAsync(context.Background(), runConfig(t, socket, "sleep", w, &stderr))
	pid := readyPID(t, awaitReady(t, ready))
	// Unavailable is not a refusal, so the run keeps reconnecting while the command runs.
	awaitRequests(t, fake, 2)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill command: %v", err)
	}
	if got := awaitStatus(t, status); got != killedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGKILL status %d", got, killedStatus)
	}
	if strings.Contains(stderr.String(), "the grant ended") {
		t.Errorf("stderr = %q, want no report that the grant ended", stderr.String())
	}
}

func TestRunReclaimContextEndsWhileReconnecting(t *testing.T) {
	fake := newPoolsFakeSteps(grantThenBreak("grant-cancel"), refusedWith(connect.CodeUnavailable))
	socket := runServe(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	status := runAsync(ctx, runConfig(t, socket, "sleep", io.Discard, io.Discard))
	awaitRequests(t, fake, 2)
	cancel()
	if got := awaitStatus(t, status); got != stoppedStatus {
		t.Errorf("RunWithGrant = %d, want the command's SIGTERM status %d", got, stoppedStatus)
	}
}

func TestRunWithGrantHoldsGrantUntilCommandExits(t *testing.T) {
	fake := newPoolsFake(nil, true, grantedWith("grant-slow", poolMembers("builds", "m1")))
	socket := runServe(t, fake)
	ready, w := stdoutPipe(t)
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinR.Close()
		_ = stdinW.Close()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := runConfig(t, socket, "stop", w, io.Discard)
	cfg.Stdin = stdinR
	status := runAsync(ctx, cfg)
	awaitReady(t, ready)
	cancel()

	// The command keeps running until its stdin closes, so the grant must stay held until then.
	select {
	case <-fake.ended:
		t.Fatal("the grant ended while the command was still running")
	case <-time.After(reclaimHold):
	}
	if err := stdinW.Close(); err != nil {
		t.Fatalf("close command stdin: %v", err)
	}
	if got := awaitStatus(t, status); got != 0 {
		t.Errorf("RunWithGrant = %d, want the command's status 0", got)
	}
	waitClosed(t, fake.ended, "the grant stayed open after the command exited")
}
