package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// StepPrompt is a prompt sent to a step session as its next message. Sender is who sent it, which the engine stores with
// it: brief, engine or owner. Round is the round of a send-back, and zero when the prompt carries none. PromptID is the
// client-chosen id of the prompt, and empty when it carries none. Loop, when set, is the step's loop state, stored in
// the one write that stores the prompt, so a restart never finds the one without the other.
type StepPrompt struct {
	Text     string
	Sender   string
	Round    int
	PromptID string
	Loop     *store.StepLoop
}

// stepSessions is what a step run drives: its session starts, is interrupted, takes a follow-up message, is released, and
// stops. Manager satisfies it.
type stepSessions interface {
	StartStep(ctx context.Context, step StepSession) error
	Interrupt(ctx context.Context, id string) error
	Continue(ctx context.Context, id string, prompt StepPrompt) error
	// Release stops the process of a live step without ending the step, so a later Continue respawns it.
	Release(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
}

// The ends a step run stores.
const (
	stepLoopEndDone         = "done"
	stepLoopEndReturned     = "returned"
	stepLoopEndChecksFailed = "checks_failed"
	stepLoopEndFailed       = "failed"
	stepLoopEndGateFailed   = "gate_failed"
)

// The names of the checks a step loop stores with each return. stepLoopGateName is the gate a writing step's done
// return runs after its commit checks pass. stepLoopAssertionsName is the check an agent gate step's schema-valid
// return passes against the gate's assertions. stepLoopReturnName is the check a step's return check stores.
const (
	stepLoopSchemaName     = "schema"
	stepLoopGateName       = "gate"
	stepLoopAssertionsName = "assertions"
	stepLoopReturnName     = "return"
)

// stepGateLogTail is the number of newest bytes of each of its stdout and stderr a step's gate keeps: a failed gate's log
// keeps them all.
const stepGateLogTail = 1 << 20

// stepGateCheckTail is the number of newest bytes of each of the gate's streams its check keeps as its stdout and stderr
// tails, which step status shows and the judge and a send-back carry.
const stepGateCheckTail = 8 << 10

// gateLogsName is the directory under the gate state directory that keeps the logs of failed step gates, one directory
// per step.
const gateLogsName = "logs"

// gateLogDirMode and gateLogFileMode are the modes of the gate log directories and files: the owner's alone, as the
// output may hold what the redaction does not catch.
const (
	gateLogDirMode  fs.FileMode = 0o700
	gateLogFileMode fs.FileMode = 0o600
)

// stepLoopMaxContinues is how many failing returns the session is sent back to fix. The next failure ends checks_failed.
const stepLoopMaxContinues = 3

// stepLoopBuffer is the capacity of the returns and exits channels. A send waits only while the buffer is full.
const stepLoopBuffer = 8

// stepCheckRetry is how long a check that finds a trip still due waits after the last check before it runs again. Such a
// trip is a tool call past its limit that a nudge could not interrupt, which the watch keeps finding past its limit.
const stepCheckRetry = 5 * time.Second

// apiErrorRateLimit is the API error an assistant message carries on a turn that hit a usage limit.
const apiErrorRateLimit = "rate_limit"

// stepRunConfig is what a stepRun is built from. A writing step may change its worktree: its done return is checked
// with commitChecks against start and owned in dir, without its owned check when unowned, and with gate when every
// commit check passes and gate is set. gate
// runs holding the members of grant that pools grants, and its check records gateName and gateSource; a nil pools fails
// the gate. countLines counts the lines a done return's commit changes from start in dir, and newStepRun makes it
// stepGitLines when nil. stateDir and scope are the state directory and the systemd-run scope the gate's holder runs
// under. A read-only
// step runs in dir too, and each of its returns is compared with checkout, the state its checkout was in at its start; a
// moved checkout ends it checks_failed. A read-only step's done return passes on its schema check alone. A nil cleanup
// has nothing to run. The watch reads now as its clock, time.Now when now is nil, and allows the step modelLimit of model
// time. The run's deadline timer is armed with newTimer, and when it fires the loop checks the watch and the agent
// gate's limit; a nil newTimer arms none. A nil haiku makes no monitor or judge calls.
// prompt is the step's first prompt, which the judge is given with each done return. agentGate is the agent gate a step
// runs, nil for any other step: each schema-valid return is checked against its assertions, and never judged. A check at
// or past the limit of wall time from the run's start ends an agent gate step overtime. returnCheck, when set, checks
// every schema-valid return: its failure lines are stored as the return check and sent back, and a step with one is
// never judged. publish is called with the step's id once the run's end or hold is stored, and a nil publish, which
// newStepRun makes a no-op, reports nothing. wake is called after each prompt the run sends its session and after each
// resume of a limited run, so a caller event that waited on the step is offered again, and a nil wake does nothing.
type stepRunConfig struct {
	id          string
	schema      string
	doneValue   string
	prompt      string
	sessions    stepSessions
	store       StepStore
	writer      *Writer
	logger      *slog.Logger
	cleanup     func(context.Context) error
	now         func() time.Time
	modelLimit  time.Duration
	haiku       haikuCaller
	newTimer    func(time.Duration) (<-chan time.Time, func() bool)
	agentGate   *gates.Gate
	returnCheck func(value json.RawMessage) []string
	publish     func(ctx context.Context, id string)
	wake        func()

	writing    bool
	dir        string
	checkout   stepCheckout
	start      string
	owned      []string
	unowned    bool
	countLines func(ctx context.Context, dir, start, commit string) (store.StepLines, error)
	gate       []string
	gateLimit  time.Duration
	environ    []string
	pools      PoolCommands
	grant      grants.Request
	gateName   string
	gateSource string
	stateDir   string
	scope      string

	// restore, when set, is the state a re-adopted step resumes from. run takes it up before it takes any return or
	// exit.
	restore *stepRestore
}

// stepRestore is the state a re-adopted step resumes from: its row as the store holds it, and the turn end its session
// last ended after its last prompt. Turn is that turn end when Found is set, and Found is what store.StepTurn returns:
// unset means the turn the last prompt started is still running.
type stepRestore struct {
	Row   store.StepRow
	Turn  store.StepTurnEnd
	Found bool
}

// stepRun runs one step's loop. Each return is one round: it is checked and stored with its checks, and a failing
// return is sent back to the session by Continue, up to stepLoopMaxContinues times. A passing return ends the step done
// when its status is doneValue and returned otherwise, or, for an agent gate step, done or gate_failed by its verdict.
// Exited ends it failed.
type stepRun struct {
	stepRunConfig

	// watch decides the step's backstops from every event the run is handed.
	watch *stepWatch
	// started is the clock reading the run was built at. An agent gate step's limit counts its wall time from it, or
	// from gateFrom once set.
	started time.Time
	// gateFrom is the clock reading an agent gate step was last resumed from a usage limit at, zero before any resume.
	// Its limit counts its wall time from it, so the wait for a limit's reset never ends the step overtime.
	gateFrom time.Time
	returns  chan session.TurnEnded
	exits    chan string
	// nudge is the text the next turn end is continued with when that turn is an error. It is set by a landed nudge's
	// interrupt and cleared by the next turn end, whatever it is.
	nudge string
	// ownerNudge is the owner's text the next turn end is continued with, whatever that turn end is. It is set by a
	// landed owner nudge and cleared by the next turn end. It is joined after nudge when it is sent.
	ownerNudge string
	// heldEnded is set when a held run's turn end has been handled, so an owner nudge continues the step at once.
	heldEnded bool
	// rateLimit is set when the session hands the run an assistant message that carries the rate limit error. Event sets it
	// on the session's goroutine, and the next turn end clears it.
	rateLimit atomic.Bool
	// limited is set once a turn has ended on a usage limit. A limited run spends nothing, arms no timer and ignores its
	// exits. limitKind and limitResets are the kind and reset the limit was stored with, empty and zero when unknown.
	limited     bool
	limitKind   string
	limitResets time.Time
	// limitReleased is set by Steps.Stop when the step's stored row is limited: its process was released for the limit, so
	// finish stops no session. Nothing else sets it, since a failed resume may leave a respawned process live.
	limitReleased bool
	// judgeSent is set once the judge has sent the step back, so a second unmet verdict ends goal_unmet.
	judgeSent bool
	// judgedCommit is the commit of the done return the judge sent back, which passed every check. It is kept in memory
	// only, so a run re-adopted after a restart has none.
	judgedCommit string
	// round is the number of the step's latest stored round. A turn end that is no continuation is the next round, which
	// judge checks, and it becomes round once its return is stored, so round is always the round the store holds.
	round int
	// stage is the stage the loop is in, and stageFrom the clock reading it started at. setStage moves both, and every
	// loop-state write stores them.
	stage     string
	stageFrom time.Time
	// continues is how many failing returns the session has been sent back to fix, up to stepLoopMaxContinues.
	continues int
	// resumeGate is the gate a restored step's row stores as running: its grant and holder, and the round it runs in.
	// runDoneGate re-adopts it in that round, and clears it then.
	resumeGate store.StepGate
	// ended is closed once finish has acted.
	ended chan struct{}
	// runDone is closed when run returns, so Return and Exited never wait on a run that will not drain them.
	runDone chan struct{}
	// calls hands owner actions to run, which runs each in its goroutine and replies with its error.
	calls chan stepOwnerCall
	// held is set when a hold lands. A held run checks nothing and arms no timer, and its turn ends are no round.
	// Run sets it and Steps.Held reads it.
	held atomic.Bool
	// wakes is a one-slot channel Event signals after it hands an event to the watch, so the loop re-arms its deadline.
	wakes chan struct{}
	// deadline is the channel of the armed deadline timer, nil while none is armed. stopDeadline stops that timer, and
	// deadlineAt is the moment it is armed for.
	deadline     <-chan time.Time
	stopDeadline func() bool
	deadlineAt   time.Time
	// checked is the clock reading the deadline last fired at, zero before it fires.
	checked time.Time
	// gateMu guards gating and gateStart. gateStart is closed when a gate begins, which releases every owner action
	// waiting for the loop, and replaced when the gate ends.
	gateMu    sync.Mutex
	gating    bool
	gateStart chan struct{}
	once      sync.Once
}

// newStepRun returns a stepRun built from cfg that is not running. Call run to start it.
func newStepRun(cfg stepRunConfig) *stepRun {
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.publish == nil {
		cfg.publish = func(context.Context, string) {}
	}
	if cfg.countLines == nil {
		cfg.countLines = stepGitLines
	}
	start := cfg.now()
	return &stepRun{
		stepRunConfig: cfg,
		watch:         newStepWatch(start, cfg.modelLimit),
		started:       start,
		stage:         store.StepStageTurn,
		stageFrom:     start,
		returns:       make(chan session.TurnEnded, stepLoopBuffer),
		exits:         make(chan string, stepLoopBuffer),
		ended:         make(chan struct{}),
		runDone:       make(chan struct{}),
		calls:         make(chan stepOwnerCall),
		gateStart:     make(chan struct{}),
		wakes:         make(chan struct{}, 1),
	}
}

// Event hands an event the session recorded to the watch, with the clock's reading now, and notes an assistant message
// that carries the rate limit error for the next turn end. It signals the loop to re-arm its deadline, since the event
// may bring a check earlier. It never blocks.
func (r *stepRun) Event(event session.Event) {
	if ev, ok := event.(session.AssistantError); ok && ev.Error == apiErrorRateLimit {
		r.rateLimit.Store(true)
	}
	r.watch.event(r.now(), event)
	select {
	case r.wakes <- struct{}{}:
	default:
	}
}

// Done reports whether the structured return of ended has the step's done value. It reads only the step's fixed config,
// so the manager may call it under its lock.
func (r *stepRun) Done(ended session.TurnEnded) bool {
	var head struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(ended.StructuredOutput, &head) == nil && head.Status == r.doneValue
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

// run handles returns, exits, owner actions and the deadline timer until the step ends or ctx ends. A run with a restore
// takes it up first, and the run arms its deadline once it is up. The loop re-arms after each case it handles, and stops
// the armed timer when it returns. A ctx that ends stores no end.
func (r *stepRun) run(ctx context.Context) {
	defer close(r.runDone)
	defer r.disarm()
	if r.restore != nil && !r.resume(ctx, *r.restore) {
		return
	}
	r.arm()
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-r.exits:
			if !r.exitReceived(ctx, reason) {
				return
			}
		case call := <-r.calls:
			reply := call.act(ctx)
			// The run re-arms before it replies, so an owner action that returns has its deadline armed for it.
			r.arm()
			call.reply <- reply
			select {
			case <-r.ended:
				return
			default:
			}
		case <-r.deadline:
			r.deadline, r.stopDeadline = nil, nil
			r.checked = r.now()
			if !r.tick(ctx) {
				return
			}
		case <-r.wakes:
		case ended := <-r.returns:
			if !r.turnEnded(ctx, ended) {
				return
			}
		}
		r.arm()
	}
}

