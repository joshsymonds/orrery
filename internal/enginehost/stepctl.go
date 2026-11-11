package enginehost

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// stepStoppedEnd is the end a step ended by Stop stores.
const stepStoppedEnd = "stopped"

// stepRestartedEnd is the end Reconcile stores on each open step row.
const stepRestartedEnd = "engine_restarted"

// stepWaitPoll is how often Wait reads the rows it waits on.
const stepWaitPoll = 20 * time.Millisecond

// Stop ends the step with id as stopped. A running loop is canceled, which kills its gate, and Stop waits for the loop
// to return before it reads the row, so the end is stored once. The step then ends through finish with a context that
// has not been canceled: its session is stopped and its worktree removed. A step that has already ended is left as it
// is and Stop returns nil. An open row with no loop is ended the same way. An id with no row returns an error matching
// store.ErrNoStep.
func (s *Steps) Stop(ctx context.Context, id string) error {
	s.mu.Lock()
	cancel, run := s.loops[id], s.runs[id]
	s.mu.Unlock()
	if run != nil {
		cancel()
		<-run.runDone
	}
	row, err := s.Status(ctx, id)
	if err != nil {
		return err
	}
	if row.End != "" {
		return nil
	}
	if run == nil {
		run = s.rowRun(row)
	}
	run.finish(context.WithoutCancel(ctx), stepStoppedEnd, "", "")
	return nil
}

// rowRun returns a stepRun for an open step row with no loop, so Stop can end it through finish. Its cleanup removes the
// row's worktree when the row has one.
func (s *Steps) rowRun(row store.StepRow) *stepRun {
	cfg := stepRunConfig{id: row.ID, sessions: s.sessions, writer: s.writer, logger: s.logger}
	if row.Worktree != "" {
		cfg.cleanup = func(ctx context.Context) error {
			return removeStepWorktree(ctx, row.Workspace, row.Worktree)
		}
	}
	return newStepRun(cfg)
}

// Wait returns the first step among ids to end. It reads the row of every id first, and an id with no row returns an
// error matching store.ErrNoStep. If a listed step has already ended, Wait returns the one that ended first at once.
// Otherwise it reads the rows every stepWaitPoll until one ends. The rows returned are every listed row in the order of
// ids. At timeout Wait returns no ended step and a nil error, and when ctx ends it returns ctx's error.
func (s *Steps) Wait(
	ctx context.Context, ids []string, timeout time.Duration,
) (*store.StepRow, []store.StepRow, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(stepWaitPoll)
	defer ticker.Stop()
	for {
		rows, err := s.readSteps(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		if ended := earliestEnded(rows); ended != nil {
			return ended, rows, nil
		}
		select {
		case <-ctx.Done():
			return nil, rows, fmt.Errorf("wait for steps: %w", ctx.Err())
		case <-timer.C:
			return nil, rows, nil
		case <-ticker.C:
		}
	}
}

// readSteps returns the row of each id in ids, in order. An id with no row returns an error matching store.ErrNoStep.
func (s *Steps) readSteps(ctx context.Context, ids []string) ([]store.StepRow, error) {
	rows := make([]store.StepRow, 0, len(ids))
	for _, id := range ids {
		row, err := s.Status(ctx, id)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// earliestEnded returns the ended row of rows with the earliest end, or nil when none has ended. A tie keeps the row
// listed first.
func earliestEnded(rows []store.StepRow) *store.StepRow {
	var first *store.StepRow
	for i := range rows {
		if rows[i].End == "" {
			continue
		}
		if first == nil || rows[i].Ended.Before(first.Ended) {
			first = &rows[i]
		}
	}
	return first
}

// Reconcile settles the open step rows that have no loop in this Steps, as a restart leaves them. Each such row ends
// engine_restarted and then has its worktree removed. Nothing is notified and no session is stopped, since a restart
// leaves no session to stop. A row whose loop is running is left alone. A worktree that cannot be removed is logged and
// the pass goes on, so one bad row never keeps the others open. It returns the first read or write error.
func (s *Steps) Reconcile(ctx context.Context) error {
	open, err := s.store.OpenSteps(ctx)
	if err != nil {
		return fmt.Errorf("reconcile steps: %w", err)
	}
	for _, row := range open {
		s.mu.Lock()
		_, live := s.loops[row.ID]
		s.mu.Unlock()
		if live {
			continue
		}
		if writeErr := s.writer.Commit(ctx, row.ID, store.StepEnd{ID: row.ID, End: stepRestartedEnd}); writeErr != nil {
			return fmt.Errorf("reconcile step %s: %w", row.ID, writeErr)
		}
		if row.Worktree != "" {
			if removeErr := removeStepWorktree(ctx, row.Workspace, row.Worktree); removeErr != nil {
				s.logger.WarnContext(ctx, "remove worktree of restarted step", "step", row.ID, "err", removeErr)
			}
		}
	}
	return nil
}

// Close cancels every loop, which kills any gate a loop is running, and waits until no loop is left. Each step row stays
// open, for Reconcile to settle after a restart.
func (s *Steps) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.loops) > 0 {
		for _, cancel := range s.loops {
			cancel()
		}
		s.idle.Wait()
	}
}
