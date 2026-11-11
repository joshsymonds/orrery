package enginehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrCapReached reports a Start that would exceed the cap of live sessions.
var ErrCapReached = errors.New("session cap reached")

// ErrNoSession reports an id with no engine row, or a Stop, Answer or Interrupt of a session that is not live.
var ErrNoSession = errors.New("no such session")

// ErrBadDir reports a Start whose directory is not an absolute path to an existing directory.
var ErrBadDir = errors.New("session directory must be an existing absolute directory")

// ErrEmptyPrompt reports a Start with an empty first prompt.
var ErrEmptyPrompt = errors.New("session prompt is empty")

// ErrBadAnswer reports an Answer that fits neither the options of the open question nor allow or deny for the open
// permission request. The request stays open.
var ErrBadAnswer = errors.New("answer does not fit the open request")

// errManagerClosed reports a Start on a manager that Close has begun closing.
var errManagerClosed = errors.New("session manager is closed")

// permissionMode is the permission mode every session runs in.
const permissionMode = "bypassPermissions"

// denyMessage is the message a permission request denied through Answer gives the session.
const denyMessage = "The orrery host denied this request."

// closeWait bounds how long Close waits for the sessions it stops to record their state.
const closeWait = 5 * time.Second

// interruptedReason is the reason a session whose turn was running when Close stopped the manager records.
const interruptedReason = "orreryd stopped during the turn"

// NoticeSink receives the notices the owner is told about. *Notifier satisfies it.
type NoticeSink interface {
	Notify(ctx context.Context, n Notice)
}

// Store reads the engine rows Reconcile settles, and the rows Say resumes and Seen marks. *store.Store satisfies it.
type Store interface {
	EngineSessions(ctx context.Context) ([]store.EngineRow, error)
	EngineSession(ctx context.Context, id string) (store.EngineRow, error)
}

// Config configures a Manager. Shell is the login shell and Claude the claude name, resolved with exec.LookPath at
// each start. Environ is the environment sessions start with, Cap the most live sessions at once, Writer stores their
// ops, Now stamps them (nil is time.Now), Notifier receives the notices of their state changes (nil sends none), Store
// is the engine rows Reconcile, Say and Seen read and Logger reports pump errors (nil discards).
type Config struct {
	Shell    string
	Claude   string
	Environ  []string
	Cap      int
	Writer   *Writer
	Now      func() time.Time
	Notifier NoticeSink
	Store    Store
	Logger   *slog.Logger
}

// liveSession is one running session: its directory, its process, the done channel closed when its pump has finished,
// and the cancel that frees a pump blocked on a write. exited is set by the pump when it records the process's exit, so
// Stop and Close do not record over the state that exit recorded. turning is set while a turn runs, from the send of its
// prompt or a Started event until its TurnEnded or the process's Exited. interrupting is set by Interrupt while a turn
// runs, until that turn's end, which then sends no notice. closing is set by Close before it stops the session. The
// manager's lock guards turning, interrupting and closing, and the pump sets exited under it too.
type liveSession struct {
	id           string
	dir          string
	sess         *session.Session
	done         chan struct{}
	cancel       context.CancelFunc
	exited       bool
	turning      bool
	interrupting bool
	closing      bool
	// order is held by the pump while it queues an event's ops, and by Answer from its send until it has queued the
	// working state, so every op an answer causes is queued after that state.
	order sync.Mutex
}

// Manager starts, stops and tracks headless sessions. Each running session has a pump goroutine that records its
// events through the Writer. Close stops every session and waits for the pumps.
type Manager struct {
	shell    string
	claude   string
	environ  []string
	limit    int
	writer   *Writer
	now      func() time.Time
	notifier NoticeSink
	store    Store
	logger   *slog.Logger

	pumps sync.WaitGroup

	mu sync.Mutex
	// sessions holds the running sessions whose pump has not finished.
	sessions map[string]*liveSession
	// reserved counts the sessions being started or running, which is what the cap limits.
	reserved int
	closed   bool

	// resumeMu serializes the resume of a session that is not live, so concurrent Says to one id start one process.
	resumeMu sync.Mutex
}

// NewManager returns a Manager for cfg.
func NewManager(cfg Config) *Manager {
	manager := &Manager{
		shell:    cfg.Shell,
		claude:   cfg.Claude,
		environ:  cfg.Environ,
		limit:    cfg.Cap,
		writer:   cfg.Writer,
		now:      cfg.Now,
		notifier: cfg.Notifier,
		store:    cfg.Store,
		logger:   cfg.Logger,
		sessions: map[string]*liveSession{},
	}
	if manager.now == nil {
		manager.now = time.Now
	}
	if manager.logger == nil {
		manager.logger = slog.New(slog.DiscardHandler)
	}
	return manager
}