// exited handles the session's exit with its reason. A limited run ignores it, since its process was released; any
// other exit ends the step failed. It reports whether the run goes on.
func (r *stepRun) exited(ctx context.Context, reason string) bool {
	if r.limited {
		return true
	}
	r.finish(ctx, stepLoopEndFailed, "", reason)
	return false
}

// exitReceived handles the session's exit with its reason once the turn ends queued before it are handled, so a
// process that exits right after its limit turn leaves the step limited. It reports whether the run goes on.
func (r *stepRun) exitReceived(ctx context.Context, reason string) bool {
	return r.takeQueuedReturns(ctx) && r.exited(ctx, reason)
}

// takeQueuedReturns handles, in order, the turn ends already queued on returns, so that an exit taken after them comes
// after their turn ends. run picks at random between a queued turn end and a queued exit, so this keeps a process that
// exits right after its limit turn limited. It reports whether the run goes on.
func (r *stepRun) takeQueuedReturns(ctx context.Context) bool {
	for {
		select {
		case ended := <-r.returns:
			if !r.turnEnded(ctx, ended) {
				return false
			}
		default:
			return true
		}
	}
}

// tick runs the check of the run's deadline when its timer fires: the agent gate's limit, then the watch. A held or
// limited run checks nothing. It reports whether the run goes on.
func (r *stepRun) tick(ctx context.Context) bool {
	if r.held.Load() || r.limited {
		return true
	}
	return r.watchGateLimit(ctx) && r.watchTick(ctx)
}

