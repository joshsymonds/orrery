package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// failsAccount is the account of TestLimitsProbeFails, whose probes all fail the way claude does when it cannot reach
// the gateway.
const failsAccount = "synthetic-acct-unreachable"

// failsCause is the error every probe of TestLimitsProbeFails fails with.
const failsCause = "synthetic claude: cannot reach the gateway"

// failsDropTime removes the time of each log record, so the logged lines hold nothing that varies from run to run.
func failsDropTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

// TestLimitsProbeFails makes a probe fail and checks that the failure is logged at Warn with its account, model and
// error, that the account is not probed again before its window ends and is probed again once it has, and that Run
// keeps passing after the failure. Run's timer is a channel the test fires, so no test waits on the wall clock.
func TestLimitsProbeFails(t *testing.T) {
	start := idleProbeStart()
	clock := &idleProbeClock{now: start}
	// The account's one reading is a 7d_oi reading taken at the start. Its Haiku probe is due 20 minutes on, and its
	// Fable probe is not due until an hour on, which is after every pass below.
	readings := &idleProbeReadingStore{}
	readings.Add(ledger.Reading{Account: failsAccount, Claim: idleProbeWeekly, Observed: start.Unix()})
	accounts := idleProbeAccounts{accounts: []store.Account{{ID: failsAccount}}}
	rec := &idleProbeRecorder{err: errors.New(failsCause)}
	var logs bytes.Buffer
	cfg := idleProbeConfig(t, accounts, readings, clock.Now, rec)
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{ReplaceAttr: failsDropTime}))
	armed := make(chan time.Duration)
	fire := make(chan time.Time)
	cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		armed <- d
		return fire, func() bool { return true }
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewLimitProber(cfg).Run(ctx)
	}()

	// next returns the duration of the timer Run arms, failing the test if Run returns instead.
	next := func() time.Duration {
		select {
		case d := <-armed:
			return d
		case <-done:
			t.Fatal("Run returned, want it to keep passing after the failed probe")
		}
		return 0
	}
	// pass fires the timer at offset from the start and returns the duration of the timer Run arms after that pass.
	pass := func(offset time.Duration) time.Duration {
		clock.now = start.Add(offset)
		fire <- clock.now
		return next()
	}

	if got := next(); got != 20*time.Minute {
		t.Fatalf("first timer armed for %v, want 20m0s", got)
	}
	if len(rec.cmds) != 0 {
		t.Fatalf("first pass recorded %d probes, want none", len(rec.cmds))
	}
	t.Logf("pass +0s: no probe, timer armed for 20m0s")

	// At 20 minutes the Haiku probe runs and fails. The failure is logged, and Run arms its timer for the one-minute
	// floor.
	if got := pass(20 * time.Minute); got != time.Minute {
		t.Fatalf("timer after the failed probe armed for %v, want 1m0s", got)
	}
	if len(rec.cmds) != 1 {
		t.Fatalf("pass at +20m0s recorded %d probes, want the one Haiku probe", len(rec.cmds))
	}
	t.Logf("pass +20m0s: Haiku probe ran and failed, timer armed for 1m0s")
	for _, want := range []string{
		"level=WARN",
		`msg="limitprobe: probe failed"`,
		"account=" + failsAccount,
		"model=" + protocol.DefaultSummaryModel,
		failsCause,
	} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log %q does not carry %q", logs.String(), want)
		}
	}

	// At 21 minutes Run passes again and the account is not probed, since its window runs to 40 minutes.
	if got := pass(21 * time.Minute); got != 19*time.Minute {
		t.Fatalf("pass at +21m0s armed %v, want 19m0s to the end of the window", got)
	}
	if len(rec.cmds) != 1 {
		t.Fatalf("pass at +21m0s probed inside the window: %d probes, want 1", len(rec.cmds))
	}
	t.Logf("pass +21m0s: no probe inside the window, timer armed for 19m0s")

	// At 40 minutes the window has ended, so the Haiku probe runs again and fails again.
	if got := pass(40 * time.Minute); got != time.Minute {
		t.Fatalf("pass at +40m0s armed %v, want 1m0s after the second failed probe", got)
	}
	if len(rec.cmds) != 2 {
		t.Fatalf("pass at +40m0s recorded %d probes, want the Haiku probe once its window ended", len(rec.cmds))
	}
	t.Logf("pass +40m0s: Haiku probe ran again once its window ended and failed, timer armed for 1m0s")

	cancel()
	<-done
	for line := range strings.Lines(logs.String()) {
		t.Logf("log: %s", strings.TrimSpace(line))
	}
}
