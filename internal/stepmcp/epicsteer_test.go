package stepmcp

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestSteerMoveSendsOneCallWithEmptySessionAndAcknowledges checks that epic_steer makes one MoveEpic call that names the
// epic and an empty session, which the daemon reads as the calling conversation, and answers the acknowledgement.
func TestSteerMoveSendsOneCallWithEmptySessionAndAcknowledges(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf(fullContract())
	fake := &fakeEpics{moveReply: &v1.MoveEpicResponse{Epic: stored}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_steer", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_steer returned an error: %s", outcome.text)
	}
	moves := fake.calls().moves
	if n := len(moves); n != 1 {
		t.Fatalf("epic_steer made %d MoveEpic calls, want 1", n)
	}
	if got := moves[0].GetId(); got != testEpicID {
		t.Fatalf("MoveEpic id = %q, want %q", got, testEpicID)
	}
	if got := moves[0].GetSession(); got != "" {
		t.Fatalf("MoveEpic session = %q, want empty for the calling conversation", got)
	}
	want := "epic " + testEpicID + ": phase accepted\nthis conversation now steers the epic"
	if outcome.text != want {
		t.Fatalf("epic_steer text = %q, want %q", outcome.text, want)
	}
}

// TestSteerMoveRefusalIsAToolFailureCarryingItsText checks that a refused MoveEpic returns the tool failure, with the
// refusal's text, after the one call.
func TestSteerMoveRefusalIsAToolFailureCarryingItsText(t *testing.T) {
	t.Parallel()
	refusal := "epic " + testEpicID + " is steered by another conversation"
	fake := &fakeEpics{err: connect.NewError(connect.CodeFailedPrecondition, errors.New(refusal))}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_steer", `{"id":"`+testEpicID+`"}`)

	if !outcome.isError {
		t.Fatalf("epic_steer returned %q without isError", outcome.text)
	}
	if want := "failed_precondition: " + refusal; outcome.text != want {
		t.Fatalf("epic_steer error text = %q, want %q", outcome.text, want)
	}
	if n := fake.calls().total(); n != 1 {
		t.Fatalf("epic_steer made %d service calls, want 1", n)
	}
}

// TestSteerMoveRefusesASessionKeyBeforeAnyCall checks that epic_steer takes the epic id alone: a session key is refused
// as an unknown field, so no conversation can name another session to move the epic to.
func TestSteerMoveRefusesASessionKeyBeforeAnyCall(t *testing.T) {
	t.Parallel()
	fake := &fakeEpics{}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_steer", `{"id":"`+testEpicID+`","session":"other"}`)

	if !outcome.isError {
		t.Fatalf("epic_steer returned %q without isError", outcome.text)
	}
	if n := fake.calls().total(); n != 0 {
		t.Fatalf("epic_steer with a session key reached the service %d times, want none", n)
	}
}

// TestSteerMoveFixtureListsEpicSteerWithTheIDSchema checks that the tools list fixture, which
// TestToolsListMatchesTheBaseFixture holds the live tools/list to, names epic_steer with an input schema of the id alone.
func TestSteerMoveFixtureListsEpicSteerWithTheIDSchema(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(toolsListFixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fixture []listedTool
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	var schema map[string]any
	for _, tool := range fixture {
		if tool.Name == "epic_steer" {
			schema = tool.InputSchema
		}
	}
	if schema == nil {
		t.Fatalf("fixture %s does not name epic_steer", toolsListFixture)
	}
	properties, _ := schema["properties"].(map[string]any)
	if _, ok := properties["id"]; !ok || len(properties) != 1 {
		t.Fatalf("epic_steer properties = %v, want only id", properties)
	}
	required, _ := schema["required"].([]any)
	if !slices.Equal(required, []any{"id"}) {
		t.Fatalf("epic_steer required = %v, want [id]", required)
	}
}