// arm keeps the run's deadline timer armed for the earliest moment a check is due, as nextCheck gives it. It drains the
// wakes first, so the moment reflects every event handed to the watch before it. While the armed timer's moment is the
// one due, the timer stays. A changed moment stops the timer and arms one for the time left, and no moment arms none. A
// run with no newTimer arms none.
func (r *stepRun) arm() {
	if r.newTimer == nil {
		return
	}
	select {
	case <-r.wakes:
	default:
	}
	now := r.now()
	at, due := r.nextCheck(now)
	if r.deadline != nil && (!due || !at.Equal(r.deadlineAt)) {
		r.disarm()
	}
	if !due || r.deadline != nil {
		return
	}
	r.deadline, r.stopDeadline = r.newTimer(at.Sub(now))
	r.deadlineAt = at
}

// disarm stops the run's deadline timer, if one is armed.
func (r *stepRun) disarm() {
	if r.deadline != nil {
		r.stopDeadline()
		r.deadline, r.stopDeadline = nil, nil
	}
}

// nextCheck returns the earliest moment a check is due as of now, and false when none is. A held or limited run has
// none. Otherwise it is the earlier of the watch's next check and, for an agent gate step, the gate's limit moment,
// which counts from the run's start or from its latest resume, as watchGateLimit reads it. A moment already due waits
// stepCheckRetry after the last check, so a trip that stays due after its check is not checked again at once.
func (r *stepRun) nextCheck(now time.Time) (time.Time, bool) {
	if r.held.Load() || r.limited {
		return time.Time{}, false
	}
	at, due := r.watch.next(now)
	if r.agentGate != nil {
		from := r.started
		if !r.gateFrom.IsZero() {
			from = r.gateFrom
		}
		if limit := from.Add(r.agentGate.Limit); !due || limit.Before(at) {
			at, due = limit, true
		}
	}
	if due && !at.After(now) {
		at = now
		if retry := r.checked.Add(stepCheckRetry); retry.After(now) {
			at = retry
		}
	}
	return at, due
}

// resume takes up a re-adopted step from restore. Its loop state is restored, and the watch's turn is marked running
// when no turn end was found. A limited row is restored as limited, and its stored turn end is not handled again. A
// turn end its latest return covers goes to resumeReturn. A turn end stored while a background task that runs a model
// is listed is a return only when it has the done value, as Manager.noteEvent hands one, so any other such turn end is
// left unread and the step waits with no turn running, as a live step does. A stored latest return that is not done is
// likewise left open while a step the run started runs, as resumeReturn leaves it (see awaitingChildren), so the step
// waits the same way. Any other turn end is handled as one the session has just given. It reports whether the run goes
// on.
func (r *stepRun) resume(ctx context.Context, restore stepRestore) bool {
	row := restore.Row
	r.round = row.LoopRound
	r.stage, r.stageFrom = row.LoopStage, row.LoopStageFrom
	r.continues = row.LoopContinues
	r.nudge = row.LoopNudge
	r.ownerNudge = row.LoopOwnerNudge
	r.judgeSent = row.LoopJudgeSent
	r.gateFrom = row.LoopGateFrom
	r.held.Store(row.LoopHeld)
	// A pending nudge is the interrupt of the running turn, so the turn's periodic check is off until its end.
	nudging := !restore.Found && row.LoopNudge != ""
	r.watch.restore(row.LoopNudged, !restore.Found, nudging, r.now())
	if row.GateHolder != "" {
		r.resumeGate = store.StepGate{ID: r.id, Grant: row.GateGrant, Holder: row.GateHolder, Round: row.GateRound}
	}
	ended := session.TurnEnded{IsError: restore.Turn.IsError, StructuredOutput: json.RawMessage(restore.Turn.Output)}
	switch {
	case row.LoopLimited:
		r.limited = true
		r.limitKind, r.limitResets = row.LoopLimitKind, row.LoopLimitResets
		return true
	case !restore.Found:
		return true
	case restore.Turn.Returned:
		return r.resumeReturn(ctx, row)
	case restore.Turn.Awaited && !r.Done(ended):
		return true
	}
	return r.turnEnded(ctx, ended)
}

// awaitingChildren reports whether the step stays open for the steps it started: value is not its done return and one of
// those steps is still running. A failed read is logged and reports true, so the step stays open, since an end cannot be
// undone and the next turn end reads again.
func (r *stepRun) awaitingChildren(ctx context.Context, value json.RawMessage) bool {
	if r.Done(session.TurnEnded{StructuredOutput: value}) {
		return false
	}
	running, err := r.store.RunningCallerSteps(ctx, r.id)
	if err != nil {
		r.logger.ErrorContext(ctx, "read running steps of step", "step", r.id, "err", err)
		return true
	}
	return len(running) > 0
}

