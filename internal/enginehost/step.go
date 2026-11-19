package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
	"github.com/joshsymonds/orrery/internal/stamp"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrBadOwned reports a step whose owned paths are refused: an owned path that is absolute or reaches outside the
// repository, or a writing step that names no owned path and is not Unowned.
var ErrBadOwned = errors.New("step owned paths are refused")

// ErrBadGate reports a step whose gate is refused: a named gate that does not resolve, or a gates file that is not
// absolute or is set without a name.
var ErrBadGate = errors.New("step gate is refused")

// ErrNoCaller reports a step start whose caller resolves to no orrery session, or a call that carries no caller header,
// so the step's end would have nowhere to go.
var ErrNoCaller = errors.New("step caller resolves to no orrery session, so the step's end would have nowhere to go")

// ErrBadWatchTick reports a StepsConfig whose WatchTick is negative.
var ErrBadWatchTick = errors.New("step watch tick must not be negative")

// ErrBadStepID reports a StepStart whose ID is not exactly 32 lowercase hex digits.
var ErrBadStepID = errors.New("step id must be 32 lowercase hex digits")

// ErrStepExists reports a StepStart whose ID is already stored, or is held by a start still in flight.
var ErrStepExists = errors.New("step id is already in use")

// stepDefaultDone is the status a step's done return carries when its StepStart names none.
const stepDefaultDone = "done"

// stepDefaultGateLimit is the gate limit a step is stored with when it names no gate.
const stepDefaultGateLimit = 30 * time.Minute

// stepDefaultModelLimit is how long a step's model time may run when its StepStart names no limit.
const stepDefaultModelLimit = 60 * time.Minute

// stepDefaultWatchTick is how often a started step's watch is checked when StepsConfig names no tick.
const stepDefaultWatchTick = 5 * time.Second

// StepStart describes a step to start. A writing step (ReadOnly false) runs in Workspace, a linked worktree, and its
// done return is checked against its commit and Owned paths and, when it names one, its gate. A writing step needs at
// least one Owned path unless it is Unowned, or the start is refused with ErrBadOwned. A read-only step runs in
// a detached worktree at Revision, which is removed when the step ends. Revision names the start commit and empty means
// HEAD. DoneValue empty means "done", and ModelLimit zero means 60 minutes, the model-time limit the step is stored
// with. Epic and Task are optional labels the step is stored with; a step with an empty Epic is never listed by List.
// Caller is the session or pane that started the step, which the step is stored with (see store.StepAdd). A non-zero
// Caller that names no sessions row is refused with ErrNoCaller, and a zero Caller is an in-process start.
//
// ID, when set, is the step's id in place of a fresh one. It must be 32 lowercase hex digits, the form a fresh id has,
// or the start is refused with ErrBadStepID. A start under an id that a stored step already has, or that another start
// is running under, is refused with ErrStepExists. A caller derives ID so it can find the step again by that id. It is
// Go-only: the Steps proto does not carry it, so only an in-process caller sets it.
//
// GateName names a gate to resolve once at start, from GatesFile when it is set and else from the project file for
// Workspace. GatesFile must be absolute and needs GateName. A start without GateName runs no gate. A named gate takes
// its argv and limit from its definition, and each pool its needs name must be declared by a pool file for Workspace.
// A named agent gate instead runs its agent as a read-only step with the verdict schema and the done value "done", and
// its prompt is built from Prompt. Such a start is refused unless it is read-only and leaves Agent, Schema and
// DoneValue empty.
//
// Agents, when set, is the JSON object of inline agent definitions the step's claude is started with as its --agents
// argument, and Agent may name one of them. Agent resolves when Agents defines it. The step stores Agents with its row,
// and a respawn of the step passes it again. Agents that is not a JSON object whose every member is a JSON object is
// refused with an error matching ErrBadAgents, and nothing is stored.
//
// AgentGate, when set, is an agent gate given as data in place of a named gate, and the step runs it as a named agent
// gate does, without reading any gates or project file. Such a start is refused unless it is read-only and leaves
// GateName, GatesFile, Agent, Schema and DoneValue empty. It may carry Agents, which defines the gate's agent inline.
// The gate needs a name, an agent, at least one assertion, a limit of at least a second, and no argv or needs. The
// step is stored with the gate's name and the source "data".
//
// CommandGate, when set, is a command gate given as data in place of a named gate, and a writing step runs it as a
// named command gate does, with no gate resolved by name and no gates file read. Such a start is refused unless it is
// writing and leaves AgentGate, GateName and GatesFile empty. The gate needs a name, an argv, a limit of at least a
// second, and no agent or assertions. Its needs are granted from the pools the workspace's pool files declare, and each
// must be declared by one. The step is stored with the gate's name and the source "data".
//
// Unowned, set on a writing step, skips the owned check on each done return: the step may change any path, and its
// head, ancestry and clean checks and its gate run as they do for an owned step. The row stores it, so a re-adopted
// step skips the check too.
//
// ReturnCheck, when set, runs on every schema-valid return of the step. Its failure lines are stored as the failed
// check named return and sent back to the session, and a step with a ReturnCheck is never judged by Haiku. It is
// Go-only: the Steps proto does not carry it, so only an in-process caller sets it.
type StepStart struct {
	ID          string
	Agent       string
	Agents      string
	Prompt      string
	Schema      string
	Workspace   string
	Revision    string
	ReadOnly    bool
	Owned       []string
	Unowned     bool
	GateName    string
	GatesFile   string
	ModelLimit  time.Duration
	DoneValue   string
	Epic        string
	Task        string
	Caller      store.EpicCaller
	AgentGate   *gates.Gate
	CommandGate *gates.Gate
	ReturnCheck func(value json.RawMessage) []string
}

