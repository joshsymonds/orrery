package interpret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// Store is the store a Runner reads an epic's rows through and writes its node runs, loops and parks through.
// *store.Store satisfies it.
type Store interface {
	Epics(ctx context.Context) ([]store.EpicRow, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	StartNodes(ctx context.Context, epic string, runs []store.NodeRun, trigger *store.NodeTrigger, at time.Time) error
	EndNodes(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error
	NextRound(ctx context.Context, epic, path string, from int64) error
	ParkLoop(ctx context.Context, epic, path string, round int64, reason string, at time.Time) error
	ParkEpic(ctx context.Context, epic, reason string, at time.Time) error
}

// Planner is the planner the interpreter passes plan runs to and waits on at shutdown.
type Planner interface {
	Recover(ctx context.Context)
	PassOnly(ctx context.Context, open func(epic string) bool)
	WaitTurns()
}

// Builder is the build the interpreter passes build runs to.
type Builder interface {
	PassTasks(ctx context.Context, id string, seqs []int64)
}

// Config configures a Runner. A Tick that is not positive means five seconds, and a nil Now means time.Now. A nil
// Logger discards.
type Config struct {
	Store   Store
	Planner Planner
	Builder Builder
	Tick    time.Duration
	Now     func() time.Time
	Logger  *slog.Logger
}

// Runner runs each epic's workflow over its node runs: it carries out the structural actions the decisions ask for,
// hands build runs to the builder and passes the planner for the epics whose plan run is running.
type Runner struct {
	store   Store
	planner Planner
	builder Builder
	tick    time.Duration
	now     func() time.Time
	logger  *slog.Logger
	kinds   map[string]kinds.Kind
}

// defaultTick is how often a Runner passes when its Config names no positive tick.
const defaultTick = 5 * time.Second

// maxSteps is how many structural actions one pass carries out for an epic before the next pass continues from the
// rows the store then holds.
const maxSteps = 16

// builtinEpic is the workflow an epic with no pinned text runs.
const builtinEpic = "epic"

// New returns a Runner for cfg, with the plan and build kinds registered.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, planner: cfg.Planner, builder: cfg.Builder, tick: cfg.Tick, now: cfg.Now,
		logger: cfg.Logger,
		kinds:  map[string]kinds.Kind{kindPlan: kinds.Plan{}, kindBuild: kinds.Build{}},
	}
	if runner.tick <= 0 {
		runner.tick = defaultTick
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	if runner.logger == nil {
		runner.logger = slog.New(slog.DiscardHandler)
	}
	return runner
}

// Run recovers the planner's turns a previous orreryd left running, then passes at once, then once per tick, until ctx
// ends. It then waits for the planner's turns and returns.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	r.planner.Recover(ctx)
	for {
		r.Pass(ctx)
		select {
		case <-ctx.Done():
			r.planner.WaitTurns()
			return
		case <-ticker.C:
		}
	}
}

// Pass runs each epic's flow once, and then passes the planner once for the epics whose plan run was given its turn.
// An epic whose flow does not load is parked once, if it is not parked already.
func (r *Runner) Pass(ctx context.Context) {
	epics, err := r.store.Epics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list epics", "err", err)
		return
	}
	planned := make(map[string]bool)
	for _, epic := range epics {
		flow, loadErr := loadFlow(epic)
		if loadErr != nil {
			r.parkUnloaded(ctx, epic, loadErr)
			continue
		}
		if r.passFlow(ctx, epic, flow) {
			planned[epic.ID] = true
		}
	}
	r.planner.PassOnly(ctx, func(epic string) bool { return planned[epic] })
}

// parkUnloaded parks epic with the reason its flow did not load, unless it is parked already, so a broken pinned text
// parks its epic once.
func (r *Runner) parkUnloaded(ctx context.Context, epic store.EpicRow, cause error) {
	if epic.Parked != "" {
		return
	}
	reason := cause.Error()
	if err := r.store.ParkEpic(ctx, epic.ID, reason, r.now()); err != nil {
		r.logger.ErrorContext(ctx, "park epic with unloadable workflow", "epic", epic.ID, "err", err)
		return
	}
	r.logger.WarnContext(ctx, "epic parked: its workflow did not load", "epic", epic.ID, "reason", reason)
}

