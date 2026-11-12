package enginehost

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepSessions is what a step run drives: its session starts, is interrupted, takes a follow-up message, and stops.
// Manager satisfies it.
type stepSessions interface {
	StartStep(ctx context.Context, step StepSession) error
	Interrupt(ctx context.Context, id string) error
	Continue(ctx context.Context, id, text string) error
	Stop(ctx context.Context, id string) error
}

// The ends a step run stores.
const (
	stepLoopEndDone         = "done"
	stepLoopEndReturned     = "returned"
	stepLoopEndChecksFailed = "checks_failed"
	stepLoopEndFailed       = "failed"
)

// The names of the checks a step loop stores with each return. stepLoopGateName is the gate a writing step's done return
// runs after its commit checks pass.
const (
	stepLoopSchemaName = "schema"
	stepLoopGateName   = "gate"
)

// stepLoopMaxContinues is how many failing returns the session is sent back to fix. The next failure ends checks_failed.
const stepLoopMaxContinues = 3

// stepLoopBuffer is the capacity of the returns and exits channels. A send waits only while the buffer is full.
const stepLoopBuffer = 8

// stepRunConfig is what a stepRun is built from. A writing step may change its worktree: its done return is checked
// with commitChecks against start and owned in dir, and with gate when every commit check passes and gate is set. A
// read-only step's done return passes on its schema check alone. A nil cleanup has nothing to run. The watch reads now
// as its clock, time.Now when now is nil, and allows the step modelLimit of model time. Each tick on ticks checks the
// watch, and a nil ticks never ticks. A nil haiku makes no monitor or judge calls. prompt is the step's first prompt,
// which the judge is given with each done return.
type stepRunConfig struct {
	id         string
	schema     string
	doneValue  string
	prompt     string
	sessions   stepSessions
	writer     *Writer
	logger     *slog.Logger
	cleanup    func(context.Context) error
	now        func() time.Time
	modelLimit time.Duration
	haiku      haikuCaller
	ticks      <-chan time.Time

	writing   bool
	dir       string
	start     string
	owned     []string
	gate      []string
	gateLimit time.Duration
	locks     []string
	environ   []string
}

// stepRun runs one step's loop. Each return is one round: it is checked and stored with its checks, and a failing return
// is sent back to the session by Continue, up to stepLoopMaxContinues times. A passing return ends the step done when
// its status is doneValue and returned otherwise. Exited ends it failed.
type stepRun struct {
	stepRunConfig

	// watch decides the step's backstops from every event the run is handed.
	watch   *stepWatch
	returns chan session.TurnEnded
	exits   chan string
	// nudge is the text the next turn end is continued with when that turn is an error. It is set by a landed nudge's
	// interrupt and cleared by the next turn end, whatever it is.
	nudge string
	// judgeSent is set once the judge has sent the step back, so a second unmet verdict ends goal_unmet.
	judgeSent bool
	// ended is closed once finish has acted.
	ended chan struct{}
	// runDone is closed when run returns, so Return and Exited never wait on a run that will not drain them.
	runDone chan struct{}
	once    sync.Once
}

// newStepRun returns a stepRun built from cfg that is not running. Call run to start it.
func newStepRun(cfg stepRunConfig) *stepRun {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	return &stepRun{
		stepRunConfig: cfg,
		watch:         newStepWatch(cfg.now(), cfg.modelLimit),
		returns:       make(chan session.TurnEnded, stepLoopBuffer),
		exits:         make(chan string, stepLoopBuffer),
		ended:         make(chan struct{}),
		runDone:       make(chan struct{}),
	}
}

// Event hands an event the session recorded to the watch, with the clock's reading now. It never blocks.
func (r *stepRun) Event(event session.Event) {
	r.watch.event(r.now(), event)
}

// Return hands the structured return of the session's ended turn to run. It returns once run has queued it, the step has
// ended, or run has returned.
func (r *stepRun) Return(ended session.TurnEnded) {
	select {
	case r.returns <- ended:
	case <-r.ended:
	case <-r.runDone:
	}
}

// Exited hands the session's exit to run with its reason. It returns once run has queued it, the step has ended, or
// run has returned.
func (r *stepRun) Exited(reason string) {
	select {
	case r.exits <- reason:
	case <-r.ended:
	case <-r.runDone:
	}
}

// run handles returns and exits until the step ends or ctx ends. A ctx that ends stores no end.
func (r *stepRun) run(ctx context.Context) {
	defer close(r.runDone)
	continues := 0
	round := 0
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-r.exits:
			r.finish(ctx, stepLoopEndFailed, "", reason)
			return
		case <-r.ticks:
			if !r.watchTick(ctx) {
				return
			}
		case ended := <-r.returns:
			nudge := r.nudge
			r.nudge = ""
			if nudge != "" && ended.IsError {
				if err := r.sessions.Continue(ctx, r.id, nudge); err != nil {
					r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("continue step %s: %v", r.id, err))
					return
				}
				continue
			}
			round++
			if !r.judge(ctx, round, ended.StructuredOutput, &continues) {
				return
			}
		}
	}
}

