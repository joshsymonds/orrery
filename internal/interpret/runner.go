package interpret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// Store is the store a Runner reads an epic's rows through and writes its node runs, loops, parks and check node work
// through. *store.Store satisfies it.
type Store interface {
	Epics(ctx context.Context) ([]store.EpicRow, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	StartNodes(ctx context.Context, epic string, runs []store.NodeRun, trigger *store.NodeTrigger, at time.Time) error
	EndNodes(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error
	EndNodesWaking(
		ctx context.Context, epic string, ends []store.NodeEnd, trigger store.NodeTrigger, at time.Time,
	) error
	NextRound(ctx context.Context, epic, path string, from int64) error
	ParkLoop(ctx context.Context, epic, path string, round int64, reason string, at time.Time) error
	ParkEpic(ctx context.Context, epic, reason string, at time.Time) error
	ParkNode(
		ctx context.Context, epic, path string, attempt int64, outcome, output, reason string, at time.Time,
	) error
	ParkNodeMetAtBase(
		ctx context.Context, epic, path string, attempt int64, outcome, output, reason string, met []string,
		at time.Time,
	) error
	ClaimBatch(
		ctx context.Context, epic, path string, attempt int64, revision, refs, result string, tasks []int64,
	) error
	EndBatch(
		ctx context.Context, epic, path string, attempt int64, outcome, output string, trigger *store.NodeTrigger,
		at time.Time,
	) error
	ParkEpicWaking(ctx context.Context, epic, reason string, trigger store.NodeTrigger, at time.Time) error
	EndPlanBuilt(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error
	PrepareNode(ctx context.Context, epic, path string, attempt int64, revision, refs string) error
	RecordNodeResult(ctx context.Context, epic, path string, attempt int64, result string) error
	CheckNodeRefs(
		ctx context.Context, epic, path string, attempt int64, park string, tells []string, at time.Time,
	) error
	TakeReleaseSlot(ctx context.Context, epic string, at time.Time) (string, error)
	FreeReleaseSlot(ctx context.Context, epic string) error
	StopRelease(ctx context.Context, epic, path string, attempt int64, reason string, at time.Time) error
	RecordRelease(ctx context.Context, epic string, run store.ReleaseRun) error
	RecordCandidate(ctx context.Context, epic string, run store.ReleaseRun) error
	MarkCandidateStale(
		ctx context.Context, epic, path string, attempt int64, head, reason string, at time.Time,
	) error
	FreezeReview(ctx context.Context, epic, path string, attempt int64, revision string, at time.Time) error
	EndReview(
		ctx context.Context, epic, path string, attempt int64, output string, verdicts []store.ReviewVerdict,
		ledger []store.LedgerFinding, at time.Time,
	) error
	EndClosure(
		ctx context.Context, epic, path string, attempt int64, outcome, output string, round store.ReviewRound,
		closures []store.Closure, at time.Time,
	) error
	HoldReviewStep(ctx context.Context, epic, step, reason string, at time.Time) error
}

// Checks runs the check of a gate or command node run under a holder, once per id. *enginehost.NodeChecks satisfies it.
type Checks interface {
	Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error)
}

// Evidence runs the evidence item of a Requirement once per id. *enginehost.Evidence satisfies it.
type Evidence interface {
	RunItem(ctx context.Context, id, epic, item, revision, phase string) (store.EvidenceRun, error)
}

// Steps starts the read-only step of an agent node run. *enginehost.Steps satisfies it.
type Steps interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
}

// The enginehost services satisfy the interfaces a Config takes for them.
var (
	_ Checks   = (*enginehost.NodeChecks)(nil)
	_ Evidence = (*enginehost.Evidence)(nil)
	_ Steps    = (*enginehost.Steps)(nil)
)

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

// Reviewers reads the epic-stage reviewers enabled for an epic, and starts the step of one reviewer, verifier or closure
// verifier at a revision of the epic's repository.
type Reviewers interface {
	Epic(epic store.EpicRow) []kinds.Reviewer
	Start(ctx context.Context, epic store.EpicRow, name, base, revision string) (enginehost.StepStart, error)
}

// Config configures a Runner. A Tick that is not positive means five seconds, a nil Now means time.Now and a nil Getenv
// means os.Getenv. A nil Logger discards. Checks, Evidence and Steps carry out the work of check node runs, so a Runner
// that runs an epic with such nodes needs each of them. Reviewers and Ending carry the end-of-epic review: with both set,
// its review and closure kinds run, and without both an epic waits at its review node. Ending also ends the epics whose
// release is decided, so a Runner that runs a release needs it. StateDir is the state directory release steps'
// worktrees live under, so a Runner that runs a release step needs it.
type Config struct {
	Store     Store
	Planner   Planner
	Builder   Builder
	Checks    Checks
	Evidence  Evidence
	Steps     Steps
	Reviewers Reviewers
	Ending    Ending
	StateDir  string
	Getenv    func(string) string
	Tick      time.Duration
	// Ticks is a test seam: when set, Run passes at start and then once at each value it receives, in place of a ticker
	// every Tick. Production leaves it nil.
	Ticks  <-chan time.Time
	Now    func() time.Time
	Logger *slog.Logger
	// Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
	Passed func()
}

// Runner runs each epic's workflow over its node runs: it carries out the structural actions the decisions ask for,
// hands build runs to the builder and passes the planner for the epics whose plan run is running.
type Runner struct {
	store     Store
	planner   Planner
	builder   Builder
	checks    Checks
	evidence  Evidence
	steps     Steps
	reviewers Reviewers
	ending    Ending
	stateDir  string
	getenv    func(string) string
	tick      time.Duration
	ticks     <-chan time.Time
	now       func() time.Time
	logger    *slog.Logger
	passed    func()
	kinds     map[string]kinds.Kind
}

// defaultTick is how often a Runner passes when its Config names no positive tick.
const defaultTick = 5 * time.Second

// maxSteps is how many structural actions one pass carries out for an epic before the next pass continues from the
// rows the store then holds.
const maxSteps = 16

// builtinEpic is the workflow an epic with no pinned text runs.
const builtinEpic = "epic"

// New returns a Runner for cfg, with the plan and build kinds registered, and the review, closure and conclude kinds too
// when cfg has both Reviewers and Ending.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, planner: cfg.Planner, builder: cfg.Builder, checks: cfg.Checks, evidence: cfg.Evidence,
		steps: cfg.Steps, reviewers: cfg.Reviewers, ending: cfg.Ending, stateDir: cfg.StateDir, getenv: cfg.Getenv,
		tick: cfg.Tick, now: cfg.Now, logger: cfg.Logger, ticks: cfg.Ticks, passed: cfg.Passed,
		kinds: map[string]kinds.Kind{
			kindPlan: kinds.Plan{}, kindBuild: kinds.Build{}, kindGate: kinds.Gate{}, kindCommand: kinds.Command{},
			kindEvidence: kinds.Evidence{}, kindOwned: kinds.Owned{}, kindAgent: kinds.Agent{},
			kindIntegrate: kinds.Integrate{}, kindRelease: kinds.Release{},
		},
	}
	if cfg.Reviewers != nil && cfg.Ending != nil {
		runner.kinds[reviewNodeKind] = kinds.Review{}
		runner.kinds[closureNodeKind] = kinds.Closure{}
		runner.kinds[concludeNodeKind] = kinds.Conclude{}
	}
	if runner.tick <= 0 {
		runner.tick = defaultTick
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	if runner.getenv == nil {
		runner.getenv = os.Getenv
	}
	if runner.logger == nil {
		runner.logger = slog.New(slog.DiscardHandler)
	}
	return runner
}

