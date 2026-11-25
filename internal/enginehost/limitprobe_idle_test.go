package enginehost

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// The invented ids and fixed values of the idle-probe tests. The gateway URL is never reached, since the Exec seam
// records each command without running it.
const (
	idleProbeGateway    = "http://fake-gateway.invalid"
	idleProbeStale      = "synthetic-acct-stale"
	idleProbeNone       = "synthetic-acct-none"
	idleProbeFresh      = "synthetic-acct-fresh"
	idleProbeWeekly     = "7d_oi"
	idleProbeBaseEntry  = "ANTHROPIC_BASE_URL="
	idleProbeHeaderEnv  = "ANTHROPIC_CUSTOM_HEADERS="
	idleProbeHeaderText = "ANTHROPIC_CUSTOM_HEADERS=X-Orrery-Account: "
)

// idleProbeStart returns the fake clock's reading at which the idle-probe tests pass.
func idleProbeStart() time.Time {
	return time.Unix(1_800_000_000, 0)
}

// idleProbeAccounts is the LimitProbeAccounts a test hands a LimitProber: a fixed list, or err when it is set.
type idleProbeAccounts struct {
	accounts []store.Account
	err      error
}

// Accounts returns the fixed list, or the error.
func (a idleProbeAccounts) Accounts(context.Context) ([]store.Account, error) {
	return a.accounts, a.err
}

// idleProbeReadings is the LimitProbeReadings a test hands a LimitProber: a fixed list, or err when it is set.
type idleProbeReadings struct {
	readings []ledger.Reading
	err      error
}

// Readings returns the fixed list, or the error.
func (r idleProbeReadings) Readings(context.Context) ([]ledger.Reading, error) {
	return r.readings, r.err
}

// idleProbeRecorder is the Exec seam of a test. It records each command and returns err, without running claude.
type idleProbeRecorder struct {
	cmds []*exec.Cmd
	err  error
}

// record appends cmd to the recorded commands and returns the recorder's error.
func (r *idleProbeRecorder) record(_ context.Context, cmd *exec.Cmd) error {
	r.cmds = append(r.cmds, cmd)
	return r.err
}

// idleProbeClock is the clock of a test that advances it by hand. Now returns its reading.
type idleProbeClock struct {
	now time.Time
}

// Now returns the clock's reading.
func (c *idleProbeClock) Now() time.Time {
	return c.now
}

// idleProbeReadingStore is the LimitProbeReadings of a test whose readings change as it runs: its traffic and the Exec
// seam of its probes add to it.
type idleProbeReadingStore struct {
	readings []ledger.Reading
}

// Readings returns a copy of the readings held.
func (s *idleProbeReadingStore) Readings(context.Context) ([]ledger.Reading, error) {
	return slices.Clone(s.readings), nil
}

// Add appends readings to those held.
func (s *idleProbeReadingStore) Add(readings ...ledger.Reading) {
	s.readings = append(s.readings, readings...)
}

// idleProbeConfig returns the config of a LimitProber over accounts and readings, whose clock is now and whose probes
// are recorded on rec. Its claude is sh, which LookPath finds, and its environment holds a stale base URL and
// summarizer entry that a probe must replace.
func idleProbeConfig(
	t *testing.T,
	accounts LimitProbeAccounts,
	readings LimitProbeReadings,
	now func() time.Time,
	rec *idleProbeRecorder,
) LimitProberConfig {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("find sh: %v", err)
	}
	return LimitProberConfig{
		Accounts: accounts,
		Readings: readings,
		Gateway:  idleProbeGateway,
		Claude:   sh,
		Environ: []string{
			"HOME=/home/synthetic-user",
			"ANTHROPIC_BASE_URL=http://stale.invalid",
			"ORRERY_SUMMARIZER=0",
		},
		Now:  now,
		Exec: rec.record,
	}
}

// idleProbeObserved returns the unix seconds of a reading observed ago before now.
func idleProbeObserved(now time.Time, ago time.Duration) int64 {
	return now.Add(-ago).Unix()
}

// idleProbeFlag returns the value after flag in args, and whether args holds flag with a value after it.
func idleProbeFlag(args []string, flag string) (string, bool) {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return "", false
	}
	return args[i+1], true
}

// idleProbeEnvEntries returns the entries of env that begin with prefix.
func idleProbeEnvEntries(env []string, prefix string) []string {
	entries := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			entries = append(entries, entry)
		}
	}
	return entries
}

