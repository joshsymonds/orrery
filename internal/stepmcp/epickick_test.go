package stepmcp

import (
	"testing"

	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
)

// TestEpicKickCallReachesTheKickWithItsIDAndText checks that epic_kick makes one KickEpic call carrying the id and the
// text it names, and returns the stored epic.
func TestEpicKickCallReachesTheKickWithItsIDAndText(t *testing.T) {
	t.Parallel()
	stored := storedEpicOf(fullContract())
	fake := &fakeEpics{kickReply: &v1.KickEpicResponse{Epic: stored}}
	p := newPeer(t, nil, fake)

	outcome := p.tool("epic_kick", `{"id":"`+testEpicID+`","text":"Try the sqlite plan first."}`)

	if outcome.isError {
		t.Fatalf("epic_kick returned an error: %s", outcome.text)
	}
	calls := fake.calls()
	if n := calls.total(); n != 1 {
		t.Fatalf("epic_kick made %d service calls, want 1", n)
	}
	if len(calls.kicks) != 1 {
		t.Fatalf("epic_kick made %d KickEpic calls, want 1", len(calls.kicks))
	}
	if got := calls.kicks[0]; got.GetId() != testEpicID || got.GetText() != "Try the sqlite plan first." {
		t.Fatalf("KickEpic request = %v, want the epic id and the text", got)
	}
	var got v1.StoredEpic
	unmarshalResult(t, outcome.text, &got)
	if !proto.Equal(&got, stored) {
		t.Fatalf("epic_kick result = %s, want the stored epic", outcome.text)
	}
}
