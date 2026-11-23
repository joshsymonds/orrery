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

// ErrUnknownPrompt reports a Withdraw of a prompt id that no owner prompt of the session carries: the id is unknown, or
// it names a prompt another sender sent. Withdraw sends nothing.
var ErrUnknownPrompt = errors.New("no owner prompt of the session has that prompt id")

// errSessionEnded reports a delivery to a session whose process ended after the Say found it live. Say resumes the session
// and delivers once more.
var errSessionEnded = errors.New("session ended before the message was delivered")

// ErrSessionBusy reports a Deliver to a live session that has a request open. Deliver stores and sends nothing for it.
var ErrSessionBusy = errors.New("session is busy")

// ErrCallerStopped reports a Deliver to a session whose row is stopped, as the owner's Stop leaves it. Deliver resumes
// nothing and sends nothing to it.
var ErrCallerStopped = errors.New("session was stopped")

// ErrCallerQuiet reports a Deliver to a live session that the owner interrupted and has said nothing to since (see
// liveSession). Deliver stores and sends nothing for it.
var ErrCallerQuiet = errors.New("session was interrupted by its owner")

// ErrCallerFailed reports a DeliverCron to a session whose row is failed. The session waits for its owner's next
// message, so DeliverCron resumes nothing and sends nothing to it.
var ErrCallerFailed = errors.New("session failed and waits for its owner's next message")

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
// ids of the quiet sessions that Reconcile holds (see readQuiet), the rows Say resumes, checks and Seen marks, the prompts
// a re-adopted holder has not written and how many prompts were written that no later turn end answers, the step rows
// Continue respawns from, the failure markers Say checks, the prompt Withdraw names, by its prompt id, for its write id and
// text, and the prompts OwnerInterrupt reads for the texts of the write ids Claude cancelled. *store.Store satisfies it.
type Store interface {
	EngineSessions(ctx context.Context) ([]store.EngineRow, error)
	EngineTextSessions(ctx context.Context) (map[string]bool, error)
	QuietSessions(ctx context.Context) ([]string, error)
	EngineSession(ctx context.Context, id string) (store.EngineRow, error)
	HolderPrompts(ctx context.Context, id string) ([]store.HolderPrompt, error)
	HolderPromptsUnanswered(ctx context.Context, id string) (int, error)
	Step(ctx context.Context, id string) (store.StepRow, error)
	EnginePromptSent(ctx context.Context, sessionID, promptID string) (bool, error)
	EnginePromptFailure(ctx context.Context, sessionID, promptID string) (string, bool, error)
	PromptWrite(ctx context.Context, id, promptID string) (store.PromptWrite, bool, error)
	PromptsOfWrites(ctx context.Context, id string, writeIDs []string) ([]store.WritePrompt, error)
	DeleteSessionCronJobs(ctx context.Context, session string) (int, error)
}

// Config configures a Manager. Shell is the login shell and Claude the claude name, resolved with exec.LookPath at
// each start. Environ is the environment sessions start with, Writer stores their ops, Now stamps them (nil is time.Now),
// Notifier receives the notices of their state changes (nil sends none), Store is the engine rows Reconcile, Say and
// Seen read and Logger reports pump errors (nil discards). HolderScope is the absolute path of systemd-run that runs
// each session's holder in its own user scope, or empty to run holders plainly; with it set, Close detaches the
// sessions instead of stopping them, so their holders keep running. StateDir is the state directory that holds the
// sessions' holders, or empty for a private directory per session. ManagedDir is the managed settings root that a
// step's agent resolves under, or empty to read none. HolderVersion is the holder wire version this manager's controller
// speaks when it re-adopts a holder, or zero for holder.WireVersion. Metrics counts each holder lost in re-adoption, or
// nil to count none.
//
// CronWake, when set, is called with a session id after the owner's Say to that session returns nil, and after a turn's
// end that leaves a session which is not a step with no turn running is stored. It is called with "" whenever the
// process of a session that is not a step ends. Each call runs on its own goroutine, off the manager's locks (see
// wakeCron).
//
// EventWake, when set, is called with no argument when a caller event may now find its session free to take it: after
// the owner's Say to a session returns nil, when the owner's Say clears a session's quiet, when a session goes live,
// when the last request a session has open closes, and when a session's process ends. It must not block or call the
// manager: the manager calls it synchronously, including under its locks. Nil does nothing (see wakeEvent).
//
// PutOffs stores the owner's put-off marks, which NotNow and BringBack set and clear, and it refuses both as unavailable
// when nil. Panes reads the panes' questions and reports a pane seen to the notice router, which NotNow and BringBack of a
// pane need, so it refuses a pane call as unavailable when nil.
type Config struct {
	Shell         string
	Claude        string
	Environ       []string
	HolderScope   string
	StateDir      string
	ManagedDir    string
	HolderVersion int
	Writer        *Writer
	Now           func() time.Time
	Notifier      NoticeSink
	Store         Store
	Logger        *slog.Logger
	Metrics       *telemetry.Daemon
	CronWake      func(session string)
	EventWake     func()
	PutOffs       PutOffStore
	Panes         PaneQuestions

	// AfterFunc arms a timer that calls the function once after the duration, and returns the function that stops the
	// timer, as time.AfterFunc does. A nil AfterFunc wraps time.AfterFunc. The idle deadline of each session is armed
	// through it.
	AfterFunc func(time.Duration, func()) (stop func() bool)
}

// liveSession is one running session: its directory, its process, the done channel closed when its pump has finished,
// and the cancel that frees a pump blocked on a write. exited is set by the pump when it records the process's exit, so
// Stop and Close do not record over the state that exit recorded. rec is the recorder the pump records with, which Stop
// and Close read for the background tasks still running once the pump has finished. turning is set while a turn runs,
// from the send of its prompt or a Started event until its TurnEnded or the process's Exited. interrupting is set by
// Interrupt while a turn runs, until that turn's end, which then sends no notice. ownerInterrupt is set by
// OwnerInterrupt while a turn runs, until that turn's end or the process's Exited, and marks that turn's end as caused
// by the owner (see noteTurnEndedLocked). closing is set by Close before it stops the session. The manager's lock
// guards turning, interrupting, ownerInterrupt, closing, sending, requests, background, awaited, returned, idleSince,
// idleTimer and idleStopped, and the pump sets exited under it too. requests holds the ids of the requests the host has
// open, background is set while a background task runs, awaited while one of those tasks runs a model (see
// awaitsTask), idleSince is when the session last became not busy (zero while it is busy), idleTimer is the function
// that stops the timer armed for the session's idle deadline (nil while none is armed), and idleStopped is set when a
// check stops the session for being idle. step is set for a step session, which sends no notice. observer receives the hand-offs of a step, which the pump makes, and returned is set from
// a hand-off until Continue sends the step's next prompt, so the step stays busy meanwhile. schema is set for a step
// that runs with a schema, whose init event must list StructuredOutput (see observe). draining is set for a session
// re-adopted from a holder one version older than the controller's, which retireLocked takes out of the live set once
// no turn, request or background task runs and it owes no answer, and retired is set once it has. stopped is closed
// once the stop of a retired session returns, which is once its holder has ended and its directory is removed. sending
// counts the sends of a prompt that markSending or claimStep began and send has not ended, so a draining session is
// not retired between the start of a send and its end.
type liveSession struct {
	id             string
	dir            string
	sess           *session.Session
	rec            *recorder
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
	closing        bool
	draining       bool
	retired        bool
	sending        int
	requests       map[string]struct{}
	background     bool
	awaited        bool
	returned       bool
	idleSince      time.Time
	idleTimer      func() bool
	idleStopped    bool
	// order is held by the pump while it records a line, by Answer from its send until it has queued the working state, so
	// every op an answer causes is queued after that state, and by deliver while it decides and writes a message, so the
	// pump's turn ends and those decisions never interleave.
	order sync.Mutex
}

