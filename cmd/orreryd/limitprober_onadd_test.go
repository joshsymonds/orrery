package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// limitProbeOnAddToken is the invented setup-token of the account TestLimitsProbeOnAddThroughOrreryd adds. It starts
// sk-ant-oat, as the Accounts service requires, and is never sent anywhere.
const limitProbeOnAddToken = "sk-ant-oat01-synthetic-on-add"

// limitProbeOnAddTimer is the timer the test gives the prober in place of time.NewTimer. It records the duration of
// each timer the prober arms and signals each arming on armed. None of its timers ever fires, so every pass after the
// first comes from a wake. The prober arms on its own goroutine, so the durations are guarded by a lock.
type limitProbeOnAddTimer struct {
	mu    sync.Mutex
	arms  []time.Duration
	armed chan struct{}
	never chan time.Time
}

// newLimitProbeOnAddTimer returns a timer seam that has armed nothing.
func newLimitProbeOnAddTimer() *limitProbeOnAddTimer {
	return &limitProbeOnAddTimer{armed: make(chan struct{}, 1), never: make(chan time.Time)}
}

// newTimer records d and returns a channel that never fires, with a stop that does nothing.
func (tm *limitProbeOnAddTimer) newTimer(d time.Duration) (<-chan time.Time, func() bool) {
	tm.mu.Lock()
	tm.arms = append(tm.arms, d)
	tm.mu.Unlock()
	select {
	case tm.armed <- struct{}{}:
	default:
	}
	return tm.never, func() bool { return true }
}

// snapshot returns a copy of the durations armed so far.
func (tm *limitProbeOnAddTimer) snapshot() []time.Duration {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return slices.Clone(tm.arms)
}

// awaitLimitProbeOnAddArms waits until the prober has armed n timers, and returns their durations. Each arming follows
// a pass, so n arms mean n passes have ended. The wait is bounded by limitProbeWiringWait, which ends only a run in
// which the pass never comes.
func awaitLimitProbeOnAddArms(t *testing.T, tm *limitProbeOnAddTimer, n int) []time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limitProbeWiringWait)
	defer cancel()
	for {
		if arms := tm.snapshot(); len(arms) >= n {
			return arms
		}
		select {
		case <-tm.armed:
		case <-ctx.Done():
			t.Fatalf("the prober armed %d timers within %v, want %d", len(tm.snapshot()), limitProbeWiringWait, n)
		}
	}
}

// TestLimitsProbeOnAddThroughOrreryd starts a --sessions host with a gateway over a store that holds no account, and
// waits for the prober's first pass, which probes nothing. It then adds an account through orreryd's Accounts service
// on the session socket, as serve forwards an add, and checks that the account is probed at once, pinned at the
// gateway: the store's add wakes the prober, since its timer never fires and its clock never moves.
func TestLimitsProbeOnAddThroughOrreryd(t *testing.T) {
	rec := newLimitProbeWiringRecorder()
	timer := newLimitProbeOnAddTimer()
	st, getenv := openLimitStore(t)
	opts := options{
		socket:       filepath.Join(t.TempDir(), "orreryd.sock"),
		claude:       eventWakeClaude(t),
		sessionShell: eventWakeShell(t),
		gateway:      limitProbeWiringGateway,
		probeNow:     rec.clock.now,
		probeExec:    rec.exec,
		probeTimer:   timer.newTimer,
	}
	host, err := startSessions(
		t.Context(), opts, limitProbeWiringHost, st, newRecordingRouter(), nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	defer func() {
		if err := host.stop(); err != nil {
			t.Errorf("stop sessions: %v", err)
		}
	}()

	first := awaitLimitProbeOnAddArms(t, timer, 1)
	if first[0] != enginehost.LimitProbeHaikuEvery {
		t.Fatalf(
			"timer after the pass with no accounts armed for %v, want %v",
			first[0],
			enginehost.LimitProbeHaikuEvery,
		)
	}
	if probes := rec.snapshot(); len(probes) != 0 {
		t.Fatalf("the pass with no accounts probed %d times, want none", len(probes))
	}
	t.Logf("first pass over no accounts: no probe, timer armed for %v and never fired", first[0])

	client := enginev1connect.NewAccountsClient(
		cronWiringHTTP(t, protocol.SessionSocketPath(opts.socket)), "http://accounts.invalid")
	added, err := client.AddAccount(t.Context(), connect.NewRequest(&v1.AddAccountRequest{
		Name: "Synthetic added account", Provider: "anthropic", Plan: "Claude Max", Token: limitProbeOnAddToken,
	}))
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	id := added.Msg.GetAccount().GetId()
	if id == "" {
		t.Fatal("AddAccount answered an account with no id")
	}

	probes := awaitLimitProbeWiring(t, rec)
	if len(probes) != 1 {
		t.Fatalf("the add brought %d probes, want 1", len(probes))
	}
	headers, _ := holderhost.EnvironValue(probes[0].cmd.Env, "ANTHROPIC_CUSTOM_HEADERS")
	if !strings.HasSuffix(headers, ": "+id) {
		t.Errorf("probe ANTHROPIC_CUSTOM_HEADERS = %q, want it to pin the added account %s", headers, id)
	}
	base, _ := holderhost.EnvironValue(probes[0].cmd.Env, "ANTHROPIC_BASE_URL")
	if base != limitProbeWiringGateway {
		t.Errorf("probe ANTHROPIC_BASE_URL = %q, want %s", base, limitProbeWiringGateway)
	}
	if !probes[0].at.Equal(limitProbeWiringNow()) {
		t.Errorf("probe ran at %v, want the unmoved injected clock's %v", probes[0].at, limitProbeWiringNow())
	}
	arms := awaitLimitProbeOnAddArms(t, timer, 2)
	t.Logf("account %s added through the Accounts service: probed at once at the gateway; timers armed %v, none fired",
		id, arms)
}
