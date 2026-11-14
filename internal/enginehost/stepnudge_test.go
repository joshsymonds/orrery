package enginehost

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// errNudgeContinue is the error nudgeSessions returns from Continue when its continueErr is set to it.
var errNudgeContinue = errors.New("continue refused")

// stepNudgeDone is the structured return a read-only step's done turn ends with.
const stepNudgeDone = `{"status":"done"}`

// nudgeSessions is stepStartSessions that records each Interrupt and Continue text. Interrupt returns interruptErr and
// Continue returns continueErr.
type nudgeSessions struct {
	*stepStartSessions

	nmu          sync.Mutex
	interrupted  []string
	continued    []StepPrompt
	interruptErr error
	continueErr  error
}

func (f *nudgeSessions) Release(context.Context, string) error {
	return errors.New("nudgeSessions: Release is not used")
}

func (f *nudgeSessions) Interrupt(_ context.Context, id string) error {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	f.interrupted = append(f.interrupted, id)
	return f.interruptErr
}

func (f *nudgeSessions) Continue(_ context.Context, _ string, prompt StepPrompt) error {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	f.continued = append(f.continued, prompt)
	return f.continueErr
}

// failInterrupts makes each later Interrupt return err.
func (f *nudgeSessions) failInterrupts(err error) {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	f.interruptErr = err
}

// failContinues makes each later Continue return err.
func (f *nudgeSessions) failContinues(err error) {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	f.continueErr = err
}

// interruptCount returns how many times Interrupt was called.
func (f *nudgeSessions) interruptCount() int {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	return len(f.interrupted)
}

// continuedTexts returns the texts Continue was called with.
func (f *nudgeSessions) continuedTexts() []string {
	prompts := f.continuedPrompts()
	if len(prompts) == 0 {
		return nil
	}
	texts := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		texts = append(texts, prompt.Text)
	}
	return texts
}

// continuedPrompts returns a copy of the prompts Continue was called with.
func (f *nudgeSessions) continuedPrompts() []StepPrompt {
	f.nmu.Lock()
	defer f.nmu.Unlock()
	return append([]StepPrompt(nil), f.continued...)
}

// stepNudgeFixture is a Steps over a real store and writer, with nudgeSessions. It is closed at cleanup.
type stepNudgeFixture struct {
	steps    *Steps
	sessions *nudgeSessions
	store    *store.Store
	writer   *Writer
}

// newStepNudgeFixture returns a fixture with one Steps.
func newStepNudgeFixture(t *testing.T) *stepNudgeFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sessions := &nudgeSessions{stepStartSessions: &stepStartSessions{writer: writer}}
	steps, err := NewSteps(StepsConfig{
		Sessions:     sessions,
		Store:        s,
		Writer:       writer,
		Worktrees:    t.TempDir(),
		Environ:      []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Logger:       slog.New(slog.DiscardHandler),
		GateStateDir: gateShortStateDir(t),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return &stepNudgeFixture{steps: steps, sessions: sessions, store: s, writer: writer}
}

// start starts a read-only step at the head of a fresh repository and returns its id.
func (f *stepNudgeFixture) start(t *testing.T) string {
	t.Helper()
	repo := stepGitTestRepo(t)
	revision := stepGitTestRun(t, repo, "rev-parse", "HEAD")
	id, err := f.steps.Start(t.Context(), stepStartReadOnly(repo, revision))
	if err != nil {
		t.Fatalf("Start read-only step: %v", err)
	}
	return id
}

// beginTurn reports to the step's observer that its session started a turn.
func (f *stepNudgeFixture) beginTurn(t *testing.T) {
	t.Helper()
	f.sessions.first(t).Observer.Event(session.Started{})
}

// endTurn ends the running turn as the session does: its event first, then its return, a done return. The loop handles
// the return on its own, so a test that acts on the outcome calls settle first.
func (f *stepNudgeFixture) endTurn(t *testing.T, isError bool) {
	t.Helper()
	observer := f.sessions.first(t).Observer
	observer.Event(session.TurnEnded{IsError: isError})
	observer.Return(session.TurnEnded{IsError: isError, StructuredOutput: []byte(stepNudgeDone)})
}

// settle returns once the loop of id has taken every return handed to it and has handled it. The loop takes returns in
// its select and handles each before it selects again, so an owner action that round-trips through the loop after the
// returns channel is empty is handled after the return.
func (f *stepNudgeFixture) settle(t *testing.T, id string) {
	t.Helper()
	f.steps.mu.Lock()
	run := f.steps.runs[id]
	f.steps.mu.Unlock()
	if run == nil {
		t.Fatalf("step %s has no running loop", id)
	}
	stepStartAwait(t, "the loop to take the turn end", func() bool {
		return len(run.returns) == 0
	})
	if err := run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("settle round trip: %v", err)
	}
	if err := f.writer.Commit(t.Context(), id, store.EngineTextClear{ID: id}); err != nil {
		t.Fatalf("settle writer: %v", err)
	}
}

