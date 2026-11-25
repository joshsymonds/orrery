package enginehost

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// cadenceMinutes is the span of TestLimitsProbeCadence, which passes once a minute through it.
const cadenceMinutes = 120

// The traffic of TestLimitsProbeCadence: the quiet accounts are read every cadenceQuietEvery minutes, and the fresh
// account every cadenceFreshEvery minutes.
const (
	cadenceQuietEvery = 3
	cadenceFreshEvery = 5
)

// The accounts of TestLimitsProbeCadence. The haiku-only and no-weekly accounts are read on 5h and 7d by their
// traffic. The haiku-only account also has one 7d_oi reading, taken at the start, which its traffic never refreshes;
// the no-weekly account never has one. The silent account has no traffic, and its probes write no reading. The fresh
// account is read on all three claims by its traffic.
const (
	cadenceHaikuOnly = "synthetic-acct-haiku-only"
	cadenceNoWeekly  = "synthetic-acct-no-weekly"
	cadenceSilent    = "synthetic-acct-silent"
	cadenceFresh     = "synthetic-acct-fresh"
)

// The claims TestLimitsProbeCadence writes for an account.
const (
	cadenceFiveHour = "5h"
	cadenceSevenDay = "7d"
)

// cadenceProbe is one probe the Exec seam of TestLimitsProbeCadence ran: the account and model it named, and the
// offset of the clock from the start when it ran.
type cadenceProbe struct {
	offset  time.Duration
	account string
	model   string
}

// cadenceRun is the Exec seam of TestLimitsProbeCadence. It records each probe and, unless the probe's account is the
// silent one, writes the 5h and 7d readings the answer carries, observed at the clock's reading. It never fails.
type cadenceRun struct {
	t        *testing.T
	clock    *idleProbeClock
	readings *idleProbeReadingStore
	start    time.Time
	probes   []cadenceProbe
}

// exec is the Exec seam of TestLimitsProbeCadence.
func (r *cadenceRun) exec(_ context.Context, cmd *exec.Cmd) error {
	headers := idleProbeEnvEntries(cmd.Env, idleProbeHeaderEnv)
	account, pinned := "", false
	if len(headers) == 1 {
		account, pinned = strings.CutPrefix(headers[0], idleProbeHeaderText)
	}
	if !pinned {
		r.t.Errorf("probe %q has no single X-Orrery-Account pin: %q", cmd.Args, headers)
	}
	model, _ := idleProbeFlag(cmd.Args, "--model")
	offset := r.clock.now.Sub(r.start)
	r.t.Logf("probe +%v account=%s model=%s", offset, account, model)
	r.probes = append(r.probes, cadenceProbe{offset: offset, account: account, model: model})
	if account != cadenceSilent {
		r.readings.Add(cadenceReadings(account, r.clock.now.Unix(), cadenceFiveHour, cadenceSevenDay)...)
	}
	return nil
}

// traffic adds the readings the simulated traffic of minute brings: the quiet accounts' 5h and 7d readings every
// cadenceQuietEvery minutes, and the fresh account's readings on all three claims every cadenceFreshEvery minutes.
func (r *cadenceRun) traffic(minute int) {
	observed := r.clock.now.Unix()
	if minute%cadenceQuietEvery == 0 {
		r.readings.Add(cadenceReadings(cadenceHaikuOnly, observed, cadenceFiveHour, cadenceSevenDay)...)
		r.readings.Add(cadenceReadings(cadenceNoWeekly, observed, cadenceFiveHour, cadenceSevenDay)...)
	}
	if minute%cadenceFreshEvery == 0 {
		r.readings.Add(cadenceReadings(cadenceFresh, observed, cadenceFiveHour, cadenceSevenDay, idleProbeWeekly)...)
	}
}

// offsets returns the offsets from the start of the probes of account on model, in the order they ran.
func (r *cadenceRun) offsets(account, model string) []time.Duration {
	offsets := make([]time.Duration, 0, len(r.probes))
	for _, probe := range r.probes {
		if probe.account == account && probe.model == model {
			offsets = append(offsets, probe.offset)
		}
	}
	return offsets
}

// cadenceReadings returns one reading of account on each of claims, observed at observed.
func cadenceReadings(account string, observed int64, claims ...string) []ledger.Reading {
	readings := make([]ledger.Reading, 0, len(claims))
	for _, claim := range claims {
		readings = append(readings, ledger.Reading{Account: account, Claim: claim, Observed: observed})
	}
	return readings
}

// cadenceSpacing returns the smallest gap between consecutive offsets, and whether there are two to compare.
func cadenceSpacing(offsets []time.Duration) (time.Duration, bool) {
	if len(offsets) < 2 {
		return 0, false
	}
	smallest := offsets[1] - offsets[0]
	for i := range len(offsets) - 1 {
		smallest = min(smallest, offsets[i+1]-offsets[i])
	}
	return smallest, true
}

