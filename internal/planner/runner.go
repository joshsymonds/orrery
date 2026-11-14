package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/agents"
	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// Store is the store a Runner reads and settles planner turns through. *store.Store satisfies it.
type Store interface {
	PlanStates(ctx context.Context) ([]store.EpicPlanState, error)
	ClaimTurn(ctx context.Context, epic string, at time.Time) (store.EpicTurn, error)
	SetTurnStep(ctx context.Context, epic string, turn int64, step string) error
	Epic(ctx context.Context, id string) (store.EpicRow, error)
	TurnTriggers(ctx context.Context, epic string, turn int64) ([]store.EpicTrigger, error)
	CompleteTurn(ctx context.Context, epic string, turn int64, plan store.EpicPlan, at time.Time) error
	FailTurn(ctx context.Context, epic string, turn int64, reason string, at time.Time) error
	FreeTurn(ctx context.Context, epic string, turn int64, free store.TurnFree, at time.Time) error
	ParkEpic(ctx context.Context, epic string, reason string) error
	RunningTurns(ctx context.Context) ([]store.EpicTurn, error)
	RateLimit(ctx context.Context, session string) (store.RateLimit, bool, error)
}

// Steps starts planner steps and waits for them. *enginehost.Steps satisfies it.
type Steps interface {
	Start(ctx context.Context, start enginehost.StepStart) (string, error)
	Status(ctx context.Context, id string) (store.StepRow, error)
	Wait(ctx context.Context, ids []string, timeout time.Duration) (*store.StepRow, []store.StepRow, error)
}

// Config configures a Runner. A Tick that is not positive means five seconds, and a nil Now means time.Now. A nil
// Logger discards.
type Config struct {
	Store  Store
	Steps  Steps
	Agent  func() (string, error)
	Tick   time.Duration
	Now    func() time.Time
	Logger *slog.Logger
}

// defaultTick is how often a Runner passes when its Config names no positive tick.
const defaultTick = 5 * time.Second

// waitInterval is how long one Wait on a turn's step lasts before the turn waits again.
const waitInterval = time.Minute

// stepDone is the end of a step whose return passed its check.
const stepDone = "done"

// stepStopped is the end of a step ended by Stop, and stepRestarted the end Reconcile stores on an open step row.
const (
	stepStopped   = "stopped"
	stepRestarted = "engine_restarted"
)

// rateLimitRejected is the status of a rate limit that refused a session's request.
const rateLimitRejected = "rejected"

// stepCapFull is the reason a turn is freed with when the step cap refuses its step, and noStep the reason a running
// turn with no step is freed with on recovery.
const (
	stepCapFull = "step cap full"
	noStep      = "no step"
)

// taskDone is the status of a finished task. Its files are no longer owned by an unfinished task.
const taskDone = "done"

// Runner passes over the epics with pending triggers and runs their planner turns. Each claimed turn runs in its own
// goroutine, and turns tracks them until Run returns.
type Runner struct {
	store  Store
	steps  Steps
	agent  func() (string, error)
	tick   time.Duration
	now    func() time.Time
	logger *slog.Logger
	turns  sync.WaitGroup
}

// New returns a Runner for cfg.
func New(cfg Config) *Runner {
	runner := &Runner{
		store: cfg.Store, steps: cfg.Steps, agent: cfg.Agent, tick: cfg.Tick, now: cfg.Now, logger: cfg.Logger,
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

// Run settles the turns a previous orreryd left running, then passes at once, then once per tick, until ctx ends. It
// then waits for the turns it started and returns.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	r.recoverTurns(ctx)
	for {
		r.pass(ctx)
		select {
		case <-ctx.Done():
			r.turns.Wait()
			return
		case <-ticker.C:
		}
	}
}

// pass decides each epic with pending triggers. A park action parks the epic with its reason, and a claim action
// claims a turn for the epic in its own goroutine. agent is read once per pass.
func (r *Runner) pass(ctx context.Context) {
	states, err := r.store.PlanStates(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "list planner states", "err", err)
		return
	}
	agent, roleErr := r.agent()
	for _, state := range states {
		action := Decide(State{
			Paused: state.Paused, Parked: state.Parked != "", Running: state.Running, Pending: state.Pending,
		}, roleErr)
		switch action.Kind {
		case ActionPark:
			if err = r.store.ParkEpic(ctx, state.Epic, action.Reason); err != nil {
				r.logger.ErrorContext(ctx, "park epic", "epic", state.Epic, "err", err)
			}
		case ActionClaim:
			r.turns.Add(1)
			go r.runTurn(ctx, state.Epic, agent)
		default:
		}
	}
}

