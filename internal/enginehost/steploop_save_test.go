package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/engine/session"
)

// loopSettled returns once the loop has taken the return the test handed it and has handled it, and once the writer has
// stored every op queued before the call. The loop takes a return from its channel before it handles it, and takes an
// owner action only when its select comes round again, so the owner action's round trip follows the handling.
func (f *stepLoopFixture) loopSettled(t *testing.T) {
	t.Helper()
	stepStartAwait(t, "the loop to take the return", func() bool { return len(f.run.returns) == 0 })
	if err := f.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("loop round trip: %v", err)
	}
	f.settle(t)
}

func TestStepLoopSaveRetryStoresRoundAndContinues(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	f.loopSettled(t)

	if got := len(f.sessions.continuedTexts()); got != 1 {
		t.Fatalf("Continue called %d times, want 1 for the failing return", got)
	}
	row := f.row(t)
	if row.LoopRound != 1 || row.LoopContinues != 1 {
		t.Fatalf("loop round %d continues %d, want 1 and 1 after a failing return is sent back",
			row.LoopRound, row.LoopContinues)
	}
}

func TestStepLoopSaveFailedContinueStoresNoRaisedCount(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, errors.New("session refused"))
	_, done := f.start(t)

	f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want failed", row.End)
	}
	if row.LoopRound != 1 || row.LoopContinues != 0 {
		t.Fatalf("loop round %d continues %d, want 1 and 0: the round's return stores the count before the retry, "+
			"and a raised count is stored only once its Continue is sent", row.LoopRound, row.LoopContinues)
	}
}

func TestStepLoopSaveReturnCommitStoresRoundAndContinuesAtChecksFailed(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	_, done := f.start(t)

	for range 4 {
		f.run.Return(session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":1}`)})
	}
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndChecksFailed {
		t.Fatalf("end = %q, want checks_failed", row.End)
	}
	if row.LoopRound != 4 || row.LoopContinues != 3 {
		t.Fatalf("loop round %d continues %d, want 4 and 3 at the fourth return that ends checks_failed",
			row.LoopRound, row.LoopContinues)
	}
}

func TestStepLoopSaveLandedHoldStoresHeld(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	f.run.Event(session.Started{})
	f.start(t)

	if err := f.run.do(t.Context(), f.run.hold); err != nil {
		t.Fatalf("hold: %v", err)
	}
	f.settle(t)

	if row := f.row(t); !row.LoopHeld {
		t.Fatal("loop held = false after a landed hold, want true")
	}
}

func TestStepLoopSaveOwnerNudgeStoredThenClearedAtTurnEnd(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	f.run.Event(session.Started{})
	f.start(t)

	const text = "use the parser API instead"
	if err := f.run.do(t.Context(), func(ctx context.Context) error {
		return f.run.nudgeOwner(ctx, text)
	}); err != nil {
		t.Fatalf("owner nudge of a running turn: %v", err)
	}
	f.settle(t)
	if row := f.row(t); row.LoopOwnerNudge != text {
		t.Fatalf("loop owner nudge = %q after a landed owner nudge, want %q", row.LoopOwnerNudge, text)
	}

	ended := session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)}
	f.run.Event(ended)
	f.run.Return(ended)
	f.loopSettled(t)

	if got := f.sessions.continuedTexts(); len(got) != 1 || got[0] != text {
		t.Fatalf("Continue texts = %q, want the owner text once", got)
	}
	if row := f.row(t); row.LoopOwnerNudge != "" {
		t.Fatalf("loop owner nudge = %q after the turn end, want it cleared", row.LoopOwnerNudge)
	}
}

func TestStepLoopSaveHeldOwnerNudgeSentClearsHeld(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	f.run.Event(session.Started{})
	f.start(t)

	if err := f.run.do(t.Context(), f.run.hold); err != nil {
		t.Fatalf("hold: %v", err)
	}
	f.settle(t)
	if row := f.row(t); !row.LoopHeld {
		t.Fatal("loop held = false after a landed hold, want true")
	}

	ended := session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)}
	f.run.Event(ended)
	f.run.Return(ended)
	f.loopSettled(t)

	const text = "resume with the parser API"
	if err := f.run.do(t.Context(), func(ctx context.Context) error {
		return f.run.nudgeOwner(ctx, text)
	}); err != nil {
		t.Fatalf("owner nudge of a held step whose turn ended: %v", err)
	}
	f.settle(t)

	if got := f.sessions.continuedTexts(); len(got) != 1 || got[0] != text {
		t.Fatalf("Continue texts = %q, want the owner text once", got)
	}
	if row := f.row(t); row.LoopHeld {
		t.Fatal("loop held = true after the owner nudge was sent, want false")
	}
}

