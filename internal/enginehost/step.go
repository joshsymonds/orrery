package enginehost

import (
	"context"
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

// ErrBadOwned reports a step whose owned path is absolute or reaches outside the repository.
var ErrBadOwned = errors.New("step owned path must be a relative path inside the repository")

// ErrBadGate reports a step whose gate is refused: a named gate that does not resolve, or a gates file that is not
// absolute or is set without a name.
var ErrBadGate = errors.New("step gate is refused")

// ErrBadWatchTick reports a StepsConfig whose WatchTick is negative.
var ErrBadWatchTick = errors.New("step watch tick must not be negative")

// stepDefaultDone is the status a step's done return carries when its StepStart names none.
const stepDefaultDone = "done"

// stepDefaultGateLimit is the gate limit a step is stored with when it names no gate.
const stepDefaultGateLimit = 30 * time.Minute

// stepDefaultModelLimit is how long a step's model time may run when its StepStart names no limit.
const stepDefaultModelLimit = 60 * time.Minute

// stepDefaultWatchTick is how often a started step's watch is checked when StepsConfig names no tick.
const stepDefaultWatchTick = 5 * time.Second

// StepStart describes a step to start. A writing step (ReadOnly false) runs in Workspace, a linked worktree, and its
// done return is checked against its commit and Owned paths and, when it names one, its gate. A read-only step runs in
// a detached worktree at Revision, which is removed when the step ends. Revision names the start commit and empty means
// HEAD. DoneValue empty means "done", and ModelLimit zero means 60 minutes, the model-time limit the step is stored
// with. Epic and Task are optional labels the step is stored with; a step with an empty Epic is never listed by List.
//
// GateName names a gate to resolve once at start, from GatesFile when it is set and else from the project file for
// Workspace. GatesFile must be absolute and needs GateName. A start without GateName runs no gate. A named gate takes
// its argv and limit from its definition, and each pool its needs name must be declared by a pool file for Workspace.
// A named agent gate instead runs its agent as a read-only step with the verdict schema and the done value "done", and
// its prompt is built from Prompt. Such a start is refused unless it is read-only and leaves Agent, Schema and
// DoneValue empty.
type StepStart struct {
	Agent      string
	Prompt     string
	Schema     string
	Workspace  string
	Revision   string
	ReadOnly   bool
	Owned      []string
	GateName   string
	GatesFile  string
	ModelLimit time.Duration
	DoneValue  string
	Epic       string
	Task       string
}

// StepStore reads the step rows Steps reports and the turn ends their sessions stored. *store.Store satisfies it.
type StepStore interface {
	Step(ctx context.Context, id string) (store.StepRow, error)
	OpenSteps(ctx context.Context) ([]store.StepRow, error)
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
	StepTurn(ctx context.Context, id string) (store.StepTurnEnd, bool, error)
}

// StepsConfig configures Steps. Sessions starts and stops the step sessions, Store reads the step rows and Writer
// stores the ops of the step loops. Pools queues each named gate's run for its needs; a nil Pools fails a gate that
// runs. Environ is the environment gates run in, and the one a named gate is resolved under.
// Worktrees is the directory read-only steps' worktrees are added under, one per step id. A nil Logger discards. Haiku
// makes the monitor and judge calls of every started step, and nil makes none. Now is the clock each step's watch reads,
// time.Now when nil. WatchTick is how often each started step's watch is checked, five seconds when zero.
// GateStateDir is the state directory each gate's holder lives under, and a directory under os.TempDir when empty.
// GateScope is the systemd-run scope each gate's holder runs in, and empty runs it without one. With GateScope set, Close
// leaves a running gate under its holder for a restarted Steps to re-adopt; without it, Close kills the gate.
type StepsConfig struct {
	Sessions     stepSessions
	Store        StepStore
	Writer       *Writer
	Pools        PoolCommands
	Worktrees    string
	Environ      []string
	Logger       *slog.Logger
	Haiku        *Haiku
	Now          func() time.Time
	WatchTick    time.Duration
	GateStateDir string
	GateScope    string
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
// and adopted holds the run Readopt rebuilt for each open row until Reconcile starts or settles it.
type Steps struct {
	sessions     stepSessions
	store        StepStore
	writer       *Writer
	pools        PoolCommands
	worktrees    string
	environ      []string
	logger       *slog.Logger
	haiku        *Haiku
	now          func() time.Time
	tick         time.Duration
	gateStateDir string
	gateScope    string

	mu      sync.Mutex
	loops   map[string]context.CancelFunc
	causes  map[string]context.CancelCauseFunc
	runs    map[string]*stepRun
	idle    *sync.Cond
	adopter StepAdopter
	adopted map[string]*stepRun
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
		sessions:     cfg.Sessions,
		store:        cfg.Store,
		writer:       cfg.Writer,
		pools:        cfg.Pools,
		worktrees:    cfg.Worktrees,
		environ:      cfg.Environ,
		logger:       logger,
		haiku:        cfg.Haiku,
		now:          cfg.Now,
		tick:         tick,
		gateStateDir: cfg.GateStateDir,
		gateScope:    cfg.GateScope,
		loops:        map[string]context.CancelFunc{},
		causes:       map[string]context.CancelCauseFunc{},
		runs:         map[string]*stepRun{},
		adopted:      map[string]*stepRun{},
	}
	steps.idle = sync.NewCond(&steps.mu)
	return steps, nil
}

// Start checks start, resolves its gate, which an agent gate turns into the step's agent, schema and prompt, and checks
// the schema. It then resolves its revision and starts the step under a fresh id and runs its loop. A refusal returns
// its error with an empty id and stores nothing; a read-only step's worktree is removed then too. A writing step runs
// in start.Workspace. A read-only step gets a detached worktree at its start commit under the worktrees directory,
// which is removed when the step ends. A folder whose stamp cannot be read refuses the start the same way. The step's
// monitor and judge calls carry the env of the stamp in effect for its folder. Any other StartStep error, after the row
// is written, ends the step failed and returns its id with the error.
func (s *Steps) Start(ctx context.Context, start StepStart) (string, error) {
	if err := checkStepStart(start); err != nil {
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
	id, err := newSessionID()
	if err != nil {
		return "", err
	}

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
		ID: id, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: commit, DoneValue: doneValue, ReadOnly: start.ReadOnly, Owned: start.Owned, Gate: gate.argv,
		GateName: gate.name, GateSource: gate.source, GateNeeds: gate.needs, GateLimit: gateLimit,
		ModelLimit: modelLimit, Epic: start.Epic, Task: start.Task,
	}
	cfg := stepRunConfig{
		id: id, schema: start.Schema, doneValue: doneValue, sessions: s.sessions, writer: s.writer, logger: s.logger,
		writing: !start.ReadOnly, start: commit, owned: start.Owned, gate: gate.argv, gateLimit: gateLimit,
		gateName: gate.name, gateSource: gate.source, grant: gate.grant, pools: s.pools,
		environ: s.environ, prompt: start.Prompt, now: s.now, modelLimit: modelLimit,
		stateDir: s.gateStateDir, scope: s.gateScope,
	}
	step := StepSession{ID: id, Dir: start.Workspace, Prompt: start.Prompt, Agent: start.Agent, Schema: start.Schema}
	if start.ReadOnly {
		wt := filepath.Join(s.worktrees, id)
		err = addStepWorktree(ctx, start.Workspace, wt, commit)
		if err != nil {
			return "", err
		}
		add.Worktree = wt
		step.Dir = wt
		cfg.cleanup = func(ctx context.Context) error {
			return removeStepWorktree(ctx, start.Workspace, wt)
		}
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

// checkStepStart reports whether start's prompt, workspace and owned paths are usable, before anything is stored or any
// worktree is added. Start checks the schema once the gate has set it.
func checkStepStart(start StepStart) error {
	if start.Prompt == "" {
		return ErrEmptyPrompt
	}
	if err := checkDir(start.Workspace); err != nil {
		return err
	}
	for _, path := range start.Owned {
		if !filepath.IsLocal(path) {
			return fmt.Errorf("%w: %q", ErrBadOwned, path)
		}
	}
	return nil
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

// resolveStepGate returns the gate start runs. A start without GateName runs no gate, and GatesFile set without GateName
// is refused. A named gate is resolved once, from start.GatesFile or else the project file for start.Workspace. An agent
// gate returns here, before any pool file is read. Any other named gate has the pools every pool file for
// start.Workspace declares loaded with it. It is refused with an error matching ErrBadGate when GatesFile is not
// absolute, when the name does not resolve, when the pool files do not load, and when a need is for a pool no pool file
// declares. The resolved argv is used as the gate's definition gives it; nothing is looked up on PATH.
func (s *Steps) resolveStepGate(ctx context.Context, start StepStart) (stepGate, error) {
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
