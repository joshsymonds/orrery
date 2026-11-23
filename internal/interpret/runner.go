package interpret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/evidence"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/workflow"
)

// Store is the store a Runner reads an epic's rows through and writes its node runs, loops, parks and check node work
// through. *store.Store satisfies it.
type Store interface {
	Epics(ctx context.Context) ([]store.EpicRow, error)
	OpenNodeRunStates(ctx context.Context) (map[string][]store.NodeRun, error)
	EpicFlow(ctx context.Context, id string) (store.EpicFlow, error)
	StartNodes(ctx context.Context, epic string, runs []store.NodeRun, trigger *store.NodeTrigger, at time.Time) error
	StartCorrectionPlan(ctx context.Context, epic string, plan store.NodeRun, at time.Time) error
	StartGapRound(
		ctx context.Context, epic string, run store.NodeRun, triggers []store.NodeTrigger, turns []store.GapTurn,
		at time.Time,
	) error
	EndNodes(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error
	EndNodesWaking(
		ctx context.Context, epic string, ends []store.NodeEnd, trigger store.NodeTrigger, at time.Time,
	) error
	NextRound(ctx context.Context, epic, path string, from int64) error
	RebuildTask(ctx context.Context, epic, path string, attempt int64) error
	ClimbTask(
		ctx context.Context, epic string, seq int64, from, to store.TaskBuild, move store.RungMove,
		version *store.TaskVersionInput, decision *store.TaskDecision, at time.Time,
	) error
	ParkLoop(ctx context.Context, epic, path string, round int64, reason string, at time.Time) error
	ParkEpic(ctx context.Context, epic string, class store.ParkClass, reason string, at time.Time) error
	ParkNode(
		ctx context.Context, epic, path string, attempt int64, outcome, output string, class store.ParkClass,
		reason string, at time.Time,
	) error
	EndNodesTelling(ctx context.Context, epic string, ends []store.NodeEnd, tell string, at time.Time) error
	ClaimBatch(
		ctx context.Context, epic, path string, attempt int64, revision, refs, result string, tasks []int64,
	) error
	EndBatch(ctx context.Context, epic string, end store.BatchEnd, trigger *store.NodeTrigger, at time.Time) error
	RecordIntegrateRound(ctx context.Context, epic string, round store.IntegrateRound, at time.Time) error
	ComposeIntegrateRound(
		ctx context.Context, epic, path string, attempt, task, round int64, commit, result string, at time.Time,
	) error
	FailIntegrateRound(
		ctx context.Context, epic, path string, attempt, task, round int64, failure string, at time.Time,
	) error
	LabelledSteps(ctx context.Context, epic, task string) ([]store.StepRow, error)
	NodeRuns(ctx context.Context, epic string) ([]store.NodeRun, error)
	PrepareSync(
		ctx context.Context, epic, path string, attempt int64, revision, result string, read store.OriginRead,
	) error
	EndSync(ctx context.Context, epic string, end store.SyncEnd, at time.Time) error
	RecordOriginRead(ctx context.Context, epic string, read store.OriginRead) error
	ParkEpicWaking(ctx context.Context, epic, reason string, trigger store.NodeTrigger, at time.Time) error
	EndPlanBuilt(ctx context.Context, epic string, ends []store.NodeEnd, at time.Time) error
	PrepareNode(ctx context.Context, epic, path string, attempt int64, revision, refs string) error
	RecordNodeResult(ctx context.Context, epic, path string, attempt int64, result string) error
	CheckNodeRefs(ctx context.Context, epic, path string, attempt int64, tells []string, at time.Time) error
	TakeReleaseSlot(ctx context.Context, epic string, at time.Time) (string, error)
	FreeReleaseSlot(ctx context.Context, epic string) error
	StopRelease(ctx context.Context, epic, path string, attempt int64, reason string, at time.Time) error
	ReplanRelease(
		ctx context.Context, epic, path string, attempt int64, plan store.NodeRun, trigger store.NodeTrigger,
		tell string, at time.Time,
	) error
	ResumeReplannedRelease(ctx context.Context, epic, path string, attempt int64, at time.Time) error
	RecordRelease(ctx context.Context, epic string, run store.ReleaseRun) error
	RecordIntegratorRound(ctx context.Context, epic string, round store.ReleaseRun, replan bool) error
	RecordCandidate(ctx context.Context, epic string, run store.ReleaseRun) error
	MarkCandidateStale(
		ctx context.Context, epic, path string, attempt int64, head, reason string, at time.Time,
	) error
	FreezeReview(ctx context.Context, epic, path string, attempt int64, revision string, at time.Time) error
	EndReview(
		ctx context.Context, epic, path string, attempt int64, output string, verdicts []store.ReviewVerdict,
		ledger []store.LedgerFinding, at time.Time,
	) error
	EndReviewRerun(
		ctx context.Context, epic string, end store.NodeEnd, verdict store.ReviewVerdict,
		findings []store.LedgerFinding, at time.Time,
	) error
	EndClosure(
		ctx context.Context, epic, path string, attempt int64, outcome, output string, round store.ReviewRound,
		closures []store.Closure, at time.Time,
	) error
	HoldReviewStep(ctx context.Context, epic, step, reason string, at time.Time) error
	OpenAmendments(ctx context.Context) ([]store.EvidenceAmendment, error)
	SettleAmendment(ctx context.Context, epic string, seq int64, at time.Time) error
	StopAmendment(ctx context.Context, epic string, seq int64, reason string, at time.Time) error
	NodeCheck(ctx context.Context, id string) (store.NodeCheck, error)
}

// Checks runs the check of a gate or command node run under a holder, once per id. *enginehost.NodeChecks satisfies it.
type Checks interface {
	Run(ctx context.Context, check store.NodeCheck) (store.NodeCheck, error)
}

// Evidence runs the evidence item of a Requirement, or the item an evidence amendment asks a Requirement to hold, once per
// id. *enginehost.Evidence satisfies it.
type Evidence interface {
	RunItem(ctx context.Context, id, epic, item, revision, phase string) (store.EvidenceRun, error)
	RunAmendment(
		ctx context.Context, id, epic, requirement string, spec evidence.Item, revision, phase string,
	) (store.EvidenceRun, error)
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
// verifier at a revision of the epic's repository. Start's doneGates are the checks of the Done gates that ran at
// revision, and its baseCost the epic's cost checks at its base, the base measurement of test cost.
type Reviewers interface {
	Epic(epic store.EpicRow) []kinds.Reviewer
	Start(
		ctx context.Context, epic store.EpicRow, name, base, revision string, doneGates, baseCost []store.NodeCheck,
	) (enginehost.StepStart, error)
}

// Config configures a Runner. A nil Now means time.Now, a nil NewTimer means time.NewTimer and a nil Getenv means
// os.Getenv. A nil Logger discards. Checks, Evidence and Steps carry out the work of check node runs, so a Runner that
// runs an epic with such nodes needs each of them. Reviewers and Ending carry the end-of-epic review: with both set, its
// review and closure kinds run, and without both an epic waits at its review node. Ending also ends the epics whose
// release is decided, so a Runner that runs a release needs it. StateDir is the state directory release steps'
// worktrees live under, so a Runner that runs a release step needs it. NewTimer arms the one timer a Runner keeps for
// the origin-read deadlines, which Run waits on.
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
	// Ticks is a test seam: when set, Run passes at start and then once at each value it receives, and wakes and
	// deadlines pass nothing. Production leaves it nil.
	Ticks    <-chan time.Time
	Now      func() time.Time
	NewTimer func(d time.Duration) (<-chan time.Time, func() bool)
	Logger   *slog.Logger
	// Passed is a test seam: when set, Run calls it after each pass. Production leaves it nil.
	Passed func()
}

// Runner runs each epic's workflow over its node runs: it carries out the structural actions the decisions ask for,
// hands build runs to the builder and passes the planner for the epics whose plan run is running. readLimit bounds each
// git ls-remote a sync runs against origin, syncReadLimit unless a test sets it, and fetchLimit a sync's fetch of
// origin's main, syncFetchLimit unless a test sets it.
//
// The fields from signal down are the wakes Run passes on: mu guards them, and Wake, which any goroutine may call, sets
// them under mu only. signal is a one-slot channel Wake fills, woken the epics to pass next, waiters the epics that
// waited on each holder of a release slot, and deadlines the moment each epic's origin read goes stale.
type Runner struct {
	store      Store
	planner    Planner
	builder    Builder
	checks     Checks
	evidence   Evidence
	steps      Steps
	reviewers  Reviewers
	ending     Ending
	stateDir   string
	getenv     func(string) string
	ticks      <-chan time.Time
	now        func() time.Time
	newTimer   func(d time.Duration) (<-chan time.Time, func() bool)
	logger     *slog.Logger
	passed     func()
	kinds      map[string]kinds.Kind
	readLimit  time.Duration
	fetchLimit time.Duration

	mu        sync.Mutex
	signal    chan struct{}
	woken     map[string]struct{}
	waiters   map[string]map[string]struct{}
	deadlines map[string]time.Time
}

// maxSteps is how many structural actions one pass carries out for an epic before the next pass continues from the
// rows the store then holds.
const maxSteps = 16

// builtinEpic is the workflow an epic with no pinned text runs.
const builtinEpic = "epic"

// New returns a Runner for cfg, with the plan, build, check, integrate, sync and release kinds registered, and the
// review, closure and conclude kinds too when cfg has both Reviewers and Ending.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, planner: cfg.Planner, builder: cfg.Builder, checks: cfg.Checks, evidence: cfg.Evidence,
		steps: cfg.Steps, reviewers: cfg.Reviewers, ending: cfg.Ending, stateDir: cfg.StateDir, getenv: cfg.Getenv,
		now: cfg.Now, newTimer: cfg.NewTimer, logger: cfg.Logger, ticks: cfg.Ticks, passed: cfg.Passed,
		readLimit: syncReadLimit, fetchLimit: syncFetchLimit,
		kinds: map[string]kinds.Kind{
			kindPlan: kinds.Plan{}, kindBuild: kinds.Build{}, kindGate: kinds.Gate{}, kindCommand: kinds.Command{},
			kindEvidence: kinds.Evidence{}, kindOwned: kinds.Owned{}, kindAgent: kinds.Agent{}, kindCost: kinds.Cost{},
			kindIntegrate: kinds.Integrate{}, kindRelease: kinds.Release{}, kindSync: kinds.Sync{},
		},
		signal:    make(chan struct{}, 1),
		woken:     make(map[string]struct{}),
		waiters:   make(map[string]map[string]struct{}),
		deadlines: make(map[string]time.Time),
	}
	if cfg.Reviewers != nil && cfg.Ending != nil {
		runner.kinds[reviewNodeKind] = kinds.Review{}
		runner.kinds[closureNodeKind] = kinds.Closure{}
		runner.kinds[concludeNodeKind] = kinds.Conclude{}
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	if runner.newTimer == nil {
		runner.newTimer = stdTimer
	}
	if runner.getenv == nil {
		runner.getenv = os.Getenv
	}
	if runner.logger == nil {
		runner.logger = slog.New(slog.DiscardHandler)
	}
	return runner
}

