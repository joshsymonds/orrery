package server_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/server"
	"github.com/joshsymonds/orrery/internal/store"
)

// stepSessionName is the id of the labeled step the watch tests stream. stepStatusUnlabeled is the id of a step the
// fake finds with no epic label, and stepStatusAbsent is the id of a step session the fake does not find.
const (
	stepSessionName     = "step-1"
	stepStatusUnlabeled = "step-2"
	stepStatusAbsent    = "step-3"
)

// stepStatusFake is a StepClient whose StepStatus finds stepSessionName with an epic label and stepStatusUnlabeled
// with none, answers statusErr for every id when it is set, and counts its calls. Any other id is not found. The
// embedded nil client is the rest of StepClient: the watch calls none of it, so a call to it panics.
type stepStatusFake struct {
	server.StepClient
	statusErr error

	mu          sync.Mutex
	statusCalls int
}

func (f *stepStatusFake) StepStatus(
	_ context.Context,
	req *connect.Request[enginev1.StepStatusRequest],
) (*connect.Response[enginev1.StepStatusResponse], error) {
	f.mu.Lock()
	f.statusCalls++
	f.mu.Unlock()
	if f.statusErr != nil {
		return nil, f.statusErr
	}
	switch req.Msg.GetId() {
	case stepSessionName:
		return connect.NewResponse(&enginev1.StepStatusResponse{
			Step: &enginev1.Step{Id: stepSessionName, Epic: "repo/epic"},
		}), nil
	case stepStatusUnlabeled:
		return connect.NewResponse(&enginev1.StepStatusResponse{
			Step: &enginev1.Step{Id: stepStatusUnlabeled},
		}), nil
	}
	return nil, connect.NewError(connect.CodeNotFound, errors.New("synthetic step not found"))
}

// calls returns how many StepStatus calls the fake has answered.
func (f *stepStatusFake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusCalls
}

// watchHandler returns the guarded handler that reads sessions from reader and asks steps about step sessions. A nil
// steps is the serve that has no step client.
func watchHandler(t *testing.T, reader *store.Reader, steps server.StepClient) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:                []string{allowedLogin},
		Tmux:                  newTmuxClient(t),
		Push:                  newPushStore(t),
		Sessions:              reader,
		Steps:                 steps,
		ConversationPoll:      convPoll,
		ConversationKeepalive: convKeepalive,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// addStepSession writes the step session id with a prompt and one reply, the same events an engine session of
// seedSessionReplies holds, so its first delta is the one an engine session sends.
func addStepSession(t *testing.T, s *store.Store, id string) {
	t.Helper()
	applyEngineOps(t, s,
		store.EngineAdd{ID: id, Dir: "/a", State: "idle", Kind: store.KindStep},
		store.EnginePrompt{ID: id, Text: "fix the bug"},
		store.EngineBlock{ID: id, Kind: "text", Role: "assistant", MessageID: "msg-1", Text: "reply 1"},
	)
}

// stepPromptItem is the conversation item of the prompt addStepSession and seedSessionReplies write first.
func stepPromptItem() *orreryv1.ConversationItem {
	return &orreryv1.ConversationItem{Kind: "prompt", Time: eventAt, Text: "fix the bug"}
}

// wantWatchError fails the test unless err is a Connect error with code and the fixed message.
func wantWatchError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("stream error: got %v, want a Connect error", err)
	}
	if connectErr.Code() != code {
		t.Fatalf("stream code: got %v (%v), want %v", connectErr.Code(), err, code)
	}
	if connectErr.Message() != message {
		t.Fatalf("stream message: got %q, want %q", connectErr.Message(), message)
	}
}

// TestSessionWatchStreamsLabeledStepFirstDelta opens a labeled step's stream at seq 0. Its first delta is the one
// an engine session sends: a reset with the reply, and the whole state of the step session.
func TestSessionWatchStreamsLabeledStepFirstDelta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepSession(t, s, stepSessionName)
	steps := &stepStatusFake{}
	client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), steps))

	stream := openSessionStream(t, client, sessionReq(stepSessionName, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: stepSessionName,
		Seq:        3,
		Reset_:     true,
		Items:      []*orreryv1.ConversationItem{stepPromptItem(), reply(1)},
		State: &orreryv1.ConversationState{
			Origin: "fix the bug",
			Session: &orreryv1.HeadlessSession{
				Id: stepSessionName, Dir: "/a", Label: "a · " + stepSessionName, State: "idle", Changed: eventAt,
			},
		},
	})
}