// Manager starts, stops and tracks headless sessions. Each running session has a pump goroutine that records its
// events through the Writer. Close stops every session and waits for the pumps.
type Manager struct {
	shell       string
	claude      string
	environ     []string
	holderScope string
	stateDir    string
	managedDir  string
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
	writer           *Writer
	now              func() time.Time
	notifier         NoticeSink
	store            Store
	logger           *slog.Logger
	metrics          *telemetry.Daemon
	// cronWake is Config.CronWake, called on its own goroutine (see wakeCron).
	cronWake func(session string)
	// eventWake is Config.EventWake, called synchronously (see wakeEvent).
	eventWake func()
	// putOffs and panes are Config.PutOffs and Config.Panes, which NotNow and BringBack use.
	putOffs PutOffStore
	panes   PaneQuestions

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
	// stepObservers holds the observer each step was started with, kept after the step stops so a respawn hands its turns
	// to the same observer.
	stepObservers map[string]StepObserver
	// kept holds the drained steps, by id, from their retirement until Stop, Release or a respawn takes them (see
	// retireLocked). Each has no live session.
	kept map[string]*liveSession
	// quiet holds the ids of the sessions the owner's Interrupt left quiet, until the owner's next Say to each clears it.
	// While an id is in it, no caller event starts a turn in that session or resumes it (see claimFree and admitEvent),
	// and Reconcile does not resume it (see resumable). It is kept apart from the live session, so a session the idle
	// check stops stays quiet when it is resumed. A restart restores it from the store's engine_interrupt events that no
	// owner prompt follows, as Reconcile reads them (see readQuiet and Store.QuietSessions), so it stays quiet across a
	// restart until its owner speaks.
	quiet map[string]struct{}
	// quietUnread is set while Reconcile runs after its read of the quiet sessions failed. Which sessions are quiet is then
	// not known, so Reconcile resumes no row (see resumable).
	quietUnread bool
	closed      bool
	// idleDue receives from each idle deadline timer, so RunIdle runs a check. idleQuit is closed by Close, so a timer
	// that fires after Close does not wait for RunIdle (see idleDeadline).
	idleDue  chan struct{}
	idleQuit chan struct{}
	// idleChecks counts the idle checks checkIdle has run, under mu. Only tests read it, to show that no check runs between
	// two deadlines.
	idleChecks int

	// resumeMu serializes the resume of a session that is not live, so concurrent Says to one id start one process.
	resumeMu sync.Mutex
	// attemptMu guards attempts. attempts holds the keyed calls running now, by key: a Start that names a session id, or
	// a Say that names a prompt id. A key's attempt is removed when it ends, so a later call with that key checks the
	// store as an unkeyed call would. An attempt that failed after it stored its record stays, with its error, so a
	// later call with that key returns the error. A Say whose send failed also stores a failure marker for its prompt,
	// which a later Say with that key returns after a restart too.
	attemptMu sync.Mutex
	attempts  map[attemptKey]*attempt

	// afterFunc arms the idle deadline timer of each session (Config.AfterFunc).
	afterFunc func(time.Duration, func()) (stop func() bool)
}

