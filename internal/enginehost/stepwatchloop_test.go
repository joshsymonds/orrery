package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// watchLoopDoing is the doing line the first canned monitor reply of most tests carries.
const watchLoopDoing = "rerunning the same grep"

// watchLoopModelLimit is a model-time limit no test reaches unless it means to.
const watchLoopModelLimit = 60 * time.Minute

// watchLoopClock is the injected clock a watch loop test drives. It reads what the test last set.
type watchLoopClock struct {
	mu sync.Mutex
	at time.Time
}

// now returns the clock's reading.
func (c *watchLoopClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

// set moves the clock to d after watchEpoch.
func (c *watchLoopClock) set(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = watchAt(d)
}

// watchLoopReply is one canned reply of a watchLoopHaiku: a structured output, or an error when err is set.
type watchLoopReply struct {
	raw json.RawMessage
	err error
}

// watchLoopVerdict is a canned monitor reply with verdict and doing.
func watchLoopVerdict(verdict, doing string) watchLoopReply {
	return watchLoopReply{raw: json.RawMessage(fmt.Sprintf(`{"verdict":%q,"doing":%q}`, verdict, doing))}
}

// watchLoopFailure is a canned reply of a failed call.
func watchLoopFailure() watchLoopReply {
	return watchLoopReply{err: errors.New("haiku: claude run failed")}
}

// watchLoopHaiku answers each call with the next canned reply and records each call's schema. A call past its replies
// fails.
type watchLoopHaiku struct {
	mu      sync.Mutex
	replies []watchLoopReply
	schemas []string
}

func (h *watchLoopHaiku) Call(_ context.Context, schema, _ string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.schemas = append(h.schemas, schema)
	if len(h.replies) == 0 {
		return nil, errors.New("watchLoopHaiku: no canned reply left")
	}
	reply := h.replies[0]
	h.replies = h.replies[1:]
	return reply.raw, reply.err
}

// recorded returns a copy of the schemas the calls were made with.
func (h *watchLoopHaiku) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.schemas...)
}

// watchLoopFixture is a stepRun over a real store and writer, with a step row, a clock the test sets and a tick channel
// the test feeds. Its run is started by newWatchLoopFixture and stops when the test ends.
type watchLoopFixture struct {
	run      *stepRun
	store    *store.Store
	sessions *stepLoopSessions
	clock    *watchLoopClock
	ticks    chan time.Time
	done     chan struct{}
}