// StepStore reads the step rows and listings Steps reports, the turn ends and finished blocks their sessions stored and
// the rate limits their sessions were rejected with, the running steps a caller started, the listings of the steps a
// caller started, and the sessions row a caller names, which Start checks a caller against before it stores a step.
// *store.Store satisfies it.
type StepStore interface {
	Step(ctx context.Context, id string) (store.StepRow, error)
	OpenSteps(ctx context.Context) ([]store.StepRow, error)
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
	StepListings(ctx context.Context, epic, task string) ([]store.StepListing, error)
	RunningStepListings(ctx context.Context, epic, task string) ([]store.StepListing, map[string]int, error)
	StepTurn(ctx context.Context, id string) (store.StepTurnEnd, bool, error)
	StepBlocks(ctx context.Context, id string, limit int) ([]store.StepBlock, error)
	StepRateLimit(ctx context.Context, id string) (store.RateLimit, bool, error)
	RunningCallerSteps(ctx context.Context, caller string) ([]store.StepRow, error)
	CallerStepListings(ctx context.Context, caller, epic string) ([]store.StepListing, error)
	CallerSession(ctx context.Context, caller store.EpicCaller) (string, bool, error)
}

// StepsConfig configures Steps. Sessions starts and stops the step sessions, Store reads the step rows and Writer
// stores the ops of the step loops. Pools queues each named gate's run for its needs; a nil Pools fails a gate that
// runs. Environ is the environment gates run in, and the one a named gate is resolved under.
// Worktrees is the directory read-only steps' worktrees are added under, one per step id. A nil Logger discards. Haiku
// makes the monitor and judge calls of every started step, and nil makes none. Now is the clock each step's watch reads,
// time.Now when nil. WatchTick is how often each started step's watch is checked, five seconds when zero.
// GateStateDir is the state directory each gate's holder lives under, and a directory under os.TempDir when empty.
// GateScope is the systemd-run scope each gate's holder runs in, and empty runs it without one. GateScopeMemory is that
// scope's memory cap in bytes, and zero takes holder.DefaultScopeMemory. With GateScope set, Close
// leaves a running gate under its holder for a restarted Steps to re-adopt; without it, Close kills the gate.
// ReadoptCheck returns the return check a step row is re-adopted with: a return check is Go-only, so the row cannot hold
// it, and the caller that started the step supplies it again. A nil ReadoptCheck, or one that returns a nil check,
// rebuilds the run with none. An error leaves the row unregistered, so Reconcile ends it engine_restarted.
// ReadoptGate returns the agent gate an agent gate step row is re-adopted with. A gate definition is not stored with the
// row, so the caller rebuilds it from the row's stored data. A nil ReadoptGate, or one that returns a nil gate or a gate
// that names no agent or not the row's gate, leaves the row unregistered, and so does an error.
// Stored, when set, is called once after each end or hold a subscription is handed, once the row is stored, so a caller
// can wake to deliver it. A nil Stored does nothing.
// EpicWake, when set, is called with the epic of each stored end or hold whose step has an epic, after Stored, so a caller
// can wake that epic's flow. A nil EpicWake does nothing.
type StepsConfig struct {
	Sessions        stepSessions
	Store           StepStore
	Writer          *Writer
	Pools           PoolCommands
	Worktrees       string
	Environ         []string
	Logger          *slog.Logger
	Haiku           *Haiku
	Now             func() time.Time
	WatchTick       time.Duration
	GateStateDir    string
	GateScope       string
	GateScopeMemory int64
	ReadoptCheck    func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error)
	ReadoptGate     func(ctx context.Context, row store.StepRow) (*gates.Gate, error)
	Stored          func()
	EpicWake        func(epic string)
}

