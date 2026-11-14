package enginehost

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepWireWatchTick is the watch tick the wiring tests run at.
const stepWireWatchTick = 5 * time.Millisecond

// stepWireMonitorLine is a success result line whose monitor verdict is progressing.
const stepWireMonitorLine = `{"type":"result","subtype":"success","is_error":false,` +
	`"structured_output":{"verdict":"progressing","doing":"building the package"}}`

// stepWireSessions is stepStartSessions that hands the observer a Started event once the step starts, as a real
// session does, so the step's turn is running on its watch.
type stepWireSessions struct {
	*stepStartSessions
}

func (f *stepWireSessions) StartStep(ctx context.Context, step StepSession) error {
	if err := f.stepStartSessions.StartStep(ctx, step); err != nil {
		return err
	}
	step.Observer.Event(session.Started{SessionID: step.ID})
	return nil
}

// stepWireFixture is a Steps configured with the Haiku caller, an injected clock and stepWireWatchTick.
type stepWireFixture struct {
	steps    *Steps
	sessions *stepWireSessions
	store    *store.Store
	clock    *watchLoopClock
}

// newStepWireFixture returns a fixture whose Steps calls haiku for the monitor and judge, with nil meaning none.
func newStepWireFixture(t *testing.T, haiku *Haiku) *stepWireFixture {
	t.Helper()
	s, _ := openStore(t)
	writer := NewWriter(WriterConfig{Applier: s, Tick: 5 * time.Millisecond})
	startRun(t, writer)
	clock := &watchLoopClock{at: watchEpoch()}
	sessions := &stepWireSessions{stepStartSessions: &stepStartSessions{writer: writer}}
	steps, err := NewSteps(StepsConfig{
		Sessions:     sessions,
		Store:        s,
		Writer:       writer,
		Worktrees:    t.TempDir(),
		Environ:      []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		Logger:       slog.New(slog.DiscardHandler),
		Haiku:        haiku,
		Now:          clock.now,
		WatchTick:    stepWireWatchTick,
		GateStateDir: gateShortStateDir(t),
	})
	if err != nil {
		t.Fatalf("NewSteps: %v", err)
	}
	t.Cleanup(func() {
		steps.mu.Lock()
		defer steps.mu.Unlock()
		for _, cancel := range steps.loops {
			cancel()
		}
	})
	return &stepWireFixture{steps: steps, sessions: sessions, store: s, clock: clock}
}

// stepWireRun returns the run of the step StartStep was called with first.
func stepWireRun(t *testing.T, f *stepWireFixture) *stepRun {
	t.Helper()
	run, ok := f.sessions.first(t).Observer.(*stepRun)
	if !ok {
		t.Fatal("the step's observer is not a stepRun")
	}
	return run
}

// stepWireStartOvertime starts a writing step with a two-minute model limit and runs its turn past that limit, so the
// first watch tick trips overtime. It returns the fixture and the step id.
func stepWireStartOvertime(t *testing.T, haiku *Haiku) (*stepWireFixture, string) {
	t.Helper()
	f := newStepWireFixture(t, haiku)
	start := stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
	start.ModelLimit = 2 * time.Minute
	id, err := f.steps.Start(t.Context(), start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.clock.set(2 * time.Minute)
	return f, id
}

func TestStepsStartWiresEachRun(t *testing.T) {
	h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepWireMonitorLine}),
		Environ: haikuEnviron()})
	cases := []struct {
		name       string
		haiku      *Haiku
		modelLimit time.Duration
		wantLimit  time.Duration
	}{
		{name: "default model limit, configured haiku", haiku: h, wantLimit: stepDefaultModelLimit},
		{name: "given model limit, no haiku", modelLimit: 7 * time.Minute, wantLimit: 7 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStepWireFixture(t, tc.haiku)
			f.clock.set(90 * time.Second)
			start := stepStartWriting(stepWorktreeTestAddLinked(t, stepGitTestRepo(t)))
			start.Prompt = "the first prompt"
			start.ModelLimit = tc.modelLimit
			if _, err := f.steps.Start(t.Context(), start); err != nil {
				t.Fatalf("Start: %v", err)
			}
			run := stepWireRun(t, f)

			if got := run.now(); !got.Equal(watchAt(90 * time.Second)) {
				t.Errorf("run clock reads %v, want the injected clock %v", got, watchAt(90*time.Second))
			}
			if run.watch.limit != tc.wantLimit {
				t.Errorf("run model limit is %v, want %v", run.watch.limit, tc.wantLimit)
			}
			if run.prompt != "the first prompt" {
				t.Errorf("run prompt is %q, want the start's prompt", run.prompt)
			}
			if tc.haiku == nil {
				if run.haiku != nil {
					t.Errorf("run has a haiku caller %T with no Haiku configured", run.haiku)
				}
				return
			}
			if got, ok := run.haiku.(*Haiku); !ok || got != tc.haiku {
				t.Errorf("run haiku caller is %v, want the configured Haiku", run.haiku)
			}
		})
	}
}

func TestStepsStartTripsWatchOnTicks(t *testing.T) {
	t.Parallel()
	dir := haikuDir(t)
	h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, dir, haikuFake{stdout: stepWireMonitorLine}),
		Environ: haikuEnviron()})
	f, id := stepWireStartOvertime(t, h)

	stepStartAwait(t, "the overtime end", func() bool {
		row, err := f.store.Step(t.Context(), id)
		return err == nil && row.End != ""
	})
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("read step: %v", err)
	}
	if row.End != "overtime" {
		t.Fatalf("step ended %q with reason %q, want overtime", row.End, row.Reason)
	}
	if len(row.Verdicts) != 1 {
		t.Fatalf("step has %d verdicts, want one monitor verdict", len(row.Verdicts))
	}
	got := row.Verdicts[0]
	if got.Kind != watchLoopKind || got.Trip != string(watchTripOvertime) || got.Verdict != watchVerdictProgressing ||
		got.Action != string(watchActEnd) {
		t.Errorf("verdict is %+v, want a monitor overtime verdict of progressing ending the step", got)
	}
	if runs := haikuRuns(t, dir); runs != 1 {
		t.Errorf("haiku claude ran %d times, want one monitor call", runs)
	}
}

func TestStepsStartStopsWatchTicksWhenStepEnds(t *testing.T) {
	t.Parallel()
	h := NewHaiku(HaikuConfig{Claude: haikuWriteFake(t, haikuDir(t), haikuFake{stdout: stepWireMonitorLine}),
		Environ: haikuEnviron()})
	f, id := stepWireStartOvertime(t, h)
	run := stepWireRun(t, f)

	stepStartAwait(t, "the step's loop to return", func() bool {
		f.steps.mu.Lock()
		defer f.steps.mu.Unlock()
		return len(f.steps.loops) == 0
	})
	row, err := f.store.Step(t.Context(), id)
	if err != nil {
		t.Fatalf("read step: %v", err)
	}
	if row.End != "overtime" {
		t.Fatalf("step ended %q, want overtime", row.End)
	}
	select {
	case <-run.ticks:
		t.Fatal("the watch ticked after its step's loop returned")
	case <-time.After(10 * stepWireWatchTick):
	}
}