// status returns the step row of id.
func (f *stepNudgeFixture) status(t *testing.T, id string) store.StepRow {
	t.Helper()
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return row
}

// wantNoRound fails the test unless row stores no end, result or check.
func wantNoRound(t *testing.T, row store.StepRow) {
	t.Helper()
	if row.End != "" || row.Result != "" || len(row.Checks) != 0 {
		t.Errorf("stored end %q result %q checks %+v, want no round", row.End, row.Result, row.Checks)
	}
}

// wantContinued fails the test unless the session was continued with exactly want.
func wantContinued(t *testing.T, f *stepNudgeFixture, want []string) {
	t.Helper()
	if got := f.sessions.continuedTexts(); !reflect.DeepEqual(got, want) {
		t.Errorf("Continue texts = %q, want %q", got, want)
	}
}

func TestStepsNudgeHeldTurnEndedResumesWithTextAndNextTurnEndIsARound(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	f.endTurn(t, false)
	f.settle(t, id)

	if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
		t.Fatalf("Nudge of a held step whose turn ended: %v", err)
	}
	wantContinued(t, f, []string{"owner text"})
	if f.steps.Held(id) {
		t.Error("Held = true after Nudge, want false")
	}
	wantNoRound(t, f.status(t, id))

	f.beginTurn(t)
	f.endTurn(t, false)
	stepStartAwait(t, "the step to end", func() bool { return f.status(t, id).End != "" })
	if end := f.status(t, id).End; end != stepLoopEndDone {
		t.Errorf("end after the next turn end = %q, want an ordinary round ending %q", end, stepLoopEndDone)
	}
}

// TestStepsNudgeHeldTurnEndStillToComeResumesAtThatTurnEnd nudges a held step whose turn has not ended. The turn end
// is no round and is continued with the text, error or not.
func TestStepsNudgeHeldTurnEndStillToComeResumesAtThatTurnEnd(t *testing.T) {
	cases := []struct {
		name    string
		isError bool
	}{
		{name: "clean turn end"},
		{name: "error turn end", isError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepNudgeFixture(t)
			id := f.start(t)
			f.beginTurn(t)
			if err := f.steps.Hold(t.Context(), id); err != nil {
				t.Fatalf("Hold: %v", err)
			}

			if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
				t.Fatalf("Nudge of a held step: %v", err)
			}
			if got := f.sessions.continuedTexts(); len(got) != 0 {
				t.Fatalf("Continue texts before the held turn ends = %q, want none", got)
			}

			f.endTurn(t, tc.isError)
			f.settle(t, id)
			wantContinued(t, f, []string{"owner text"})
			if f.steps.Held(id) {
				t.Error("Held = true after the held turn ended, want false")
			}
			wantNoRound(t, f.status(t, id))
		})
	}
}

// TestStepsNudgeRunningTurnInterruptsAndContinuesInPlaceOfRound nudges a running turn. The interrupt lands, and the
// turn end, error or not, is continued with the text and stores no round.
func TestStepsNudgeRunningTurnInterruptsAndContinuesInPlaceOfRound(t *testing.T) {
	cases := []struct {
		name    string
		isError bool
	}{
		{name: "clean turn end"},
		{name: "error turn end", isError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepNudgeFixture(t)
			id := f.start(t)
			f.beginTurn(t)

			if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
				t.Fatalf("Nudge of a running turn: %v", err)
			}
			if got := f.sessions.interruptCount(); got != 1 {
				t.Fatalf("Interrupt calls = %d, want 1", got)
			}
			if f.steps.Held(id) {
				t.Error("Held = true after a nudge, want false")
			}

			f.endTurn(t, tc.isError)
			f.settle(t, id)
			wantContinued(t, f, []string{"owner text"})
			wantNoRound(t, f.status(t, id))

			f.beginTurn(t)
			f.endTurn(t, false)
			stepStartAwait(t, "the step to end", func() bool { return f.status(t, id).End != "" })
			if end := f.status(t, id).End; end != stepLoopEndDone {
				t.Errorf("end after the next turn end = %q, want %q", end, stepLoopEndDone)
			}
		})
	}
}

