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

// passHeld starts each held epic that starts, from origin main on the host the epic lives on, and returns epics with
// each epic it started replaced by its row as the store holds it now. A held epic starts when its held question was
// answered start, or when its predecessors have all shipped. A held epic that has a predecessor which will not ship is
// asked about it, once, and stays held. It runs first in every pass, so a held epic starts on the pass that sees its
// last predecessor ship. A held epic whose fetch or start fails is logged, stays held, and is tried again after the
// held retry.
func (r *Runner) passHeld(ctx context.Context, epics []store.EpicRow) []store.EpicRow {
	held, err := r.store.HeldEpics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list held epics", "err", err)
		return epics
	}
	var started []string
	for _, epic := range held {
		if !startsHeld(epic) {
			r.askLostPredecessor(ctx, epic)
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

// startsHeld reports whether a held epic starts on this pass: its held question was answered start, or its predecessors
// have all shipped.
func startsHeld(epic store.HeldEpic) bool {
	return epic.Answer == store.ChoiceStart || predecessorsShipped(epic)
}

// askLostPredecessor asks the steering conversation of epic, once, when one of its predecessors will not ship. The
// question names epic, that predecessor and why it will not ship. An epic with a held question already is not asked
// again, and an epic whose predecessors may all still ship is not asked. A failed ask is logged and tried again after
// the held retry.
func (r *Runner) askLostPredecessor(ctx context.Context, epic store.HeldEpic) {
	if epic.Question != 0 {
		return
	}
	for _, predecessor := range epic.Predecessors {
		why := lossWhy(predecessor)
		if why == "" {
			continue
		}
		text := fmt.Sprintf("Epic %s is held after %s, which will not ship: %s.", epic.ID, predecessor.Contract, why)
		if err := r.store.AskHeldEpic(ctx, epic.ID, text, r.now()); err != nil {
			r.logger.ErrorContext(ctx, "ask held epic", "epic", epic.ID, "err", err)
			r.retryHeld(epic.ID)
		}
		return
	}
}

// lossWhy returns why predecessor will not ship, or "" when it may ship: its contract is set aside, or its epic is
// closed superseded, closed abandoned or ended with gaps.
func lossWhy(predecessor store.HeldPredecessor) string {
	if predecessor.ContractState == store.ContractSetAside {
		return "its contract is set aside"
	}
	switch predecessor.EpicPhase {
	case store.PhaseClosedSuperseded:
		return "its epic is closed superseded"
	case store.PhaseClosedAbandoned:
		return "its epic is closed abandoned"
	case phaseEndedWithGaps:
		return "its epic ended with gaps"
	}
	return ""
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
