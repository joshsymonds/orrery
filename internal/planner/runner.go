package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/kinds"
	"github.com/joshsymonds/orrery/internal/store"
)

// Store is the store a Runner reads and settles planner turns through. *store.Store satisfies it.
type Store interface {
	PlanStates(ctx context.Context) ([]store.EpicPlanState, error)
	ClaimTurn(ctx context.Context, epic string, at time.Time) (store.EpicTurn, error)
	SetTurnStep(ctx context.Context, epic string, turn int64, step string) error
	TurnStopped(ctx context.Context, epic string, seq int64) (bool, error)
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	EpicReview(ctx context.Context, epic string) (store.Review, error)
	TurnTriggers(ctx context.Context, epic string, turn int64) ([]store.EpicTrigger, error)
	CompleteTurn(ctx context.Context, epic string, turn int64, plan store.EpicPlan, at time.Time) error
	PendingActions(ctx context.Context) ([]store.PlannerAction, error)
	CarryOutAction(ctx context.Context, action store.PlannerAction, at time.Time) ([]string, error)
	FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error
	FreeTurn(ctx context.Context, epic string, turn int64, free store.TurnFree, at time.Time) error
	RunningTurns(ctx context.Context) ([]store.EpicTurn, error)
	RateLimit(ctx context.Context, session string) (store.RateLimit, bool, error)
	Follows(ctx context.Context) ([]store.LabelFollow, error)
}

// Steps starts planner steps, subscribes to their ends and holds, and stops them. *enginehost.Steps satisfies it.
type Steps interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
	Status(ctx context.Context, id string) (store.StepRow, error)
	Subscribe(ctx context.Context, ids []string) (*enginehost.StepSubscription, error)
	Stop(ctx context.Context, id string) error
}

// Config configures a Runner. A Tick that is not positive means five seconds, and a nil Now means time.Now. A nil
// Logger discards. Resolve answers whether the gates and judges of a planned task resolve for its epic. NewTimer arms a
// timer that sends on its channel once, after a duration, and returns the timer's stop; a nil NewTimer wraps
// time.NewTimer. After returns the channel a record of a turn's step that the store refused as busy waits on before it
// is tried again, and nil is time.After.
type Config struct {
	Store    Store
	Steps    Steps
	Agent    func() (string, error)
	Tick     time.Duration
	Now      func() time.Time
	Logger   *slog.Logger
	Resolve  Resolver
	NewTimer func(time.Duration) (<-chan time.Time, func() bool)
	After    func(time.Duration) <-chan time.Time
}

// stepRecordRetry is how long after the store refuses the record of a turn's step as busy the record is tried again.
const stepRecordRetry = time.Second

// defaultTick is how often a Runner passes when its Config names no positive tick.
const defaultTick = 5 * time.Second

// stepDone is the end of a step whose return passed its check.
const stepDone = "done"

// stepStopped is the end of a step ended by Stop, and stepRestarted the end Reconcile stores on an open step row.
const (
	stepStopped   = "stopped"
	stepRestarted = "engine_restarted"
)

// rateLimitRejected is the status of a rate limit that refused a session's request.
const rateLimitRejected = "rejected"

// stepCapFull is the reason a turn is freed with when the step cap refuses its step.
const stepCapFull = "step cap full"

// taskOpen is the status of a task that is not yet accepted or failed. Only an open task owns its files.
const taskOpen = "open"

// taskFailed is the status of a task whose build failed. A failed task's claims do not count toward coverage.
const taskFailed = "failed"

// plannerNamespace starts the parts every planner turn's step id hashes, so the ids are this package's.
// turnStepIDLength is the number of hex digits of a step id, the form a fresh id has.
const (
	plannerNamespace = "orrery-planner"
	turnStepIDLength = 32
)

// Runner passes over the epics with pending triggers and runs their planner turns. Each claimed turn runs in its own
// goroutine, and turns tracks them until Run returns.
type Runner struct {
	store    Store
	steps    Steps
	agent    func() (string, error)
	tick     time.Duration
	now      func() time.Time
	after    func(time.Duration) <-chan time.Time
	logger   *slog.Logger
	resolve  Resolver
	newTimer func(time.Duration) (<-chan time.Time, func() bool)
	turns    sync.WaitGroup
}

