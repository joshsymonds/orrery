package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/store"
)

// StepPrompt is a prompt sent to a step session as its next message. Sender is who sent it, which the engine stores with
// it: brief, engine or owner. Round is the round of a send-back, and zero when the prompt carries none.
type StepPrompt struct {
	Text   string
	Sender string
	Round  int
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

// stepLoopMaxContinues is how many failing returns the session is sent back to fix. The next failure ends checks_failed.
const stepLoopMaxContinues = 3

// stepLoopBuffer is the capacity of the returns and exits channels. A send waits only while the buffer is full.
const stepLoopBuffer = 8

// apiErrorRateLimit is the API error an assistant message carries on a turn that hit a usage limit.
const apiErrorRateLimit = "rate_limit"

// stepRunConfig is what a stepRun is built from. A writing step may change its worktree: its done return is checked
// with commitChecks against start and owned in dir, and with gate when every commit check passes and gate is set. gate
// runs holding the members of grant that pools grants, and its check records gateName and gateSource; a nil pools fails
// the gate. stateDir and scope are the state directory and the systemd-run scope the gate's holder runs under. A read-only
// step's done return passes on its schema check alone. A nil cleanup has nothing to run. The watch
// reads now as its clock, time.Now when now is nil, and allows the step modelLimit of model time. Each tick on ticks
// checks the watch, and a nil ticks never ticks. A nil haiku makes no monitor or judge calls. prompt is the step's first
// prompt, which the judge is given with each done return. agentGate is the agent gate a step runs, nil for any other
// step: each schema-valid return is checked against its assertions, and never judged. A tick at or past the limit of
// wall time from the run's start ends an agent gate step overtime. returnCheck, when set, checks every schema-valid
// return: its failure lines are stored as the return check and sent back, and a step with one is never judged.
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
	ticks       <-chan time.Time
	agentGate   *gates.Gate
	returnCheck func(value json.RawMessage) []string

	writing    bool
	dir        string
	start      string
	owned      []string
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
// last ended after its last prompt. Turn is that turn end when Found is set, and Found is what store.StepTurn returns.
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
	// started is the clock reading the run was built at. An agent gate step's limit counts its wall time from it.
	started time.Time
	returns chan session.TurnEnded
	exits   chan string
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
	// limited is set once a turn has ended on a usage limit. A limited run spends nothing, skips its ticks and ignores its
	// exits. limitKind and limitResets are the kind and reset the limit was stored with, empty and zero when unknown.
	limited     bool
	limitKind   string
	limitResets time.Time
	// limitReleased is set by Steps.Stop when the step's stored row is limited: its process was released for the limit, so
	// finish stops no session. Nothing else sets it, since a failed resume may leave a respawned process live.
	limitReleased bool
	// judgeSent is set once the judge has sent the step back, so a second unmet verdict ends goal_unmet.
	judgeSent bool
	// round is the number of the step's latest round. A turn end that is no continuation is the next round, which judge
	// checks.
	round int
	// continues is how many failing returns the session has been sent back to fix, up to stepLoopMaxContinues.
	continues int
	// resumeGate is the gate a restored step's row stores as running: its grant and holder, and the round it runs in.
	// runDoneGate re-adopts it in that round, and clears it then.
	resumeGate store.StepGate
	// firstFails is the first fails result of any schema-valid agent gate output that holds one, a round's return or a
	// turn end that is no round, nil until one does. It is never cleared, so no return after it can end done.
	firstFails *verdictResult
	// ended is closed once finish has acted.
	ended chan struct{}
	// runDone is closed when run returns, so Return and Exited never wait on a run that will not drain them.
	runDone chan struct{}
	// calls hands owner actions to run, which runs each in its goroutine and replies with its error.
	calls chan stepOwnerCall
	// held is set when a hold lands. A held run checks nothing: its ticks are skipped, and its turn ends are no round.
	// Run sets it and Steps.Held reads it.
	held atomic.Bool
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
	start := cfg.now()
	return &stepRun{
		stepRunConfig: cfg,
		watch:         newStepWatch(start, cfg.modelLimit),
		started:       start,
		returns:       make(chan session.TurnEnded, stepLoopBuffer),
		exits:         make(chan string, stepLoopBuffer),
		ended:         make(chan struct{}),
		runDone:       make(chan struct{}),
		calls:         make(chan stepOwnerCall),
		gateStart:     make(chan struct{}),
	}
}

