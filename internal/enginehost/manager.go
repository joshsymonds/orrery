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

// ErrBadModel reports a Start whose model is set but is not 1 to 64 bytes of letters, digits and . _ - [ ] : that does
// not start with -.
var ErrBadModel = errors.New("session model must be 1 to 64 letters, digits or . _ - [ ] : and not start with -")

// ErrBadID reports a Start whose id is set but is not 32 lowercase hex digits.
var ErrBadID = errors.New("session id must be 32 lowercase hex digits")

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

// idleCheckEvery is the pause between the checks RunIdle makes.
const idleCheckEvery = 30 * time.Second

// defaultIdleAfter is how long a session must stay not busy, by the manager's clock, before RunIdle stops it.
const defaultIdleAfter = 10 * time.Minute

// interruptedReason is the reason a session whose turn was running when Close stopped the manager records.
const interruptedReason = "orreryd stopped during the turn"

// NoticeSink receives the notices the owner is told about. *Notifier satisfies it.
type NoticeSink interface {
	Notify(ctx context.Context, n Notice)
}

// Store reads the engine rows Reconcile settles, the rows Say resumes, checks and Seen marks, and the failure markers
// Say checks. *store.Store satisfies it.
type Store interface {
	EngineSessions(ctx context.Context) ([]store.EngineRow, error)
	EngineSession(ctx context.Context, id string) (store.EngineRow, error)
	EnginePromptSent(ctx context.Context, sessionID, promptID string) (bool, error)
	EnginePromptFailure(ctx context.Context, sessionID, promptID string) (string, bool, error)
}

