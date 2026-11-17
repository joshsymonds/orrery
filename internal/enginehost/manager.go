package enginehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/engine/transport"
	"github.com/joshsymonds/orrery/internal/holder"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/telemetry"
)

// ErrCapReached reports a Start that would exceed the cap of live sessions.
var ErrCapReached = errors.New("session cap reached")

// ErrNoSession reports an id with no engine row, or a Stop, Answer or Interrupt of a session that is not live.
var ErrNoSession = errors.New("no such session")

// ErrNotResumable reports a Resume of an engine row that is not interrupted, or whose session has no Claude session to
// resume. The error's text says which.
var ErrNotResumable = errors.New("session cannot be resumed")

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

// ErrNotHeld reports a Withdraw of a prompt id the session holds no message for: the message was written already, or the
// id is unknown. Withdraw removes nothing.
var ErrNotHeld = errors.New("no held message has that prompt id")

// errSessionEnded reports a delivery to a session whose process ended after the Say found it live. Say resumes the session
// and delivers once more.
var errSessionEnded = errors.New("session ended before the message was delivered")

// ErrSessionBusy reports a Deliver to a live session that has a turn running, a request open, a background task or a
// hand-off. Deliver stores and sends nothing for it.
var ErrSessionBusy = errors.New("session is busy")

// ErrCallerStopped reports a Deliver to a session whose row is stopped, as the owner's Stop leaves it. Deliver resumes
// nothing and sends nothing to it.
var ErrCallerStopped = errors.New("session was stopped")

// ErrCallerQuiet reports a Deliver to a live session that the owner interrupted and has said nothing to since (see
// liveSession). Deliver stores and sends nothing for it.
var ErrCallerQuiet = errors.New("session was interrupted by its owner")

// errManagerClosed reports a Start on a manager that Close has begun closing.
var errManagerClosed = errors.New("session manager is closed")

// permissionMode is the permission mode every session runs in.
const permissionMode = "bypassPermissions"

// denyMessage is the message a permission request denied through Answer gives the session.
const denyMessage = "The orrery host denied this request."

// stepDenyMessage is the message a step's request is denied with. A step has nobody to answer it, so the
// message tells the step to give its answer as the structured return.
const stepDenyMessage = "The orrery host does not answer questions in a step. Do not ask; give your answer as the structured return."

// ownerSpokeMessage is the denial each open request gets when the owner sends a message or an interrupt instead of
// answering it, so the session reads the message instead of waiting on the request.
const ownerSpokeMessage = "The owner did not answer this request; they sent a message or an interrupt instead. Read it and act on it."

// closeWait bounds how long Close waits for the sessions it stops to record their state.
const closeWait = 5 * time.Second

// idleCheckEvery is the pause between the checks RunIdle makes.
const idleCheckEvery = 30 * time.Second

// defaultIdleAfter is how long a session must stay not busy, by the manager's clock, before RunIdle stops it.
const defaultIdleAfter = 10 * time.Minute

// interruptedReason is the reason a session whose turn was running when Close stopped the manager records.
const interruptedReason = "orreryd stopped during the turn"

// NoticeSink receives the notices the owner is told about, the changes that end them, and the seen reports that clear
// them. *Notifier satisfies it.
type NoticeSink interface {
	Notify(ctx context.Context, n Notice)
	Changed(ctx context.Context, session string)
	Seen(ctx context.Context, session string, seen time.Time)
}

// Store reads the engine rows Reconcile settles, the ids of the sessions holding unfinished text that Reconcile clears, the
// rows Say resumes, checks and Seen marks, the prompts a re-adopted holder has not written and how many prompts were written
// that no later turn end answers, the step rows Continue respawns from, the failure markers Say checks, and the messages a
// session holds, which Say, the pump and Withdraw read. *store.Store satisfies it.
type Store interface {
	EngineSessions(ctx context.Context) ([]store.EngineRow, error)
	EngineTextSessions(ctx context.Context) (map[string]bool, error)
	EngineSession(ctx context.Context, id string) (store.EngineRow, error)
	HolderPrompts(ctx context.Context, id string) ([]store.HolderPrompt, error)
	HolderPromptsUnanswered(ctx context.Context, id string) (int, error)
	Step(ctx context.Context, id string) (store.StepRow, error)
	EnginePromptSent(ctx context.Context, sessionID, promptID string) (bool, error)
	EnginePromptFailure(ctx context.Context, sessionID, promptID string) (string, bool, error)
	HeldPrompts(ctx context.Context, id string) ([]store.HeldPrompt, error)
}

// Config configures a Manager. Shell is the login shell and Claude the claude name, resolved with exec.LookPath at
// each start. Environ is the environment sessions start with, Cap the most live sessions at once, StepCap the most
// live step sessions at once, counted apart from Cap, Writer stores their ops, Now stamps them (nil is time.Now),
// Notifier receives the notices of their state changes (nil sends none), Store is the engine rows Reconcile, Say and
// Seen read and Logger reports pump errors (nil discards). HolderScope is the absolute path of systemd-run that runs
// each session's holder in its own user scope, or empty to run holders plainly; with it set, Close detaches the
// sessions instead of stopping them, so their holders keep running. HolderScopeMemory is that scope's memory cap in
// bytes, or zero for holder.DefaultScopeMemory. StateDir is the state directory that holds the
// sessions' holders, or empty for a private directory per session. ManagedDir is the managed settings root that a
// step's agent resolves under, or empty to read none. HolderVersion is the holder wire version this manager's controller
// speaks when it re-adopts a holder, or zero for holder.WireVersion. Metrics counts each holder lost in re-adoption, or
// nil to count none.
type Config struct {
	Shell             string
	Claude            string
	Environ           []string
	HolderScope       string
	HolderScopeMemory int64
	StateDir          string
	ManagedDir        string
	HolderVersion     int
	Cap               int
	StepCap           int
	Writer            *Writer
	Now               func() time.Time
	Notifier          NoticeSink
	Store             Store
	Logger            *slog.Logger
	Metrics           *telemetry.Daemon
}

// liveSession is one running session: its directory, its process, the done channel closed when its pump has finished,
// and the cancel that frees a pump blocked on a write. exited is set by the pump when it records the process's exit, so
// Stop and Close do not record over the state that exit recorded. turning is set while a turn runs, from the send of its
// prompt or a Started event until its TurnEnded or the process's Exited. interrupting is set by Interrupt while a turn
// runs, until that turn's end, which then sends no notice. ownerInterrupt is set by OwnerInterrupt while a turn runs,
// until that turn's end or the process's Exited, and marks that turn's end as caused by the owner (see
// noteTurnEndedLocked). starter is who started the turn running now, or the last one: the owner, through Start, Say or
// a held message, a caller event through Deliver, or claude itself, as a background
// task's end starts a turn (see turnStarter). A turn that a prompt follows keeps its starter, since the prompt that follows
// it set the starter when it was sent. closing is set by Close before it stops the session. The manager's lock guards
// turning, interrupting, ownerInterrupt, starter, closing, sending, requests, background, returned, idleSince and
// idleStopped, and the pump sets exited under it too. requests holds the ids of the requests the host has open,
// background is set while a background task runs, idleSince is when the session last became not busy (zero while it
// is busy), and idleStopped is set when a check stops the session for being idle. step is set for a step session, which sends no notice and frees a
// step slot rather than a session slot. observer receives the hand-offs of a step, which the pump makes, and returned is
// set from a hand-off until Continue sends the step's next prompt, so the step stays busy meanwhile. schema is set for a
// step that runs with a schema, whose init event must list StructuredOutput (see observe). draining is set for a session
// re-adopted from a holder one version older than the controller's, which retireLocked takes out of the live set
// once no turn, request or background task runs and it owes no answer, and retired is set once it has. stopped is closed
// once the stop of a retired session returns, which is once its holder has ended and its directory is removed. sending
// counts the sends of a prompt that markSending or claimStep began and send has not ended, so a draining session
// is not retired between the start of a send and its end.
type liveSession struct {
	id             string
	dir            string
	sess           *session.Session
	done           chan struct{}
	stopped        chan struct{}
	cancel         context.CancelFunc
	step           bool
	agent          string
	schema         bool
	observer       StepObserver
	exited         bool
	turning        bool
	interrupting   bool
	ownerInterrupt bool
	starter        turnStarter
	closing        bool
	draining       bool
	retired        bool
	sending        int
	requests       map[string]struct{}
	background     bool
	returned       bool
	idleSince      time.Time
	idleStopped    bool
	// order is held by the pump while it records a line, by Answer from its send until it has queued the working state, so
	// every op an answer causes is queued after that state, and by deliver and Withdraw while they decide or write held
	// messages, so the pump's turn ends and those decisions never interleave.
	order sync.Mutex
}

// turnStarter names who started a turn of a live session. The zero value is ownerTurn, so a turn the manager did not see
// start, as the running turn of a re-adopted session, counts as the owner's.
type turnStarter int

// The starters of a turn: the owner's prompt, a caller event's prompt, or claude itself, with no prompt.
const (
	ownerTurn turnStarter = iota
	eventTurn
	claudeTurn
)