// passFlow carries out the actions flow asks for on epic, and reports whether epic's plan run was given its turn. Each
// structural action is carried out and the rows read again, at most maxSteps times. Once a decision has no structural
// action, its build runs go to the builder.
func (r *Runner) passFlow(ctx context.Context, epic store.EpicRow, flow workflow.Workflow) bool {
	for range maxSteps {
		acts, err := r.decide(ctx, epic.ID, flow)
		if err != nil {
			r.logger.ErrorContext(ctx, "decide epic", "epic", epic.ID, "err", err)
			return false
		}
		act, structural := firstStructural(acts)
		if !structural {
			return r.deliver(ctx, epic.ID, acts)
		}
		if err = r.carry(ctx, epic.ID, act); err != nil {
			r.logger.ErrorContext(ctx, "carry out node action", "epic", epic.ID, "action", act.Kind, "err", err)
			return false
		}
	}
	r.logger.WarnContext(ctx, "interpret pass reached its step limit", "epic", epic.ID, "steps", maxSteps)
	return false
}

// decide reads the rows of epic in one read and returns the actions flow asks for on them.
func (r *Runner) decide(ctx context.Context, epic string, flow workflow.Workflow) ([]Action, error) {
	read, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return nil, fmt.Errorf("read epic %s: %w", epic, err)
	}
	return Decide(flow, kinds.Rows{Epic: read.Epic, Runs: read.Runs, Pending: read.Pending}, r.kinds), nil
}

// firstStructural returns the first action that changes a node run, and whether acts has one. A run action only hands
// a node run to its kind, so it changes none.
func firstStructural(acts []Action) (Action, bool) {
	for _, act := range acts {
		if act.Kind != ActRun {
			return act, true
		}
	}
	return Action{}, false
}

// carry writes one structural action to the store at the runner's clock.
func (r *Runner) carry(ctx context.Context, epic string, act Action) error {
	at := r.now()
	var err error
	switch act.Kind {
	case ActStart:
		err = r.store.StartNodes(ctx, epic, act.Runs, act.Trigger, at)
	case ActEnd:
		err = r.store.EndNodes(ctx, epic, act.Ends, at)
	case ActRound:
		err = r.store.NextRound(ctx, epic, act.Path, act.Round)
	case ActPark:
		err = r.store.ParkLoop(ctx, epic, act.Path, act.Round, act.Reason, at)
	default:
		return fmt.Errorf("action %s changes no node run", act.Kind)
	}
	if err != nil {
		return fmt.Errorf("%s of epic %s: %w", act.Kind, epic, err)
	}
	return nil
}

// deliver hands the build runs of acts to the builder, and reports whether acts gives a plan run its turn.
func (r *Runner) deliver(ctx context.Context, epic string, acts []Action) bool {
	var seqs []int64
	planned := false
	for _, act := range acts {
		if act.Kind != ActRun {
			continue
		}
		switch act.NodeKind {
		case kindPlan:
			planned = true
		case kindBuild:
			seqs = append(seqs, act.Task)
		}
	}
	if len(seqs) > 0 {
		r.builder.PassTasks(ctx, epic, seqs)
	}
	return planned
}

// loadFlow returns the flow epic runs. An epic with no pinned text runs the built-in epic. Any other epic runs its
// pinned text, which must hash to its pinned sha256 and parse and check. A failure is an error that names the
// workflow.
func loadFlow(epic store.EpicRow) (workflow.Workflow, error) {
	pin := epic.Workflow
	if pin.Text == "" {
		built, err := workflow.Load(builtinEpic, noRepoFile)
		if err != nil {
			return workflow.Workflow{}, fmt.Errorf("workflow %s: %w", builtinEpic, err)
		}
		return built.Workflow, nil
	}
	flow, err := parsePinned(pin)
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("workflow %s: %w", pin.Name, err)
	}
	return flow, nil
}

// parsePinned returns the workflow of pin's text once the text is checked against pin's sha256, parsed as the file
// pinned/<name>.yaml and checked.
func parsePinned(pin store.EpicWorkflow) (workflow.Workflow, error) {
	sum := sha256.Sum256([]byte(pin.Text))
	if got := hex.EncodeToString(sum[:]); got != pin.SHA256 {
		return workflow.Workflow{}, fmt.Errorf("text sha256 %s does not match the pinned %s", got, pin.SHA256)
	}
	flow, err := workflow.Parse("pinned/"+pin.Name+".yaml", []byte(pin.Text))
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("parse text: %w", err)
	}
	if err = workflow.Check(flow); err != nil {
		return workflow.Workflow{}, fmt.Errorf("check text: %w", err)
	}
	return flow, nil
}

// noRepoFile is the reader of a load that has no repository file, so the built-in workflow is the one read.
func noRepoFile(string) ([]byte, bool, error) {
	return nil, false, nil
}
