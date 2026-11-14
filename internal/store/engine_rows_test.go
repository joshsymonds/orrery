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
		{ID: "e1", Dir: "/a", State: "waiting", Question: "deploy?", ClaudeSession: "claude-1", Reason: "blocked",
			Kind: KindEngine},
		{ID: "e2", Dir: "/b", State: "idle", Kind: KindEngine},
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

func TestEngineTextSessionsNamesEachSessionWithUnfinishedText(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineAdd{ID: "e2", Dir: "/b", State: "working"},
		EngineAdd{ID: "e3", Dir: "/c", State: "working"},
		EngineAdd{ID: "e4", Dir: "/d", State: "working"},
		EngineAdd{ID: "e5", Dir: "/e", State: "idle"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "one"},
		EngineText{ID: "e1", MessageID: "m1", Index: 1, Kind: "text", Text: "two"},
		EngineText{ID: "e2", MessageID: "m2", Index: 0, Kind: "thinking", Text: "pending"},
		EngineText{ID: "e3", MessageID: "m3", Index: 0, Kind: "text", Text: "finished"},
		EngineTextDone{ID: "e3", MessageID: "m3", Index: 0},
		EngineText{ID: "e4", MessageID: "m4", Index: 0, Kind: "text", Text: "cleared"},
		EngineTextClear{ID: "e4"},
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	want := map[string]bool{"e1": true, "e2": true}
	got, err := s.EngineTextSessions(ctx)
	if err != nil {
		t.Fatalf("EngineTextSessions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EngineTextSessions: got %v, want %v", got, want)
	}
}

func TestEngineTextSessionsEmptyStoreIsEmpty(t *testing.T) {
	s := openSyncStore(t)

	got, err := s.EngineTextSessions(context.Background())
	if err != nil {
		t.Fatalf("EngineTextSessions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("EngineTextSessions of an empty store: got %v, want none", got)
	}
}

func TestEngineTextSessionsClosedStoreErrors(t *testing.T) {
	s := openSyncStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := s.EngineTextSessions(context.Background()); err == nil {
		t.Fatalf("EngineTextSessions of a closed store: got nil error, want one")
	}
}
