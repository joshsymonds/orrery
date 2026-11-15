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

// resumePrompt is the prompt a restart sends to the session of a working turn it interrupted, so that the turn continues
// on the session's Claude id.
const resumePrompt = "Resume working."

// resumedBody is the body of the notice that tells the owner a restart resumed the turn it interrupted.
const resumedBody = "orreryd restarted during the turn; resumed it"

// Reconcile goes through the holder directories under the manager's state directory whose session has no pump in this
// manager. Each such holder is re-adopted when its row allows it (see readoptable). A holder that has ended is drained
// into the store, and its row is settled by that drain. A live holder that cannot be attached is stopped first, then drained
// the same way. A step's holder that has ended or cannot be attached is cleared, never drained or stopped. Each other holder
// is cleared. Then it settles the engine rows that have no process in this manager, as a restart leaves them. A re-adopted
// session is live, so its row is left as its replay gave it, except that a session re-adopted from a holder one version
// older is stopped once it is not busy, at its turn's end or at once (see retireLocked), and that stop records nothing. A
// step's stop keeps its slot for its id, and the step stays held (see StepLive). A drained row is settled by its drain.
// Each other row loses its unfinished text. A row whose turn was running, working or waiting, is recorded interrupted with
// restartedReason and no question. A working turn of an engine row whose session has a Claude id is then resumed with
// resumePrompt, unless its last prompt is resumePrompt already, and the owner is told of the resume in place of the
// interruption (see resumable). A step row is settled without a notice. A row in any other state keeps its state. No row
// is removed. A holder that fails to re-adopt, to stop, to drain or to clear is logged and its session is cleared, and the
// rows are settled anyway. The rows that need a write are settled in one store transaction, so a row that needs nothing,
// with no text and no turn running, is not written. A write that fails stops no other row. It returns the first read or
// write error.
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
	// The sessions with a pump are taken before the rows are read again: a pump that ends after this has stored its
	// state before the read, and one that runs at the read is left to record its own.
	pumped := m.pumpingIDs()
	// A drain that failed after it recorded lines has changed the rows, so they are read again after the holder pass.
	rows, err = m.store.EngineSessions(ctx)
	if err != nil {
		return fmt.Errorf("reconcile sessions: %w", err)
	}
	text, textErr := m.store.EngineTextSessions(ctx)
	var firstErr error
	if textErr != nil {
		firstErr = fmt.Errorf("read unfinished text: %w", textErr)
	}
	pending := m.pendingRows(rows, pumped, settled, text, textErr)
	groups := make([]SessionOps, len(pending))
	for i, row := range pending {
		groups[i] = SessionOps{ID: row.ID, Ops: settleOps(row)}
	}
	errs, err := m.writer.CommitAll(ctx, groups)
	if err != nil {
		if firstErr != nil {
			return firstErr
		}
		return fmt.Errorf("reconcile sessions: %w", err)
	}
	writeErr := m.notifySettled(ctx, pending, errs)
	if firstErr != nil {
		return firstErr
	}
	return writeErr
}

// pendingRows returns the rows Reconcile settles: each row that had no pump in this manager when pumped was taken and
// that the holder pass did not settle, and that has a running turn or unfinished text in text. A failed text read,
// textErr, makes every row count as holding text.
func (m *Manager) pendingRows(
	rows []store.EngineRow, pumped, settled, text map[string]bool, textErr error,
) []store.EngineRow {
	var pending []store.EngineRow
	for _, row := range rows {
		if pumped[row.ID] || settled[row.ID] {
			continue
		}
		if turnRunning(row) || textErr != nil || text[row.ID] {
			pending = append(pending, row)
		}
	}
	return pending
}

// notifySettled tells the owner of each row in rows whose write, in errs, was stored (see resumeOrNotify). errs holds one
// error per row, in order. A row whose write was dropped is not notified, and the first such error is returned.
func (m *Manager) notifySettled(ctx context.Context, rows []store.EngineRow, errs []error) error {
	var firstErr error
	for i, row := range rows {
		if errs[i] != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reconcile session %s: %w", row.ID, errs[i])
			}
			continue
		}
		m.resumeOrNotify(ctx, row)
	}
	return firstErr
}