// Run recovers the planner's turns a previous orreryd left running, then passes every open epic at once. With
// Config.Ticks set, it then passes every open epic once per value it receives. Without it, it passes the epics a Wake
// named since its last pass, each once in sorted order, and the epics whose origin-read deadline has come, and nothing
// on a clock of its own, until ctx ends. Config.Passed is called after each pass. Run then waits for the planner's turns
// and returns.
func (r *Runner) Run(ctx context.Context) {
	r.planner.Recover(ctx)
	// The pass below reads every open epic, so a wake that came before it is covered by it.
	r.takeWoken()
	r.Pass(ctx)
	r.passDone()
	if r.ticks != nil {
		r.runTicks(ctx)
		return
	}
	r.runWakes(ctx)
}

// runTicks passes every open epic once per value Config.Ticks sends, until ctx ends, and then waits for the planner's
// turns.
func (r *Runner) runTicks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.planner.WaitTurns()
			return
		case <-r.ticks:
		}
		r.Pass(ctx)
		r.passDone()
	}
}

// runWakes passes the epics woken since its last pass, each once, until ctx ends, and then waits for the planner's turns.
// It waits on a wake, on the deadline timer and on ctx. A wake that arrives during a pass is passed after it, since the
// signal it leaves is taken on the next turn of the loop. A pass never starts once ctx has ended.
func (r *Runner) runWakes(ctx context.Context) {
	timer := &deadlineTimer{}
	r.armDeadline(timer)
	for {
		select {
		case <-ctx.Done():
		case <-r.signal:
		case <-timer.fire:
			timer.fired()
			r.wakeDue(r.now())
		}
		if ctx.Err() != nil {
			r.planner.WaitTurns()
			return
		}
		if woken := r.takeWoken(); len(woken) > 0 {
			if open := r.openWoken(ctx, woken); len(open) > 0 {
				r.passEpics(ctx, open)
				r.passDone()
			}
		}
		r.armDeadline(timer)
	}
}

