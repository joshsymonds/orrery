package server_test

import (
	"errors"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
)

// TestControlNotNowForwardsHeadlessAndPane puts a headless question and a pane question off through Control. The engine
// receives each request with its kind, id, request_id, question_hash and changed unchanged.
func TestControlNotNowForwardsHeadlessAndPane(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	headless := &orreryv1.NotNowRequest{Kind: "headless", Id: synthSessionID, RequestId: "synthetic-request"}
	pane := &orreryv1.NotNowRequest{Kind: "pane", Id: "%3", QuestionHash: "synthetic-hash", Changed: 1700000000}
	for _, req := range []*orreryv1.NotNowRequest{headless, pane} {
		if _, err := control.NotNow(t.Context(), connect.NewRequest(req)); err != nil {
			t.Fatalf("NotNow %v: %v", req, err)
		}
	}
	wantHeadless := &enginev1.NotNowRequest{Kind: "headless", Id: synthSessionID, RequestId: "synthetic-request"}
	wantPane := &enginev1.NotNowRequest{Kind: "pane", Id: "%3", QuestionHash: "synthetic-hash", Changed: 1700000000}
	calls := fake.recorded()
	if len(calls) != 2 || !proto.Equal(calls[0], wantHeadless) || !proto.Equal(calls[1], wantPane) {
		t.Fatalf("NotNow forwarded %v, want %v then %v", calls, wantHeadless, wantPane)
	}
}

// TestControlNotNowBringBackForwardsHeadlessAndPane brings a headless question and a pane question back through
// Control. The engine receives each request with its kind, id, request_id, question_hash and changed unchanged.
func TestControlNotNowBringBackForwardsHeadlessAndPane(t *testing.T) {
	t.Parallel()
	fake := &fakeEngine{}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	headless := &orreryv1.BringBackRequest{Kind: "headless", Id: synthSessionID, RequestId: "synthetic-request"}
	pane := &orreryv1.BringBackRequest{Kind: "pane", Id: "%3", QuestionHash: "synthetic-hash", Changed: 1700000000}
	for _, req := range []*orreryv1.BringBackRequest{headless, pane} {
		if _, err := control.BringBack(t.Context(), connect.NewRequest(req)); err != nil {
			t.Fatalf("BringBack %v: %v", req, err)
		}
	}
	wantHeadless := &enginev1.BringBackRequest{Kind: "headless", Id: synthSessionID, RequestId: "synthetic-request"}
	wantPane := &enginev1.BringBackRequest{Kind: "pane", Id: "%3", QuestionHash: "synthetic-hash", Changed: 1700000000}
	calls := fake.recorded()
	if len(calls) != 2 || !proto.Equal(calls[0], wantHeadless) || !proto.Equal(calls[1], wantPane) {
		t.Fatalf("BringBack forwarded %v, want %v then %v", calls, wantHeadless, wantPane)
	}
}

// TestControlNotNowRefusalPassesThrough makes the engine refuse a question that is not open. Control answers the caller
// with the same code and the engine's message.
func TestControlNotNowRefusalPassesThrough(t *testing.T) {
	t.Parallel()
	refusal := connect.NewError(connect.CodeFailedPrecondition, errors.New("synthetic question not open"))
	fake := &fakeEngine{err: refusal}
	control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

	_, err := control.NotNow(t.Context(), connect.NewRequest(&orreryv1.NotNowRequest{
		Kind: "pane", Id: "%3", QuestionHash: "synthetic-hash", Changed: 1700000000,
	}))
	assertConnectError(t, err, connect.CodeFailedPrecondition, "synthetic question not open")
}