// StepAdopter is what Readopt registers each open step with, and what Reconcile asks whether the step is live. Manager
// satisfies it.
type StepAdopter interface {
	ExpectStep(id string, observer StepObserver)
	StepLive(id string) bool
}

// Steps starts steps and reports their rows. Each started step's loop runs under a context Steps owns: loops holds the
// cancel of every loop that has not returned, causes holds the same cancels with a cause, and runs holds its stepRun, all
// by step id. idle is broadcast whenever a loop is removed from loops. adopter is what Readopt registered the steps with,
// and adopted holds the run Readopt rebuilt for each open row until Reconcile starts or settles it. starting holds the
// caller-given id of each start in flight, so a second start under that id is refused until the first returns. stored is
// the StepsConfig Stored callback, epicWake is the StepsConfig EpicWake callback, and subs holds each subscription Subscribe
// registered and Close has not removed.
type Steps struct {
	sessions        stepSessions
	store           StepStore
	writer          *Writer
	pools           PoolCommands
	worktrees       string
	environ         []string
	logger          *slog.Logger
	haiku           *Haiku
	now             func() time.Time
	tick            time.Duration
	gateStateDir    string
	gateScope       string
	gateScopeMemory int64
	readoptCheck    func(ctx context.Context, row store.StepRow) (func(value json.RawMessage) []string, error)
	readoptGate     func(ctx context.Context, row store.StepRow) (*gates.Gate, error)
	stored          func()
	epicWake        func(epic string)

	mu       sync.Mutex
	loops    map[string]context.CancelFunc
	causes   map[string]context.CancelCauseFunc
	runs     map[string]*stepRun
	idle     *sync.Cond
	adopter  StepAdopter
	adopted  map[string]*stepRun
	starting map[string]bool
	subs     map[*StepSubscription]bool
	// calls counts the calls under way that can start or end work (see callsInFlight).
	calls callsInFlight
}

// NewSteps returns a Steps for cfg. A negative cfg.WatchTick returns an error matching ErrBadWatchTick and no Steps.
func NewSteps(cfg StepsConfig) (*Steps, error) {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tick := cfg.WatchTick
	if tick < 0 {
		return nil, fmt.Errorf("%w: %s", ErrBadWatchTick, tick)
	}
	if tick == 0 {
		tick = stepDefaultWatchTick
	}
	steps := &Steps{
		sessions:        cfg.Sessions,
		store:           cfg.Store,
		writer:          cfg.Writer,
		pools:           cfg.Pools,
		worktrees:       cfg.Worktrees,
		environ:         cfg.Environ,
		logger:          logger,
		haiku:           cfg.Haiku,
		now:             cfg.Now,
		tick:            tick,
		gateStateDir:    cfg.GateStateDir,
		gateScope:       cfg.GateScope,
		gateScopeMemory: cfg.GateScopeMemory,
		readoptCheck:    cfg.ReadoptCheck,
		readoptGate:     cfg.ReadoptGate,
		stored:          cfg.Stored,
		epicWake:        cfg.EpicWake,
		loops:           map[string]context.CancelFunc{},
		causes:          map[string]context.CancelCauseFunc{},
		runs:            map[string]*stepRun{},
		adopted:         map[string]*stepRun{},
		starting:        map[string]bool{},
		subs:            map[*StepSubscription]bool{},
	}
	steps.idle = sync.NewCond(&steps.mu)
	return steps, nil
}

