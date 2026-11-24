package server_test

import (
	"path/filepath"
	"testing"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// TestEventsWatchRecordsTranscriptPassesOverDesignLines checks that Events.Watch, which has no typed message for an
// engine_design event, passes over the design line the owner answered through serve and still sends the engine events
// on either side of it, with the design line's seq skipped.
func TestEventsWatchRecordsTranscriptPassesOverDesignLines(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	activityDesign(t, s, "design-log", eventEngine)
	if err := s.AppendDesignLog(ctx, store.DesignLogLine{
		Design: "design-log", At: eventAt + 1, Maker: store.LogMakerOwner, What: "answered", Words: "Keep the scout",
	}); err != nil {
		t.Fatalf("AppendDesignLog owner answer: %v", err)
	}
	promptEngine(t, s, 1)
	events := orreryv1connect.NewEventsClient(guardedHTTPClient(), startServer(t, path, nil))

	at := int64(eventAt)
	w := openWatch(t, events, 0)
	wantMessage(t, w.next(t), &orreryv1.WatchResponse{
		Seq: 1, SessionId: eventEngine, At: at,
		Event: &orreryv1.WatchResponse_EngineAdded{EngineAdded: &orreryv1.EngineAdded{Dir: "/a", State: "idle"}},
	})
	wantMessage(t, w.next(t), &orreryv1.WatchResponse{
		Seq: 3, SessionId: eventEngine, At: at,
		Event: &orreryv1.WatchResponse_EnginePrompt{EnginePrompt: &orreryv1.EnginePrompt{Text: "hi"}},
	})
}

// TestSessionWatchRecordsTranscriptDesignItemsAndPending serves a session that works design-log, whose owner answered
// through serve and whose conversation then asked a question. The first message carries both lines as design items, each
// with the design's id and its text: what was done, then the words on a new line. The owner's answer is also a pending
// event of the session that carries its design, while the conversation's own question owes the session nothing.
func TestSessionWatchRecordsTranscriptDesignItemsAndPending(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	activityDesign(t, s, "design-log", eventEngine)
	at := int64(eventAt)
	if err := s.AppendDesignLog(ctx, store.DesignLogLine{
		Design: "design-log", At: at + 1, Maker: store.LogMakerOwner, What: "answered", IDs: []string{"Q1"},
		Words: "Keep the scout",
	}); err != nil {
		t.Fatalf("AppendDesignLog owner answer: %v", err)
	}
	if err := s.AppendDesignLog(ctx, store.DesignLogLine{
		Design: "design-log", At: at + 2, Maker: store.LogMakerConversation, Session: eventEngine, What: "asked",
		IDs: []string{"Q2"}, Words: "Should the scout stay?",
	}); err != nil {
		t.Fatalf("AppendDesignLog conversation question: %v", err)
	}

	clock := newBgClock(at + 5)
	stream := openSessionStream(t, bgWatchClient(t, path, s, clock, activityTimers), sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		Oldest:     2,
		Items: []*orreryv1.ConversationItem{
			{
				Kind: "design", Time: at + 1, Text: "answered\nKeep the scout",
				Design: &orreryv1.EngineDesign{Design: "design-log", What: "answered", Ids: []string{"Q1"}},
			},
			{
				Kind: "design", Time: at + 2, Text: "asked\nShould the scout stay?",
				Design: &orreryv1.EngineDesign{Design: "design-log", What: "asked", Ids: []string{"Q2"}},
			},
		},
		State: &orreryv1.ConversationState{
			Session: engineSession("idle"),
			Pending: []*orreryv1.PendingEvent{{
				Id: "design:design-log:1", Kind: "design", Waits: "free",
				Design: &orreryv1.EngineDesign{
					Design: "design-log", What: "answered", Ids: []string{"Q1"}, Words: "Keep the scout",
					Maker: &orreryv1.RecordMaker{Kind: store.LogMakerOwner},
				},
			}},
		},
	})
}