// TestStepsNudgeRunningTurnContinuesWithPendingWatchdogTextFirst checks that a turn end continued for an owner nudge
// carries the watchdog's pending text ahead of the owner's.
func TestStepsNudgeRunningTurnContinuesWithPendingWatchdogTextFirst(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	const watchdog = "watchdog nudge text"
	f.steps.mu.Lock()
	run := f.steps.runs[id]
	f.steps.mu.Unlock()
	run.nudge = watchdog

	if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
		t.Fatalf("Nudge: %v", err)
	}
	f.endTurn(t, false)
	f.settle(t, id)

	texts := f.sessions.continuedTexts()
	if len(texts) != 1 {
		t.Fatalf("Continue texts = %q, want one continue", texts)
	}
	w := strings.Index(texts[0], watchdog)
	o := strings.Index(texts[0], "owner text")
	if w < 0 || o < 0 || w > o {
		t.Errorf("continued text %q, want the watchdog text then the owner's", texts[0])
	}
	wantNoRound(t, f.status(t, id))
}

func TestStepsNudgeInterruptErrorLeavesNothingPending(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	f.sessions.failInterrupts(errHoldInterrupt)

	err := f.steps.Nudge(t.Context(), id, "owner text")
	if !errors.Is(err, errHoldInterrupt) {
		t.Fatalf("Nudge = %v, want the interrupt error", err)
	}
	if f.steps.Held(id) {
		t.Error("Held = true after a failed nudge, want false")
	}

	f.endTurn(t, false)
	wantContinued(t, f, nil)
	stepStartAwait(t, "the step to end", func() bool { return f.status(t, id).End != "" })
	if end := f.status(t, id).End; end != stepLoopEndDone {
		t.Errorf("end = %q, want the clean turn end to be an ordinary round ending %q", end, stepLoopEndDone)
	}
}

func TestStepsNudgeWithNoTurnRunningIsRefused(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)

	err := f.steps.Nudge(t.Context(), id, "owner text")
	if !errors.Is(err, ErrStepNoTurn) {
		t.Fatalf("Nudge with no turn running = %v, want ErrStepNoTurn", err)
	}
	if got := f.sessions.interruptCount(); got != 0 {
		t.Errorf("Interrupt calls = %d, want none", got)
	}
	if f.steps.Held(id) {
		t.Error("Held = true after a refused nudge, want false")
	}
}

func TestStepsNudgeEmptyTextIsRefused(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)

	err := f.steps.Nudge(t.Context(), id, "")
	if !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("Nudge with empty text = %v, want ErrEmptyPrompt", err)
	}
	if got := f.sessions.interruptCount(); got != 0 {
		t.Errorf("Interrupt calls = %d, want none", got)
	}
}

func TestStepsNudgeUnknownIDErrorsWithErrNoStep(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)

	err := f.steps.Nudge(t.Context(), "no-such-step", "owner text")
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Nudge of an unknown id = %v, want an error matching store.ErrNoStep", err)
	}
}