// passDone calls Config.Passed, when it is set, after a pass.
func (r *Runner) passDone() {
	if r.passed != nil {
		r.passed()
	}
}

// Pass drives each open evidence amendment once (see passAmendments), runs each epic's flow once, and then passes the
// planner once for the epics whose plan run was given its turn. Every open epic is passed. An epic whose flow does not
// load is parked once, if it is not parked already. The node runs of every open epic are read first, in one query, and
// an epic at rest (Rests) is not read again: an ended epic whose workflow has run to its end, or waits on its owner, has
// nothing to decide. When that read fails, every epic's flow is run.
func (r *Runner) Pass(ctx context.Context) {
	epics, err := r.store.Epics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list epics", "err", err)
		return
	}
	r.passEpics(ctx, epics)
}

// openWoken returns the epics woken names that are open, in the order woken gives them. An id that names no epic, or a
// closed one, is skipped, and a list that fails is logged and returns none.
func (r *Runner) openWoken(ctx context.Context, woken []string) []store.EpicRow {
	epics, err := r.store.Epics(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list epics", "err", err)
		return nil
	}
	byID := make(map[string]store.EpicRow, len(epics))
	for _, epic := range epics {
		byID[epic.ID] = epic
	}
	open := make([]store.EpicRow, 0, len(woken))
	for _, id := range woken {
		if epic, ok := byID[id]; ok && !store.IsClosedPhase(epic.Phase) {
			open = append(open, epic)
		}
	}
	return open
}