// New returns a Runner for cfg.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, steps: cfg.Steps, agent: cfg.Agent, tick: cfg.Tick, now: cfg.Now, after: cfg.After,
		logger: cfg.Logger, resolve: cfg.Resolve, newTimer: cfg.NewTimer,
	}
	if runner.tick <= 0 {
		runner.tick = defaultTick
	}
	if runner.now == nil {
		runner.now = time.Now
	}
	if runner.newTimer == nil {
		runner.newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	if runner.after == nil {
		runner.after = time.After
	}
	if runner.logger == nil {
		runner.logger = slog.New(slog.DiscardHandler)
	}
	return runner
}

// Run settles the turns a previous orreryd left running, then passes at once, then once per tick, until ctx ends. It
// then waits for the turns it started and returns.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	r.recoverTurns(ctx)
	for {
		r.pass(ctx, nil)
		select {
		case <-ctx.Done():
			r.turns.Wait()
			return
		case <-ticker.C:
		}
	}
}

// Recover settles the turns a previous orreryd left running, as Run does before its first pass.
func (r *Runner) Recover(ctx context.Context) {
	r.recoverTurns(ctx)
}

// Pass carries out the pending actions and decides each epic with pending triggers once, as Run does on each tick.
func (r *Runner) Pass(ctx context.Context) {
	r.pass(ctx, nil)
}

// PassOnly does what Pass does, only for the epics that open reports true for. Every other epic is left as it is.
func (r *Runner) PassOnly(ctx context.Context, open func(epic string) bool) {
	r.pass(ctx, open)
}

// WaitTurns waits until every turn the runner started has settled, as Run does before it returns.
func (r *Runner) WaitTurns() {
	r.turns.Wait()
}

// pass carries out every pending action, then decides each epic with pending triggers that open reports true for, or
// each such epic when open is nil. A park action claims a turn for the epic in its own goroutine and frees it with the
// park reason, decided under the planner park the epic held, and a claim action claims a turn for the epic in its own
// goroutine. agent is read once per pass.
func (r *Runner) pass(ctx context.Context, open func(epic string) bool) {
	r.carryOutActions(ctx)
	states, err := r.store.PlanStates(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list planner states", "err", err)
		return
	}
	agent, roleErr := r.agent()
	for _, state := range states {
		if open != nil && !open(state.Epic) {
			continue
		}
		action := Decide(State{
			Paused: state.Paused, Parked: state.Parked != "", Running: state.Running, Pending: state.Pending,
		}, roleErr)
		switch action.Kind {
		case ActionPark:
			r.turns.Add(1)
			go r.parkTurn(ctx, state.Epic, action.Reason, state.PlannerPark)
		case ActionClaim:
			r.turns.Add(1)
			go r.runTurn(ctx, state.Epic, agent)
		default:
		}
	}
}

// carryOutActions carries out every pending action in the order the store lists them, and stops each step the carrying
// out returns. An action whose carrying out fails is logged and left pending, so the next pass carries it out again; an
// action a restart left pending is carried out the same way.
func (r *Runner) carryOutActions(ctx context.Context) {
	actions, err := r.store.PendingActions(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list pending planner actions", "err", err)
		return
	}
	for _, action := range actions {
		if ctx.Err() != nil {
			return
		}
		stop, carryErr := r.store.CarryOutAction(ctx, action, r.now())
		if carryErr != nil {
			r.logger.ErrorContext(ctx, "carry out planner action", "epic", action.Epic, "turn", action.Turn,
				"position", action.Position, "err", carryErr)
			continue
		}
		for _, id := range stop {
			if stopErr := r.steps.Stop(ctx, id); stopErr != nil {
				r.logger.ErrorContext(ctx, "stop step of carried out planner action", "epic", action.Epic,
					"step", id, "err", stopErr)
			}
		}
	}
}

