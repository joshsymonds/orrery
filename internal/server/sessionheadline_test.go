package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// fixedHeadlineHaiku is a haiku caller that answers every call with the same headline.
type fixedHeadlineHaiku struct{}

// Call answers with the fixed headline, whatever the schema and input.
func (fixedHeadlineHaiku) Call(context.Context, string, string) (json.RawMessage, error) {
	return json.RawMessage(`{"headline":"Billing rounding plan"}`), nil
}

// TestSessionHeadlineLoopHeadlineIsWhatListHeadlessCarries runs the headline loop over a store of three engine sessions
// and checks that ListHeadless carries the loop's headline for the session whose turn ended, the title for the session
// with a title and no turn end, and the first words of the first prompt for the session with only a prompt.
func TestSessionHeadlineLoopHeadlineIsWhatListHeadlessCarries(t *testing.T) {
	t.Parallel()
	// promptOnlyID has only a first prompt, so its headline is the prompt's first words.
	const promptOnlyID = "cccccccccccccccccccccccccccccccc"
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openHeadlessStore(t, path)
	now := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	applyHeadless(t, s, now.Add(-time.Hour), store.EngineAdd{ID: headlessAlphaID, Dir: "/work/gamma", State: "idle"})
	applyHeadless(t, s, now.Add(-time.Hour), store.EnginePrompt{ID: headlessAlphaID, Text: labelPrompt})
	applyHeadless(t, s, now.Add(-time.Hour), store.EngineTurnEnded{
		ID: headlessAlphaID, Subtype: "success", TerminalReason: "completed", Result: "done", Origin: "user",
	})
	applyHeadless(t, s, now.Add(-time.Hour), store.EngineAdd{ID: headlessBetaID, Dir: "/work/delta", State: "idle"})
	applyHeadless(t, s, now.Add(-time.Hour), store.EngineAdd{ID: promptOnlyID, Dir: "/work/epsilon", State: "idle"})
	applyHeadless(t, s, now.Add(-time.Hour), store.EnginePrompt{ID: promptOnlyID, Text: labelPrompt})
	if err := s.SetSessionTitle(t.Context(), headlessBetaID, "Gamma work"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}

	// The loop runs its first pass over s, and stops once that pass has run.
	passed := make(chan struct{}, 1)
	headlines := enginehost.NewSessionHeadlines(enginehost.SessionHeadlinesConfig{
		Store: s,
		Haiku: fixedHeadlineHaiku{},
		Now:   func() time.Time { return now },
		Passed: func() {
			passed <- struct{}{}
		},
	})
	runCtx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		headlines.Run(runCtx)
	}()
	<-passed
	stop()
	<-done
	if err := s.Close(); err != nil {
		t.Fatalf("Close store: %v", err)
	}

	sessions := listedSessions(t, path, now)
	wantHeadlines := map[string]string{
		headlessAlphaID: "Billing rounding plan",
		headlessBetaID:  "Gamma work",
		promptOnlyID:    "Refactor the billing module so every inv",
	}
	for id, want := range wantHeadlines {
		session, ok := sessions[id]
		if !ok {
			t.Fatalf("ListHeadless does not list session %s", id)
		}
		if got := session.GetHeadline(); got != want {
			t.Fatalf("headline of %s = %q, want %q", id, got, want)
		}
	}
}
