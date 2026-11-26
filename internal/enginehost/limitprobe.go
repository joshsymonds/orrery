package enginehost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/childproc"
	"github.com/joshsymonds/orrery/internal/holderhost"
	"github.com/joshsymonds/orrery/internal/ledger"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/store"
)

// LimitProbeHaikuEvery is the interval at which an idle account is probed on the Haiku model.
const LimitProbeHaikuEvery = 20 * time.Minute

// LimitProbeFableEvery is the interval at which an idle account is probed on LimitProbeFableModel.
const LimitProbeFableEvery = time.Hour

// LimitProbeFableModel is the model a Fable probe names.
const LimitProbeFableModel = "claude-fable-5-2"

// limitProbeDefaultTimeout applies when LimitProberConfig leaves Timeout zero.
const limitProbeDefaultTimeout = 60 * time.Second

// limitProbeKillGrace bounds how long a killed claude may hold its output pipes open before the wait returns.
const limitProbeKillGrace = 2 * time.Second

// limitProbeMinGap is the least time between a pass and the next one, and how long after a pass that could not read its
// accounts or readings the next one comes.
const limitProbeMinGap = time.Minute

// limitProbeWeeklyClaim is the claim of the reading that says when an account's weekly limits were last read.
const limitProbeWeeklyClaim = "7d_oi"

// limitProbePrompt is the one word a probe sends on stdin.
const limitProbePrompt = "ping"

// limitProbePinHeader is the gateway's header that pins a request to a stored account (internal/gateway/pin.go).
const limitProbePinHeader = "X-Orrery-Account"

// The environment entries a probe replaces. Each is dropped from the environment it starts from.
const (
	limitProbeBaseURLPrefix = "ANTHROPIC_BASE_URL="
	limitProbeHeadersPrefix = "ANTHROPIC_CUSTOM_HEADERS="
)

// LimitProbeAccounts lists the accounts a pass may probe. *store.Store satisfies it.
type LimitProbeAccounts interface {
	Accounts(ctx context.Context) ([]store.Account, error)
}

// LimitProbeReadings reads the limit readings, which say when each account's limits were last read. *ledger.Reader
// satisfies it.
type LimitProbeReadings interface {
	Readings(ctx context.Context) ([]ledger.Reading, error)
}

// LimitProberConfig configures a LimitProber. Accounts and Readings name the accounts a pass may probe and when
// each was last read. Gateway, Claude and Environ say where and how a probe runs. Now is the clock, and NewTimer
// arms a timer that sends on its channel once, after a duration, and returns the timer's stop, as DuePassConfig's
// does. Exec runs one probe command, Timeout bounds one probe, and Logger logs a failed probe.
type LimitProberConfig struct {
	// Accounts lists the accounts a pass may probe.
	Accounts LimitProbeAccounts
	// Readings reads the limit readings, which say when each account's limits were last read.
	Readings LimitProbeReadings
	// Gateway is the gateway's base URL, which every probe is sent to.
	Gateway string
	// Claude is the claude binary a probe runs.
	Claude string
	// Environ is the environment probes start from.
	Environ []string
	// Now is the clock. A nil Now means time.Now.
	Now func() time.Time
	// NewTimer arms a timer that sends on its channel once, after a duration, and returns the timer's stop. A nil
	// NewTimer wraps time.NewTimer.
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
	// Exec runs one probe command. A nil Exec starts the command and waits for it, killing its process group when ctx
	// ends.
	Exec func(ctx context.Context, cmd *exec.Cmd) error
	// Timeout bounds one probe. Zero means 60 seconds.
	Timeout time.Duration
	// Logger logs a failed probe. A nil Logger discards.
	Logger *slog.Logger
}

// LimitProber probes idle accounts so their limit readings stay fresh. The readings come from the probes' own traffic
// through the gateway, never from a usage endpoint. A probe is a one-turn, toolless, schemaless claude -p run at the
// gateway, pinned to its account with ANTHROPIC_CUSTOM_HEADERS. The prober keeps in memory the last attempt at each
// probe, by model and account, so a probe that fails, or exits without writing a reading, still waits its window.
type LimitProber struct {
	cfg LimitProberConfig
	// mu guards attempts, which Pass reads and writes.
	mu sync.Mutex
	// attempts holds the clock reading at which each account was last probed, by model and then by account.
	attempts map[string]map[string]time.Time
}

