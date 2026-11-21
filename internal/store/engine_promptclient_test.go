package store

import "testing"

// TestPromptClientPayloadCarriesClientOnlyWhenSet checks that an engine_prompt payload has a client key when the prompt
// was sent with one, and no client key when it was not.
func TestPromptClientPayloadCarriesClientOnlyWhenSet(t *testing.T) {
	t.Parallel()
	s := openSyncStore(t)

	ops := []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EnginePrompt{ID: "e1", Text: "from the phone", Client: "orrery-android", WriteID: "w1"},
		EnginePrompt{ID: "e1", Text: "from nobody", WriteID: "w2"},
	}
	if err := s.ApplyEngine(t.Context(), ops, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	assertEngineEvents(t, s, engineNowAt(), []wantEngineEvent{
		{"e1", EventEngineAdded, map[string]any{"dir": "/a", "state": "working"}},
		{"e1", EventEnginePrompt, map[string]any{"text": "from the phone", "client": "orrery-android"}},
		{"e1", EventEnginePrompt, map[string]any{"text": "from nobody"}},
	})
}