// cadenceCrossGap returns the smallest gap between an offset in haiku and one in fable, and whether both have one.
func cadenceCrossGap(haiku, fable []time.Duration) (time.Duration, bool) {
	var smallest time.Duration
	found := false
	for _, h := range haiku {
		for _, f := range fable {
			gap := max(h-f, f-h)
			if !found || gap < smallest {
				smallest, found = gap, true
			}
		}
	}
	return smallest, found
}

// TestLimitsProbeCadence advances a fake clock minute by minute through two hours and checks how each account is
// probed. The haiku-only account, kept fresh on 5h and 7d by Haiku traffic, is sent a Fable probe once its 7d_oi
// reading is an hour old. The fresh account, whose 7d_oi traffic keeps it fresh, is sent none. Every account is probed
// by Haiku at most once per 20 minutes and by Fable at most once an hour, including the silent account, whose probes
// exit without writing a reading, and the no-weekly account, whose Fable answers write 5h and 7d but no 7d_oi.
func TestLimitsProbeCadence(t *testing.T) {
	start := idleProbeStart()
	clock := &idleProbeClock{now: start}
	readings := &idleProbeReadingStore{}
	readings.Add(ledger.Reading{Account: cadenceHaikuOnly, Claim: idleProbeWeekly, Observed: start.Unix()})
	run := &cadenceRun{t: t, clock: clock, readings: readings, start: start}
	accounts := idleProbeAccounts{accounts: []store.Account{
		{ID: cadenceHaikuOnly}, {ID: cadenceNoWeekly}, {ID: cadenceSilent}, {ID: cadenceFresh},
	}}
	cfg := idleProbeConfig(t, accounts, readings, clock.Now, &idleProbeRecorder{})
	cfg.Exec = run.exec
	prober := NewLimitProber(cfg)
	for minute := range cadenceMinutes + 1 {
		clock.now = start.Add(time.Duration(minute) * time.Minute)
		run.traffic(minute)
		prober.Pass(t.Context())
	}

	for _, id := range []string{cadenceHaikuOnly, cadenceNoWeekly, cadenceSilent, cadenceFresh} {
		haiku := run.offsets(id, protocol.DefaultSummaryModel)
		fable := run.offsets(id, LimitProbeFableModel)
		if gap, ok := cadenceSpacing(haiku); ok && gap < LimitProbeHaikuEvery {
			t.Errorf("account %s Haiku probes %v are %v apart, want at least %v", id, haiku, gap, LimitProbeHaikuEvery)
		}
		if gap, ok := cadenceSpacing(fable); ok && gap < LimitProbeFableEvery {
			t.Errorf("account %s Fable probes %v are %v apart, want at least %v", id, fable, gap, LimitProbeFableEvery)
		}
		gap, both := cadenceCrossGap(haiku, fable)
		t.Logf("summary account=%s haiku=%v fable=%v smallest haiku-fable gap=%v (both probed: %t)",
			id, haiku, fable, gap, both)
	}

	if got := run.offsets(cadenceHaikuOnly, protocol.DefaultSummaryModel); len(got) != 0 {
		t.Errorf("haiku-only account got %d Haiku probes at %v, want none: its traffic keeps 5h and 7d fresh",
			len(got), got)
	}
	if got := run.offsets(cadenceHaikuOnly, LimitProbeFableModel); len(got) == 0 || got[0] != time.Hour {
		t.Errorf("haiku-only account's Fable probes %v, want the first an hour in, once its 7d_oi reading is old", got)
	}
	if got := run.offsets(cadenceNoWeekly, LimitProbeFableModel); len(got) == 0 || got[0] != 0 {
		t.Errorf("no-weekly account's Fable probes %v, want the first at the start, when it has no 7d_oi reading", got)
	}
	fresh := len(run.offsets(cadenceFresh, protocol.DefaultSummaryModel)) +
		len(run.offsets(cadenceFresh, LimitProbeFableModel))
	if fresh != 0 {
		t.Errorf("fresh account got %d probes, want none: its 7d_oi traffic keeps it fresh", fresh)
	}
	if got := run.offsets(cadenceSilent, protocol.DefaultSummaryModel); len(got) < 2 {
		t.Errorf("silent account got %d Haiku probes at %v, want the repeats its 20-minute windows allow",
			len(got), got)
	}
	if got := run.offsets(cadenceSilent, LimitProbeFableModel); len(got) < 1 {
		t.Error("silent account got no Fable probe, want one once its readings are absent")
	}
}
