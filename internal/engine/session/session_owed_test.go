package session

import (
	"strings"
	"testing"
)

// readoptOwing re-adopts a process that replays nothing, owing owed prompts to the previous controller, and returns the
// session with the process it runs on.
func readoptOwing(t *testing.T, owed int) (*Session, *readoptProcess) {
	t.Helper()
	process := newReadoptProcess()
	session, _ := readopt(t, process, ReadoptOptions{From: readoptFrom, Consumed: readoptFrom, Owed: owed})
	return session, process
}

// lastSentUUID returns the uuid of the user message written last to process, which must carry text.
func lastSentUUID(t *testing.T, process *readoptProcess, text string) string {
	t.Helper()
	writes := process.input.all()
	if len(writes) == 0 {
		t.Fatal("process received no writes, want a user message")
	}
	return requireUserEnvelope(t, strings.TrimSuffix(writes[len(writes)-1].data, "\n"), text)
}

func TestReadoptOwingPromptsCountsThemAsUnanswered(t *testing.T) {
	t.Parallel()
	session, _ := readoptOwing(t, 2)
	if got := session.UnansweredPrompts(); got != 2 {
		t.Errorf("UnansweredPrompts = %d after a re-adoption owing 2, want 2", got)
	}
}

func TestTurnListingTwoUnsentUUIDsClearsOwedPrompts(t *testing.T) {
	t.Parallel()
	session, process := readoptOwing(t, 2)
	deliverResult(t, session, process.fakeProcess, "uuid-from-old-holder-1", "uuid-from-old-holder-2")
	if got := session.UnansweredPrompts(); got != 0 {
		t.Errorf("UnansweredPrompts = %d after a turn answered both owed prompts, want 0", got)
	}
}

func TestTurnListingOneUnsentUUIDLeavesOneOwedPrompt(t *testing.T) {
	t.Parallel()
	session, process := readoptOwing(t, 2)
	deliverResult(t, session, process.fakeProcess, "uuid-from-old-holder-1")
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d after a turn answered one owed prompt, want 1", got)
	}
}

func TestTurnListingNoUUIDLeavesOwedPrompts(t *testing.T) {
	t.Parallel()
	session, process := readoptOwing(t, 2)
	deliverResult(t, session, process.fakeProcess)
	if got := session.UnansweredPrompts(); got != 2 {
		t.Errorf("UnansweredPrompts = %d after a turn answered nothing, want 2", got)
	}
}

func TestOwedPromptsKeepCountingTheSessionsOwnSends(t *testing.T) {
	t.Parallel()
	session, process := readoptOwing(t, 1)
	if err := session.Prompt("write-1", "own prompt"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if got := session.UnansweredPrompts(); got != 2 {
		t.Fatalf("UnansweredPrompts = %d after one send beside one owed prompt, want 2", got)
	}
	deliverResult(t, session, process.fakeProcess, lastSentUUID(t, process, "own prompt"))
	if got := session.UnansweredPrompts(); got != 1 {
		t.Errorf("UnansweredPrompts = %d after a turn answered the session's own send, want 1: "+
			"the owed prompt stays", got)
	}
	deliverResult(t, session, process.fakeProcess, "uuid-from-old-holder-1")
	if got := session.UnansweredPrompts(); got != 0 {
		t.Errorf("UnansweredPrompts = %d after a turn answered the owed prompt, want 0", got)
	}
}