// TestStepsNudgeHeldContinueFailureEndsStepFailed checks that a held step's resume that cannot continue ends the step
// failed and returns the error.
func TestStepsNudgeHeldContinueFailureEndsStepFailed(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	f.endTurn(t, false)
	f.settle(t, id)
	f.sessions.failContinues(errNudgeContinue)

	err := f.steps.Nudge(t.Context(), id, "owner text")
	if !errors.Is(err, errNudgeContinue) {
		t.Fatalf("Nudge = %v, want the continue error", err)
	}
	if end := f.status(t, id).End; end != stepLoopEndFailed {
		t.Errorf("end = %q, want %q", end, stepLoopEndFailed)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.steps.mu.Lock()
		_, inLoops := f.steps.loops[id]
		_, inRuns := f.steps.runs[id]
		f.steps.mu.Unlock()
		if !inLoops && !inRuns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("step %s still in loops (%t) or runs (%t) after its failed Nudge ended it", id, inLoops, inRuns)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStepsHoldDropsPendingOwnerText holds a step after an owner nudge has interrupted its turn. The held turn's end
// continues nothing and stores no round.
func TestStepsHoldDropsPendingOwnerText(t *testing.T) {
	t.Parallel()
	f := newStepNudgeFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	if err := f.steps.Nudge(t.Context(), id, "owner text"); err != nil {
		t.Fatalf("Nudge: %v", err)
	}
	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold after the nudge: %v", err)
	}

	f.endTurn(t, false)
	f.settle(t, id)
	wantContinued(t, f, nil)
	wantNoRound(t, f.status(t, id))
	if !f.steps.Held(id) {
		t.Error("Held = false after the held turn ended, want true")
	}
}

// TestStepsNudgeAndHoldDuringGateRefusedPromptly checks that Hold and Nudge refuse at once while a step runs its gate.
// The gate sleeps a minute, so a call that waited for the loop would not return within the test's timeout.
func TestStepsNudgeAndHoldDuringGateRefusedPromptly(t *testing.T) {
	f := newStepCtlFixture(t)
	linked := stepWorktreeTestAddLinked(t, stepGitTestRepo(t))
	pidFile := filepath.Join(t.TempDir(), "gate.pid")
	id := f.startGated(t, linked, pidFile)
	stepCtlAwaitGatePid(t, pidFile)

	calls := []struct {
		name string
		call func() error
	}{
		{name: "Hold", call: func() error { return f.steps.Hold(t.Context(), id) }},
		{name: "Nudge", call: func() error { return f.steps.Nudge(t.Context(), id, "owner text") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			errs := make(chan error, 1)
			go func() { errs <- tc.call() }()
			select {
			case err := <-errs:
				if !errors.Is(err, ErrStepGating) {
					t.Fatalf("%s during the gate = %v, want ErrStepGating", tc.name, err)
				}
			case <-time.After(waitTimeout):
				t.Fatalf("%s did not return during the gate", tc.name)
			}
		})
	}
}

// TestStepRunOwnerActionWaitingWhenGateBeginsIsRefused checks an owner action already waiting for the loop when a gate
// begins: the loop is not running, so the call can only be released by the gate.
func TestStepRunOwnerActionWaitingWhenGateBeginsIsRefused(t *testing.T) {
	t.Parallel()
	run := newStepRun(stepRunConfig{id: stepLoopID, logger: slog.New(slog.DiscardHandler)})

	errs := make(chan error, 1)
	go func() {
		errs <- run.do(t.Context(), func(context.Context) error { return nil })
	}()
	run.beginGate()

	select {
	case err := <-errs:
		if !errors.Is(err, ErrStepGating) {
			t.Fatalf("owner action when the gate began = %v, want ErrStepGating", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("owner action waiting when the gate began did not return")
	}
}

// TestStepRunLoopingVerdictStillNudgesAfterOwnerNudge checks that an owner nudge does not spend the watchdog's one
// nudge: a later looping verdict still interrupts and continues the step with the watchdog's text.
func TestStepRunLoopingVerdictStillNudgesAfterOwnerNudge(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{
		watchLoopVerdict(watchVerdictLooping, watchLoopDoing),
	}}
	sessions := &stepLoopSessions{}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	ownerNudge := func(ctx context.Context) error { return f.run.nudgeOwner(ctx, "owner text") }
	if err := f.run.do(t.Context(), ownerNudge); err != nil {
		t.Fatalf("owner nudge: %v", err)
	}
	f.turnEnd(time.Minute, session.TurnEnded{IsError: true})
	if got := f.waitContinue(t); got != "owner text" {
		t.Fatalf("first continue = %q, want the owner's text", got)
	}

	f.event(time.Minute, session.Started{})
	f.tick(t, 6*time.Minute)
	verdicts := f.waitVerdicts(t, 1)
	watchLoopWantVerdict(t, verdicts[0], watchTripPeriodic, watchVerdictLooping, watchLoopDoing, false, "nudge")
	if got := sessions.interruptedIDs(); len(got) != 2 {
		t.Fatalf("interrupts = %d, want 2: the owner's and the looping verdict's", len(got))
	}

	f.turnEnd(6*time.Minute, session.TurnEnded{IsError: true})
	watchLoopWait(t, "the watchdog continue", func() bool {
		return len(sessions.continuedTexts()) == 2
	})
	watchLoopWantContains(t, sessions.continuedTexts()[1], "periodic", watchLoopDoing)
}