// newWatchLoopFixture returns a started fixture whose run calls caller for monitor verdicts (nil for none), drives
// sessions, and allows the step modelLimit of model time.
func newWatchLoopFixture(t *testing.T, caller haikuCaller, sessions *stepLoopSessions,
	modelLimit time.Duration) *watchLoopFixture {
	t.Helper()
	s, _ := openStore(t)
	if err := s.ApplyEngine(t.Context(), []store.EngineOp{
		store.EngineAdd{ID: stepLoopID, Dir: "/work", State: "working", Kind: store.KindStep},
		store.StepAdd{
			ID: stepLoopID, Agent: "implementer", Prompt: "do it", Schema: stepLoopSchema, Workspace: "/ws",
			Revision: "abc123", Worktree: "/wt", DoneValue: stepLoopDone,
		},
	}, time.Unix(1, 0)); err != nil {
		t.Fatalf("add step row: %v", err)
	}
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	f := &watchLoopFixture{
		store:    s,
		sessions: sessions,
		clock:    &watchLoopClock{at: watchEpoch()},
		ticks:    make(chan time.Time),
		done:     make(chan struct{}),
	}
	f.run = newStepRun(stepRunConfig{
		id: stepLoopID, schema: stepLoopSchema, doneValue: stepLoopDone, sessions: sessions, writer: writer,
		logger: slog.New(slog.DiscardHandler), now: f.clock.now, modelLimit: modelLimit, haiku: caller,
		ticks: f.ticks,
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		defer close(f.done)
		f.run.run(ctx)
	}()
	return f
}

// event hands ev to the watch at d after watchEpoch.
func (f *watchLoopFixture) event(d time.Duration, ev session.Event) {
	f.clock.set(d)
	f.run.Event(ev)
}

// turnEnd ends the running turn at d as the session does: its event first, then its return.
func (f *watchLoopFixture) turnEnd(d time.Duration, ended session.TurnEnded) {
	f.event(d, ended)
	f.run.Return(ended)
}

// tick sets the clock to d and hands the run one tick. The send returns once the run has taken the tick, and the run
// handles a tick completely before it takes another hand-off, so a later hand-off is handled after this tick.
func (f *watchLoopFixture) tick(t *testing.T, d time.Duration) {
	t.Helper()
	f.clock.set(d)
	select {
	case f.ticks <- time.Time{}:
	case <-time.After(waitTimeout):
		t.Fatal("run did not take the tick")
	}
}

// row returns the step row the fixture's store holds.
func (f *watchLoopFixture) row(t *testing.T) store.StepRow {
	t.Helper()
	row, err := f.store.Step(t.Context(), stepLoopID)
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	return row
}

// waitVerdicts returns the step's verdicts once there are at least n.
func (f *watchLoopFixture) waitVerdicts(t *testing.T, n int) []store.StepVerdict {
	t.Helper()
	var verdicts []store.StepVerdict
	watchLoopWait(t, fmt.Sprintf("%d verdicts", n), func() bool {
		verdicts = f.row(t).Verdicts
		return len(verdicts) >= n
	})
	return verdicts
}

// waitContinue returns the text of the first Continue once one has been called.
func (f *watchLoopFixture) waitContinue(t *testing.T) string {
	t.Helper()
	var texts []string
	watchLoopWait(t, "a continue", func() bool {
		texts = f.sessions.continuedTexts()
		return len(texts) > 0
	})
	return texts[0]
}

// waitEnd waits for the run to return and returns the step row.
func (f *watchLoopFixture) waitEnd(t *testing.T) store.StepRow {
	t.Helper()
	waitRunEnd(t, f.done)
	return f.row(t)
}

// watchLoopWait polls ok until it returns true and fails the test once waitTimeout has passed.
func watchLoopWait(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// watchLoopWantVerdict asserts that got is the stored monitor verdict for trip with the given verdict, doing line and
// action. wantErr says whether the call failed: its verdict and doing line are then empty and its error is set.
func watchLoopWantVerdict(t *testing.T, got store.StepVerdict, trip watchTripKind, verdict, doing string,
	wantErr bool, action string) {
	t.Helper()
	if wantErr && got.Error == "" {
		t.Fatalf("verdict %+v has no error, want the failed call's", got)
	}
	if !wantErr && got.Error != "" {
		t.Fatalf("verdict %+v has error %q, want none", got, got.Error)
	}
	got.At, got.Error = 0, ""
	want := store.StepVerdict{Kind: "monitor", Trip: string(trip), Verdict: verdict, Detail: doing, Action: action}
	if got != want {
		t.Fatalf("verdict = %+v, want %+v", got, want)
	}
}

// watchLoopWantContains asserts that text contains each of parts.
func watchLoopWantContains(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("text %q does not contain %q", text, part)
		}
	}
}

func TestWatchLoopLoopingNudgesThenEndsLooping(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictLooping, watchLoopDoing),
		watchLoopVerdict(watchVerdictLooping, "still rerunning the grep"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripPeriodic, watchVerdictLooping, watchLoopDoing, false, "nudge")
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupts = %d, want 1 for the first looping verdict", len(got))
	}

	f.turnEnd(5*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	text := f.waitContinue(t)
	watchLoopWantContains(t, text, "periodic", watchLoopDoing)
	if row := f.row(t); row.Result != "" || len(row.Checks) != 0 {
		t.Fatalf("interrupted turn end was judged: result %q, checks %+v", row.Result, row.Checks)
	}

	f.event(5*time.Minute, session.Started{})
	f.tick(t, 10*time.Minute)
	verdicts = f.waitVerdicts(t, 2)
	watchLoopWantVerdict(t, verdicts[1], watchTripPeriodic, watchVerdictLooping, "still rerunning the grep",
		false, "end")

	row := f.waitEnd(t)
	if row.End != "looping" {
		t.Fatalf("end = %q, want looping", row.End)
	}
	watchLoopWantContains(t, row.Reason, "periodic")
	if row.Doing != "still rerunning the grep" {
		t.Fatalf("doing = %q, want the latest doing line", row.Doing)
	}
	if got := sessions.stoppedIDs(); len(got) != 1 {
		t.Fatalf("stops = %d, want 1", len(got))
	}
	if got := sessions.continuedTexts(); len(got) != 1 {
		t.Fatalf("continues = %d, want 1: the nudge only", len(got))
	}
	for i, schema := range haiku.recorded() {
		if schema != stepMonitorSchema {
			t.Fatalf("monitor call %d used schema %q, want the monitor schema", i, schema)
		}
	}
}

