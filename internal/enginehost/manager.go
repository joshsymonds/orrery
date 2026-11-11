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

// ErrNoSession reports a Stop or Seen of an id this manager did not start, or of a session that is not live.
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

// closeWait bounds how long Close waits for the sessions it stops to record their stop.
const closeWait = 5 * time.Second

// Config configures a Manager. Shell is the login shell and Claude the claude name, resolved with exec.LookPath at
// each start. Environ is the environment sessions start with, Cap the most live sessions at once, Writer stores their
// ops, Now stamps them (nil is time.Now) and Logger reports pump errors (nil discards).
type Config struct {
	Shell   string
	Claude  string
	Environ []string
	Cap     int
	Writer  *Writer
	Now     func() time.Time
	Logger  *slog.Logger
}

// liveSession is one running session: its process, the done channel closed when its pump has finished, and the cancel
// that frees a pump blocked on a write. exited is set by the pump when the process exited on its own, so Stop does not
// record over the state that exit recorded.
type liveSession struct {
	id     string
	sess   *session.Session
	done   chan struct{}
	cancel context.CancelFunc
	exited bool
	// order is held by the pump while it queues an event's ops, and by Answer from its send until it has queued the
	// working state, so every op an answer causes is queued after that state.
	order sync.Mutex
}

// Manager starts, stops and tracks headless sessions. Each running session has a pump goroutine that records its
// events through the Writer. Close stops every session and waits for the pumps.
type Manager struct {
	shell   string
	claude  string
	environ []string
	limit   int
	writer  *Writer
	now     func() time.Time
	logger  *slog.Logger

	pumps sync.WaitGroup

	mu sync.Mutex
	// started holds every id Start committed, so Seen can name sessions that have since stopped.
	started map[string]struct{}
	// sessions holds the running sessions whose pump has not finished.
	sessions map[string]*liveSession
	// reserved counts the sessions being started or running, which is what the cap limits.
	reserved int
	closed   bool
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
		logger:   cfg.Logger,
		started:  map[string]struct{}{},
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
	m.mu.Lock()
	m.started[id] = struct{}{}
	m.mu.Unlock()

	sess, err := m.spawn(id, dir)
	if err != nil {
		return id, m.fail(ctx, id, err)
	}
	pumpCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	live := &liveSession{id: id, sess: sess, done: make(chan struct{}), cancel: cancel}
	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.sessions[id] = live
		m.pumps.Add(1)
	}
	m.mu.Unlock()
	if closed {
		cancel()
		return id, m.fail(ctx, id, errors.Join(errManagerClosed, sess.Stop()))
	}
	go m.pump(pumpCtx, live, newRecorder(id))

	if err = m.writer.Put(ctx, id, store.EnginePrompt{ID: id, Text: prompt}); err != nil {
		return id, m.abandon(ctx, live, err)
	}
	if err = sess.Prompt(prompt); err != nil {
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

// Seen marks the session id this manager started as seen now. It returns ErrNoSession for any other id.
func (m *Manager) Seen(ctx context.Context, id string) error {
	m.mu.Lock()
	_, ok := m.started[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("mark session %s seen: %w", id, ErrNoSession)
	}
	seen := strconv.FormatInt(m.now().Unix(), 10)
	return m.writer.Commit(ctx, id, store.EngineSet{ID: id, Changes: store.EngineChanges{Seen: &seen}})
}

// Say sends text to the live session id as its next prompt. It records the prompt and the working state before it
// sends, so the stored conversation has the prompt ahead of its reply. It returns ErrEmptyPrompt for empty text and
// an error wrapping ErrNoSession when id is not live. A send that fails is recorded as the session's failed state.
func (m *Manager) Say(ctx context.Context, id, text string) error {
	if text == "" {
		return ErrEmptyPrompt
	}
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("say to session %s: %w", id, err)
	}
	working := string(protocol.StateWorking)
	if err = m.writer.Commit(ctx, id, store.EnginePrompt{ID: id, Text: text},
		store.EngineSet{ID: id, Changes: store.EngineChanges{State: &working}}); err != nil {
		return fmt.Errorf("record prompt of session %s: %w", id, err)
	}
	if err = live.sess.Prompt(text); err != nil {
		return fmt.Errorf("say to session %s: %w", id, errors.Join(err, m.recordFailed(ctx, id, err)))
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
// it. It returns an error wrapping ErrNoSession when id is not live.
func (m *Manager) Interrupt(_ context.Context, id string) error {
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	if err = live.sess.Interrupt(); err != nil {
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

// Close stops every live session and waits up to 5 s for each to record its stop. It then cancels any write a pump
// still has blocked and waits for the pumps to return. Start refuses from the moment Close begins, and later calls
// to Close return at once.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	running := make([]*liveSession, 0, len(m.sessions))
	for _, live := range m.sessions {
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
		if err := m.recordStopped(ctx, live); err != nil {
			m.logger.Warn("record session stopped on close", "session", live.id, "err", err)
		}
	}
	for _, live := range running {
		live.cancel()
	}
	m.pumps.Wait()
}

// checkStart reports whether a start's directory and prompt are usable, before anything is stored.
func checkStart(dir, prompt string) error {
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
	if prompt == "" {
		return ErrEmptyPrompt
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

// spawn resolves claude and starts the session process for id in dir. The prompt is not sent here.
func (m *Manager) spawn(id, dir string) (*session.Session, error) {
	claude, err := exec.LookPath(m.claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	sess, err := session.Start(session.Config{
		Shell:          m.shell,
		Claude:         claude,
		Dir:            dir,
		PermissionMode: permissionMode,
		Name:           id,
		Environ:        m.environ,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	return sess, nil
}

// pump records the events of one running session, in order, until its events close. It is the only reader of the
// session's events and the only user of its recorder. When it ends it frees the session's slot.
func (m *Manager) pump(ctx context.Context, live *liveSession, rec *recorder) {
	defer m.pumps.Done()
	for event := range live.sess.Events() {
		if _, ok := event.(session.Exited); ok {
			live.exited = true
		}
		if ops := rec.ops(event); len(ops) > 0 {
			live.order.Lock()
			err := m.writer.Put(ctx, live.id, ops...)
			live.order.Unlock()
			if err != nil {
				m.logger.WarnContext(ctx, "record session event", "session", live.id, "err", err)
			}
		}
	}
	m.mu.Lock()
	delete(m.sessions, live.id)
	m.reserved--
	m.mu.Unlock()
	close(live.done)
	live.cancel()
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
	return m.recordFailed(ctx, live.id, errors.Join(cause, stopErr))
}

// fail records a session that never got a running process as failed with the reason err, and frees its slot.
func (m *Manager) fail(ctx context.Context, id string, err error) error {
	m.release()
	return m.recordFailed(ctx, id, err)
}

// recordFailed writes the failed state of session id with the reason err. It returns only an error that the write hit.
func (m *Manager) recordFailed(ctx context.Context, id string, err error) error {
	failed := stateFailed
	reason := err.Error()
	if writeErr := m.writer.Commit(ctx, id, store.EngineSet{ID: id,
		Changes: store.EngineChanges{State: &failed, Reason: &reason}}); writeErr != nil {
		return fmt.Errorf("record session %s failed: %w", id, writeErr)
	}
	return nil
}
