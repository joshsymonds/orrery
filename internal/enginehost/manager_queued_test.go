package enginehost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// writtenPrompts returns the text of each prompt the session wrote, in order: the engine_prompt events of history.
func writtenPrompts(t *testing.T, history store.EngineHistory) []string {
	t.Helper()
	var texts []string
	for _, event := range history.Events {
		if event.Kind != store.EventEnginePrompt {
			continue
		}
		var payload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
			t.Fatalf("decode prompt payload %q: %v", event.Payload, err)
		}
		texts = append(texts, payload.Text)
	}
	return texts
}

// turnEndCount returns how many engine_turn_ended events history holds.
func turnEndCount(history store.EngineHistory) int {
	n := 0
	for _, event := range history.Events {
		if event.Kind == store.EventEngineTurnEnded {
			n++
		}
	}
	return n
}

// doneBetweenTurnEnds reports whether history records the state done after its first turn end and before its second.
func doneBetweenTurnEnds(t *testing.T, history store.EngineHistory) bool {
	t.Helper()
	ends := 0
	for _, event := range history.Events {
		switch event.Kind {
		case store.EventEngineTurnEnded:
			ends++
		case store.EventEngineChanged:
			var payload struct {
				Fields struct {
					State string `json:"state"`
				} `json:"fields"`
			}
			if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
				t.Fatalf("decode engine_changed payload %q: %v", event.Payload, err)
			}
			if ends == 1 && payload.Fields.State == "done" {
				return true
			}
		}
	}
	return false
}

// TestQueuedPromptKeepsTheSessionWorkingUntilItsTurnEnds sends a prompt while a JUDGEHOLDS turn runs. The prompt is written
// at once, and the turn reads none of it, so the turn's end leaves it unanswered and claude runs it as the turn after. That
// prompt plays WAITOPEN, which holds before it opens its turn, so the first turn's end is stored while the queued turn is
// still unanswered: the session reads working. Once the queued turn opens and ends, the session is done, with no done
// recorded between the two turn ends, and the owner's first notice is the queued turn's done, carrying its reply, with none
// for the first turn's end before it.
func TestQueuedPromptKeepsTheSessionWorkingUntilItsTurnEnds(t *testing.T) {
	t.Parallel()
	sink := &fakeSink{}
	_, path := openStore(t)
	m := newManagerOn(t, path, 1, fakeClaudePath(t), sink)
	dir := t.TempDir()
	id, err := m.Start(t.Context(), StartRequest{Dir: dir, Prompt: "JUDGEHOLDS"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err = m.Say(t.Context(), SayRequest{ID: id, Text: "WAITOPEN"}); err != nil {
		t.Fatalf("Say during the running turn: %v", err)
	}
	if got := writtenPrompts(t, historyOf(t, path, id)); !slices.Contains(got, "WAITOPEN") {
		t.Fatalf("prompts written when Say returns = %q, want the Say written at once", got)
	}

	releaseHeldTurn(t, dir)
	history := waitHistory(t, engineOf(m), path, id, "the first turn's stored end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 1
	})
	if history.State != "working" {
		t.Fatalf("state after the first turn's end, with the queued turn held = %q, want working", history.State)
	}
	if got := sink.got(); len(got) != 0 {
		t.Fatalf("notices while the queued turn is held = %+v, want none", got)
	}

	if err = os.WriteFile(filepath.Join(dir, "fakeclaude.open"), nil, 0o600); err != nil {
		t.Fatalf("open the queued turn: %v", err)
	}
	history = waitHistory(t, engineOf(m), path, id, "the queued turn's end", func(h store.EngineHistory) bool {
		return turnEndCount(h) == 2 && h.State == "done"
	})
	if !hasBlock(t, history, "fake reply 2") {
		t.Fatal("the queued turn's reply fake reply 2 is not recorded")
	}
	if doneBetweenTurnEnds(t, history) {
		t.Fatal("done was recorded between the first turn's end and the queued turn's end")
	}
	// The pump tells the owner after it stores a line, and it records lines in order, so a notice of the first turn's
	// end would come before the queued turn's.
	want := Notice{Session: id, Dir: dir, State: "done", Body: "fake reply 2"}
	if got := waitNotices(t, engineOf(m), sink); len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("notices after the queued turn's end = %+v, want exactly [%+v]", got, want)
	}
}
