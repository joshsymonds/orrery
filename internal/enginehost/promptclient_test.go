package enginehost

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// promptWithClient is what a test reads of an engine_prompt event: its text, prompt id, sender and client.
type promptWithClient struct {
	Text     string `json:"text"`
	PromptID string `json:"promptId"`
	Sender   string `json:"sender"`
	Client   string `json:"client"`
}

// promptsWithClient returns the payload of each engine_prompt event of history, in seq order.
func promptsWithClient(t *testing.T, history store.EngineHistory) []promptWithClient {
	t.Helper()
	var prompts []promptWithClient
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var prompt promptWithClient
		if err := json.Unmarshal([]byte(event.Payload), &prompt); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		prompts = append(prompts, prompt)
	}
	return prompts
}

// TestPromptClientStartAndSayStoreTheClient checks that a Start and a Say that carry a client store their prompts with
// that client.
func TestPromptClientStartAndSayStoreTheClient(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Client: "orrery-android"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	err = m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1", Client: "orrery-android"})
	if err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to p1", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	want := []promptWithClient{
		{Text: "hi", Client: "orrery-android"},
		{Text: "again", PromptID: "p1", Sender: store.PromptSenderOwner, Client: "orrery-android"},
	}
	if got := promptsWithClient(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}

// TestPromptClientDeliveriesStoreNoClient checks that an event delivery and a cron delivery to a session that a Start
// gave a client store their prompts with no client, though the session's own prompt has one.
func TestPromptClientDeliveriesStoreNoClient(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi", Client: "orrery-android"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the first reply done", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1") && h.State == string(protocol.StateDone)
	})

	if err := m.Deliver(t.Context(), DeliverRequest{ID: id, Text: "the event text", PromptID: "e1"}); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	waitHistory(t, engineOf(m), path, id, "the reply to the event prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2") && h.State == string(protocol.StateDone)
	})

	if err := m.DeliverCron(t.Context(), DeliverRequest{ID: id, Text: "the cron text", PromptID: "c1"}); err != nil {
		t.Fatalf("DeliverCron: %v", err)
	}
	history := waitHistory(t, engineOf(m), path, id, "the reply to the cron prompt", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	want := []promptWithClient{
		{Text: "hi", Client: "orrery-android"},
		{Text: "the event text", PromptID: "e1", Sender: store.PromptSenderEvent},
		{Text: "the cron text", PromptID: "c1", Sender: store.PromptSenderCron},
	}
	if got := promptsWithClient(t, history); !reflect.DeepEqual(got, want) {
		t.Fatalf("prompts = %+v, want %+v", got, want)
	}
}
