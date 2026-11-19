package enginehost

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// denialOf reports whether the fake claude in dir has read from its stdin a deny response to the request requestID.
func denialOf(t *testing.T, dir, requestID string) bool {
	t.Helper()
	denied := false
	for _, line := range stdinLines(t, dir) {
		var reply struct {
			Type     string `json:"type"`
			Response struct {
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior string `json:"behavior"`
				} `json:"response"`
			} `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("stdin line %q is not JSON: %v", line, err)
		}
		if reply.Type == "control_response" && reply.Response.RequestID == requestID &&
			reply.Response.Response.Behavior == "deny" {
			denied = true
		}
	}
	return denied
}

// TestDeclineHeadlessDeniesAndInterrupts declines the open question of a session that asks one (ASK). The fake reads a
// deny response to that request, then the owner's interrupt, which cancels the queue. The decline stores an
// engine_interrupt that says a turn runs, since the question is open in its turn.
func TestDeclineHeadlessDeniesAndInterrupts(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	requestID := requestIDOf(t, history)

	if _, err = m.Decline(t.Context(), id, requestID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if !denialOf(t, dir, requestID) {
		t.Fatalf("the fake read no deny response to request %s", requestID)
	}
	interrupts := interruptEnvelopesOf(t, dir)
	if len(interrupts) != 1 || !interrupts[0].Request.CancelQueued {
		t.Fatalf("interrupt requests = %+v, want one that cancels the queue", interrupts)
	}
	if got := interruptMarks(t, historyOf(t, path, id)); !slices.Equal(got, []bool{true}) {
		t.Fatalf("engine_interrupt turning = %v, want [true]: the question's turn runs", got)
	}
}

// TestDeclineHeadlessQuietUntilSay declines the open question of a session that asks one (ASK). A caller event that is
// pending afterwards starts no turn: Deliver returns ErrCallerQuiet, and the step-end pass sends nothing. The owner's
// next Say runs as a turn, and once it ends the pass delivers the pending event.
func TestDeclineHeadlessQuietUntilSay(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	s := storeOf(t, m)
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	if _, err = m.Decline(t.Context(), id, requestIDOf(t, history)); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the declined turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake answered 1") && h.State == string(protocol.StateDone)
	})
	addEndedCallerStep(t, s, store.EpicCaller{Session: id}, "the work is in")

	block := wantStepEndBlock("the work is in")
	if err = m.Deliver(t.Context(), DeliverRequest{ID: id, Text: block, PromptID: callerStepEvent}); !errors.Is(
		err, ErrCallerQuiet,
	) {
		t.Fatalf("Deliver after the decline error = %v, want ErrCallerQuiet", err)
	}
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	if got := enginePromptsOf(t, historyOf(t, path, id)); !reflect.DeepEqual(got, []deliveredPrompt{{Text: "ASK"}}) {
		t.Fatalf("prompts after the decline = %+v, want only the start's prompt", got)
	}
	if eventMarked(t, s, id) {
		t.Fatal("step end marked while the decline keeps its caller quiet")
	}

	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "owner words", PromptID: "p-owner"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the owner's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && turnEndCount(h) == 2 && h.State == string(protocol.StateDone)
	})
	stepEndPass(newStepEndSender(t, m, s), s).Pass(t.Context())
	history = waitHistory(t, engineOf(m), path, id, "the event's turn's end", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3") && turnEndCount(h) == 3 && h.State == string(protocol.StateDone)
	})
	want := []deliveredPrompt{
		{Text: "ASK"},
		{Text: "owner words", PromptID: "p-owner", Sender: store.PromptSenderOwner},
		{Text: block, PromptID: callerStepEvent, Sender: store.PromptSenderEvent},
	}
	if got := enginePromptsOf(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
	if !eventMarked(t, s, id) {
		t.Fatal("step end not marked after its delivery")
	}
}

// TestDeclineHeadlessRefusesUnopenedRequest declines a request id the session has not open, and an empty one. Each
// returns ErrQuestionNotOpen naming the id, and sends nothing: the fake reads no further line and the session is not
// quieted. The open question is still open afterwards, so declining it then succeeds.
func TestDeclineHeadlessRefusesUnopenedRequest(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	requestID := requestIDOf(t, history)
	sent := stdinLines(t, dir)

	for _, unopened := range []string{"no-such-request", ""} {
		_, err = m.Decline(t.Context(), id, unopened)
		if !errors.Is(err, ErrQuestionNotOpen) || !strings.Contains(err.Error(), `"`+unopened+`"`) {
			t.Fatalf("Decline of request %q error = %v, want ErrQuestionNotOpen naming it", unopened, err)
		}
	}
	if got := stdinLines(t, dir); !slices.Equal(got, sent) {
		t.Fatalf("refused Declines wrote %q to the fake, want nothing", got)
	}
	if isQuiet(m, id) {
		t.Fatal("a refused Decline left the session quiet")
	}

	if _, err = m.Decline(t.Context(), id, requestID); err != nil {
		t.Fatalf("Decline of the open request after the refusals: %v", err)
	}
}
