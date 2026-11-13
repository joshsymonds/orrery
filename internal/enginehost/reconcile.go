package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// restartedReason is the reason a session whose turn was running when orreryd restarted is recorded interrupted with.
const restartedReason = "orreryd restarted during the turn"

// holdersSubdir is the subdirectory of the state directory that holds each session's holder directory. It is the
// holder package's holders directory.
const holdersSubdir = "holders"

// clearTimeout bounds the holder.Clear of one session's holder directory in Reconcile.
const clearTimeout = 10 * time.Second

// Reconcile clears the holder of each session under the manager's state directory that has no live session in this
// manager, then settles the engine rows that have no process in this manager, as a restart leaves them. Each such row
// loses its unfinished text. A row whose turn was running, working or waiting, is recorded interrupted with
// restartedReason and no question. The owner is notified of an engine row's interruption; a step row is settled without
// a notice. A row in any other state keeps its state. No row is removed. A holder that fails to clear is logged and the
// rows are settled anyway. It returns the first read or write error.
func (m *Manager) Reconcile(ctx context.Context) error {
	m.clearHolders(ctx)
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

// clearHolders clears the holder directory of each session under the state directory that no live session of this
// manager owns. A holder still serving there is stopped first. A missing holders directory, or an empty state directory,
// has nothing to clear. A failed clear is logged with the session's name, and the next session is cleared.
func (m *Manager) clearHolders(ctx context.Context) {
	if m.stateDir == "" {
		return
	}
	holders := filepath.Join(m.stateDir, holdersSubdir)
	entries, err := os.ReadDir(holders)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			m.logger.WarnContext(ctx, "read holders dir", "dir", holders, "err", err)
		}
		return
	}
	for _, entry := range entries {
		session := entry.Name()
		m.mu.Lock()
		_, live := m.sessions[session]
		m.mu.Unlock()
		if live {
			continue
		}
		clearCtx, cancel := context.WithTimeout(ctx, clearTimeout)
		if clearErr := holder.Clear(clearCtx, filepath.Join(holders, session)); clearErr != nil {
			m.logger.WarnContext(ctx, "clear holder", "session", session, "err", clearErr)
		}
		cancel()
	}
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