// Manager starts, stops and tracks headless sessions. Each running session has a pump goroutine that records its
// events through the Writer. Close stops every session and waits for the pumps.
type Manager struct {
	shell       string
	claude      string
	environ     []string
	holderScope string
	// holderScopeMemory is the memory cap of each holder's scope, in bytes, or zero for holder.DefaultScopeMemory.
	holderScopeMemory int64
	stateDir          string
	managedDir        string
	// holderVersion is the holder wire version the controller speaks when it re-adopts a holder.
	holderVersion int
	// attachTimeout bounds a re-adoption's attach in place of holder.AttachTimeout when it is not zero. Tests set it.
	attachTimeout time.Duration
	// closeWait bounds Close's wait for the pumps in place of the closeWait constant when it is not zero. A test whose
	// store refuses every write by the close, as a crash leaves it, sets it: a pump blocked on a refused write ends only
	// when Close cancels it.
	closeWait time.Duration
	// promptWritten, when set, is called once a send has written its prompt, before the send ends. Tests set it to hold
	// a send open while the turn its prompt opened ends.
	promptWritten func(id string)
	// interruptWritten, when set, is called with the id of a session just before its interrupt is sent, after an owner's
	// interrupt has recorded its engine_interrupt. Tests set it to read the session while the interrupt is pending, or to
	// end the session so the send fails.
	interruptWritten func(id string)
	limit            int
	writer           *Writer
	now              func() time.Time
	notifier         NoticeSink
	store            Store
	logger           *slog.Logger
	metrics          *telemetry.Daemon

	pumps sync.WaitGroup
	// calls counts the calls under way that can start or end work (see callsInFlight).
	calls callsInFlight

	mu sync.Mutex
	// sessions holds the running sessions whose pump has not finished.
	sessions map[string]*liveSession
	// stopping holds the retired sessions, by id, until their pump has ended and their stop has returned (see
	// stopRetired). A Say or Continue that finds no live session waits for the one held here, so its old holder's
	// directory is gone when it resumes (see awaitStopping).
	stopping map[string]*liveSession
	// reserved counts the sessions being started or running, which is what the cap limits. stepLimit is the cap on
	// step sessions, and stepsRunning counts the step sessions being started or running, apart from reserved.
	reserved     int
	stepLimit    int
	stepsRunning int
	// stepObservers holds the observer each step was started with, kept after the step stops so a respawn hands its turns
	// to the same observer.
	stepObservers map[string]StepObserver
	// kept holds the drained steps whose step slot is kept for their id, by id (see retireLocked). Each has no live session,
	// and its slot still counts in stepsRunning until Stop, Release or a respawn takes it.
	kept map[string]*liveSession
	// quiet holds the ids of the sessions the owner's Interrupt left quiet, until the owner's next Say to each clears it.
	// While an id is in it, no caller event starts a turn in that session or resumes it (see claimFree and admitEvent).
	// An interrupt that a Say's preempt sends (see interruptFor) adds nothing. It is kept apart from the live session, so
	// a session the idle check stops stays quiet when it is resumed, and in memory only, so a restart of orreryd clears it.
	quiet  map[string]struct{}
	closed bool

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
		shell:             cfg.Shell,
		claude:            cfg.Claude,
		environ:           cfg.Environ,
		holderScope:       cfg.HolderScope,
		holderScopeMemory: cfg.HolderScopeMemory,
		stateDir:          cfg.StateDir,
		managedDir:        cfg.ManagedDir,
		holderVersion:     cfg.HolderVersion,
		limit:             cfg.Cap,
		stepLimit:         cfg.StepCap,
		writer:            cfg.Writer,
		now:               cfg.Now,
		notifier:          cfg.Notifier,
		store:             cfg.Store,
		logger:            cfg.Logger,
		metrics:           cfg.Metrics,
		sessions:          map[string]*liveSession{},
		stopping:          map[string]*liveSession{},
		stepObservers:     map[string]StepObserver{},
		kept:              map[string]*liveSession{},
		quiet:             map[string]struct{}{},
		attempts:          map[attemptKey]*attempt{},
	}
	if manager.holderVersion == 0 {
		manager.holderVersion = holder.WireVersion
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
	defer m.calls.enter()()
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
	writeID, err := newWriteID()
	if err != nil {
		return id, m.abandon(ctx, live, err)
	}
	live.sess.ExpectPrompt(writeID)
	if err = m.writer.Commit(ctx, id, store.EnginePrompt{ID: id, Text: req.Prompt, WriteID: writeID}); err != nil {
		live.sess.DropPrompt(writeID)
		return id, m.abandon(ctx, live, err)
	}
	m.setTurning(live, true)
	if err = live.sess.Prompt(writeID, req.Prompt); err != nil {
		m.setTurning(live, false)
		return id, m.abandon(ctx, live, err)
	}
	return id, nil
}

// Stop stops the live session id and records it stopped once its pump has stored its last ops. A drained step with no live
// session is recorded stopped the same way, and its kept slot is freed (see stopDrained). It returns ErrNoSession when id
// is neither. resumeMu is held, as Release holds it, so a respawn cannot take the kept slot between the lookup and the stop.
func (m *Manager) Stop(ctx context.Context, id string) error {
	defer m.calls.enter()()
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	m.mu.Lock()
	live := m.sessions[id]
	drained := m.kept[id]
	m.mu.Unlock()
	switch {
	case live != nil:
		stopErr := live.sess.Stop()
		recordErr := m.recordStopped(ctx, live)
		if stopErr == nil && recordErr == nil {
			return nil
		}
		return fmt.Errorf("stop session %s: %w", id, errors.Join(stopErr, recordErr))
	case drained != nil:
		return m.stopDrained(ctx, drained)
	}
	return fmt.Errorf("stop session %s: %w", id, ErrNoSession)
}

// stopDrained records the drained step stopped once its pump has ended, then frees its kept slot. The caller holds
// resumeMu. The slot is freed even when the record fails, since the step is stopped either way.
func (m *Manager) stopDrained(ctx context.Context, drained *liveSession) error {
	recordErr := m.recordStopped(ctx, drained)
	m.mu.Lock()
	m.dropKeptLocked(drained.id)
	m.mu.Unlock()
	if recordErr != nil {
		return fmt.Errorf("stop session %s: %w", drained.id, recordErr)
	}
	return nil
}

// dropKeptLocked frees the step slot kept for the drained step id, if one is kept (see retireLocked). The caller holds the
// lock.
func (m *Manager) dropKeptLocked(id string) {
	if m.kept[id] != nil {
		delete(m.kept, id)
		m.stepsRunning--
	}
}

// Seen marks the engine row id as seen now, and once that is stored reports the seen time to the notifier, which clears
// the session's notice. It returns an error wrapping ErrNoSession when no engine row has that id.
func (m *Manager) Seen(ctx context.Context, id string) error {
	defer m.calls.enter()()
	if _, err := m.engineRow(ctx, id); err != nil {
		return fmt.Errorf("mark session %s seen: %w", id, err)
	}
	now := m.now()
	seen := strconv.FormatInt(now.Unix(), 10)
	if err := m.writer.Commit(ctx, id, store.EngineSet{ID: id, Changes: store.EngineChanges{Seen: &seen}}); err != nil {
		return err
	}
	if m.notifier != nil {
		m.notifier.Seen(ctx, id, now)
	}
	return nil
}

// SayRequest is what Say needs: the session ID, the text of the prompt, and the client-chosen PromptID. An empty
// PromptID is not deduplicated.
type SayRequest struct {
	ID       string
	Text     string
	PromptID string
}

// Say sends req.Text to the session req.ID as its next prompt. A session that is not live is resumed first, from its
// engine row (see resumeSession). A message is written at once, as its prompt, when the session is idle and holds no
// message; otherwise it is held (see deliver). It records the prompt and the working state before it sends, so the stored
// conversation has the prompt ahead of its reply. A non-empty req.PromptID runs as one attempt per session and prompt
// id: while an attempt for it runs, a Say with that id waits for the attempt and returns its result, and sends nothing.
// Once begun, the attempt finishes even if ctx ends, and a Say whose ctx ends returns ctx's error. With no attempt
// running, a req.PromptID that an engine_prompt event of the session already carries, or that the session holds, makes Say
// return nil and do nothing else; otherwise the stored prompt carries req.PromptID. An attempt that fails after it stores the
// prompt keeps its error, and a later Say with that prompt id returns the error and does nothing else. A send that fails is
// recorded as the session's failed state; with a req.PromptID, the same write stores that prompt's failure marker, so a Say
// with that prompt id returns the stored reason and does nothing else, even after a restart. It returns ErrEmptyPrompt for
// empty text, an error wrapping ErrNoSession when no engine row has the id, and the errors of resumeSession when a live
// session cannot be started.
func (m *Manager) Say(ctx context.Context, req SayRequest) error {
	defer m.calls.enter()()
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
			recorded, err := m.promptRecorded(ctx, req.ID, req.PromptID)
			if err != nil {
				return "", err
			}
			if recorded {
				return "", nil
			}
			return "", m.say(ctx, req)
		},
		func(ctx context.Context) bool {
			recorded, err := m.promptRecorded(ctx, req.ID, req.PromptID)
			return recorded || err != nil
		},
	)
	return err
}

// promptRecorded reports whether session id has the prompt promptID, sent or held: an engine_prompt event of the session
// carries it, or the session holds its message.
func (m *Manager) promptRecorded(ctx context.Context, id, promptID string) (bool, error) {
	sent, err := m.store.EnginePromptSent(ctx, id, promptID)
	if err != nil {
		return false, fmt.Errorf("check prompt %s of session %s: %w", promptID, id, err)
	}
	if sent {
		return true, nil
	}
	held, err := m.store.HeldPrompts(ctx, id)
	if err != nil {
		return false, fmt.Errorf("check held prompts of session %s: %w", id, err)
	}
	return slices.ContainsFunc(held, func(message store.HeldPrompt) bool { return message.PromptID == promptID }), nil
}

// say delivers the message of req as Say does, without the check of its prompt id. A session that is not live is resumed
// first. A session that ends between the lookup and the delivery is resumed once more, then the delivery fails.
func (m *Manager) say(ctx context.Context, req SayRequest) error {
	live, err := m.live(req.ID)
	if errors.Is(err, ErrNoSession) {
		live, err = m.resumeSession(ctx, req.ID, nil)
	}
	if err != nil {
		return fmt.Errorf("say to session %s: %w", req.ID, err)
	}
	err = m.deliver(ctx, live, req)
	if errors.Is(err, errSessionEnded) {
		if live, err = m.resumeSession(ctx, req.ID, nil); err == nil {
			err = m.deliver(ctx, live, req)
		}
	}
	if err != nil {
		return fmt.Errorf("say to session %s: %w", req.ID, err)
	}
	return nil
}

// deliver writes or holds the message of req for live (see decide), and sends it when the decision is to write it.
func (m *Manager) deliver(ctx context.Context, live *liveSession, req SayRequest) error {
	sendNow, err := m.decide(ctx, live, req)
	if err != nil {
		return err
	}
	return sendNow()
}

// nothingToSend is the send of a message that decide holds or records for the pump, which needs no send of its own.
func nothingToSend() error {
	return nil
}

// decide makes the delivery decision for req on live under live's order lock, so it is made against the session's turn
// and held messages as the pump leaves them. A request the session has open is declined first (see declineLocked). The
// owner's message clears the quiet that his Interrupt set, so caller events are delivered again (see claimFree). A
// step's message, and a message after such a decline, is written at once, since a decline leaves the turn running with
// the message as its next prompt. A message is also written at once when the session is idle and holds no message. A
// turn that a caller event or claude itself started does not hold the owner's message: that turn is interrupted, and
// the message is written as the next turn at once when the session holds no message or a request was declined (see
// preemptLocked), or held behind the held messages, whose oldest the interrupted turn's end writes. Otherwise it is
// held behind the messages held before it, and when the session is idle the oldest of those is recorded as its next
// turn here, so the held messages go in order. decide returns the send to make once the lock is released, since a send
// waits on the holder and the pump needs the lock to record the turn end the send follows. When the message is held, or
// is recorded for the pump, the send it returns is nothingToSend. It returns errSessionEnded when live is no longer the
// session the manager runs.
func (m *Manager) decide(ctx context.Context, live *liveSession, req SayRequest) (func() error, error) {
	live.order.Lock()
	defer live.order.Unlock()
	if !m.isLive(live) {
		return nil, errSessionEnded
	}
	message := store.EnginePrompt{ID: live.id, Text: req.Text, PromptID: req.PromptID}
	declined, err := m.declineLocked(ctx, live)
	if err != nil {
		return nil, err
	}
	if live.step {
		m.markSending(live)
		return func() error { return m.send(ctx, live, message) }, nil
	}
	held, err := m.store.HeldPrompts(ctx, live.id)
	if err != nil {
		return nil, fmt.Errorf("read held prompts of session %s: %w", live.id, err)
	}
	m.mu.Lock()
	delete(m.quiet, live.id)
	turning := live.turning
	preempt := turning && live.starter != ownerTurn
	m.mu.Unlock()
	if preempt && (declined || len(held) == 0) {
		return m.preemptLocked(ctx, live, message)
	}
	if declined || (!turning && len(held) == 0) {
		m.markSending(live)
		return func() error { return m.send(ctx, live, message) }, nil
	}
	return m.holdLocked(ctx, live, message, held, turning, preempt)
}

