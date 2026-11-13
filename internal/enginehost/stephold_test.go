package enginehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// errHoldInterrupt is the error holdSessions returns from Interrupt when its interruptErr is set to it.
var errHoldInterrupt = errors.New("interrupt refused")

// holdSessions is stepStartSessions that records each Interrupt, Continue and Stop. Interrupt returns interruptErr.
type holdSessions struct {
	*stepStartSessions

	mu           sync.Mutex
	interrupted  []string
	continued    []string
	stopped      []string
	interruptErr error
}

func (f *holdSessions) Interrupt(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupted = append(f.interrupted, id)
	return f.interruptErr
}

func (f *holdSessions) Continue(_ context.Context, id string, _ StepPrompt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.continued = append(f.continued, id)
	return nil
}

func (f *holdSessions) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, id)
	return nil
}

// interruptedIDs returns a copy of the ids Interrupt was called with.
func (f *holdSessions) interruptedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.interrupted...)
}

// continuedIDs returns a copy of the ids Continue was called with.
func (f *holdSessions) continuedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.continued...)
}

// stoppedIDs returns a copy of the ids Stop was called with.
func (f *holdSessions) stoppedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stopped...)
}

// stepHoldFixture is a Steps over a real store and writer, with holdSessions and worktrees under t.TempDir().
type stepHoldFixture struct {
	steps     *Steps
	sessions  *holdSessions
	store     *store.Store
	writer    *Writer
	worktrees string
}

