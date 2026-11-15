package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/storetest"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// Synthetic values only. The flight threshold is lowered from the production ten seconds. A forced delay of
// flightWiringSlow is over it, and a reconcile of an empty store is far under it.
const (
	flightWiringThreshold = time.Second
	flightWiringSlow      = 2 * flightWiringThreshold
	flightWiringHost      = "synthetic-host"
	flightWiringWait      = 10 * time.Second
	flightWiringPoll      = 10 * time.Millisecond
)

// flightWiringLog is the stderr of a run. Its goroutines may write while the test reads it, so the writes are locked.
type flightWiringLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write implements io.Writer.
func (l *flightWiringLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// String returns everything written so far.
func (l *flightWiringLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// flightWiringEnv returns a getenv over a fresh state directory and tmux directory, the store path that getenv
// resolves, and the traces directory beside that store. The store is XDG_STATE_HOME/orrery/orrery.db, so the traces
// are in XDG_STATE_HOME/orrery/traces.
func flightWiringEnv(t *testing.T) (func(string) string, string, string) {
	t.Helper()
	state := t.TempDir()
	env := map[string]string{
		"XDG_STATE_HOME":  state,
		"XDG_CONFIG_HOME": t.TempDir(),
		"TMUX_TMPDIR":     t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return getenv, path, filepath.Join(state, "orrery", "traces")
}

// flightWiringRecorder starts the flight recorder over the store getenv resolves, at the lowered threshold, as run
// does, and stops it when the test ends. The recorder is process-wide, so the tests here do not run in parallel.
func flightWiringRecorder(t *testing.T, getenv func(string) string) *telemetry.Flight {
	t.Helper()
	flight := startFlight(t.Context(), getenv, flightWiringThreshold, slog.New(slog.DiscardHandler))
	if flight == nil {
		t.Fatal("flight recorder did not start")
	}
	t.Cleanup(flight.Stop)
	return flight
}

// flightWiringHold blocks for d, or until ctx ends. It is the forced delay that the held reconcile and the late READY
// stand on.
func flightWiringHold(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// flightWiringTraces returns how many trace files named for phase dir holds.
func flightWiringTraces(t *testing.T, dir, phase string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read traces: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-"+phase+".trace") {
			n++
		}
	}
	return n
}

// TestFlightReconcileTraceOnlyWhenSlow checks that a sessions reconcile over the lowered threshold writes one
// reconcile trace in the traces directory beside the store, and that a reconcile under it writes none.
func TestFlightReconcileTraceOnlyWhenSlow(t *testing.T) {
	cases := []struct {
		name string
		hold time.Duration
		want int
	}{
		{name: "slow", hold: flightWiringSlow, want: 1},
		{name: "quick", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv, path, traces := flightWiringEnv(t)
			flight := flightWiringRecorder(t, getenv)
			st := openPaneStore(t, path)
			opts := evidenceWiringOptions(t)
			opts.flight = flight
			if tc.hold > 0 {
				opts.holdReconcile = func(ctx context.Context) { flightWiringHold(ctx, tc.hold) }
			}
			host, err := startSessions(
				t.Context(), opts, flightWiringHost, st, &recordingRouter{},
				slog.New(slog.DiscardHandler), getenv,
			)
			if err != nil {
				t.Fatalf("startSessions: %v", err)
			}
			if err := host.stop(); err != nil {
				t.Fatalf("stop sessions: %v", err)
			}
			if got := flightWiringTraces(t, traces, "reconcile"); got != tc.want {
				t.Errorf("reconcile traces = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestFlightStartTraceWhenReadyIsLate checks that a --sessions orreryd whose READY comes after the lowered threshold
// writes one start trace in the traces directory beside the store.
func TestFlightStartTraceWhenReadyIsLate(t *testing.T) {
	getenv, path, traces := flightWiringEnv(t)
	flight := flightWiringRecorder(t, getenv)
	storetest.Seed(t, path)
	opts, err := parseFlags([]string{
		"--sessions", "--session-shell", "/bin/sh",
		"--socket", filepath.Join(t.TempDir(), "orreryd.sock"),
	}, getenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	opts.flight = flight
	opts.started = time.Now()
	opts.holdReconcile = func(ctx context.Context) { flightWiringHold(ctx, flightWiringSlow) }

	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, opts, slog.New(slog.DiscardHandler), getenv) }()

	// READY comes after the held reconcile, and the start trace is written just after it, so the test waits for
	// the file.
	deadline := time.After(flightWiringWait)
	for flightWiringTraces(t, traces, "start") == 0 {
		select {
		case err := <-served:
			t.Fatalf("serve returned before READY wrote a start trace: %v", err)
		case <-deadline:
			cancel()
			<-served
			t.Fatal("READY wrote no start trace")
		case <-time.After(flightWiringPoll):
		}
	}
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if got := flightWiringTraces(t, traces, "start"); got != 1 {
		t.Errorf("start traces = %d, want 1", got)
	}
}

// TestFlightRefusedStartServesOn checks that a recorder which cannot start, because one already runs in the
// process, is logged, and that orreryd serves on until it is signaled.
func TestFlightRefusedStartServesOn(t *testing.T) {
	getenv, _, _ := flightWiringEnv(t)
	held, err := telemetry.StartFlight(t.TempDir(), time.Hour)
	if err != nil {
		t.Fatalf("hold a flight recorder: %v", err)
	}
	t.Cleanup(held.Stop)

	socket := filepath.Join(t.TempDir(), "orreryd.sock")
	var stderr flightWiringLog
	codes := make(chan int, 1)
	go func() { codes <- run([]string{"--socket", socket}, &stderr, getenv) }()

	// serve logs "store open" once the store has opened, after the sockets listen. Signaling before that would
	// interrupt the open, so the test waits for the log line.
	deadline := time.After(flightWiringWait)
	for !strings.Contains(stderr.String(), "store open") {
		select {
		case code := <-codes:
			t.Fatalf("run returned %d before it served; stderr %q", code, stderr.String())
		case <-deadline:
			t.Fatalf("store did not open; stderr %q", stderr.String())
		case <-time.After(flightWiringPoll):
		}
	}
	// run registers its signal handler before it listens, so the signal reaches run and not the test binary.
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("signal run: %v", err)
	}
	var code int
	select {
	case code = <-codes:
	case <-time.After(flightWiringWait):
		t.Fatal("run did not return after SIGINT")
	}
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr %q", code, exitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "start flight recorder") {
		t.Errorf("stderr %q does not log the refused flight recorder", stderr.String())
	}
}