// holdLocked holds message for live behind held, the messages live holds, as decide decides it, and returns the send to make
// once live's order lock is released. While a turn runs, preempt interrupts it, since a caller event or claude itself
// started it, so its end writes the oldest held message. When no turn runs, the oldest held message is recorded as the next
// turn here. The caller holds live's order lock.
func (m *Manager) holdLocked(
	ctx context.Context,
	live *liveSession,
	message store.EnginePrompt,
	held []store.HeldPrompt,
	turning, preempt bool,
) (func() error, error) {
	var err error
	if message.PromptID == "" {
		if message.PromptID, err = newWriteID(); err != nil {
			return nil, fmt.Errorf("prompt id for session %s: %w", live.id, err)
		}
	}
	hold := store.EngineHold{ID: live.id, PromptID: message.PromptID, Text: message.Text}
	if err = m.writer.Commit(ctx, live.id, hold); err != nil {
		return nil, fmt.Errorf("hold prompt of session %s: %w", live.id, err)
	}
	if turning {
		if preempt {
			m.interruptFor(ctx, live)
		}
		return nothingToSend, nil
	}
	// The session is idle with messages held before this one, so the oldest goes first. This message stays held behind it.
	send := m.startHeldLocked(ctx, live, held)
	return func() error {
		send()
		return nil
	}, nil
}

// preemptLocked writes message as the next turn of live, whose running turn a caller event or claude itself started, and
// interrupts that turn, so the owner's message is never held behind it. The message is recorded first, with the session
// working, and counts as sent from before that write, so the interrupted turn's end is followed and leaves the session
// working with no notice (see noteTurnEndedLocked). The turn is the owner's from then on, so a later message is held
// behind this one. The interrupt goes before the message, so claude ends its turn and runs the message as the next. The
// caller holds live's order lock, so the pump records no turn end between the record and the interrupt. It returns the
// send of the message, which runs once the lock is released. A failed record leaves the turn running as it was and is
// returned.
func (m *Manager) preemptLocked(
	ctx context.Context,
	live *liveSession,
	message store.EnginePrompt,
) (func() error, error) {
	m.mu.Lock()
	live.sending++
	m.mu.Unlock()
	writeID, err := m.record(ctx, live, message)
	if err != nil {
		m.endSend(live)
		return nil, err
	}
	m.mu.Lock()
	live.starter = ownerTurn
	m.mu.Unlock()
	m.interruptFor(ctx, live)
	return func() error {
		defer m.endSend(live)
		if sendErr := m.transmit(live, message.Text, writeID); sendErr != nil {
			return m.failSend(ctx, live, message, sendErr)
		}
		return nil
	}, nil
}

// interruptFor interrupts the running turn of live for the owner's message, as Interrupt does, unless an interrupt of it is
// already pending, since a second could reach claude after that turn ended and interrupt the owner's. That turn's end
// sends no notice. Unlike Interrupt, it leaves the session open to caller events, since the owner's message follows. A
// failed interrupt is logged, and the owner's message then follows the turn once it ends.
func (m *Manager) interruptFor(ctx context.Context, live *liveSession) {
	m.mu.Lock()
	pending := live.interrupting
	live.interrupting = live.turning
	m.mu.Unlock()
	if pending {
		return
	}
	if err := live.sess.Interrupt(); err != nil {
		m.setInterrupting(live, false)
		m.logger.WarnContext(ctx, "interrupt a turn for the owner's message", "session", live.id, "err", err)
	}
}

// startHeldLocked records the oldest of held as the next turn of live, which must be idle, and returns the send of that turn.
// The caller holds live's order lock, so the message is recorded as the pump leaves the held set, and a withdrawal cannot
// take it between the decision and its write. The send runs once the lock is released. A failure to record it leaves every
// message held and is logged, and the idle check writes the oldest one later (see writeHeldIdle). A failed send is logged
// with the failure the session records.
func (m *Manager) startHeldLocked(ctx context.Context, live *liveSession, held []store.HeldPrompt) func() {
	head := held[0]
	headPrompt := store.EnginePrompt{ID: live.id, Text: head.Text, PromptID: head.PromptID}
	m.markSending(live)
	writeID, err := m.record(ctx, live, headPrompt, store.EngineHeldRelease{ID: live.id, PromptID: head.PromptID})
	if err != nil {
		m.setTurning(live, false)
		m.endSend(live)
		m.logger.WarnContext(ctx, "write held prompt", "session", live.id, "err", err)
		return func() {}
	}
	return func() {
		defer m.endSend(live)
		if sendErr := m.transmit(live, head.Text, writeID); sendErr != nil {
			m.logger.WarnContext(
				ctx,
				"write held prompt",
				"session",
				live.id,
				"err",
				m.failSend(ctx, live, headPrompt, sendErr),
			)
		}
	}
}

// isLive reports whether live is the session the manager runs under its id, whose pump has not ended.
func (m *Manager) isLive(live *liveSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[live.id] == live
}

// declineOpen denies every request live has open with ownerSpokeMessage, so a session waiting on a question or a
// permission reads the owner's message or interrupt instead of holding it behind the request. It is declineLocked under
// live's order lock.
func (m *Manager) declineOpen(ctx context.Context, live *liveSession) error {
	live.order.Lock()
	defer live.order.Unlock()
	_, err := m.declineLocked(ctx, live)
	return err
}

// declineLocked denies every request live has open, as declineOpen does, and reports whether it denied any. A request the
// session already answered or withdrew is skipped. When it denies any, it records the session working with no question. The
// caller holds live's order lock.
func (m *Manager) declineLocked(ctx context.Context, live *liveSession) (bool, error) {
	m.mu.Lock()
	open := slices.Collect(maps.Keys(live.requests))
	m.mu.Unlock()
	declined := false
	for _, requestID := range open {
		err := live.sess.Deny(requestID, ownerSpokeMessage)
		if errors.Is(err, session.ErrNoPendingRequest) {
			m.closeRequest(live, requestID)
			continue
		}
		if err != nil {
			return declined, fmt.Errorf("decline request %s: %w", requestID, err)
		}
		m.closeRequest(live, requestID)
		declined = true
	}
	if !declined {
		return false, nil
	}
	working := string(protocol.StateWorking)
	question := ""
	if err := m.writer.Put(ctx, live.id, store.EngineSet{ID: live.id,
		Changes: store.EngineChanges{State: &working, Question: &question}}); err != nil {
		return true, fmt.Errorf("record session %s working: %w", live.id, err)
	}
	if !live.step {
		m.changed(ctx, live.id)
	}
	return true, nil
}

// Resume sends resumePrompt to the session id as its next prompt, through Say, so that its interrupted turn continues
// on its Claude session. It returns an error wrapping ErrNoSession when no engine row has id, and an error wrapping
// ErrNotResumable, saying which condition failed, when the row is not interrupted or has no Claude session. A refused
// Resume sends nothing.
func (m *Manager) Resume(ctx context.Context, id string) error {
	defer m.calls.enter()()
	row, err := m.engineRow(ctx, id)
	if err != nil {
		return fmt.Errorf("resume session %s: %w", id, err)
	}
	if row.State != stateInterrupted {
		return fmt.Errorf(
			"resume session %s: %w: its state is %s, not %s", id, ErrNotResumable, row.State, stateInterrupted,
		)
	}
	if row.ClaudeSession == "" {
		return fmt.Errorf("resume session %s: %w: it has no Claude session", id, ErrNotResumable)
	}
	return m.Say(ctx, SayRequest{ID: id, Text: resumePrompt})
}

// DeliverRequest is what Deliver needs: the session ID, the text of the event's prompt, and the PromptID that the
// prompt is deduplicated by. An empty PromptID is not deduplicated.
type DeliverRequest struct {
	ID       string
	Text     string
	PromptID string
}

// Deliver sends req.Text to the headless session req.ID as its next prompt, stored with the sender event and
// req.PromptID. Unlike Say, it never answers, denies or declines a request the session has open. A live session that
// is busy, with a turn running, a request open, a background task or a hand-off, returns ErrSessionBusy and stores and
// sends nothing, so the caller delivers the text once the session is not busy. A live session that the owner
// interrupted and has said nothing to since returns ErrCallerQuiet and stores and sends nothing, so the caller delivers
// the text once the owner has spoken. A session that is not live is resumed from its row, as Say resumes one, unless its
// row is stopped, which returns ErrCallerStopped, or the owner interrupted it and has said nothing to it since, as when
// the idle check stopped it after his Interrupt, which returns ErrCallerQuiet and resumes nothing. A step session returns
// an error naming it. A non-empty req.PromptID that an engine_prompt of the session already carries makes Deliver
// return nil and do nothing else, also after a restart. It returns ErrEmptyPrompt for empty text. A non-empty
// req.PromptID runs as one attempt per session and prompt id, as Say's does (see keyed).
func (m *Manager) Deliver(ctx context.Context, req DeliverRequest) error {
	defer m.calls.enter()()
	if req.Text == "" {
		return ErrEmptyPrompt
	}
	if req.PromptID == "" {
		return m.deliverEvent(ctx, req)
	}
	_, err := m.keyed(
		ctx,
		attemptKey{session: req.ID, prompt: req.PromptID},
		func(ctx context.Context) (string, error) {
			sent, err := m.store.EnginePromptSent(ctx, req.ID, req.PromptID)
			if err != nil {
				return "", fmt.Errorf("check prompt %s of session %s: %w", req.PromptID, req.ID, err)
			}
			if sent {
				return "", nil
			}
			return "", m.deliverEvent(ctx, req)
		},
		func(ctx context.Context) bool {
			sent, err := m.store.EnginePromptSent(ctx, req.ID, req.PromptID)
			return sent || err != nil
		},
	)
	return err
}