// newStepHoldFixture returns a fixture with one Steps, closed at cleanup.
func newStepHoldFixture(t *testing.T) *stepHoldFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	sessions := &holdSessions{stepStartSessions: &stepStartSessions{writer: writer}}
	worktrees := t.TempDir()
	steps, err := NewSteps(StepsConfig{
		Sessions:  sessions,
		Store:     s,
		Writer:    writer,
		Worktrees: worktrees,
		Environ:   []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Logger:    slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(steps.Close)
	return &stepHoldFixture{steps: steps, sessions: sessions, store: s, writer: writer, worktrees: worktrees}
}

// start starts a read-only step at the head of a fresh repository and returns its id.
func (f *stepHoldFixture) start(t *testing.T) string {
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
func (f *stepHoldFixture) beginTurn(t *testing.T) {
	t.Helper()
	f.sessions.first(t).Observer.Event(session.Started{})
}

// loopRun returns the stepRun of the running loop of id. The test fails if there is none.
func (f *stepHoldFixture) loopRun(t *testing.T, id string) *stepRun {
	t.Helper()
	f.steps.mu.Lock()
	defer f.steps.mu.Unlock()
	run := f.steps.runs[id]
	if run == nil {
		t.Fatalf("step %s has no running loop", id)
	}
	return run
}

// settleHeldTurnEnd returns once the loop of id has taken the turn end handed to it and has handled it. The loop takes
// from its returns channel in its select and handles each one before it selects again, so a round trip through the
// loop's owner calls, made after the channel is empty, is handled after the turn end.
func (f *stepHoldFixture) settleHeldTurnEnd(t *testing.T, id string) {
	t.Helper()
	run := f.loopRun(t, id)
	stepStartAwait(t, "the loop to take the turn end", func() bool {
		return len(run.returns) == 0
	})
	if err := f.steps.Hold(t.Context(), id); !errors.Is(err, ErrStepHeld) {
		t.Fatalf("Hold after the turn end = %v, want ErrStepHeld", err)
	}
	f.settleWriter(t, id)
}

// settleWriter returns once every op queued before the call is stored.
func (f *stepHoldFixture) settleWriter(t *testing.T, id string) {
	t.Helper()
	if err := f.writer.Commit(t.Context(), id, store.EngineTextClear{ID: id}); err != nil {
		t.Fatalf("settle writer: %v", err)
	}
}

// status returns the step row of id.
func (f *stepHoldFixture) status(t *testing.T, id string) store.StepRow {
	t.Helper()
	row, err := f.steps.Status(t.Context(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return row
}

func TestStepsHoldInterruptsRunningTurnOnceAndHoldsStep(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	if f.steps.Held(id) {
		t.Fatal("Held = true before Hold, want false")
	}

	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold: %v", err)
	}

	if got, want := f.sessions.interruptedIDs(), []string{id}; !reflect.DeepEqual(got, want) {
		t.Errorf("Interrupt calls = %v, want %v", got, want)
	}
	if !f.steps.Held(id) {
		t.Error("Held = false after Hold, want true")
	}
	if row := f.status(t, id); row.End != "" {
		t.Errorf("end = %q after Hold, want the step still open", row.End)
	}
}

func TestStepsHoldWithNoTurnRunningIsRefused(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)

	err := f.steps.Hold(t.Context(), id)
	if !errors.Is(err, ErrStepNoTurn) {
		t.Fatalf("Hold with no turn running = %v, want ErrStepNoTurn", err)
	}
	if got := f.sessions.interruptedIDs(); len(got) != 0 {
		t.Errorf("Interrupt calls = %v, want none", got)
	}
	if f.steps.Held(id) {
		t.Error("Held = true after a refused Hold, want false")
	}
}

func TestStepsHoldOfHeldStepIsRefused(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold: %v", err)
	}

	err := f.steps.Hold(t.Context(), id)
	if !errors.Is(err, ErrStepHeld) {
		t.Fatalf("second Hold = %v, want ErrStepHeld", err)
	}
	if got := f.sessions.interruptedIDs(); len(got) != 1 {
		t.Errorf("Interrupt calls = %v, want exactly one", got)
	}
}

func TestStepsHoldInterruptErrorLeavesStepUnheld(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	f.sessions.mu.Lock()
	f.sessions.interruptErr = errHoldInterrupt
	f.sessions.mu.Unlock()

	err := f.steps.Hold(t.Context(), id)
	if !errors.Is(err, errHoldInterrupt) {
		t.Fatalf("Hold = %v, want the interrupt error", err)
	}
	if f.steps.Held(id) {
		t.Error("Held = true after a failed interrupt, want false")
	}
}

func TestStepsHoldOfEndedStepIsRefused(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)
	f.beginTurn(t)
	raw := fmt.Sprintf(`{"status":%q}`, stepDefaultDone)
	f.sessions.first(t).Observer.Return(session.TurnEnded{StructuredOutput: []byte(raw)})
	stepStartAwait(t, "the step to end", func() bool {
		return f.status(t, id).End != ""
	})

	err := f.steps.Hold(t.Context(), id)
	if !errors.Is(err, ErrStepNotRunning) {
		t.Fatalf("Hold of an ended step = %v, want ErrStepNotRunning", err)
	}
	if f.steps.Held(id) {
		t.Error("Held = true for an ended step, want false")
	}
}

func TestStepsHoldUnknownIDErrorsWithErrNoStep(t *testing.T) {
	f := newStepHoldFixture(t)

	err := f.steps.Hold(t.Context(), "no-such-step")
	if !errors.Is(err, store.ErrNoStep) {
		t.Fatalf("Hold of an unknown id = %v, want an error matching store.ErrNoStep", err)
	}
	if f.steps.Held("no-such-step") {
		t.Error("Held = true for an unknown id, want false")
	}
}