// Start checks start and its caller, resolves its gate, which an agent gate turns into the step's agent, schema and
// prompt, and checks the schema. It then resolves its revision and starts the step under start.ID, or under a fresh id
// when it names none, and runs its loop. A refusal returns its error with an empty id and stores nothing; a read-only
// step's worktree is removed then too. A writing step runs in start.Workspace. A read-only step gets a detached
// worktree at its start commit under the worktrees directory, which is removed when the step ends. Its checkout is
// fingerprinted before its agent starts, and a git error there refuses the start the same way. A folder whose stamp
// cannot be read refuses the start the same way. The step's monitor and judge calls carry the env of the stamp in
// effect for its folder. Any other StartStep error, after the row is written, ends the step failed and returns its id
// with the error.
func (s *Steps) Start(ctx context.Context, start StepStart) (string, error) {
	defer s.calls.enter()()
	if err := s.checkStart(ctx, start); err != nil {
		return "", err
	}
	start, gate, err := s.resolveStart(ctx, start)
	if err != nil {
		return "", err
	}
	if !start.ReadOnly {
		err = checkWritingWorkspace(ctx, start.Workspace)
		if err != nil {
			return "", err
		}
	}
	revision := start.Revision
	if revision == "" {
		revision = "HEAD"
	}
	commit, err := stepGitResolve(ctx, start.Workspace, revision)
	if err != nil {
		return "", fmt.Errorf("resolve revision %q: %w", revision, err)
	}
	id, release, err := s.startID(ctx, start.ID)
	if err != nil {
		return "", err
	}
	defer release()

	doneValue := start.DoneValue
	if doneValue == "" {
		doneValue = stepDefaultDone
	}
	gateLimit := gate.limit
	if gateLimit == 0 {
		gateLimit = stepDefaultGateLimit
	}
	modelLimit := start.ModelLimit
	if modelLimit == 0 {
		modelLimit = stepDefaultModelLimit
	}
	add := store.StepAdd{
		ID: id, Agent: start.Agent, Agents: start.Agents, Prompt: start.Prompt, Schema: start.Schema,
		Workspace: start.Workspace, Revision: commit, DoneValue: doneValue, ReadOnly: start.ReadOnly,
		Owned: start.Owned, Unowned: start.Unowned, Gate: gate.argv, GateName: gate.name, GateSource: gate.source,
		GateNeeds: gate.needs, GateLimit: gateLimit, ModelLimit: modelLimit, Epic: start.Epic, Task: start.Task,
		Caller: start.Caller,
	}
	cfg := stepRunConfig{
		id: id, schema: start.Schema, doneValue: doneValue, sessions: s.sessions, store: s.store, writer: s.writer,
		logger: s.logger, writing: !start.ReadOnly, start: commit, owned: start.Owned, unowned: start.Unowned,
		gate:      gate.argv,
		gateLimit: gateLimit, gateName: gate.name, gateSource: gate.source, grant: gate.grant, pools: s.pools,
		environ: s.environ, prompt: start.Prompt, now: s.now, modelLimit: modelLimit, agentGate: gate.agent,
		stateDir: s.gateStateDir, scope: s.gateScope, scopeMemory: s.gateScopeMemory, returnCheck: start.ReturnCheck,
		publish: s.publish,
		wake:    s.stored,
	}
	step := StepSession{
		ID: id, Dir: start.Workspace, Prompt: start.Prompt, Agent: start.Agent, Agents: start.Agents,
		Schema: start.Schema, Epic: start.Epic,
	}
	if start.ReadOnly {
		if err = s.addReadOnlyStep(ctx, start, id, commit, &add, &cfg); err != nil {
			return s.refuseStart(ctx, id, cfg.cleanup, err)
		}
		step.Dir = cfg.dir
	} else {
		cfg.dir = start.Workspace
	}
	if err = s.bindHaiku(&cfg, step.Dir); err != nil {
		return s.refuseStart(ctx, id, cfg.cleanup, err)
	}

	run := newStepRun(cfg)
	step.Observer = run
	step.Ops = []store.EngineOp{add}
	if err = s.sessions.StartStep(ctx, step); err != nil {
		return s.startFailed(ctx, id, run, err)
	}
	s.runLoop(ctx, id, run)
	return id, nil
}

// addReadOnlyStep adds the detached worktree of the read-only step id at commit, under the worktrees directory. It records
// the checkout's state in add, and sets cfg to run in the worktree and to remove it when the step ends. cfg's cleanup is
// set only once the worktree exists, so a caller that refuses the start on the error removes exactly what was added.
func (s *Steps) addReadOnlyStep(
	ctx context.Context, start StepStart, id, commit string, add *store.StepAdd, cfg *stepRunConfig,
) error {
	wt := filepath.Join(s.worktrees, id)
	if err := addStepWorktree(ctx, start.Workspace, wt, commit); err != nil {
		return err
	}
	add.Worktree = wt
	cfg.cleanup = func(ctx context.Context) error {
		return removeStepWorktree(ctx, start.Workspace, wt)
	}
	cfg.dir = wt
	checkout, err := stepGitCheckout(ctx, wt)
	if err != nil {
		return fmt.Errorf("fingerprint worktree %s: %w", wt, err)
	}
	cfg.checkout = checkout
	add.CheckoutHead, add.CheckoutHash = checkout.head, checkout.hash
	return nil
}

// resolveStart resolves start's gate and returns start as the step runs it, with the gate. An agent gate sets start's
// agent, schema and prompt, and the schema is checked whatever set it. A refusal returns its error and no start.
func (s *Steps) resolveStart(ctx context.Context, start StepStart) (StepStart, stepGate, error) {
	gate, err := s.resolveStepGate(ctx, start)
	if err != nil {
		return StepStart{}, stepGate{}, err
	}
	start, err = agentGateStart(start, gate)
	if err != nil {
		return StepStart{}, stepGate{}, err
	}
	err = checkStepSchema(start.Schema)
	if err != nil {
		return StepStart{}, stepGate{}, err
	}
	return start, gate, nil
}

