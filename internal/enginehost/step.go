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

// StepStart describes a step to start. A writing step (ReadOnly false) runs in Workspace, a linked worktree, and its
// done return is checked against its commit and Owned paths and its Gate. A read-only step runs in a detached worktree
// at Revision, which is removed when the step ends. Revision names the start commit and empty means HEAD. DoneValue
// empty means "done", and GateLimit zero means 30 minutes.
type StepStart struct {
	Agent     string
	Prompt    string
	Schema    string
	Workspace string
	Revision  string
	ReadOnly  bool
	Owned     []string
	Gate      []string
	GateLimit time.Duration
	DoneValue string
}

// StepStore reads the step rows Steps reports. *store.Store satisfies it.
type StepStore interface {
	Step(ctx context.Context, id string) (store.StepRow, error)
	OpenSteps(ctx context.Context) ([]store.StepRow, error)
}

// StepsConfig configures Steps. Sessions starts and stops the step sessions, Store reads the step rows and Writer
// stores the ops of the step loops. Locks are the gate lock files and Environ is the environment gates run in.
// Worktrees is the directory read-only steps' worktrees are added under, one per step id. A nil Logger discards.
type StepsConfig struct {
	Sessions  stepSessions
	Store     StepStore
	Writer    *Writer
	Locks     []string
	Worktrees string
	Environ   []string
	Logger    *slog.Logger
}

// Steps starts steps and reports their rows. Each started step's loop runs under a context Steps owns: loops holds the
// cancel of every loop that has not returned, by step id.
type Steps struct {
	sessions  stepSessions
	store     StepStore
	writer    *Writer
	locks     []string
	worktrees string
	environ   []string
	logger    *slog.Logger

	mu    sync.Mutex
	loops map[string]context.CancelFunc
}

// NewSteps returns a Steps for cfg.
func NewSteps(cfg StepsConfig) *Steps {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Steps{
		sessions:  cfg.Sessions,
		store:     cfg.Store,
		writer:    cfg.Writer,
		locks:     cfg.Locks,
		worktrees: cfg.Worktrees,
		environ:   cfg.Environ,
		logger:    logger,
		loops:     map[string]context.CancelFunc{},
	}
}

// Start checks start and resolves its revision, then starts the step under a fresh id and runs its loop. A refusal
// returns its error with an empty id and stores nothing. A writing step runs in start.Workspace. A read-only step gets
// a detached worktree at its start commit under the worktrees directory, which is removed when the step ends. A
// StartStep error ends the step failed and returns its id with the error.
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
	add := store.StepAdd{
		ID: id, Agent: start.Agent, Prompt: start.Prompt, Schema: start.Schema, Workspace: start.Workspace,
		Revision: commit, DoneValue: doneValue, ReadOnly: start.ReadOnly, Owned: start.Owned, Gate: gate,
		GateLimit: gateLimit,
	}
	cfg := stepRunConfig{
		id: id, schema: start.Schema, doneValue: doneValue, sessions: s.sessions, writer: s.writer, logger: s.logger,
		writing: !start.ReadOnly, start: commit, owned: start.Owned, gate: gate, gateLimit: gateLimit, locks: s.locks,
		environ: s.environ,
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
		// The caller's ctx may have ended the start, so the failed end and the worktree removal run without it.
		run.finish(context.WithoutCancel(ctx), stepLoopEndFailed, "", err.Error())
		return id, fmt.Errorf("start step %s: %w", id, err)
	}
	s.runLoop(ctx, id, run)
	return id, nil
}

// Status returns the step row with id. It returns an error matching store.ErrNoStep when no step has that id.
func (s *Steps) Status(ctx context.Context, id string) (store.StepRow, error) {
	row, err := s.store.Step(ctx, id)
	if err != nil {
		return store.StepRow{}, fmt.Errorf("read step %s: %w", id, err)
	}
	return row, nil
}

// runLoop runs run in its own goroutine under a context derived from ctx's values, not its cancellation, so the loop
// outlives the Start call. The context's cancel is kept in loops until the loop returns, and then it is removed and
// called.
func (s *Steps) runLoop(ctx context.Context, id string, run *stepRun) {
	loop, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.mu.Lock()
	s.loops[id] = cancel
	s.mu.Unlock()
	go func() {
		defer s.endLoop(id)
		run.run(loop)
	}()
}

// endLoop removes the loop of id from loops and cancels its context.
func (s *Steps) endLoop(id string) {
	s.mu.Lock()
	cancel := s.loops[id]
	delete(s.loops, id)
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