// Event hands an event the session recorded to the watch, with the clock's reading now, and notes an assistant message
// that carries the rate limit error for the next turn end. It never blocks.
func (r *stepRun) Event(event session.Event) {
	if ev, ok := event.(session.AssistantError); ok && ev.Error == apiErrorRateLimit {
		r.rateLimit.Store(true)
	}
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

// run handles returns and exits until the step ends or ctx ends. A run with a restore takes it up first. A ctx that
// ends stores no end.
func (r *stepRun) run(ctx context.Context) {
	defer close(r.runDone)
	if r.restore != nil && !r.resume(ctx, *r.restore) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-r.exits:
			if !r.exitReceived(ctx, reason) {
				return
			}
		case call := <-r.calls:
			call.reply <- call.act(ctx)
			select {
			case <-r.ended:
				return
			default:
			}
		case <-r.ticks:
			if !r.tick(ctx) {
				return
			}
		case ended := <-r.returns:
			if !r.turnEnded(ctx, ended) {
				return
			}
		}
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

// tick checks the watch on one tick. A held or limited run checks nothing. It reports whether the run goes on.
func (r *stepRun) tick(ctx context.Context) bool {
	if r.held.Load() || r.limited {
		return true
	}
	return r.watchGateLimit(ctx) && r.watchTick(ctx)
}

// resume takes up a re-adopted step from restore. Its loop state is restored, and the watch's turn is marked running
// when no turn end was found. A limited row is restored as limited, and its stored turn end is not handled again. A turn
// end its latest return covers goes to resumeReturn. Any other turn end is handled as one the session has just given. It
// reports whether the run goes on.
func (r *stepRun) resume(ctx context.Context, restore stepRestore) bool {
	row := restore.Row
	r.round = row.LoopRound
	r.continues = row.LoopContinues
	r.nudge = row.LoopNudge
	r.ownerNudge = row.LoopOwnerNudge
	r.judgeSent = row.LoopJudgeSent
	r.held.Store(row.LoopHeld)
	// A pending nudge is the interrupt of the running turn, so the turn's periodic check is off until its end.
	nudging := !restore.Found && row.LoopNudge != ""
	r.watch.restore(row.LoopNudged, !restore.Found, nudging, r.now())
	if row.GateHolder != "" {
		r.resumeGate = store.StepGate{ID: r.id, Grant: row.GateGrant, Holder: row.GateHolder, Round: row.GateRound}
	}
	switch {
	case row.LoopLimited:
		r.limited = true
		r.limitKind, r.limitResets = row.LoopLimitKind, row.LoopLimitResets
		return true
	case !restore.Found:
		return true
	case restore.Turn.Returned:
		return r.resumeReturn(ctx, row)
	}
	return r.turnEnded(ctx, session.TurnEnded{
		IsError: restore.Turn.IsError, StructuredOutput: json.RawMessage(restore.Turn.Output),
	})
}

// resumeReturn takes up a step whose latest return is stored and covered by its turn end, so nothing has been sent
// since that return. Its failed checks go back as retry sends them. A done return is judged as judge judges it, and
// any other return ends the step returned. It reports whether the run goes on.
func (r *stepRun) resumeReturn(ctx context.Context, row store.StepRow) bool {
	checks := stepLoopRoundChecks(row.Checks, row.LoopRound)
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
// Whatever else it is, it clears the pending nudges and stores the clear before anything is sent. A held step's turn end
// is no round and continues nothing, unless an owner nudge is pending, which resumes the step with it. An owner nudge
// pending continues the step with it, error or not. A pending watchdog nudge is sent back with an error turn end. A turn end
// that is no round latches its output's first fails for an agent gate step, as judge would latch a round's. Any other
// turn end is the next round, which judge checks. It reports whether the run goes on.
func (r *stepRun) turnEnded(ctx context.Context, ended session.TurnEnded) bool {
	if limited, goOn := r.limitTurn(ctx); limited {
		return goOn
	}
	nudge, owner := r.nudge, r.ownerNudge
	r.nudge, r.ownerNudge = "", ""
	if nudge != "" || owner != "" {
		r.saveLoop(ctx)
	}
	if r.held.Load() {
		r.latchNoRound(ended.StructuredOutput)
		if owner == "" {
			r.heldEnded = true
			return true
		}
		r.held.Store(false)
		r.heldEnded = false
		r.saveLoop(ctx)
		return r.continueWith(ctx, StepPrompt{Text: joinNudges(nudge, owner), Sender: store.PromptSenderOwner})
	}
	if owner != "" {
		r.latchNoRound(ended.StructuredOutput)
		return r.continueWith(ctx, StepPrompt{Text: joinNudges(nudge, owner), Sender: store.PromptSenderOwner})
	}
	if nudge != "" && ended.IsError {
		r.latchNoRound(ended.StructuredOutput)
		return r.continueWith(ctx, StepPrompt{Text: nudge, Sender: store.PromptSenderEngine})
	}
	r.round++
	return r.judge(ctx, r.round, ended.StructuredOutput)
}

// limitTurn checks whether the turn that has just ended ended on a usage limit: the rate limit the store holds for the
// step, or the rate limit error the session handed the run since the previous turn end. A limited turn drops the pending
// nudges and the hold, stores the loop state with its limit, and releases the step's process, so the step stays open
// and its slot is free. It reports whether the turn was limited and, when it was, whether the run goes on. A failed
// read, store or release ends the step failed.
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
	if err = r.writer.Commit(ctx, r.id, r.loopState()); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("record limit of step %s: %v", r.id, err))
		return true, false
	}
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

