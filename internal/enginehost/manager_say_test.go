package enginehost

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// promptIDsOf returns the promptId of each engine_prompt event of history in seq order, and "" for a prompt stored
// without one.
func promptIDsOf(t *testing.T, history store.EngineHistory) []string {
	t.Helper()
	var ids []string
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var payload struct {
			PromptID string `json:"promptId"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		ids = append(ids, payload.PromptID)
	}
	return ids
}

func TestSayStoresPromptID(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}); err != nil {
		t.Fatalf("Say: %v", err)
	}
	history := waitHistory(t, path, id, "the reply to the prompt with id p1", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if got := promptIDsOf(t, history); !reflect.DeepEqual(got, []string{"", "p1"}) {
		t.Fatalf("prompt ids of session = %q, want the start's prompt without an id, then p1", got)
	}
}

func TestSayRepeatedPromptIDIsNoOp(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}); err != nil {
		t.Fatalf("Say with p1: %v", err)
	}
	waitHistory(t, path, id, "the reply to p1", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	before := historyOf(t, path, id)

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}); err != nil {
		t.Fatalf("repeated Say with p1: %v", err)
	}
	if after := historyOf(t, path, id); len(after.Events) != len(before.Events) || after.State != before.State {
		t.Fatalf("repeated Say changed the session: events %d -> %d, state %q -> %q",
			len(before.Events), len(after.Events), before.State, after.State)
	}

	// The sentinel resumes the stopped session. Its reply follows the first two only when the repeated Say sent nothing.
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "sentinel", PromptID: "p2"}); err != nil {
		t.Fatalf("Say with p2: %v", err)
	}
	history := waitHistory(t, path, id, "the reply to the sentinel", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	if got := conversationOf(t, history); !reflect.DeepEqual(got,
		[]string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2",
			"prompt: sentinel", "reply: fake reply 3"}) {
		t.Fatalf("conversation = %q, want no second send of p1", got)
	}
	if args := fakeArgs(t, dir); len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want the first start and one resume for p2", args)
	}
}

func TestSayWithAnotherPromptIDIsSent(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"}); err != nil {
		t.Fatalf("Say with p1: %v", err)
	}
	waitHistory(t, path, id, "the reply to p1", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})

	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "second", PromptID: "p2"}); err != nil {
		t.Fatalf("Say with p2: %v", err)
	}
	history := waitHistory(t, path, id, "the reply to p2", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	if got := conversationOf(t, history); !reflect.DeepEqual(got,
		[]string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2",
			"prompt: second", "reply: fake reply 3"}) {
		t.Fatalf("conversation = %q, want p2 sent after p1", got)
	}
}

func TestConcurrentSaysWithOnePromptIDSendOnce(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "hi"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the first reply block", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 1")
	})
	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	const says = 2
	errs := make(chan error, says)
	var wg sync.WaitGroup
	for range says {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.Say(t.Context(), SayRequest{ID: id, Text: "again", PromptID: "p1"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Say with p1: %v", err)
		}
	}

	waitHistory(t, path, id, "the reply to the one prompt sent", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 2")
	})
	// The sentinel is processed after any duplicate prompt, so its reply shows how many prompts the fake received.
	if err := m.Say(t.Context(), SayRequest{ID: id, Text: "sentinel", PromptID: "p2"}); err != nil {
		t.Fatalf("Say with p2: %v", err)
	}
	history := waitHistory(t, path, id, "the reply to the sentinel", func(h store.EngineHistory) bool {
		return hasBlock(t, h, "fake reply 3")
	})
	if got := conversationOf(t, history); !reflect.DeepEqual(got,
		[]string{"prompt: hi", "reply: fake reply 1", "prompt: again", "reply: fake reply 2",
			"prompt: sentinel", "reply: fake reply 3"}) {
		t.Fatalf("conversation = %q, want one send for the concurrent p1 Says", got)
	}
	if args := fakeArgs(t, dir); len(args) != 2 {
		t.Fatalf("fake claude starts = %q, want the first start and one resume", args)
	}
}
