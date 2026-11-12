package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// ErrBadOwned reports a step whose owned path is absolute or reaches outside the repository.
var ErrBadOwned = errors.New("step owned path must be a relative path inside the repository")

// ErrBadGate reports a step whose gate program is not found on PATH.
var ErrBadGate = errors.New("step gate program is not found on PATH")

// stepDefaultDone is the status a step's done return carries when its StepStart names none.
const stepDefaultDone = "done"

// stepDefaultGateLimit is how long a step's gate may run when its StepStart names no limit.
const stepDefaultGateLimit = 30 * time.Minute

// stepDefaultModelLimit is how long a step's model time may run when its StepStart names no limit.
const stepDefaultModelLimit = 60 * time.Minute

// stepDefaultWatchTick is how often a started step's watch is checked when StepsConfig names no tick.
const stepDefaultWatchTick = 5 * time.Second

// StepStart describes a step to start. A writing step (ReadOnly false) runs in Workspace, a linked worktree, and its
// done return is checked against its commit and Owned paths and its Gate. A read-only step runs in a detached worktree
// at Revision, which is removed when the step ends. Revision names the start commit and empty means HEAD. DoneValue
// empty means "done", GateLimit zero means 30 minutes, and ModelLimit zero means 60 minutes, the model-time limit the
// step is stored with. Epic and Task are optional labels the step is stored with; a step with an empty Epic is never
// listed by List.
type StepStart struct {
	Agent      string
	Prompt     string
	Schema     string
	Workspace  string
	Revision   string
	ReadOnly   bool
	Owned      []string
	Gate       []string
	GateLimit  time.Duration
	ModelLimit time.Duration
	DoneValue  string
	Epic       string
	Task       string
}

// StepStore reads the step rows Steps reports. *store.Store satisfies it.
type StepStore interface {
	Step(ctx context.Context, id string) (store.StepRow, error)
	OpenSteps(ctx context.Context) ([]store.StepRow, error)
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
}

// StepsConfig configures Steps. Sessions starts and stops the step sessions, Store reads the step rows and Writer
// stores the ops of the step loops. Locks are the gate lock files and Environ is the environment gates run in.
// Worktrees is the directory read-only steps' worktrees are added under, one per step id. A nil Logger discards. Haiku
// makes the monitor and judge calls of every started step, and nil makes none. Now is the clock each step's watch reads,
// time.Now when nil. WatchTick is how often each started step's watch is checked, five seconds when zero.
type StepsConfig struct {
	Sessions  stepSessions
	Store     StepStore
	Writer    *Writer
	Locks     []string
	Worktrees string
	Environ   []string
	Logger    *slog.Logger
	Haiku     *Haiku
	Now       func() time.Time
	WatchTick time.Duration
}

// Steps starts steps and reports their rows. Each started step's loop runs under a context Steps owns: loops holds the
// cancel of every loop that has not returned, and runs holds its stepRun, both by step id. idle is broadcast whenever a
// loop is removed from loops.
type Steps struct {
	sessions  stepSessions
	store     StepStore
	writer    *Writer
	locks     []string
	worktrees string
	environ   []string
	logger    *slog.Logger
	haiku     *Haiku
	now       func() time.Time
	tick      time.Duration

	mu    sync.Mutex
	loops map[string]context.CancelFunc
	runs  map[string]*stepRun
	idle  *sync.Cond
}

// NewSteps returns a Steps for cfg.
func NewSteps(cfg StepsConfig) *Steps {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tick := cfg.WatchTick
	if tick == 0 {
		tick = stepDefaultWatchTick
	}
	steps := &Steps{
		sessions:  cfg.Sessions,
		store:     cfg.Store,
		writer:    cfg.Writer,
		locks:     cfg.Locks,
		worktrees: cfg.Worktrees,
		environ:   cfg.Environ,
		logger:    logger,
		haiku:     cfg.Haiku,
		now:       cfg.Now,
		tick:      tick,
		loops:     map[string]context.CancelFunc{},
		runs:      map[string]*stepRun{},
	}
	steps.idle = sync.NewCond(&steps.mu)
	return steps
}

