package enginehost

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// gateLimitTestLimit is the limit of the agent gate these tests run.
const gateLimitTestLimit = 300 * time.Second

// gateLimitTestReason is the reason the agent gate of these tests ends overtime with.
const gateLimitTestReason = "agent gate ran past its limit of 300 seconds"

// newGateLimitTestFixture returns a started watch loop fixture whose step is an agent gate with gate, or an ordinary
// step when gate is nil. Its run calls caller for monitor verdicts (nil for none) and drives sessions.
func newGateLimitTestFixture(t *testing.T, caller haikuCaller, sessions *stepLoopSessions,
	gate *gates.Gate) *watchLoopFixture {
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
		logger: slog.New(slog.DiscardHandler), now: f.clock.now, modelLimit: watchLoopModelLimit, haiku: caller,
		ticks: f.ticks, agentGate: gate,
	})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		defer close(f.done)
		f.run.run(ctx)
	}()
	return f
}

// gateLimitTestGate returns an agent gate with the test limit.
func gateLimitTestGate() *gates.Gate {
	return &gates.Gate{Name: "reviewer", Agent: "reviewer", Limit: gateLimitTestLimit}
}

// gateLimitTestWantRunning asserts the run has not ended. The run serves a call only while it runs, after it has
// handled every tick handed to it before.
func gateLimitTestWantRunning(t *testing.T, f *watchLoopFixture) {
	t.Helper()
	if err := f.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("run ended: %v", err)
	}
}

// gateLimitTestWantOvertime asserts the step ended overtime at the agent gate's limit with its reason, no status and
// no monitor verdict.
func gateLimitTestWantOvertime(t *testing.T, row store.StepRow) {
	t.Helper()
	if row.End != string(watchEndOvertime) {
		t.Fatalf("end = %q, want %q", row.End, watchEndOvertime)
	}
	if row.Reason != gateLimitTestReason {
		t.Fatalf("reason = %q, want %q", row.Reason, gateLimitTestReason)
	}
	if row.Status != "" {
		t.Fatalf("status = %q, want none", row.Status)
	}
	if len(row.Verdicts) != 0 {
		t.Fatalf("verdicts = %+v, want none: the limit makes no monitor call", row.Verdicts)
	}
}

func TestGateLimitEndsAgentGateOvertimeAtItsLimit(t *testing.T) {
	haiku := &watchLoopHaiku{}
	f := newGateLimitTestFixture(t, haiku, &stepLoopSessions{}, gateLimitTestGate())
	f.event(0, session.Started{})

	f.tick(t, gateLimitTestLimit-time.Second)
	gateLimitTestWantRunning(t, f)

	f.tick(t, gateLimitTestLimit)
	gateLimitTestWantOvertime(t, f.waitEnd(t))
	if got := haiku.recorded(); len(got) != 0 {
		t.Fatalf("monitor calls = %d, want none", len(got))
	}
}

func TestGateLimitEndsAgentGateOvertimeWithNoTurnRunning(t *testing.T) {
	haiku := &watchLoopHaiku{}
	f := newGateLimitTestFixture(t, haiku, &stepLoopSessions{}, gateLimitTestGate())

	f.tick(t, gateLimitTestLimit)
	gateLimitTestWantOvertime(t, f.waitEnd(t))
	if got := haiku.recorded(); len(got) != 0 {
		t.Fatalf("monitor calls = %d, want none", len(got))
	}
}

func TestGateLimitHeldAgentGateWaitsForRelease(t *testing.T) {
	haiku := &watchLoopHaiku{}
	sessions := &stepLoopSessions{}
	f := newGateLimitTestFixture(t, haiku, sessions, gateLimitTestGate())
	f.event(0, session.Started{})
	if err := f.run.do(t.Context(), f.run.hold); err != nil {
		t.Fatalf("hold: %v", err)
	}

	f.tick(t, gateLimitTestLimit+time.Second)
	gateLimitTestWantRunning(t, f)

	// The release is an owner nudge to the held run. The held turn's end then continues the step with it.
	if err := f.run.do(t.Context(), func(ctx context.Context) error {
		return f.run.nudgeOwner(ctx, "carry on")
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	f.turnEnd(gateLimitTestLimit+2*time.Second, session.TurnEnded{Subtype: "success"})
	if text := f.waitContinue(t); text != "carry on" {
		t.Fatalf("release continue = %q, want %q", text, "carry on")
	}

	f.tick(t, gateLimitTestLimit+3*time.Second)
	gateLimitTestWantOvertime(t, f.waitEnd(t))
	if got := haiku.recorded(); len(got) != 0 {
		t.Fatalf("monitor calls = %d, want none", len(got))
	}
}

func TestGateLimitOrdinaryStepPastLimitIsNotEnded(t *testing.T) {
	haiku := &watchLoopHaiku{}
	f := newGateLimitTestFixture(t, haiku, &stepLoopSessions{}, nil)
	f.event(gateLimitTestLimit-2*time.Second, session.Started{})

	f.tick(t, gateLimitTestLimit+time.Second)
	gateLimitTestWantRunning(t, f)
	if got := haiku.recorded(); len(got) != 0 {
		t.Fatalf("monitor calls = %d, want none before any trip", len(got))
	}
}