// resumeReturn takes up a step whose latest return is stored and covered by its turn end, so nothing has been sent
// since that return. A return that is not done stays open while a step it started runs (see awaitingChildren). A stored
// failed checkout check ends it checks_failed, as judge would have, and its other failed checks go back as retry sends
// them. A done return is judged as judge judges it, and any other return ends the step returned. It reports whether the
// run goes on.
func (r *stepRun) resumeReturn(ctx context.Context, row store.StepRow) bool {
	if r.awaitingChildren(ctx, json.RawMessage(row.Result)) {
		return true
	}
	checks := stepLoopRoundChecks(row.Checks, row.LoopRound)
	if check, moved := stepLoopCheckoutFailure(checks); moved {
		r.finish(ctx, stepLoopEndChecksFailed, "", check.Name+": "+check.Detail)
		return false
	}
	if len(stepLoopFailures(checks)) > 0 {
		return r.retry(ctx, row.LoopRound, checks)
	}
	var head struct {
		Status string          `json:"status"`
		Commit json.RawMessage `json:"commit"`
	}
	if err := json.Unmarshal([]byte(row.Result), &head); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("read status of step %s: %v", r.id, err))
		return false
	}
	if head.Status != r.doneValue {
		r.finish(ctx, stepLoopEndReturned, head.Status, "")
		return false
	}
	var commit string
	if r.writing {
		commit = r.stepLoopCommit(ctx, head.Commit)
	}
	return r.judgeReturn(ctx, row.LoopRound, json.RawMessage(row.Result), commit, head.Status, checks)
}

// turnEnded handles a turn end the run takes. A turn that ended on a usage limit is handled by limitTurn, and is no round.
// Whatever else it is, it clears the pending nudges. A turn end that continues the step stores the clear, and a held
// step's resume, in the write of the prompt it sends (continueStep), so a restart before that write finds the nudge
// still pending and the step still held, and sends it again. Any other turn end stores the clear before it goes on. A
// held step's turn end is no round and continues nothing, unless an owner nudge is pending, which resumes the step with
// it. An owner nudge pending continues the step with it, error or not. A pending watchdog nudge is sent back with an
// error turn end. A return that is not done while a step the run started runs is left open, with no round (see
// awaitingChildren). Any other turn end is the next round, which judge checks. It reports whether the run goes on.
func (r *stepRun) turnEnded(ctx context.Context, ended session.TurnEnded) bool {
	if limited, goOn := r.limitTurn(ctx); limited {
		return goOn
	}
	nudge, owner := r.nudge, r.ownerNudge
	r.nudge, r.ownerNudge = "", ""
	if r.held.Load() {
		if owner == "" {
			r.heldEnded = true
			if nudge != "" {
				r.saveLoop(ctx)
			}
			return true
		}
		r.held.Store(false)
		r.heldEnded = false
		return r.continueWith(ctx, StepPrompt{Text: joinNudges(nudge, owner), Sender: store.PromptSenderOwner})
	}
	if owner != "" {
		return r.continueWith(ctx, StepPrompt{Text: joinNudges(nudge, owner), Sender: store.PromptSenderOwner})
	}
	if nudge != "" && ended.IsError {
		return r.continueWith(ctx, StepPrompt{Text: nudge, Sender: store.PromptSenderEngine})
	}
	if nudge != "" {
		r.saveLoop(ctx)
	}
	if r.awaitingChildren(ctx, ended.StructuredOutput) {
		return true
	}
	return r.judge(ctx, r.round+1, ended.StructuredOutput)
}

// limitTurn checks whether the turn that has just ended ended on a usage limit: the rate limit the store holds for the
// step, or the rate limit error the session handed the run since the previous turn end. A limited turn drops the pending
// nudges and the hold, stores the loop state with its limit, publishes the held step, and releases the step's process,
// so the step stays open with no process running. It reports whether the turn was limited and, when it was, whether the run
// goes on. A failed read, store or release ends the step failed.
func (r *stepRun) limitTurn(ctx context.Context) (limited, goOn bool) {
	errored := r.rateLimit.Swap(false)
	row, found, err := r.store.StepRateLimit(ctx, r.id)
	if err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("read rate limit of step %s: %v", r.id, err))
		return true, false
	}
	if !found && !errored {
		return false, true
	}
	r.limited = true
	r.limitKind, r.limitResets = "", time.Time{}
	if found {
		r.limitKind, r.limitResets = row.Kind, row.ResetsAt
	}
	r.nudge, r.ownerNudge = "", ""
	r.held.Store(false)
	r.heldEnded = false
	r.setStage(store.StepStageLimited)
	if err = r.writer.Commit(ctx, r.id, r.loopState()); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record limit of step %s: %v", r.id, err))
		return true, false
	}
	r.publish(ctx, r.id)
	if err = r.sessions.Release(ctx, r.id); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("release step %s: %v", r.id, err))
		return true, false
	}
	return true, true
}

// continueWith sends prompt to the session as its next message. A failed send ends the step failed. It reports whether
// the run goes on.
func (r *stepRun) continueWith(ctx context.Context, prompt StepPrompt) bool {
	if err := r.continueStep(ctx, prompt); err != nil {
		return false
	}
	return true
}

// continueStep sends prompt to the session as its next message, with the loop in turn as its Loop, so the loop state is
// stored in the write that stores the prompt. A failed store or send ends the step failed and returns the error. A sent
// prompt wakes the caller events (see wakeCaller).
func (r *stepRun) continueStep(ctx context.Context, prompt StepPrompt) error {
	r.setStage(store.StepStageTurn)
	loop := r.loopState()
	prompt.Loop = &loop
	if err := r.sessions.Continue(ctx, r.id, prompt); err != nil {
		err = fmt.Errorf("continue step %s: %w", r.id, err)
		r.finish(ctx, stepLoopEndFailed, "", err.Error())
		return err
	}
	r.wakeCaller()
	return nil
}

// wakeCaller calls the run's wake, when it has one, so a caller event that waited on the step is offered again. A nil
// wake does nothing.
func (r *stepRun) wakeCaller() {
	if r.wake != nil {
		r.wake()
	}
}

