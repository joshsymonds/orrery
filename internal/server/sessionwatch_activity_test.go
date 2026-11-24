package server_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	enginev1connect "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	orreryv1connect "github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// activityTimers is the timer function the activity test's server arms its timers with. The timers it arms never fire,
// so the test reads no clock but its own.
func activityTimers(time.Duration) (<-chan time.Time, func() bool) {
	return make(chan time.Time), func() bool { return true }
}

// activityFeed is the change feed of the activity test's server. Its one Changes.Watch call hands the test the call, and
// the call sends only the messages the test sends on it. A stream therefore wakes when the test says a change happened,
// and never on a write the test made, so each delta the test reads is the one its change asked for.
type activityFeed struct {
	calls chan *activityCall
}

// activityCall is the Changes.Watch call of an activityFeed. The test sends each change on msgs.
type activityCall struct {
	msgs chan *enginev1.ChangesWatchResponse
}

// Watch serves one call: it hands the call to the test, then sends each message the test sends on it until the call
// ends.
func (f *activityFeed) Watch(
	ctx context.Context,
	_ *connect.Request[enginev1.ChangesWatchRequest],
	stream *connect.ServerStream[enginev1.ChangesWatchResponse],
) error {
	call := &activityCall{msgs: make(chan *enginev1.ChangesWatchResponse)}
	select {
	case f.calls <- call:
	case <-ctx.Done():
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg := <-call.msgs:
			if err := stream.Send(msg); err != nil {
				return fmt.Errorf("send change: %w", err)
			}
		}
	}
}

// activityWatchClient serves the store at path through a server whose clock is clock and whose change feed is feed, and
// returns a Sessions client for it. Its timers never fire (see activityTimers).
func activityWatchClient(
	t *testing.T,
	path string,
	clock *bgClock,
	feed *activityFeed,
) orreryv1connect.SessionsClient {
	t.Helper()
	_, changesHandler := enginev1connect.NewChangesHandler(feed)
	changes := httptest.NewServer(changesHandler)
	t.Cleanup(changes.Close)
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  newTmuxClient(t),
		Push:                  newPushStore(t),
		Sessions:              store.NewReader(path),
		Changes:               enginev1connect.NewChangesClient(http.DefaultClient, changes.URL),
		NewTimer:              activityTimers,
		ConversationKeepalive: convKeepalive,
		Now:                   clock.Now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return sessionsWatchClient(t, handler)
}

// activityDesign creates the design id, worked by conversation, at the time eventAt.
func activityDesign(t *testing.T, s *store.Store, id, conversation string) {
	t.Helper()
	if err := s.CreateDesign(t.Context(), store.Design{
		ID: id, Title: id, Conversation: conversation, Created: time.Unix(eventAt, 0),
	}); err != nil {
		t.Fatalf("CreateDesign %s: %v", id, err)
	}
}

// addActivityStep starts step at the unix second at, and stores its design step row with the same start.
func addActivityStep(t *testing.T, s *store.Store, at int64, step store.DesignStep) {
	t.Helper()
	bgApplyAt(t, s, at, store.StepAdd{ID: step.Step, Agent: "researcher", Prompt: "work it"})
	step.Created = time.Unix(at, 0)
	if err := s.AddDesignStep(t.Context(), step); err != nil {
		t.Fatalf("AddDesignStep %s: %v", step.Step, err)
	}
}

// activityStepRow is the background row of the running design step id of design-activity, with the words it shows and
// the unix second it started at.
func activityStepRow(id, words string, started int64) *orreryv1.BackgroundTask {
	return &orreryv1.BackgroundTask{
		Id: id, Description: words, Type: "step", Status: "running", Started: started,
		Design: &orreryv1.EngineDesign{Design: "design-activity"},
	}
}

// seedActivity writes eventEngine with a running agent task that starts a second after the test's start, the design
// design-activity that eventEngine works with a research step, a discovery step and a reproduce step started in that
// order, and the design design-other that engine-other works with a research step of its own.
func seedActivity(t *testing.T, s *store.Store) {
	t.Helper()
	at := int64(eventAt)
	addEngine(t, s)
	bgApplyAt(t, s, at+1, bgAgentTask("running", ""))
	activityDesign(t, s, "design-activity", eventEngine)
	activityDesign(t, s, "design-other", "engine-other")
	addActivityStep(t, s, at, store.DesignStep{
		Step: "step-research", Design: "design-activity", Kind: store.StepResearch,
		Subject: "Does the cache hold entries?\nRead the store.",
	})
	addActivityStep(t, s, at+2, store.DesignStep{
		Step: "step-discovery", Design: "design-activity", Kind: store.StepDiscovery,
		Subject: "Find the gaps\nin the queue.",
	})
	addActivityStep(t, s, at+3, store.DesignStep{
		Step: "step-reproduce", Design: "design-activity", Kind: store.StepReproduce,
		Subject: "go test ./internal/cache\n-run TestCache",
	})
	addActivityStep(t, s, at, store.DesignStep{
		Step: "step-other", Design: "design-other", Kind: store.StepResearch, Subject: "Who reads the other design?",
	})
}

// TestSessionWatchRecordsActivityStepRowsServeThenLeave serves a session that works design-activity, with a running
// research, discovery and reproduce step and a running agent task. The first delta serves the three steps as rows with
// their design and words, oldest start first beside the agent row, and leaves out the step of design-other. Then the
// reproduce step ends and the agent's progress is written, and the change feed says the session changed. The next delta
// serves the other rows and no longer the ended step.
func TestSessionWatchRecordsActivityStepRowsServeThenLeave(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedActivity(t, s)
	at := int64(eventAt)
	clock := newBgClock(at + 5)
	feed := &activityFeed{calls: make(chan *activityCall, 1)}
	stream := openSessionStream(t, activityWatchClient(t, path, clock, feed), sessionReq(eventEngine, 0, 0))
	call := <-feed.calls

	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        2,
		Reset_:     true,
		State: &orreryv1.ConversationState{
			Session: engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{
				activityStepRow("step-research", "Does the cache hold entries?", at),
				bgAgentRow("running", "", at+1, 0),
				activityStepRow("step-discovery", "discovery · Find the gaps", at+2),
				activityStepRow("step-reproduce", "go test ./internal/cache", at+3),
			},
		},
	})

	clock.set(at + 6)
	bgApplyAt(t, s, at+6, store.StepEnd{ID: "step-reproduce", End: "done", Status: "done", Reason: "returned"})
	bgApplyAt(t, s, at+6, bgAgentTask("running", "Grep"))
	call.msgs <- &enginev1.ChangesWatchResponse{Seq: 3, Sessions: []string{eventEngine}}
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		State: &orreryv1.ConversationState{
			Session: engineSession("idle"),
			Background: []*orreryv1.BackgroundTask{
				activityStepRow("step-research", "Does the cache hold entries?", at),
				bgAgentRow("running", "Grep", at+1, 0),
				activityStepRow("step-discovery", "discovery · Find the gaps", at+2),
			},
		},
	})
}