// Start checks start and resolves its revision, then starts the step under a fresh id and runs its loop. A refusal
// returns its error with an empty id and stores nothing; a read-only step's worktree is removed then too. A writing step
// runs in start.Workspace. A read-only step gets a detached worktree at its start commit under the worktrees directory,
// which is removed when the step ends. Any other StartStep error, after the row is written, ends the step failed and
// returns its id with the error.
func (s *Steps) Start(ctx context.Context, start StepStart) (string, error) {
	if err := checkStepStart(start); err != nil {
		return "", err
	}
	gate, err := stepGateProgram(start.Gate)
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
	gateLimit := start.GateLimit
	if gateLimit == 0 {
		gateLimit = stepDefaultGateLimit
	}
	modelLimit := start.ModelLimit
	if modelLimit == 0 {
		modelLimit = stepDefaultModelLimit
	}
	add := store.StepAdd{
		ID: id, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: commit, DoneValue: doneValue, ReadOnly: start.ReadOnly, Owned: start.Owned, Gate: gate,
		GateLimit: gateLimit, ModelLimit: modelLimit, Epic: start.Epic, Task: start.Task,
	}
	cfg := stepRunConfig{
		id: id, schema: start.Schema, doneValue: doneValue, sessions: s.sessions, writer: s.writer, logger: s.logger,
		writing: !start.ReadOnly, start: commit, owned: start.Owned, gate: gate, gateLimit: gateLimit, locks: s.locks,
		environ: s.environ, prompt: start.Prompt, now: s.now, modelLimit: modelLimit,
	}
	if s.haiku != nil {
		cfg.haiku = s.haiku
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

	run := newStepRun(cfg)
	step.Observer = run
	step.Ops = []store.EngineOp{add}
	if err = s.sessions.StartStep(ctx, step); err != nil {
		return s.startFailed(ctx, id, run, err)
	}
	s.runLoop(ctx, id, run)
	return id, nil
}

// startFailed handles a StartStep error for the step id run runs. A refusal stored nothing, so it removes the step's
// worktree and returns the refusal with an empty id. Any other error ends the step failed and returns its id with the
// error. The caller's ctx may have ended the start, so the removal and the failed end run without it.
func (s *Steps) startFailed(ctx context.Context, id string, run *stepRun, err error) (string, error) {
	var refused stepRefusedError
	if errors.As(err, &refused) {
		if run.cleanup != nil {
			if cleanErr := run.cleanup(context.WithoutCancel(ctx)); cleanErr != nil {
				s.logger.ErrorContext(ctx, "clean up refused step", "step", id, "err", cleanErr)
			}
		}
		return "", refused.err
	}
	run.finish(context.WithoutCancel(ctx), stepLoopEndFailed, "", err.Error())
	return id, fmt.Errorf("start step %s: %w", id, err)
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
// returns. The context's cancel and run are kept in loops and runs until the loop returns, and then they are removed and
// the cancel is called.
func (s *Steps) runLoop(ctx context.Context, id string, run *stepRun) {
	loop, cancel := context.WithCancel(context.WithoutCancel(ctx))
	ticker := time.NewTicker(s.tick)
	run.ticks = ticker.C
	s.mu.Lock()
	s.loops[id] = cancel
	s.runs[id] = run
	s.mu.Unlock()
	go func() {
		defer s.endLoop(id)
		defer ticker.Stop()
		run.run(loop)
	}()
}

// endLoop removes the loop of id from loops and runs, wakes any Close waiting on loops, and cancels the loop's context.
func (s *Steps) endLoop(id string) {
	s.mu.Lock()
	cancel := s.loops[id]
	delete(s.loops, id)
	delete(s.runs, id)
	s.idle.Broadcast()
	s.mu.Unlock()
	cancel()
}

// checkStepStart reports whether start's prompt, schema, workspace and owned paths are usable, before anything is
// stored or any worktree is added.
func checkStepStart(start StepStart) error {
	if start.Prompt == "" {
		return ErrEmptyPrompt
	}
	if err := checkStepSchema(start.Schema); err != nil {
		return err
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

// stepGateProgram returns gate with its program resolved to the path it has on PATH. An empty gate names no program
// and returns nil. A program that is not on PATH returns an error matching ErrBadGate.
func stepGateProgram(gate []string) ([]string, error) {
	if len(gate) == 0 {
		return nil, nil
	}
	program, err := exec.LookPath(gate[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBadGate, gate[0], err)
	}
	return append([]string{program}, gate[1:]...), nil
}
