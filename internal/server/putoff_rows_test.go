package server_test

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"

	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// notNowQuestion is the question the pane tests put off. notNowChanged is the time the pane first asks it, and
// notNowChanged+1 is a later ask of the same words.
const (
	notNowQuestion = "synthetic not-now question?"
	notNowChanged  = 1794301000
)

// notNowHandler builds the guarded handler that admits allowedLogin, routes to tmux, and reads the store through
// reader. Its clock is eventAt, so the headless retention window covers the rows the tests write.
func notNowHandler(t *testing.T, tmux *tmuxctl.Client, reader *store.Reader) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  tmux,
		Push:                  newPushStore(t),
		Sessions:              reader,
		Now:                   func() time.Time { return time.Unix(eventAt, 0) },
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// askRequest appends the permission request requestID to eventEngine and sets the session waiting on it.
func askRequest(t *testing.T, s *store.Store, requestID string) {
	t.Helper()
	applyEngineOps(t, s,
		store.EngineRequest{
			ID: eventEngine, Kind: "permission", RequestID: requestID, ToolUseID: "toolu-" + requestID,
			ToolName: "Write", Subtype: "can_use_tool", Input: `{"path":"/x"}`,
		},
		store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{
			State: optional("waiting"), Question: optional("Write?"),
		}},
	)
}

// seedWaitingRequest writes the headless session eventEngine and has it wait on the permission request req-1.
func seedWaitingRequest(t *testing.T, s *store.Store) {
	t.Helper()
	applyEngineOps(t, s, store.EngineAdd{ID: eventEngine, Dir: "/a", State: "idle"})
	askRequest(t, s, "req-1")
}

// notNowPut marks the question of kind in session as put off at eventAt.
func notNowPut(t *testing.T, s *store.Store, kind, session, question string) {
	t.Helper()
	if err := s.PutOffQuestion(t.Context(), kind, session, question, time.Unix(eventAt, 0)); err != nil {
		t.Fatalf("PutOffQuestion: %v", err)
	}
}

// notNowBringBack clears the put-off mark on the question of kind in session.
func notNowBringBack(t *testing.T, s *store.Store, kind, session, question string) {
	t.Helper()
	if err := s.BringBackQuestion(t.Context(), kind, session, question); err != nil {
		t.Fatalf("BringBackQuestion: %v", err)
	}
}

// listedPutOff lists the headless sessions through client and returns the put_off of eventEngine, which must be the
// only session listed.
func listedPutOff(t *testing.T, client orreryv1connect.SessionsClient) bool {
	t.Helper()
	got, err := client.ListHeadless(t.Context(), connect.NewRequest(&orreryv1.ListHeadlessRequest{}))
	if err != nil {
		t.Fatalf("ListHeadless: %v", err)
	}
	sessions := got.Msg.GetSessions()
	if len(sessions) != 1 || sessions[0].GetId() != eventEngine {
		t.Fatalf("ListHeadless: got %v, want only session %s", sessions, eventEngine)
	}
	return sessions[0].GetPutOff()
}

// listedPanePutOff lists the panes through client and returns the put_off of the pane with id pane.
func listedPanePutOff(t *testing.T, client orreryv1connect.PanesClient, pane string) bool {
	t.Helper()
	got, err := client.List(t.Context(), connect.NewRequest(&orreryv1.PanesListRequest{}))
	if err != nil {
		t.Fatalf("Panes.List: %v", err)
	}
	for _, listed := range got.Msg.GetPanes() {
		if listed.GetId() == pane {
			return listed.GetPutOff()
		}
	}
	t.Fatalf("Panes.List: pane %s is not listed", pane)
	return false
}

// setPaneWaiting sets pane to wait on notNowQuestion, asked at changed.
func setPaneWaiting(t *testing.T, tmux *tmuxctl.Client, pane string, changed int64) {
	t.Helper()
	options := map[string]string{
		protocol.OptionState:    string(protocol.StateWaiting),
		protocol.OptionQuestion: notNowQuestion,
		protocol.OptionChanged:  strconv.FormatInt(changed, 10),
	}
	for name, value := range options {
		if err := tmux.SetPaneOption(t.Context(), pane, name, value); err != nil {
			t.Fatalf("SetPaneOption(%s, %s): %v", pane, name, err)
		}
	}
}

// paneNotNowKey is the question key of notNowQuestion asked at notNowChanged, which a pane mark names.
func paneNotNowKey() string {
	return store.PaneQuestionKey(protocol.QuestionHash(notNowQuestion), notNowChanged)
}

// waitingRequestState is the state of eventEngine waiting on the open request req-1, with put_off set to putOff on the
// session and on the request.
func waitingRequestState(putOff bool) *orreryv1.ConversationState {
	session := engineSession("waiting")
	session.Question = "Write?"
	session.PutOff = putOff
	return &orreryv1.ConversationState{
		Session: session,
		Request: &orreryv1.OpenRequest{
			Id: "req-1", Kind: "permission", Question: "Write", Options: []string{"allow", "deny"}, PutOff: putOff,
		},
	}
}

// TestNotNowHeadlessRowReadsPutOffUntilBringBack marks the open request of a waiting headless session. Its row reads
// put_off until the mark is brought back.
func TestNotNowHeadlessRowReadsPutOffUntilBringBack(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedWaitingRequest(t, s)
	notNowPut(t, s, store.PutOffHeadless, eventEngine, "req-1")
	client := sessionsWatchClient(t, notNowHandler(t, newTmuxClient(t), store.NewReader(path)))

	if !listedPutOff(t, client) {
		t.Fatalf("ListHeadless: the marked request reads put_off false, want true")
	}
	notNowBringBack(t, s, store.PutOffHeadless, eventEngine, "req-1")
	if listedPutOff(t, client) {
		t.Fatalf("ListHeadless: the brought-back request reads put_off true, want false")
	}
}