// passEpics runs one pass over epics: their amendments, then their flows, then the planner for those given a plan turn
// or planner-parked. Pass passes every listed epic, and a wake passes the open epics it names.
func (r *Runner) passEpics(ctx context.Context, epics []store.EpicRow) {
	r.passAmendments(ctx, epics)
	states, statesErr := r.store.OpenNodeRunStates(ctx)
	if statesErr != nil {
		r.logger.ErrorContext(ctx, "list node run states", "err", statesErr)
	}
	planned := make(map[string]bool)
	for _, epic := range epics {
		if store.IsClosedPhase(epic.Phase) {
			continue
		}
		flow, loadErr := loadFlow(epic)
		if loadErr != nil {
			r.parkUnloaded(ctx, epic, loadErr)
			continue
		}
		// A planner park wakes the planner even when no plan run was given its turn, since no node runs on a parked epic.
		plannerParked := epic.Parked != "" && !epic.ParkClass.OpensQuestion()
		resting := statesErr == nil && Rests(flow, kinds.Rows{Epic: epic, Runs: states[epic.ID]}, r.kinds)
		if (!resting && r.passFlow(ctx, epic, flow)) || plannerParked {
			planned[epic.ID] = true
		}
	}
	r.planner.PassOnly(ctx, func(epic string) bool { return planned[epic] })
}

// parkUnloaded parks epic as an orchestrator park with the reason its flow did not load, unless it is parked already, so
// a broken pinned text parks its epic once.
func (r *Runner) parkUnloaded(ctx context.Context, epic store.EpicRow, cause error) {
	if epic.Parked != "" {
		return
	}
	reason := cause.Error()
	if err := r.store.ParkEpic(ctx, epic.ID, store.ParkClassOrchestrator, reason, r.now()); err != nil {
		r.logger.ErrorContext(ctx, "park epic with unloadable workflow", "epic", epic.ID, "err", err)
		return
	}
	r.logger.WarnContext(ctx, "epic parked: its workflow did not load", "epic", epic.ID, "reason", reason)
}