// Start starts a session in dir with prompt as its first message and returns its id. It records the session as
// working before it spawns the process. A spawn or prompt failure is recorded as the session's failed state with the
// reason, and Start returns the id with no error.
func (m *Manager) Start(ctx context.Context, dir, prompt string) (string, error) {
	if err := checkStart(dir, prompt); err != nil {
		return "", err
	}
	if err := m.reserve(); err != nil {
		return "", err
	}
	id, err := newSessionID()
	if err != nil {
		m.release()
		return "", err
	}
	add := store.EngineAdd{ID: id, Dir: dir, State: string(protocol.StateWorking)}
	if err = m.writer.Commit(ctx, id, add); err != nil {
		m.release()
		return "", fmt.Errorf("record session %s: %w", id, err)
	}
	live, err := m.spawnLive(ctx, id, dir, "")
	if err != nil {
		return id, m.fail(ctx, id, dir, err)
	}
	if err = m.writer.Put(ctx, id, store.EnginePrompt{ID: id, Text: prompt}); err != nil {
		return id, m.abandon(ctx, live, err)
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(prompt); err != nil {
		m.setTurning(live, false)
		return id, m.abandon(ctx, live, err)
	}
	return id, nil
}

// Stop stops the live session id and records it stopped once its pump has stored its last ops. It returns
// ErrNoSession when id is not a live session.
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.mu.Lock()
	live := m.sessions[id]
	m.mu.Unlock()
	if live == nil {
		return fmt.Errorf("stop session %s: %w", id, ErrNoSession)
	}
	stopErr := live.sess.Stop()
	recordErr := m.recordStopped(ctx, live)
	if stopErr == nil && recordErr == nil {
		return nil
	}
	return fmt.Errorf("stop session %s: %w", id, errors.Join(stopErr, recordErr))
}

// Seen marks the engine row id as seen now. It returns an error wrapping ErrNoSession when no engine row has that id.
func (m *Manager) Seen(ctx context.Context, id string) error {
	if _, err := m.engineRow(ctx, id); err != nil {
		return fmt.Errorf("mark session %s seen: %w", id, err)
	}
	seen := strconv.FormatInt(m.now().Unix(), 10)
	return m.writer.Commit(ctx, id, store.EngineSet{ID: id, Changes: store.EngineChanges{Seen: &seen}})
}

