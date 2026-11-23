package interpret

import (
	"context"
	"fmt"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// defaultHeldRetry is the wait before a held epic whose fetch or start failed is tried again, when Config.HeldRetry is
// zero.
const defaultHeldRetry = time.Minute

// passHeld starts each held epic whose predecessors have all shipped, from origin main on the host the epic lives on, and
// returns epics with each epic it started replaced by its row as the store holds it now. It runs first in every pass, so
// a held epic starts on the pass that sees its last predecessor ship. A held epic whose fetch or start fails is logged,
// stays held, and is tried again after the held retry.
func (r *Runner) passHeld(ctx context.Context, epics []store.EpicRow) []store.EpicRow {
	held, err := r.store.HeldEpics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list held epics", "err", err)
		return epics
	}
	var started []string
	for _, epic := range held {
		if !predecessorsShipped(epic) {
			continue
		}
		if err = r.startHeldEpic(ctx, epic); err != nil {
			r.logger.ErrorContext(ctx, "start held epic", "epic", epic.ID, "err", err)
			r.retryHeld(epic.ID)
			continue
		}
		started = append(started, epic.ID)
	}
	if len(started) == 0 {
		return epics
	}
	return r.replaceEpics(ctx, epics, started)
}

// predecessorsShipped reports whether epic has a predecessor and each predecessor's epic is in a phase that ships:
// released, done or closed shipped. An epic with no predecessor is not started, since it holds by no contract.
func predecessorsShipped(epic store.HeldEpic) bool {
	if len(epic.Predecessors) == 0 {
		return false
	}
	for _, predecessor := range epic.Predecessors {
		switch predecessor.EpicPhase {
		case "released", "done", store.PhaseClosedShipped:
		default:
			return false
		}
	}
	return true
}

// startHeldEpic fetches origin main of the epic's repository on the host the epic lives on, and starts the epic from the
// commit it returns.
func (r *Runner) startHeldEpic(ctx context.Context, epic store.HeldEpic) error {
	host, err := r.hosts.Host(epic.Host)
	if err != nil {
		return fmt.Errorf("host of epic %s: %w", epic.ID, err)
	}
	base, err := r.originMain(ctx, host, epic.Repository)
	if err != nil {
		return fmt.Errorf("origin main of epic %s: %w", epic.ID, err)
	}
	if err = r.store.StartHeldEpic(ctx, epic.ID, base, r.now()); err != nil {
		return fmt.Errorf("start held epic %s at %s: %w", epic.ID, base, err)
	}
	r.logger.InfoContext(ctx, "held epic started", "epic", epic.ID, "base", base)
	return nil
}

// retryHeld wakes the runner for epic once the held retry has passed, so a held epic whose fetch or start failed is tried
// again. The wait runs on a timer of its own, so no pass waits on it.
func (r *Runner) retryHeld(epic string) {
	time.AfterFunc(r.heldRetry, func() { r.Wake([]string{epic}) })
}

// replaceEpics returns epics with each epic named by started replaced by its row as the store holds it now. An epic
// started that epics does not list is appended. A read that fails is logged and returns epics, and the epics started
// are then passed by the next pass that reads them.
func (r *Runner) replaceEpics(ctx context.Context, epics []store.EpicRow, started []string) []store.EpicRow {
	rows, err := r.store.Epics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list epics", "err", err)
		return epics
	}
	named := make(map[string]bool, len(started))
	for _, id := range started {
		named[id] = true
	}
	out := make([]store.EpicRow, 0, len(epics)+len(started))
	for _, epic := range epics {
		if !named[epic.ID] {
			out = append(out, epic)
		}
	}
	for _, row := range rows {
		if named[row.ID] {
			out = append(out, row)
		}
	}
	return out
}