// judge checks the return of round and stores it with its checks. A done return whose checks all passed is judged; any
// other return ends the step, or its failed checks are sent back. It reports whether the run goes on.
func (r *stepRun) judge(ctx context.Context, round int, value json.RawMessage, continues *int) bool {
	schemaErr := validateReturn(r.schema, value)
	schema := store.StepCheck{Round: round, Name: stepLoopSchemaName, OK: schemaErr == nil}
	if schemaErr != nil {
		schema.Detail = schemaErr.Error()
	}
	checks := []store.StepCheck{schema}
	var head struct {
		Status string          `json:"status"`
		Commit json.RawMessage `json:"commit"`
	}
	if schemaErr == nil {
		if err := json.Unmarshal(value, &head); err != nil {
			r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("read status of step %s: %v", r.id, err))
			return false
		}
		if head.Status == r.doneValue && r.writing {
			var err error
			if checks, err = r.doneChecks(ctx, round, stepLoopCommit(head.Commit), checks); err != nil {
				r.finish(ctx, stepLoopEndFailed, "", err.Error())
				return false
			}
		}
	}
	ret := store.StepReturn{ID: r.id, Result: string(value), Checks: checks}
	if err := r.writer.Commit(ctx, r.id, ret); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record return of step %s: %v", r.id, err))
		return false
	}
	if len(stepLoopFailures(checks)) > 0 {
		return r.retry(ctx, checks, continues)
	}
	if head.Status == r.doneValue {
		return r.judgeReturn(ctx, round, value, stepLoopCommit(head.Commit), head.Status, checks)
	}
	r.finish(ctx, stepLoopEndReturned, head.Status, "")
	return false
}

// doneChecks appends the checks of a writing step's done return to checks. The four commit checks run first. The gate
// runs only when they all pass and a gate is set. A gate that cannot run is an error.
func (r *stepRun) doneChecks(ctx context.Context, round int, commit string,
	checks []store.StepCheck) ([]store.StepCheck, error) {
	passed := true
	for _, c := range commitChecks(ctx, r.dir, r.start, commit, r.owned) {
		checks = append(checks, stepLoopRecord(round, c))
		passed = passed && c.OK
	}
	if !passed || len(r.gate) == 0 {
		return checks, nil
	}
	result, err := runGate(ctx, gateRun{
		Argv: r.gate, Dir: r.dir, Limit: r.gateLimit, Locks: r.locks, Environ: r.environ,
	})
	if err != nil {
		return checks, fmt.Errorf("run gate of step %s: %w", r.id, err)
	}
	return append(checks, stepLoopGateCheck(round, result, r.gateLimit)), nil
}

// stepLoopCommit returns the commit a done return names. A missing or non-string commit names none, which the head
// check then fails, so the return goes back like any other failure.
func stepLoopCommit(raw json.RawMessage) string {
	var commit string
	if err := json.Unmarshal(raw, &commit); err != nil {
		return ""
	}
	return commit
}

// stepLoopRecord returns c as a check stored with round.
func stepLoopRecord(round int, c stepCheck) store.StepCheck {
	return store.StepCheck{Round: round, Name: c.Name, OK: c.OK, Detail: c.Detail}
}

// stepLoopGateCheck returns the gate check of round for result. It passes when the gate exits 0 without passing limit.
// Its detail gives the exit status, or the limit when the gate was killed for it, and the tail of the gate's output.
func stepLoopGateCheck(round int, result gateResult, limit time.Duration) store.StepCheck {
	check := store.StepCheck{Round: round, Name: stepLoopGateName, OK: !result.TimedOut && result.Exit == 0}
	if result.TimedOut {
		check.Detail = fmt.Sprintf("gate ran past its limit of %s and was killed", limit)
	} else {
		check.Detail = fmt.Sprintf("gate exited %d", result.Exit)
	}
	if tail := strings.TrimSpace(result.Output); tail != "" {
		check.Detail += "; output tail: " + tail
	}
	return check
}

// retry sends the failed checks back to the session while it has continues left, and otherwise ends checks_failed. It
// reports whether the run goes on.
func (r *stepRun) retry(ctx context.Context, checks []store.StepCheck, continues *int) bool {
	failures := stepLoopFailures(checks)
	if *continues == stepLoopMaxContinues {
		r.finish(ctx, stepLoopEndChecksFailed, "", strings.Join(failures, "; "))
		return false
	}
	*continues++
	text := "Your return failed these checks. Return again with a result that passes them:\n- " +
		strings.Join(failures, "\n- ")
	if err := r.sessions.Continue(ctx, r.id, text); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("continue step %s: %v", r.id, err))
		return false
	}
	return true
}

// stepLoopFailures names each failed check with its detail.
func stepLoopFailures(checks []store.StepCheck) []string {
	var failures []string
	for _, check := range checks {
		if !check.OK {
			failures = append(failures, check.Name+": "+check.Detail)
		}
	}
	return failures
}

// finish ends the step once. It stores StepEnd, stops the session, runs cleanup and closes ended. A store, stop or cleanup
// error is logged, since nothing above finish can act on it. A ctx that has ended stores no end, so finish does nothing
// then. It reports whether this call acted.
func (r *stepRun) finish(ctx context.Context, end, status, reason string) bool {
	if ctx.Err() != nil {
		return false
	}
	// Once the end is decided it is carried out whole: a cancel arriving now, as from a Stop or Close racing this end,
	// must not cut off the stored end, the session stop or the worktree removal.
	ctx = context.WithoutCancel(ctx)
	acted := false
	r.once.Do(func() {
		acted = true
		defer close(r.ended)
		stepEnd := store.StepEnd{ID: r.id, End: end, Status: status, Reason: reason}
		if err := r.writer.Commit(ctx, r.id, stepEnd); err != nil {
			r.logger.ErrorContext(ctx, "record step end", "step", r.id, "err", err)
		}
		if err := r.sessions.Stop(ctx, r.id); err != nil {
			r.logger.ErrorContext(ctx, "stop step session", "step", r.id, "err", err)
		}
		if r.cleanup != nil {
			if err := r.cleanup(ctx); err != nil {
				r.logger.ErrorContext(ctx, "clean up step", "step", r.id, "err", err)
			}
		}
	})
	return acted
}
