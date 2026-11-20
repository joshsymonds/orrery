package store_test

import (
	"context"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// callerMakerOf returns who made an epic call as caller names it in s, and fails the test when the read errors.
func callerMakerOf(t *testing.T, s *store.Store, caller store.EpicCaller) store.Maker {
	t.Helper()
	got, err := s.CallerMaker(context.Background(), caller)
	if err != nil {
		t.Fatalf("CallerMaker(%+v): %v", caller, err)
	}
	return got
}

func assertMaker(t *testing.T, got, want store.Maker) {
	t.Helper()
	if got != want {
		t.Fatalf("CallerMaker = %+v, want %+v", got, want)
	}
}

func TestCallerMakerNoCallerIsOwner(t *testing.T) {
	t.Parallel()
	s, _ := openEpicCallStore(t)

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{}), store.Maker{By: store.MakerOwner})
}

func TestCallerMakerSessionEngineIsConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	// The engine row has an empty state, so only its kind makes it a conversation.
	engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{engine}, epicCallStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Session: "engine-1"}),
		store.Maker{By: store.MakerConversation, Session: "engine-1"})
}

func TestCallerMakerPaneWithStateIsConversation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{State: "working"}}
	if err := s.SyncPane(ctx, "-L orrery", pane, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%1"}),
		store.Maker{By: store.MakerConversation, Session: "orrery-1"})
}

func TestCallerMakerPaneWithEmptyStateIsOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	if err := s.SyncPane(ctx, "-L orrery", store.Pane{ID: "orrery-1", Pane: "%1"}, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%1"}),
		store.Maker{By: store.MakerOwner})
}

func TestCallerMakerSessionWithNoRowIsOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	engine := store.EngineAdd{ID: "engine-1", Dir: "/work/engine", State: "idle"}
	if err := s.ApplyEngine(ctx, []store.EngineOp{engine}, epicCallStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Session: "engine-9"}),
		store.Maker{By: store.MakerOwner})
}

func TestCallerMakerPaneWithNoRowIsOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	pane := store.Pane{ID: "orrery-1", Pane: "%1", Fields: store.Fields{State: "working"}}
	if err := s.SyncPane(ctx, "-L orrery", pane, epicCallStart()); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Server: "-L orrery", Pane: "%9"}),
		store.Maker{By: store.MakerOwner})
}

func TestCallerMakerSessionStepIsOrreryd(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := openEpicCallStore(t)
	step := store.EngineAdd{ID: "step-1", Dir: "/wt/s1", State: "idle", Kind: store.KindStep}
	if err := s.ApplyEngine(ctx, []store.EngineOp{step}, epicCallStart()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	assertMaker(t, callerMakerOf(t, s, store.EpicCaller{Session: "step-1"}),
		store.Maker{By: store.MakerOrreryd})
}