// Say sends text to the session id as its next prompt. A session that is not live is resumed first, from its engine
// row (see resumeSession). It records the prompt and the working state before it sends, so the stored conversation has
// the prompt ahead of its reply. It returns ErrEmptyPrompt for empty text, an error wrapping ErrNoSession when no engine
// row has id, and the errors of resumeSession when a live session cannot be started. A send that fails is recorded as
// the session's failed state.
func (m *Manager) Say(ctx context.Context, id, text string) error {
	if text == "" {
		return ErrEmptyPrompt
	}
	live, err := m.live(id)
	if err != nil {
		live, err = m.resumeSession(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("say to session %s: %w", id, err)
	}
	working := string(protocol.StateWorking)
	if err = m.writer.Commit(ctx, id, store.EnginePrompt{ID: id, Text: text},
		store.EngineSet{ID: id, Changes: store.EngineChanges{State: &working}}); err != nil {
		return fmt.Errorf("record prompt of session %s: %w", id, err)
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(text); err != nil {
		m.setTurning(live, false)
		return fmt.Errorf("say to session %s: %w", id, errors.Join(err, m.recordFailed(ctx, id, live.dir, err)))
	}
	return nil
}

// Answer answers the open request requestID of the live session id. A question takes one of its option labels. A
// permission request takes allow, which approves it with its own input, or deny, which refuses it with a fixed
// message. Any other answer returns an error matching ErrBadAnswer and leaves the request open. On success Answer
// records the session working with no question. It returns an error wrapping ErrNoSession when id is not live.
func (m *Manager) Answer(ctx context.Context, id, requestID, answer string) error {
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("answer session %s: %w", id, err)
	}
	// Holding order until the working state is queued keeps the pump from queuing the answer's reply, or the turn's
	// end, ahead of it.
	live.order.Lock()
	defer live.order.Unlock()
	err = live.sess.AnswerQuestion(requestID, answer)
	switch {
	case err == nil:
	case errors.Is(err, session.ErrUnverifiedAnswer):
		return fmt.Errorf("answer session %s: %w: %w", id, ErrBadAnswer, err)
	case errors.Is(err, session.ErrNotQuestion):
		if err = answerPermission(live.sess, requestID, answer); err != nil {
			return fmt.Errorf("answer session %s: %w", id, err)
		}
	default:
		return fmt.Errorf("answer session %s: %w", id, err)
	}
	working := string(protocol.StateWorking)
	question := ""
	if err = m.writer.Put(ctx, id, store.EngineSet{ID: id,
		Changes: store.EngineChanges{State: &working, Question: &question}}); err != nil {
		return fmt.Errorf("record answer of session %s: %w", id, err)
	}
	return nil
}

// answerPermission allows or denies the open permission request requestID for answer, which must be allow or deny.
// It returns ErrBadAnswer, without claiming the request, for any other answer.
func answerPermission(sess *session.Session, requestID, answer string) error {
	switch answer {
	case "allow":
		if err := sess.Allow(requestID, nil); err != nil {
			return fmt.Errorf("allow request %s: %w", requestID, err)
		}
		return nil
	case "deny":
		if err := sess.Deny(requestID, denyMessage); err != nil {
			return fmt.Errorf("deny request %s: %w", requestID, err)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q is neither allow nor deny", ErrBadAnswer, answer)
	}
}

// Interrupt asks the live session id to end the turn in progress. The turn's end is recorded when the session reports
// it, and that end sends no notice. It returns an error wrapping ErrNoSession when id is not live.
func (m *Manager) Interrupt(_ context.Context, id string) error {
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	m.setInterrupting(live, true)
	if err = live.sess.Interrupt(); err != nil {
		m.setInterrupting(live, false)
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	return nil
}

// live returns the live session id, or an error wrapping ErrNoSession when no session with that id is live.
func (m *Manager) live(id string) (*liveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok {
		return nil, ErrNoSession
	}
	return live, nil
}

// resumeSession returns the live session id, starting it from its engine row when it is not live. The process starts in
// the row's directory and resumes the row's claude session, when it has one. It reserves a slot under the cap before it
// reads the directory, so a full cap refuses the resume before anything is recorded. A directory that is missing or not
// a directory records the session failed and returns an error wrapping ErrBadDir. resumeMu makes concurrent calls for one
// id start a single process: each call after the first finds the session live.
func (m *Manager) resumeSession(ctx context.Context, id string) (*liveSession, error) {
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	if live, err := m.live(id); err == nil {
		return live, nil
	}
	row, err := m.engineRow(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.resume(ctx, row)
}

// resume starts the process of row, an engine row that is not live, and registers it live. It takes a slot under the cap
// first, then checks the row's directory. A failed check or spawn records the row failed and frees the slot.
func (m *Manager) resume(ctx context.Context, row store.EngineRow) (*liveSession, error) {
	if err := m.reserve(); err != nil {
		return nil, err
	}
	if err := checkDir(row.Dir); err != nil {
		return nil, errors.Join(err, m.fail(ctx, row.ID, row.Dir, err))
	}
	live, err := m.spawnLive(ctx, row.ID, row.Dir, row.ClaudeSession)
	if err != nil {
		return nil, errors.Join(err, m.fail(ctx, row.ID, row.Dir, err))
	}
	return live, nil
}

// engineRow returns the engine row with id. It returns an error wrapping ErrNoSession when no engine row has that id.
func (m *Manager) engineRow(ctx context.Context, id string) (store.EngineRow, error) {
	row, err := m.store.EngineSession(ctx, id)
	switch {
	case errors.Is(err, store.ErrNoEngineSession):
		return store.EngineRow{}, fmt.Errorf("%w: %w", ErrNoSession, err)
	case err != nil:
		return store.EngineRow{}, fmt.Errorf("read engine row %s: %w", id, err)
	}
	return row, nil
}

// spawnLive starts the process for id in dir, resuming claudeSession when it is set, and registers it as live under a
// pump that records its events. The caller holds the slot from reserve. A process that starts after the manager closed
// is stopped, and the error reports the close. An error that returns here leaves nothing registered.
func (m *Manager) spawnLive(ctx context.Context, id, dir, claudeSession string) (*liveSession, error) {
	sess, err := m.spawn(id, dir, claudeSession)
	if err != nil {
		return nil, err
	}
	pumpCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	live := &liveSession{id: id, dir: dir, sess: sess, done: make(chan struct{}), cancel: cancel}
	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.sessions[id] = live
		m.pumps.Add(1)
	}
	m.mu.Unlock()
	if closed {
		cancel()
		return nil, errors.Join(errManagerClosed, sess.Stop())
	}
	rec := newRecorder(id)
	rec.claudeSession = claudeSession
	go m.pump(pumpCtx, live, rec)
	return live, nil
}

// Close stops every live session and waits up to 5 s for each to record its state. A session whose turn was running is
// recorded interrupted, and every session loses its unfinished text. It then cancels any write a pump still has blocked
// and waits for the pumps to return. Start refuses from the moment Close begins, and later calls to Close return at
// once.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	running := make([]*liveSession, 0, len(m.sessions))
	for _, live := range m.sessions {
		live.closing = true
		running = append(running, live)
	}
	m.mu.Unlock()

	for _, live := range running {
		if err := live.sess.Stop(); err != nil {
			m.logger.Warn("session stop failed on close", "session", live.id, "err", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), closeWait)
	defer cancel()
	for _, live := range running {
		if err := m.recordClosed(ctx, live); err != nil {
			m.logger.Warn("record session closed", "session", live.id, "err", err)
		}
	}
	for _, live := range running {
		live.cancel()
	}
	m.pumps.Wait()
}

// checkStart reports whether a start's directory and prompt are usable, before anything is stored.
func checkStart(dir, prompt string) error {
	if err := checkDir(dir); err != nil {
		return err
	}
	if prompt == "" {
		return ErrEmptyPrompt
	}
	return nil
}

// checkDir reports whether dir is an absolute path to an existing directory. It returns an error wrapping ErrBadDir
// when it is not.
func checkDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %q is not absolute", ErrBadDir, dir)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrBadDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrBadDir, dir)
	}
	return nil
}

// reserve takes one slot under the cap for a start. It refuses when the manager is closed or the cap is full.
func (m *Manager) reserve() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	if m.reserved >= m.limit {
		return fmt.Errorf("%w: %d live sessions, cap %d", ErrCapReached, m.reserved, m.limit)
	}
	m.reserved++
	return nil
}

