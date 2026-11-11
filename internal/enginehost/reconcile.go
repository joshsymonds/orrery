package enginehost

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// restartedReason is the reason a session whose turn was running when orreryd restarted is recorded interrupted with.
const restartedReason = "orreryd restarted during the turn"

// Reconcile settles the engine rows that have no process in this manager, as a restart leaves them. Each such row loses
// its unfinished text. A row whose turn was running, working or waiting, is recorded interrupted with restartedReason
// and no question, and the owner is notified of it. A row in any other state keeps its state. No row is removed. It
// returns the first read or write error.
func (m *Manager) Reconcile(ctx context.Context) error {
	rows, err := m.store.EngineSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile sessions: %w", err)
	}
	for _, row := range rows {
		m.mu.Lock()
		_, live := m.sessions[row.ID]
		m.mu.Unlock()
		if live {
			continue
		}
		if settleErr := m.reconcileRow(ctx, row); settleErr != nil {
			return settleErr
		}
	}
	return nil
}

// reconcileRow removes the unfinished text of row and, when row's turn was running, records it interrupted and notifies
// the owner. It returns the error of the write.
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
	if turning {
		m.notify(ctx, row.ID, row.Dir, stateInterrupted, restartedReason)
	}
	return nil
}
