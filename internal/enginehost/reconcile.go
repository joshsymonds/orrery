package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// restartedReason is the reason a session whose turn was running when orreryd restarted is recorded interrupted with.
const restartedReason = "orreryd restarted during the turn"

// holdersSubdir is the subdirectory of the state directory that holds each session's holder directory. It is the
// holder package's holders directory.
const holdersSubdir = "holders"

// clearTimeout bounds the re-adoption, or the holder.Clear, of one session's holder directory in Reconcile.
const clearTimeout = 10 * time.Second

// Reconcile goes through the holder directories under the manager's state directory whose session has no live session in
// this manager. Each such holder is re-adopted when its row allows it (see readoptable). A holder that has ended is drained
// into the store, and its row is settled by that drain. A live holder that cannot be attached is stopped first, then drained
// the same way. A step's holder that has ended or cannot be attached is cleared, never drained or stopped. Each other holder
// is cleared. Then it settles the engine rows that have no process in this manager, as a restart leaves them. A re-adopted
// session is live, so its row is left as its replay gave it. A drained row is settled by its drain. Each other row loses
// its unfinished text. A row whose turn was running, working or waiting, is recorded interrupted with restartedReason and no
// question. The owner is notified of an engine row's interruption; a step row is settled without a notice. A row in any
// other state keeps its state. No row is removed. A holder that fails to re-adopt, to stop, to drain or to clear is logged
// and its session is cleared, and the rows are settled anyway. It returns the first
// read or write error.
func (m *Manager) Reconcile(ctx context.Context) error {
	rows, err := m.store.EngineSessions(ctx)
	// A failed read leaves no row, so every holder is then cleared.
	byID := make(map[string]store.EngineRow, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	settled := m.reconcileHolders(ctx, byID)
	if err != nil {
		return fmt.Errorf("reconcile sessions: %w", err)
	}
	for _, row := range rows {
		if m.isLive(row.ID) || settled[row.ID] {
			continue
		}
		// A drain that failed after it recorded lines has changed the row, so the row is read again before it settles.
		current, readErr := m.engineRow(ctx, row.ID)
		if readErr != nil {
			return fmt.Errorf("reconcile session %s: %w", row.ID, readErr)
		}
		if settleErr := m.reconcileRow(ctx, current); settleErr != nil {
			return settleErr
		}
	}
	return nil
}

// reconcileHolders goes through the holder directories under the state directory. The holder of each session with no live
// session in this manager is re-adopted when its row, in rows by id, allows it (see readoptable), and drained when it has
// ended, or stopped and drained when it is live but cannot be attached. A step's holder is never drained or stopped. Every
// other holder is cleared, as is one that fails to re-adopt, to stop or to drain. It returns the ids of the sessions whose
// holder was drained, and whose row the drain settled. A missing holders directory, or an empty state directory, has
// nothing to go through.
func (m *Manager) reconcileHolders(ctx context.Context, rows map[string]store.EngineRow) map[string]bool {
	settled := map[string]bool{}
	if m.stateDir == "" {
		return settled
	}
	holders := filepath.Join(m.stateDir, holdersSubdir)
	entries, err := os.ReadDir(holders)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.logger.WarnContext(ctx, "read holders dir", "dir", holders, "err", err)
		}
		return settled
	}
	for _, entry := range entries {
		id := entry.Name()
		if m.isLive(id) {
			continue
		}
		dir := filepath.Join(holders, id)
		if row, ok := rows[id]; ok && m.readoptable(row) {
			readopted, drained := m.readoptHolder(ctx, row, dir)
			if readopted {
				continue
			}
			if drained {
				settled[id] = true
			}
		}
		m.clearHolder(ctx, id, dir)
	}
	return settled
}

// readoptable reports whether the holder of row's session may be re-adopted. Row must be tracked by a launch and be neither
// stopped nor failed. An engine row is then re-adopted. A step row is re-adopted only when ExpectStep registered an observer
// for its id. An untracked row, a stopped or failed row, and a step with no expected observer are cleared instead.
func (m *Manager) readoptable(row store.EngineRow) bool {
	if !row.Tracked || row.State == stateStopped || row.State == stateFailed {
		return false
	}
	switch row.Kind {
	case store.KindEngine:
		return true
	case store.KindStep:
		return m.expectsStep(row.ID)
	}
	return false
}

// readoptHolder re-adopts the session of row from its holder in dir, bounded by clearTimeout. When the holder has ended, it
// drains the session instead. A live holder that cannot be attached is stopped, then drained. A step's holder that has
// ended or cannot be attached is left for the caller to clear, neither drained nor stopped. A failure is logged, and the
// holder is left for the caller to clear. It reports whether the session was re-adopted, and whether it was drained and its
// row settled.
func (m *Manager) readoptHolder(ctx context.Context, row store.EngineRow, dir string) (readopted, drained bool) {
	readoptCtx, cancel := context.WithTimeout(ctx, clearTimeout)
	defer cancel()
	err := m.readopt(readoptCtx, row, dir)
	switch {
	case err == nil:
		return true, false
	case row.Kind == store.KindStep && (errors.Is(err, holder.ErrGone) || errors.Is(err, holder.ErrUnattachable)):
		return false, false
	case errors.Is(err, holder.ErrGone):
		return false, m.drainGone(ctx, row, dir)
	case errors.Is(err, holder.ErrUnattachable):
		return false, m.stopThenDrain(ctx, row, dir)
	default:
		m.logger.WarnContext(ctx, "re-adopt session", "session", row.ID, "err", err)
		return false, false
	}
}