// loopState returns the loop state the run stores: its round and continue count, the nudges pending for the next turn
// end, whether the watch has spent its nudge, whether the run is held, whether the judge has sent the step back, its
// usage limit, and its stage with the time the stage started.
func (r *stepRun) loopState() store.StepLoop {
	return store.StepLoop{
		ID: r.id, Round: r.round, Continues: r.continues, Nudge: r.nudge, OwnerNudge: r.ownerNudge,
		Nudged: r.watch.nudgeSpent(), Held: r.held.Load(), JudgeSent: r.judgeSent,
		Limited: r.limited, LimitKind: r.limitKind, LimitResets: r.limitResets, GateFrom: r.gateFrom,
		Stage: r.stage, StageFrom: r.stageFrom,
	}
}

// setStage moves the loop to stage, starting the stage at the clock's reading now. A stage that is already the loop's
// keeps its start, so the start moves only on a stage change.
func (r *stepRun) setStage(stage string) {
	if r.stage == stage {
		return
	}
	r.stage, r.stageFrom = stage, r.now()
}

// enterChecks moves a turn's loop into checks and stores it before the return's checks run. A loop already in checks or
// gate is a restored return's, whose checks or gate run on in their stage, so it is stored as it is. It reports whether
// the run goes on.
func (r *stepRun) enterChecks(ctx context.Context) bool {
	if r.stage == store.StepStageChecks || r.stage == store.StepStageGate {
		return true
	}
	r.setStage(store.StepStageChecks)
	if err := r.writer.Commit(ctx, r.id, r.loopState()); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record checks of step %s: %v", r.id, err))
		return false
	}
	return true
}

// commitReturn stores ret with the loop state of round, the round the return is stored as. Until it is stored, the loop
// state holds the round before round, so a restart judges the turn end again as round.
func (r *stepRun) commitReturn(ctx context.Context, round int, ret store.StepReturn) error {
	r.round = round
	return r.writer.Commit(ctx, r.id, ret, r.loopState())
}

// saveLoop queues the loop state with Put. A failed put is logged and the loop goes on, since the next change stores the
// state again.
func (r *stepRun) saveLoop(ctx context.Context) {
	if err := r.writer.Put(ctx, r.id, r.loopState()); err != nil {
		r.logger.ErrorContext(ctx, "save loop state of step", "step", r.id, "err", err)
	}
}

// gateState returns the channel a gate start closes and whether a gate is running now. A caller that sees no gate
// running waits on the channel while it asks the loop for an action.
func (r *stepRun) gateState() (<-chan struct{}, bool) {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	return r.gateStart, r.gating
}

// beginGate marks the run as running its gate and releases every owner action waiting for the loop. It runs in the
// loop goroutine, and endGate ends it.
func (r *stepRun) beginGate() {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	r.gating = true
	close(r.gateStart)
}

// endGate ends the gate beginGate marked and makes the channel the next gate start closes.
func (r *stepRun) endGate() {
	r.gateMu.Lock()
	defer r.gateMu.Unlock()
	r.gating = false
	r.gateStart = make(chan struct{})
}

// joinNudges returns the texts that are not empty, the first before the second, one per line.
func joinNudges(first, second string) string {
	switch {
	case first == "":
		return second
	case second == "":
		return first
	}
	return first + "\n" + second
}

// judge checks the return of round and stores it with its checks and the loop state, in one commit. A read-only step whose
// checkout moved is stored with its failed checkout check and ends checks_failed at once, with nothing sent back and no
// judge call. An agent gate step's schema-valid return is also checked against the gate's assertions, and it ends done or
// gate_failed without a judge call. A schema-valid return of a step with a return check is checked by it. A done return
// whose checks all passed is judged, unless the step has a return check, which ends it done without a judge call. Any
// other return ends the step, or its failed checks are sent back. It reports whether the run goes on.
func (r *stepRun) judge(ctx context.Context, round int, value json.RawMessage) bool {
	if !r.enterChecks(ctx) {
		return false
	}
	schemaErr := validateReturn(r.schema, value)
	schema := store.StepCheck{Round: round, Name: stepLoopSchemaName, OK: schemaErr == nil}
	if schemaErr != nil {
		schema.Detail = schemaErr.Error()
	}
	checks := []store.StepCheck{schema}
	if check, moved := r.checkoutMove(ctx, round); moved {
		r.endCheckoutMoved(ctx, round, value, append(checks, check), check)
		return false
	}
	var head struct {
		Status string          `json:"status"`
		Commit json.RawMessage `json:"commit"`
	}
	var commit, gateDir string
	var verdict verdictReturn
	var lines *store.StepLines
	if schemaErr == nil {
		if err := json.Unmarshal(value, &head); err != nil {
			r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("read status of step %s: %v", r.id, err))
			return false
		}
		if head.Status == r.doneValue && r.writing {
			commit = r.stepLoopCommit(ctx, head.Commit)
			var ok bool
			if checks, gateDir, ok = r.writingChecks(ctx, round, commit, checks); !ok {
				return false
			}
			lines = r.countLinesOf(ctx, commit)
		}
	}
	if schemaErr == nil {
		checks, verdict = r.valueChecks(round, value, checks)
	}
	ret := store.StepReturn{ID: r.id, Result: string(value), Checks: checks, Lines: lines}
	if err := r.commitReturn(ctx, round, ret); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record return of step %s: %v", r.id,
			errors.Join(err, gateClear(ctx, gateDir))))
		return false
	}
	// The gate's holder keeps its exit record until the return that judges it is stored.
	if err := gateClear(ctx, gateDir); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("clear gate of step %s: %v", r.id, err))
		return false
	}
	if len(stepLoopFailures(checks)) > 0 {
		return r.retry(ctx, round, checks)
	}
	if r.agentGate != nil {
		r.endVerdict(ctx, head.Status, verdict)
		return false
	}
	if head.Status == r.doneValue {
		return r.endDoneReturn(ctx, round, value, commit, head.Status, checks)
	}
	r.endNotDone(ctx, head.Status, head.Commit)
	return false
}

// endNotDone ends the step on a return with status, which is not done, that passed its checks: goal_unmet when the
// judged commit is kept (see keepJudgedCommit), and returned otherwise.
func (r *stepRun) endNotDone(ctx context.Context, status string, commit json.RawMessage) {
	if r.keepJudgedCommit(ctx, status, commit) {
		return
	}
	r.finish(ctx, stepLoopEndReturned, status, "")
}

