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
// clock, Tick is the channel each pass after the first waits on, and Logger logs a failed resume or close. A nil Logger
// discards.
type DuePassConfig struct {
	Store  DueStore
	Steps  DueSteps
	Now    func() time.Time
	Tick   <-chan time.Time
	Logger *slog.Logger
}

// DuePass resumes each due row whose reset has passed by dueGrace. It keeps nothing between passes: the rows are the
// state a pass reads.
type DuePass struct {
	cfg DuePassConfig
}

// NewDuePass returns a DuePass for cfg.
func NewDuePass(cfg DuePassConfig) *DuePass {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &DuePass{cfg: cfg}
}

// Run passes once at start and once at each tick until ctx ends.
func (d *DuePass) Run(ctx context.Context) {
	d.Pass(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Tick:
			d.Pass(ctx)
		}
	}
}

// Pass resumes each due row whose reset is at least dueGrace before the clock's time, oldest reset first. A row before
// its time is left alone. A failed read is logged and the pass changes nothing.
func (d *DuePass) Pass(ctx context.Context) {
	rows, err := d.cfg.Store.DueResumes(ctx)
	if err != nil {
		d.cfg.Logger.ErrorContext(ctx, "read due resumes", "error", err)
		return
	}
	now := d.cfg.Now()
	for _, row := range rows {
		if now.Before(row.Resets.Add(dueGrace)) {
			continue
		}
		switch row.Kind {
		case dueKindStep:
			d.resumeStep(ctx, row)
		case dueKindEpic:
			d.resumeEpic(ctx, row)
		default:
			d.cfg.Logger.ErrorContext(ctx, "due resume of unknown kind", "id", row.ID, "kind", row.Kind)
		}
	}
}

// resumeStep resumes the limited step of row for the reset row names. A refusal by the step cap or by the step's gate
// leaves the row due for the next pass. A step that is not limited at that reset, has ended, or has no row is closed
// with no resume. Any other error is logged and leaves the row due. A successful resume is closed by its own write.
func (d *DuePass) resumeStep(ctx context.Context, row store.DueResume) {
	err := d.cfg.Steps.AutoResume(ctx, row.Target, row.Resets)
	switch {
	case err == nil:
	case errors.Is(err, ErrCapReached), errors.Is(err, ErrStepGating):
	case errors.Is(err, ErrStepNotLimited), errors.Is(err, ErrStepNotRunning), errors.Is(err, store.ErrNoStep):
		d.closeRow(ctx, row)
	default:
		d.cfg.Logger.ErrorContext(ctx, "resume due step", "step", row.Target, "error", err)
	}
}

// resumeEpic resumes the epic of row through ResumeDueEpic, which closes the row whether or not it resumed. A failed
// write is logged and leaves the row due.
func (d *DuePass) resumeEpic(ctx context.Context, row store.DueResume) {
	if _, err := d.cfg.Store.ResumeDueEpic(ctx, row.ID); err != nil {
		d.cfg.Logger.ErrorContext(ctx, "resume due epic", "epic", row.Target, "error", err)
	}
}

// closeRow marks row done without resuming anything. A failed close is logged.
func (d *DuePass) closeRow(ctx context.Context, row store.DueResume) {
	if err := d.cfg.Store.CloseDueResume(ctx, row.ID); err != nil {
		d.cfg.Logger.ErrorContext(ctx, "close due resume", "id", row.ID, "error", err)
	}
}