// TestLimitsProbeIdle checks that one pass probes an account whose newest reading is exactly 20 minutes old and an
// account with no readings, each once on Haiku, and no account read 5 minutes ago, with a fresh 7d_oi reading too. Each
// probe runs claude -p with one turn, no tools, no JSON schema, and the gateway and account pin in its environment.
func TestLimitsProbeIdle(t *testing.T) {
	now := idleProbeStart()
	accounts := idleProbeAccounts{accounts: []store.Account{
		{ID: idleProbeStale}, {ID: idleProbeNone}, {ID: idleProbeFresh},
	}}
	readings := idleProbeReadings{readings: []ledger.Reading{
		{Account: idleProbeStale, Claim: "5h", Observed: idleProbeObserved(now, 20*time.Minute)},
		{Account: idleProbeFresh, Claim: "5h", Observed: idleProbeObserved(now, 5*time.Minute)},
		{Account: idleProbeFresh, Claim: idleProbeWeekly, Observed: idleProbeObserved(now, 5*time.Minute)},
	}}
	rec := &idleProbeRecorder{}
	NewLimitProber(idleProbeConfig(t, accounts, readings, func() time.Time { return now }, rec)).Pass(t.Context())

	counts := make(map[string]int)
	for _, cmd := range rec.cmds {
		headers := idleProbeEnvEntries(cmd.Env, idleProbeHeaderEnv)
		base := idleProbeEnvEntries(cmd.Env, idleProbeBaseEntry)
		account, pinned := "", false
		if len(headers) == 1 {
			account, pinned = strings.CutPrefix(headers[0], idleProbeHeaderText)
		}
		if !pinned {
			t.Errorf("probe %q has no single X-Orrery-Account pin in its env: %q", cmd.Args, headers)
			continue
		}
		counts[account]++
		model, _ := idleProbeFlag(cmd.Args, "--model")
		maxTurns, _ := idleProbeFlag(cmd.Args, "--max-turns")
		tools, hasTools := idleProbeFlag(cmd.Args, "--tools")
		t.Logf("probe account=%s model=%s args=%q base=%q headers=%q", account, model, cmd.Args, base, headers)

		if !slices.Contains(cmd.Args, "-p") {
			t.Errorf("probe for %s: args %q lack -p", account, cmd.Args)
		}
		if maxTurns != "1" {
			t.Errorf("probe for %s: --max-turns is %q, want 1", account, maxTurns)
		}
		if !hasTools || tools != "" {
			t.Errorf("probe for %s: --tools is %q (present %t), want an empty string", account, tools, hasTools)
		}
		if slices.Contains(cmd.Args, "--json-schema") {
			t.Errorf("probe for %s: args %q name --json-schema", account, cmd.Args)
		}
		if model != protocol.DefaultSummaryModel {
			t.Errorf("probe for %s: --model is %q, want %q", account, model, protocol.DefaultSummaryModel)
		}
		if !slices.Equal(base, []string{idleProbeBaseEntry + idleProbeGateway}) {
			t.Errorf("probe for %s: base URL entries %q, want the gateway once", account, base)
		}
	}
	for _, id := range []string{idleProbeStale, idleProbeNone, idleProbeFresh} {
		t.Logf("account %s: %d probe(s)", id, counts[id])
	}
	want := map[string]int{idleProbeStale: 1, idleProbeNone: 1, idleProbeFresh: 0}
	for id, n := range want {
		if counts[id] != n {
			t.Errorf("account %s got %d probes, want %d", id, counts[id], n)
		}
	}
	if len(rec.cmds) != 2 {
		t.Errorf("one pass recorded %d probes, want 2", len(rec.cmds))
	}
}

// TestLimitsProbeIdleFable checks that an account whose Haiku reading is fresh but whose 7d_oi reading is 61 minutes
// old gets one Fable probe, and that an account whose 7d_oi reading is under an hour old gets none.
func TestLimitsProbeIdleFable(t *testing.T) {
	now := idleProbeStart()
	accounts := idleProbeAccounts{accounts: []store.Account{{ID: idleProbeStale}, {ID: idleProbeFresh}}}
	readings := idleProbeReadings{readings: []ledger.Reading{
		{Account: idleProbeStale, Claim: "5h", Observed: idleProbeObserved(now, 5*time.Minute)},
		{Account: idleProbeStale, Claim: idleProbeWeekly, Observed: idleProbeObserved(now, 61*time.Minute)},
		{Account: idleProbeFresh, Claim: "5h", Observed: idleProbeObserved(now, 5*time.Minute)},
		{Account: idleProbeFresh, Claim: idleProbeWeekly, Observed: idleProbeObserved(now, 59*time.Minute)},
	}}
	rec := &idleProbeRecorder{}
	NewLimitProber(idleProbeConfig(t, accounts, readings, func() time.Time { return now }, rec)).Pass(t.Context())

	if len(rec.cmds) != 1 {
		t.Fatalf("one pass recorded %d probes, want 1 Fable probe", len(rec.cmds))
	}
	cmd := rec.cmds[0]
	headers := idleProbeEnvEntries(cmd.Env, idleProbeHeaderEnv)
	if !slices.Equal(headers, []string{idleProbeHeaderText + idleProbeStale}) {
		t.Errorf("Fable probe pin %q, want the stale account", headers)
	}
	if model, _ := idleProbeFlag(cmd.Args, "--model"); model != LimitProbeFableModel {
		t.Errorf("Fable probe --model is %q, want %q", model, LimitProbeFableModel)
	}
	t.Logf("probe model=%s args=%q headers=%q", LimitProbeFableModel, cmd.Args, headers)
}

