package enginehost

import (
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// TestLiveSessionSurvivesPrune checks that the engine row of a live session outlives the retention prune of a second
// store that reads the manager's liveness, and that the same prune removes the row once the session has stopped.
func TestLiveSessionSurvivesPrune(t *testing.T) {
	t.Parallel()
	m, path := newTestManager(t, 1, fakeClaudePath(t))
	id, err := m.Start(t.Context(), StartRequest{Dir: t.TempDir(), Prompt: "ASK"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitHistory(t, path, id, "the waiting state", func(h store.EngineHistory) bool {
		return h.State == "waiting"
	})
	// The test writer stamps events with the wall clock, so every event so far is at or before now. A prune at now
	// has its cutoff an hour after them, so only a live session keeps its row.
	now := time.Now().Add(store.EventRetention + time.Hour)

	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store Close: %v", err)
		}
	})
	s.SetEngineLive(m.Live)

	if err := s.SyncServer(t.Context(), "-L a", nil, now); err != nil {
		t.Fatalf("SyncServer while live: %v", err)
	}
	if _, err := s.EngineSession(t.Context(), id); err != nil {
		t.Fatalf("live session %s was pruned: %v", id, err)
	}

	if err := m.Stop(t.Context(), id); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	waitUntil(t, "session "+id+" to leave the live set", func() bool {
		return len(m.Live()) == 0
	})
	if err := s.SyncServer(t.Context(), "-L a", nil, now); err != nil {
		t.Fatalf("SyncServer after stop: %v", err)
	}
	if _, err := s.EngineSession(t.Context(), id); !errors.Is(err, store.ErrNoEngineSession) {
		t.Fatalf("stopped session %s after prune: error %v, want ErrNoEngineSession", id, err)
	}
}