// continueStep sends prompt to the session as its next message. A failed send ends the step failed and returns the error.
func (r *stepRun) continueStep(ctx context.Context, prompt StepPrompt) error {
	if err := r.sessions.Continue(ctx, r.id, prompt); err != nil {
		err = fmt.Errorf("continue step %s: %w", r.id, err)
		r.finish(ctx, stepLoopEndFailed, "", err.Error())
		return err
	}
	return nil
}

// loopState returns the loop state the run stores: its round and continue count, the nudges pending for the next turn
// end, whether the watch has spent its nudge, whether the run is held, whether the judge has sent the step back and its
// usage limit.
func (r *stepRun) loopState() store.StepLoop {
	return store.StepLoop{
		ID: r.id, Round: r.round, Continues: r.continues, Nudge: r.nudge, OwnerNudge: r.ownerNudge,
		Nudged: r.watch.nudgeSpent(), Held: r.held.Load(), JudgeSent: r.judgeSent,
		Limited: r.limited, LimitKind: r.limitKind, LimitResets: r.limitResets,
	}
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

// judge checks the return of round and stores it with its checks and the loop state, in one commit. An agent gate step's
// schema-valid return is also checked against the gate's assertions, and it ends done or gate_failed without a judge
// call. A schema-valid return of a step with a return check is checked by it. A done return whose checks all passed is
// judged, unless the step has a return check, which ends it done without a judge call. Any other return ends the step,
// or its failed checks are sent back. It reports whether the run goes on.
func (r *stepRun) judge(ctx context.Context, round int, value json.RawMessage) bool {
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
	var commit, gateDir string
	var verdict verdictReturn
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
		}
	}
	if schemaErr == nil {
		checks, verdict = r.valueChecks(round, value, checks)
	}
	ret := store.StepReturn{ID: r.id, Result: string(value), Checks: checks}
	if err := r.writer.Commit(ctx, r.id, ret, r.loopState()); err != nil {
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
	r.finish(ctx, stepLoopEndReturned, head.Status, "")
	return false
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
// gate it ran, which the caller clears once the return is stored, or at once when the checks fail. The four commit
// checks run first. The gate runs only when they all pass and a gate is set. A gate that cannot run is an error:
// errGateLost when its outcome is lost, and errGateDetach when it is left running under its holder. The gate's check
// opens with the record of its run, stepGateRecord. The step row stores the gate's grant, holder and round before the
// gate launches. runDoneGate re-adopts a gate the restored row stores as running in this round.
func (r *stepRun) doneChecks(ctx context.Context, round int, commit string,
	checks []store.StepCheck) ([]store.StepCheck, string, error) {
	passed := true
	for _, c := range commitChecks(ctx, r.dir, r.start, commit, r.owned) {
		checks = append(checks, stepLoopRecord(round, c))
		passed = passed && c.OK
	}
	if !passed || len(r.gate) == 0 {
		return checks, "", nil
	}
	r.beginGate()
	result, err := r.runDoneGate(ctx, round)
	r.endGate()
	if err != nil {
		return checks, "", fmt.Errorf("run gate of step %s: %w", r.id, err)
	}
	check := stepLoopGateCheck(round, result, r.gateLimit)
	record, err := stepGateRecord(r.gateName, r.gateSource, r.gate, result.Members)
	if err != nil {
		return checks, result.Holder, fmt.Errorf("record gate of step %s: %w", r.id, err)
	}
	check.Detail = record + check.Detail
	return append(checks, check), result.Holder, nil
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

// runDoneGate runs the gate of round. A gate the restored row stores as running in round is re-adopted from its grant
// and holder, and its exit is the result, so the gate is not run again. Any other gate runs once under a new grant, and
// Started stores that grant and holder before the gate launches.
func (r *stepRun) runDoneGate(ctx context.Context, round int) (gateResult, error) {
	run := gateRun{
		Argv: r.gate, Dir: r.dir, Limit: r.gateLimit, Environ: r.environ,
		Name: r.id + "-gate", StateDir: r.stateDir, Scope: r.scope,
	}
	if restored := r.resumeGate; restored.Holder != "" && restored.Round == round {
		r.resumeGate = store.StepGate{}
		return readoptGrantedGate(ctx, r.pools, r.grant, run, restored.Grant)
	}
	run.Started = func(ctx context.Context, grantID, dir string) error {
		return r.writer.Commit(ctx, r.id, store.StepGate{ID: r.id, Grant: grantID, Holder: dir, Round: round})
	}
	return runGrantedGate(ctx, r.pools, r.grant, run)
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

// retry sends the failed checks of round back to the session while it has continues left, and otherwise ends
// checks_failed. A raised continue count is stored once its Continue is sent. It reports whether the run goes on.
func (r *stepRun) retry(ctx context.Context, round int, checks []store.StepCheck) bool {
	failures := stepLoopFailures(checks)
	if r.continues == stepLoopMaxContinues {
		r.finish(ctx, stepLoopEndChecksFailed, "", strings.Join(failures, "; "))
		return false
	}
	r.continues++
	text := "Your return failed these checks. Return again with a result that passes them:\n- " +
		strings.Join(failures, "\n- ")
	prompt := StepPrompt{Text: text, Sender: store.PromptSenderEngine, Round: round}
	if err := r.sessions.Continue(ctx, r.id, prompt); err != nil {
		r.finish(ctx, stepLoopEndFailed, "", fmt.Sprintf("continue step %s: %v", r.id, err))
		return false
	}
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

// finish ends the step once. It stores StepEnd, stops the session unless limitReleased says a limit released it, runs
// cleanup and closes ended. A store, stop or cleanup error is logged, since nothing above finish can act on it. A ctx
// that has ended stores no end, so finish does nothing then. It reports whether this call acted.
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