// keepJudgedCommit ends a writing step goal_unmet, as a second unmet verdict does, when it returns status, which is not
// done, after the judge sent back a done return whose commit passed every check, while HEAD is still that commit and
// the return names no other. The commit is kept instead of the work being lost to the return. It reports whether it
// ended the step.
func (r *stepRun) keepJudgedCommit(ctx context.Context, status string, raw json.RawMessage) bool {
	if !r.writing || r.judgedCommit == "" || !stepGitHeadCheck(ctx, r.dir, r.judgedCommit).OK {
		return false
	}
	var named string
	if err := json.Unmarshal(raw, &named); err == nil && named != "" {
		resolved, resolveErr := stepGitResolve(ctx, r.dir, named)
		if resolveErr != nil || resolved != r.judgedCommit {
			return false
		}
	}
	r.finish(ctx, judgeLoopEndGoalUnmet, "", fmt.Sprintf(
		"returned %s after the judge sent back commit %s, which passed every check; that commit is kept",
		status, r.judgedCommit))
	return true
}

// checkoutMove compares a read-only step's checkout with the state it started in. It returns the failed checkout check of
// round and true when the checkout moved, and nothing otherwise. A step with no before state is never compared.
func (r *stepRun) checkoutMove(ctx context.Context, round int) (store.StepCheck, bool) {
	if r.checkout == (stepCheckout{}) {
		return store.StepCheck{}, false
	}
	check := stepGitCheckoutCheck(ctx, r.dir, r.checkout)
	if check.OK {
		return store.StepCheck{}, false
	}
	return stepLoopRecord(round, check), true
}

// endCheckoutMoved stores value's return with checks, which end with its failed checkout check, and ends the step
// checks_failed at once. Nothing is sent back and no judge call runs.
func (r *stepRun) endCheckoutMoved(
	ctx context.Context, round int, value json.RawMessage, checks []store.StepCheck, check store.StepCheck,
) {
	ret := store.StepReturn{ID: r.id, Result: string(value), Checks: checks}
	if err := r.commitReturn(ctx, round, ret); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record return of step %s: %v", r.id, err))
		return
	}
	r.finish(ctx, stepLoopEndChecksFailed, "", check.Name+": "+check.Detail)
}

// stepLoopCheckoutFailure returns the failed checkout check among checks, and whether there is one.
func stepLoopCheckoutFailure(checks []store.StepCheck) (store.StepCheck, bool) {
	for _, check := range checks {
		if check.Name == stepGitCheckoutName && !check.OK {
			return check, true
		}
	}
	return store.StepCheck{}, false
}

// valueChecks appends the checks a schema-valid return of round runs on its value to checks: an agent gate step's
// assertions, whose verdict it returns, and a step's return check.
func (r *stepRun) valueChecks(round int, value json.RawMessage,
	checks []store.StepCheck) ([]store.StepCheck, verdictReturn) {
	var verdict verdictReturn
	if r.agentGate != nil {
		var check store.StepCheck
		check, verdict = r.assertionsCheck(round, value)
		checks = append(checks, check)
	}
	if r.returnCheck != nil {
		checks = append(checks, stepLoopReturnRecord(round, r.returnCheck(value)))
	}
	return checks, verdict
}

// endDoneReturn ends a done return whose checks all passed. A step with a return check ends done without a judge call,
// and any other step's return is judged by judgeReturn. It reports whether the run goes on.
func (r *stepRun) endDoneReturn(ctx context.Context, round int, value json.RawMessage, commit, status string,
	checks []store.StepCheck) bool {
	if r.returnCheck != nil {
		r.finish(ctx, stepLoopEndDone, status, "")
		return false
	}
	return r.judgeReturn(ctx, round, value, commit, status, checks)
}

// stepLoopReturnRecord returns the return check of round for the failure lines a return check gave a schema-valid
// return. It passes when there are none, and its detail joins them.
func stepLoopReturnRecord(round int, failures []string) store.StepCheck {
	return store.StepCheck{
		Round: round, Name: stepLoopReturnName, OK: len(failures) == 0, Detail: strings.Join(failures, "; "),
	}
}

// doneChecks appends the checks of a writing step's done return to checks, and returns the holder directory of the
// gate it ran, which the caller clears once the return is stored, or at once when the checks fail. The commit checks
// run first: the four of commitChecks, or without the owned check when the step is unowned. The gate runs only when
// they all pass and a gate is set. A gate that cannot run is an error:
// errGateLost when its outcome is lost, and errGateDetach when it is left running under its holder. The gate's check
// opens with the record of its run, stepGateRecord. The step row stores the gate's grant, holder and round before the
// gate launches. runDoneGate re-adopts a gate the restored row stores as running in this round.
func (r *stepRun) doneChecks(ctx context.Context, round int, commit string,
	checks []store.StepCheck) ([]store.StepCheck, string, error) {
	passed := true
	var ran []stepCheck
	if r.unowned {
		ran = commitChecksWithoutOwned(ctx, r.dir, r.start, commit)
	} else {
		ran = commitChecks(ctx, r.dir, r.start, commit, r.owned)
	}
	for _, c := range ran {
		checks = append(checks, stepLoopRecord(round, c))
		passed = passed && c.OK
	}
	if !passed || len(r.gate) == 0 {
		r.setStage(store.StepStageChecks)
		return checks, "", nil
	}
	r.beginGate()
	result, err := r.runDoneGate(ctx, round)
	r.endGate()
	if err != nil {
		// A failed run returns the zero gateResult, except a gate that exited store.EnvironmentExit on its last run, whose
		// holder is cleared with the step's end.
		return checks, result.Holder, fmt.Errorf("run gate of step %s: %w", r.id, err)
	}
	// The gate has ended, so the return's checks are what runs next, and the stage moves back to checks.
	r.setStage(store.StepStageChecks)
	check := stepLoopGateCheck(round, result, r.gateLimit)
	record, err := stepGateRecord(r.gateName, r.gateSource, r.gate, result.Members)
	if err != nil {
		return checks, result.Holder, fmt.Errorf("record gate of step %s: %w", r.id, err)
	}
	check.Detail = record + check.Detail
	if !check.OK {
		// The gate's holder directory is cleared once its result is read, so a failed gate's output is kept apart.
		if check.Log, err = r.keepGateLog(round, result); err != nil {
			check.Detail += "; gate output not kept: " + err.Error()
		}
	}
	return append(checks, check), result.Holder, nil
}

