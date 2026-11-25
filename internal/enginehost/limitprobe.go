package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
	"time"

	"github.com/joshsymonds/orrery/internal/childproc"
	"github.com/joshsymonds/orrery/internal/ledger"
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

// LimitProber probes idle accounts so their limit readings stay fresh. No code calls /api/oauth/usage. A probe is a
// one-turn, toolless, schemaless claude -p run at the gateway, pinned to its account with ANTHROPIC_CUSTOM_HEADERS.
type LimitProber struct {
	cfg LimitProberConfig
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
	return &LimitProber{cfg: cfg}
}

// Pass probes each due account at most once and returns the earliest moment a probe may next be due.
func (p *LimitProber) Pass(_ context.Context) time.Time {
	return p.cfg.Now().Add(LimitProbeHaikuEvery)
}

// Run passes at start, then at each moment Pass returns, but no sooner than one minute after a pass, until ctx ends.
func (p *LimitProber) Run(ctx context.Context) {
	<-ctx.Done()
}

// limitProbeExec starts cmd in a process group of its own and waits for it. When ctx ends first it kills the group,
// so no claude a probe started outlives the probe, and it returns once the killed command has exited.
func limitProbeExec(ctx context.Context, cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = limitProbeKillGrace
	waited, err := childproc.Start(cmd)
	if err != nil {
		return fmt.Errorf("limitprobe: start claude: %w", err)
	}
	select {
	case waitErr := <-waited:
		return waitErr
	case <-ctx.Done():
		killErr := limitProbeKillGroup(cmd.Process.Pid)
		waitErr := <-waited
		return errors.Join(fmt.Errorf("limitprobe: claude run: %w", ctx.Err()), killErr, waitErr)
	}
}

// limitProbeKillGroup kills every process in the group pgid of a probe whose context ended. A group that already
// exited is not an error.
func limitProbeKillGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("limitprobe: kill claude group %d: %w", pgid, err)
	}
	return nil
}
