package stepmcp

import (
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestEpicEscalate_CallReachesTheServiceWithIDQuestionAndReason checks that epic_escalate makes one EscalateEpic call
// carrying the id, the question and the reason it names, and returns the acknowledgement that names the question and
// the reason.
func TestEpicEscalate_CallReachesTheServiceWithIDQuestionAndReason(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf(fullContract())
	fake := &fakeEpics{getReply: &v1.GetEpicResponse{Epic: stored}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_escalate",
		`{"id":"`+testEpicID+`","question":"Q2","reason":"Only the billing lead knows the rate."}`)

	if outcome.isError {
		t.Fatalf("epic_escalate returned an error: %s", outcome.text)
	}
	calls := fake.calls()
	if len(calls.escalations) != 1 {
		t.Fatalf("epic_escalate made %d EscalateEpic calls, want 1", len(calls.escalations))
	}
	got := calls.escalations[0]
	if got.GetId() != testEpicID || got.GetQuestion() != "Q2" ||
		got.GetReason() != "Only the billing lead knows the rate." {
		t.Fatalf("EscalateEpic request = %v, want the epic id, Q2 and the reason", got)
	}
	want := "epic " + testEpicID + ": phase accepted\n" +
		"question Q2 escalated to the owner: Only the billing lead knows the rate."
	if outcome.text != want {
		t.Fatalf("epic_escalate text = %q, want %q", outcome.text, want)
	}
}

// TestEpicEscalate_RefusalIsAToolError checks that a refusal of the service, here a step of the epic refused with a
// failed precondition, comes back as an error result carrying the service's text.
func TestEpicEscalate_RefusalIsAToolError(t *testing.T) {
	t.Parallel()
	refusal := "a step of the epic may not escalate a question"
	fake := &fakeEpics{err: connect.NewError(connect.CodeFailedPrecondition, errors.New(refusal))}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_escalate",
		`{"id":"`+testEpicID+`","question":"Q2","reason":"Only the billing lead knows the rate."}`)

	if !outcome.isError {
		t.Fatalf("epic_escalate returned %q without isError, want the refusal as an error", outcome.text)
	}
	if !strings.Contains(outcome.text, refusal) {
		t.Fatalf("epic_escalate error text = %q, want it to carry %q", outcome.text, refusal)
	}
}

// TestEpicEscalate_UnknownKeyRefusedBeforeAnyCall checks that an argument key epic_escalate does not take is refused as
// an error that names the key, and that no service call is made.
func TestEpicEscalate_UnknownKeyRefusedBeforeAnyCall(t *testing.T) {
	t.Parallel()
	fake := &fakeEpics{}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_escalate",
		`{"id":"`+testEpicID+`","question":"Q2","reason":"Only the billing lead knows the rate.","bogus":1}`)

	if !outcome.isError {
		t.Fatalf("epic_escalate returned %q without isError", outcome.text)
	}
	if !strings.Contains(outcome.text, "unknown field") || !strings.Contains(outcome.text, "bogus") {
		t.Fatalf("epic_escalate error text = %q, want it to name the unknown field", outcome.text)
	}
	if n := fake.calls().total(); n != 0 {
		t.Fatalf("epic_escalate reached the service %d times, want none", n)
	}
}

// TestEpicEscalate_IsMutating checks that epic_escalate is a mutating tool, whose cancel leaves its daemon call
// running.
func TestEpicEscalate_IsMutating(t *testing.T) {
	t.Parallel()
	if !mutatingTool("epic_escalate") {
		t.Fatal("epic_escalate is not a mutating tool")
	}
}