func TestStepLoopSaveWatchdogNudgeStoresNudgeAndNudged(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictLooping, watchLoopDoing)}}
	f := newWatchLoopFixture(t, haiku, &stepLoopSessions{}, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	f.waitVerdicts(t, 1)

	row := f.row(t)
	watchLoopWantContains(t, row.LoopNudge, "watchdog interrupted", watchLoopDoing)
	if !row.LoopNudged {
		t.Fatal("loop nudged = false after a landed watchdog nudge, want true")
	}
}

func TestStepLoopSaveJudgeSendBackStoresJudgeSent(t *testing.T) {
	t.Parallel()
	haiku := &judgeLoopHaiku{replies: []judgeLoopReply{judgeLoopVerdict("unmet", "the report is missing its summary")}}
	f := newJudgeLoopFixture(t, haiku, judgeLoopNoConfig)
	f.start(t)

	f.run.Return(judgeLoopDone("first"))
	f.loopSettled(t)

	if got := len(f.sessions.continuedTexts()); got != 1 {
		t.Fatalf("Continue called %d times, want 1 for the judge's send-back", got)
	}
	if row := f.row(t); !row.LoopJudgeSent {
		t.Fatal("loop judge sent = false after the judge sent the step back, want true")
	}
}

func TestStepLoopSaveFailedPutIsLoggedAndLoopGoesOn(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, nil)
	var logs bytes.Buffer
	f.run.logger = slog.New(slog.NewTextHandler(&logs, nil))
	stopped := NewWriter(WriterConfig{Applier: f.store, Tick: 5 * time.Millisecond})
	stop := startRun(t, stopped)
	stop()
	f.run.writer = stopped
	f.run.Event(session.Started{})
	f.start(t)

	if err := f.run.do(t.Context(), f.run.hold); err != nil {
		t.Fatalf("hold with a stopped writer: %v", err)
	}
	if !f.run.held.Load() {
		t.Fatal("run is not held after a landed hold")
	}
	if err := f.run.do(t.Context(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("loop did not go on after a failed put: %v", err)
	}
	if !strings.Contains(logs.String(), "save loop state of step") {
		t.Fatalf("logs %q do not record the failed put", logs.String())
	}
}

// TestStepLoopSaveTurnEndClearsOwnerNudgeBeforeSend checks that a turn end stores the clear of the pending owner text
// before the text is sent, so a failed send leaves no pending text stored.
func TestStepLoopSaveTurnEndClearsOwnerNudgeBeforeSend(t *testing.T) {
	t.Parallel()
	f := newStepLoopFixture(t, errors.New("session refused"))
	f.run.Event(session.Started{})
	_, done := f.start(t)

	const text = "use the parser API instead"
	if err := f.run.do(t.Context(), func(ctx context.Context) error {
		return f.run.nudgeOwner(ctx, text)
	}); err != nil {
		t.Fatalf("owner nudge of a running turn: %v", err)
	}
	f.settle(t)
	if row := f.row(t); row.LoopOwnerNudge != text {
		t.Fatalf("loop owner nudge = %q after a landed owner nudge, want %q", row.LoopOwnerNudge, text)
	}

	ended := session.TurnEnded{StructuredOutput: json.RawMessage(`{"status":"DONE"}`)}
	f.run.Event(ended)
	f.run.Return(ended)
	waitRunEnd(t, done)

	row := f.row(t)
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want failed by the refused send", row.End)
	}
	if row.LoopOwnerNudge != "" {
		t.Fatalf("loop owner nudge = %q after the turn end, want it cleared before the send", row.LoopOwnerNudge)
	}
}

// TestStepLoopSaveTurnEndClearsWatchdogNudgeBeforeSend checks that a turn end stores the clear of the pending watchdog
// text before the text is sent, so a failed send leaves no pending text stored.
func TestStepLoopSaveTurnEndClearsWatchdogNudgeBeforeSend(t *testing.T) {
	t.Parallel()
	haiku := &watchLoopHaiku{replies: []watchLoopReply{watchLoopVerdict(watchVerdictLooping, watchLoopDoing)}}
	sessions := &stepLoopSessions{continueErr: errors.New("session refused")}
	f := newWatchLoopFixture(t, haiku, sessions, watchLoopModelLimit)
	f.event(0, session.Started{})

	f.tick(t, 5*time.Minute)
	f.waitVerdicts(t, 1)
	if row := f.row(t); row.LoopNudge == "" {
		t.Fatal("loop nudge empty after a landed watchdog nudge, want it set")
	}

	f.turnEnd(5*time.Minute, session.TurnEnded{IsError: true, Subtype: "error_during_execution"})
	row := f.waitEnd(t)
	if row.End != stepLoopEndFailed {
		t.Fatalf("end = %q, want failed by the refused send", row.End)
	}
	if row.LoopNudge != "" {
		t.Fatalf("loop nudge = %q after the turn end, want it cleared before the send", row.LoopNudge)
	}
}