// claim claims a turn for epic. It returns false when no turn is claimed: an epic that is not claimable is left
// quietly, and a claim that fails is logged.
func (r *Runner) claim(ctx context.Context, epic string) (store.EpicTurn, bool) {
	turn, err := r.store.ClaimTurn(ctx, epic, r.now())
	if errors.Is(err, store.ErrNotClaimable) {
		return store.EpicTurn{}, false
	}
	if err != nil {
		r.logger.ErrorContext(ctx, "claim planner turn", "epic", epic, "err", err)
		return store.EpicTurn{}, false
	}
	return turn, true
}

// parkTurn claims a turn for epic and frees it with reason as both its reason and its park, so the park has a turn
// that records it. The park was decided while the epic held the planner park held, empty when it held none, so it is
// set only while the epic still holds that park: a retry since the decision voids it. The turn starts no step and
// returns its triggers to pending.
func (r *Runner) parkTurn(ctx context.Context, epic, reason, held string) {
	defer r.turns.Done()
	turn, ok := r.claim(ctx, epic)
	if !ok {
		return
	}
	r.release(ctx, epic, turn.Seq, store.TurnFree{Reason: reason, Park: reason, DecidedUnder: &held})
}

// runTurn claims a turn for epic, runs its planner step to an end and settles the turn. A turn whose context ends is
// left running for the next start.
func (r *Runner) runTurn(ctx context.Context, epic, agent string) {
	defer r.turns.Done()
	turn, ok := r.claim(ctx, epic)
	if !ok {
		return
	}
	id, err := r.startStep(ctx, epic, turn, agent)
	if err != nil {
		r.refuse(ctx, epic, turn.Seq, err)
		return
	}
	r.finish(ctx, epic, turn.Seq, id)
}

// turnStepID returns the id turn seq of epic starts its planner step under: the lowercase hex sha256 of the namespace,
// the epic and the seq joined with NUL, cut to turnStepIDLength digits. The same epic and seq give the same id, so
// recovery finds a step that was started before its id was recorded on the turn.
func turnStepID(epic string, seq int64) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{plannerNamespace, epic, strconv.FormatInt(seq, 10)}, "\x00")))
	return hex.EncodeToString(sum[:])[:turnStepIDLength]
}

// startStep reads the claimed turn's epic and triggers and starts its read-only planner step under turnStepID. It
// returns the step's id. A start that returns an id with an error still has its id recorded on the turn (see
// recordStep), and a step whose turn was flagged to stop before it had one is stopped as soon as its id is recorded.
func (r *Runner) startStep(ctx context.Context, epic string, turn store.EpicTurn, agent string) (string, error) {
	row, err := r.store.Epic(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", epic, err)
	}
	review, err := r.store.EpicReview(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("read review of epic %s: %w", epic, err)
	}
	triggers, err := r.store.TurnTriggers(ctx, epic, turn.Seq)
	if err != nil {
		return "", fmt.Errorf("read triggers of turn %d of epic %s: %w", turn.Seq, epic, err)
	}
	follows, err := r.store.Follows(ctx)
	if err != nil {
		return "", fmt.Errorf("read advice follows: %w", err)
	}
	spent, err := spentLines(ctx, r.store, row, triggers)
	if err != nil {
		return "", fmt.Errorf("read the spent lines of turn %d of epic %s: %w", turn.Seq, epic, err)
	}
	prompt, err := Prompt(row, turn.Seq, triggers, follows, spent)
	if err != nil {
		return "", fmt.Errorf("render prompt of turn %d of epic %s: %w", turn.Seq, epic, err)
	}
	id, err := r.steps.Start(ctx, enginehost.StepStart{
		ID:          turnStepID(epic, turn.Seq),
		Agent:       agent,
		Prompt:      prompt,
		Schema:      Schema,
		Workspace:   row.Repository,
		ReadOnly:    true,
		Epic:        row.ID,
		ReturnCheck: returnCheck(ctx, row, review, r.resolve, spent),
	})
	if id != "" {
		if setErr := r.recordStep(ctx, epic, turn.Seq, id); setErr != nil {
			r.logger.ErrorContext(ctx, "record step of planner turn",
				"epic", epic, "turn", turn.Seq, "step", id, "err", setErr)
		} else {
			r.stopIfFlagged(ctx, epic, turn.Seq, id)
		}
	}
	if err != nil {
		return id, fmt.Errorf("start planner step: %w", err)
	}
	return id, nil
}