// deliverEvent sends req as Deliver does, without the check of its prompt id. The session is claimed only when it is
// not busy and not quiet, and a session that is not live is resumed from its row first, so a busy, stopped or quiet
// session is left as it is.
func (m *Manager) deliverEvent(ctx context.Context, req DeliverRequest) error {
	live, err := m.claimFree(req.ID)
	if errors.Is(err, ErrNoSession) {
		if _, err = m.resumeSession(ctx, req.ID, m.admitEvent); err == nil {
			live, err = m.claimFree(req.ID)
		}
	}
	if err != nil {
		return fmt.Errorf("deliver to session %s: %w", req.ID, err)
	}
	return m.send(ctx, live, store.EnginePrompt{
		ID: req.ID, Text: req.Text, PromptID: req.PromptID, Sender: store.PromptSenderEvent,
	})
}

// admitEvent returns ErrCallerStopped for a row the owner's Stop recorded stopped, and ErrCallerQuiet for a session the
// owner's Interrupt left quiet (see Manager.quiet), so a delivery resumes nothing for either. Any other row is admitted.
func (m *Manager) admitEvent(row store.EngineRow) error {
	if row.State == stateStopped {
		return ErrCallerStopped
	}
	m.mu.Lock()
	_, quiet := m.quiet[row.ID]
	m.mu.Unlock()
	if quiet {
		return ErrCallerQuiet
	}
	return nil
}

// claimFree returns the live session id with its turn marked running, started by a caller event, and a send begun, as
// markSending does, but only while the session is not busy and not quiet. The check and the claim are one step under the
// lock, so a delivery that claims the session leaves every other delivery to find it busy. It returns ErrNoSession when no
// session with that id is live, or when it is closing or its exit is recorded, ErrSessionBusy when it is busy,
// ErrCallerQuiet when the owner's Interrupt left it quiet, and an error naming it when it is a step.
func (m *Manager) claimFree(id string) (*liveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok || live.closing || live.exited {
		return nil, ErrNoSession
	}
	if live.step {
		return nil, fmt.Errorf("session %s is a step session, which takes no event", id)
	}
	if live.busy() {
		return nil, ErrSessionBusy
	}
	if _, quiet := m.quiet[id]; quiet {
		return nil, ErrCallerQuiet
	}
	live.turning = true
	live.starter = eventTurn
	live.sending++
	m.updateIdleLocked(live)
	return live, nil
}

// send records prompt as the next prompt of live, with the working state and the ops with it, in one write, and then sends
// its text (see record, transmit and failSend). live must have its turn marked running and a send begun, as markSending,
// claimStep or claimFree leaves it, and the send ends when send returns (see endSend). A failed record leaves the turn not
// running.
func (m *Manager) send(
	ctx context.Context,
	live *liveSession,
	prompt store.EnginePrompt,
	with ...store.EngineOp,
) error {
	defer m.endSend(live)
	writeID, err := m.record(ctx, live, prompt, with...)
	if err != nil {
		m.setTurning(live, false)
		return err
	}
	if err = m.transmit(live, prompt.Text, writeID); err != nil {
		return m.failSend(ctx, live, prompt, err)
	}
	return nil
}

// record stores prompt as the next prompt of live, with the working state and the ops with it, in one write, and returns the
// write id the prompt is sent under. The prompt counts as sent from before the write (see session.Session.ExpectPrompt). A
// write that fails stores nothing, and the caller settles the turn. A step sends no notice, and an engine session's owner is
// told the session changed.
func (m *Manager) record(
	ctx context.Context,
	live *liveSession,
	prompt store.EnginePrompt,
	with ...store.EngineOp,
) (string, error) {
	id := live.id
	working := string(protocol.StateWorking)
	writeID, err := newWriteID()
	if err != nil {
		return "", fmt.Errorf("record prompt of session %s: %w", id, err)
	}
	prompt.WriteID = writeID
	live.sess.ExpectPrompt(writeID)
	ops := append(
		[]store.EngineOp{prompt, store.EngineSet{ID: id, Changes: store.EngineChanges{State: &working}}},
		with...)
	if err = m.writer.Commit(ctx, id, ops...); err != nil {
		live.sess.DropPrompt(writeID)
		return "", fmt.Errorf("record prompt of session %s: %w", id, err)
	}
	if !live.step {
		m.changed(ctx, id)
	}
	return writeID, nil
}

// transmit sends text as one user message under writeID, which record stored for it. promptWritten, when set, runs once the
// send returns.
func (m *Manager) transmit(live *liveSession, text, writeID string) error {
	if m.promptWritten != nil {
		defer m.promptWritten(live.id)
	}
	if err := live.sess.Prompt(writeID, text); err != nil {
		return fmt.Errorf("write prompt %s: %w", writeID, err)
	}
	return nil
}

// failSend records the session failed with err, the error of a transmit of prompt, and returns the error to report. With a
// prompt.PromptID the same write stores that prompt's failure marker, so a Say with that prompt id returns the reason. A step
// sends no notice, so its failed send is recorded without one.
func (m *Manager) failSend(ctx context.Context, live *liveSession, prompt store.EnginePrompt, err error) error {
	m.setTurning(live, false)
	var marks []store.EngineOp
	if prompt.PromptID != "" {
		marks = append(marks, store.EnginePromptFailed{ID: live.id, PromptID: prompt.PromptID, Reason: err.Error()})
	}
	var recordErr error
	if live.step {
		recordErr = m.writeFailed(ctx, live.id, err, marks...)
	} else {
		recordErr = m.recordFailed(ctx, live.id, live.dir, err, marks...)
	}
	return fmt.Errorf("send prompt to session %s: %w", live.id, errors.Join(err, recordErr))
}