func TestWatchLoopNonErrorTurnEndAfterNudgeIsARound(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictProgressing, "reading the diff"),
		watchLoopVerdict(watchVerdictProgressing, "thinking"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripPeriodic, watchVerdictProgressing, "reading the diff", false, "none")

	f.tick(t, 10*time.Minute)
	verdicts = f.waitVerdicts(t, 2)
	watchLoopWantVerdict(t, verdicts[1], watchTripSilent, watchVerdictProgressing, "thinking", false, "nudge")

	f.turnEnd(10*time.Minute, session.TurnEnded{
		Subtype: "success", StructuredOutput: json.RawMessage(`{"status":"DONE"}`),
	})
	row := f.waitEnd(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done: the non-error turn end is a round", row.End)
	}
	if got := sessions.continuedTexts(); len(got) != 0 {
		t.Fatalf("continues = %q, want none: the nudge is dropped", got)
	}
	if len(row.Checks) != 1 {
		t.Fatalf("checks = %+v, want one schema check", row.Checks)
	}
	if check := row.Checks[0]; check.Round != 1 || check.Name != stepLoopSchemaName || !check.OK {
		t.Fatalf("check = %+v, want a passing schema check of round 1", check)
	}
}

func TestWatchLoopSilentNudgesThenEndsSilent(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictProgressing, "thinking"),
		watchLoopVerdict(watchVerdictProgressing, "still thinking"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, watchVerdictProgressing, "thinking", false, "nudge")

	f.turnEnd(10*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	text := f.waitContinue(t)
	watchLoopWantContains(t, text, "silent", "thinking")

	f.event(10*time.Minute, session.Started{})
	f.tick(t, 20*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripSilent, watchVerdictProgressing, "still thinking", false, "end")
	if row.End != "silent" {
		t.Fatalf("end = %q, want silent", row.End)
	}
	watchLoopWantContains(t, row.Reason, "silent")
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupts = %d, want 1", len(got))
	}
}

func TestWatchLoopLongCommandExtendsThenEndsToolStuck(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictLongCommand, "running the test suite"),
		watchLoopVerdict(watchVerdictProgressing, "waiting on the tests"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})
	f.event(0, session.ToolCallStarted{ToolUseID: watchToolID, Name: "Bash"})

	f.tick(t, 30*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripToolStuck, watchVerdictLongCommand, "running the test suite",
		false, "extend")

	f.tick(t, 60*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripToolStuck, watchVerdictProgressing, "waiting on the tests",
		false, "end")
	if row.End != "tool_stuck" {
		t.Fatalf("end = %q, want tool_stuck after the extension ran out", row.End)
	}
	if got := sessions.interruptedIDs(); len(got) != 0 {
		t.Fatalf("interrupts = %d, want none for an extension", len(got))
	}
	if got := sessions.continuedTexts(); len(got) != 0 {
		t.Fatalf("continues = %q, want none for an extension", got)
	}
}

func TestWatchLoopOvertimeEndsStep(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictProgressing, "still going")}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, 20*time.Minute)
	f.event(0, session.Started{})

	f.tick(t, 20*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[0], watchTripOvertime, watchVerdictProgressing, "still going", false, "end")
	if row.End != "overtime" {
		t.Fatalf("end = %q, want overtime", row.End)
	}
	if got := sessions.interruptedIDs(); len(got) != 0 {
		t.Fatalf("interrupts = %d, want none at overtime", len(got))
	}
}

func TestWatchLoopFailedCallActsMechanically(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopFailure(), watchLoopFailure()}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, "", "", true, "nudge")
	f.turnEnd(10*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	text := f.waitContinue(t)
	watchLoopWantContains(t, text, "silent")

	f.event(10*time.Minute, session.Started{})
	f.tick(t, 20*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripSilent, "", "", true, "end")
	if row.End != "silent" {
		t.Fatalf("end = %q, want silent", row.End)
	}
}

func TestWatchLoopNilCallerEndsToolStuckMechanically(t *testing.T) {
	t.Parallel()
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, nil, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})
	f.event(0, session.ToolCallStarted{ToolUseID: watchToolID, Name: "Bash"})

	f.tick(t, 30*time.Minute)
	row := f.waitEnd(t)
	if len(row.Verdicts) != 1 {
		t.Fatalf("verdicts = %d, want 1", len(row.Verdicts))
	}
	watchLoopWantVerdict(t, row.Verdicts[0], watchTripToolStuck, "", "", true, "end")
	if row.End != "tool_stuck" {
		t.Fatalf("end = %q, want tool_stuck", row.End)
	}
}