// NewLimitProber returns a LimitProber for cfg. It applies the defaults cfg leaves nil or zero: time.Now as the
// clock, a timer that wraps time.NewTimer, a Logger that discards, the process-group Exec, and a 60-second Timeout.
func NewLimitProber(cfg LimitProberConfig) *LimitProber {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewTimer == nil {
		cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	if cfg.Exec == nil {
		cfg.Exec = limitProbeExec
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = limitProbeDefaultTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &LimitProber{cfg: cfg, attempts: make(map[string]map[string]time.Time)}
}

// Pass probes each account whose probe is due and returns the earliest moment any account's probe next becomes due. A
// Haiku probe is due when no reading of the account is under LimitProbeHaikuEvery old and no Haiku attempt is either,
// and a Fable probe when no 7d_oi reading is under LimitProbeFableEvery old and no Fable attempt is either; Haiku goes
// first, so an account gets at most one probe a pass. An attempt is recorded before its probe runs, whether or not the
// probe succeeds. An account's moment is worked out before its own probe is claimed, so a pass that probes an account
// returns a moment no later than that pass, and the next pass finds the probe's window still open. A failed read is
// logged, probes nothing, and returns one minute after now.
func (p *LimitProber) Pass(ctx context.Context) time.Time {
	now := p.cfg.Now()
	accounts, err := p.cfg.Accounts.Accounts(ctx)
	if err != nil {
		p.cfg.Logger.ErrorContext(ctx, "limitprobe: read accounts", "error", err)
		return now.Add(limitProbeMinGap)
	}
	p.forget(accounts)
	readings, err := p.cfg.Readings.Readings(ctx)
	if err != nil {
		p.cfg.Logger.ErrorContext(ctx, "limitprobe: read readings", "error", err)
		return now.Add(limitProbeMinGap)
	}
	if len(accounts) == 0 {
		return now.Add(LimitProbeHaikuEvery)
	}
	newest, weekly := limitProbeNewest(readings)
	var next time.Time
	for i, account := range accounts {
		model, at := p.claim(now, newest, weekly, account.ID)
		if model != "" {
			p.probe(ctx, account.ID, model)
		}
		if i == 0 || at.Before(next) {
			next = at
		}
	}
	return next
}

// claim decides which probe of account is due at now. When one is, it records that probe's attempt at cfg.Now() under
// the lock, so the attempt is in place before the probe's command starts and two passes cannot both claim the probe. It
// returns the model of the due probe, or "" when none is due, and the moment the account's next probe is due, worked
// out before this claim.
func (p *LimitProber) claim(now time.Time, newest, weekly map[string]time.Time, account string) (string, time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	haikuAt := limitProbeDueAt(now, newest, p.attempts[protocol.DefaultSummaryModel], account, LimitProbeHaikuEvery)
	fableAt := limitProbeDueAt(now, weekly, p.attempts[LimitProbeFableModel], account, LimitProbeFableEvery)
	at := haikuAt
	if fableAt.Before(at) {
		at = fableAt
	}
	var model string
	switch {
	case !now.Before(haikuAt):
		model = protocol.DefaultSummaryModel
	case !now.Before(fableAt):
		model = LimitProbeFableModel
	default:
		return "", at
	}
	if p.attempts[model] == nil {
		p.attempts[model] = make(map[string]time.Time)
	}
	p.attempts[model][account] = p.cfg.Now()
	return model, at
}

// forget drops the attempts of every account not in accounts, so the record holds only accounts Pass still lists.
func (p *LimitProber) forget(accounts []store.Account) {
	listed := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		listed[account.ID] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for model, byAccount := range p.attempts {
		for account := range byAccount {
			if !listed[account] {
				delete(byAccount, account)
			}
		}
		if len(byAccount) == 0 {
			delete(p.attempts, model)
		}
	}
}

// limitProbeNewest returns the newest observation of each account's readings, and of its 7d_oi readings, by account
// id. Observed is unix seconds, and Dropped is ignored.
func limitProbeNewest(readings []ledger.Reading) (newest, weekly map[string]time.Time) {
	newest = make(map[string]time.Time)
	weekly = make(map[string]time.Time)
	for _, r := range readings {
		at := time.Unix(r.Observed, 0)
		limitProbeLatest(newest, r.Account, at)
		if r.Claim == limitProbeWeeklyClaim {
			limitProbeLatest(weekly, r.Account, at)
		}
	}
	return newest, weekly
}

// limitProbeLatest sets latest[account] to at when no observation of account is held yet, or when at is later.
func limitProbeLatest(latest map[string]time.Time, account string, at time.Time) {
	if prev, ok := latest[account]; !ok || at.After(prev) {
		latest[account] = at
	}
}

// limitProbeNext returns the moment a probe spaced every apart from the newest observation of account in latest is
// next allowed. An account with no observation is allowed at now.
func limitProbeNext(now time.Time, latest map[string]time.Time, account string, every time.Duration) time.Time {
	last, ok := latest[account]
	if !ok {
		return now
	}
	return last.Add(every)
}

// limitProbeDueAt returns the moment a probe of account, spaced every apart, is next due: the later of the moment its
// readings allow, in latest, and the moment its last attempt allows, in attempts.
func limitProbeDueAt(
	now time.Time,
	latest, attempts map[string]time.Time,
	account string,
	every time.Duration,
) time.Time {
	at := limitProbeNext(now, latest, account, every)
	if attempt := limitProbeNext(now, attempts, account, every); attempt.After(at) {
		at = attempt
	}
	return at
}

// probe runs one probe of account on model, whose attempt claim has already recorded. A failed probe is logged at Warn
// with the account, the model and the error, and is never returned, so the pass goes on.
func (p *LimitProber) probe(ctx context.Context, account, model string) {
	if err := p.run(ctx, account, model); err != nil {
		p.cfg.Logger.WarnContext(ctx, "limitprobe: probe failed", "account", account, "model", model, "error", err)
	}
}

// run runs one probe of account on model through cfg.Exec, under a context that ends after cfg.Timeout. The probe is a
// one-turn claude -p with no tools and no JSON schema, whose user CLAUDE.md is excluded as Haiku's calls exclude it.
func (p *LimitProber) run(ctx context.Context, account, model string) error {
	path, err := exec.LookPath(p.cfg.Claude)
	if err != nil {
		return fmt.Errorf("limitprobe: find claude: %w", err)
	}
	memory, err := holderhost.UserMemoryOf(p.cfg.Environ)
	if err != nil {
		return fmt.Errorf("limitprobe: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	cmd := &exec.Cmd{
		Path: path,
		Args: []string{
			path, "-p", "--model", model,
			"--settings", stamp.Settings(true, nil, []string{memory}),
			"--no-session-persistence", "--tools", "", "--strict-mcp-config", "--max-turns", "1",
		},
		Dir:   "/",
		Env:   limitProbeEnv(p.cfg.Environ, p.cfg.Gateway, account),
		Stdin: strings.NewReader(limitProbePrompt),
	}
	return p.cfg.Exec(ctx, cmd)
}

// limitProbeEnv returns environ without the entries a probe sets itself, plus the gateway as the base URL, the account
// as the pinned account header, and the summarizer sentinel.
func limitProbeEnv(environ []string, gateway, account string) []string {
	env := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		if strings.HasPrefix(entry, limitProbeBaseURLPrefix) || strings.HasPrefix(entry, limitProbeHeadersPrefix) ||
			strings.HasPrefix(entry, protocol.EnvSummarizer+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		limitProbeBaseURLPrefix+gateway,
		limitProbeHeadersPrefix+limitProbePinHeader+": "+account,
		protocol.EnvSummarizer+"="+protocol.SummarizerValue,
	)
}

// Run passes at start, then again each time its timer fires, until ctx ends. After each pass it arms a timer for the
// later of the moment the pass returned and one minute after the pass, and it stops that timer when it fires and when
// ctx ends.
func (p *LimitProber) Run(ctx context.Context) {
	next := p.Pass(ctx)
	for {
		now := p.cfg.Now()
		at := next
		if floor := now.Add(limitProbeMinGap); at.Before(floor) {
			at = floor
		}
		fire, stop := p.cfg.NewTimer(at.Sub(now))
		select {
		case <-ctx.Done():
			stop()
			return
		case <-fire:
			stop()
		}
		next = p.Pass(ctx)
	}
}

// limitProbeExec starts cmd in a process group of its own and waits for it. When ctx ends first it kills the group,
// so no claude a probe started outlives the probe, and it returns once the killed command has exited. A run that fails
// returns its wait error with the trimmed stderr of claude.
func limitProbeExec(ctx context.Context, cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = limitProbeKillGrace
	waited, err := childproc.Start(cmd)
	if err != nil {
		return fmt.Errorf("limitprobe: start claude: %w", err)
	}
	select {
	case waitErr := <-waited:
		if waitErr != nil {
			return limitProbeRunError(waitErr, &stderr)
		}
		return nil
	case <-ctx.Done():
		killErr := limitProbeKillGroup(cmd.Process.Pid)
		waitErr := <-waited
		return errors.Join(limitProbeRunError(ctx.Err(), &stderr), killErr, waitErr)
	}
}

// limitProbeRunError returns err as the failure of a claude run, with the trimmed stderr of that run when it wrote any.
func limitProbeRunError(err error, stderr *bytes.Buffer) error {
	if trimmed := strings.TrimSpace(stderr.String()); trimmed != "" {
		return fmt.Errorf("limitprobe: claude run: %w: %s", err, trimmed)
	}
	return fmt.Errorf("limitprobe: claude run: %w", err)
}

// limitProbeKillGroup kills every process in the group pgid of a probe whose context ended. A group that already
// exited is not an error.
func limitProbeKillGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("limitprobe: kill claude group %d: %w", pgid, err)
	}
	return nil
}
