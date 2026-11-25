package store

import (
	"context"
	"testing"
	"time"
)

// heardSession is the engine session the heard tests steer their epic by.
const heardSession = "engine-1"

// createSteeredEpic creates claimEpic steered by session, and claims its turn so Attention lists the epic. An empty
// session leaves the epic with no steerer.
func createSteeredEpic(t *testing.T, s *Store, session string) {
	t.Helper()
	row := fullEpic(claimEpic)
	row.Steerer = session
	createEpic(t, s, row)
	if _, err := s.ClaimTurn(context.Background(), claimEpic, claimedAt()); err != nil {
		t.Fatalf("ClaimTurn: %v", err)
	}
}

// addHeardSession adds the engine session heardSession in state, so an epic can be steered by it.
func addHeardSession(t *testing.T, s *Store, state string) {
	t.Helper()
	if err := s.ApplyEngine(
		context.Background(), []EngineOp{EngineAdd{ID: heardSession, Dir: "/work/synthetic-repo", State: state}},
		claimedAt(),
	); err != nil {
		t.Fatalf("ApplyEngine add %s: %v", heardSession, err)
	}
}

// heardOf returns the Heard of the attention entry of claimEpic, and fails the test when Attention has no entry for it.
func heardOf(t *testing.T, s *Store) bool {
	t.Helper()
	attention, err := s.Attention(context.Background())
	if err != nil {
		t.Fatalf("Attention: %v", err)
	}
	for _, entry := range attention {
		if entry.Epic == claimEpic {
			return entry.Heard
		}
	}
	t.Fatalf("Attention has no entry for %s", claimEpic)
	return false
}

func TestAttentionHeard(t *testing.T) {
	t.Parallel()
	t.Run("running_steerer", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		addHeardSession(t, s, "working")
		createSteeredEpic(t, s, heardSession)

		if !heardOf(t, s) {
			t.Errorf("Heard of an epic with a running steerer = false, want true")
		}
	})

	t.Run("no_steerer", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		createSteeredEpic(t, s, "")

		if heardOf(t, s) {
			t.Errorf("Heard of an epic with no steerer = true, want false")
		}
	})

	t.Run("steerer_row_gone", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		s := openEpicStore(t)
		const server = "-L orrery"
		if err := s.SyncPane(ctx, server, Pane{
			ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"},
		}, claimedAt()); err != nil {
			t.Fatalf("SyncPane: %v", err)
		}
		createSteeredEpic(t, s, "orrery-1")
		if !heardOf(t, s) {
			t.Fatalf("Heard before the steerer's row is gone = false, want true")
		}

		// The pane leaves the server, so its sessions row is removed and the steerer's row is gone with it.
		if err := s.SyncServer(ctx, "", server, nil, claimedAt().Add(time.Minute)); err != nil {
			t.Fatalf("SyncServer: %v", err)
		}
		if heardOf(t, s) {
			t.Errorf("Heard of an epic whose steerer's row is gone = true, want false")
		}
	})

	t.Run("steerer_stopped", func(t *testing.T) {
		t.Parallel()
		s := openEpicStore(t)
		addHeardSession(t, s, "stopped")
		createSteeredEpic(t, s, heardSession)

		if heardOf(t, s) {
			t.Errorf("Heard of an epic whose steerer is stopped = true, want false")
		}
	})
}