// Run recovers the planner's turns a previous orreryd left running, then passes at once, then once per tick, until ctx
// ends. It then waits for the planner's turns and returns. With Config.Ticks set, the ticks are the values it receives,
// and Config.Passed is called after each pass.
func (r *Runner) Run(ctx context.Context) {
	ticks := r.ticks
	if ticks == nil {
		ticker := time.NewTicker(r.tick)
		defer ticker.Stop()
		ticks = ticker.C
	}
	r.planner.Recover(ctx)
	for {
		r.Pass(ctx)
		if r.passed != nil {
			r.passed()
		}
		select {
		case <-ctx.Done():
			r.planner.WaitTurns()
			return
		case <-ticks:
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
		row, acts, err := r.decide(ctx, epic.ID, flow)
		if err != nil {
			r.logger.ErrorContext(ctx, "decide epic", "epic", epic.ID, "err", err)
			return false
		}
		act, structural := firstStructural(acts)
		if !structural {
			return r.deliver(ctx, epic.ID, acts)
		}
		if err = r.carry(ctx, row, act); err != nil {
			r.logger.ErrorContext(ctx, "carry out node action", "epic", epic.ID, "action", act.Kind, "err", err)
			return false
		}
	}
	r.logger.WarnContext(ctx, "interpret pass reached its step limit", "epic", epic.ID, "steps", maxSteps)
	return false
}

// decide reads the rows of epic in one read. It returns the epic as read, which the actions are carried out for, and
// the actions flow asks for on the rows.
func (r *Runner) decide(ctx context.Context, epic string, flow workflow.Workflow) (store.EpicRow, []Action, error) {
	read, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return store.EpicRow{}, nil, fmt.Errorf("read epic %s: %w", epic, err)
	}
	rows := kinds.Rows{
		Epic: read.Epic, Runs: read.Runs, Checks: read.Checks, Evidence: read.Evidence, Steps: read.Steps,
		Pending: read.Pending, Releases: read.Releases, Slot: read.Slot, Review: read.Review,
	}
	if r.reviewers != nil {
		rows.Reviewers = r.reviewers.Epic(read.Epic)
	}
	return read.Epic, Decide(flow, rows, r.kinds), nil
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

// carry writes one structural action of epic to the store at the runner's clock. Its work is carried out on the epic as
// the action was decided.
func (r *Runner) carry(ctx context.Context, epic store.EpicRow, act Action) error {
	at := r.now()
	var err error
	switch act.Kind {
	case ActStart:
		err = r.store.StartNodes(ctx, epic.ID, act.Runs, act.Trigger, at)
	case ActEnd:
		switch {
		case act.Built:
			err = r.store.EndPlanBuilt(ctx, epic.ID, act.Ends, at)
		case act.Trigger != nil:
			err = r.store.EndNodesWaking(ctx, epic.ID, act.Ends, *act.Trigger, at)
		default:
			err = r.store.EndNodes(ctx, epic.ID, act.Ends, at)
		}
	case ActRound:
		err = r.store.NextRound(ctx, epic.ID, act.Path, act.Round)
	case ActPark:
		err = r.store.ParkLoop(ctx, epic.ID, act.Path, act.Round, act.Reason, at)
	case ActParkNode:
		if len(act.Met) > 0 {
			err = r.store.ParkNodeMetAtBase(
				ctx, epic.ID, act.Path, act.Attempt, act.Outcome, act.Output, act.Reason, act.Met, at,
			)
		} else {
			err = r.store.ParkNode(ctx, epic.ID, act.Path, act.Attempt, act.Outcome, act.Output, act.Reason, at)
		}
	case ActParkEpic:
		err = r.store.ParkEpicWaking(ctx, epic.ID, act.Reason, *act.Trigger, at)
	case ActBatch:
		err = r.store.EndBatch(ctx, epic.ID, act.Path, act.Attempt, act.Outcome, act.Output, act.Trigger, at)
	case ActHold, ActReview, ActClosure, ActConclude:
		return r.carryReview(ctx, epic.ID, act, at)
	case ActWork:
		err = r.work(ctx, epic, act, at)
	default:
		return fmt.Errorf("action %s changes no node run", act.Kind)
	}
	if err != nil {
		return fmt.Errorf("%s of epic %s: %w", act.Kind, epic.ID, err)
	}
	return nil
}

// carryReview writes one review action of epic to the store at at: the hold of a stopped review step, the verdicts and
// ledger of a review run, the round and closures of a closure run, or the ending of a conclusion run.
func (r *Runner) carryReview(ctx context.Context, epic string, act Action, at time.Time) error {
	var err error
	switch act.Kind {
	case ActHold:
		err = r.store.HoldReviewStep(ctx, epic, act.IDs[0], act.Reason, at)
	case ActReview:
		err = r.store.EndReview(ctx, epic, act.Path, act.Attempt, act.Output, act.Verdicts, act.Ledger, at)
	case ActClosure:
		err = r.store.EndClosure(
			ctx, epic, act.Path, act.Attempt, act.Outcome, act.Output, act.ReviewRound, act.Closures, at,
		)
	case ActConclude:
		err = r.conclude(ctx, epic, act, at)
	default:
		return fmt.Errorf("action %s is no review action", act.Kind)
	}
	if err != nil {
		return fmt.Errorf("%s of epic %s: %w", act.Kind, epic, err)
	}
	return nil
}

// conclude ends epic at the phase act names, with act's output as its ending body, and ends the conclusion run at act's
// path passed, in one write. Split in two, a kill after the ending and before the run's end, with a release moving the
// phase on in between, left the run running: the next pass decided the ending again and ended the epic a second time.
func (r *Runner) conclude(ctx context.Context, epic string, act Action, at time.Time) error {
	ends := []store.NodeEnd{{Path: act.Path, Outcome: outcomePassed, Output: act.Output}}
	if err := r.ending.EndEpic(ctx, epic, act.Phase, act.Output, ends, at); err != nil {
		return fmt.Errorf("end epic %s and its conclusion run %s: %w", epic, act.Path, err)
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