// NewManager returns a Manager for cfg.
func NewManager(cfg Config) *Manager {
	manager := &Manager{
		shell:         cfg.Shell,
		claude:        cfg.Claude,
		environ:       cfg.Environ,
		holderScope:   cfg.HolderScope,
		stateDir:      cfg.StateDir,
		managedDir:    cfg.ManagedDir,
		holderVersion: cfg.HolderVersion,
		writer:        cfg.Writer,
		now:           cfg.Now,
		afterFunc:     cfg.AfterFunc,
		notifier:      cfg.Notifier,
		store:         cfg.Store,
		logger:        cfg.Logger,
		metrics:       cfg.Metrics,
		cronWake:      cfg.CronWake,
		eventWake:     cfg.EventWake,
		putOffs:       cfg.PutOffs,
		panes:         cfg.Panes,
		sessions:      map[string]*liveSession{},
		stopping:      map[string]*liveSession{},
		stepObservers: map[string]StepObserver{},
		kept:          map[string]*liveSession{},
		quiet:         map[string]struct{}{},
		attempts:      map[attemptKey]*attempt{},
		idleDue:       make(chan struct{}),
		idleQuit:      make(chan struct{}),
	}
	if manager.holderVersion == 0 {
		manager.holderVersion = holder.WireVersion
	}
	if manager.now == nil {
		manager.now = time.Now
	}
	if manager.afterFunc == nil {
		manager.afterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	if manager.logger == nil {
		manager.logger = slog.New(slog.DiscardHandler)
	}
	return manager
}

// StartRequest is what Start needs: the directory the session runs in, its first prompt, and the model alias it runs.
// An empty Model runs claude's default. An empty ID mints a session id; a non-empty ID is the id the session takes.
// Client is the X-Orrery-Client of the request, stored with the first prompt; empty when none was sent.
type StartRequest struct {
	Dir    string
	Prompt string
	Model  string
	ID     string
	Client string
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

// start starts the session id as req describes. It checks req and that the manager is open, records the row, then
// spawns the process and sends the first prompt.
func (m *Manager) start(ctx context.Context, id string, req StartRequest) (string, error) {
	if err := checkStart(req); err != nil {
		return "", err
	}
	if err := m.checkOpen(); err != nil {
		return "", err
	}
	add := store.EngineAdd{ID: id, Dir: req.Dir, State: string(protocol.StateWorking)}
	if err := m.writer.Commit(ctx, id, add); err != nil {
		return "", fmt.Errorf("record session %s: %w", id, err)
	}
	live, err := m.spawnLive(ctx, id, req.Dir, spawnSpec{model: req.Model})
	if err != nil {
		return id, m.recordFailed(ctx, id, req.Dir, err)
	}
	writeID, err := newWriteID()
	if err != nil {
		return id, m.abandon(ctx, live, err)
	}
	live.sess.ExpectPrompt(writeID)
	prompt := store.EnginePrompt{ID: id, Text: req.Prompt, WriteID: writeID, Client: req.Client}
	if err = m.writer.Commit(ctx, id, prompt); err != nil {
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
// session is recorded stopped the same way, and leaves the drained set (see stopDrained). It returns ErrNoSession when id
// is neither. Unless id is a step, which keeps its own cron, Stop first deletes the cron jobs of id, so none fires into
// the stopped session, whether id is live or idle-stopped. A deletion error is joined into the error Stop returns, and
// the stop goes on. resumeMu is held, as Release holds it, so a respawn cannot take the drained step between the lookup
// and the stop.
func (m *Manager) Stop(ctx context.Context, id string) error {
	defer m.calls.enter()()
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	m.mu.Lock()
	live := m.sessions[id]
	drained := m.kept[id]
	m.mu.Unlock()
	var cronErr error
	if drained == nil && (live == nil || !live.step) {
		if _, err := m.store.DeleteSessionCronJobs(ctx, id); err != nil {
			cronErr = fmt.Errorf("delete cron jobs of session %s: %w", id, err)
		}
	}
	switch {
	case live != nil:
		stopErr := live.sess.Stop()
		recordErr := m.recordStopped(ctx, live)
		if stopErr == nil && recordErr == nil {
			return cronErr
		}
		return errors.Join(cronErr, fmt.Errorf("stop session %s: %w", id, errors.Join(stopErr, recordErr)))
	case drained != nil:
		return m.stopDrained(ctx, drained)
	}
	return errors.Join(cronErr, fmt.Errorf("stop session %s: %w", id, ErrNoSession))
}

// stopDrained records the drained step stopped once its pump has ended, then drops it from the drained set. The caller
// holds resumeMu. It is dropped even when the record fails, since the step is stopped either way.
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

// dropKeptLocked drops the drained step id from the drained set, if it is there (see retireLocked). The caller holds the
// lock.
func (m *Manager) dropKeptLocked(id string) {
	delete(m.kept, id)
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
// PromptID is replaced by a minted one, so the message is stored under a prompt id like every other. Client is the
// X-Orrery-Client of the request, stored with the prompt; empty when none was sent.
type SayRequest struct {
	ID       string
	Text     string
	PromptID string
	Client   string
}

// Say sends req.Text to the session req.ID as its next prompt. A session that is not live is resumed first, from its
// engine row (see resumeSession). The message is written at once, whatever the session is doing: while a turn runs,
// whoever started it, with a background task listed, or idle. Nothing is held and no turn is interrupted (see deliver).
// The prompt it records has the owner as its sender. It records the prompt and the working state before it sends, so the
// stored conversation has the prompt ahead of its reply. An empty req.PromptID is minted first (see newWriteID). The
// attempt for a prompt id runs once per session: while an attempt for it runs, a Say with that id waits for the attempt
// and returns its result, and sends nothing. Once begun, the attempt finishes even if ctx ends, and a Say whose ctx ends
// returns ctx's error. With no attempt running, a req.PromptID that an engine_prompt event of the session already carries
// makes Say return nil and do nothing else. An attempt that fails after it stores the prompt keeps its error, and a later
// Say with that prompt id returns the error and does nothing else. A send that fails is recorded as the session's failed
// state; the same write stores that prompt's failure marker, so a Say with that prompt id returns the stored reason and
// does nothing else, even after a restart. It returns ErrEmptyPrompt for empty text, an error wrapping ErrNoSession when no
// engine row has the id, and the errors of resumeSession when a live session cannot be started.
func (m *Manager) Say(ctx context.Context, req SayRequest) error {
	defer m.calls.enter()()
	if err := m.sayPrompt(ctx, req); err != nil {
		return err
	}
	m.wakeCron(req.ID)
	m.wakeEvent()
	return nil
}

// sayPrompt sends req as Say does, without the call of the cron hook that Say makes once it returns nil.
func (m *Manager) sayPrompt(ctx context.Context, req SayRequest) error {
	if req.Text == "" {
		return ErrEmptyPrompt
	}
	if req.PromptID == "" {
		promptID, err := newWriteID()
		if err != nil {
			return fmt.Errorf("prompt id of session %s: %w", req.ID, err)
		}
		req.PromptID = promptID
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

// promptRecorded reports whether session id has the prompt promptID sent: an engine_prompt event of the session carries it.
func (m *Manager) promptRecorded(ctx context.Context, id, promptID string) (bool, error) {
	sent, err := m.store.EnginePromptSent(ctx, id, promptID)
	if err != nil {
		return false, fmt.Errorf("check prompt %s of session %s: %w", promptID, id, err)
	}
	return sent, nil
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

// deliver writes the message of req to live (see decide) and sends it.
func (m *Manager) deliver(ctx context.Context, live *liveSession, req SayRequest) error {
	sendNow, err := m.decide(ctx, live, req)
	if err != nil {
		return err
	}
	return sendNow()
}

// decide makes the delivery decision for req on live under live's order lock, so it is made against the session's turn as
// the pump leaves it. A request the session has open is declined first (see declineLocked). The owner's message clears the
// quiet that his Interrupt set, so caller events are delivered again (see claimFree). Every message is then written at once
// as the session's next prompt, whatever the session is doing: a turn running, whoever started it, a background task
// listed, or the session idle. Nothing is held and no turn is interrupted. decide returns the send to make once the lock is
// released, since a send waits on the holder and the pump needs the lock to record the turn end the send follows. It
// returns errSessionEnded when live is no longer the session the manager runs.
func (m *Manager) decide(ctx context.Context, live *liveSession, req SayRequest) (func() error, error) {
	live.order.Lock()
	defer live.order.Unlock()
	if !m.isLive(live) {
		return nil, errSessionEnded
	}
	message := store.EnginePrompt{
		ID: live.id, Text: req.Text, PromptID: req.PromptID, Sender: store.PromptSenderOwner, Client: req.Client,
	}
	if err := m.declineLocked(ctx, live); err != nil {
		return nil, err
	}
	if live.step {
		m.markSending(live)
		return func() error { return m.send(ctx, live, message) }, nil
	}
	m.mu.Lock()
	if _, quiet := m.quiet[live.id]; quiet {
		delete(m.quiet, live.id)
		m.wakeEvent()
	}
	m.mu.Unlock()
	wasTurning := m.markSending(live)
	return func() error { return m.sendSay(ctx, live, message, wasTurning) }, nil
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
	return m.declineLocked(ctx, live)
}

// declineLocked denies every request live has open, as declineOpen does. A request the session already answered or
// withdrew is skipped. When it denies any, it records the session working with no question. The caller holds live's order
// lock.
func (m *Manager) declineLocked(ctx context.Context, live *liveSession) error {
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
			return fmt.Errorf("decline request %s: %w", requestID, err)
		}
		m.closeRequest(live, requestID)
		declined = true
	}
	if !declined {
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

// Deliver sends req.Text to the headless session req.ID as its next prompt at once, stored with the sender event and
// req.PromptID. Whatever the session is doing takes it: a turn running, whoever started it, a background task listed,
// or an idle session. Unlike Say, it never answers, denies or declines a request the session has open. A live session
// that has a request open returns ErrSessionBusy and stores and sends nothing, so the caller delivers the text once the
// request is answered. A live session that the owner interrupted and has said nothing to since returns ErrCallerQuiet
// and stores and sends nothing, so the caller delivers the text once the owner has spoken. A session that is not live is
// resumed from its row, as Say resumes one, unless its row is stopped, which returns ErrCallerStopped, or the owner
// interrupted it and has said nothing to it since, as when the idle check stopped it after his Interrupt, which returns
// ErrCallerQuiet and resumes nothing. A step session returns an error naming it. A non-empty req.PromptID that an
// engine_prompt of the session already carries makes Deliver return nil and do nothing else, also after a restart. It
// returns ErrEmptyPrompt for empty text. A non-empty req.PromptID runs as one attempt per session and prompt id, as
// Say's does (see keyed).
func (m *Manager) Deliver(ctx context.Context, req DeliverRequest) error {
	defer m.calls.enter()()
	return m.deliverKeyed(ctx, req, m.deliverEvent)
}

// DeliverCron sends req.Text to the headless session req.ID as its next prompt, stored with the sender cron and
// req.PromptID, for a cron fire the session's jobs owe. It shares Deliver's handling of empty text, its keyed attempt
// and its prompt-id check, and like Deliver it never answers, denies or declines a request the session has open. A live
// session is claimed only when no turn runs, no request is open and it has no hand-off, so a running background task
// does not hold it: a session that is busy returns ErrSessionBusy and stores and sends nothing. A live session that the
// owner interrupted and has said nothing to since returns ErrCallerQuiet. A session that is not live is resumed from its
// row, unless its row is stopped, which returns ErrCallerStopped, failed, which returns ErrCallerFailed, or the owner
// interrupted it and has said nothing to it since, which returns ErrCallerQuiet; each of those starts no process. A step
// session returns an error naming it.
func (m *Manager) DeliverCron(ctx context.Context, req DeliverRequest) error {
	defer m.calls.enter()()
	return m.deliverKeyed(ctx, req, m.deliverCron)
}

// deliverKeyed is the delivery that Deliver and DeliverCron share. It returns ErrEmptyPrompt for empty text. A
// non-empty req.PromptID that an engine_prompt of the session already carries makes it return nil and do nothing else,
// and any other non-empty req.PromptID runs as one attempt per session and prompt id (see keyed). deliver sends req.
func (m *Manager) deliverKeyed(
	ctx context.Context, req DeliverRequest, deliver func(context.Context, DeliverRequest) error,
) error {
	if req.Text == "" {
		return ErrEmptyPrompt
	}
	if req.PromptID == "" {
		return deliver(ctx, req)
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
			return "", deliver(ctx, req)
		},
		func(ctx context.Context) bool {
			sent, err := m.store.EnginePromptSent(ctx, req.ID, req.PromptID)
			return sent || err != nil
		},
	)
	return err
}

// deliverEvent sends req as Deliver does, without the check of its prompt id, as a caller event's prompt. The event is
// claimed and written at once (see claimFree), so only a session with a request open or quiet from the owner's interrupt
// is left as it is.
func (m *Manager) deliverEvent(ctx context.Context, req DeliverRequest) error {
	return m.deliverTo(ctx, req, store.PromptSenderEvent, m.claimFree, m.admitEvent)
}

// deliverCron sends req as DeliverCron does, without the check of its prompt id, as a cron fire's prompt. The session
// is claimed only when no turn runs, no request is open and it has no hand-off (see claimCron).
func (m *Manager) deliverCron(ctx context.Context, req DeliverRequest) error {
	return m.deliverTo(ctx, req, store.PromptSenderCron, m.claimCron, m.admitCron)
}

// deliverTo sends req as a prompt stored with sender, through the send that claim returns for the session. A session
// that is not live is resumed from its row first, and admit sees that row before the process starts (see
// resumeSession), so a stopped or quiet session is left as it is, and so is a session that claim refuses.
func (m *Manager) deliverTo(
	ctx context.Context,
	req DeliverRequest,
	sender string,
	claim func(ctx context.Context, id string, message store.EnginePrompt) (func() error, error),
	admit func(store.EngineRow) error,
) error {
	message := store.EnginePrompt{ID: req.ID, Text: req.Text, PromptID: req.PromptID, Sender: sender}
	send, err := claim(ctx, req.ID, message)
	if errors.Is(err, ErrNoSession) {
		if _, err = m.resumeSession(ctx, req.ID, admit); err == nil {
			send, err = claim(ctx, req.ID, message)
		}
	}
	if err != nil {
		return fmt.Errorf("deliver to session %s: %w", req.ID, err)
	}
	return send()
}

// admitEvent returns ErrCallerStopped for a row the owner's Stop recorded stopped, and whatever admitQuiet returns
// otherwise, so a delivery resumes nothing for either.
func (m *Manager) admitEvent(row store.EngineRow) error {
	if row.State == stateStopped {
		return ErrCallerStopped
	}
	return m.admitQuiet(row.ID)
}

// admitCron returns ErrCallerStopped for a row the owner's Stop recorded stopped, ErrCallerFailed for a failed row, and
// whatever admitQuiet returns otherwise, so a cron fire resumes nothing for any of them.
func (m *Manager) admitCron(row store.EngineRow) error {
	switch row.State {
	case stateStopped:
		return ErrCallerStopped
	case stateFailed:
		return ErrCallerFailed
	}
	return m.admitQuiet(row.ID)
}

// admitQuiet returns ErrCallerQuiet for a session the owner's Interrupt left quiet (see Manager.quiet), and nil for any
// other session.
func (m *Manager) admitQuiet(id string) error {
	m.mu.Lock()
	_, quiet := m.quiet[id]
	m.mu.Unlock()
	if quiet {
		return ErrCallerQuiet
	}
	return nil
}

// claimFree returns the send that writes message, a caller event, to the live session id at once as its next prompt. The
// check is made under the session's order lock, as decide makes a Say's, so it is made against the turn as the pump leaves
// it. A turn running, whoever started it, and a background task listed do not refuse. The send is begun by markSending
// and ends in sendSay, which restores whether a turn ran when its record fails. It returns ErrNoSession when no session
// with that id is live, or when it is closing or its exit is recorded, ErrSessionBusy when a request is open,
// ErrCallerQuiet when the owner's Interrupt left it quiet, and an error naming it when it is a step.
func (m *Manager) claimFree(ctx context.Context, id string, message store.EnginePrompt) (func() error, error) {
	m.mu.Lock()
	live, ok := m.sessions[id]
	m.mu.Unlock()
	if !ok {
		return nil, ErrNoSession
	}
	live.order.Lock()
	defer live.order.Unlock()
	if err := m.eventRefusal(live); err != nil {
		return nil, err
	}
	wasTurning := m.markSending(live)
	return func() error { return m.sendSay(ctx, live, message, wasTurning) }, nil
}

// eventRefusal returns the error that refuses live a caller event, or nil when it takes one: ErrNoSession when live is no
// longer the session the manager runs, or is closing or exited, an error naming it when it is a step, ErrSessionBusy when a
// request is open, and ErrCallerQuiet when the owner's Interrupt left it quiet.
func (m *Manager) eventRefusal(live *liveSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[live.id] != live || live.closing || live.exited {
		return ErrNoSession
	}
	if live.step {
		return fmt.Errorf("session %s is a step session, which takes no event", live.id)
	}
	if len(live.requests) > 0 {
		return ErrSessionBusy
	}
	if _, quiet := m.quiet[live.id]; quiet {
		return ErrCallerQuiet
	}
	return nil
}

// claimCron returns the send that writes message, a cron fire, to the live session id as its next prompt, with its turn
// marked running and a send begun, as markSending does, but only while it holds no turn, request or hand-off, so a
// running background task does not hold it (see busyForCron), and the owner's Interrupt has not left it quiet. The check
// and the claim are one step under the lock, so a delivery that claims the session leaves every other cron fire to find
// it busy. The send ends in send. It returns ErrNoSession when no session with that id is live, or when it is closing or
// its exit is recorded, ErrSessionBusy when it holds a turn, request or hand-off, ErrCallerQuiet when the owner's
// Interrupt left it quiet, and an error naming it when it is a step.
func (m *Manager) claimCron(ctx context.Context, id string, message store.EnginePrompt) (func() error, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	live, ok := m.sessions[id]
	if !ok || live.closing || live.exited {
		return nil, ErrNoSession
	}
	if live.step {
		return nil, fmt.Errorf("session %s is a step session, which takes no cron fire", id)
	}
	if live.busyForCron() {
		return nil, ErrSessionBusy
	}
	if _, quiet := m.quiet[id]; quiet {
		return nil, ErrCallerQuiet
	}
	live.turning = true
	live.sending++
	m.updateIdleLocked(live)
	return func() error { return m.send(ctx, live, message) }, nil
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

// sendSay writes message, an owner's message to the live session that is not a step, as its next prompt at once, then
// sends its text (see record and transmit). wasTurning is whether a turn ran before the message, which a failed record
// leaves as it was: a turn that runs still runs, and an idle session stays idle. The send ends when sendSay returns (see
// endSend).
func (m *Manager) sendSay(ctx context.Context, live *liveSession, message store.EnginePrompt, wasTurning bool) error {
	defer m.endSend(live)
	writeID, err := m.record(ctx, live, message)
	if err != nil {
		m.setTurning(live, wasTurning)
		return err
	}
	if err = m.transmit(live, message.Text, writeID); err != nil {
		return m.failSend(ctx, live, message, err)
	}
	return nil
}

// record stores prompt as the next prompt of live, with the working state and the ops with it, in one write, and returns the
// write id the prompt is sent under. The prompt counts as sent from before the write (see session.Session.ExpectPrompt). An
// owner's prompt to a session that is not a step is stored queued too (see store.EnginePromptState), since claude has not
// yet taken it in. A write that fails stores nothing, and the caller settles the turn. A step sends no notice, and an engine
// session's owner is told the session changed.
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
	ops := []store.EngineOp{prompt, store.EngineSet{ID: id, Changes: store.EngineChanges{State: &working}}}
	if !live.step && prompt.Sender == store.PromptSenderOwner {
		ops = append(ops, store.EnginePromptState{ID: id, WriteID: writeID, State: store.PromptQueued})
	}
	ops = append(ops, with...)
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
// respawned first from its rows (see respawnStep). The prompt is recorded with its PromptID, sender and round, and a failed
// send records the step failed without a notice. It returns an error wrapping ErrNoSession when id is not a step row, and
// ErrEmptyPrompt for empty text.
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
// old holder's record or directory. Then it takes id out of the drained set, refusing when the manager is closed (see
// claimRespawn), before it reads the rows. A directory that is gone, an agent that no longer
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
	if err := m.claimRespawn(id); err != nil {
		return nil, err
	}
	row, step, err := m.stepLaunch(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = checkDir(row.Dir); err != nil {
		return nil, errors.Join(err, m.writeFailed(ctx, id, err))
	}
	session := StepSession{ID: id, Dir: row.Dir, Agent: step.Agent, Agents: step.Agents}
	if err = m.resolveStepAgent(ctx, session); err != nil {
		return nil, errors.Join(err, m.writeFailed(ctx, id, err))
	}
	m.mu.Lock()
	observer := m.stepObservers[id]
	m.mu.Unlock()
	live, err := m.spawnLive(ctx, id, row.Dir, spawnSpec{claudeSession: row.ClaudeSession, agent: step.Agent,
		agents: step.Agents, schema: step.Schema, step: true, observer: observer})
	if err != nil {
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
	return m.interruptLive(ctx, live, live.sess.Interrupt)
}

// OwnerInterrupt is the owner's Interrupt of the live session id (see Interrupt), which also records it, and cancels the
// owner's messages Claude has queued. The quiet is set, and the interrupt is recorded as an engine_interrupt that says
// whether a turn runs (see recordOwnerInterrupt). A turn that runs is then marked as the owner's, so its end is recorded as
// ownerInterrupted. The interrupt asks Claude to cancel its queue as well (cancel_queued) and waits for Claude's reply,
// which lists the write ids it cancelled. It returns the texts of the owner's prompts among them, in send order, so the
// owner can take them back. A caller event's text is never among them. Nothing is stored for the cancellation: Claude's
// cancelled lifecycle lines make each cancelled prompt answered (see the recorder). When the interrupt fails, it is
// recorded as not turning, the quiet stays, and the error is returned. A failed read of the cancelled prompts returns its
// error, though the interrupt was sent. It returns an error wrapping ErrNoSession when id is not live. The caller holds no
// lock, since the reply reaches the wait through the session's reader.
func (m *Manager) OwnerInterrupt(ctx context.Context, id string) ([]string, error) {
	defer m.calls.enter()()
	live, err := m.live(id)
	if err != nil {
		return nil, fmt.Errorf("interrupt session %s: %w", id, err)
	}
	m.mu.Lock()
	m.quiet[id] = struct{}{}
	m.mu.Unlock()
	if err = m.recordOwnerInterrupt(ctx, live); err != nil {
		return nil, fmt.Errorf("interrupt session %s: %w", id, err)
	}
	var cancelled []string
	err = m.interruptLive(ctx, live, func() error {
		var sendErr error
		cancelled, sendErr = live.sess.InterruptCancelQueued(ctx)
		if sendErr != nil {
			return fmt.Errorf("send interrupt: %w", sendErr)
		}
		return nil
	})
	if err != nil {
		m.recordFailedInterrupt(ctx, live)
		return nil, err
	}
	return m.ownerTexts(ctx, id, cancelled)
}

// Decline is the owner's decline of the open question or permission request requestID of the live session id. It
// returns an error wrapping ErrNoSession when id is not live. It returns an error wrapping ErrQuestionNotOpen, naming
// requestID, when requestID is empty or is not an open request of the session, and then sends and records nothing.
// Otherwise it is OwnerInterrupt, which denies every open request, ends the turn and leaves the session quiet. Once
// OwnerInterrupt returns with no error, the session is reported seen through the notifier at the manager's time, which
// ends its pending notice, and Decline returns OwnerInterrupt's texts. A failed OwnerInterrupt reports nothing.
func (m *Manager) Decline(ctx context.Context, id, requestID string) ([]string, error) {
	defer m.calls.enter()()
	live, err := m.live(id)
	if err != nil {
		return nil, fmt.Errorf("decline session %s: %w", id, err)
	}
	m.mu.Lock()
	_, open := live.requests[requestID]
	m.mu.Unlock()
	if requestID == "" || !open {
		return nil, fmt.Errorf("decline request %q of session %s: %w", requestID, id, ErrQuestionNotOpen)
	}
	texts, err := m.OwnerInterrupt(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.notifier != nil {
		m.notifier.Seen(ctx, id, m.now())
	}
	return texts, nil
}

// ownerTexts returns the texts of the owner's prompts of session id among the write ids cancelled, in the order they were
// sent. A caller event's prompt is left out. It returns no texts when no write id is one of those prompts.
func (m *Manager) ownerTexts(ctx context.Context, id string, cancelled []string) ([]string, error) {
	prompts, err := m.store.PromptsOfWrites(ctx, id, cancelled)
	if err != nil {
		return nil, fmt.Errorf("read the cancelled prompts of session %s: %w", id, err)
	}
	var texts []string
	for _, prompt := range prompts {
		if prompt.Sender == store.PromptSenderOwner {
			texts = append(texts, prompt.Text)
		}
	}
	return texts, nil
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

// interruptLive denies each request live has open, marks its running turn interrupted and sends the interrupt through send.
// Interrupt sends a plain interrupt and OwnerInterrupt one that cancels the queue; both share this. The caller holds no
// lock, since send may wait for Claude's reply.
func (m *Manager) interruptLive(ctx context.Context, live *liveSession, send func() error) error {
	if err := m.declineOpen(context.WithoutCancel(ctx), live); err != nil {
		return fmt.Errorf("interrupt session %s: %w", live.id, err)
	}
	m.setInterrupting(live, true)
	if m.interruptWritten != nil {
		m.interruptWritten(live.id)
	}
	if err := send(); err != nil {
		m.setInterrupting(live, false)
		return fmt.Errorf("interrupt session %s: %w", live.id, err)
	}
	return nil
}

// Withdraw takes back the owner prompt of session id that carries prompt id promptID from Claude's queue, and returns
// its text. An empty promptID returns no texts and sends nothing. A promptID that no owner prompt of the session
// carries returns an error wrapping ErrUnknownPrompt and sends nothing. It returns an error wrapping ErrNoSession when
// no engine row has id, and no texts when the session is not live. A live session's Claude cancels the prompt only
// while it is still queued, so the reply says whether it did: a cancelled prompt's text is returned, and a prompt
// Claude already took in returns no texts and no error. Withdraw stores nothing. The cancelled state Claude reports for
// the prompt is what makes the prompt answered, live and in a re-adopted session. It holds no lock while it waits for
// the reply, which reaches it through the session's reader, since the pump takes the session's order lock.
func (m *Manager) Withdraw(ctx context.Context, id, promptID string) ([]string, error) {
	defer m.calls.enter()()
	if _, err := m.engineRow(ctx, id); err != nil {
		return nil, fmt.Errorf("withdraw from session %s: %w", id, err)
	}
	if promptID == "" {
		return nil, nil
	}
	prompt, found, err := m.store.PromptWrite(ctx, id, promptID)
	if err != nil {
		return nil, fmt.Errorf("withdraw from session %s: %w", id, err)
	}
	if !found || prompt.Sender != store.PromptSenderOwner {
		return nil, fmt.Errorf("withdraw prompt %s from session %s: %w", promptID, id, ErrUnknownPrompt)
	}
	live, err := m.live(id)
	if errors.Is(err, ErrNoSession) {
		// A session that is not live has no queue to take the prompt back from.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("withdraw prompt %s from session %s: %w", promptID, id, err)
	}
	cancelled, err := live.sess.CancelQueued(ctx, prompt.WriteID)
	if err != nil {
		return nil, fmt.Errorf("withdraw prompt %s from session %s: %w", promptID, id, err)
	}
	if !cancelled {
		return nil, nil
	}
	return []string{prompt.Text}, nil
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
// end (see awaitStopping), then checks that the manager is open before it reads the directory, so a closed manager refuses
// the resume before anything is recorded. A directory that is missing or not a directory records the session failed and
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
// before that. A drained step stays in the drained set when it ends (see retireLocked). It returns an error wrapping ctx's
// error when ctx ends first.
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

// resume starts the process of row, an engine row that is not live, and registers it live. It checks that the manager is
// open first, then checks the row's directory. A failed check or spawn records the row failed.
func (m *Manager) resume(ctx context.Context, row store.EngineRow) (*liveSession, error) {
	if err := m.checkOpen(); err != nil {
		return nil, err
	}
	if err := checkDir(row.Dir); err != nil {
		return nil, errors.Join(err, m.recordFailed(ctx, row.ID, row.Dir, err))
	}
	live, err := m.spawnLive(ctx, row.ID, row.Dir, spawnSpec{claudeSession: row.ClaudeSession})
	if err != nil {
		return nil, errors.Join(err, m.recordFailed(ctx, row.ID, row.Dir, err))
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
// events. An error that returns here leaves nothing registered.
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
	live := &liveSession{id: id, dir: dir, sess: sess, rec: rec, done: make(chan struct{}), cancel: cancel,
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
	m.wakeEvent()
	return live, nil
}

// prime builds the state of live, a re-adopted session not yet registered, from re. Its turn runs when its row is
// working or waiting, and its background and awaited flags are the row's. Each replayed line goes through noteEvent and
// the recorder with the ops discarded, so the requests it leaves open, its unfinished text and its reply are those the
// replay gives. A waiting row with no request open goes working with no question. Each prompt the holder has not
// written is sent in order, marking the turn running. It returns the reply the pump starts from.
func (m *Manager) prime(ctx context.Context, live *liveSession, rec *recorder, re *readoption) (string, error) {
	row := re.row
	m.mu.Lock()
	live.turning = row.State == string(protocol.StateWorking) || row.State == string(protocol.StateWaiting)
	live.background = row.Background
	live.awaited = row.Awaited
	m.updateIdleLocked(live)
	m.mu.Unlock()
	reply := m.replay(live, rec, re.lines)
	if err := m.markWorking(ctx, live, row); err != nil {
		return "", err
	}
	if err := m.sendUnwritten(ctx, live, re.written); err != nil {
		return "", err
	}
	return reply, nil
}

// replay runs the replayed lines through noteEvent and rec, with rec's ops discarded, and returns the reply the lines
// leave: the text of the last finished main-conversation block after the last turn's end.
func (m *Manager) replay(live *liveSession, rec *recorder, lines []session.Line) string {
	reply := ""
	for _, line := range lines {
		for _, event := range line.Events {
			if !m.noteEvent(live, event, false).record {
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
	close(m.idleQuit)
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

// RunIdle stops the sessions that are idle for the idle timeout, until ctx ends. Each session's idle timer hands its
// deadline here (see armIdleLocked), and each deadline runs a check (see checkIdle), so no check runs between deadlines.
func (m *Manager) RunIdle(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.idleDue:
			m.checkIdle(ctx)
		}
	}
}

// idleDeadline is the function each session's idle timer calls at its deadline. It hands the deadline to RunIdle, and
// returns without waiting once Close has begun, so a timer that fires then blocks on nothing.
func (m *Manager) idleDeadline() {
	select {
	case m.idleDue <- struct{}{}:
	case <-m.idleQuit:
	}
}

// checkIdle stops each live session that has been not busy for the idle timeout by the clock, and waits for its pump to
// end. A session is busy while a turn runs, a request is open, or a background task runs. The stop records
// nothing, so the session's row and events stay as they were. The session is taken out of the live set under the lock
// before it is stopped, and resumeMu is held through the stop, so a Say that finds no live session waits, then resumes it
// from its row. A session whose deadline timer fires before it is due, or fires after it turned busy, is not stopped.
func (m *Manager) checkIdle(ctx context.Context) {
	defer m.calls.enter()()
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	now := m.now()
	m.mu.Lock()
	m.idleChecks++
	var stopping []*liveSession
	for id, live := range m.sessions {
		if live.closing || live.busy() || now.Sub(live.idleSince) < defaultIdleAfter {
			continue
		}
		live.idleStopped = true
		delete(m.sessions, id)
		m.disarmIdleLocked(live)
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

// busyForCron reports whether live has a turn running, an open request or a hand-off the next prompt has not yet
// answered. Unlike busy, a background task does not count, so a cron fire goes to a session that is only waiting on its
// tasks. The caller holds the lock.
func (live *liveSession) busyForCron() bool {
	return live.turning || live.returned || len(live.requests) > 0
}

// updateIdleLocked clears the idle stamp of live while it is busy, and stamps the time it became not busy. It disarms
// live's idle timer while it is busy. A registered session that is not busy has its idle timer armed for its deadline
// (see armIdleLocked). The caller holds the lock.
func (m *Manager) updateIdleLocked(live *liveSession) {
	if live.busy() {
		live.idleSince = time.Time{}
		m.disarmIdleLocked(live)
		return
	}
	if live.idleSince.IsZero() {
		live.idleSince = m.now()
	}
	if live.idleTimer == nil && m.sessions[live.id] == live {
		m.armIdleLocked(live)
	}
}

// armIdleLocked arms live's idle timer for its deadline, idleSince plus defaultIdleAfter, by the manager's clock. The
// timer calls idleDeadline when it fires. A deadline that has passed arms a timer that fires at once. The caller holds the
// lock.
func (m *Manager) armIdleLocked(live *liveSession) {
	live.idleTimer = m.afterFunc(live.idleSince.Add(defaultIdleAfter).Sub(m.now()), m.idleDeadline)
}

// disarmIdleLocked stops live's idle timer, if one is armed. A timer that has already fired cannot be stopped, so its
// deadline reaches RunIdle, and checkIdle then stops nothing for a session that is busy, not due or gone. The caller holds
// the lock.
func (m *Manager) disarmIdleLocked(live *liveSession) {
	if live.idleTimer != nil {
		live.idleTimer()
		live.idleTimer = nil
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

// markSending marks the turn of live running and a send begun, under one lock hold, and returns whether a turn ran before.
// A send whose record fails restores that (see sendSay). The send ends in send or sendSay.
func (m *Manager) markSending(live *liveSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	wasTurning := live.turning
	live.turning = true
	live.sending++
	m.updateIdleLocked(live)
	return wasTurning
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
	_, open := live.requests[requestID]
	delete(live.requests, requestID)
	m.updateIdleLocked(live)
	m.wakeIfNoneOpen(live, open)
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

// checkOpen refuses a start or resume once the manager is closed.
func (m *Manager) checkOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errManagerClosed
	}
	return nil
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
// prompt is not sent here. The process takes its settings from spawnSettings, and settings that cannot be read fail the
// spawn before claude runs. The launch of id is committed before the holder starts, so a commit that fails fails the
// spawn too.
func (m *Manager) spawn(ctx context.Context, id, dir string, sp spawnSpec) (*session.Session, error) {
	claude, err := exec.LookPath(m.claude)
	if err != nil {
		return nil, fmt.Errorf("find claude: %w", err)
	}
	settings, err := m.spawnSettings(dir, sp.step)
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	if err = m.writer.Commit(ctx, id, store.EngineLaunch{ID: id}); err != nil {
		return nil, fmt.Errorf("launch session %s: %w", id, err)
	}
	sess, err := session.Start(ctx, session.Config{
		Shell:          m.shell,
		Claude:         claude,
		Dir:            dir,
		PermissionMode: permissionMode,
		Settings:       settings,
		Resume:         sp.claudeSession,
		Model:          sp.model,
		Agent:          sp.agent,
		Agents:         sp.agents,
		Schema:         sp.schema,
		Name:           id,
		HolderScope:    m.holderScope,
		StateDir:       m.stateDir,
		Environ:        m.environ,
	})
	if err != nil {
		return nil, fmt.Errorf("spawn session %s: %w", id, err)
	}
	return sess, nil
}

// spawnSettings returns the --settings JSON of a spawn in dir. The stamp is looked up under the HOME entry of the
// manager's environment, which must be an absolute path. A session the owner talks to, a resume included, always takes
// stamp.HeadlessSettings: the stamp's env when one applies, and nothing otherwise, plus the PreToolUse hook that
// carries its cron tools to orrery. The orrery that runs the hook is holder.Binary(""), and one that cannot be resolved
// fails here, before claude runs. A step takes settings with no hook: the stamp's env when one applies, and
// claudeMdExcludes naming the user CLAUDE.md, so a step does not load the owner's own rules. Its path is read from
// m.environ, and a step whose path cannot be resolved fails here, before claude runs.
func (m *Manager) spawnSettings(dir string, step bool) (string, error) {
	home, err := stampHome(m.environ)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", dir, err)
	}
	found, err := stamp.Find(home, dir)
	if err != nil {
		return "", fmt.Errorf("find stamp for %s: %w", dir, err)
	}
	if !step {
		var orrery string
		if orrery, err = holder.Binary(""); err != nil {
			return "", fmt.Errorf("cron hook for %s: %w", dir, err)
		}
		return stamp.HeadlessSettings(found.Env, orrery), nil
	}
	memory, err := userMemoryOf(m.environ)
	if err != nil {
		return "", fmt.Errorf("step in %s: %w", dir, err)
	}
	return stamp.Settings(false, found.Env, []string{memory}), nil
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

// userMemoryOf returns the path of the user's CLAUDE.md that the claude running with environ loads: its HOME and
// CLAUDE_CONFIG_DIR entries, read as stamp.UserMemory reads them. It fails when that path cannot be resolved.
func userMemoryOf(environ []string) (string, error) {
	home, _ := environValue(environ, "HOME")
	configDir, _ := environValue(environ, "CLAUDE_CONFIG_DIR")
	memory, err := stamp.UserMemory(home, configDir)
	if err != nil {
		return "", fmt.Errorf("user memory: %w", err)
	}
	return memory, nil
}

// pump records the events of one running session, line by line and in order, until its lines close, and tells the owner
// of each event once it is recorded. reply is the reply text of the turn the session's earlier lines left, so the pump
// continues that turn. It is the only reader of the session's lines and the only user of its recorder. When it ends it
// removes the session's live entry, only while that entry is its own, and wakes the caller events and, for a session that
// is not a step, the cron hook. A retired step stays in the drained set, and a retired session leaves m.stopping when its
// stop has returned (see stopRetired).
func (m *Manager) pump(ctx context.Context, live *liveSession, rec *recorder, reply string) {
	defer m.pumps.Done()
	for line := range live.sess.Lines() {
		reply = m.recordLine(ctx, live, rec, line, reply)
	}
	m.mu.Lock()
	if m.sessions[live.id] == live {
		delete(m.sessions, live.id)
	}
	m.disarmIdleLocked(live)
	if !live.step || !live.retired {
		m.wakeEvent()
	}
	m.mu.Unlock()
	close(live.done)
	live.cancel()
	if !live.step {
		m.wakeCron("")
	}
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
// reply as of that event, and the interrupted, handed, followed, opened and idle results of noteEvent for it.
type recordedEvent struct {
	event       session.Event
	reply       string
	interrupted bool
	handed      bool
	followed    bool
	opened      bool
	idle        bool
}

// recordLine records one line of the running session in one write: the ops of all its events, then the line's consumed
// offset, so a crash stores the line wholly or not at all. A line that holds a turn's end waits until the store has
// it, and the session's replay-from is then set to the line's end. Once the write succeeds, the owner of each recorded
// event is told, in order, each with the reply as of that event. A failed write is logged and tells no owner. The
// observer is given every recorded event either way, as before. The line is recorded under the session's order lock, so
// the turn's state does not change under it. It returns the reply text of the main conversation so far. Once the write
// succeeds, a turn's end that leaves a session which is not a step with no turn running calls the cron hook for it.
func (m *Manager) recordLine(
	ctx context.Context, live *liveSession, rec *recorder, line session.Line, reply string,
) string {
	live.order.Lock()
	ops, recorded, reply := m.lineOps(ctx, live, rec, line, reply)
	written := m.storeLine(ctx, live, line, ops)
	live.order.Unlock()
	m.tellRecorded(ctx, live, recorded, written)
	// A turn's end that leaves the session idle wakes the cron hook once the end is stored, so a refused cron
	// fire is retried after the turn, not before it.
	if written && slices.ContainsFunc(recorded, func(r recordedEvent) bool { return r.idle }) {
		m.wakeCron(live.id)
	}
	return reply
}

// lineOps returns the store ops that record the events of line in order, the events the owner is told of, and the reply as
// of the line's end.
func (m *Manager) lineOps(
	ctx context.Context, live *liveSession, rec *recorder, line session.Line, reply string,
) ([]store.EngineOp, []recordedEvent, string) {
	var ops []store.EngineOp
	var recorded []recordedEvent
	for _, event := range line.Events {
		if request, ok := event.(session.Request); ok && live.step && !request.Refused && !request.Answered {
			m.answerStepRequest(ctx, live, request)
			continue
		}
		note := m.noteEvent(live, event, true)
		if !note.record {
			continue
		}
		if note.opened {
			ops = append(ops, rec.turnOpened()...)
		}
		ops = append(ops, eventOps(rec, event, note.followed, note.ownerInterrupted)...)
		if text, ok := mainText(event); ok {
			reply = text
		}
		recorded = append(recorded, recordedEvent{
			event: event, reply: reply, interrupted: note.interrupted, handed: note.handed, followed: note.followed,
			opened: note.opened, idle: note.idle,
		})
		if _, ended := event.(session.TurnEnded); ended {
			reply = ""
		}
	}
	return ops, recorded, reply
}

// storeLine stores the ops of one line of live in one write, with the line's consumed offset when it holds one, and reports
// whether the write succeeded. A line that holds a turn's end waits until the store has the ops, and the session's
// replay-from is then set to the line's end.
func (m *Manager) storeLine(ctx context.Context, live *liveSession, line session.Line, ops []store.EngineOp) bool {
	ended := false
	if !holdsExit(line) {
		var consumed store.EngineConsumed
		consumed, ended = consumedOf(live.id, line)
		ops = append(ops, consumed)
	}
	written := true
	if err := m.writeLine(ctx, live, ops, ended); err != nil {
		m.logger.WarnContext(ctx, "record session event", "session", live.id, "err", err)
		written = false
	}
	if written && ended {
		live.sess.SetReplayFrom(line.End)
	}
	return written
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
// observer, and whether another turn follows it; whether the event opens a turn that claude started by itself; and
// whether a turn's end leaves a session that is not a step with no turn running, for which the cron hook is called once
// the end is stored (see recordLine).
type noted struct {
	record, interrupted, ownerInterrupted, handed, followed, opened, idle bool
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
// which clears the mark for the next turn, and whether the turn is handed to the step's observer: a step's turn that
// ends with no background task live that runs a model (see awaitsTask), or whose structured return is the step's done
// value while one is live (see StepObserver.Done). A shell task, a Monitor's watch among them, holds no turn end back:
// the step's loop takes the return, and the step's stop ends what the session left running. The hand-off makes the step
// busy in the same lock hold. A pumped turn's end is followed by another turn when a prompt the session counts as sent
// is still unanswered after it, as its reader counted at that end or as the session counts now: claude runs a prompt
// sent during a turn as the turn after it. A prompt counts from before its record is stored (see
// session.Session.ExpectPrompt), so the send under way of the prompt this very turn answers follows nothing, and a send
// whose prompt the turn does not answer is still counted. A followed turn end keeps the turn running, hands nothing to
// the observer and retires nothing. A pumped event that opens a turn (see opensTurn) while no turn runs opens one
// claude started by itself, as a background task's end starts: the turn is marked running, and noteEvent reports it
// opened. A draining session whose turn ends is retired in the same lock hold (see retireLocked). The exit of a session
// that Close or a retirement is stopping is not recorded here: recordClosed records a closed session's state from its
// turn instead, and under a holder scope records nothing, and a retired session records nothing.
func (m *Manager) noteEvent(live *liveSession, event session.Event, pumping bool) noted {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.updateIdleLocked(live)
	opened := false
	if pumping && opensTurn(event) && !live.turning {
		live.turning = true
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
		_, open := live.requests[ev.RequestID]
		delete(live.requests, ev.RequestID)
		m.wakeIfNoneOpen(live, open)
	case session.BackgroundTasks:
		live.background = len(ev.Tasks) > 0
		live.awaited = awaitsTask(ev.Tasks)
	case session.TurnEnded:
		return m.noteTurnEndedLocked(live, ev, pumping)
	case session.Exited:
		live.turning = false
		live.ownerInterrupt = false
		clear(live.requests)
		live.background = false
		live.awaited = false
		if live.closing || live.idleStopped || live.retired {
			return noted{}
		}
		live.exited = true
	}
	return noted{record: true, opened: opened}
}

// noteTurnEndedLocked tracks the end of a turn of live, as noteEvent gives it. A turn's end that a prompt the session counts
// as sent still follows is followed: the turn stays running, since claude runs that prompt next. Any other turn's end ends
// the turn, hands a step's turn to its observer and retires a draining session. The caller holds the lock.
func (m *Manager) noteTurnEndedLocked(live *liveSession, ended session.TurnEnded, pumping bool) noted {
	hadOpen := len(live.requests) > 0
	clear(live.requests)
	m.wakeIfNoneOpen(live, hadOpen)
	interrupted := live.interrupting
	live.interrupting = false
	ownerInterrupted := live.ownerInterrupt
	live.ownerInterrupt = false
	if pumping && (ended.Unanswered > 0 || live.sess.UnansweredPrompts() > 0) {
		return noted{record: true, interrupted: interrupted, ownerInterrupted: ownerInterrupted, followed: true}
	}
	live.turning = false
	handed := false
	if live.observer != nil && (!live.awaited || live.observer.Done(ended)) {
		live.returned = true
		handed = true
	}
	m.retireLocked(live)
	return noted{
		record: true, interrupted: interrupted, ownerInterrupted: ownerInterrupted, handed: handed, idle: !live.step,
	}
}

// retireLocked takes live out of the live set and stops its process, when live is draining and not busy: no turn runs, no
// request is open and no background task runs, so the stop ends nothing the session is still doing. It also needs no send
// pending, and every prompt the session sent answered, so the stop leaves no prompt unanswered: a prompt claude runs as a
// following turn, or folds into the running turn, is answered on this holder before it stops. A background task still
// running at a turn's end keeps the session until a later turn ends with none, or until the idle stop takes it. A step's
// hand-off does not hold a draining step: its next Continue respawns it, so the step is retired at its hand-off. A retired
// step enters the drained set, m.kept, which its pump's end does not leave. Each step that can leave a draining session
// drained calls it under the lock: register, noteEvent at a turn's end, and endSend. The session leaves m.sessions and
// enters m.stopping in that lock hold, so no prompt reaches it afterwards. Its stop runs on its own goroutine, so the pump
// that calls this goes on recording the line it is on. The stop records nothing. awaitStopping waits for the pump's end and
// the stop, so a new holder never starts over this one's directory. The
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
	m.disarmIdleLocked(live)
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
	m.notifyNotice(ctx, Notice{Session: id, Dir: dir, State: state, Body: body})
}

// notifyNotice sends the owner the notice n. It sends nothing without a notifier.
func (m *Manager) notifyNotice(ctx context.Context, n Notice) {
	if m.notifier == nil {
		return
	}
	m.notifier.Notify(ctx, n)
}

// tellOwner tells the owner of recorded event of live: a notice when the event calls for one, and otherwise that the
// session changed when the event ends its pending notice. A step tells the owner nothing. A notice of an open request
// carries the request's options and id.
func (m *Manager) tellOwner(
	ctx context.Context, live *liveSession, event session.Event, reply string, interrupted bool,
) {
	if live.step {
		return
	}
	if state, body, notify := eventNotice(event, reply, interrupted); notify {
		options, request := requestChoices(event)
		m.notifyNotice(ctx, Notice{
			Session: live.id, Dir: live.dir, State: state, Body: body, Options: options, Request: request,
		})
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

// wakeCron calls the cron hook (see Config.CronWake) with session id, or with "" for an ended process. It runs the hook on
// its own goroutine, so the caller holds no lock while the hook runs and never waits on it. It does nothing without a
// hook.
func (m *Manager) wakeCron(id string) {
	if m.cronWake != nil {
		go m.cronWake(id)
	}
}

// wakeEvent calls the event hook (see Config.EventWake). The hook does not block, so the caller calls it where the
// change is made, under the locks the change holds. It does nothing without a hook.
func (m *Manager) wakeEvent() {
	if m.eventWake != nil {
		m.eventWake()
	}
}

// wakeIfNoneOpen calls the event hook when live has no request open after a request of it closed, which hadOpen says it
// had open before the change. The caller holds the lock.
func (m *Manager) wakeIfNoneOpen(live *liveSession, hadOpen bool) {
	if hadOpen && len(live.requests) == 0 {
		m.wakeEvent()
	}
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

// recordStopped waits for the pump of live to finish, then records live stopped, with its unfinished text removed, each
// background task still running recorded stopped, and each prompt claude had not started recorded discarded, unless its
// exit already recorded them. A stopped session's turn has ended, and a stopped process delivers no exit of its own.
// The stop tells the owner the session changed. It returns the error of the wait or of the write.
func (m *Manager) recordStopped(ctx context.Context, live *liveSession) error {
	if err := m.waitPump(ctx, live); err != nil {
		return err
	}
	if live.exited {
		return nil
	}
	stopped := stateStopped
	ops := append(live.rec.stoppedTasks(), store.EngineTextClear{ID: live.id}, store.EnginePromptsDiscard{ID: live.id},
		store.EngineSet{ID: live.id, Changes: store.EngineChanges{State: &stopped}})
	err := m.writer.Commit(ctx, live.id, ops...)
	if err != nil {
		return fmt.Errorf("record session %s stopped: %w", live.id, err)
	}
	if !live.step {
		m.changed(ctx, live.id)
	}
	return nil
}

// recordClosed waits for the pump of live to finish, then records live as Close leaves it: interrupted with the reason
// when its turn was running, and otherwise in the state its turn gave it. Either way its unfinished text is removed,
// each background task still running is recorded stopped, and each prompt claude had not started is recorded
// discarded. An interruption notifies the owner. A session whose exit already recorded its state is left alone. Under
// a holder scope Close only detaches the session, so nothing is recorded for it. It returns the error of the wait or of
// the write.
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
	ops := append(live.rec.stoppedTasks(), store.EngineTextClear{ID: live.id}, store.EnginePromptsDiscard{ID: live.id})
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
