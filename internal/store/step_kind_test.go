package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStepKindEngineAddStoresKindStep(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	now := engineNowAt()

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
		EngineAdd{ID: "eng-a", Dir: "/e", State: "idle"},
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	row, err := s.EngineSession(ctx, "step-a")
	if err != nil {
		t.Fatalf("EngineSession step-a: %v", err)
	}
	if row.Kind != "step" {
		t.Fatalf("EngineSession step-a Kind: got %q, want %q", row.Kind, "step")
	}

	rows, err := s.EngineSessions(ctx)
	if err != nil {
		t.Fatalf("EngineSessions: %v", err)
	}
	wantKinds := map[string]string{"step-a": "step", "eng-a": "engine"}
	if len(rows) != len(wantKinds) {
		t.Fatalf("EngineSessions: got %+v, want %d rows", rows, len(wantKinds))
	}
	for _, r := range rows {
		if want := wantKinds[r.ID]; r.Kind != want {
			t.Fatalf("EngineSessions %s Kind: got %q, want %q", r.ID, r.Kind, want)
		}
	}

	engineRow, err := s.EngineSession(ctx, "eng-a")
	if err != nil {
		t.Fatalf("EngineSession eng-a: %v", err)
	}
	if engineRow.Kind != "engine" {
		t.Fatalf("EngineSession eng-a Kind: got %q, want %q", engineRow.Kind, "engine")
	}
}

func TestStepKindEngineSetChangesStepState(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
		EngineSet{ID: "step-a", Changes: EngineChanges{State: strOf("working")}},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	row, err := s.EngineSession(ctx, "step-a")
	if err != nil {
		t.Fatalf("EngineSession step-a: %v", err)
	}
	if row.State != "working" {
		t.Fatalf("EngineSession step-a State: got %q, want %q", row.State, "working")
	}
	if row.Kind != "step" {
		t.Fatalf("EngineSession step-a Kind after EngineSet: got %q, want %q", row.Kind, "step")
	}
}

func TestStepKindReaderSnapshotListsNoStepRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := time.Unix(1_700_000_000, 0)

	if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	snap := snapshotOf(t, NewReader(path))
	ids := make([]string, 0, len(snap.Sessions))
	for _, sess := range snap.Sessions {
		ids = append(ids, sess.ID)
	}
	if want := []string{"orrery-1"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("Snapshot session ids: got %v, want %v (step row must not be listed)", ids, want)
	}
}

func TestStepKindRowSurvivesPruneWhileStaleEngineRowIsRemoved(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	panes := []Pane{{ID: "orrery-1", Pane: "%1"}}

	if err := s.SyncServer(ctx, "-L a", panes, t0); err != nil {
		t.Fatalf("SyncServer at T: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
		EngineAdd{ID: "eng-a", Dir: "/e", State: "idle"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine at T: %v", err)
	}

	now := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", panes, now); err != nil {
		t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"step-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine rows: got %v, want %v (step row kept, stale engine row removed)", got, want)
	}
}
