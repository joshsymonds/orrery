package enginehost

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/gates"
	"github.com/joshsymonds/orrery/internal/store"
)

// verdictNoRoundReason is the gate_failed reason a return that holds every assertion ends with when an earlier return,
// one that was no round, held fails on verdictTestMust.
const verdictNoRoundReason = "an earlier return held fails: " + verdictTestMust

// verdictNoRoundFails returns a schema-valid verdict return whose first assertion fails, with its finding.
func verdictNoRoundFails(t *testing.T) json.RawMessage {
	t.Helper()
	raw := verdictTestJSON(t, "done",
		[]verdictTestEntry{
			verdictTestResult(verdictTestMust, gates.KindMust, "fails"),
			verdictTestResult(verdictTestMustNot, gates.KindMustNot, "holds"),
		},
		[]verdictTestFinding{verdictTestCiting(verdictTestMust)})
	if err := validateReturn(gates.VerdictSchema, raw); err != nil {
		t.Fatalf("fails return does not pass the verdict schema: %v", err)
	}
	return raw
}

// verdictNoRoundSchemaInvalid returns verdictNoRoundFails without test_runner_requests, which the verdict schema
// requires, so the return fails the schema while it still carries a fails result.
func verdictNoRoundSchemaInvalid(t *testing.T) json.RawMessage {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(verdictNoRoundFails(t), &doc); err != nil {
		t.Fatalf("read fails return: %v", err)
	}
	delete(doc, "test_runner_requests")
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal return: %v", err)
	}
	if validateReturn(gates.VerdictSchema, raw) == nil {
		t.Fatalf("return %s passes the verdict schema, want it to fail", raw)
	}
	return raw
}

// verdictNoRoundHolds returns a schema-valid verdict return that holds every assertion.
func verdictNoRoundHolds(t *testing.T) json.RawMessage {
	t.Helper()
	return verdictTestJSON(t, "done", verdictTestHolds(), nil)
}

// verdictNoRoundNudge returns an action that sends the owner's nudge to run.
func verdictNoRoundNudge(run *stepRun) func(context.Context) error {
	return func(ctx context.Context) error { return run.nudgeOwner(ctx, "carry on") }
}

// verdictNoRoundSettle returns once the loop of run has taken the turn end handed to it and has handled it. The loop
// takes returns in its select and handles each one before it selects again, so a round trip made after the returns
// channel is empty is handled after the turn end.
func verdictNoRoundSettle(t *testing.T, run *stepRun) {
	t.Helper()
	stepStartAwait(t, "the loop to take the turn end", func() bool {
		return len(run.returns) == 0
	})
	if err := run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("settle round trip: %v", err)
	}
}

// verdictNoRoundWantRoundOne fails the test unless row stores exactly the checks of one round 1 return, the holds
// return that ends the step: its schema check and its assertions check, both passing. A turn end that is no round
// stores no check, so none of the checks of a no-round turn end appear.
func verdictNoRoundWantRoundOne(t *testing.T, row store.StepRow) {
	t.Helper()
	wantChecks := []store.StepCheck{
		{Round: 1, Name: stepLoopSchemaName, OK: true},
		{Round: 1, Name: stepLoopAssertionsName, OK: true},
	}
	if !reflect.DeepEqual(row.Checks, wantChecks) {
		t.Errorf("checks = %+v, want %+v", row.Checks, wantChecks)
	}
}

// newVerdictWatchFixture returns a started agent gate step fixture whose clock and ticks the test drives, with haiku as
// its monitor. Its gate's limit is an hour, so the periodic check is due before the gate runs overtime.
func newVerdictWatchFixture(t *testing.T, haiku haikuCaller) *watchLoopFixture {
	t.Helper()
	clock := &watchLoopClock{at: watchEpoch()}
	ticks := make(chan time.Time)
	gate := stepAgentGate("watch-reviewer")
	gate.Limit = time.Hour
	base := newStepDoneFixture(t, func(cfg *stepRunConfig) {
		cfg.schema = gates.VerdictSchema
		cfg.doneValue = stepDefaultDone
		cfg.agentGate = &gate
		cfg.haiku = haiku
		cfg.now = clock.now
		cfg.modelLimit = watchLoopModelLimit
		cfg.ticks = ticks
	})
	f := &watchLoopFixture{
		run:      base.run,
		store:    base.store,
		sessions: base.sessions,
		clock:    clock,
		ticks:    ticks,
		done:     make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		defer close(f.done)
		f.run.run(ctx)
	}()
	return f
}