// reconcileHolders goes through the holder directories under the state directory. The holder of each session with no pump
// in this manager is re-adopted when its row, in rows by id, allows it (see readoptable), and drained when it has
// ended, or stopped and drained when it is live but cannot be attached. A step's holder is never drained or stopped. Every
// other holder is cleared, as is one that fails to re-adopt, to stop or to drain. A drained holder is cleared by its drain's
// settle (see reconcileRow), since a resume there starts a new holder in the same directory. It returns the ids of the
// sessions whose holder was drained, and whose row the drain settled. A missing holders directory, or an empty state
// directory, has nothing to go through.
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
		if m.pumping(id) {
			continue
		}
		dir := m.holderDirOf(id)
		if row, ok := rows[id]; ok && m.readoptable(row) {
			readopted, drained := m.readoptHolder(ctx, row, dir)
			if readopted {
				continue
			}
			if drained {
				settled[id] = true
				continue
			}
		}
		m.clearHolder(ctx, id, dir)
	}
	return settled
}

// holderDirOf returns the holder directory of session id under the state directory.
func (m *Manager) holderDirOf(id string) string {
	return filepath.Join(m.stateDir, holdersSubdir, id)
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
// primed from that replay (see prime). It owes the session the prompts the previous controller left unanswered (see
// owedPrompts). A holder one version older than the controller's is re-adopted as well, but its session is draining, so it
// is retired once no turn runs (see retireLocked); a step is retired at its hand-off and keeps its step slot for its id. It
// takes a slot without the cap check, since the holder's process already runs: a step slot for a step, and a session slot
// otherwise. A failure after the holder is attached ends the session with Detach, which leaves the holder running for the
// caller to clear.
func (m *Manager) readopt(ctx context.Context, row store.EngineRow, dir string) error {
	sp, err := m.readoptSpec(ctx, row)
	if err != nil {
		return err
	}
	process, written, err := holder.Reattach(ctx, dir, row.ReplayFrom, m.holderVersion)
	if err != nil {
		return fmt.Errorf("attach holder of session %s: %w", row.ID, err)
	}
	owed, err := m.owedPrompts(ctx, row.ID, written)
	if err != nil {
		return errors.Join(err, process.Detach())
	}
	draining := process.Version() == m.holderVersion-1
	sess, lines, err := session.Readopt(ctx, process, session.ReadoptOptions{
		From: row.ReplayFrom, Consumed: row.Consumed, Written: written, Owed: owed,
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
	re := &readoption{row: row, lines: lines, written: written, draining: draining}
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

// owedPrompts returns how many prompts the previous controller wrote to the holder of session id that no later stored turn
// end answers, and which the re-adopted session did not send. It is the store's count less the prompts sendUnwritten sends,
// since Prompt counts those itself, and never below 0. written holds the write ids the holder has applied.
func (m *Manager) owedPrompts(ctx context.Context, id string, written []string) (int, error) {
	unanswered, err := m.store.HolderPromptsUnanswered(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("count prompts of session %s no turn end answers: %w", id, err)
	}
	prompts, err := m.store.HolderPrompts(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("read prompts of session %s: %w", id, err)
	}
	return max(unanswered-len(unwrittenPrompts(prompts, written)), 0), nil
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

// pumpingIDs returns the ids of the sessions that a pump in this manager is still running (see pumping).
func (m *Manager) pumpingIDs() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make(map[string]bool, len(m.sessions)+len(m.stopping))
	for id := range m.sessions {
		ids[id] = true
	}
	for id := range m.stopping {
		ids[id] = true
	}
	return ids
}

// pumping reports whether a pump in this manager is still running the session with id: the session is live, or it is
// retired and its pump has not ended (see retireLocked). Such a pump records the session's state itself, so Reconcile
// leaves its row and holder to that pump.
func (m *Manager) pumping(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, live := m.sessions[id]
	_, stopping := m.stopping[id]
	return live || stopping
}

// ExpectStep registers observer for the step id before Reconcile. Reconcile re-adopts the holder of the step as a live
// step that hands its events, turn ends and exit to observer. A step with no registered observer has its holder cleared
// instead. A nil observer leaves the step with no observer.
func (m *Manager) ExpectStep(id string, observer StepObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stepObservers[id] = observer
}

// StepLive reports whether this manager holds the step id for its loop: a live step session, or a drained one whose step
// slot is kept for its next Continue (see retireLocked).
func (m *Manager) StepLive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	return (ok && live.step) || m.kept[id] != nil
}

// reconcileRow settles row in one write as a restart leaves it (see settleOps). The drain of its holder has read that
// holder to its end, so the holder directory is cleared next, before a resume starts a new holder in it. Then the owner is
// told of the row's interruption, or the row is resumed (see resumeOrNotify). It returns the error of the write.
func (m *Manager) reconcileRow(ctx context.Context, row store.EngineRow) error {
	if err := m.writer.Commit(ctx, row.ID, settleOps(row)...); err != nil {
		return fmt.Errorf("reconcile session %s: %w", row.ID, err)
	}
	m.clearHolder(ctx, row.ID, m.holderDirOf(row.ID))
	m.resumeOrNotify(ctx, row)
	return nil
}

// settleOps returns the ops that settle row as a restart leaves it: its unfinished text is removed, and when its turn
// was running it is recorded interrupted with restartedReason and no question.
func settleOps(row store.EngineRow) []store.EngineOp {
	textClear := store.EngineTextClear{ID: row.ID}
	if !turnRunning(row) {
		return []store.EngineOp{textClear}
	}
	interrupted := stateInterrupted
	question := ""
	reason := restartedReason
	return []store.EngineOp{textClear, store.EngineSet{ID: row.ID,
		Changes: store.EngineChanges{State: &interrupted, Question: &question, Reason: &reason}}}
}

// turnRunning reports whether the turn of row was running: its row is working or waiting.
func turnRunning(row store.EngineRow) bool {
	return row.State == string(protocol.StateWorking) || row.State == string(protocol.StateWaiting)
}

// resumeOrNotify tells the owner that the turn of row, which the settle write has stored, was interrupted by the restart,
// when row is an engine row whose turn was running. A step row sends no notice. A row that resumable accepts is resumed
// instead, and its resume notice replaces the interruption's.
func (m *Manager) resumeOrNotify(ctx context.Context, row store.EngineRow) {
	if !turnRunning(row) || row.Kind != store.KindEngine {
		return
	}
	if m.resumable(ctx, row) {
		m.resumeInterrupted(ctx, row)
		return
	}
	m.notify(ctx, row.ID, row.Dir, stateInterrupted, restartedReason)
}

// resumable reports whether row, an engine row whose turn was running, is resumed rather than only interrupted. Its turn
// must have been working, not waiting for an answer, and its session must have a Claude id to resume. Its last prompt must
// not be resumePrompt already, so that a turn a restart interrupts again after its resume is interrupted, not resumed
// again. A prompt that cannot be read leaves the turn interrupted.
func (m *Manager) resumable(ctx context.Context, row store.EngineRow) bool {
	if row.State != string(protocol.StateWorking) || row.ClaudeSession == "" {
		return false
	}
	prompts, err := m.store.HolderPrompts(ctx, row.ID)
	if err != nil {
		m.logger.WarnContext(ctx, "read prompts of session", "session", row.ID, "err", err)
		return false
	}
	return len(prompts) == 0 || prompts[len(prompts)-1].Text != resumePrompt
}

// resumeInterrupted resumes the session of row with resumePrompt, after the settle write of row is stored, so the resume
// starts from the interrupted row. The resume's notice replaces the interruption's. A resume that the session cap refuses
// leaves the row interrupted, and the interruption's notice stands. A resume whose spawn or send fails records the session
// failed and notifies that failure itself, so no interruption notice follows it.
func (m *Manager) resumeInterrupted(ctx context.Context, row store.EngineRow) {
	err := m.Say(ctx, SayRequest{ID: row.ID, Text: resumePrompt})
	if err == nil {
		m.notify(ctx, row.ID, row.Dir, stateInterrupted, resumedBody)
		return
	}
	m.logger.WarnContext(ctx, "resume interrupted session", "session", row.ID, "err", err)
	after, readErr := m.engineRow(ctx, row.ID)
	if readErr != nil {
		m.logger.WarnContext(ctx, "read session after resume", "session", row.ID, "err", readErr)
	}
	if readErr == nil && after.State == stateFailed {
		return
	}
	m.notify(ctx, row.ID, row.Dir, stateInterrupted, restartedReason)
}