// startFailed handles a StartStep error for the step id run runs. A refusal stored nothing, so it removes the step's
// worktree and returns the refusal with an empty id. Any other error ends the step failed and returns its id with the
// error. The caller's ctx may have ended the start, so the removal and the failed end run without it.
func (s *Steps) startFailed(ctx context.Context, id string, run *stepRun, err error) (string, error) {
	var refused stepRefusedError
	if errors.As(err, &refused) {
		return s.refuseStart(ctx, id, run.cleanup, refused.err)
	}
	run.finish(context.WithoutCancel(ctx), stepLoopEndFailed, "", err.Error())
	return id, fmt.Errorf("start step %s: %w", id, err)
}

// refuseStart refuses a start that stored nothing. It runs cleanup, when there is one, to remove the step's worktree,
// without the caller's ctx so a canceled start still cleans up, and returns err with an empty id.
func (s *Steps) refuseStart(
	ctx context.Context, id string, cleanup func(context.Context) error, err error,
) (string, error) {
	if cleanup != nil {
		if cleanErr := cleanup(context.WithoutCancel(ctx)); cleanErr != nil {
			s.logger.ErrorContext(ctx, "clean up refused step", "step", id, "err", cleanErr)
		}
	}
	return "", err
}

// bindHaiku sets cfg's haiku to the caller a step whose folder is folder makes its monitor and judge calls with. With
// no Haiku it sets none. The stamp in effect for folder, found from the HOME entry of the Steps' environ, which must be
// an absolute path, sets the env of each call's --settings. With no stamp, the caller is the Steps' Haiku as it is.
func (s *Steps) bindHaiku(cfg *stepRunConfig, folder string) error {
	if s.haiku == nil {
		return nil
	}
	home, err := stampHome(s.environ)
	if err != nil {
		return fmt.Errorf("find step stamp: %w", err)
	}
	found, err := stamp.Find(home, folder)
	if err != nil {
		return fmt.Errorf("find step stamp: %w", err)
	}
	if found.Folder == "" {
		cfg.haiku = s.haiku
		return nil
	}
	cfg.haiku = s.haiku.withEnv(found.Env)
	return nil
}

// Status returns the step row with id. It returns an error matching store.ErrNoStep when no step has that id.
func (s *Steps) Status(ctx context.Context, id string) (store.StepRow, error) {
	row, err := s.store.Step(ctx, id)
	if err != nil {
		return store.StepRow{}, fmt.Errorf("read step %s: %w", id, err)
	}
	return row, nil
}

// List returns the labeled step rows narrowed to epic when it is not empty and to task when it is not empty, oldest
// first. A step started without an epic is never listed.
func (s *Steps) List(ctx context.Context, epic, task string) ([]store.StepRow, error) {
	rows, err := s.store.LabelledSteps(ctx, epic, task)
	if err != nil {
		return nil, fmt.Errorf("list labeled steps: %w", err)
	}
	return rows, nil
}

// Listings returns the listings of the labeled steps narrowed to epic when it is not empty and to task when it is not
// empty, oldest first. A step started without an epic is never listed.
func (s *Steps) Listings(ctx context.Context, epic, task string) ([]store.StepListing, error) {
	listings, err := s.store.StepListings(ctx, epic, task)
	if err != nil {
		return nil, fmt.Errorf("list labeled step listings: %w", err)
	}
	return listings, nil
}

// RunningListings returns the listings of the labeled steps that have not ended, narrowed as Listings narrows them,
// oldest first, and by epic how many of the narrowed labeled steps have ended. It reads no ended step's row.
func (s *Steps) RunningListings(ctx context.Context, epic, task string) ([]store.StepListing, map[string]int, error) {
	listings, ended, err := s.store.RunningStepListings(ctx, epic, task)
	if err != nil {
		return nil, nil, fmt.Errorf("list running step listings: %w", err)
	}
	return listings, ended, nil
}

// runLoop runs run in its own goroutine under a context derived from ctx's values, not its cancellation, so the loop
// outlives the Start call. The run's watch ticks come from a ticker at the Steps' tick, which stops when the loop
// returns. The context's cancel, with and without a cause, and run are kept in loops, causes and runs until the loop
// returns, and then they are removed and the cancel is called.
func (s *Steps) runLoop(ctx context.Context, id string, run *stepRun) {
	loop, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	ticker := time.NewTicker(s.tick)
	run.ticks = ticker.C
	s.mu.Lock()
	s.loops[id] = func() { cancel(nil) }
	s.causes[id] = cancel
	s.runs[id] = run
	s.mu.Unlock()
	go func() {
		defer s.endLoop(id)
		defer ticker.Stop()
		run.run(loop)
	}()
}