// passFlow carries out the actions flow asks for on epic, and reports whether epic's plan run was given its turn. Each
// structural action is carried out and the rows read again, at most maxSteps times. Once a decision has no structural
// action, its build runs go to the builder. An epic that the rows show closed is left with nothing carried out: a close
// that lands while the pass carries out an action, such as a sync's prepare, ends the epic's running runs, and the walk
// would read the plan run it ended as a node that ended and start the node after it.
func (r *Runner) passFlow(ctx context.Context, epic store.EpicRow, flow workflow.Workflow) bool {
	for range maxSteps {
		row, acts, err := r.decide(ctx, epic.ID, flow)
		if err != nil {
			r.logger.ErrorContext(ctx, "decide epic", "epic", epic.ID, "err", err)
			return false
		}
		if store.IsClosedPhase(row.Phase) {
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
// the actions flow asks for on the rows, decided at the runner's clock. The read also records the wakes it leaves for
// epic (noteRead). The stranded tasks of the epic are read only when one of its tasks waits on dependencies, since only
// a build that waits on a dependency reads them (awaitsDependencies).
func (r *Runner) decide(ctx context.Context, epic string, flow workflow.Workflow) (store.EpicRow, []Action, error) {
	read, err := r.store.EpicFlow(ctx, epic)
	if err != nil {
		return store.EpicRow{}, nil, fmt.Errorf("read epic %s: %w", epic, err)
	}
	rows := flowRows(read)
	roundSteps, err := r.roundSteps(ctx, epic, read.Runs)
	if err != nil {
		return store.EpicRow{}, nil, err
	}
	rows.Steps = append(rows.Steps, roundSteps...)
	if awaitsDependencies(read.Epic.Tasks) {
		rows.Stranded, err = startStranded(ctx, read.Epic, read.Runs)
		if err != nil {
			return store.EpicRow{}, nil, fmt.Errorf("read stranded tasks of epic %s: %w", epic, err)
		}
	}
	rows.Now = r.now()
	if r.reviewers != nil {
		rows.Reviewers = r.reviewers.Epic(read.Epic)
	}
	r.noteRead(epic, read, rows.Now)
	return read.Epic, Decide(flow, rows, r.kinds), nil
}

// awaitsDependencies reports whether one of tasks is open, has not started and depends on other tasks. A build of such
// a task waits on them, and reads the stranded tasks of its epic (kinds.Rows.Stranded).
func awaitsDependencies(tasks []store.EpicTask) bool {
	return slices.ContainsFunc(tasks, func(task store.EpicTask) bool {
		return task.Status == taskOpen && task.Build.Stage == "" && len(task.DependsOn) > 0
	})
}

// flowRows returns the rows a kind reads of the epic read holds, with no reviewers and no clock.
func flowRows(read store.EpicFlow) kinds.Rows {
	return kinds.Rows{
		Epic: read.Epic, Runs: read.Runs, Checks: read.Checks, Evidence: read.Evidence, Steps: read.Steps,
		Pending: read.Pending, Actions: read.Actions, Releases: read.Releases, Slot: read.Slot, Review: read.Review,
		GapTurns: read.GapTurns, Unresolvable: read.Unresolvable, Reruns: read.Reruns, Rounds: read.Rounds,
	}
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
		case act.Tell != "":
			err = r.store.EndNodesTelling(ctx, epic.ID, act.Ends, act.Tell, at)
		default:
			err = r.store.EndNodes(ctx, epic.ID, act.Ends, at)
		}
	case ActRound, ActPark, ActParkNode, ActRebuild:
		err = r.moveRun(ctx, epic.ID, act, at)
	case ActGapTask:
		cause := store.ClimbCause(act.Reason, false)
		move := store.RungMove{MovedBy: store.MovedByDependency, Cause: cause, To: store.GapRung}
		err = r.store.ClimbTask(
			ctx, epic.ID, act.Task, act.Build, store.ClimbedBuild(act.Build, store.GapRung, cause), move, nil, nil, at,
		)
		if errors.Is(err, store.ErrTaskMoved) {
			err = nil
		}
	case ActParkEpic:
		err = r.store.ParkEpicWaking(ctx, epic.ID, act.Reason, *act.Trigger, at)
	case ActBatch:
		err = r.endBatch(ctx, epic, act, at)
	case ActSync, ActReadOrigin:
		err = r.carrySync(ctx, epic, act, at)
	case ActHold, ActReview, ActClosure, ActConclude, ActGapRound, ActRerun:
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

// moveRun writes one action of epic that moves a single node run at act's path, at at: the next round of a loop, the park
// of a loop or of a node run, or the rebuild of a build run whose task is open again.
func (r *Runner) moveRun(ctx context.Context, epic string, act Action, at time.Time) error {
	var err error
	switch act.Kind {
	case ActRound:
		err = r.store.NextRound(ctx, epic, act.Path, act.Round)
	case ActPark:
		err = r.store.ParkLoop(ctx, epic, act.Path, act.Round, act.Reason, at)
	case ActParkNode:
		err = r.store.ParkNode(
			ctx, epic, act.Path, act.Attempt, act.Outcome, act.Output, store.ParkClassNode, act.Reason, at,
		)
	case ActRebuild:
		err = r.store.RebuildTask(ctx, epic, act.Path, act.Attempt)
	default:
		return fmt.Errorf("action %s moves no node run", act.Kind)
	}
	if err != nil {
		return fmt.Errorf("move node run %s: %w", act.Path, err)
	}
	return nil
}

// carrySync carries out one sync action of epic at at: the end of a sync run, or the read of origin's main a sync is
// due to start from.
func (r *Runner) carrySync(ctx context.Context, epic store.EpicRow, act Action, at time.Time) error {
	if act.Kind == ActReadOrigin {
		return r.readOrigin(ctx, epic, at)
	}
	if err := r.store.EndSync(ctx, epic.ID, store.SyncEnd{
		Path: act.Path, Attempt: act.Attempt, Outcome: act.Outcome, Output: act.Output, Tell: act.Reason,
	}, at); err != nil {
		return fmt.Errorf("end sync %s: %w", act.Path, err)
	}
	return nil
}

// carryReview writes one review action of epic to the store at at: the hold of a stopped review step, the verdicts and
// ledger of a review run, the round and closures of a closure run, the ending of a conclusion run, the gap round a
// conclusion starts, or the end of a rerun run with the verdict and findings its reviewer settled, which replace the
// reviewer's stored verdict and findings.
func (r *Runner) carryReview(ctx context.Context, epic string, act Action, at time.Time) error {
	var err error
	switch act.Kind {
	case ActGapRound:
		err = r.store.StartGapRound(ctx, epic, act.Runs[0], act.Triggers, act.GapTurns, at)
	case ActHold:
		err = r.store.HoldReviewStep(ctx, epic, act.IDs[0], act.Reason, at)
	case ActReview:
		err = r.store.EndReview(ctx, epic, act.Path, act.Attempt, act.Output, act.Verdicts, act.Ledger, at)
	case ActRerun:
		err = r.store.EndReviewRerun(
			ctx, epic, store.NodeEnd{Path: act.Path, Outcome: outcomePassed, Output: act.Output}, act.Verdicts[0],
			act.Ledger, at,
		)
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
// path passed, in one write. An ending with gaps also opens act's question for the owner. Split in two, a kill after
// the ending and before the run's end, with a release moving the phase on in between, left the run running: the next
// pass decided the ending again and ended the epic a second time.
func (r *Runner) conclude(ctx context.Context, epic string, act Action, at time.Time) error {
	ends := []store.NodeEnd{{Path: act.Path, Outcome: outcomePassed, Output: act.Output}}
	var err error
	if act.Phase == phaseEndedWithGaps {
		err = r.ending.EndEpicWithGaps(ctx, epic, act.Output, act.Question, ends, at)
	} else {
		err = r.ending.EndEpic(ctx, epic, act.Phase, act.Output, ends, at)
	}
	if err != nil {
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