// runTurn claims a turn for epic, runs its planner step to an end and settles the turn. A turn whose context ends is
// left running for the next start.
func (r *Runner) runTurn(ctx context.Context, epic, agent string) {
	defer r.turns.Done()
	turn, err := r.store.ClaimTurn(ctx, epic, r.now())
	if errors.Is(err, store.ErrNotClaimable) {
		return
	}
	if err != nil {
		r.logger.ErrorContext(ctx, "claim planner turn", "epic", epic, "err", err)
		return
	}
	id, err := r.startStep(ctx, epic, turn, agent)
	if err != nil {
		r.refuse(ctx, epic, turn.Seq, err)
		return
	}
	r.finish(ctx, epic, turn.Seq, id)
}

// startStep reads the claimed turn's epic and triggers and starts its read-only planner step. It returns the step's
// id. A start that returns an id with an error still has its id recorded on the turn.
func (r *Runner) startStep(ctx context.Context, epic string, turn store.EpicTurn, agent string) (string, error) {
	row, err := r.store.Epic(ctx, epic)
	if err != nil {
		return "", fmt.Errorf("read epic %s: %w", epic, err)
	}
	triggers, err := r.store.TurnTriggers(ctx, epic, turn.Seq)
	if err != nil {
		return "", fmt.Errorf("read triggers of turn %d of epic %s: %w", turn.Seq, epic, err)
	}
	id, err := r.steps.Start(ctx, enginehost.StepStart{
		Agent:       agent,
		Prompt:      Prompt(row, turn.Seq, triggers),
		Schema:      Schema,
		Workspace:   row.Repository,
		ReadOnly:    true,
		Epic:        row.ID,
		ReturnCheck: returnCheck(row),
	})
	if id != "" {
		if setErr := r.store.SetTurnStep(ctx, epic, turn.Seq, id); setErr != nil {
			r.logger.ErrorContext(ctx, "record step of planner turn",
				"epic", epic, "turn", turn.Seq, "step", id, "err", setErr)
		}
	}
	if err != nil {
		return id, fmt.Errorf("start planner step: %w", err)
	}
	return id, nil
}

// recoverTurns settles each turn a previous orreryd left running, before the first pass. A turn with no step is freed
// with noStep. A turn whose step has ended is settled by that end, and a turn whose step is still open is waited on in
// its own goroutine. A turn whose step cannot be read stays running and is logged; it still counts as running, so its
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
		if turn.Step == "" {
			r.release(ctx, turn.Epic, turn.Seq, store.TurnFree{Reason: noStep})
			continue
		}
		row, readErr := r.steps.Status(ctx, turn.Step)
		if readErr != nil {
			r.logger.ErrorContext(ctx, "read step of running planner turn",
				"epic", turn.Epic, "turn", turn.Seq, "step", turn.Step, "err", readErr)
			continue
		}
		if row.End == "" {
			r.turns.Add(1)
			go r.resume(ctx, turn.Epic, turn.Seq, turn.Step)
			continue
		}
		if err = r.settle(ctx, turn.Epic, turn.Seq, turn.Step, row); err != nil {
			r.logger.ErrorContext(ctx, "settle planner turn", "epic", turn.Epic, "turn", turn.Seq, "err", err)
		}
	}
}

// resume settles turn seq of epic by the end of step id, which a previous orreryd left open.
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

