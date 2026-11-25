package main

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values for the limit prober wiring test. The account, the gateway and the clock are invented.
const (
	limitProbeWiringHost    = "synthetic-host"
	limitProbeWiringAccount = "acct-limit-probe-wiring"
	limitProbeWiringGateway = "http://gateway.synthetic.invalid:4110"
	limitProbeWiringStamp   = 1_700_000_000
	// limitProbeWiringPhase is the shutdown phase the prober's stop and wait are logged under.
	limitProbeWiringPhase = "limit probes"
	// limitProbeWiringWait bounds the wait for the recorded probe. The prober's first pass starts its probe at once, so
	// the bound ends only a run in which no probe comes.
	limitProbeWiringWait = 30 * time.Second
)

// limitProbeWiringNow is the moment the injected clock returns: a fixed moment, so a probe's record carries it.
func limitProbeWiringNow() time.Time {
	return time.Unix(limitProbeWiringStamp, 0)
}

// limitProbeWiringClock is the injected clock the prober reads in place of time.Now. It counts the reads and keeps the
// moment of the last one, so a probe can be stamped with the moment the prober last read the clock before the probe
// ran. The prober reads it on its own goroutine, so it is guarded by a lock.
type limitProbeWiringClock struct {
	mu    sync.Mutex
	reads int
	last  time.Time
}

// now is the clock the prober reads: the injected moment, which it records as the last read.
func (c *limitProbeWiringClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	c.last = limitProbeWiringNow()
	return c.last
}

// lastRead returns the moment of the last read and the number of reads. The moment is the zero time when no read
// happened.
func (c *limitProbeWiringClock) lastRead() (time.Time, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.reads
}

// limitProbeWiringProbe is one probe the recording Exec ran: its command, and the moment the prober last read the
// injected clock when it ran.
type limitProbeWiringProbe struct {
	cmd *exec.Cmd
	at  time.Time
}

// limitProbeWiringRecorder is the Exec the test gives the prober in place of running claude. It records each probe with
// the moment the prober last read the injected clock, and signals each one on recorded. The prober runs on its own
// goroutine, so the probes are guarded by a lock.
type limitProbeWiringRecorder struct {
	clock    *limitProbeWiringClock
	mu       sync.Mutex
	probes   []limitProbeWiringProbe
	recorded chan struct{}
}

// newLimitProbeWiringRecorder returns an empty recorder with its own injected clock.
func newLimitProbeWiringRecorder() *limitProbeWiringRecorder {
	return &limitProbeWiringRecorder{clock: &limitProbeWiringClock{}, recorded: make(chan struct{}, 1)}
}

// exec records cmd at the moment the prober last read the injected clock, and succeeds without running claude.
func (r *limitProbeWiringRecorder) exec(_ context.Context, cmd *exec.Cmd) error {
	at, _ := r.clock.lastRead()
	r.mu.Lock()
	r.probes = append(r.probes, limitProbeWiringProbe{cmd: cmd, at: at})
	r.mu.Unlock()
	select {
	case r.recorded <- struct{}{}:
	default:
	}
	return nil
}

// snapshot returns a copy of the probes recorded so far.
func (r *limitProbeWiringRecorder) snapshot() []limitProbeWiringProbe {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]limitProbeWiringProbe(nil), r.probes...)
}

// awaitLimitProbeWiring waits until the recorder holds a probe, and returns the probes. The wait is bounded by
// limitProbeWiringWait, and fails the test when no probe is recorded within it.
func awaitLimitProbeWiring(t *testing.T, rec *limitProbeWiringRecorder) []limitProbeWiringProbe {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limitProbeWiringWait)
	defer cancel()
	for {
		if probes := rec.snapshot(); len(probes) > 0 {
			return probes
		}
		select {
		case <-rec.recorded:
		case <-ctx.Done():
			t.Fatalf("no probe recorded within %v", limitProbeWiringWait)
		}
	}
}