// TestNotNowNewHeadlessRequestIsNotPutOff marks the request req-1 of a waiting headless session, then has the session
// ask req-2. The row reads put_off while req-1 is open, and not once req-2, a new question, is open.
func TestNotNowNewHeadlessRequestIsNotPutOff(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedWaitingRequest(t, s)
	notNowPut(t, s, store.PutOffHeadless, eventEngine, "req-1")
	client := sessionsWatchClient(t, notNowHandler(t, newTmuxClient(t), store.NewReader(path)))
	if !listedPutOff(t, client) {
		t.Fatalf("ListHeadless: the marked request reads put_off false, want true before req-2 asks")
	}

	askRequest(t, s, "req-2")
	if listedPutOff(t, client) {
		t.Fatalf("ListHeadless: the new request req-2 reads put_off true, want false")
	}
}

// TestNotNowConversationRequestReadsPutOff watches the conversation of a waiting headless session whose open request is
// marked. The state carries put_off on the session and on the request. After the mark is brought back, a later delta
// carries both false.
func TestNotNowConversationRequestReadsPutOff(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedWaitingRequest(t, s)
	notNowPut(t, s, store.PutOffHeadless, eventEngine, "req-1")
	client := sessionsWatchClient(t, notNowHandler(t, newTmuxClient(t), store.NewReader(path)))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		State:      waitingRequestState(true),
	})

	notNowBringBack(t, s, store.PutOffHeadless, eventEngine, "req-1")
	wantStreamMessage(t, stream.nextDelta(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		State:      waitingRequestState(false),
	})
}

// TestNotNowPaneRowReadsPutOffUntilBringBack marks the question of a waiting pane. Its row reads put_off until the mark
// is brought back.
func TestNotNowPaneRowReadsPutOffUntilBringBack(t *testing.T) {
	t.Parallel()
	tmux := newTmuxServer(t)
	startSession(t, tmux, "waiter", longCommand)
	pane := paneID(t, tmux, "waiter")
	setPaneWaiting(t, tmux, pane, notNowChanged)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	notNowPut(t, s, store.PutOffPane, pane, paneNotNowKey())
	client := panesClient(t, notNowHandler(t, tmux, store.NewReader(path)))

	if !listedPanePutOff(t, client, pane) {
		t.Fatalf("Panes.List: the marked question reads put_off false, want true")
	}
	notNowBringBack(t, s, store.PutOffPane, pane, paneNotNowKey())
	if listedPanePutOff(t, client, pane) {
		t.Fatalf("Panes.List: the brought-back question reads put_off true, want false")
	}
}

// TestNotNowPaneAskingAgainIsNotPutOff marks the question of a waiting pane, then has the pane wait again on the same
// words at a later changed time. The new ask reads put_off false.
func TestNotNowPaneAskingAgainIsNotPutOff(t *testing.T) {
	t.Parallel()
	tmux := newTmuxServer(t)
	startSession(t, tmux, "waiter", longCommand)
	pane := paneID(t, tmux, "waiter")
	setPaneWaiting(t, tmux, pane, notNowChanged)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	notNowPut(t, s, store.PutOffPane, pane, paneNotNowKey())
	client := panesClient(t, notNowHandler(t, tmux, store.NewReader(path)))
	if !listedPanePutOff(t, client, pane) {
		t.Fatalf("Panes.List: the marked question reads put_off false, want true before the pane asks again")
	}

	setPaneWaiting(t, tmux, pane, notNowChanged+1)
	if listedPanePutOff(t, client, pane) {
		t.Fatalf("Panes.List: the pane asking again reads put_off true, want false")
	}
}

// TestNotNowMarkOnClosedQuestionIsInert marks the open question of a headless session and of a pane. Once the session
// and the pane stop waiting, both rows read put_off false, though the marks are still stored.
func TestNotNowMarkOnClosedQuestionIsInert(t *testing.T) {
	t.Parallel()
	tmux := newTmuxServer(t)
	startSession(t, tmux, "waiter", longCommand)
	pane := paneID(t, tmux, "waiter")
	setPaneWaiting(t, tmux, pane, notNowChanged)
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	seedWaitingRequest(t, s)
	notNowPut(t, s, store.PutOffHeadless, eventEngine, "req-1")
	notNowPut(t, s, store.PutOffPane, pane, paneNotNowKey())
	handler := notNowHandler(t, tmux, store.NewReader(path))
	sessions := sessionsWatchClient(t, handler)
	panes := panesClient(t, handler)
	if !listedPutOff(t, sessions) || !listedPanePutOff(t, panes, pane) {
		t.Fatalf("the marked questions read put_off false, want true while they are open")
	}

	applyEngineOps(t, s, store.EngineSet{ID: eventEngine, Changes: store.EngineChanges{State: optional("idle")}})
	if err := tmux.SetPaneOption(t.Context(), pane, protocol.OptionState, string(protocol.StateWorking)); err != nil {
		t.Fatalf("SetPaneOption(%s, %s): %v", pane, protocol.OptionState, err)
	}
	if listedPutOff(t, sessions) {
		t.Fatalf("ListHeadless: the session no longer waiting reads put_off true, want false")
	}
	if listedPanePutOff(t, panes, pane) {
		t.Fatalf("Panes.List: the pane no longer waiting reads put_off true, want false")
	}
}