// recordStep records step id on turn seq of epic. A record the store refuses as busy is tried again stepRecordRetry
// later, until it lands or ctx ends: a turn whose step is unrecorded keeps its epic's planner waiting until a restart,
// and a stop of the turn finds no step to stop. It returns the error of the last try.
func (r *Runner) recordStep(ctx context.Context, epic string, seq int64, id string) error {
	for {
		err := r.store.SetTurnStep(ctx, epic, seq, id)
		if err == nil {
			return nil
		}
		if !store.Busy(err) || ctx.Err() != nil {
			return fmt.Errorf("record step %s: %w", id, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("record step %s: %w", id, err)
		case <-r.after(stepRecordRetry):
		}
	}
}

// stopIfFlagged stops step id when turn seq of epic was flagged to stop while it had no step. A read or stop error is
// logged, and the wait that follows settles the turn either way.
func (r *Runner) stopIfFlagged(ctx context.Context, epic string, seq int64, id string) {
	stopped, err := r.store.TurnStopped(ctx, epic, seq)
	if err != nil {
		r.logger.ErrorContext(ctx, "read stop flag of planner turn", "epic", epic, "turn", seq, "err", err)
		return
	}
	if !stopped {
		return
	}
	if err = r.steps.Stop(ctx, id); err != nil {
		r.logger.ErrorContext(ctx, "stop planner step of flagged turn",
			"epic", epic, "turn", seq, "step", id, "err", err)
	}
}

// recoverTurns settles each turn a previous orreryd left running, before the first pass. A turn with no recorded step
// is looked up under the id its step starts under: a step stored under that id is recorded on the turn, and a turn with
// no such step has its step started under that id, as a claimed turn's is. A turn whose step has ended is settled by
// that end. A turn whose step is still open is waited on in its own goroutine, and stopped first when the turn was
// flagged to stop. A turn whose step cannot be read stays running and is logged; it still counts as running, so its
// epic gets no new turn.
func (r *Runner) recoverTurns(ctx context.Context) {
	turns, err := r.store.RunningTurns(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list running planner turns", "err", err)
		return
	}
	for _, turn := range turns {
		if ctx.Err() != nil {
			return
		}
		r.recoverTurn(ctx, turn)
	}
}

// recoverTurn settles one running turn as recoverTurns describes. A turn with no recorded step reads the step stored
// under its derived id, and is restarted when there is none.
func (r *Runner) recoverTurn(ctx context.Context, turn store.EpicTurn) {
	id, recorded := turn.Step, turn.Step != ""
	if !recorded {
		id = turnStepID(turn.Epic, turn.Seq)
	}
	row, err := r.steps.Status(ctx, id)
	if !recorded && errors.Is(err, store.ErrNoStep) {
		r.restart(ctx, turn)
		return
	}
	if err != nil {
		r.logger.ErrorContext(ctx, "read step of running planner turn",
			"epic", turn.Epic, "turn", turn.Seq, "step", id, "err", err)
		return
	}
	if !recorded && !r.recordFound(ctx, turn.Epic, turn.Seq, id, row) {
		return
	}
	if row.End == "" {
		r.turns.Add(1)
		go r.resume(ctx, turn.Epic, turn.Seq, id)
		return
	}
	if err = r.settle(ctx, turn.Epic, turn.Seq, id, row); err != nil {
		r.logger.ErrorContext(ctx, "settle planner turn", "epic", turn.Epic, "turn", turn.Seq, "err", err)
	}
}

// restart starts the planner step of a running turn that has no step and no step stored under its derived id, which a
// previous orreryd was killed before starting. The agent is read as a pass reads it: a role error parks the epic with
// its text, as a pass parks it. Otherwise the start settles the turn as runTurn's does, a refused start by refuse and a
// started step by resume.
func (r *Runner) restart(ctx context.Context, turn store.EpicTurn) {
	agent, roleErr := r.agent()
	if roleErr != nil {
		reason := roleErr.Error()
		r.release(ctx, turn.Epic, turn.Seq, store.TurnFree{Reason: reason, Park: reason})
		return
	}
	id, err := r.startStep(ctx, turn.Epic, turn, agent)
	if err != nil {
		r.refuse(ctx, turn.Epic, turn.Seq, err)
		return
	}
	r.turns.Add(1)
	go r.resume(ctx, turn.Epic, turn.Seq, id)
}

// recordFound records step id on turn seq of epic (see recordStep), a turn with no recorded step whose step was found
// under its derived id. An open step is stopped when the turn was flagged to stop, as a step started before its id was recorded is. It
// reports false, after logging, when the step is not recorded; the turn then stays running for the next start.
func (r *Runner) recordFound(ctx context.Context, epic string, seq int64, id string, row store.StepRow) bool {
	if err := r.recordStep(ctx, epic, seq, id); err != nil {
		r.logger.ErrorContext(ctx, "record step of running planner turn",
			"epic", epic, "turn", seq, "step", id, "err", err)
		return false
	}
	if row.End == "" {
		r.stopIfFlagged(ctx, epic, seq, id)
	}
	return true
}

// resume settles turn seq of epic by the end of step id, which is open: a step a previous orreryd left open, or one
// restart started.
func (r *Runner) resume(ctx context.Context, epic string, seq int64, id string) {
	defer r.turns.Done()
	r.finish(ctx, epic, seq, id)
}

// finish waits until step id ends and settles turn seq of epic by that end. Once ctx has ended, the turn is left
// running for the next start.
func (r *Runner) finish(ctx context.Context, epic string, seq int64, id string) {
	ended, ok := r.await(ctx, id)
	if !ok {
		return
	}
	if err := r.settle(ctx, epic, seq, id, ended); err != nil {
		r.logger.ErrorContext(ctx, "settle planner turn", "epic", epic, "turn", seq, "err", err)
	}
}

// stepWait is the state of one await of a planner step: its subscription, the one retry timer armed while a Subscribe
// or a Stop has failed, and whether the step has been stopped.
type stepWait struct {
	id      string
	sub     *enginehost.StepSubscription
	retry   <-chan time.Time
	disarm  func() bool
	stopped bool
}

// await waits until the step id ends and returns its row, and reports false once ctx has ended. It subscribes to the
// step's ends and holds, and never polls. A row that holds the step stops it, once. A Stop that fails is logged and arms
// one retry timer: when that fires and the step has still not ended, the Stop is tried again. A Subscribe that fails is
// logged and arms the same one-shot retry, and when that fires it subscribes again. The subscription is closed when
// await returns, and so is the armed timer.
func (r *Runner) await(ctx context.Context, id string) (store.StepRow, bool) {
	wait := &stepWait{id: id}
	defer wait.close()
	r.subscribeStep(ctx, wait)
	for {
		select {
		case <-ctx.Done():
			return store.StepRow{}, false
		case <-wait.ready():
			if row, ok := r.takeRows(ctx, wait); ok {
				return row, true
			}
		case <-wait.retry:
			if row, ok := r.retryStep(ctx, wait); ok {
				return row, true
			}
		}
	}
}

// takeRows takes the rows pending on the subscription of wait. It returns the first row that ends the step, if one is
// pending. Otherwise a pending hold stops the step, unless a Stop is already pending on the retry timer or the step is
// stopped.
func (r *Runner) takeRows(ctx context.Context, wait *stepWait) (store.StepRow, bool) {
	rows := wait.sub.Take()
	if row, ok := endedRow(rows); ok {
		return row, true
	}
	held := slices.ContainsFunc(rows, func(row store.StepRow) bool { return row.LoopLimited })
	if held && !wait.stopped && wait.retry == nil {
		r.stopStep(ctx, wait)
	}
	return store.StepRow{}, false
}

// retryStep runs when wait's retry timer fires. A step with no subscription subscribes again. Otherwise the step's end is
// taken if one is pending, and the step is stopped again if it has not ended.
func (r *Runner) retryStep(ctx context.Context, wait *stepWait) (store.StepRow, bool) {
	wait.retry, wait.disarm = nil, nil
	if wait.sub == nil {
		r.subscribeStep(ctx, wait)
		return store.StepRow{}, false
	}
	if row, ok := endedRow(wait.sub.Take()); ok {
		return row, true
	}
	r.stopStep(ctx, wait)
	return store.StepRow{}, false
}

// subscribeStep subscribes wait to its step. A Subscribe that fails is logged and arms the retry timer.
func (r *Runner) subscribeStep(ctx context.Context, wait *stepWait) {
	sub, err := r.steps.Subscribe(ctx, []string{wait.id})
	if err != nil {
		r.logger.ErrorContext(ctx, "subscribe to planner step", "step", wait.id, "err", err)
		r.armRetry(wait)
		return
	}
	wait.sub = sub
}

// stopStep stops the step of wait. A Stop that fails is logged and arms the retry timer; one that succeeds marks the step
// stopped, so no later hold stops it again.
func (r *Runner) stopStep(ctx context.Context, wait *stepWait) {
	if err := r.steps.Stop(ctx, wait.id); err != nil {
		r.logger.ErrorContext(ctx, "stop limited planner step", "step", wait.id, "err", err)
		r.armRetry(wait)
		return
	}
	wait.stopped = true
}

// armRetry arms the one retry timer of wait, of one tick.
func (r *Runner) armRetry(wait *stepWait) {
	wait.retry, wait.disarm = r.newTimer(r.tick)
}

// ready returns the channel that signals the pending rows of wait. It is nil while wait has no subscription, which blocks
// a select on it.
func (wait *stepWait) ready() <-chan struct{} {
	if wait.sub == nil {
		return nil
	}
	return wait.sub.Ready()
}

// close stops the armed retry timer, if one is armed, and closes the subscription, if there is one.
func (wait *stepWait) close() {
	if wait.disarm != nil {
		wait.disarm()
	}
	if wait.sub != nil {
		wait.sub.Close()
	}
}

// endedRow returns the first row in rows that ends its step, and whether there is one.
func endedRow(rows []store.StepRow) (store.StepRow, bool) {
	for _, row := range rows {
		if row.End != "" {
			return row, true
		}
	}
	return store.StepRow{}, false
}

// refuse settles turn seq of epic whose planner step did not start, by the error of its start. A refusal for the step
// cap frees the turn to rerun. An agent that does not resolve, or is not a valid agent name, frees the turn and parks
// the epic with the error text. Any other error fails the turn with its text.
func (r *Runner) refuse(ctx context.Context, epic string, seq int64, err error) {
	switch {
	case errors.Is(err, enginehost.ErrCapReached):
		r.release(ctx, epic, seq, store.TurnFree{Reason: stepCapFull})
	case errors.Is(err, agents.ErrUnresolved), errors.Is(err, enginehost.ErrBadAgent):
		r.release(ctx, epic, seq, store.TurnFree{Reason: err.Error(), Park: err.Error()})
	default:
		r.fail(ctx, epic, seq, err.Error())
	}
}

// settle settles turn seq of epic, whose step id ended as ended. A kept rejected rate limit of the step frees the turn
// as a limit with its reset time and pauses the epic, whatever the end. A done end completes the turn with the plan
// its return holds. A stopped step frees the turn and pauses the epic, and an engine_restarted step frees it to rerun.
// Every other end fails the turn with its end and reason. The read of the kept limit and the write that ends the turn
// are retried until they land (see retry).
func (r *Runner) settle(ctx context.Context, epic string, seq int64, id string, ended store.StepRow) error {
	var limit store.RateLimit
	var limited bool
	if err := r.retry(ctx, "read rate limit of step "+id, func() error {
		var readErr error
		if limit, limited, readErr = r.store.RateLimit(ctx, id); readErr != nil {
			return fmt.Errorf("read: %w", readErr)
		}
		return nil
	}); err != nil || ctx.Err() != nil {
		return err
	}
	if limited && limit.Status == rateLimitRejected {
		reason := fmt.Sprintf("%s rate limit, reset time unknown", limit.Kind)
		if !limit.ResetsAt.IsZero() {
			reason = fmt.Sprintf("%s rate limit, resets %s", limit.Kind, limit.ResetsAt.UTC().Format(time.RFC3339))
		}
		return r.freeTurn(ctx, epic, seq, store.TurnFree{
			Reason: reason,
			Pause:  true,
			Limit:  true,
			Resets: limit.ResetsAt,
		})
	}
	switch ended.End {
	case stepDone:
		return r.complete(ctx, epic, seq, ended.Result)
	case stepStopped:
		return r.freeTurn(ctx, epic, seq, store.TurnFree{Reason: stepStopped, Pause: true})
	case stepRestarted:
		return r.freeTurn(ctx, epic, seq, store.TurnFree{Reason: stepRestarted})
	default:
		return r.failTurn(ctx, epic, seq, ended.End+": "+ended.Reason)
	}
}

// complete completes turn seq of epic with the plan that result holds. A result that does not parse fails the turn, and
// so does a plan with a question the store refuses, with the refusal as the reason.
func (r *Runner) complete(ctx context.Context, epic string, seq int64, result string) error {
	ret, err := Parse(json.RawMessage(result))
	if err != nil {
		return r.failTurn(ctx, epic, seq, err.Error())
	}
	plan, at := planOf(ret), r.now()
	err = r.retry(ctx, fmt.Sprintf("complete turn %d of epic %s", seq, epic), func() error {
		return r.store.CompleteTurn(ctx, epic, seq, plan, at)
	})
	if errors.Is(err, store.ErrBadQuestion) {
		return r.failTurn(ctx, epic, seq, err.Error())
	}
	return err
}

// fail fails turn seq of epic with reason. An error the write ends with is logged.
func (r *Runner) fail(ctx context.Context, epic string, seq int64, reason string) {
	if err := r.failTurn(ctx, epic, seq, reason); err != nil {
		r.logger.ErrorContext(ctx, "fail planner turn", "epic", epic, "turn", seq, "err", err)
	}
}

// release frees turn seq of epic with free. An error the write ends with is logged.
func (r *Runner) release(ctx context.Context, epic string, seq int64, free store.TurnFree) {
	if err := r.freeTurn(ctx, epic, seq, free); err != nil {
		r.logger.ErrorContext(ctx, "free planner turn", "epic", epic, "turn", seq, "err", err)
	}
}

// failTurn ends turn seq of epic as failed with reason, retrying the write until it lands.
func (r *Runner) failTurn(ctx context.Context, epic string, seq int64, reason string) error {
	at := r.now()
	return r.retry(ctx, fmt.Sprintf("fail turn %d of epic %s", seq, epic), func() error {
		return r.store.FailTurn(ctx, epic, seq, reason, at)
	})
}

// freeTurn ends turn seq of epic without a return, as free describes, retrying the write until it lands.
func (r *Runner) freeTurn(ctx context.Context, epic string, seq int64, free store.TurnFree) error {
	at := r.now()
	return r.retry(ctx, fmt.Sprintf("free turn %d of epic %s", seq, epic), func() error {
		return r.store.FreeTurn(ctx, epic, seq, free, at)
	})
}

// retry runs op, a store call that settles a turn, until it succeeds or the store answers ErrTurnNotRunning or
// ErrBadQuestion, which no later call changes, and returns that error wrapped with what. Until then each failed call is
// logged and run again after a tick with the same arguments. Once ctx has ended, retry calls nothing more and returns
// nil, which leaves the turn running for the next start.
func (r *Runner) retry(ctx context.Context, what string, op func() error) error {
	for ctx.Err() == nil {
		err := op()
		if err == nil {
			return nil
		}
		err = fmt.Errorf("%s: %w", what, err)
		if errors.Is(err, store.ErrTurnNotRunning) || errors.Is(err, store.ErrBadQuestion) {
			return err
		}
		r.logger.ErrorContext(ctx, "retry planner turn write", "err", err)
		select {
		case <-ctx.Done():
		case <-time.After(r.tick):
		}
	}
	return nil
}

// planOf returns the plan a valid planner return stores.
func planOf(ret Return) store.EpicPlan {
	tasks := make([]store.EpicTaskInput, 0, len(ret.Tasks))
	for _, task := range ret.Tasks {
		tasks = append(tasks, store.EpicTaskInput{
			Title: task.Title, Deliverable: task.Deliverable, Files: task.Files,
			Requirements: task.Requirements, Contributes: task.Contributes, TestChanges: task.TestChanges,
			Gate: task.Gate, Evidence: task.Evidence,
		})
	}
	questions := make([]store.EpicQuestionInput, 0, len(ret.Questions))
	for _, question := range ret.Questions {
		questions = append(questions, questionInputOf(question))
	}
	actions := make([]store.EpicActionInput, 0, len(ret.Actions))
	for _, action := range ret.Actions {
		actions = append(actions, store.EpicActionInput{
			Kind: action.Kind, Node: action.Node, Task: action.Task, Close: action.Close, By: action.By,
			Requirement: action.Requirement, Item: action.Item, UndoNone: action.UndoNone, Reason: action.Reason,
		})
	}
	return store.EpicPlan{
		Decisions: ret.Decisions, Tasks: tasks, Outline: ret.Outline, Questions: questions, Outcome: ret.Outcome,
		Actions: actions,
	}
}

// questionInputOf returns the store's input for a question of a planner return. A non-empty why is stored as "Catastrophe
// <kind>: <why>", so the owner reads which catastrophe the question names. An empty why stays empty, which the store
// refuses.
func questionInputOf(question Question) store.EpicQuestionInput {
	choices := make([]store.EpicChoice, 0, len(question.Choices))
	for _, choice := range question.Choices {
		choices = append(choices, store.EpicChoice{ID: choice.ID, Does: choice.Does, Closes: closesOf(choice.Closes)})
	}
	why := question.Why
	if why != "" {
		why = "Catastrophe " + question.Catastrophe + ": " + why
	}
	return store.EpicQuestionInput{
		Label: question.Label, Text: question.Text, Choices: choices, Recommended: question.Recommended,
		Why: why, Confidence: question.Confidence, Replaces: question.Replaces,
	}
}

// returnCheck returns the check a planner step's return runs for epic. A return that does not parse is one line.
// Any other return gets the refusals of Check, against the epic's Requirement ids, the files of its open tasks, and the
// gates and judges that resolve answers for epic, then the refusals of Coverage against the epic's tasks, then those of
// checkAmendKeeps against the epic's Requirements, then those of checkClosureTasks against the epic's phase, then those
// of checkCloses against the epic's phase and the findings of review that the next closure round checks, then those of
// checkSpentLines against spent, the lines of failed tasks the turn may not try again. When epic holds a planner-class
// park, a return with no action and no question gets one more line from checkPark, which names the park reason. An
// owner park is not checked, since the owner answers it. The gate resolves under ctx without its cancellation, so the
// turn that started the step ending does not refuse its return.
func returnCheck(
	ctx context.Context, epic store.EpicRow, review store.Review, resolve Resolver, spent []SpentLine,
) func(value json.RawMessage) []string {
	requirements := make([]string, 0, len(epic.Requirements))
	for _, requirement := range epic.Requirements {
		requirements = append(requirements, requirement.ID)
	}
	findings := kinds.OpenFindings(review, int64(len(review.Rounds))+1)
	open := make([]string, 0, len(findings))
	for _, finding := range findings {
		open = append(open, finding.ID)
	}
	owned := openFiles(epic.Tasks)
	park := ""
	if !epic.ParkClass.OwnerHeld() {
		park = epic.Parked
	}
	gateCtx := context.WithoutCancel(ctx)
	gate := func(name string) error { return resolve.Gate(gateCtx, epic, name) }
	judge := func(name string) error { return resolve.Judge(epic, name) }
	return func(value json.RawMessage) []string {
		ret, err := Parse(value)
		if err != nil {
			return []string{err.Error()}
		}
		refusals := append(Check(ret, requirements, owned, gate, judge), Coverage(ret, requirements, epic.Tasks)...)
		refusals = append(refusals, checkAmendKeeps(ret, epic.Requirements)...)
		refusals = append(refusals, checkClosureTasks(ret, epic.Phase)...)
		refusals = append(refusals, checkCloses(ret, epic.Phase, open)...)
		refusals = append(refusals, checkSpentLines(ret, spent, epic.Tasks)...)
		return append(refusals, checkPark(ret, park)...)
	}
}

// openFiles returns the files of the tasks whose status is open, in task order.
func openFiles(tasks []store.EpicTask) []string {
	var files []string
	for _, task := range tasks {
		if task.Status == taskOpen {
			files = append(files, task.Files...)
		}
	}
	return files
}
