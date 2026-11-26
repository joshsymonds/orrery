package enginehost

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// onAddAccount is the invented account TestLimitsProbeOnAdd adds once the prober has passed with no accounts.
const onAddAccount = "synthetic-acct-added"

// onAddAccounts is the LimitProbeAccounts of TestLimitsProbeOnAdd: the accounts the test has added, which start empty.
// Run reads them on its own goroutine, so they are guarded by a lock.
type onAddAccounts struct {
	mu       sync.Mutex
	accounts []store.Account
}

// Accounts returns a copy of the accounts added so far.
func (a *onAddAccounts) Accounts(context.Context) ([]store.Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.accounts), nil
}

// add adds the account with id.
func (a *onAddAccounts) add(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.accounts = append(a.accounts, store.Account{ID: id})
}

// onAddProbes is the Exec seam of TestLimitsProbeOnAdd. It records the account and model of each probe without running
// claude. Run probes on its own goroutine, so the probes are guarded by a lock.
type onAddProbes struct {
	mu     sync.Mutex
	probes []cadenceProbe
}

// exec records the probe cmd runs.
func (p *onAddProbes) exec(_ context.Context, cmd *exec.Cmd) error {
	headers := idleProbeEnvEntries(cmd.Env, idleProbeHeaderEnv)
	var account string
	if len(headers) == 1 {
		account, _ = strings.CutPrefix(headers[0], idleProbeHeaderText)
	}
	model, _ := idleProbeFlag(cmd.Args, "--model")
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probes = append(p.probes, cadenceProbe{account: account, model: model})
	return nil
}

// snapshot returns a copy of the probes recorded so far.
func (p *onAddProbes) snapshot() []cadenceProbe {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.probes)
}

// onAddClock is the clock of TestLimitsProbeOnAdd, which the test moves by hand while Run reads it on its own goroutine,
// so its reading is guarded by a lock.
type onAddClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock's reading.
func (c *onAddClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// advance moves the clock on by d.
func (c *onAddClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// TestLimitsProbeOnAdd checks that a wake makes Run pass at once, so an account added after a pass that found no
// accounts is probed without its timer firing, and that a wake within the account's windows probes nothing. A new
// account has no reading, so both its Haiku and its Fable windows are open: the wake after the add sends the Haiku
// probe, and since a pass sends an account one probe, the next wake sends the Fable probe. Every later wake inside the
// windows those probes opened probes nothing. Run's timer is a channel that never fires, and the clock moves only by
// hand, so only the wakes bring the passes after the first, and no test waits on the wall clock.
func TestLimitsProbeOnAdd(t *testing.T) {
	clock := &onAddClock{now: idleProbeStart()}
	accounts := &onAddAccounts{}
	probes := &onAddProbes{}
	cfg := idleProbeConfig(t, accounts, idleProbeReadings{}, clock.Now, &idleProbeRecorder{})
	cfg.Exec = probes.exec
	armed := make(chan time.Duration)
	never := make(chan time.Time)
	var stopsMu sync.Mutex
	stops := 0
	cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		armed <- d
		return never, func() bool {
			stopsMu.Lock()
			defer stopsMu.Unlock()
			stops++
			return true
		}
	}
	prober := NewLimitProber(cfg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		prober.Run(ctx)
	}()
	// wake wakes the prober and returns the duration of the timer Run arms after the pass the wake brings.
	wake := func() time.Duration {
		prober.Wake()
		return <-armed
	}

	// The first pass finds no accounts, probes nothing, and arms the timer for the Haiku interval.
	if got := <-armed; got != LimitProbeHaikuEvery {
		t.Fatalf("timer after the pass with no accounts armed for %v, want %v", got, LimitProbeHaikuEvery)
	}
	if got := probes.snapshot(); len(got) != 0 {
		t.Fatalf("the pass with no accounts probed %v, want nothing", got)
	}
	t.Logf("pass with no accounts: no probe, timer armed for %v and never fired", LimitProbeHaikuEvery)

	// The account is added and the prober woken, with the timer still unfired and the clock unmoved. The wake's pass
	// probes the account on Haiku. Its Fable probe is still due, so the timer is armed for the one-minute floor.
	accounts.add(onAddAccount)
	if got := wake(); got != limitProbeMinGap {
		t.Fatalf("timer after the wake's pass armed for %v, want %v", got, limitProbeMinGap)
	}
	haiku := cadenceProbe{account: onAddAccount, model: protocol.DefaultSummaryModel}
	if got := probes.snapshot(); !slices.Equal(got, []cadenceProbe{haiku}) {
		t.Fatalf("the wake's pass probed %v, want %v", got, []cadenceProbe{haiku})
	}
	t.Logf("wake after the add: account %s probed on %s at once", onAddAccount, haiku.model)

	// The next wake, inside the Haiku window, sends the Fable probe, whose window was open since the account was added.
	fable := cadenceProbe{account: onAddAccount, model: LimitProbeFableModel}
	if got := wake(); got != limitProbeMinGap {
		t.Fatalf("timer after the second wake's pass armed for %v, want %v", got, limitProbeMinGap)
	}
	want := []cadenceProbe{haiku, fable}
	if got := probes.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("the second wake's pass left probes %v, want %v", got, want)
	}
	t.Logf("second wake: account %s probed on %s, its Haiku window held", onAddAccount, fable.model)

	// A wake at the same moment probes nothing, and the timer is armed for the end of the Haiku window. A wake 19
	// minutes on, still inside it, probes nothing either, and the timer is armed for the minute left of it.
	if got := wake(); got != LimitProbeHaikuEvery {
		t.Fatalf("timer after a wake inside both windows armed for %v, want %v", got, LimitProbeHaikuEvery)
	}
	clock.advance(LimitProbeHaikuEvery - time.Minute)
	if got := wake(); got != time.Minute {
		t.Fatalf("timer after a wake 19m on armed for %v, want the 1m0s left of the Haiku window", got)
	}
	if got := probes.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("wakes inside the windows left probes %v, want only %v: a wake probed inside a window", got, want)
	}
	t.Logf("wakes at +0s and +19m inside both windows: no probe")

	cancel()
	<-done
	stopsMu.Lock()
	defer stopsMu.Unlock()
	if stops != 5 {
		t.Errorf("Run stopped %d timers, want all five armed timers stopped", stops)
	}
}

// TestLimitsProbeOnAddWakeNeverBlocks checks that Wake returns at once when no Run takes it, however often it is
// called: the wakes past the pending one are the same wake.
func TestLimitsProbeOnAddWakeNeverBlocks(t *testing.T) {
	cfg := idleProbeConfig(t, &onAddAccounts{}, idleProbeReadings{}, idleProbeStart, &idleProbeRecorder{})
	prober := NewLimitProber(cfg)
	for range 3 {
		prober.Wake()
	}
	if n := len(prober.wake); n != 1 {
		t.Errorf("three wakes with no Run left %d pending, want 1", n)
	}
}