// Continue sends prompt to the step id as its next prompt, which ends the step's hand-off: the step is busy again until
// this turn ends. A non-empty prompt.PromptID that an engine_prompt of the step already carries makes Continue return nil
// and do nothing else, as Say does. A step that is not live, as after an idle stop or a drain at its hand-off, is
// respawned first from its rows (see respawnStep). The prompt is recorded with its PromptID, sender and round, as Say
// records one without the sender and round, and a failed send records the step failed without a notice. It returns an
// error wrapping ErrNoSession when id is not a step row, and ErrEmptyPrompt for empty text.
func (m *Manager) Continue(ctx context.Context, id string, prompt StepPrompt) error {
	defer m.calls.enter()()
	if prompt.Text == "" {
		return ErrEmptyPrompt
	}
	if prompt.PromptID != "" {
		sent, err := m.store.EnginePromptSent(ctx, id, prompt.PromptID)
		if err != nil {
			return fmt.Errorf("check prompt %s of step %s: %w", prompt.PromptID, id, err)
		}
		if sent {
			return nil
		}
	}
	live, err := m.claimStep(id)
	if errors.Is(err, ErrNoSession) {
		live, err = m.respawnStep(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("continue step %s: %w", id, err)
	}
	var loop []store.EngineOp
	if prompt.Loop != nil {
		loop = append(loop, *prompt.Loop)
	}
	return m.send(ctx, live, store.EnginePrompt{
		ID: id, Text: prompt.Text, PromptID: prompt.PromptID, Sender: prompt.Sender, Round: prompt.Round,
	}, loop...)
}

// respawnStep starts the step id that is not live again, from its engine row and its steps row: it resumes the row's
// claude session with the step's agent, inline agents and schema, registered as a step with the observer it was started
// with. It first waits for a drained holder of id to end (see awaitStopping), so the new process never starts over the
// old holder's record or directory. Then it takes the step slot kept for id, or one under StepCap, before reading the rows, so a full cap
// refuses the respawn before anything is recorded (see reserveStep). A directory that is gone, an agent that no longer
// resolves, or a spawn that fails, records the step failed without a notice and returns the error. It returns the step
// claimed with its turn running, as claimStep does. resumeMu makes concurrent calls for one id start a single process. It returns an error
// wrapping ErrNoSession when id is not a step row.
func (m *Manager) respawnStep(ctx context.Context, id string) (*liveSession, error) {
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	if live, err := m.claimStep(id); err == nil {
		return live, nil
	}
	if err := m.awaitStopping(ctx, id); err != nil {
		return nil, err
	}
	if err := m.reserveStep(id); err != nil {
		return nil, err
	}
	row, step, err := m.stepLaunch(ctx, id)
	if err != nil {
		m.releaseStep()
		return nil, err
	}
	if err = checkDir(row.Dir); err != nil {
		m.releaseStep()
		return nil, errors.Join(err, m.writeFailed(ctx, id, err))
	}
	if err = m.resolveStepAgent(StepSession{ID: id, Dir: row.Dir, Agent: step.Agent, Agents: step.Agents}); err != nil {
		m.releaseStep()
		return nil, errors.Join(err, m.writeFailed(ctx, id, err))
	}
	m.mu.Lock()
	observer := m.stepObservers[id]
	m.mu.Unlock()
	live, err := m.spawnLive(ctx, id, row.Dir, spawnSpec{claudeSession: row.ClaudeSession, agent: step.Agent,
		agents: step.Agents, schema: step.Schema, step: true, observer: observer})
	if err != nil {
		m.releaseStep()
		return nil, errors.Join(err, m.writeFailed(ctx, id, err))
	}
	m.markSending(live)
	return live, nil
}

// stepLaunch returns the engine row and the steps row of the step id that a respawn starts from. It returns an error
// wrapping ErrNoSession when id is not a step row.
func (m *Manager) stepLaunch(ctx context.Context, id string) (store.EngineRow, store.StepRow, error) {
	row, err := m.engineRow(ctx, id)
	if err != nil {
		return store.EngineRow{}, store.StepRow{}, err
	}
	if row.Kind != store.KindStep {
		return store.EngineRow{}, store.StepRow{}, fmt.Errorf("%w: %s is not a step session", ErrNoSession, id)
	}
	step, err := m.store.Step(ctx, id)
	if err != nil {
		return store.EngineRow{}, store.StepRow{}, fmt.Errorf("read step %s: %w", id, err)
	}
	return row, step, nil
}

// Answer answers the open request requestID of the live session id. A question takes one of its option labels. A
// permission request takes allow, which approves it with its own input, or deny, which refuses it with a fixed
// message. Any other answer returns an error matching ErrBadAnswer and leaves the request open. On success Answer
// records the session working with no question. It returns an error wrapping ErrNoSession when id is not live.
func (m *Manager) Answer(ctx context.Context, id, requestID, answer string) error {
	defer m.calls.enter()()
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
	if !live.step {
		m.changed(ctx, id)
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

// Interrupt asks the live session id to end the turn in progress, first denying any request it has open (see
// declineOpen), since a session waiting on a request does not read an interrupt. The turn's end is recorded when the session reports
// it, and that end sends no notice. The session is left quiet, with or without a turn running, so no caller event starts
// a turn in it or resumes it once it stops until the owner's next Say (see Manager.quiet, claimFree, admitEvent and
// decide). The quiet is set before anything is sent and stays set when the interrupt fails, since the owner asked for
// the session to stop. It returns an error wrapping ErrNoSession when id is not live. The step loop calls it and
// records nothing; the owner's interrupt is OwnerInterrupt.
func (m *Manager) Interrupt(ctx context.Context, id string) error {
	defer m.calls.enter()()
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	m.mu.Lock()
	m.quiet[id] = struct{}{}
	m.mu.Unlock()
	return m.interruptLive(ctx, live)
}

// OwnerInterrupt is the owner's Interrupt of the live session id (see Interrupt), which also records it. The quiet is
// set, and the interrupt is recorded as an engine_interrupt that says whether a turn runs (see recordOwnerInterrupt). A
// turn that runs is then marked as the owner's, so its end is recorded as ownerInterrupted. When the interrupt fails, it
// is recorded as not turning, and the quiet stays. It returns an error wrapping ErrNoSession when id is not live.
func (m *Manager) OwnerInterrupt(ctx context.Context, id string) error {
	defer m.calls.enter()()
	live, err := m.live(id)
	if err != nil {
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	m.mu.Lock()
	m.quiet[id] = struct{}{}
	m.mu.Unlock()
	if err = m.recordOwnerInterrupt(ctx, live); err != nil {
		return fmt.Errorf("interrupt session %s: %w", id, err)
	}
	if err = m.interruptLive(ctx, live); err != nil {
		m.recordFailedInterrupt(ctx, live)
		return err
	}
	return nil
}

// recordOwnerInterrupt stores the owner's interrupt of live as engine_interrupt, whose turning is whether a turn runs.
// It writes under the session's order lock, so the record and a turn's end never interleave. A turn that runs is marked
// as the owner's interrupt, which its end then reports (see noteTurnEndedLocked). A failed record is returned, and
// nothing is sent. The caller holds no lock.
func (m *Manager) recordOwnerInterrupt(ctx context.Context, live *liveSession) error {
	live.order.Lock()
	defer live.order.Unlock()
	m.mu.Lock()
	turning := live.turning
	m.mu.Unlock()
	if err := m.writer.Commit(ctx, live.id, store.EngineInterrupt{ID: live.id, Turning: turning}); err != nil {
		return fmt.Errorf("record interrupt: %w", err)
	}
	if turning {
		m.mu.Lock()
		live.ownerInterrupt = true
		m.mu.Unlock()
	}
	return nil
}

// recordFailedInterrupt clears the owner's interrupt of live, whose interrupt failed, and stores it as engine_interrupt
// that is not turning. It writes under the session's order lock, as recordOwnerInterrupt does. A failed write is logged,
// since the owner's error is the one the caller returns. The caller holds no lock.
func (m *Manager) recordFailedInterrupt(ctx context.Context, live *liveSession) {
	live.order.Lock()
	defer live.order.Unlock()
	m.mu.Lock()
	live.ownerInterrupt = false
	m.mu.Unlock()
	if err := m.writer.Commit(ctx, live.id, store.EngineInterrupt{ID: live.id, Turning: false}); err != nil {
		m.logger.WarnContext(ctx, "record failed interrupt", "session", live.id, "err", err)
	}
}

// interruptLive denies each request live has open, marks its running turn interrupted and sends the interrupt.
// Interrupt and OwnerInterrupt both send through it. The caller holds no lock.
func (m *Manager) interruptLive(ctx context.Context, live *liveSession) error {
	if err := m.declineOpen(context.WithoutCancel(ctx), live); err != nil {
		return fmt.Errorf("interrupt session %s: %w", live.id, err)
	}
	m.setInterrupting(live, true)
	if m.interruptWritten != nil {
		m.interruptWritten(live.id)
	}
	if err := live.sess.Interrupt(); err != nil {
		m.setInterrupting(live, false)
		return fmt.Errorf("interrupt session %s: %w", live.id, err)
	}
	return nil
}

// Withdraw removes the held messages of session id that carry prompt id promptID, or every held message of the session when
// promptID is empty, in one store write, and returns their text in order. A promptID the session holds no message for returns
// an error wrapping ErrNotHeld and removes nothing, so a message already written is never withdrawn. It returns an error
// wrapping ErrNoSession when no engine row has id. It takes the session's delivery locks as Say does, so a message the pump is
// about to write is either withdrawn or written, never both.
func (m *Manager) Withdraw(ctx context.Context, id, promptID string) ([]string, error) {
	defer m.calls.enter()()
	if _, err := m.engineRow(ctx, id); err != nil {
		return nil, fmt.Errorf("withdraw from session %s: %w", id, err)
	}
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	if live, err := m.live(id); err == nil {
		live.order.Lock()
		defer live.order.Unlock()
	}
	held, err := m.store.HeldPrompts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("withdraw from session %s: %w", id, err)
	}
	var texts []string
	var ops []store.EngineOp
	for _, message := range held {
		if promptID != "" && message.PromptID != promptID {
			continue
		}
		texts = append(texts, message.Text)
		ops = append(ops, store.EngineHeldRelease{ID: id, PromptID: message.PromptID})
	}
	if promptID != "" && len(ops) == 0 {
		return nil, fmt.Errorf("withdraw prompt %s from session %s: %w", promptID, id, ErrNotHeld)
	}
	if len(ops) == 0 {
		return texts, nil
	}
	if err = m.writer.Commit(ctx, id, ops...); err != nil {
		return nil, fmt.Errorf("withdraw from session %s: %w", id, err)
	}
	return texts, nil
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
// the row's directory and resumes the row's claude session, when it has one. It waits for a retired session with id to
// end (see awaitStopping), then reserves a slot under the cap before it reads the directory, so a full cap refuses the
// resume before anything is recorded. A directory that is missing or not a directory records the session failed and
// returns an error wrapping ErrBadDir. resumeMu makes concurrent calls for one id start a single process: each call after
// the first finds the session live. A non-nil admit sees the row under resumeMu before the process starts, and an error
// it returns refuses the resume.
func (m *Manager) resumeSession(
	ctx context.Context, id string, admit func(store.EngineRow) error,
) (*liveSession, error) {
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	if live, err := m.live(id); err == nil {
		return live, nil
	}
	if err := m.awaitStopping(ctx, id); err != nil {
		return nil, err
	}
	row, err := m.engineRow(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.Kind == store.KindStep {
		return nil, fmt.Errorf("%w: %s is a step session", ErrNoSession, id)
	}
	if admit != nil {
		err = admit(row)
		if err != nil {
			return nil, err
		}
	}
	return m.resume(ctx, row)
}

// awaitStopping waits until the retired session with id, if one is stopping, has ended, so it records nothing more and
// its holder's directory is gone for a new holder to take: it waits until stopRetired has seen both the session's pump
// end and its stop return, which is once the holder has ended and its directory is removed, since the stream can end
// before that. A drained step's slot is kept for its id, not freed, when it ends (see retireLocked). It returns an error
// wrapping ctx's error when ctx ends first.
func (m *Manager) awaitStopping(ctx context.Context, id string) error {
	m.mu.Lock()
	old := m.stopping[id]
	m.mu.Unlock()
	if old == nil {
		return nil
	}
	select {
	case <-old.stopped:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for session %s to stop: %w", id, ctx.Err())
	}
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
// events. The caller holds the slot from reserve or reserveStep. An error that returns here leaves nothing registered.
func (m *Manager) spawnLive(ctx context.Context, id, dir string, sp spawnSpec) (*liveSession, error) {
	sess, err := m.spawn(ctx, id, dir, sp)
	if err != nil {
		return nil, err
	}
	rec := newRecorder(id)
	rec.claudeSession = sp.claudeSession
	return m.register(ctx, sess, id, dir, sp, rec, nil)
}

// readoption is what register primes a re-adopted session with before its pump starts: the row the session's state is
// taken from, the lines its replay read, the write ids its holder has applied to its stdin, and whether its holder is one
// version older than the controller's, so the session is draining (see retireLocked).
type readoption struct {
	row      store.EngineRow
	lines    []session.Line
	written  []string
	draining bool
}

// register registers sess, a started session of id in dir, as live under a pump that records its events with rec. The
// live session takes the step and observer of sp. A re-adopted session, re non-nil, is primed from its replay before its
// pump starts, and its pump starts from the reply prime returns. A draining session with no turn running is retired at
// once. A process that starts after the manager closed is stopped, and the error reports the close. An error that returns
// here leaves nothing registered; a re-adopted session whose prime fails is left running, for the caller to end.
func (m *Manager) register(
	ctx context.Context, sess *session.Session, id, dir string, sp spawnSpec, rec *recorder, re *readoption,
) (*liveSession, error) {
	pumpCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	live := &liveSession{id: id, dir: dir, sess: sess, done: make(chan struct{}), cancel: cancel,
		step: sp.step, agent: sp.agent, schema: sp.schema != "", observer: sp.observer, requests: map[string]struct{}{},
		draining: re != nil && re.draining}
	reply := ""
	if re != nil {
		var err error
		if reply, err = m.prime(ctx, live, rec, re); err != nil {
			cancel()
			return nil, err
		}
	}
	m.mu.Lock()
	closed := m.closed
	if !closed {
		m.sessions[id] = live
		m.pumps.Add(1)
		m.updateIdleLocked(live)
		m.retireLocked(live)
	}
	m.mu.Unlock()
	if closed {
		cancel()
		return nil, errors.Join(errManagerClosed, sess.Stop())
	}
	go m.pump(pumpCtx, live, rec, reply)
	return live, nil
}

// prime builds the state of live, a re-adopted session not yet registered, from re. Its turn runs when its row is working
// or waiting, and its background flag is the row's. Each replayed line goes through noteEvent and the recorder with the ops
// discarded, so the requests it leaves open, its unfinished text and its reply are those the replay gives. A waiting row
// with no request open goes working with no question. Each prompt the holder has not written is sent in order, marking the
// turn running. A session left idle that holds messages then writes its oldest one (see writeHeldIdle). It returns the reply
// the pump starts from.
func (m *Manager) prime(ctx context.Context, live *liveSession, rec *recorder, re *readoption) (string, error) {
	row := re.row
	m.mu.Lock()
	live.turning = row.State == string(protocol.StateWorking) || row.State == string(protocol.StateWaiting)
	live.background = row.Background
	m.updateIdleLocked(live)
	m.mu.Unlock()
	reply := m.replay(live, rec, re.lines)
	if err := m.markWorking(ctx, live, row); err != nil {
		return "", err
	}
	if err := m.sendUnwritten(ctx, live, re.written); err != nil {
		return "", err
	}
	// live is not registered yet, so no pump or Say can reach it: the held write runs under its order lock without the
	// registration check writeHeldIdle makes.
	live.order.Lock()
	send := m.idleHeldLocked(ctx, live)
	live.order.Unlock()
	if send != nil {
		send()
	}
	return reply, nil
}

// idleHeldLocked returns the send of the oldest message held for live when live is idle, and nil when none is written. Idle
// means no turn, request or send pending, and that live is neither a step, draining nor closing: a draining session writes
// nothing new, since it stops once its turn ends, and its held messages wait for a later Say. The caller holds live's order
// lock.
func (m *Manager) idleHeldLocked(ctx context.Context, live *liveSession) func() {
	m.mu.Lock()
	idle := !live.turning && !live.draining && !live.closing && !live.step
	m.mu.Unlock()
	if !idle {
		return nil
	}
	held, err := m.store.HeldPrompts(ctx, live.id)
	if err != nil {
		m.logger.WarnContext(ctx, "read held prompts", "session", live.id, "err", err)
		return nil
	}
	if len(held) == 0 {
		return nil
	}
	return m.startHeldLocked(ctx, live, held)
}

// writeHeldIdle writes the oldest message held for the registered session live as its next turn, when live is idle (see
// idleHeldLocked). It takes live's order lock, as a Say does, and writes nothing once live is no longer the session the
// manager runs. checkIdle makes it for each live session, so a message whose write failed is written at the next check.
func (m *Manager) writeHeldIdle(ctx context.Context, live *liveSession) {
	live.order.Lock()
	var send func()
	if m.isLive(live) {
		send = m.idleHeldLocked(ctx, live)
	}
	live.order.Unlock()
	if send != nil {
		send()
	}
}

// replay runs the replayed lines through noteEvent and rec, with rec's ops discarded, and returns the reply the lines
// leave: the text of the last finished main-conversation block after the last turn's end.
func (m *Manager) replay(live *liveSession, rec *recorder, lines []session.Line) string {
	reply := ""
	for _, line := range lines {
		for _, event := range line.Events {
			if !m.noteEvent(live, event, false, false).record {
				continue
			}
			rec.ops(event)
			if text, ok := mainText(event); ok {
				reply = text
			}
			if _, ended := event.(session.TurnEnded); ended {
				reply = ""
			}
		}
	}
	return reply
}

// markWorking records a waiting row working with no question when the replay left no request open, since the holder's
// claude has none to answer, and tells the notifier the session changed, as Answer does. Any other row is left as it is.
func (m *Manager) markWorking(ctx context.Context, live *liveSession, row store.EngineRow) error {
	m.mu.Lock()
	open := len(live.requests)
	m.mu.Unlock()
	if row.State != string(protocol.StateWaiting) || open > 0 {
		return nil
	}
	working := string(protocol.StateWorking)
	question := ""
	if err := m.writer.Put(ctx, live.id, store.EngineSet{ID: live.id,
		Changes: store.EngineChanges{State: &working, Question: &question}}); err != nil {
		return fmt.Errorf("record session %s working: %w", live.id, err)
	}
	if !live.step {
		m.changed(ctx, live.id)
	}
	return nil
}

// sendUnwritten sends each prompt the store has for live's session, in order, whose write id is not in written, the write
// ids its holder has applied. Each send marks the turn running.
func (m *Manager) sendUnwritten(ctx context.Context, live *liveSession, written []string) error {
	prompts, err := m.store.HolderPrompts(ctx, live.id)
	if err != nil {
		return fmt.Errorf("read prompts of session %s: %w", live.id, err)
	}
	for _, prompt := range unwrittenPrompts(prompts, written) {
		m.setTurning(live, true)
		if err = live.sess.Prompt(prompt.WriteID, prompt.Text); err != nil {
			return fmt.Errorf("send prompt of session %s: %w", live.id, err)
		}
	}
	return nil
}

// unwrittenPrompts returns the prompts whose write ids are not in written, the write ids the holder has applied, in their
// order.
func unwrittenPrompts(prompts []store.HolderPrompt, written []string) []store.HolderPrompt {
	var unwritten []store.HolderPrompt
	for _, prompt := range prompts {
		if !slices.Contains(written, prompt.WriteID) {
			unwritten = append(unwritten, prompt)
		}
	}
	return unwritten
}

// Close stops every live session and waits up to 5 s for each to record its state. With a holder scope, Close detaches
// each session instead, so its holder and child keep running, and it records nothing for the session: the row keeps the
// state and unfinished text its events gave it, for a later orreryd to re-adopt where its stored offsets say. Without a
// holder scope, a session whose turn was running is recorded interrupted, and every session loses its unfinished text.
// It then cancels any write a pump still has blocked and waits for the pumps to return, and for the stops of the
// sessions retired before the close, whose holder directories are gone once those stops return. Start refuses from the
// moment Close begins, and later calls to Close return at once.
func (m *Manager) Close() {
	defer m.calls.enter()()
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
		var err error
		if m.holderScope != "" {
			err = live.sess.Detach()
		} else {
			err = live.sess.Stop()
		}
		if err != nil {
			m.logger.Warn("session stop failed on close", "session", live.id, "err", err)
		}
	}
	wait := closeWait
	if m.closeWait != 0 {
		wait = m.closeWait
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
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
	// A session retired before the close is still being stopped on its own goroutine (stopRetired), and its holder's
	// directory is removed only once that stop returns. Close waits for those stops too, so no stop of this manager
	// still touches a holder directory once Close returns.
	m.mu.Lock()
	retired := make([]*liveSession, 0, len(m.stopping))
	for _, live := range m.stopping {
		retired = append(retired, live)
	}
	m.mu.Unlock()
	for _, live := range retired {
		<-live.stopped
	}
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

// checkIdle first writes the oldest held message of each live session that is idle (see writeHeldIdle), which is how a held
// message whose write failed is written, then stops each live session that has been not busy for the idle timeout by the
// clock, and waits for its pump, which frees its slot. A session is busy while a turn runs, a request is open, or a
// background task runs. The stop records nothing, so the session's row and events stay as they were. The session is taken
// out of the live set under the lock before it is stopped, and resumeMu is held through the stop, so a Say that finds no
// live session waits, then resumes it from its row.
func (m *Manager) checkIdle(ctx context.Context) {
	defer m.calls.enter()()
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	m.mu.Lock()
	running := slices.Collect(maps.Values(m.sessions))
	m.mu.Unlock()
	for _, live := range running {
		m.writeHeldIdle(ctx, live)
	}
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

// claimStep returns the live step id with its turn marked running, its hand-off ended and a send begun, under the lock, so
// a check cannot stop the step between the lookup and the prompt. The send ends in send. It returns ErrNoSession when no
// live step has that id.
func (m *Manager) claimStep(id string) (*liveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok || !live.step {
		return nil, ErrNoSession
	}
	live.turning = true
	live.returned = false
	live.sending++
	m.updateIdleLocked(live)
	return live, nil
}

// markSending marks the turn of live running, started by the owner, and a send begun, for a session that resumeSession or
// respawnStep has just started. The send ends in send.
func (m *Manager) markSending(live *liveSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live.turning = true
	live.starter = ownerTurn
	live.sending++
	m.updateIdleLocked(live)
}

// endSend ends the send that markSending or claimStep began for live, and retires live when the send leaves it
// drained (see retireLocked). A prompt the send sent stays unanswered until its turn ends, so that turn retires the
// session; a send that failed sent nothing to answer, so this retires it.
func (m *Manager) endSend(live *liveSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live.sending--
	m.retireLocked(live)
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

// newWriteID returns the write id of a prompt about to be sent: a random version-4 UUID, which the prompt is also sent
// under as its user-message uuid.
func newWriteID() (string, error) {
	id, err := transport.RandomUUID()
	if err != nil {
		return "", fmt.Errorf("write id: %w", err)
	}
	return id, nil
}

// spawnSpec describes the process a spawn starts: the claude session it resumes, the model it runs, and for a step the
// agent, the JSON object of inline agents and the schema it runs with, and the observer of its hand-offs. step marks a
// step session.
type spawnSpec struct {
	claudeSession string
	model         string
	agent         string
	agents        string
	schema        string
	step          bool
	observer      StepObserver
}

// spawn resolves claude and starts the session process for id in dir as sp describes, ctx bounding its launch. The
// prompt is not sent here. The process takes the context stamp in effect for dir as its settings, and a stamp that cannot
// be read fails the spawn before claude runs. The launch of id is committed before the holder starts, so a commit that
// fails fails the spawn too.
func (m *Manager) spawn(ctx context.Context, id, dir string, sp spawnSpec) (*session.Session, error) {
	claude, err := exec.LookPath(m.claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	settings, err := m.stampSettings(dir)
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	if err = m.writer.Commit(ctx, id, store.EngineLaunch{ID: id}); err != nil {
		return nil, fmt.Errorf("launch session %s: %w", id, err)
	}
	sess, err := session.Start(ctx, session.Config{
		Shell:             m.shell,
		Claude:            claude,
		Dir:               dir,
		PermissionMode:    permissionMode,
		Settings:          settings,
		Resume:            sp.claudeSession,
		Model:             sp.model,
		Agent:             sp.agent,
		Agents:            sp.agents,
		Schema:            sp.schema,
		Name:              id,
		HolderScope:       m.holderScope,
		HolderScopeMemory: m.holderScopeMemory,
		StateDir:          m.stateDir,
		Environ:           m.environ,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	return sess, nil
}

// stampSettings returns the --settings JSON that carries the whole env of the context stamp in effect for dir. The stamp
// is looked up under the HOME entry of the manager's environment, which must be an absolute path. With no stamp, it
// returns "".
func (m *Manager) stampSettings(dir string) (string, error) {
	home, err := stampHome(m.environ)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", dir, err)
	}
	found, err := stamp.Find(home, dir)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", dir, err)
	}
	if found.Folder == "" {
		return "", nil
	}
	return stamp.Settings(false, found.Env), nil
}

// environValue returns the value of name in environ, and whether environ has it. The last entry for name wins, as it
// does for a child process that exec starts with environ.
func environValue(environ []string, name string) (string, bool) {
	value, found := "", false
	for _, entry := range environ {
		key, val, ok := strings.Cut(entry, "=")
		if ok && key == name {
			value, found = val, true
		}
	}
	return value, found
}

// stampHome returns the HOME entry of environ, the home every stamp is looked up under. It refuses a HOME that is absent,
// empty, or not an absolute path, so no stamp is ever read below a home the lookup cannot resolve.
func stampHome(environ []string) (string, error) {
	home, ok := environValue(environ, "HOME")
	if !ok {
		return "", errors.New("HOME is not set")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("HOME %q is not an absolute path", home)
	}
	return home, nil
}

// pump records the events of one running session, line by line and in order, until its lines close, and tells the owner
// of each event once it is recorded. reply is the reply text of the turn the session's earlier lines left, so the pump
// continues that turn. It is the only reader of the session's lines and the only user of its recorder. When it ends it
// frees the session's slot and removes its live entry, only while that entry is its own. A retired step keeps its slot for
// its id instead, and a retired session leaves m.stopping when its stop has returned (see stopRetired).
func (m *Manager) pump(ctx context.Context, live *liveSession, rec *recorder, reply string) {
	defer m.pumps.Done()
	for line := range live.sess.Lines() {
		reply = m.recordLine(ctx, live, rec, line, reply)
	}
	m.mu.Lock()
	if m.sessions[live.id] == live {
		delete(m.sessions, live.id)
	}
	switch {
	case !live.step:
		m.reserved--
	case !live.retired:
		m.stepsRunning--
	}
	m.mu.Unlock()
	close(live.done)
	live.cancel()
}

// eventOps returns the store ops that record event, as rec gives them. A turn's end that another turn follows leaves
// the session working (see recorder.followedTurnEnd), and one the owner's interrupt caused is recorded as such.
func eventOps(rec *recorder, event session.Event, followed, ownerInterrupted bool) []store.EngineOp {
	ended, ok := event.(session.TurnEnded)
	switch {
	case !ok:
		return rec.ops(event)
	case followed:
		return rec.followedTurnEnd(ended, ownerInterrupted)
	default:
		return rec.turnEnd(ended, protocol.StateDone, ownerInterrupted)
	}
}

// recordedEvent is an event of a line the pump has recorded, with what its owner notice and observer call need: the
// reply as of that event, and the interrupted, handed, followed and opened results of noteEvent for it.
type recordedEvent struct {
	event       session.Event
	reply       string
	interrupted bool
	handed      bool
	followed    bool
	opened      bool
}

// heldWrite is a held message that a turn's end writes as the next turn: the message, and the write id it is sent under.
type heldWrite struct {
	prompt  store.HeldPrompt
	writeID string
}

// recordLine records one line of the running session in one write: the ops of all its events, then the line's consumed
// offset, so a crash stores the line wholly or not at all. A line that holds a turn's end waits until the store has
// it, and the session's replay-from is then set to the line's end. A turn's end that the session's oldest held message
// follows is recorded as followed, and that message is written in the same write: its engine_prompt, and its removal from
// the held set (see heldNext). Once the write succeeds, the message is sent. Once the write succeeds, the owner of each
// recorded event is told, in order, each with the reply as of that event. A failed write is logged and tells no owner. The
// observer is given every recorded event either way, as before. The line is recorded under the session's order lock, so
// the held messages and the turn's state do not change under it. It returns the reply text of the main conversation so far.
func (m *Manager) recordLine(
	ctx context.Context, live *liveSession, rec *recorder, line session.Line, reply string,
) string {
	live.order.Lock()
	ops, recorded, sent, reply := m.lineOps(ctx, live, rec, line, m.heldNext(ctx, live, line), reply)
	written := m.storeLine(ctx, live, line, ops, sent)
	live.order.Unlock()
	m.tellRecorded(ctx, live, recorded, written)
	return reply
}

// lineOps returns the store ops that record the events of line in order, the events the owner is told of, the held message
// that the line's turn end writes as the next turn (see heldNext), or nil when none is, and the reply as of the line's end. next
// is the held message the line may write, and it is taken by the first turn end that follows it.
func (m *Manager) lineOps(
	ctx context.Context, live *liveSession, rec *recorder, line session.Line, next *heldWrite, reply string,
) ([]store.EngineOp, []recordedEvent, *heldWrite, string) {
	var ops []store.EngineOp
	var recorded []recordedEvent
	var sent *heldWrite
	for _, event := range line.Events {
		if request, ok := event.(session.Request); ok && live.step && !request.Refused && !request.Answered {
			m.answerStepRequest(ctx, live, request)
			continue
		}
		note := m.noteEvent(live, event, true, next != nil)
		if !note.record {
			continue
		}
		if note.opened {
			ops = append(ops, rec.turnOpened()...)
		}
		ops = append(ops, eventOps(rec, event, note.followed, note.ownerInterrupted)...)
		if note.held {
			ops = append(ops, heldOps(live, next)...)
			sent, next = next, nil
		}
		if text, ok := mainText(event); ok {
			reply = text
		}
		recorded = append(recorded, recordedEvent{
			event: event, reply: reply, interrupted: note.interrupted, handed: note.handed, followed: note.followed,
			opened: note.opened,
		})
		if _, ended := event.(session.TurnEnded); ended {
			reply = ""
		}
	}
	return ops, recorded, sent, reply
}

// heldOps returns the ops that write the held message next as the turn that a turn's end in the line follows: its removal from
// the held set, and its engine_prompt, which carries the write id it is sent under. The write id is expected before the write,
// as send expects it.
func heldOps(live *liveSession, next *heldWrite) []store.EngineOp {
	live.sess.ExpectPrompt(next.writeID)
	return []store.EngineOp{
		store.EngineHeldRelease{ID: live.id, PromptID: next.prompt.PromptID},
		store.EnginePrompt{ID: live.id, Text: next.prompt.Text, PromptID: next.prompt.PromptID, WriteID: next.writeID},
	}
}

// storeLine stores the ops of one line of live in one write, with the line's consumed offset when it holds one, and reports
// whether the write succeeded. A line that holds a turn's end, or writes a held message, waits until the store has the ops, and
// the session's replay-from is then set to the line's end. A held message the line writes is sent once its write is stored,
// and a failed write takes it back (see sendHeld).
func (m *Manager) storeLine(
	ctx context.Context, live *liveSession, line session.Line, ops []store.EngineOp, sent *heldWrite,
) bool {
	ended := false
	if !holdsExit(line) {
		var consumed store.EngineConsumed
		consumed, ended = consumedOf(live.id, line)
		ops = append(ops, consumed)
	}
	written := true
	if err := m.writeLine(ctx, live, ops, ended || sent != nil); err != nil {
		m.logger.WarnContext(ctx, "record session event", "session", live.id, "err", err)
		written = false
	}
	if written && ended {
		live.sess.SetReplayFrom(line.End)
	}
	if sent != nil {
		m.sendHeld(ctx, live, *sent, written)
	}
	return written
}

// heldNext returns the oldest message held for live, with the write id it would be sent under, when line holds a turn's end
// of a session that is not a step, and the session holds a message. Otherwise it returns nil. It reads the store under the
// caller's order lock, so the message is the one a turn's end in line can write.
func (m *Manager) heldNext(ctx context.Context, live *liveSession, line session.Line) *heldWrite {
	if live.step || !slices.ContainsFunc(line.Events, isTurnEnd) {
		return nil
	}
	held, err := m.store.HeldPrompts(ctx, live.id)
	if err != nil {
		m.logger.WarnContext(ctx, "read held prompts", "session", live.id, "err", err)
		return nil
	}
	if len(held) == 0 {
		return nil
	}
	writeID, err := newWriteID()
	if err != nil {
		m.logger.WarnContext(ctx, "write id of held prompt", "session", live.id, "err", err)
		return nil
	}
	return &heldWrite{prompt: held[0], writeID: writeID}
}

// isTurnEnd reports whether event is a turn's end.
func isTurnEnd(event session.Event) bool {
	_, ok := event.(session.TurnEnded)
	return ok
}

// sendHeld sends head, the held message a turn's end wrote as the next turn in the line recordLine stored, and ends the send
// that recordLine began. When the line was not stored, the message is taken back: it stays held, and the turn is over, so
// the session is idle. A failed send records the session failed, and a session being closed records nothing, as Close leaves
// it. The caller holds live's order lock.
func (m *Manager) sendHeld(ctx context.Context, live *liveSession, head heldWrite, written bool) {
	defer m.endSend(live)
	if !written {
		live.sess.DropPrompt(head.writeID)
		m.setTurning(live, false)
		return
	}
	prompt := store.EnginePrompt{ID: live.id, Text: head.prompt.Text, PromptID: head.prompt.PromptID}
	err := m.transmit(live, head.prompt.Text, head.writeID)
	if err == nil {
		return
	}
	m.mu.Lock()
	closing := live.closing
	m.mu.Unlock()
	if closing {
		m.setTurning(live, false)
		m.logger.WarnContext(ctx, "send held prompt on close", "session", live.id, "err", err)
		return
	}
	m.logger.WarnContext(ctx, "send held prompt", "session", live.id, "err", m.failSend(ctx, live, prompt, err))
}

// tellRecorded tells the owner of live of each recorded event when the line's write succeeded, and gives the observer
// every one either way. A followed turn end leaves the session working, so it tells the owner nothing: the turn after it
// does. An event that opened a turn claude started by itself first tells the owner the session changed, since it left
// done for working.
func (m *Manager) tellRecorded(ctx context.Context, live *liveSession, recorded []recordedEvent, written bool) {
	for _, rec := range recorded {
		if written && rec.opened && !live.step {
			m.changed(ctx, live.id)
		}
		if written && !rec.followed {
			m.tellOwner(ctx, live, rec.event, rec.reply, rec.interrupted)
		}
		observe(live, rec.event, rec.handed)
	}
}

// writeLine queues the ops of one line of the session of live in one write. A line that holds a turn's end, ended,
// waits until the store has the ops. The caller holds live's order lock, so every op an answer causes is queued after the
// line's.
func (m *Manager) writeLine(ctx context.Context, live *liveSession, ops []store.EngineOp, ended bool) error {
	if ended {
		return m.writer.Commit(ctx, live.id, ops...)
	}
	return m.writer.Put(ctx, live.id, ops...)
}

// answerStepRequest denies request, a request of the step live that the session did not refuse, with stepDenyMessage, so
// the request never opens and the step never waits on it. Only questions and permission requests reach here: the session
// refuses every other kind itself. A failed answer is logged; the session then stays blocked on the request until its
// process exits, which the pump records.
func (m *Manager) answerStepRequest(ctx context.Context, live *liveSession, request session.Request) {
	if err := live.sess.DenyEvent(request, stepDenyMessage); err != nil {
		m.logger.WarnContext(ctx, "answer step request", "session", live.id, "request", request.RequestID, "err", err)
	}
}

// structuredOutputTool is the tool claude returns a --json-schema result through. A step with a schema whose init event
// does not list it cannot return a value.
const structuredOutputTool = "StructuredOutput"

// observe gives the observer of the step live the event the pump has just recorded: the event itself, then the turn end
// of a hand-off, or an exit with its failure reason, or stepExitedReason for a clean exit, since a step ends either way.
// A step whose init event does not run its agent ends with an exit reason naming the agent, and its observer stops it.
// A step with a schema whose init event lists no StructuredOutput tool ends the same way, before any turn end, since
// its agent cannot return the schema's value. A session without an observer gets nothing.
func observe(live *liveSession, event session.Event, handed bool) {
	if live.observer == nil {
		return
	}
	live.observer.Event(event)
	switch ev := event.(type) {
	case session.Started:
		switch {
		case live.step && !initNamesAgent(ev.Agents, live.agent):
			live.observer.Exited(fmt.Sprintf("claude's init event does not run the step's agent %q", live.agent))
		case live.step && live.schema && !slices.Contains(ev.Tools, structuredOutputTool):
			live.observer.Exited(fmt.Sprintf(
				"claude's init event gives the step's agent %q no %s tool", live.agent, structuredOutputTool,
			))
		}
	case session.TurnEnded:
		if handed {
			live.observer.Return(ev)
		}
	case session.Exited:
		reason, failed := exitFailure(ev)
		if !failed {
			reason = stepExitedReason
		}
		live.observer.Exited(reason)
	}
}

// stepExitedReason is the reason a step's observer gets when the step's process exits cleanly on its own.
const stepExitedReason = "the step's process exited"

// initNamesAgent reports whether the agents list of a session's init event runs agent. The list names agent, or holds
// a scoped entry ending in ":"+agent, which is how claude resolves a bare name. web-fetch never runs as a step's agent,
// listed or not.
func initNamesAgent(agents []string, agent string) bool {
	if agent == "web-fetch" {
		return false
	}
	for _, name := range agents {
		if name == agent || strings.HasSuffix(name, ":"+agent) {
			return true
		}
	}
	return false
}

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

// noted is what noteEvent reports of an event: whether the pump records it; for a turn's end, whether that turn was
// interrupted through Interrupt, whether the owner's OwnerInterrupt caused it, whether it is handed to the step's
// observer, whether another turn follows it, and whether that turn is the oldest held message, which is written as the
// next turn (held implies followed); and whether the event opens a turn that claude started by itself.
type noted struct {
	record, interrupted, ownerInterrupted, handed, followed, held, opened bool
}

// opensTurn reports whether event is one only a running turn gives: its init, its streamed or finished content, a tool
// call, an assistant error or a request of the host. A report of background tasks, a rate limit, a notice or an exit can
// come between turns, so it opens none. Content or a tool call of a subagent, one with a ParentToolUseID, opens none
// either: a subagent launched in the background streams after the turn that launched it has ended, and the turn claude
// starts when its task ends opens on that turn's own main-conversation events.
func opensTurn(event session.Event) bool {
	switch ev := event.(type) {
	case session.TextDelta:
		return ev.ParentToolUseID == ""
	case session.ThinkingDelta:
		return ev.ParentToolUseID == ""
	case session.ToolCallStarted:
		return ev.ParentToolUseID == ""
	case session.Block:
		return ev.ParentToolUseID == ""
	case session.Started, session.AssistantError, session.Request:
		return true
	}
	return false
}

// noteEvent tracks the turn of live through event, one the pump reads when pumping is true and one replayed otherwise.
// It reports whether the pump records event, and for a turn's end, whether that turn was interrupted through Interrupt,
// which clears the mark for the next turn, and whether the turn is handed to the step's observer: a step's turn that ends
// with no background task live, or whose structured return is the step's done value while one is live (see
// StepObserver.Done). The hand-off makes the step busy in the same lock hold. A pumped turn's end is followed by another
// turn when a prompt the session counts as sent is still unanswered after it, as its reader counted at that end or as the
// session counts now: claude runs a prompt sent during a turn as the turn after it. A prompt counts from before its
// record is stored (see session.Session.ExpectPrompt), so the send under way of the prompt this very turn answers
// follows nothing, and a send whose prompt the turn does not answer is still counted. A followed
// turn end keeps the turn running, hands nothing to the observer and retires nothing. A pumped event that opens a turn
// (see opensTurn) while no turn runs opens one claude started by itself, as a background task's end starts: the turn is
// marked running, started by claude, and noteEvent reports it opened. A draining session whose turn ends
// is retired in the same lock hold (see retireLocked). held is set when the oldest held message of the session can follow
// a pumped turn's end, as recordLine reads it (see noteTurnEndedLocked). The exit of a session that Close or a retirement is
// stopping is not recorded here: recordClosed records a closed session's state from its turn instead, and under a holder
// scope records nothing, and a retired session records nothing.
func (m *Manager) noteEvent(live *liveSession, event session.Event, pumping, held bool) noted {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.updateIdleLocked(live)
	opened := false
	if pumping && opensTurn(event) && !live.turning {
		live.turning = true
		live.starter = claudeTurn
		opened = true
	}
	switch ev := event.(type) {
	case session.Started:
		live.turning = true
	case session.Request:
		if !ev.Refused && !ev.Answered {
			live.requests[ev.RequestID] = struct{}{}
		}
	case session.RequestCancelled:
		delete(live.requests, ev.RequestID)
	case session.BackgroundTasks:
		live.background = len(ev.Tasks) > 0
	case session.TurnEnded:
		return m.noteTurnEndedLocked(live, ev, pumping, held)
	case session.Exited:
		live.turning = false
		live.ownerInterrupt = false
		clear(live.requests)
		live.background = false
		if live.closing || live.idleStopped || live.retired {
			return noted{}
		}
		live.exited = true
	}
	return noted{record: true, opened: opened}
}

// noteTurnEndedLocked tracks the end of a turn of live, as noteEvent gives it. A turn's end that no prompt follows is
// followed by the oldest held message instead, when held is set and the session has a live process that is not draining
// or closing: the turn stays running, and a send is pending until recordLine has written that message and sent it. Such
// a session is the one the manager runs under its id, so its turn ends write the held messages, and a draining one does not.
// A held message is the owner's, so the turn it starts is the owner's. The caller holds the lock.
func (m *Manager) noteTurnEndedLocked(live *liveSession, ended session.TurnEnded, pumping, held bool) noted {
	clear(live.requests)
	interrupted := live.interrupting
	live.interrupting = false
	ownerInterrupted := live.ownerInterrupt
	live.ownerInterrupt = false
	if pumping && (ended.Unanswered > 0 || live.sess.UnansweredPrompts() > 0) {
		return noted{record: true, interrupted: interrupted, ownerInterrupted: ownerInterrupted, followed: true}
	}
	if pumping && held && m.sessions[live.id] == live && !live.draining && !live.closing {
		live.turning = true
		live.starter = ownerTurn
		live.sending++
		return noted{
			record: true, interrupted: interrupted, ownerInterrupted: ownerInterrupted, followed: true, held: true,
		}
	}
	live.turning = false
	handed := false
	if live.observer != nil && (!live.background || live.observer.Done(ended)) {
		live.returned = true
		handed = true
	}
	m.retireLocked(live)
	return noted{record: true, interrupted: interrupted, ownerInterrupted: ownerInterrupted, handed: handed}
}

// retireLocked takes live out of the live set and stops its process, when live is draining and not busy: no turn runs, no
// request is open and no background task runs, so the stop ends nothing the session is still doing. It also needs no send
// pending, and every prompt the session sent answered, so the stop leaves no prompt unanswered: a prompt claude runs as a
// following turn, or folds into the running turn, is answered on this holder before it stops. A background task still
// running at a turn's end keeps the session until a later turn ends with none, or until the idle stop takes it. A step's
// hand-off does not hold a draining step: its next Continue respawns it, so the step is retired at its hand-off. A retired
// step keeps its step slot for its id, in m.kept, which its pump does not free. Each step that can leave a draining session
// drained calls it under the lock: register, noteEvent at a turn's end, and endSend. The session leaves m.sessions and
// enters m.stopping in that lock hold, so no prompt reaches it afterwards. Its stop runs on its own goroutine, so the pump
// that calls this goes on recording the line it is on. The stop records nothing. An engine session holds its slot until its
// pump ends, and awaitStopping also waits for the stop, so a new holder never starts over this one's directory. The
// caller holds the lock.
func (m *Manager) retireLocked(live *liveSession) {
	if !live.draining || live.retired || live.sending > 0 || live.sess.UnansweredPrompts() > 0 {
		return
	}
	if live.turning || live.background || len(live.requests) > 0 {
		return
	}
	live.retired = true
	live.stopped = make(chan struct{})
	if m.sessions[live.id] == live {
		delete(m.sessions, live.id)
	}
	m.stopping[live.id] = live
	if live.step {
		m.kept[live.id] = live
	}
	go m.stopRetired(live)
}

// stopRetired stops the process of live, a session retireLocked retired, waits for its pump to end, then takes it out of
// m.stopping and closes live.stopped. The pump ends once the stream does, which can be before the stop returns, since
// the holder's directory is removed after its stream ends; the stop returns once that directory is gone. A failed stop
// is logged.
func (m *Manager) stopRetired(live *liveSession) {
	if err := live.sess.Stop(); err != nil {
		m.logger.Warn("stop drained session", "session", live.id, "err", err)
	}
	<-live.done
	m.mu.Lock()
	if m.stopping[live.id] == live {
		delete(m.stopping, live.id)
	}
	m.mu.Unlock()
	close(live.stopped)
}

// notify sends the owner a notice of session id in dir, in state with body. It sends nothing without a notifier.
func (m *Manager) notify(ctx context.Context, id, dir, state, body string) {
	if m.notifier == nil {
		return
	}
	m.notifier.Notify(ctx, Notice{Session: id, Dir: dir, State: state, Body: body})
}

// tellOwner tells the owner of recorded event of live: a notice when the event calls for one, and otherwise that the
// session changed when the event ends its pending notice. A step tells the owner nothing.
func (m *Manager) tellOwner(
	ctx context.Context, live *liveSession, event session.Event, reply string, interrupted bool,
) {
	if live.step {
		return
	}
	if state, body, notify := eventNotice(event, reply, interrupted); notify {
		m.notify(ctx, live.id, live.dir, state, body)
	} else if endsNotice(event) {
		m.changed(ctx, live.id)
	}
}

// changed tells the notifier that session id left the state its pending notice was about. It sends nothing without a
// notifier.
func (m *Manager) changed(ctx context.Context, id string) {
	if m.notifier == nil {
		return
	}
	m.notifier.Changed(ctx, id)
}

// endsNotice reports whether a recorded event records a session working or stopped, which ends its pending notice as
// a pane's transition does. A failed exit records failed, which notifies instead.
func endsNotice(event session.Event) bool {
	switch ev := event.(type) {
	case session.RequestCancelled:
		return true
	case session.Exited:
		_, failed := exitFailure(ev)
		return !failed
	}
	return false
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
// of its own. The stop tells the owner the session changed. It returns the error of the wait or of the write.
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
	if !live.step {
		m.changed(ctx, live.id)
	}
	return nil
}

// recordClosed waits for the pump of live to finish, then records live as Close leaves it: interrupted with the reason
// when its turn was running, and otherwise in the state its turn gave it. Either way its unfinished text is removed. An
// interruption notifies the owner. A session whose exit already recorded its state is left alone. Under a holder scope
// Close only detaches the session, so nothing is recorded for it. It returns the error of the wait or of the write.
func (m *Manager) recordClosed(ctx context.Context, live *liveSession) error {
	if err := m.waitPump(ctx, live); err != nil {
		return err
	}
	if live.exited || m.holderScope != "" {
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