func TestWatchLoopFailedInterruptDropsNudge(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictProgressing, "thinking")}}
	sessions := &stepLoopSessions{interruptErr: errors.New("interrupt refused")}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, watchVerdictProgressing, "thinking", false, "none")
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupt attempts = %d, want 1", len(got))
	}

	f.turnEnd(10*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	text := f.waitContinue(t)
	watchLoopWantContains(t, text, "schema:")
	if strings.Contains(text, "watchdog") {
		t.Fatalf("continue %q carries the dropped nudge", text)
	}

	f.turnEnd(10*time.Minute, session.TurnEnded{
		Subtype: "success", StructuredOutput: json.RawMessage(`{"status":"DONE"}`),
	})
	if row := f.waitEnd(t); row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
}

func TestWatchLoopFailedNudgeContinueEndsFailed(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictProgressing, "thinking")}}
	sessions := &stepLoopSessions{continueErr: errors.New("continue refused")}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	f.waitVerdicts(t, 1)
	f.turnEnd(10*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	row := f.waitEnd(t)
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want failed", row.End)
	}
	watchLoopWantContains(t, row.Reason, "continue step")
}

func TestWatchLoopQuietTickMakesNoCall(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{}
	f := newWatchLoopFixture(t, haiku, &stepLoopSessions{}, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, time.Minute)
	f.turnEnd(time.Minute, session.TurnEnded{
		Subtype: "success", StructuredOutput: json.RawMessage(`{"status":"DONE"}`),
	})
	row := f.waitEnd(t)
	// The done return is judged by the same caller, so its one call is a judge call, not a monitor call.
	var monitors int
	for _, schema := range haiku.recorded() {
		if schema == stepMonitorSchema {
			monitors++
		}
	}
	if monitors != 0 {
		t.Fatalf("monitor calls = %d, want none before any trip", monitors)
	}
	for _, v := range row.Verdicts {
		if v.Kind == watchLoopKind {
			t.Fatalf("verdicts = %+v, want no monitor verdict", row.Verdicts)
		}
	}
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
}

// The turn end after a nudge's interrupt is no round: the next return is judged as round 1.
func TestWatchLoopInterruptedTurnEndIsNoRound(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictLooping, watchLoopDoing)}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	f.waitVerdicts(t, 1)
	f.turnEnd(5*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	watchLoopWantContains(t, f.waitContinue(t), "watchdog")

	f.event(5*time.Minute, session.Started{})
	f.turnEnd(6*time.Minute, session.TurnEnded{
		Subtype: "success", StructuredOutput: json.RawMessage(`{"status":"DONE"}`),
	})
	row := f.waitEnd(t)
	if row.End != stepLoopEndDone {
		t.Fatalf("end = %q, want done", row.End)
	}
	if len(row.Checks) != 1 || row.Checks[0].Round != 1 {
		t.Fatalf("checks = %+v, want one check of round 1", row.Checks)
	}
}

// A non-error turn end after a nudge is a round and drops the nudge: a later error turn end is a round too, sent back
// for its checks, never continued with the nudge.
func TestWatchLoopNonErrorTurnEndDropsTheNudge(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictProgressing, "thinking")}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, watchVerdictProgressing, "thinking", false, "nudge")

	f.turnEnd(10*time.Minute, session.TurnEnded{
		Subtype: "success", StructuredOutput: json.RawMessage(`{"status":1}`),
	})
	watchLoopWantContains(t, f.waitContinue(t), "schema:")
	f.turnEnd(11*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	watchLoopWait(t, "a second continue", func() bool { return len(sessions.continuedTexts()) >= 2 })
	for i, text := range sessions.continuedTexts() {
		if strings.Contains(text, "watchdog") {
			t.Fatalf("continue %d %q carries the dropped nudge", i, text)
		}
		watchLoopWantContains(t, text, "schema:")
	}
	watchLoopWait(t, "the second round's checks", func() bool {
		checks := f.row(t).Checks
		return len(checks) == 2 && checks[1].Round == 2
	})
}