// startLimitProbeWiringHost starts a --sessions host over a store that holds one invented account. Its --gateway is
// gateway, its prober's clock is rec's injected clock, and its prober's Exec is rec. It returns the host and the log
// its shutdown writes to.
func startLimitProbeWiringHost(t *testing.T, gateway string, rec *limitProbeWiringRecorder) (*sessionHost, *logBuffer) {
	t.Helper()
	st, getenv := openLimitStore(t)
	if _, err := st.AddAccount(t.Context(), store.NewAccount{
		ID: limitProbeWiringAccount, Name: "Synthetic account", Provider: "anthropic", Token: "synthetic-token",
		Created: limitProbeWiringStamp,
	}); err != nil {
		t.Fatalf("add account %s: %v", limitProbeWiringAccount, err)
	}
	logs := &logBuffer{}
	opts := options{
		socket:       filepath.Join(t.TempDir(), "orreryd.sock"),
		claude:       eventWakeClaude(t),
		sessionShell: eventWakeShell(t),
		gateway:      gateway,
		probeNow:     rec.clock.now,
		probeExec:    rec.exec,
	}
	host, err := startSessions(
		t.Context(), opts, limitProbeWiringHost, st, newRecordingRouter(), nil,
		slog.New(slog.NewTextHandler(logs, nil)), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host, logs
}

// checkLimitProbeWiringFlag checks that --gateway gives options.gateway, which is empty without the flag, and that a
// value that is not an absolute http or https URL with a host is a usage error.
func checkLimitProbeWiringFlag(t *testing.T) {
	t.Helper()
	opts, err := parseFlags(nil, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if opts.gateway != "" {
		t.Errorf("gateway without the flag = %q, want empty", opts.gateway)
	}
	opts, err = parseFlags([]string{"--gateway", limitProbeWiringGateway}, holderScopeGetenv)
	if err != nil {
		t.Fatalf("parseFlags(--gateway %s): %v", limitProbeWiringGateway, err)
	}
	if opts.gateway != limitProbeWiringGateway {
		t.Errorf("gateway with the flag = %q, want %s", opts.gateway, limitProbeWiringGateway)
	}
	t.Logf("--gateway %s gives options.gateway", limitProbeWiringGateway)
	for _, value := range []string{
		"gateway.synthetic.invalid:4110", "ftp://gateway.synthetic.invalid:4110", "/gateway", "http://", "http://:4110",
	} {
		_, err := parseFlags([]string{"--gateway", value}, holderScopeGetenv)
		var usage usageError
		if !errors.As(err, &usage) {
			t.Errorf("parseFlags(--gateway %q) error = %v, want a usage error", value, err)
		}
	}
	t.Logf("--gateway refuses values that are not an absolute http or https URL with a host")
}

// checkLimitProbeWiringNoGateway checks that with an empty gateway no prober starts: the host holds no stop for one,
// its shutdown logs no limit probes phase, and no probe is recorded.
func checkLimitProbeWiringNoGateway(t *testing.T) {
	t.Helper()
	rec := newLimitProbeWiringRecorder()
	host, logs := startLimitProbeWiringHost(t, "", rec)
	if host.stopProbes != nil {
		t.Errorf("a prober started with an empty gateway")
	}
	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	if strings.Contains(logs.String(), limitProbeWiringPhase) {
		t.Errorf("shutdown logged the %q phase with an empty gateway:\n%s", limitProbeWiringPhase, logs.String())
	}
	if n := len(rec.snapshot()); n != 0 {
		t.Errorf("%d probes recorded with an empty gateway, want none", n)
	}
	if _, reads := rec.clock.lastRead(); reads != 0 {
		t.Errorf("the injected clock was read %d times with an empty gateway, want none", reads)
	}
	t.Logf("empty gateway: no prober started, shutdown logged no %q phase, no probe recorded, the clock never read",
		limitProbeWiringPhase)
}

// checkLimitProbeWiringGateway checks that with a gateway the prober starts beside the other loops, probes the account
// pinned at the gateway at the injected clock's time, and that shutdown stops it and closes its done channel.
func checkLimitProbeWiringGateway(t *testing.T) {
	t.Helper()
	rec := newLimitProbeWiringRecorder()
	host, logs := startLimitProbeWiringHost(t, limitProbeWiringGateway, rec)
	if host.stopProbes == nil {
		t.Fatalf("no prober started with gateway %s", limitProbeWiringGateway)
	}
	probes := awaitLimitProbeWiring(t, rec)
	probe := probes[0]
	base, ok := holderhost.EnvironValue(probe.cmd.Env, "ANTHROPIC_BASE_URL")
	if !ok || base != limitProbeWiringGateway {
		t.Errorf("probe ANTHROPIC_BASE_URL = %q (set %t), want %s", base, ok, limitProbeWiringGateway)
	}
	headers, _ := holderhost.EnvironValue(probe.cmd.Env, "ANTHROPIC_CUSTOM_HEADERS")
	if !strings.Contains(headers, limitProbeWiringAccount) {
		t.Errorf("probe ANTHROPIC_CUSTOM_HEADERS = %q, want it to name account %s", headers, limitProbeWiringAccount)
	}
	if !probe.at.Equal(limitProbeWiringNow()) {
		t.Errorf("probe recorded at %v, want the injected clock's %v", probe.at, limitProbeWiringNow())
	}
	_, reads := rec.clock.lastRead()
	t.Logf("gateway %s: prober started, account %s probed at the injected clock's %v after %d clock reads",
		limitProbeWiringGateway, limitProbeWiringAccount, probe.at, reads)

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
	select {
	case <-host.probesDone:
	default:
		t.Errorf("the prober's done channel is open after shutdown")
	}
	if !strings.Contains(logs.String(), limitProbeWiringPhase) {
		t.Errorf("shutdown logged no %q phase with a gateway:\n%s", limitProbeWiringPhase, logs.String())
	}
	t.Logf("gateway %s: shutdown stopped the prober and closed its done channel", limitProbeWiringGateway)
}

// TestLimitsProberLifecycle checks that orreryd runs the limit prober with its loops only when given a gateway address:
// that --gateway gives options.gateway and refuses a value that is not a gateway URL; that with an empty gateway no
// prober starts; and that with a gateway the prober probes the account at the gateway on the injected clock, and
// shutdown stops it.
func TestLimitsProberLifecycle(t *testing.T) {
	checkLimitProbeWiringFlag(t)
	checkLimitProbeWiringNoGateway(t)
	checkLimitProbeWiringGateway(t)
}