// keepGateLog writes the output a failed gate of round kept, its stdout and then its stderr under their labels, each
// already redacted, to round-<round>.log in the step's directory under the logs directory of the gate state directory.
// That directory outlives the gate's holder. The step's directory and the file are made through an os.Root of the logs
// directory, so the step id cannot lead them outside it. It returns the file's path.
func (r *stepRun) keepGateLog(round int, result gateResult) (string, error) {
	stateDir, err := gateStateDir(r.stateDir)
	if err != nil {
		return "", err
	}
	logs := filepath.Join(stateDir, gateLogsName)
	if err = os.MkdirAll(logs, gateLogDirMode); err != nil {
		return "", fmt.Errorf("make gate log directory: %w", err)
	}
	root, err := os.OpenRoot(logs)
	if err != nil {
		return "", fmt.Errorf("open gate log directory: %w", err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			r.logger.Warn("close gate log directory", "step", r.id, "err", closeErr)
		}
	}()
	if err = root.MkdirAll(r.id, gateLogDirMode); err != nil {
		return "", fmt.Errorf("make gate log directory of step %s: %w", r.id, err)
	}
	var log strings.Builder
	for _, stream := range []struct{ label, text string }{{"stdout", result.Stdout}, {"stderr", result.Stderr}} {
		log.WriteString("== " + stream.label + " ==\n" + stream.text)
		if stream.text != "" && !strings.HasSuffix(stream.text, "\n") {
			log.WriteString("\n")
		}
	}
	name := filepath.Join(r.id, fmt.Sprintf("round-%d.log", round))
	if err = root.WriteFile(name, []byte(log.String()), gateLogFileMode); err != nil {
		return "", fmt.Errorf("write gate log: %w", err)
	}
	return filepath.Join(logs, name), nil
}

// writingChecks runs the done checks of a writing step's return with doneChecks. A failed run ends the step: errGateLost
// ends it engine_restarted, and any other error ends it failed. errGateDetach ends nothing: the gate runs on under its
// holder, so the step is left open for re-adoption, with no end stored and no session stopped, and the holder's
// directory is not cleared. It returns the checks, the gate's holder directory and whether the run goes on.
func (r *stepRun) writingChecks(ctx context.Context, round int, commit string,
	checks []store.StepCheck) ([]store.StepCheck, string, bool) {
	checks, gateDir, err := r.doneChecks(ctx, round, commit, checks)
	switch {
	case errors.Is(err, errGateDetach):
		return checks, "", false
	case err != nil:
		end := stepLoopEndFailed
		if errors.Is(err, errGateLost) {
			end = stepRestartedEnd
		}
		r.finish(ctx, end, "", errors.Join(err, gateClear(ctx, gateDir)).Error())
		return checks, "", false
	}
	return checks, gateDir, true
}

// runDoneGate runs the gate of round. A gate that exits store.EnvironmentExit without timing out runs again, up to
// store.EnvironmentReruns times, and a gate that exits so on its last run returns its result with an error that names it.
// Each attempt runs under its own session (see stepGateSession), so the holder a restored row stores says which attempt
// it is, and a restart carries on from there. A gate the restored row stores as running in round is re-adopted from its
// grant and holder, and its exit is the result, so that gate is not run again.
func (r *stepRun) runDoneGate(ctx context.Context, round int) (gateResult, error) {
	attempt, err := r.gateAttemptOf(round)
	if err != nil {
		return gateResult{}, err
	}
	prev := ""
	for {
		result, runErr := r.runGateAttempt(ctx, round, attempt, prev)
		if runErr != nil || !store.EnvironmentExited(result.Exit, result.TimedOut) {
			return result, runErr
		}
		if attempt == store.EnvironmentReruns {
			return result, fmt.Errorf("gate %s exited %d on its last run: an environment result, not a verdict",
				r.gateName, store.EnvironmentExit)
		}
		attempt++
		prev = result.Holder
	}
}

// gateAttemptOf returns the attempt of the gate of round that the restored row stores as running, from the session of its
// holder. It is 0 when no gate of round is stored as running, so the first attempt runs.
func (r *stepRun) gateAttemptOf(round int) (int, error) {
	restored := r.resumeGate
	if restored.Holder == "" || restored.Round != round {
		return 0, nil
	}
	session := filepath.Base(restored.Holder)
	for attempt := 0; attempt <= store.EnvironmentReruns; attempt++ {
		if stepGateSession(r.id, attempt) == session {
			return attempt, nil
		}
	}
	return 0, fmt.Errorf("gate holder %s of step %s is not a session its gate runs under", session, r.id)
}

// runGateAttempt runs attempt of the gate of round. A restored gate that is stored as running in this round is re-adopted
// as the attempt its holder names. Any other attempt runs under a new grant, and its Started stores that grant, holder and
// the gate stage before the gate launches. The holder of the attempt before it, prev, is cleared once that is stored, so
// the row never names a holder that is gone.
func (r *stepRun) runGateAttempt(ctx context.Context, round, attempt int, prev string) (gateResult, error) {
	run := gateRun{
		Argv: r.gate, Dir: r.dir, Limit: r.gateLimit, Environ: r.environ,
		Name: stepGateSession(r.id, attempt), StateDir: r.stateDir, Scope: r.scope,
		Tail: stepGateLogTail,
	}
	if restored := r.resumeGate; restored.Holder != "" && restored.Round == round {
		r.resumeGate = store.StepGate{}
		// A holder an older orreryd launched keeps the default tail, and its exit record does not say which it kept, so
		// a re-adopted gate's tails are redacted as cut from the default size: at worst a long tail loses its first line.
		run.Tail = gateTailDefault
		return readoptGrantedGate(ctx, r.pools, r.grant, run, restored.Grant)
	}
	run.Started = func(ctx context.Context, grantID, dir string) error {
		r.setStage(store.StepStageGate)
		gate := store.StepGate{ID: r.id, Grant: grantID, Holder: dir, Round: round}
		if err := r.writer.Commit(ctx, r.id, gate, r.loopState()); err != nil {
			return err
		}
		return gateClear(ctx, prev)
	}
	return runGrantedGate(ctx, r.pools, r.grant, run)
}

// stepGateSession returns the session the gate of the step with id runs under on attempt. The first attempt's session is
// the id with "-gate" added, as it always was, and each rerun's adds its attempt number.
func stepGateSession(id string, attempt int) string {
	if attempt == 0 {
		return id + "-gate"
	}
	return fmt.Sprintf("%s-gate-%d", id, attempt)
}

