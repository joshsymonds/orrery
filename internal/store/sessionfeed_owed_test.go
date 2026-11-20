package store

import (
	"context"
	"slices"
	"testing"
)

// sessionFeedOwedEngine adds the engine session id, so a step's caller, an epic's creator and an engine prompt name
// a sessions row.
func sessionFeedOwedEngine(t *testing.T, s *Store, id string) {
	t.Helper()
	add := EngineAdd{ID: id, Dir: "/work/engine", State: "idle"}
	if err := s.ApplyEngine(context.Background(), []EngineOp{add}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add engine %s: %v", id, err)
	}
}

// sessionFeedOwedWant fails the test unless the hook was called once since the last take, with seq 0 and sessions.
func sessionFeedOwedWant(t *testing.T, feed *sessionFeedRecorder, sessions ...string) {
	t.Helper()
	calls := feed.take()
	if len(calls) != 1 || calls[0].seq != 0 || !slices.Equal(calls[0].sessions, sessions) {
		t.Errorf("hook calls = %v, want one call with seq 0 and sessions %v", calls, sessions)
	}
}

// sessionFeedOwedQuiet fails the test when the hook was called since the last take.
func sessionFeedOwedQuiet(t *testing.T, feed *sessionFeedRecorder, what string) {
	t.Helper()
	if calls := feed.take(); len(calls) != 0 {
		t.Errorf("hook calls after %s = %v, want none", what, calls)
	}
}

// sessionFeedOwedQuestion completes the first turn of epic with one question, which raises it.
func sessionFeedOwedQuestion(ctx context.Context, t *testing.T, s *Store, epic string) {
	t.Helper()
	plan := EpicPlan{Outcome: "wait", Questions: []EpicQuestionInput{
		shapedQuestion("which-store", "Which store holds the plan?"),
	}}
	if err := s.CompleteTurn(ctx, epic, 1, plan, completedAt()); err != nil {
		t.Fatalf("CompleteTurn %s: %v", epic, err)
	}
}

// TestSessionFeedOwedStreamFeedNamesOwedSessions checks that each write of owed state names the session it changed,
// with no event appended, so each call has seq 0 and the one session.
func TestSessionFeedOwedStreamFeedNamesOwedSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	sessionFeedOwedEngine(t, s, "S")
	createEpic(t, s, fullEpic(claimEpic))
	if _, err := s.RecordEpicCall(ctx, claimEpic, EpicCaller{Session: "S"}, "epic_create", claimedAt()); err != nil {
		t.Fatalf("RecordEpicCall epic_create by S: %v", err)
	}
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn %s: %v", claimEpic, err)
	}
	add := StepAdd{ID: "owed-step", Agent: "implementer", Prompt: "do it", Caller: EpicCaller{Session: "S"}}
	if err := s.ApplyEngine(ctx, []EngineOp{add}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add step owed-step: %v", err)
	}
	feed := &sessionFeedRecorder{}
	s.OnSessionEvents(feed.record)

	if err := s.MarkCallerEvent(ctx, "step:owed-step", "S", engineNowAt()); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	sessionFeedOwedWant(t, feed, "S")

	if err := s.PutOffQuestion(ctx, "pane", "T", "question-1", engineNowAt()); err != nil {
		t.Fatalf("PutOffQuestion: %v", err)
	}
	sessionFeedOwedWant(t, feed, "T")
	if err := s.BringBackQuestion(ctx, "pane", "T", "question-1"); err != nil {
		t.Fatalf("BringBackQuestion: %v", err)
	}
	sessionFeedOwedWant(t, feed, "T")

	prompt := EnginePromptState{ID: "S", WriteID: "write-1", State: PromptQueued}
	if err := s.ApplyEngine(ctx, []EngineOp{prompt}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine prompt state: %v", err)
	}
	sessionFeedOwedWant(t, feed, "S")

	end := StepEnd{ID: "owed-step", End: "done", Status: "passed", Reason: "gate green"}
	if err := s.ApplyEngine(ctx, []EngineOp{end}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine end step owed-step: %v", err)
	}
	sessionFeedOwedWant(t, feed, "S")

	sessionFeedOwedQuestion(ctx, t, s, claimEpic)
	sessionFeedOwedWant(t, feed, "S")
}

// TestSessionFeedOwedStreamFeedSilentWithoutOwedChange checks that a write names no session when no owed state of one
// changed: a question on an epic with no creator, and the end of a step with no caller. A caller event after them
// shows the hook is live, so the silence is the store's and not a hook that never fires.
func TestSessionFeedOwedStreamFeedSilentWithoutOwedChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	createEpic(t, s, fullEpic(claimEpic))
	if _, err := s.ClaimTurn(ctx, claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn %s: %v", claimEpic, err)
	}
	add := StepAdd{ID: "bare-step", Agent: "implementer", Prompt: "do it"}
	if err := s.ApplyEngine(ctx, []EngineOp{add}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine add step bare-step: %v", err)
	}
	feed := &sessionFeedRecorder{}
	s.OnSessionEvents(feed.record)

	sessionFeedOwedQuestion(ctx, t, s, claimEpic)
	sessionFeedOwedQuiet(t, feed, "a question on an epic with no creator")

	end := StepEnd{ID: "bare-step", End: "done", Status: "passed", Reason: "gate green"}
	if err := s.ApplyEngine(ctx, []EngineOp{end}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine end step bare-step: %v", err)
	}
	sessionFeedOwedQuiet(t, feed, "the end of a step with no caller")

	if err := s.MarkCallerEvent(ctx, "event-1", "S", engineNowAt()); err != nil {
		t.Fatalf("MarkCallerEvent: %v", err)
	}
	sessionFeedOwedWant(t, feed, "S")
}
