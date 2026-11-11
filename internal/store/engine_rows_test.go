package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestEngineRowsReadEngineRowsOnly(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineSet{ID: "e1", Changes: EngineChanges{
			State:         strOf("waiting"),
			Question:      strOf("deploy?"),
			ClaudeSession: strOf("claude-1"),
			Reason:        strOf("blocked"),
		}},
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	pane := Pane{ID: "orrery-a", Pane: "%1", Fields: Fields{State: "working", Label: "api"}}
	if err := s.SyncPane(ctx, "-L demo", pane, now); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	wantAll := []EngineRow{
		{ID: "e1", Dir: "/a", State: "waiting", Question: "deploy?", ClaudeSession: "claude-1", Reason: "blocked"},
		{ID: "e2", Dir: "/b", State: "idle"},
	}
	got, err := s.EngineSessions(ctx)
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	if !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("EngineSessions: got %+v, want %+v", got, wantAll)
	}

	for _, want := range wantAll {
		row, err := s.EngineSession(ctx, want.ID)
		if err != nil {
			t.Fatalf("EngineSession %s: %v", want.ID, err)
		}
		if row != want {
			t.Fatalf("EngineSession %s: got %+v, want %+v", want.ID, row, want)
		}
	}

	if _, err := s.EngineSession(ctx, "orrery-a"); !errors.Is(err, ErrNoEngineSession) {
		t.Fatalf("EngineSession of a pane row: got %v, want ErrNoEngineSession", err)
	}
	if _, err := s.EngineSession(ctx, "nope"); !errors.Is(err, ErrNoEngineSession) {
		t.Fatalf("EngineSession of an unknown id: got %v, want ErrNoEngineSession", err)
	}
}
