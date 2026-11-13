package enginehost

import (
	"context"
	"fmt"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/store"
)

// drain records what the holder of row's session in dir spooled while no orreryd attached to it, then settles the row
// through reconcileRow. The holder has ended, so holder.StopOrphan first stops a child that still runs; a failure is
// logged and the drain goes on. The stdout from row's replay-from to its consumed offset was recorded already, so it is
// replayed with nothing stored. Each later line is recorded as the pump records it, so each is stored once. The session
// is not registered live, and takes no slot. Its exit is recorded only when the holder wrote an exit record whose reason
// is exited, the child ending on its own. Any other exit, or none, records nothing for the session's end, since the
// child was stopped, idled, or ended with no record of why.
func (m *Manager) drain(ctx context.Context, row store.EngineRow, dir string) error {
	stopCtx, cancel := context.WithTimeout(ctx, clearTimeout)
	defer cancel()
	if err := holder.StopOrphan(stopCtx, dir); err != nil {
		m.logger.WarnContext(ctx, "stop orphan of session", "session", row.ID, "err", err)
	}
	// The record path runs to the end of the spool. A canceled ctx would fail a write, and the holder would then be
	// cleared with lines unrecorded. The spool is finite, so the drain still ends.
	recordCtx := context.WithoutCancel(ctx)
	gone, err := holder.OpenGone(dir, row.ReplayFrom)
	if err != nil {
		return fmt.Errorf("open holder of session %s: %w", row.ID, err)
	}
	sess, lines, err := session.Readopt(recordCtx, gone,
		session.ReadoptOptions{From: row.ReplayFrom, Consumed: row.Consumed})
	if err != nil {
		return fmt.Errorf("replay drained session %s: %w", row.ID, err)
	}
	live := &liveSession{id: row.ID, dir: row.Dir, sess: sess, requests: map[string]struct{}{}}
	if exit, found := gone.Exit(); !found || exit.Reason != holder.ReasonExited {
		live.closing = true
	}
	rec := newRecorder(row.ID)
	rec.claudeSession = row.ClaudeSession
	reply := m.replay(live, rec, lines)
	for line := range sess.Lines() {
		reply = m.recordLine(recordCtx, live, rec, line, reply)
	}
	// The drain changed the row, so it settles the row as the drain left it, not as the caller read it. The lines'
	// ops may still be queued, so a commit of the unfinished-text clear, which reconcileRow writes too, waits until the
	// writer has stored them: it stores its queue in order.
	if err = m.writer.Commit(recordCtx, row.ID, store.EngineTextClear{ID: row.ID}); err != nil {
		return fmt.Errorf("store drained session %s: %w", row.ID, err)
	}
	fresh, err := m.engineRow(recordCtx, row.ID)
	if err != nil {
		return err
	}
	return m.reconcileRow(recordCtx, fresh)
}