// release frees a slot taken by reserve for a session that never started running.
func (m *Manager) release() {
	m.mu.Lock()
	m.reserved--
	m.mu.Unlock()
}

// newSessionID returns 128 random bits in hex.
func newSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("session id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// spawn resolves claude and starts the session process for id in dir, resuming claudeSession when it is set. The prompt
// is not sent here.
func (m *Manager) spawn(id, dir, claudeSession string) (*session.Session, error) {
	claude, err := exec.LookPath(m.claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	sess, err := session.Start(session.Config{
		Shell:          m.shell,
		Claude:         claude,
		Dir:            dir,
		PermissionMode: permissionMode,
		Resume:         claudeSession,
		Name:           id,
		Environ:        m.environ,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	return sess, nil
}

// pump records the events of one running session, in order, until its events close, and notifies the owner of each
// event that calls for it once the event is recorded. It is the only reader of the session's events and the only user
// of its recorder. When it ends it frees the session's slot and removes its entry, only while that entry is its own.
func (m *Manager) pump(ctx context.Context, live *liveSession, rec *recorder) {
	defer m.pumps.Done()
	reply := ""
	for event := range live.sess.Events() {
		record, interrupted := m.noteEvent(live, event)
		if !record {
			continue
		}
		if text, ok := mainText(event); ok {
			reply = text
		}
		if ops := rec.ops(event); len(ops) > 0 {
			live.order.Lock()
			err := m.writer.Put(ctx, live.id, ops...)
			live.order.Unlock()
			if err != nil {
				m.logger.WarnContext(ctx, "record session event", "session", live.id, "err", err)
			} else if state, body, notify := eventNotice(event, reply, interrupted); notify {
				m.notify(ctx, live.id, live.dir, state, body)
			}
		}
		if _, ended := event.(session.TurnEnded); ended {
			reply = ""
		}
	}
	m.mu.Lock()
	if m.sessions[live.id] == live {
		delete(m.sessions, live.id)
	}
	m.reserved--
	m.mu.Unlock()
	close(live.done)
	live.cancel()
}

// setTurning sets whether live has a turn running.
func (m *Manager) setTurning(live *liveSession, turning bool) {
	m.mu.Lock()
	live.turning = turning
	m.mu.Unlock()
}

// setInterrupting marks the turn running on live as interrupted, so its end sends no notice. A mark needs a turn that
// is running, so an interrupt sent between turns marks nothing.
func (m *Manager) setInterrupting(live *liveSession, interrupting bool) {
	m.mu.Lock()
	live.interrupting = interrupting && live.turning
	m.mu.Unlock()
}

// noteEvent tracks the turn of live through event. It reports whether the pump records event, and for a turn's end,
// whether that turn was interrupted through Interrupt, which clears the mark for the next turn. The exit of a session
// that Close is stopping is not recorded here: recordClosed records that session's state from its turn instead.
func (m *Manager) noteEvent(live *liveSession, event session.Event) (record, interrupted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch event.(type) {
	case session.Started:
		live.turning = true
	case session.TurnEnded:
		live.turning = false
		interrupted = live.interrupting
		live.interrupting = false
	case session.Exited:
		live.turning = false
		if live.closing {
			return false, false
		}
		live.exited = true
	}
	return true, interrupted
}

// notify sends the owner a notice of session id in dir, in state with body. It sends nothing without a notifier.
func (m *Manager) notify(ctx context.Context, id, dir, state, body string) {
	if m.notifier == nil {
		return
	}
	m.notifier.Notify(ctx, Notice{Session: id, Dir: dir, State: state, Body: body})
}

// waitPump waits until the pump of live has finished, or until ctx ends.
func (m *Manager) waitPump(ctx context.Context, live *liveSession) error {
	select {
	case <-live.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for session %s pump: %w", live.id, ctx.Err())
	}
}

// recordStopped waits for the pump of live to finish, then records live stopped, with its unfinished text removed,
// unless its exit already recorded both. A stopped session's turn has ended, and a stopped process delivers no exit
// of its own. It returns the error of the wait or of the write.
func (m *Manager) recordStopped(ctx context.Context, live *liveSession) error {
	if err := m.waitPump(ctx, live); err != nil {
		return err
	}
	if live.exited {
		return nil
	}
	stopped := stateStopped
	err := m.writer.Commit(ctx, live.id, store.EngineTextClear{ID: live.id}, store.EngineSet{ID: live.id,
		Changes: store.EngineChanges{State: &stopped}})
	if err != nil {
		return fmt.Errorf("record session %s stopped: %w", live.id, err)
	}
	return nil
}

// recordClosed waits for the pump of live to finish, then records live as Close leaves it: interrupted with the reason
// when its turn was running, and otherwise in the state its turn gave it. Either way its unfinished text is removed. An
// interruption notifies the owner. A session whose exit already recorded its state is left alone. It returns the error
// of the wait or of the write.
func (m *Manager) recordClosed(ctx context.Context, live *liveSession) error {
	if err := m.waitPump(ctx, live); err != nil {
		return err
	}
	if live.exited {
		return nil
	}
	m.mu.Lock()
	turning := live.turning
	m.mu.Unlock()
	ops := []store.EngineOp{store.EngineTextClear{ID: live.id}}
	if turning {
		interrupted := stateInterrupted
		reason := interruptedReason
		ops = append(ops, store.EngineSet{ID: live.id,
			Changes: store.EngineChanges{State: &interrupted, Reason: &reason}})
	}
	if err := m.writer.Commit(ctx, live.id, ops...); err != nil {
		return fmt.Errorf("record session %s closed: %w", live.id, err)
	}
	if turning {
		m.notify(ctx, live.id, live.dir, stateInterrupted, interruptedReason)
	}
	return nil
}

// abandon stops a session whose first prompt could not be sent, and records it failed with the reason. It returns
// only an error that recording the failure hit.
func (m *Manager) abandon(ctx context.Context, live *liveSession, cause error) error {
	stopErr := live.sess.Stop()
	if err := m.waitPump(ctx, live); err != nil {
		return errors.Join(cause, stopErr, err)
	}
	if live.exited {
		return nil
	}
	return m.recordFailed(ctx, live.id, live.dir, errors.Join(cause, stopErr))
}

// fail records a session in dir that never got a running process as failed with the reason err, and frees its slot.
func (m *Manager) fail(ctx context.Context, id, dir string, err error) error {
	m.release()
	return m.recordFailed(ctx, id, dir, err)
}

// recordFailed writes the failed state of session id in dir with the reason err, then notifies the owner of it. It
// returns only an error that the write hit.
func (m *Manager) recordFailed(ctx context.Context, id, dir string, err error) error {
	failed := stateFailed
	reason := err.Error()
	if writeErr := m.writer.Commit(ctx, id, store.EngineSet{ID: id,
		Changes: store.EngineChanges{State: &failed, Reason: &reason}}); writeErr != nil {
		return fmt.Errorf("record session %s failed: %w", id, writeErr)
	}
	m.notify(ctx, id, dir, failed, reason)
	return nil
}
