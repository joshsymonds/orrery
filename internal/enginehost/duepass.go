package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// dueGrace is how long past a due row's reset a pass waits before it resumes the row.
const dueGrace = 60 * time.Second

// dueRetry is how long after a pass a row it leaves due for a gate or a logged error waits for the next pass.
const dueRetry = 5 * time.Second

// The kinds of due row, as the store names them.
const (
	dueKindStep = "step"
	dueKindEpic = "epic"
)

// DueStore reads the due rows and settles them. *store.Store satisfies it.
type DueStore interface {
	DueResumes(ctx context.Context) ([]store.DueResume, error)
	CloseDueResume(ctx context.Context, id string) error
	ResumeDueEpic(ctx context.Context, id string) (bool, error)
}

// DueSteps resumes the limited step with id for the usage limit that resets at resets. *Steps satisfies it.
type DueSteps interface {
	AutoResume(ctx context.Context, id string, resets time.Time) error
}

// DuePassConfig configures DuePass. Store reads and settles the due rows, Steps resumes the limited steps, Now is the
// clock, NewTimer arms a timer that sends on its channel once, after a duration, and returns the timer's stop, and Logger
// logs a failed resume or close. A nil NewTimer wraps time.NewTimer, and a nil Logger discards.
type DuePassConfig struct {
	Store    DueStore
	Steps    DueSteps
	Now      func() time.Time
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
	Logger   *slog.Logger
}

// DuePass resumes each due row whose reset has passed by dueGrace. It keeps nothing between passes: the rows are the
// state a pass reads.
type DuePass struct {
	cfg DuePassConfig
	// calls counts the passes under way (see callsInFlight).
	calls callsInFlight
	// wakes holds one pending wake, so a wake that comes while one is pending is absorbed (see Wake).
	wakes chan struct{}
}

// NewDuePass returns a DuePass for cfg.
func NewDuePass(cfg DuePassConfig) *DuePass {
	if cfg.NewTimer == nil {
		cfg.NewTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &DuePass{cfg: cfg, wakes: make(chan struct{}, 1)}
}

// dueTimer is the one timer Run arms: the moment it is armed for, the channel it fires on, and its stop.
type dueTimer struct {
	at   time.Time
	fire <-chan time.Time
	stop func() bool
}

// Run passes once at start, then once on each Wake and each time its timer fires, until ctx ends. After each pass it
// arms one timer for the earliest moment a pass is next needed (see pass). It stops that timer when the moment changes
// and when it returns. A timer that fires before anything is due passes, finds nothing due, and arms the time left.
func (d *DuePass) Run(ctx context.Context) {
	var timer dueTimer
	defer func() {
		if timer.stop != nil {
			timer.stop()
		}
	}()
	next, due := d.pass(ctx)
	d.rearm(&timer, next, due)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.wakes:
		case <-timer.fire:
			timer.fire = nil
		}
		next, due = d.pass(ctx)
		d.rearm(&timer, next, due)
	}
}

// rearm leaves timer as it is when it is armed for next already, and when next is due. Otherwise it stops timer, and arms
// a timer for next, measured from the clock's time now, or arms none when nothing is due.
func (d *DuePass) rearm(timer *dueTimer, next time.Time, due bool) {
	if due && timer.fire != nil && next.Equal(timer.at) {
		return
	}
	if timer.stop != nil {
		timer.stop()
	}
	*timer = dueTimer{}
	if !due {
		return
	}
	fire, stop := d.cfg.NewTimer(next.Sub(d.cfg.Now()))
	*timer = dueTimer{at: next, fire: fire, stop: stop}
}

// Wake asks Run for a pass, without blocking. A wake that comes while one is pending is absorbed by that one. It takes no
// lock, so it is safe from any goroutine, including under a lock the caller holds.
func (d *DuePass) Wake() {
	select {
	case d.wakes <- struct{}{}:
	default:
	}
}

// Pass resumes each due row whose reset is at least dueGrace before the clock's time, oldest reset first. A row before
// its time is left alone. A failed read is logged and the pass changes nothing.
func (d *DuePass) Pass(ctx context.Context) {
	d.pass(ctx)
}

// pass is Pass. It also returns the earliest moment a pass is next needed, and whether there is one: the moment of each
// row still before its time, and now plus dueRetry for each row it leaves due on a gate or a logged error, or when the
// read fails.
func (d *DuePass) pass(ctx context.Context) (time.Time, bool) {
	defer d.calls.enter()()
	now := d.cfg.Now()
	rows, err := d.cfg.Store.DueResumes(ctx)
	if err != nil {
		d.cfg.Logger.ErrorContext(ctx, "read due resumes", "error", err)
		return now.Add(dueRetry), true
	}
	var next dueMoment
	for _, row := range rows {
		if moment := row.Resets.Add(dueGrace); now.Before(moment) {
			next.add(moment)
			continue
		}
		var retry bool
		switch row.Kind {
		case dueKindStep:
			retry = d.resumeStep(ctx, row)
		case dueKindEpic:
			retry = d.resumeEpic(ctx, row)
		default:
			d.cfg.Logger.ErrorContext(ctx, "due resume of unknown kind", "id", row.ID, "kind", row.Kind)
			retry = true
		}
		if retry {
			next.add(now.Add(dueRetry))
		}
	}
	return next.at, next.set
}

// dueMoment is the earliest moment a pass is next needed, once one is added.
type dueMoment struct {
	at  time.Time
	set bool
}

// add makes moment the earliest when none is held, or when it is before the one held.
func (m *dueMoment) add(moment time.Time) {
	if !m.set || moment.Before(m.at) {
		m.at, m.set = moment, true
	}
}

// resumeStep resumes the limited step of row for the reset row names. A refusal by the step's gate leaves the row due for
// a retry, which it reports, and so does any other error, which is logged. A step that is not limited at that reset, has ended, or has no row is closed with no
// resume. A successful resume is closed by its own write.
func (d *DuePass) resumeStep(ctx context.Context, row store.DueResume) (retry bool) {
	err := d.cfg.Steps.AutoResume(ctx, row.Target, row.Resets)
	switch {
	case err == nil:
	case errors.Is(err, ErrStepGating):
		return true
	case errors.Is(err, ErrStepNotLimited), errors.Is(err, ErrStepNotRunning), errors.Is(err, store.ErrNoStep):
		return d.closeRow(ctx, row)
	default:
		d.cfg.Logger.ErrorContext(ctx, "resume due step", "step", row.Target, "error", err)
		return true
	}
	return false
}

// resumeEpic resumes the epic of row through ResumeDueEpic, which closes the row whether or not it resumed. A failed
// write is logged and leaves the row due, which it reports as a retry.
func (d *DuePass) resumeEpic(ctx context.Context, row store.DueResume) (retry bool) {
	if _, err := d.cfg.Store.ResumeDueEpic(ctx, row.ID); err != nil {
		d.cfg.Logger.ErrorContext(ctx, "resume due epic", "epic", row.Target, "error", err)
		return true
	}
	return false
}

// closeRow marks row done without resuming anything. A failed close is logged, and leaves the row due, which it reports
// as a retry.
func (d *DuePass) closeRow(ctx context.Context, row store.DueResume) (retry bool) {
	if err := d.cfg.Store.CloseDueResume(ctx, row.ID); err != nil {
		d.cfg.Logger.ErrorContext(ctx, "close due resume", "id", row.ID, "error", err)
		return true
	}
	return false
}
