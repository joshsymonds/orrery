package session

import (
	"encoding/json"
	"fmt"
	"testing"
)

// lifecycleLine returns the command_lifecycle line that reports state for the prompt written under uuid.
func lifecycleLine(t *testing.T, uuid string, state LifecycleState) []byte {
	t.Helper()
	listed, err := json.Marshal(uuid)
	if err != nil {
		t.Fatalf("marshal uuid: %v", err)
	}
	return fmt.Appendf(nil,
		`{"type":"command_lifecycle","command_uuid":%s,"state":%q,"session_id":"session_1"}`, listed, state)
}

// deliverLifecycle emits the command_lifecycle line for uuid and waits for the session to deliver it as the Lifecycle
// it translates to. It fails the test unless that is the only event delivered.
func deliverLifecycle(t *testing.T, session *Session, process *fakeProcess, uuid string, state LifecycleState) {
	t.Helper()
	emit(t, process, lifecycleLine(t, uuid, state))
	requireEvents(t, readEvents(t, session, 1), []Event{Lifecycle{CommandUUID: uuid, State: state}})
}

// deliverTurnEnd emits a successful turn's result line listing answered and returns the TurnEnded the session delivers.
func deliverTurnEnd(t *testing.T, session *Session, process *fakeProcess, answered ...string) TurnEnded {
	t.Helper()
	listed, err := json.Marshal(answered)
	if err != nil {
		t.Fatalf("marshal answered uuids: %v", err)
	}
	emit(t, process, fmt.Appendf(nil,
		`{"type":"result","subtype":"success","is_error":false,"result":"all done",`+
			`"terminal_reason":"completed","user_message_uuids":%s,"session_id":"session_1"}`,
		listed,
	))
	event := nextEvent(t, session)
	ended, ok := event.(TurnEnded)
	if !ok {
		t.Fatalf("event = %#v, want TurnEnded", event)
	}
	return ended
}

func TestQueueLifecycleTranslatesEachState(t *testing.T) {
	t.Parallel()
	for _, state := range []LifecycleState{
		LifecycleQueued, LifecycleStarted, LifecycleCompleted, LifecycleCancelled, LifecycleDiscarded,
	} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			got := translateLines(t, newTranslator(), string(lifecycleLine(t, "uuid_1", state)))
			requireEvents(t, got, []Event{Lifecycle{CommandUUID: "uuid_1", State: state}})
		})
	}
}

func TestQueueLifecycleReplayedLineYieldsTheSameEvent(t *testing.T) {
	t.Parallel()
	process := newReadoptProcess()
	line := lifecycleLine(t, "uuid_1", LifecycleStarted)
	emit(t, process.fakeProcess, line)
	consumed := int64(readoptFrom + len(line) + 1)

	_, lines := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: consumed})
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want the one replayed line: %#v", len(lines), lines)
	}
	requireEvents(t, lines[0].Events, []Event{Lifecycle{CommandUUID: "uuid_1", State: LifecycleStarted}})
}

func TestQueueLifecycleCancelledOrDiscardedPromptIsAnsweredByTurnEnd(t *testing.T) {
	t.Parallel()
	for _, state := range []LifecycleState{LifecycleCancelled, LifecycleDiscarded} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			session, process := startSession(t)
			first, second := sendTwoPrompts(t, session, process)
			deliverLifecycle(t, session, process, first, state)
			ended := deliverTurnEnd(t, session, process, second)
			if ended.Unanswered != 0 {
				t.Errorf(
					"TurnEnded.Unanswered = %d after a turn answered the second prompt and the first was %s, want 0",
					ended.Unanswered,
					state,
				)
			}
			if got := session.UnansweredPrompts(); got != 0 {
				t.Errorf("UnansweredPrompts = %d after the turn end, want 0", got)
			}
		})
	}
}

func TestQueueLifecycleCompletedPromptIsAnsweredOnlyByTurnEnd(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := session.Prompt("write-1", "queued prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	deliverLifecycle(t, session, process, "write-1", LifecycleCompleted)
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d after the prompt completed, want 1: only a turn end answers it", got)
	}
	ended := deliverTurnEnd(t, session, process, "write-1")
	if ended.Unanswered != 0 {
		t.Errorf("TurnEnded.Unanswered = %d after a turn answered the completed prompt, want 0", ended.Unanswered)
	}
	if got := session.UnansweredPrompts(); got != 0 {
		t.Errorf("UnansweredPrompts = %d after the turn end, want 0", got)
	}
}