// Config configures a Manager. Shell is the login shell and Claude the claude name, resolved with exec.LookPath at
// each start. Environ is the environment sessions start with, Cap the most live sessions at once, StepCap the most
// live step sessions at once, counted apart from Cap, Writer stores their ops, Now stamps them (nil is time.Now),
// Notifier receives the notices of their state changes (nil sends none), Store is the engine rows Reconcile, Say and
// Seen read and Logger reports pump errors (nil discards).
type Config struct {
	Shell    string
	Claude   string
	Environ  []string
	Cap      int
	StepCap  int
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
// manager's lock guards turning, interrupting, closing, requests, background, returned, idleSince and idleStopped, and
// the pump sets exited under it too. requests holds the ids of the requests the host has open, background is set while a
// background task runs, idleSince is when the session last became not busy (zero while it is busy), and idleStopped is
// set when a check stops the session for being idle. step is set for a step session, which sends no notice and frees a
// step slot rather than a session slot. observer receives the hand-offs of a step, which the pump makes, and returned is
// set from a hand-off until Continue sends the step's next prompt, so the step stays busy meanwhile.
type liveSession struct {
	id           string
	dir          string
	sess         *session.Session
	done         chan struct{}
	cancel       context.CancelFunc
	step         bool
	observer     StepObserver
	exited       bool
	turning      bool
	interrupting bool
	closing      bool
	requests     map[string]struct{}
	background   bool
	returned     bool
	idleSince    time.Time
	idleStopped  bool
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
	// reserved counts the sessions being started or running, which is what the cap limits. stepLimit is the cap on
	// step sessions, and stepsRunning counts the step sessions being started or running, apart from reserved.
	reserved     int
	stepLimit    int
	stepsRunning int
	closed       bool

	// resumeMu serializes the resume of a session that is not live, so concurrent Says to one id start one process.
	resumeMu sync.Mutex
	// attemptMu guards attempts. attempts holds the keyed calls running now, by key: a Start that names a session id, or
	// a Say that names a prompt id. A key's attempt is removed when it ends, so a later call with that key checks the
	// store as an unkeyed call would. An attempt that failed after it stored its record stays, with its error, so a
	// later call with that key returns the error. A Say whose send failed also stores a failure marker for its prompt,
	// which a later Say with that key returns after a restart too.
	attemptMu sync.Mutex
	attempts  map[attemptKey]*attempt
}

// NewManager returns a Manager for cfg.
func NewManager(cfg Config) *Manager {
	manager := &Manager{
		shell:     cfg.Shell,
		claude:    cfg.Claude,
		environ:   cfg.Environ,
		limit:     cfg.Cap,
		stepLimit: cfg.StepCap,
		writer:    cfg.Writer,
		now:       cfg.Now,
		notifier:  cfg.Notifier,
		store:     cfg.Store,
		logger:    cfg.Logger,
		sessions:  map[string]*liveSession{},
		attempts:  map[attemptKey]*attempt{},
	}
	if manager.now == nil {
		manager.now = time.Now
	}
	if manager.logger == nil {
		manager.logger = slog.New(slog.DiscardHandler)
	}
	return manager
}

// StartRequest is what Start needs: the directory the session runs in, its first prompt, and the model alias it runs.
// An empty Model runs claude's default. An empty ID mints a session id; a non-empty ID is the id the session takes.
type StartRequest struct {
	Dir    string
	Prompt string
	Model  string
	ID     string
}

// Start starts a session as req describes and returns its id. An empty req.ID mints one. A non-empty req.ID must be 32
// lowercase hex digits, or Start returns an error matching ErrBadID before it stores anything. A non-empty req.ID runs
// as one attempt: while an attempt for it runs, a Start with that id waits for the attempt and returns its result, and
// starts nothing. Once begun, the attempt finishes even if ctx ends, and a Start whose ctx ends returns ctx's error. With
// no attempt running, a req.ID that already has an engine row returns that id and does nothing else. An attempt that
// fails after it stores the row keeps its error, and a later Start with that id returns the error and does nothing else.
// An attempt that fails before it stores the row frees the id. Start records the session as working before it spawns
// the process. A spawn or prompt failure is recorded as the session's failed state with the reason, and Start returns
// the id with no error.
func (m *Manager) Start(ctx context.Context, req StartRequest) (string, error) {
	if req.ID == "" {
		id, err := newSessionID()
		if err != nil {
			return "", err
		}
		return m.start(ctx, id, req)
	}
	if err := checkID(req.ID); err != nil {
		return "", err
	}
	return m.keyed(ctx, attemptKey{session: req.ID},
		func(ctx context.Context) (string, error) {
			_, err := m.engineRow(ctx, req.ID)
			switch {
			case err == nil:
				return req.ID, nil
			case !errors.Is(err, ErrNoSession):
				return "", err
			}
			return m.start(ctx, req.ID, req)
		},
		func(ctx context.Context) bool {
			_, err := m.engineRow(ctx, req.ID)
			return !errors.Is(err, ErrNoSession)
		},
	)
}

// attemptKey names a keyed call: a Start of a session id, which has an empty prompt, or a Say of a prompt id in a
// session, which has a non-empty one. So a Start and a Say never share a key.
type attemptKey struct {
	session string
	prompt  string
}

// attempt is a keyed call that is running or has ended. done closes once id and err hold the call's result.
type attempt struct {
	done chan struct{}
	id   string
	err  error
}

// keyed runs the keyed call key as one attempt, unless an attempt with key is running or has kept its error, which it
// waits for or returns instead. run receives a context that ctx's cancellation does not reach, so the attempt finishes
// its store, spawn or resume, and send even when ctx ends. A caller whose ctx ends first returns ctx's error and leaves
// the attempt running. recorded reports whether the record of key exists, or cannot be checked, and is asked only of an
// attempt that returned an error (see execute).
func (m *Manager) keyed(
	ctx context.Context,
	key attemptKey,
	run func(context.Context) (string, error),
	recorded func(context.Context) bool,
) (string, error) {
	m.attemptMu.Lock()
	running, ok := m.attempts[key]
	if !ok {
		running = &attempt{done: make(chan struct{})}
		m.attempts[key] = running
		go m.execute(context.WithoutCancel(ctx), key, running, run, recorded)
	}
	m.attemptMu.Unlock()
	select {
	case <-running.done:
		return running.id, running.err
	case <-ctx.Done():
		return "", fmt.Errorf("wait for keyed call: %w", ctx.Err())
	}
}

// execute runs the attempt at with run, then wakes the callers that wait for it. It frees key unless the attempt
// failed after it stored its record, which keeps key with the error for later calls. An attempt whose record cannot be
// checked is kept too.
func (m *Manager) execute(
	ctx context.Context,
	key attemptKey,
	at *attempt,
	run func(context.Context) (string, error),
	recorded func(context.Context) bool,
) {
	at.id, at.err = run(ctx)
	if at.err == nil || !recorded(ctx) {
		m.attemptMu.Lock()
		delete(m.attempts, key)
		m.attemptMu.Unlock()
	}
	close(at.done)
}

// start starts the session id as req describes. It checks req and takes a slot under the cap, records the row, then
// spawns the process and sends the first prompt. A failure before the row is stored frees the slot.
func (m *Manager) start(ctx context.Context, id string, req StartRequest) (string, error) {
	if err := checkStart(req); err != nil {
		return "", err
	}
	if err := m.reserve(); err != nil {
		return "", err
	}
	add := store.EngineAdd{ID: id, Dir: req.Dir, State: string(protocol.StateWorking)}
	if err := m.writer.Commit(ctx, id, add); err != nil {
		m.release()
		return "", fmt.Errorf("record session %s: %w", id, err)
	}
	live, err := m.spawnLive(ctx, id, req.Dir, spawnSpec{model: req.Model})
	if err != nil {
		return id, m.fail(ctx, id, req.Dir, err)
	}
	if err = m.writer.Put(ctx, id, store.EnginePrompt{ID: id, Text: req.Prompt}); err != nil {
		return id, m.abandon(ctx, live, err)
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(req.Prompt); err != nil {
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

// SayRequest is what Say needs: the session ID, the text of the prompt, and the client-chosen PromptID. An empty
// PromptID is not deduplicated.
type SayRequest struct {
	ID       string
	Text     string
	PromptID string
}

// Say sends req.Text to the session req.ID as its next prompt. A session that is not live is resumed first, from its
// engine row (see resumeSession). It records the prompt and the working state before it sends, so the stored
// conversation has the prompt ahead of its reply. A non-empty req.PromptID runs as one attempt per session and prompt
// id: while an attempt for it runs, a Say with that id waits for the attempt and returns its result, and sends nothing.
// Once begun, the attempt finishes even if ctx ends, and a Say whose ctx ends returns ctx's error. With no attempt
// running, a req.PromptID that an engine_prompt event of the session already carries makes Say return nil and do
// nothing else; otherwise the stored prompt carries req.PromptID. An attempt that fails after it stores the prompt keeps
// its error, and a later Say with that prompt id returns the error and does nothing else. A send that fails is recorded
// as the session's failed state; with a req.PromptID, the same write stores that prompt's failure marker, so a Say with
// that prompt id returns the stored reason and does nothing else, even after a restart. It returns ErrEmptyPrompt for
// empty text, an error wrapping ErrNoSession when no engine row has the id, and the errors of resumeSession when a live
// session cannot be started.
func (m *Manager) Say(ctx context.Context, req SayRequest) error {
	if req.Text == "" {
		return ErrEmptyPrompt
	}
	if req.PromptID == "" {
		return m.say(ctx, req)
	}
	_, err := m.keyed(
		ctx,
		attemptKey{session: req.ID, prompt: req.PromptID},
		func(ctx context.Context) (string, error) {
			reason, failed, err := m.store.EnginePromptFailure(ctx, req.ID, req.PromptID)
			if err != nil {
				return "", fmt.Errorf("check failure of prompt %s of session %s: %w", req.PromptID, req.ID, err)
			}
			if failed {
				return "", fmt.Errorf("prompt %s of session %s failed to send: %s", req.PromptID, req.ID, reason)
			}
			sent, err := m.store.EnginePromptSent(ctx, req.ID, req.PromptID)
			if err != nil {
				return "", fmt.Errorf("check prompt %s of session %s: %w", req.PromptID, req.ID, err)
			}
			if sent {
				return "", nil
			}
			return "", m.say(ctx, req)
		},
		func(ctx context.Context) bool {
			sent, err := m.store.EnginePromptSent(ctx, req.ID, req.PromptID)
			return sent || err != nil
		},
	)
	return err
}

// say sends the prompt of req as Say does, without the check of its prompt id.
func (m *Manager) say(ctx context.Context, req SayRequest) error {
	live, err := m.claim(req.ID)
	if errors.Is(err, ErrNoSession) {
		live, err = m.resumeSession(ctx, req.ID)
		if err == nil {
			m.setTurning(live, true)
		}
	}
	if err != nil {
		return fmt.Errorf("say to session %s: %w", req.ID, err)
	}
	return m.send(ctx, live, req)
}

// send records req.Text as the next prompt of live, with the working state, and then sends it. live must have its turn
// marked running. A failed send records the session failed with the send's error, and with a req.PromptID the same write
// stores that prompt's failure marker. A step sends no notice, so its failed send is recorded without one.
func (m *Manager) send(ctx context.Context, live *liveSession, req SayRequest) error {
	id, text := req.ID, req.Text
	working := string(protocol.StateWorking)
	if err := m.writer.Commit(ctx, id, store.EnginePrompt{ID: id, Text: text, PromptID: req.PromptID},
		store.EngineSet{ID: id, Changes: store.EngineChanges{State: &working}}); err != nil {
		m.setTurning(live, false)
		return fmt.Errorf("record prompt of session %s: %w", id, err)
	}
	if err := live.sess.Prompt(text); err != nil {
		m.setTurning(live, false)
		var marks []store.EngineOp
		if req.PromptID != "" {
			marks = append(marks, store.EnginePromptFailed{ID: id, PromptID: req.PromptID, Reason: err.Error()})
		}
		// A step sends no notice, so its failed send is recorded without one.
		var recordErr error
		if live.step {
			recordErr = m.writeFailed(ctx, id, err, marks...)
		} else {
			recordErr = m.recordFailed(ctx, id, live.dir, err, marks...)
		}
		return fmt.Errorf("send prompt to session %s: %w", id, errors.Join(err, recordErr))
	}
	return nil
}

// Continue sends text to the live step id as its next prompt, which ends the step's hand-off: the step is busy again
// until this turn ends. The prompt is recorded as Say records one, and a failed send records the step failed without a
// notice. It returns an error wrapping ErrNoSession when no live step has id, and ErrEmptyPrompt for empty text.
func (m *Manager) Continue(ctx context.Context, id, text string) error {
	if text == "" {
		return ErrEmptyPrompt
	}
	live, err := m.claimStep(id)
	if err != nil {
		return fmt.Errorf("continue step %s: %w", id, err)
	}
	return m.send(ctx, live, SayRequest{ID: id, Text: text})
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
	m.closeRequest(live, requestID)
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

// Live returns the ids of the live sessions. It is safe to call from any goroutine.
func (m *Manager) Live() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
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
	if row.Kind == store.KindStep {
		return nil, fmt.Errorf("%w: %s is a step session", ErrNoSession, id)
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
	live, err := m.spawnLive(ctx, row.ID, row.Dir, spawnSpec{claudeSession: row.ClaudeSession})
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

// spawnLive starts the process for id in dir as sp describes, and registers it as live under a pump that records its
// events. The caller holds the slot from reserve or reserveStep. A process that starts after the manager closed is
// stopped, and the error reports the close. An error that returns here leaves nothing registered.
func (m *Manager) spawnLive(ctx context.Context, id, dir string, sp spawnSpec) (*liveSession, error) {
	sess, err := m.spawn(id, dir, sp)
	if err != nil {
		return nil, err
	}
	pumpCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	live := &liveSession{id: id, dir: dir, sess: sess, done: make(chan struct{}), cancel: cancel,
		step: sp.step, observer: sp.observer, requests: map[string]struct{}{}}
	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.sessions[id] = live
		m.pumps.Add(1)
		m.updateIdleLocked(live)
	}
	m.mu.Unlock()
	if closed {
		cancel()
		return nil, errors.Join(errManagerClosed, sess.Stop())
	}
	rec := newRecorder(id)
	rec.claudeSession = sp.claudeSession
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

// RunIdle stops the sessions that are idle for the idle timeout, checking every 30 s, until ctx ends. See checkIdle.
func (m *Manager) RunIdle(ctx context.Context) {
	ticker := time.NewTicker(idleCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkIdle(ctx)
		}
	}
}

// checkIdle stops each live session that has been not busy for the idle timeout by the clock, and waits for its pump,
// which frees its slot. A session is busy while a turn runs, a request is open, or a background task runs. The stop
// records nothing, so the session's row and events stay as they were. The session is taken out of the live set under
// the lock before it is stopped, and resumeMu is held through the stop, so a Say that finds no live session waits, then
// resumes it from its row.
func (m *Manager) checkIdle(ctx context.Context) {
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	now := m.now()
	m.mu.Lock()
	var stopping []*liveSession
	for id, live := range m.sessions {
		if live.closing || live.busy() || now.Sub(live.idleSince) < defaultIdleAfter {
			continue
		}
		live.idleStopped = true
		delete(m.sessions, id)
		stopping = append(stopping, live)
	}
	m.mu.Unlock()
	for _, live := range stopping {
		if err := live.sess.Stop(); err != nil {
			m.logger.WarnContext(ctx, "stop idle session", "session", live.id, "err", err)
		}
		if err := m.waitPump(ctx, live); err != nil {
			m.logger.WarnContext(ctx, "wait for idle session", "session", live.id, "err", err)
		}
	}
}

// busy reports whether live has a turn running, an open request, a background task, or a hand-off the next prompt has
// not yet answered. The caller holds the lock.
func (live *liveSession) busy() bool {
	return live.turning || live.background || live.returned || len(live.requests) > 0
}

// updateIdleLocked clears the idle stamp of live while it is busy, and stamps the time it became not busy. The caller
// holds the lock.
func (m *Manager) updateIdleLocked(live *liveSession) {
	if live.busy() {
		live.idleSince = time.Time{}
		return
	}
	if live.idleSince.IsZero() {
		live.idleSince = m.now()
	}
}

// claim returns the live session id with its turn marked running, under the lock, so a check cannot stop the session
// between the lookup and the prompt. It returns ErrNoSession when no session with that id is live.
func (m *Manager) claim(id string) (*liveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok {
		return nil, ErrNoSession
	}
	live.turning = true
	m.updateIdleLocked(live)
	return live, nil
}

// claimStep returns the live step id with its turn marked running and its hand-off ended, under the lock, so a check
// cannot stop the step between the lookup and the prompt. It returns ErrNoSession when no live step has that id.
func (m *Manager) claimStep(id string) (*liveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok || !live.step {
		return nil, ErrNoSession
	}
	live.turning = true
	live.returned = false
	m.updateIdleLocked(live)
	return live, nil
}

// closeRequest removes the open request requestID from live once the session has accepted its answer.
func (m *Manager) closeRequest(live *liveSession, requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(live.requests, requestID)
	m.updateIdleLocked(live)
}

// checkStart reports whether a start's directory, prompt and model are usable, before anything is stored.
func checkStart(req StartRequest) error {
	if err := checkDir(req.Dir); err != nil {
		return err
	}
	if req.Prompt == "" {
		return ErrEmptyPrompt
	}
	return checkModel(req.Model)
}

// checkID reports whether id is 32 lowercase hex digits, the form newSessionID mints. It returns ErrBadID otherwise.
func checkID(id string) error {
	if len(id) != 32 {
		return ErrBadID
	}
	for i := range len(id) {
		if !isHexByte(id[i]) {
			return ErrBadID
		}
	}
	return nil
}

// isHexByte reports whether c is a lowercase hex digit.
func isHexByte(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f')
}

// maxModelBytes is the longest model alias a start may name.
const maxModelBytes = 64

// checkModel reports whether model is usable. An empty model runs claude's default. Any other model must be 1 to
// maxModelBytes bytes of letters, digits and . _ - [ ] :, not starting with -. It returns ErrBadModel otherwise. The
// model reaches claude's argv, so only these bytes may pass.
func checkModel(model string) error {
	if model == "" {
		return nil
	}
	if len(model) > maxModelBytes || model[0] == '-' {
		return ErrBadModel
	}
	for i := range len(model) {
		if !isModelByte(model[i]) {
			return ErrBadModel
		}
	}
	return nil
}

// isModelByte reports whether c may appear in a model alias.
func isModelByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == '.', c == '_', c == '-', c == '[', c == ']', c == ':':
		return true
	}
	return false
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

// spawnSpec describes the process a spawn starts: the claude session it resumes, the model it runs, and for a step the
// agent and schema it runs with, and the observer of its hand-offs. step marks a step session.
type spawnSpec struct {
	claudeSession string
	model         string
	agent         string
	schema        string
	step          bool
	observer      StepObserver
}

// spawn resolves claude and starts the session process for id in dir as sp describes. The prompt is not sent here.
func (m *Manager) spawn(id, dir string, sp spawnSpec) (*session.Session, error) {
	claude, err := exec.LookPath(m.claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	sess, err := session.Start(session.Config{
		Shell:          m.shell,
		Claude:         claude,
		Dir:            dir,
		PermissionMode: permissionMode,
		Resume:         sp.claudeSession,
		Model:          sp.model,
		Agent:          sp.agent,
		Schema:         sp.schema,
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
		record, interrupted, handed := m.noteEvent(live, event)
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
			} else if state, body, notify := eventNotice(event, reply, interrupted); notify && !live.step {
				m.notify(ctx, live.id, live.dir, state, body)
			}
		}
		if _, ended := event.(session.TurnEnded); ended {
			reply = ""
		}
		observe(live, event, handed)
	}
	m.mu.Lock()
	if m.sessions[live.id] == live {
		delete(m.sessions, live.id)
	}
	if live.step {
		m.stepsRunning--
	} else {
		m.reserved--
	}
	m.mu.Unlock()
	close(live.done)
	live.cancel()
}

// observe gives the observer of the step live the event the pump has just recorded: the turn end of a hand-off, or an
// exit with its failure reason, or stepExitedReason for a clean exit, since a step ends either way. A session without an
// observer gets nothing.
func observe(live *liveSession, event session.Event, handed bool) {
	switch ev := event.(type) {
	case session.TurnEnded:
		if handed {
			live.observer.Return(ev)
		}
	case session.Exited:
		if live.observer == nil {
			return
		}
		reason, failed := exitFailure(ev)
		if !failed {
			reason = stepExitedReason
		}
		live.observer.Exited(reason)
	}
}

// stepExitedReason is the reason a step's observer gets when the step's process exits cleanly on its own.
const stepExitedReason = "the step's process exited"

// setTurning sets whether live has a turn running.
func (m *Manager) setTurning(live *liveSession, turning bool) {
	m.mu.Lock()
	live.turning = turning
	m.updateIdleLocked(live)
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
// whether that turn was interrupted through Interrupt, which clears the mark for the next turn, and whether the turn is
// handed to the step's observer: a step's turn that ends with no background task live. The hand-off makes the step busy
// in the same lock hold. The exit of a session that Close is stopping is not recorded here: recordClosed records that
// session's state from its turn instead.
func (m *Manager) noteEvent(live *liveSession, event session.Event) (record, interrupted, handed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.updateIdleLocked(live)
	switch ev := event.(type) {
	case session.Started:
		live.turning = true
	case session.Request:
		if !ev.Refused {
			live.requests[ev.RequestID] = struct{}{}
		}
	case session.RequestCancelled:
		delete(live.requests, ev.RequestID)
	case session.BackgroundTasks:
		live.background = len(ev.Tasks) > 0
	case session.TurnEnded:
		live.turning = false
		clear(live.requests)
		interrupted = live.interrupting
		live.interrupting = false
		if live.observer != nil && !live.background {
			live.returned = true
			handed = true
		}
	case session.Exited:
		live.turning = false
		clear(live.requests)
		live.background = false
		if live.closing || live.idleStopped {
			return false, false, false
		}
		live.exited = true
	}
	return true, interrupted, handed
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
	if turning && !live.step {
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
// returns only an error that the write hit, and then notifies nothing.
func (m *Manager) recordFailed(ctx context.Context, id, dir string, err error, marks ...store.EngineOp) error {
	if writeErr := m.writeFailed(ctx, id, err, marks...); writeErr != nil {
		return writeErr
	}
	m.notify(ctx, id, dir, stateFailed, err.Error())
	return nil
}

// writeFailed writes the failed state of session id with the reason err, after marks in the same write. It sends no
// notice. It returns only an error that the write hit.
func (m *Manager) writeFailed(ctx context.Context, id string, err error, marks ...store.EngineOp) error {
	failed := stateFailed
	reason := err.Error()
	set := store.EngineSet{ID: id, Changes: store.EngineChanges{State: &failed, Reason: &reason}}
	if writeErr := m.writer.Commit(ctx, id, append(marks, set)...); writeErr != nil {
		return fmt.Errorf("record session %s failed: %w", id, writeErr)
	}
	return nil
}
