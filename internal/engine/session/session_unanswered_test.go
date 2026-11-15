package session

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

// sendTwoPrompts sends a first and a second prompt and returns the uuids they were written under, read from stdin.
func sendTwoPrompts(t *testing.T, session *Session, process *fakeProcess) (first, second string) {
	t.Helper()
	if err := session.Prompt("write-1", "first prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := session.Prompt("write-2", "second prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	lines := stdinLines(process)
	if len(lines) != 3 {
		t.Fatalf("stdin has %d lines, want initialize and two prompts: %q", len(lines), lines)
	}
	return requireUserEnvelope(t, lines[1], "first prompt"), requireUserEnvelope(t, lines[2], "second prompt")
}

// deliverResult emits a successful turn's result line, listing answered as the user messages it answered, and waits
// for the session to deliver it. It fails the test unless the line is delivered as a TurnEnded that lists answered.
func deliverResult(t *testing.T, session *Session, process *fakeProcess, answered ...string) {
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
	if !slices.Equal(ended.Answered, answered) {
		t.Fatalf("TurnEnded answered = %q, want %q", ended.Answered, answered)
	}
}

func TestUnansweredPromptsCountsEachSentPrompt(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	sendTwoPrompts(t, session, process)
	if got := session.UnansweredPrompts(); got != 2 {
		t.Errorf("UnansweredPrompts = %d after two prompts, want 2", got)
	}
}

func TestTurnEndAnsweringFirstPromptLeavesSecondUnanswered(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	first, _ := sendTwoPrompts(t, session, process)
	deliverResult(t, session, process, first)
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d after a turn answered the first prompt, want 1", got)
	}
}

func TestTurnEndAnsweringBothPromptsLeavesNoneUnanswered(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	first, second := sendTwoPrompts(t, session, process)
	deliverResult(t, session, process, first, second)
	if got := session.UnansweredPrompts(); got != 0 {
		t.Errorf("UnansweredPrompts = %d after one turn answered both prompts, want 0", got)
	}
}

func TestTurnEndAnsweringOtherUUIDsLeavesPromptsUnanswered(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	sendTwoPrompts(t, session, process)
	deliverResult(t, session, process, "uuid-not-sent-by-this-session")
	if got := session.UnansweredPrompts(); got != 2 {
		t.Errorf("UnansweredPrompts = %d after a turn answered no sent prompt, want 2", got)
	}
}

func TestPromptThatFailsToSendIsNotCounted(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	if err := session.Prompt("write-1", "first prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if err := process.stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	if err := session.Prompt("write-2", "second prompt"); err == nil {
		t.Fatal("Prompt after stdin closed returned nil, want an error")
	}
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d after a prompt failed to send, want 1", got)
	}
}

// TestUnansweredPromptsAnswerBeforeSendIsNotCounted covers a turn that answers a prompt before the session records
// the send: the reader can see the result only after the envelope is written, and the send is recorded after the write
// returns. Such a prompt is not counted once it is recorded, and other sends still are.
func TestUnansweredPromptsAnswerBeforeSendIsNotCounted(t *testing.T) {
	t.Parallel()
	unanswered := newUnansweredPrompts()
	unanswered.answer([]string{"early"})
	unanswered.sent("early")
	unanswered.sent("later")
	if got := unanswered.count(); got != 1 {
		t.Errorf("count = %d, want 1: a prompt answered before its send was recorded is not counted", got)
	}
}

// TestUnansweredPromptsReadableFromAnotherGoroutine reads the count once from a goroutine that nothing orders against
// the session's reader, which records a turn's end meanwhile. The race detector fails the test if the count is read
// without the lock the reader holds.
func TestUnansweredPromptsReadableFromAnotherGoroutine(t *testing.T) {
	t.Parallel()
	session, process := startSession(t)
	first, _ := sendTwoPrompts(t, session, process)
	read := make(chan int, 1)
	go func() { read <- session.UnansweredPrompts() }()
	deliverResult(t, session, process, first)
	if got := <-read; got != 1 && got != 2 {
		t.Errorf("UnansweredPrompts read beside the turn's end = %d, want 2 before it or 1 after it", got)
	}
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d, want 1", got)
	}
}
