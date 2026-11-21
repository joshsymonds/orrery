package stepmcp

import (
	"testing"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestFeatureBaseEpicCreateWithoutBaseSendsNoBase checks that epic_create called with no base reaches CreateEpic with an
// empty base, and returns the acknowledgement of the epic CreateEpic stores.
func TestFeatureBaseEpicCreateWithoutBaseSendsNoBase(t *testing.T) {
	fake := &fakeEpics{createReply: &v1.CreateEpicResponse{Epic: storedEpicOf(fullContract())}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_create", `{"id":"`+testEpicID+`"}`)

	if outcome.isError {
		t.Fatalf("epic_create returned an error: %s", outcome.text)
	}
	calls := fake.calls().creates
	if len(calls) != 1 || calls[0].GetBase() != "" {
		t.Fatalf("CreateEpic requests = %v, want one with an empty base", calls)
	}
	wantText := "epic " + testEpicID + ": phase accepted\ncreated with 1 requirements and 2 decisions"
	if outcome.text != wantText {
		t.Fatalf("epic_create text = %q, want %q", outcome.text, wantText)
	}
}
