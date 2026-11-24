package planner

import (
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// designEventDesign is the design the design event tests answer. It is made in contractEventConversation, which has a
// sessions row, so its working conversation is that session.
const designEventDesign = "design-answer"

// TestDesignEventsRecordsTranscriptOwnerServeAnswerSentOnce checks that each line the owner made through serve is sent
// once to the design's working conversation, as a design caller event whose block names the design, what the owner did,
// its ids and its words. A line with no ids and no words sends a body with only its first line. A later pass sends
// nothing more.
func TestDesignEventsRecordsTranscriptOwnerServeAnswerSentOnce(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	addCallerEngine(t, s, contractEventConversation, store.KindEngine)
	if err := s.CreateDesign(ctx, store.Design{
		ID: designEventDesign, Title: "Design answer", Conversation: contractEventConversation,
		Created: time.Unix(contractEventAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign: %v", err)
	}
	for _, line := range []store.DesignLogLine{
		{
			Design: designEventDesign, At: contractEventAt + 200, Maker: store.LogMakerOwner, What: "answered",
			IDs: []string{"Q1"}, Words: "Keep the scout",
		},
		{Design: designEventDesign, At: contractEventAt + 210, Maker: store.LogMakerOwner, What: "closure skipped"},
	} {
		if err := s.AppendDesignLog(ctx, line); err != nil {
			t.Fatalf("AppendDesignLog %s: %v", line.What, err)
		}
	}

	deliver := &fakeCallerDeliver{}
	pass := callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t)))
	passEpics(ctx, t, pass)
	want := []enginehost.DeliverRequest{
		{
			ID: contractEventConversation, PromptID: "design:design-answer:1",
			Text: `<orrery-event kind="design" id="design:design-answer:1" design="design-answer">
Design design-answer: the owner answered, ids Q1.
words: Keep the scout
</orrery-event>`,
		},
		{
			ID: contractEventConversation, PromptID: "design:design-answer:2",
			Text: `<orrery-event kind="design" id="design:design-answer:2" design="design-answer">
Design design-answer: the owner closure skipped.
</orrery-event>`,
		},
	}
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered = %+v, want %+v", deliver.delivered, want)
	}

	passEpics(ctx, t, pass)
	if !reflect.DeepEqual(deliver.delivered, want) {
		t.Fatalf("delivered after a later pass = %+v, want %+v", deliver.delivered, want)
	}
}

// TestDesignEventsRecordsTranscriptOwnerAcceptNotDuplicated checks that the owner's accept of a contract is sent once,
// as the contract's event, and that the accepted line the owner made for it in the design's log is not sent again as a
// design event.
func TestDesignEventsRecordsTranscriptOwnerAcceptNotDuplicated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := openRunnerStore(t)
	addCallerEngine(t, s, contractEventConversation, store.KindEngine)
	proposeContractEvents(t, s)
	if err := s.AcceptContract(ctx, contractEventID, store.ContractChange{
		Session: "", Words: "Accept the synthetic contract", At: contractEventAt + 100,
	}); err != nil {
		t.Fatalf("AcceptContract by the owner: %v", err)
	}
	if err := s.AppendDesignLog(ctx, store.DesignLogLine{
		Design: contractEventID, At: contractEventAt + 100, Maker: store.LogMakerOwner, What: "accepted",
		IDs: []string{contractEventID}, Words: "Accept the synthetic contract",
	}); err != nil {
		t.Fatalf("AppendDesignLog accepted: %v", err)
	}

	deliver := &fakeCallerDeliver{}
	passEpics(ctx, t, callerEpicEventsOver(s, callerSenderOver(s, deliver.deliver, noWake(t))))
	accept := store.ContractStateEvent(contractEventID, 2)
	if len(deliver.delivered) != 1 || deliver.delivered[0].PromptID != accept {
		t.Fatalf("delivered = %+v, want the one contract event %s", deliver.delivered, accept)
	}
}