// A nudge whose interrupt fails leaves the step its nudge and the watch awake: the next silent trip in the same turn
// tries the nudge again.
func TestWatchLoopFailedInterruptKeepsWatching(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictProgressing, "thinking"),
		watchLoopVerdict(watchVerdictProgressing, "still thinking"),
	}}
	sessions := &stepLoopSessions{interruptErr: errors.New("interrupt refused")}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	f.waitVerdicts(t, 1)
	f.tick(t, 20*time.Minute)
	verdicts := f.waitVerdicts(t, 2)
	watchLoopWantVerdict(t, verdicts[1], watchTripSilent, watchVerdictProgressing, "still thinking", false, "none")
	if got := sessions.interruptedIDs(); len(got) != 2 {
		t.Fatalf("interrupt attempts = %d, want 2", len(got))
	}
}

// A landed silent nudge whose turn never ends still ends silent: the silence count runs from the nudge.
func TestWatchLoopNudgedTurnEndsSilentWithoutTurnEnd(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictProgressing, "thinking"),
		watchLoopVerdict(watchVerdictProgressing, "still thinking"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, watchVerdictProgressing, "thinking", false, "nudge")

	f.tick(t, 20*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripSilent, watchVerdictProgressing, "still thinking", false, "end")
	if row.End != "silent" {
		t.Fatalf("end = %q, want silent", row.End)
	}
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupts = %d, want 1", len(got))
	}
}

// A landed nudge on a stuck tool call whose turn never ends still ends tool_stuck: the call's clock restarts at the
// nudge, so the call trips again thirty minutes after it.
func TestWatchLoopNudgedToolCallEndsToolStuckWithoutTurnEnd(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictWedged, "stuck on the grep"),
		watchLoopVerdict(watchVerdictProgressing, "still stuck"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})
	f.event(0, session.ToolCallStarted{ToolUseID: watchToolID, Name: "Bash"})

	f.tick(t, 30*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripToolStuck, watchVerdictWedged, "stuck on the grep", false, "nudge")

	f.event(31*time.Minute, session.Block{Kind: session.BlockText, Role: "assistant", Text: "still waiting"})
	f.tick(t, 60*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripToolStuck, watchVerdictProgressing, "still stuck", false, "end")
	if row.End != "tool_stuck" {
		t.Fatalf("end = %q, want tool_stuck", row.End)
	}
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupts = %d, want 1", len(got))
	}
}

// A landed nudge whose turn never ends still ends overtime at the model-time limit.
func TestWatchLoopNudgedTurnEndsOvertimeWithoutTurnEnd(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictProgressing, "thinking"),
		watchLoopVerdict(watchVerdictProgressing, "still going"),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, 20*time.Minute)
	f.event(0, session.Started{})

	f.tick(t, 10*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripSilent, watchVerdictProgressing, "thinking", false, "nudge")

	f.event(15*time.Minute, session.Block{Kind: session.BlockText, Role: "assistant", Text: "still thinking"})
	f.tick(t, 20*time.Minute)
	row := f.waitEnd(t)
	watchLoopWantVerdict(t, row.Verdicts[1], watchTripOvertime, watchVerdictProgressing, "still going", false, "end")
	if row.End != "overtime" {
		t.Fatalf("end = %q, want overtime", row.End)
	}
	if got := sessions.interruptedIDs(); len(got) != 1 {
		t.Fatalf("interrupts = %d, want 1", len(got))
	}
}

// A nudge on a stuck tool call whose interrupt fails leaves the call's clock alone: the call is still past its limit,
// so the next tick trips tool_stuck again and tries the nudge again.
func TestWatchLoopFailedInterruptKeepsToolClock(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictWedged, "stuck on the grep"),
		watchLoopVerdict(watchVerdictWedged, "still stuck"),
	}}
	sessions := &stepLoopSessions{interruptErr: errors.New("interrupt refused")}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})
	f.event(0, session.ToolCallStarted{ToolUseID: watchToolID, Name: "Bash"})

	f.tick(t, 30*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripToolStuck, watchVerdictWedged, "stuck on the grep", false, "none")

	f.tick(t, 30*time.Minute+5*time.Second)
	verdicts = f.waitVerdicts(t, 2)
	watchLoopWantVerdict(t, verdicts[1], watchTripToolStuck, watchVerdictWedged, "still stuck", false, "none")
	if got := sessions.interruptedIDs(); len(got) != 2 {
		t.Fatalf("interrupt attempts = %d, want 2", len(got))
	}
}