// endLoop removes the loop of id from loops, causes and runs, wakes any Close waiting on loops, and cancels the loop's
// context.
func (s *Steps) endLoop(id string) {
	s.mu.Lock()
	cancel := s.loops[id]
	delete(s.loops, id)
	delete(s.causes, id)
	delete(s.runs, id)
	s.idle.Broadcast()
	s.mu.Unlock()
	cancel()
}

// checkStepStart reports whether start's prompt, id, workspace and owned paths are usable, before anything is stored or
// any worktree is added. A writing step that is not Unowned needs at least one owned path, because an empty list would
// let it change nothing. Start checks the schema once the gate has set it.
func checkStepStart(start StepStart) error {
	if start.Prompt == "" {
		return ErrEmptyPrompt
	}
	if start.ID != "" && checkID(start.ID) != nil {
		return fmt.Errorf("%w: %q", ErrBadStepID, start.ID)
	}
	if err := checkDir(start.Workspace); err != nil {
		return err
	}
	if !start.ReadOnly && !start.Unowned && len(start.Owned) == 0 {
		return fmt.Errorf("%w: a writing step needs owned_paths", ErrBadOwned)
	}
	for _, path := range start.Owned {
		if !filepath.IsLocal(path) {
			return fmt.Errorf("%w: %q is not a relative path inside the repository", ErrBadOwned, path)
		}
	}
	return nil
}

// checkStart reports whether start may be started: checkStepStart must pass, and then checkCaller must allow its caller.
func (s *Steps) checkStart(ctx context.Context, start StepStart) error {
	if err := checkStepStart(start); err != nil {
		return err
	}
	return s.checkCaller(ctx, start.Caller)
}

// checkCaller reports whether caller may start a step. The zero caller is an in-process start and is always allowed. A
// caller that names a sessions row is allowed, and one that names no row is refused with an error matching ErrNoCaller,
// since the step's end would have nowhere to go. A read error is returned wrapped.
func (s *Steps) checkCaller(ctx context.Context, caller store.EpicCaller) error {
	if caller == (store.EpicCaller{}) {
		return nil
	}
	_, found, err := s.store.CallerSession(ctx, caller)
	if err != nil {
		return fmt.Errorf("resolve caller of step: %w", err)
	}
	if found {
		return nil
	}
	name := caller.Session
	if name == "" {
		name = fmt.Sprintf("server %q pane %s", caller.Server, caller.Pane)
	}
	return fmt.Errorf("%w: %s", ErrNoCaller, name)
}

// startID returns the id a start runs under and the release that frees it once Start returns. A start with no id gets a
// fresh one that nothing else holds. A start with an id holds it while it runs, so a second start under that id is
// refused with ErrStepExists while the first is in flight and before its row is stored. An id a stored step already has
// is refused the same way, and holds nothing. Any other store read error is returned wrapped, so errors.Is still matches
// it.
func (s *Steps) startID(ctx context.Context, id string) (string, func(), error) {
	if id == "" {
		fresh, err := newSessionID()
		return fresh, func() {}, err
	}
	s.mu.Lock()
	held := s.starting[id]
	s.starting[id] = true
	s.mu.Unlock()
	if held {
		return "", nil, fmt.Errorf("%w: %s", ErrStepExists, id)
	}
	release := func() {
		s.mu.Lock()
		delete(s.starting, id)
		s.mu.Unlock()
	}
	_, err := s.store.Step(ctx, id)
	if !errors.Is(err, store.ErrNoStep) {
		release()
		if err != nil {
			return "", nil, fmt.Errorf("read step %s: %w", id, err)
		}
		return "", nil, fmt.Errorf("%w: %s", ErrStepExists, id)
	}
	return id, release, nil
}

// stepGate is the gate a step runs: its argv and limit, with the name it was resolved from, the file that declares it,
// the pool members it holds and the grant a run of it queues. An agent gate has its definition in agent and no argv,
// needs or grant. A start that names no gate has no name, source, needs, grant or agent, and a zero limit.
type stepGate struct {
	argv   []string
	limit  time.Duration
	name   string
	source string
	needs  []store.StepNeed
	grant  grants.Request
	agent  *gates.Gate
}

