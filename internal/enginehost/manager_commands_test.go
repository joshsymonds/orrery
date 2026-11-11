package enginehost

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/engine/session"
	"github.com/joshsymonds/orrery/internal/store"
)

// unknownID is a session id no manager started.
const unknownID = "0123456789abcdef0123456789abcdef"

// requestIDOf returns the request id of the last engine_request event in history.
func requestIDOf(t *testing.T, history store.EngineHistory) string {
	t.Helper()
	id := ""
	for _, event := range history.Events {
		if event.Kind != store.EventEngineRequest {
			continue
		}
		var payload struct {
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode request payload %q: %v", event.Payload, err)
		}
		id = payload.RequestID
	}
	return id
}

// lastQuestion returns the question of the last engine_changed event that names one, and whether one does.
func lastQuestion(t *testing.T, history store.EngineHistory) (string, bool) {
	t.Helper()
	question, found := "", false
	for _, fields := range changedFields(t, history) {
		if value, ok := fields["question"]; ok {
			question, found = value, true
		}
	}
	return question, found
}

// workingWithoutQuestion reports whether history holds an engine_changed that sets state working and clears the
// question.
func workingWithoutQuestion(t *testing.T, history store.EngineHistory) bool {
	t.Helper()
	for _, fields := range changedFields(t, history) {
		if fields["state"] == "working" && fields["question"] == "" {
			return true
		}
	}
	return false
}

// turnEndsWithError reports whether history holds an engine_turn_ended whose turn ended with an error.
func turnEndsWithError(t *testing.T, history store.EngineHistory) bool {
	t.Helper()
	for _, event := range history.Events {
		if event.Kind != store.EventEngineTurnEnded {
			continue
		}
		var payload struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode turn ended payload %q: %v", event.Payload, err)
		}
		if payload.IsError {
			return true
		}
	}
	return false
}

// conversationOf returns the prompts and the text blocks of history in seq order, each tagged with its role.
func conversationOf(t *testing.T, history store.EngineHistory) []string {
	t.Helper()
	var lines []string
	for _, event := range history.Events {
		switch event.Kind {
		case store.EventEnginePrompt:
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
			}
			lines = append(lines, "prompt: "+payload.Text)
		case store.EventEngineBlock:
			var payload struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode block payload %q: %v", event.Payload, err)
			}
			if payload.Kind == string(session.BlockText) {
				lines = append(lines, "reply: "+payload.Text)
			}
		}
	}
	return lines
}

func TestSayStoresReplyToSecondPrompt(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	if err := m.Say(t.Context(), id, "again"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(t, path, id, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if !workingWithoutQuestion(t, history) {
		t.Fatalf("no engine_changed sets state working with no question in %+v", changedFields(t, history))
	}
}

func TestSayRefusesEmptyTextAndUnknownID(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	if err := m.Say(t.Context(), id, ""); !errors.Is(err, ErrEmptyPrompt) {
		t.Fatalf("Say empty text error = %v, want ErrEmptyPrompt", err)
	}
	if err := m.Say(t.Context(), unknownID, "hi"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Say of an unknown id error = %v, want ErrNoSession", err)
	}
	if got := conversationOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got,
		[]string{"prompt: hi", "reply: fake reply 1"}) {
		t.Fatalf("conversation after refused Say = %q, want the first prompt and reply only", got)
	}
}

func TestAskWaitsAndAnswerResumesTurn(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	if question, ok := lastQuestion(t, history); !ok || question != "Which color?" {
		t.Fatalf("question = %q (set %v), want %q", question, ok, "Which color?")
	}
	requestID := requestIDOf(t, history)

	if err := m.Answer(t.Context(), id, requestID, "green"); !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("Answer with a label that is not an option error = %v, want ErrBadAnswer", err)
	}
	if got := historyOf(t, path, id).State; got != "waiting" {
		t.Fatalf("state after refused label = %q, want waiting", got)
	}

	if err := m.Answer(t.Context(), id, requestID, "red"); err != nil {
		t.Fatalf("Answer red: %v", err)
	}
	history = waitHistory(t, path, id, "the answered reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1")
	})
	if !workingWithoutQuestion(t, history) {
		t.Fatalf("no engine_changed sets state working with no question in %+v", changedFields(t, history))
	}
	// The answer's working state is queued before anything the answer causes, so the turn's end is the last state.
	waitHistory(t, path, id, "the done state after the answered turn", func(h store.EngineHistory) bool {
		return h.State == "done"
	})
}

func TestAnswerRefusesUnknownRequest(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	if err := m.Answer(t.Context(), id, "no-such-request", "red"); !errors.Is(err, session.ErrNoPendingRequest) {
		t.Fatalf("Answer of an unknown request error = %v, want session.ErrNoPendingRequest", err)
	}
	if err := m.Answer(t.Context(), unknownID, "ask-1", "red"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Answer of an unknown id error = %v, want ErrNoSession", err)
	}
}

func TestPermitAllowAndDeny(t *testing.T) {
	m, path := newTestManager(t, 2, fakeClaudePath(t))
	allowID, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "PERMIT"})
	if err != nil {
		t.Fatalf("Start allow session: %v", err)
	}
	denyID, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "PERMIT"})
	if err != nil {
		t.Fatalf("Start deny session: %v", err)
	}

	allowHistory := waitHistory(t, path, allowID, "the allow session waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	if question, ok := lastQuestion(t, allowHistory); !ok || question != "Write" {
		t.Fatalf("permission question = %q (set %v), want %q", question, ok, "Write")
	}
	if err := m.Answer(t.Context(), allowID, requestIDOf(t, allowHistory), "maybe"); !errors.Is(err, ErrBadAnswer) {
		t.Fatalf("Answer maybe error = %v, want ErrBadAnswer", err)
	}
	if err := m.Answer(t.Context(), allowID, requestIDOf(t, allowHistory), "allow"); err != nil {
		t.Fatalf("Answer allow: %v", err)
	}
	waitHistory(t, path, allowID, "the allowed reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake allowed 1")
	})

	denyHistory := waitHistory(t, path, denyID, "the deny session waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	if err := m.Answer(t.Context(), denyID, requestIDOf(t, denyHistory), "deny"); err != nil {
		t.Fatalf("Answer deny: %v", err)
	}
	waitHistory(t, path, denyID, "the denied reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake denied 1")
	})
}

func TestInterruptEndsTurnWithError(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "HANG"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the hanging reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake hanging 1")
	})

	if err := m.Interrupt(t.Context(), id); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	waitHistory(t, path, id, "the turn ended with an error", func(h store.EngineHistory) bool {
		return turnEndsWithError(t, h)
	})
}

func TestInterruptRefusesUnknownID(t *testing.T) {
	m, _ := newTestManager(t, 1, fakeClaudePath(t))
	if err := m.Interrupt(t.Context(), unknownID); !errors.Is(err, ErrNoSession) {
		t.Fatalf("Interrupt of an unknown id error = %v, want ErrNoSession", err)
	}
}

func TestReplayRebuildsConversationInOrder(t *testing.T) {
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Say(t.Context(), id, "again"); err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(t, path, id, "the second reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})

	want := []string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2"}
	if got := conversationOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed conversation = %q, want %q", got, want)
	}
}