// stepGateRecord returns the record a granted gate's check detail opens with: the gate's name and the file it resolved
// from, the argv it ran as a JSON array, and the members it held as POOL:MEMBER, comma-separated, then a semicolon.
func stepGateRecord(name, source string, argv []string, members []grants.Member) (string, error) {
	raw, err := json.Marshal(argv)
	if err != nil {
		return "", fmt.Errorf("encode argv of gate %s: %w", name, err)
	}
	held := make([]string, 0, len(members))
	for _, member := range members {
		held = append(held, member.Pool+":"+member.Name)
	}
	return fmt.Sprintf("gate %s from %s ran %s holding %s; ", name, source, raw, strings.Join(held, ",")), nil
}

// stepLoopCommit returns the commit a done return names. A return that names no commit string, one missing, empty or
// not a string, takes the workspace's HEAD when a writing step's HEAD has moved past start. Otherwise it returns "",
// which the head check then fails, so the return goes back like any other failure.
func (r *stepRun) stepLoopCommit(ctx context.Context, raw json.RawMessage) string {
	var commit string
	if err := json.Unmarshal(raw, &commit); err == nil && commit != "" {
		return commit
	}
	head, err := stepGitResolve(ctx, r.dir, "HEAD")
	if err != nil || head == r.start {
		return ""
	}
	return head
}

// countLinesOf returns the line counts of commit, the commit a done return's checks ran on. An unowned step's return is
// not counted, so it returns nil and leaves the row's counts as they are. When the count fails, it logs the failure and
// returns uncounted counts, which clear the counts of an earlier return.
func (r *stepRun) countLinesOf(ctx context.Context, commit string) *store.StepLines {
	if r.unowned {
		return nil
	}
	lines, err := r.countLines(ctx, r.dir, r.start, commit)
	if err != nil {
		r.logger.WarnContext(ctx, "count lines of step", "step", r.id, "err", err)
		return &store.StepLines{}
	}
	return &lines
}

// stepLoopRecord returns c as a check stored with round.
func stepLoopRecord(round int, c stepCheck) store.StepCheck {
	return store.StepCheck{Round: round, Name: c.Name, OK: c.OK, Detail: c.Detail}
}

// stepLoopGateCheck returns the gate check of round for result. It passes when the gate exits 0 without passing limit.
// Its detail gives the exit status, or the limit when the gate was killed for it, and then the processes its scope's
// memory cap killed, when it killed any. Its Stdout and Stderr keep the newest
// stepGateCheckTail bytes of the gate's two streams apart (see stepCheckTail).
func stepLoopGateCheck(round int, result gateResult, limit time.Duration) store.StepCheck {
	check := store.StepCheck{
		Round:  round,
		Name:   stepLoopGateName,
		OK:     !result.TimedOut && result.Exit == 0,
		Stdout: stepCheckTail(result.Stdout),
		Stderr: stepCheckTail(result.Stderr),
	}
	if result.TimedOut {
		check.Detail = fmt.Sprintf("gate ran past its limit of %s and was killed", limit)
	} else {
		check.Detail = fmt.Sprintf("gate exited %d", result.Exit)
	}
	if result.MemoryKills > 0 {
		check.Detail += fmt.Sprintf("; its scope's memory cap killed %d of its processes", result.MemoryKills)
	}
	return check
}

// stepCheckTail returns the newest stepGateCheckTail bytes of tail, a gate stream already redacted, from the start of a
// line: a longer tail loses its oldest bytes and the line they cut, so the check keeps whole lines. A tail with no line
// break in its newest bytes keeps them from the start of a character.
func stepCheckTail(tail string) string {
	if len(tail) <= stepGateCheckTail {
		return tail
	}
	newest := tail[len(tail)-stepGateCheckTail:]
	if i := strings.IndexByte(newest, '\n'); i >= 0 && i+1 < len(newest) {
		return newest[i+1:]
	}
	for newest != "" && !utf8.RuneStart(newest[0]) {
		newest = newest[1:]
	}
	return newest
}

// retry sends the failed checks of round back to the session while it has continues left, and otherwise ends
// checks_failed. The turn stage is stored before the Continue sends the prompt, and a raised continue count is stored
// once its Continue is sent. It reports whether the run goes on.
func (r *stepRun) retry(ctx context.Context, round int, checks []store.StepCheck) bool {
	failures := stepLoopFailures(checks)
	if r.continues == stepLoopMaxContinues {
		r.finish(ctx, stepLoopEndChecksFailed, "", strings.Join(failures, "; "))
		return false
	}
	text := "Your return failed these checks. Return again with a result that passes them:\n- " +
		strings.Join(failures, "\n- ")
	prompt := StepPrompt{Text: text, Sender: store.PromptSenderEngine, Round: round}
	if err := r.continueStep(ctx, prompt); err != nil {
		return false
	}
	r.continues++
	r.saveLoop(ctx)
	return true
}

// stepLoopRoundChecks returns the checks among checks that ran in round, in the order they were stored.
func stepLoopRoundChecks(checks []store.StepCheck, round int) []store.StepCheck {
	var out []store.StepCheck
	for _, check := range checks {
		if check.Round == round {
			out = append(out, check)
		}
	}
	return out
}

// stepLoopFailures names each failed check with its detail, and with the stdout and stderr tails it kept, each labeled.
func stepLoopFailures(checks []store.StepCheck) []string {
	var failures []string
	for _, check := range checks {
		if check.OK {
			continue
		}
		failure := check.Name + ": " + check.Detail
		if tail := strings.TrimSpace(check.Stdout); tail != "" {
			failure += "; stdout tail: " + tail
		}
		if tail := strings.TrimSpace(check.Stderr); tail != "" {
			failure += "; stderr tail: " + tail
		}
		failures = append(failures, failure)
	}
	return failures
}

// finish ends the step once. It stores StepEnd and publishes the end once it is stored, stops the session unless
// limitReleased says a limit released it, runs cleanup and closes ended. A store, stop or cleanup error is logged, since
// nothing above finish can act on it. A ctx that has ended stores no end, so finish does nothing then. It reports whether
// this call acted.
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
		} else {
			r.publish(ctx, r.id)
		}
		if !r.limitReleased {
			if err := r.sessions.Stop(ctx, r.id); err != nil {
				r.logger.ErrorContext(ctx, "stop step session", "step", r.id, "err", err)
			}
		}
		if r.cleanup != nil {
			if err := r.cleanup(ctx); err != nil {
				r.logger.ErrorContext(ctx, "clean up step", "step", r.id, "err", err)
			}
		}
	})
	return acted
}