// TestStepsHeldTurnEndIsNoRound holds a running turn, then ends it, clean or with an error, with a nudge pending or not.
// The held turn's end stores no return or check, continues nothing and leaves the step held.
func TestStepsHeldTurnEndIsNoRound(t *testing.T) {
	cases := []struct {
		name    string
		isError bool
		nudge   string
	}{
		{name: "clean turn end"},
		{name: "error turn end", isError: true},
		{name: "error turn end with a pending nudge", isError: true, nudge: "watchdog nudge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepHoldFixture(t)
			id := f.start(t)
			f.beginTurn(t)
			if err := f.steps.Hold(t.Context(), id); err != nil {
				t.Fatalf("Hold: %v", err)
			}
			run := f.loopRun(t, id)
			run.nudge = tc.nudge

			observer := f.sessions.first(t).Observer
			observer.Event(session.TurnEnded{IsError: tc.isError})
			raw := fmt.Sprintf(`{"status":%q}`, stepDefaultDone)
			observer.Return(session.TurnEnded{IsError: tc.isError, StructuredOutput: json.RawMessage(raw)})
			f.settleHeldTurnEnd(t, id)

			row := f.status(t, id)
			if row.End != "" || row.Result != "" || len(row.Checks) != 0 {
				t.Errorf("held turn end stored end %q result %q checks %+v, want none", row.End, row.Result, row.Checks)
			}
			if got := f.sessions.continuedIDs(); len(got) != 0 {
				t.Errorf("Continue calls = %v, want none", got)
			}
			if got, want := f.sessions.interruptedIDs(), []string{id}; !reflect.DeepEqual(got, want) {
				t.Errorf("Interrupt calls = %v, want %v", got, want)
			}
			if !f.steps.Held(id) {
				t.Error("Held = false after the held turn ended, want true")
			}
		})
	}
}

func TestStepsStopOfHeldStepEndsItStopped(t *testing.T) {
	f := newStepHoldFixture(t)
	id := f.start(t)
	wt := filepath.Join(f.worktrees, id)
	f.beginTurn(t)
	if err := f.steps.Hold(t.Context(), id); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if !f.steps.Held(id) {
		t.Fatal("Held = false after Hold, want true")
	}

	if err := f.steps.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if row := f.status(t, id); row.End != "stopped" {
		t.Errorf("end = %q, want stopped", row.End)
	}
	if got := f.sessions.stoppedIDs(); !reflect.DeepEqual(got, []string{id}) {
		t.Errorf("session Stop calls = %v, want [%s]", got, id)
	}
	if !stepStartGone(wt) {
		t.Errorf("worktree %s still exists after Stop", wt)
	}
	if f.steps.Held(id) {
		t.Error("Held = true after Stop, want false")
	}
}

func TestStepWatchTurnRunningFollowsTurnEvents(t *testing.T) {
	start := time.Unix(1, 0)
	w := newStepWatch(start, time.Hour)
	if w.turnRunning() {
		t.Fatal("turnRunning = true before any turn, want false")
	}
	w.event(start, session.Started{})
	if !w.turnRunning() {
		t.Fatal("turnRunning = false after Started, want true")
	}
	w.event(start.Add(time.Minute), session.TurnEnded{})
	if w.turnRunning() {
		t.Error("turnRunning = true after TurnEnded, want false")
	}
}

// TestStepRunHeldStepTripsNoBackstop holds a running turn, then hands the run a tick an hour past the one-hour model
// limit. A checked step would trip overtime and end at that tick. The held step's run is still running after it, and an
// unheld step ends overtime at the same tick.
func TestStepRunHeldStepTripsNoBackstop(t *testing.T) {
	held := newWatchLoopFixture(t, nil, &stepLoopSessions{}, time.Hour)
	held.event(0, session.Started{})
	if err := held.run.do(t.Context(), held.run.hold); err != nil {
		t.Fatalf("hold: %v", err)
	}
	held.tick(t, 2*time.Hour)
	if err := held.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("held run ended on a tick: %v", err)
	}

	free := newWatchLoopFixture(t, nil, &stepLoopSessions{}, time.Hour)
	free.event(0, session.Started{})
	free.tick(t, 2*time.Hour)
	if end := free.waitEnd(t).End; end != string(watchEndOvertime) {
		t.Fatalf("unheld tick end = %q, want %q", end, watchEndOvertime)
	}
}