// stopThenDrain stops the live holder in dir that could not be attached, bounded by clearTimeout of its own, then drains
// the session as a gone holder. A failed stop is logged, and the holder is left for the caller to clear. It reports whether
// the row was settled by the drain.
func (m *Manager) stopThenDrain(ctx context.Context, row store.EngineRow, dir string) bool {
	stopCtx, cancel := context.WithTimeout(ctx, clearTimeout)
	defer cancel()
	if err := holder.Stop(stopCtx, dir); err != nil {
		m.logger.WarnContext(ctx, "stop unattachable holder", "session", row.ID, "err", err)
		return false
	}
	return m.drainGone(ctx, row, dir)
}

// drainGone drains the session of row from its ended holder in dir. A failure is logged. It reports whether the row was
// settled by the drain.
func (m *Manager) drainGone(ctx context.Context, row store.EngineRow, dir string) bool {
	if err := m.drain(ctx, row, dir); err != nil {
		m.logger.WarnContext(ctx, "drain session", "session", row.ID, "err", err)
		return false
	}
	return true
}

// readopt re-adopts the session of row from its holder in dir. It reads the spec the session is registered with, attaches
// the holder at row's replay-from, replays the stdout from there to row's consumed offset, and registers the session live,
// primed from that replay (see prime). It takes a slot without the cap check, since the holder's process already runs: a
// step slot for a step, and a session slot otherwise. A failure after the holder is attached ends the session with Detach,
// which leaves the holder running for the caller to clear.
func (m *Manager) readopt(ctx context.Context, row store.EngineRow, dir string) error {
	sp, err := m.readoptSpec(ctx, row)
	if err != nil {
		return err
	}
	process, written, err := holder.Reattach(ctx, dir, row.ReplayFrom)
	if err != nil {
		return fmt.Errorf("attach holder of session %s: %w", row.ID, err)
	}
	sess, lines, err := session.Readopt(ctx, process, session.ReadoptOptions{
		From: row.ReplayFrom, Consumed: row.Consumed, Written: written,
	})
	if err != nil {
		return fmt.Errorf("replay session %s: %w", row.ID, err)
	}
	rec := newRecorder(row.ID)
	rec.claudeSession = row.ClaudeSession
	m.mu.Lock()
	if sp.step {
		m.stepsRunning++
	} else {
		m.reserved++
	}
	m.mu.Unlock()
	re := &readoption{row: row, lines: lines, written: written}
	if _, err = m.register(ctx, sess, row.ID, row.Dir, sp, rec, re); err != nil {
		if sp.step {
			m.releaseStep()
		} else {
			m.release()
		}
		return errors.Join(fmt.Errorf("register session %s: %w", row.ID, err), sess.Detach())
	}
	return nil
}

// readoptSpec returns the spec the session of row is registered with when it is re-adopted. An engine session has none. A
// step has the agent of its steps row and the observer expected for it, so it is registered as a step.
func (m *Manager) readoptSpec(ctx context.Context, row store.EngineRow) (spawnSpec, error) {
	if row.Kind != store.KindStep {
		return spawnSpec{}, nil
	}
	step, err := m.store.Step(ctx, row.ID)
	if err != nil {
		return spawnSpec{}, fmt.Errorf("read step %s: %w", row.ID, err)
	}
	m.mu.Lock()
	observer := m.stepObservers[row.ID]
	m.mu.Unlock()
	return spawnSpec{agent: step.Agent, step: true, observer: observer}, nil
}

// expectsStep reports whether ExpectStep registered an observer for the step id.
func (m *Manager) expectsStep(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stepObservers[id] != nil
}

// clearHolder clears the holder directory dir of session id, bounded by clearTimeout. A failed clear is logged with the
// session's name.
func (m *Manager) clearHolder(ctx context.Context, id, dir string) {
	clearCtx, cancel := context.WithTimeout(ctx, clearTimeout)
	defer cancel()
	if err := holder.Clear(clearCtx, dir); err != nil {
		m.logger.WarnContext(ctx, "clear holder", "session", id, "err", err)
	}
}

// isLive reports whether this manager has a live session with id.
func (m *Manager) isLive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, live := m.sessions[id]
	return live
}

// ExpectStep registers observer for the step id before Reconcile. Reconcile re-adopts the holder of the step as a live
// step that hands its events, turn ends and exit to observer. A step with no registered observer has its holder cleared
// instead. A nil observer leaves the step with no observer.
func (m *Manager) ExpectStep(id string, observer StepObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepObservers[id] = observer
}

// StepLive reports whether this manager has a live step session with id.
func (m *Manager) StepLive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	return ok && live.step
}

// reconcileRow removes the unfinished text of row and, when row's turn was running, records it interrupted and notifies
// the owner of an engine row. It returns the error of the write.
func (m *Manager) reconcileRow(ctx context.Context, row store.EngineRow) error {
	ops := []store.EngineOp{store.EngineTextClear{ID: row.ID}}
	turning := row.State == string(protocol.StateWorking) || row.State == string(protocol.StateWaiting)
	if turning {
		interrupted := stateInterrupted
		question := ""
		reason := restartedReason
		ops = append(ops, store.EngineSet{ID: row.ID,
			Changes: store.EngineChanges{State: &interrupted, Question: &question, Reason: &reason}})
	}
	if err := m.writer.Commit(ctx, row.ID, ops...); err != nil {
		return fmt.Errorf("reconcile session %s: %w", row.ID, err)
	}
	if turning && row.Kind == store.KindEngine {
		m.notify(ctx, row.ID, row.Dir, stateInterrupted, restartedReason)
	}
	return nil
}