// resolveStepGate returns the gate start runs. A CommandGate or an AgentGate is the gate as the start gives it, checked
// by dataCommandGate or dataAgentGate, and no file is read for it. A CommandGate is checked first, so it also refuses a
// start that sets an AgentGate. A start without GateName runs no gate, and GatesFile set without GateName is refused. A
// named gate is resolved once, from start.GatesFile or else the project file for start.Workspace. An agent gate returns
// here, before any pool file is read. Any other named gate has the pools every pool file for start.Workspace declares
// loaded with it. It is refused with an error matching ErrBadGate when GatesFile is not absolute, when the name does not
// resolve, when the pool files do not load, and when a need is for a pool no pool file declares. The resolved argv is
// used as the gate's definition gives it; nothing is looked up on PATH.
func (s *Steps) resolveStepGate(ctx context.Context, start StepStart) (stepGate, error) {
	if start.CommandGate != nil {
		return s.dataCommandGate(ctx, start)
	}
	if start.AgentGate != nil {
		return dataAgentGate(start)
	}
	if start.GateName == "" {
		if start.GatesFile != "" {
			return stepGate{}, fmt.Errorf("%w: gates file %s is set without a gate name", ErrBadGate, start.GatesFile)
		}
		return stepGate{}, nil
	}
	if start.GatesFile != "" && !filepath.IsAbs(start.GatesFile) {
		return stepGate{}, fmt.Errorf("%w: gates file %s is not an absolute path", ErrBadGate, start.GatesFile)
	}
	getenv := stepGetenv(s.environ)
	resolved, err := gates.Resolve(ctx, start.GateName, start.GatesFile, start.Workspace, getenv)
	if err != nil {
		return stepGate{}, fmt.Errorf("%w: %w", ErrBadGate, err)
	}
	if resolved.Agent != "" {
		return stepGate{
			name: start.GateName, source: resolved.Source, limit: resolved.Limit, agent: &resolved.Gate,
		}, nil
	}
	grant, err := stepGrant(ctx, start.Workspace, start.GateName, resolved.Argv, gateGrantNeeds(resolved.Needs), getenv)
	if err != nil {
		return stepGate{}, err
	}
	return stepGate{
		argv: resolved.Argv, limit: resolved.Limit, name: start.GateName, source: resolved.Source,
		needs: stepNeeds(resolved.Needs), grant: grant,
	}, nil
}

// stepGateSourceData is the source a step is stored with when its gate is given as data rather than read from a file.
const stepGateSourceData = "data"

// dataCommandGate returns the gate of start, whose CommandGate is set, and resolves no gate by name. It is refused with
// an error matching ErrBadGate when start also sets AgentGate, GateName or GatesFile, when start is read-only, and when
// the gate has no name, no argv, an agent or assertions, or a limit under a second. Its needs are granted from the pools
// the workspace's pool files declare, as a named gate's are, and a need for a pool no pool file declares is refused.
func (s *Steps) dataCommandGate(ctx context.Context, start StepStart) (stepGate, error) {
	gate := *start.CommandGate
	switch {
	case start.AgentGate != nil || start.GateName != "" || start.GatesFile != "":
		return stepGate{}, fmt.Errorf(
			"%w: command gate %s is given as data, so the start must leave AgentGate, GateName and GatesFile empty",
			ErrBadGate, gate.Name,
		)
	case start.ReadOnly:
		return stepGate{}, fmt.Errorf("%w: command gate %s runs only in a writing step", ErrBadGate, gate.Name)
	case gate.Name == "":
		return stepGate{}, fmt.Errorf("%w: command gate has no name", ErrBadGate)
	case len(gate.Argv) == 0:
		return stepGate{}, fmt.Errorf("%w: command gate %s has no argv", ErrBadGate, gate.Name)
	case gate.Agent != "" || len(gate.Assertions) > 0:
		return stepGate{}, fmt.Errorf("%w: command gate %s names an agent or assertions", ErrBadGate, gate.Name)
	case gate.Limit < time.Second:
		return stepGate{}, fmt.Errorf(
			"%w: command gate %s limit %s is under a second", ErrBadGate, gate.Name, gate.Limit,
		)
	}
	grant, err := stepGrant(ctx, start.Workspace, gate.Name, gate.Argv, gateGrantNeeds(gate.Needs),
		stepGetenv(s.environ))
	if err != nil {
		return stepGate{}, err
	}
	return stepGate{
		argv: gate.Argv, limit: gate.Limit, name: gate.Name, source: stepGateSourceData,
		needs: stepNeeds(gate.Needs), grant: grant,
	}, nil
}