// TestLimitsProbeIdleRun checks that Run passes once at start, arms its timer for the later of the pass's return and
// one minute after the pass, probes again when the timer fires, and stops its timer when ctx ends. The timer is a
// channel the test fires, so no test waits on the wall clock.
func TestLimitsProbeIdleRun(t *testing.T) {
	now := idleProbeStart()
	accounts := idleProbeAccounts{accounts: []store.Account{{ID: idleProbeFresh}}}
	readings := idleProbeReadings{readings: []ledger.Reading{
		{Account: idleProbeFresh, Claim: "5h", Observed: idleProbeObserved(now, 5*time.Minute)},
		{Account: idleProbeFresh, Claim: idleProbeWeekly, Observed: idleProbeObserved(now, 5*time.Minute)},
	}}
	rec := &idleProbeRecorder{}
	armed := make(chan time.Duration, 2)
	fire := make(chan time.Time)
	stops := 0
	cfg := idleProbeConfig(t, accounts, readings, func() time.Time { return now }, rec)
	cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		armed <- d
		return fire, func() bool {
			stops++
			return true
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewLimitProber(cfg).Run(ctx)
	}()

	// The account's Haiku probe is due in 15 minutes, which is later than the minute after the pass.
	if got := <-armed; got != 15*time.Minute {
		t.Fatalf("first timer armed for %v, want 15m0s", got)
	}
	if len(rec.cmds) != 0 {
		t.Fatalf("first pass recorded %d probes, want none", len(rec.cmds))
	}

	// At 15 minutes the reading is 20 minutes old: the pass probes once, and the next moment is already past, so the
	// timer is armed for one minute after the pass.
	now = now.Add(15 * time.Minute)
	fire <- now
	if got := <-armed; got != time.Minute {
		t.Fatalf("second timer armed for %v, want 1m0s", got)
	}
	if len(rec.cmds) != 1 {
		t.Fatalf("second pass recorded %d probes, want 1", len(rec.cmds))
	}

	cancel()
	<-done
	if stops != 2 {
		t.Errorf("Run stopped %d timers, want both armed timers stopped", stops)
	}
}

// TestLimitsProbeIdleFailures checks that a failed read is logged and probes nothing, and that a failed probe is logged
// and the pass goes on to the next account.
func TestLimitsProbeIdleFailures(t *testing.T) {
	now := idleProbeStart()
	clock := func() time.Time { return now }

	t.Run("accounts read fails", func(t *testing.T) {
		rec := &idleProbeRecorder{}
		var logs bytes.Buffer
		failing := idleProbeAccounts{err: errors.New("synthetic accounts failure")}
		cfg := idleProbeConfig(t, failing, idleProbeReadings{}, clock, rec)
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		got := NewLimitProber(cfg).Pass(t.Context())
		if !got.Equal(now.Add(time.Minute)) {
			t.Errorf("Pass returned %v, want one minute after now", got)
		}
		if len(rec.cmds) != 0 {
			t.Errorf("a failed accounts read recorded %d probes, want none", len(rec.cmds))
		}
		if !strings.Contains(logs.String(), "synthetic accounts failure") {
			t.Errorf("log %q does not carry the accounts error", logs.String())
		}
	})

	t.Run("readings read fails", func(t *testing.T) {
		rec := &idleProbeRecorder{}
		var logs bytes.Buffer
		accounts := idleProbeAccounts{accounts: []store.Account{{ID: idleProbeNone}}}
		cfg := idleProbeConfig(
			t,
			accounts,
			idleProbeReadings{err: errors.New("synthetic readings failure")},
			clock,
			rec,
		)
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		got := NewLimitProber(cfg).Pass(t.Context())
		if !got.Equal(now.Add(time.Minute)) {
			t.Errorf("Pass returned %v, want one minute after now", got)
		}
		if len(rec.cmds) != 0 {
			t.Errorf("a failed readings read recorded %d probes, want none", len(rec.cmds))
		}
		if !strings.Contains(logs.String(), "synthetic readings failure") {
			t.Errorf("log %q does not carry the readings error", logs.String())
		}
	})

	t.Run("probe fails and the pass goes on", func(t *testing.T) {
		rec := &idleProbeRecorder{err: errors.New("synthetic claude failure")}
		var logs bytes.Buffer
		accounts := idleProbeAccounts{accounts: []store.Account{{ID: idleProbeStale}, {ID: idleProbeNone}}}
		cfg := idleProbeConfig(t, accounts, idleProbeReadings{}, clock, rec)
		cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
		NewLimitProber(cfg).Pass(t.Context())
		if len(rec.cmds) != 2 {
			t.Fatalf("a failed probe left %d probes sent, want the pass to reach both accounts", len(rec.cmds))
		}
		for _, id := range []string{idleProbeStale, idleProbeNone} {
			if !strings.Contains(logs.String(), "account="+id) {
				t.Errorf("log %q does not name failed account %s", logs.String(), id)
			}
		}
		if !strings.Contains(logs.String(), "synthetic claude failure") {
			t.Errorf("log %q does not carry the probe error", logs.String())
		}
	})
}
