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

// Readopt registers each open step row with adopter before the manager's Reconcile runs, so the manager re-adopts the
// holder of each step whose run could be rebuilt. Each run is rebuilt as Start builds one, from the row and from the turn
// end its session last stored. That turn end is read here, before the restarted holders hand anything over, so it is
// the turn end stored before the restart. The run is registered with adopter as its step's observer, and Reconcile starts
// its loop once adopter reports the step live. A row whose run cannot be rebuilt is logged and left unregistered, so
// Reconcile ends it engine_restarted. It returns the error of reading the open rows.
func (s *Steps) Readopt(ctx context.Context, adopter StepAdopter) error {
	open, err := s.store.OpenSteps(ctx)
	if err != nil {
		return fmt.Errorf("readopt steps: %w", err)
	}
	s.mu.Lock()
	s.adopter = adopter
	s.mu.Unlock()
	for _, row := range open {
		run, rebuildErr := s.readoptRun(ctx, row)
		if rebuildErr != nil {
			s.logger.ErrorContext(ctx, "re-adopt step", "step", row.ID, "err", rebuildErr)
			continue
		}
		adopter.ExpectStep(row.ID, run)
		s.mu.Lock()
		s.adopted[row.ID] = run
		s.mu.Unlock()
	}
	return nil
}

// readoptRun rebuilds the run of an open step row as Start builds the run of a started step, restoring it from the turn
// end its session last stored. A named gate's grant is requested from the pools of the row's workspace, and a read-only
// step's worktree is removed when the step ends. It returns an error when the turn end cannot be read, or when the run's
// grant or haiku caller cannot be bound.
func (s *Steps) readoptRun(ctx context.Context, row store.StepRow) (*stepRun, error) {
	turn, found, err := s.store.StepTurn(ctx, row.ID)
	if err != nil {
		return nil, fmt.Errorf("read turn end of step %s: %w", row.ID, err)
	}
	cfg := stepRunConfig{
		id: row.ID, schema: row.Schema, doneValue: row.DoneValue, sessions: s.sessions, writer: s.writer,
		logger: s.logger, writing: !row.ReadOnly, start: row.Revision, owned: row.Owned, gate: row.Gate,
		gateLimit: row.GateLimit, gateName: row.GateName, gateSource: row.GateSource, pools: s.pools,
		environ: s.environ, prompt: row.Prompt, now: s.now, modelLimit: row.ModelLimit, stateDir: s.gateStateDir,
		scope: s.gateScope, restore: &stepRestore{Row: row, Turn: turn, Found: found},
	}
	folder := row.Workspace
	if row.ReadOnly {
		folder = row.Worktree
		cfg.cleanup = func(ctx context.Context) error {
			return removeStepWorktree(ctx, row.Workspace, row.Worktree)
		}
	} else {
		cfg.dir = row.Workspace
	}
	if len(row.Gate) > 0 {
		cfg.grant, err = stepGrant(ctx, row.Workspace, row.GateName, row.Gate, storedGrantNeeds(row.GateNeeds),
			stepGetenv(s.environ))
		if err != nil {
			return nil, err
		}
	}
	err = s.bindHaiku(&cfg, folder)
	if err != nil {
		return nil, err
	}
	return newStepRun(cfg), nil
}

// Reconcile settles the open step rows that have no loop in this Steps, as a restart leaves them. A row Readopt registered
// whose step its adopter reports live starts its loop. Each other such row is ended by endRestarted. A Steps Readopt did
// not run for has no adopter, so it unregisters nothing. A row whose loop is running is left alone. It returns the first
// read or write error.
func (s *Steps) Reconcile(ctx context.Context) error {
	open, err := s.store.OpenSteps(ctx)
	if err != nil {
		return fmt.Errorf("reconcile steps: %w", err)
	}
	for _, row := range open {
		s.mu.Lock()
		_, live := s.loops[row.ID]
		run := s.adopted[row.ID]
		delete(s.adopted, row.ID)
		adopter := s.adopter
		s.mu.Unlock()
		if live {
			continue
		}
		if run != nil && adopter.StepLive(row.ID) {
			s.runLoop(ctx, row.ID, run)
			continue
		}
		if endErr := s.endRestarted(ctx, row, adopter); endErr != nil {
			return endErr
		}
	}
	return nil
}

// endRestarted ends the open row of a step with no live loop as a restart leaves it: engine_restarted, then its worktree
// removed, its gate holder stopped and cleared, and its observer unregistered from adopter when there is one. Nothing is
// notified and no session is stopped, since a restart leaves no session to stop. A worktree or gate holder that cannot be
// removed or cleared is logged and the rest goes on, so one bad row never keeps the others open. It returns the error of
// the end's write.
func (s *Steps) endRestarted(ctx context.Context, row store.StepRow, adopter StepAdopter) error {
	if err := s.writer.Commit(ctx, row.ID, store.StepEnd{ID: row.ID, End: stepRestartedEnd}); err != nil {
		return fmt.Errorf("reconcile step %s: %w", row.ID, err)
	}
	if row.Worktree != "" {
		if err := removeStepWorktree(ctx, row.Workspace, row.Worktree); err != nil {
			s.logger.WarnContext(ctx, "remove worktree of restarted step", "step", row.ID, "err", err)
		}
	}
	if row.GateHolder != "" {
		if err := gateEnd(ctx, row.GateHolder); err != nil {
			s.logger.WarnContext(ctx, "end gate of restarted step", "step", row.ID, "err", err)
		}
	}
	if adopter != nil {
		adopter.ExpectStep(row.ID, nil)
	}
	return nil
}

// Close cancels every loop and waits until no loop is left. Without a gate scope the cancel kills any gate a loop is
// running. With one, each loop is canceled with errGateDetach, which leaves its gate running under its holder for a
// restarted Steps to re-adopt. Each step row stays open, for Reconcile to settle after a restart.
func (s *Steps) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var cause error
	if s.gateScope != "" {
		cause = errGateDetach
	}
	for len(s.loops) > 0 {
		for _, cancel := range s.causes {
			cancel(cause)
		}
		s.idle.Wait()
	}
}