// dataAgentGate returns the gate of start, whose AgentGate is set, and reads no file. It is refused with an error
// matching ErrBadGate when start also names a gate or a gates file, and when the gate has no name, no agent, no
// assertion, a limit under a second, or an argv or needs. The file parser refuses each of those for an agent gate, and
// the run would otherwise drop argv or needs, or end the step overtime at its first tick.
func dataAgentGate(start StepStart) (stepGate, error) {
	gate := *start.AgentGate
	switch {
	case start.GateName != "" || start.GatesFile != "":
		return stepGate{}, fmt.Errorf(
			"%w: agent gate %s is given as data, so the start must leave GateName and GatesFile empty",
			ErrBadGate, gate.Name,
		)
	case gate.Name == "":
		return stepGate{}, fmt.Errorf("%w: agent gate has no name", ErrBadGate)
	case gate.Agent == "":
		return stepGate{}, fmt.Errorf("%w: agent gate %s names no agent", ErrBadGate, gate.Name)
	case len(gate.Assertions) == 0:
		return stepGate{}, fmt.Errorf("%w: agent gate %s has no assertion", ErrBadGate, gate.Name)
	case len(gate.Argv) > 0 || len(gate.Needs) > 0:
		return stepGate{}, fmt.Errorf("%w: agent gate %s takes no argv or needs", ErrBadGate, gate.Name)
	case gate.Limit < time.Second:
		return stepGate{}, fmt.Errorf(
			"%w: agent gate %s limit %s is under a second", ErrBadGate, gate.Name, gate.Limit,
		)
	}
	return stepGate{name: gate.Name, source: stepGateSourceData, limit: gate.Limit, agent: &gate}, nil
}

// stepGrant returns the grant request a gate runs under in workspace: the project and the pool definitions every pool file
// for workspace declares, with needs and argv as the command. It is refused with an error matching ErrBadGate when the pool
// files do not load, or when a need is for a pool no pool file declares, which the error names gateName for.
func stepGrant(
	ctx context.Context, workspace, gateName string, argv []string, needs []grants.Need, getenv func(string) string,
) (grants.Request, error) {
	project, defs, err := pools.Load(ctx, workspace, getenv)
	if err != nil {
		return grants.Request{}, fmt.Errorf("%w: %w", ErrBadGate, err)
	}
	for _, need := range needs {
		if !slices.ContainsFunc(defs, func(p pools.Pool) bool { return p.Name == need.Pool }) {
			return grants.Request{}, fmt.Errorf(
				"%w: gate %s needs pool %s, which no pool file declares", ErrBadGate, gateName, need.Pool,
			)
		}
	}
	return grants.Request{Project: project, Pools: defs, Needs: needs, ClientPID: os.Getpid(), Command: argv}, nil
}

// agentGateStart returns start as the step of gate runs it when gate is an agent gate: the gate's agent, the verdict
// schema, the done value "done" and the prompt the gate builds from start's prompt. A start for any other gate is
// returned unchanged. An agent gate's start is refused with an error matching ErrBadGate when it is a writing start, or
// when it names an agent, schema or done value, since the gate supplies them.
func agentGateStart(start StepStart, gate stepGate) (StepStart, error) {
	if gate.agent == nil {
		return start, nil
	}
	if !start.ReadOnly {
		return StepStart{}, fmt.Errorf("%w: agent gate %s runs only in a read-only step", ErrBadGate, gate.name)
	}
	if start.Agent != "" || start.Schema != "" || start.DoneValue != "" {
		return StepStart{}, fmt.Errorf(
			"%w: agent gate %s supplies the agent, schema and done value, so the start must leave them empty",
			ErrBadGate, gate.name,
		)
	}
	start.Agent = gate.agent.Agent
	start.Schema = gates.VerdictSchema
	start.DoneValue = stepDefaultDone
	start.Prompt = gates.Prompt(*gate.agent, start.Prompt)
	return start, nil
}

// stepNeeds returns needs as the store records them.
func stepNeeds(needs []gates.Need) []store.StepNeed {
	out := make([]store.StepNeed, 0, len(needs))
	for _, need := range needs {
		out = append(out, store.StepNeed{Pool: need.Pool, Count: need.Count})
	}
	return out
}

// gateGrantNeeds returns needs as the grants a gate's run requests them.
func gateGrantNeeds(needs []gates.Need) []grants.Need {
	out := make([]grants.Need, 0, len(needs))
	for _, need := range needs {
		out = append(out, grants.Need{Pool: need.Pool, Count: need.Count})
	}
	return out
}

// storedGrantNeeds returns the needs a step row stores as the grants a gate's run requests them.
func storedGrantNeeds(needs []store.StepNeed) []grants.Need {
	out := make([]grants.Need, 0, len(needs))
	for _, need := range needs {
		out = append(out, grants.Need{Pool: need.Pool, Count: need.Count})
	}
	return out
}

// stepGetenv returns a lookup of name in environ. The last entry for name wins, as it does in a process's environment.
func stepGetenv(environ []string) func(string) string {
	return func(name string) string {
		value := ""
		for _, entry := range environ {
			if key, val, found := strings.Cut(entry, "="); found && key == name {
				value = val
			}
		}
		return value
	}
}