// TestSessionWatchStepWithoutLabelAnswersNotFound opens the stream of a step the StepStatus lookup finds unlabelled,
// one it does not find, and a step with no Steps client. Each ends at once with not_found and the session message.
func TestSessionWatchStepWithoutLabelAnswersNotFound(t *testing.T) {
	cases := []struct {
		name  string
		id    string
		steps server.StepClient
	}{
		{name: "unlabelled step", id: stepStatusUnlabeled, steps: &stepStatusFake{}},
		{name: "unknown step", id: stepStatusAbsent, steps: &stepStatusFake{}},
		{name: "nil step client", id: stepSessionName, steps: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "orrery.db")
			s := openEventStore(t, path)
			addStepSession(t, s, stepSessionName)
			addStepSession(t, s, stepStatusUnlabeled)
			addStepSession(t, s, stepStatusAbsent)
			client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), tc.steps))

			stream := openSessionStream(t, client, sessionReq(tc.id, 0, 0))
			wantWatchError(t, stream.end(t), connect.CodeNotFound, "session not found")
		})
	}
}

// TestSessionWatchStepStatusFailureAnswersUnavailable makes StepStatus fail with an error that is not not_found. The
// stream ends with unavailable and the step service message, and sends no delta.
func TestSessionWatchStepStatusFailureAnswersUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepSession(t, s, stepSessionName)
	steps := &stepStatusFake{statusErr: errors.New("synthetic status failure")}
	client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), steps))

	stream := openSessionStream(t, client, sessionReq(stepSessionName, 0, 0))
	wantWatchError(t, stream.end(t), connect.CodeUnavailable, "step service unavailable")
}

// TestSessionWatchAsksStepStatusOncePerStream streams a labeled step through a second delta. The second delta can
// only follow a poll after the first, so StepStatus would be asked again on every poll if the stream asked it each
// time.
func TestSessionWatchAsksStepStatusOncePerStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addStepSession(t, s, stepSessionName)
	steps := &stepStatusFake{}
	client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), steps))
	stream := openSessionStream(t, client, sessionReq(stepSessionName, 0, 0))
	stream.next(t)

	applyEngineOps(t, s, store.EngineBlock{
		ID: stepSessionName, Kind: "text", Role: "assistant", MessageID: "msg-2", Text: "reply 2",
	})
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: stepSessionName,
		Seq:        4,
		Items:      []*orreryv1.ConversationItem{reply(2)},
	})
	if got := steps.calls(); got != 1 {
		t.Fatalf("StepStatus calls: got %d, want 1", got)
	}
}

// TestSessionWatchEngineSessionNeverAsksStepStatus streams an engine session with a Steps client present. Its first
// delta is the engine delta, and StepStatus is never asked.
func TestSessionWatchEngineSessionNeverAsksStepStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openEventStore(t, path)
	addEngine(t, s)
	seedSessionReplies(t, s, 1)
	steps := &stepStatusFake{}
	client := sessionsWatchClient(t, watchHandler(t, store.NewReader(path), steps))

	stream := openSessionStream(t, client, sessionReq(eventEngine, 0, 0))
	wantStreamMessage(t, stream.next(t), &orreryv1.SessionsWatchConversationResponse{
		Transcript: eventEngine,
		Seq:        3,
		Reset_:     true,
		Items:      []*orreryv1.ConversationItem{stepPromptItem(), reply(1)},
		State: &orreryv1.ConversationState{
			Origin:  "fix the bug",
			Session: engineSession("idle"),
		},
	})
	if got := steps.calls(); got != 0 {
		t.Fatalf("StepStatus calls for an engine session: got %d, want 0", got)
	}
}