func TestVerdictNoRoundFailsLatchesBeforeOwnerNudge(t *testing.T) {
	t.Parallel()
	f := newVerdictFixture(t, nil)
	_, done := f.start(t)
	f.run.Event(session.Started{})
	if err := f.run.do(t.Context(), verdictNoRoundNudge(f.run)); err != nil {
		t.Fatalf("owner nudge: %v", err)
	}

	f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundFails(t)})
	f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundHolds(t)})
	waitRunEnd(t, done)

	if got := f.sessions.continuedTexts(); !reflect.DeepEqual(got, []string{"carry on"}) {
		t.Fatalf("continues = %q, want the owner's nudge only", got)
	}
	row := f.row(t)
	if row.End != stepLoopEndGateFailed || row.Reason != verdictNoRoundReason {
		t.Fatalf("end = %q reason %q, want gate_failed with reason %q", row.End, row.Reason, verdictNoRoundReason)
	}
	verdictNoRoundWantRoundOne(t, row)
}

func TestVerdictNoRoundFailsLatchesAroundHold(t *testing.T) {
	cases := []struct {
		name string
		// nudgeFirst sends the owner's nudge while the turn is held and before it ends, so the held turn end carries
		// it. Otherwise the held turn end comes first, and the nudge resumes the step after it.
		nudgeFirst bool
	}{
		{name: "held turn end, then owner nudge"},
		{name: "owner nudge pending at held turn end", nudgeFirst: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t, nil)
			_, done := f.start(t)
			f.run.Event(session.Started{})
			if err := f.run.do(t.Context(), f.run.hold); err != nil {
				t.Fatalf("hold: %v", err)
			}

			if tc.nudgeFirst {
				if err := f.run.do(t.Context(), verdictNoRoundNudge(f.run)); err != nil {
					t.Fatalf("owner nudge: %v", err)
				}
				f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundFails(t)})
			} else {
				f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundFails(t)})
				verdictNoRoundSettle(t, f.run)
				if err := f.run.do(t.Context(), verdictNoRoundNudge(f.run)); err != nil {
					t.Fatalf("owner nudge: %v", err)
				}
			}
			f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundHolds(t)})
			waitRunEnd(t, done)

			if got := f.sessions.continuedTexts(); !reflect.DeepEqual(got, []string{"carry on"}) {
				t.Fatalf("continues = %q, want the owner's nudge only", got)
			}
			row := f.row(t)
			if row.End != stepLoopEndGateFailed || row.Reason != verdictNoRoundReason {
				t.Fatalf("end = %q reason %q, want gate_failed with reason %q",
					row.End, row.Reason, verdictNoRoundReason)
			}
			verdictNoRoundWantRoundOne(t, row)
		})
	}
}

func TestVerdictNoRoundFailsLatchesAtWatchdogNudge(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictLooping, watchLoopDoing)}}
	f := newVerdictWatchFixture(t, haiku)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	f.waitVerdicts(t, 1)
	f.turnEnd(5*time.Minute, session.TurnEnded{IsError: true, StructuredOutput: verdictNoRoundFails(t)})
	watchLoopWantContains(t, f.waitContinue(t), "periodic", watchLoopDoing)

	f.event(5*time.Minute, session.Started{})
	f.turnEnd(6*time.Minute, session.TurnEnded{StructuredOutput: verdictNoRoundHolds(t)})
	row := f.waitEnd(t)
	if row.End != stepLoopEndGateFailed || row.Reason != verdictNoRoundReason {
		t.Fatalf("end = %q reason %q, want gate_failed with reason %q", row.End, row.Reason, verdictNoRoundReason)
	}
	verdictNoRoundWantRoundOne(t, row)
}

func TestVerdictNoRoundSchemaInvalidLatchesNothing(t *testing.T) {
	cases := []struct {
		name string
		// hold holds the turn before its end, which the owner's nudge then releases.
		hold bool
	}{
		{name: "owner nudge"},
		{name: "held turn end, then owner nudge", hold: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerdictFixture(t, nil)
			_, done := f.start(t)
			f.run.Event(session.Started{})

			if tc.hold {
				if err := f.run.do(t.Context(), f.run.hold); err != nil {
					t.Fatalf("hold: %v", err)
				}
				f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundSchemaInvalid(t)})
				verdictNoRoundSettle(t, f.run)
				if err := f.run.do(t.Context(), verdictNoRoundNudge(f.run)); err != nil {
					t.Fatalf("owner nudge: %v", err)
				}
			} else {
				if err := f.run.do(t.Context(), verdictNoRoundNudge(f.run)); err != nil {
					t.Fatalf("owner nudge: %v", err)
				}
				f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundSchemaInvalid(t)})
			}
			f.run.Return(session.TurnEnded{StructuredOutput: verdictNoRoundHolds(t)})
			waitRunEnd(t, done)

			if got := f.sessions.continuedTexts(); !reflect.DeepEqual(got, []string{"carry on"}) {
				t.Fatalf("continues = %q, want the owner's nudge only", got)
			}
			row := f.row(t)
			if row.End != stepLoopEndDone || row.Reason != "" {
				t.Fatalf("end = %q reason %q, want done with no reason", row.End, row.Reason)
			}
			verdictNoRoundWantRoundOne(t, row)
		})
	}
}