// await waits until the step id ends and returns its row, and reports false once ctx has ended. Each Wait lasts a
// minute, and a Wait that times out is repeated. A Wait that fails is logged and repeated after a tick, since the step
// may still be running.
func (r *Runner) await(ctx context.Context, id string) (store.StepRow, bool) {
	for {
		ended, _, err := r.steps.Wait(ctx, []string{id}, waitInterval)
		if ctx.Err() != nil {
			return store.StepRow{}, false
		}
		if err != nil {
			r.logger.ErrorContext(ctx, "wait for planner step", "step", id, "err", err)
			select {
			case <-ctx.Done():
				return store.StepRow{}, false
			case <-time.After(r.tick):
			}
			continue
		}
		if ended != nil {
			return *ended, true
		}
	}
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
// and pauses the epic, whatever the end. A done end completes the turn with the plan its return holds. A stopped step
// frees the turn and pauses the epic, and an engine_restarted step frees it to rerun. Every other end fails the turn
// with its end and reason. The write that ends the turn is retried until it lands (see retry).
func (r *Runner) settle(ctx context.Context, epic string, seq int64, id string, ended store.StepRow) error {
	limit, limited, err := r.store.RateLimit(ctx, id)
	if err != nil {
		return fmt.Errorf("read rate limit of step %s: %w", id, err)
	}
	if limited && limit.Status == rateLimitRejected {
		return r.freeTurn(ctx, epic, seq, store.TurnFree{
			Reason: fmt.Sprintf("%s rate limit, resets %s", limit.Kind, limit.ResetsAt.UTC().Format(time.RFC3339)),
			Pause:  true,
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

// complete completes turn seq of epic with the plan that result holds. A result that does not parse fails the turn.
func (r *Runner) complete(ctx context.Context, epic string, seq int64, result string) error {
	ret, err := Parse(json.RawMessage(result))
	if err != nil {
		return r.failTurn(ctx, epic, seq, err.Error())
	}
	plan, at := planOf(ret), r.now()
	return r.retry(ctx, fmt.Sprintf("complete turn %d of epic %s", seq, epic), func() error {
		return r.store.CompleteTurn(ctx, epic, seq, plan, at)
	})
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

// retry runs write, a store write that ends a turn, until it succeeds or the store answers ErrTurnNotRunning, which it
// returns wrapped with what. Until then each failed write is logged and run again after a tick with the same arguments.
// Once ctx has ended, retry writes nothing more and returns nil, which leaves the turn running for the next start.
func (r *Runner) retry(ctx context.Context, what string, write func() error) error {
	for ctx.Err() == nil {
		err := write()
		if err == nil {
			return nil
		}
		err = fmt.Errorf("%s: %w", what, err)
		if errors.Is(err, store.ErrTurnNotRunning) {
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
			Requirements: task.Requirements, Check: task.Check,
		})
	}
	return store.EpicPlan{
		Decisions: ret.Decisions, Tasks: tasks, Outline: ret.Outline, Questions: ret.Questions, Outcome: ret.Outcome,
	}
}

// returnCheck returns the check a planner step's return runs for epic. A return that does not parse is one line.
// Any other return gets the refusals of Check, against the epic's Requirement ids and the files of its tasks that are
// not done.
func returnCheck(epic store.EpicRow) func(value json.RawMessage) []string {
	requirements := make([]string, 0, len(epic.Requirements))
	for _, requirement := range epic.Requirements {
		requirements = append(requirements, requirement.ID)
	}
	owned := unfinishedFiles(epic.Tasks)
	return func(value json.RawMessage) []string {
		ret, err := Parse(value)
		if err != nil {
			return []string{err.Error()}
		}
		return Check(ret, requirements, owned)
	}
}

// unfinishedFiles returns the files of the tasks whose status is not done, in task order.
func unfinishedFiles(tasks []store.EpicTask) []string {
	var files []string
	for _, task := range tasks {
		if task.Status != taskDone {
			files = append(files, task.Files...)
		}
	}
	return files
}
