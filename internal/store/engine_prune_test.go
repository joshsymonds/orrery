package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// engineIDsOf returns the ids of every engine session row, in id order.
func engineIDsOf(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := sqlcgen.New(s.db).ListEngineSessions(context.Background())
	if err != nil {
		t.Fatalf("ListEngineSessions: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// paneIDsOf returns the ids of every pane session row, in id order.
func paneIDsOf(t *testing.T, s *Store) []string {
	t.Helper()
	rows := sessionsOf(t, s)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func engineTextCountOf(t *testing.T, s *Store, id string) int {
	t.Helper()
	rows, err := sqlcgen.New(s.db).ListEngineText(context.Background(), id)
	if err != nil {
		t.Fatalf("ListEngineText %s: %v", id, err)
	}
	return len(rows)
}

func TestSyncServerRemovesEngineSessionWithNoEventInRetention(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	later := t0.Add(13 * 24 * time.Hour)
	panes := []Pane{{ID: "orrery-1", Pane: "%1"}}

	// The pane row is written first so its session_added is older than the engine events.
	if err := s.SyncServer(ctx, "-L a", panes, t0); err != nil {
		t.Fatalf("SyncServer at T: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-a", Dir: "/a", State: "idle"},
		EngineText{ID: "eng-a", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine eng-a: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-b", Dir: "/b", State: "idle"},
	}, later); err != nil {
		t.Fatalf("ApplyEngine eng-b: %v", err)
	}

	now := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", panes, now); err != nil {
		t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"eng-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
	if got := engineTextCountOf(t, s, "eng-a"); got != 0 {
		t.Fatalf("engine_text rows of removed eng-a: got %d, want 0", got)
	}
	if got, want := paneIDsOf(t, s), []string{"orrery-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pane sessions: got %v, want %v", got, want)
	}
	// The removal appends no event: the only surviving event is eng-b's engine_added.
	assertEngineEvents(t, s, later, []wantEngineEvent{
		{session: "eng-b", kind: EventEngineAdded, payload: map[string]any{"dir": "/b", "state": "idle"}},
	})
}

func TestSyncServerKeepsEngineSessionsWithEventInRetention(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	panes := []Pane{{ID: "orrery-1", Pane: "%1"}}

	if err := s.SyncServer(ctx, "-L a", panes, t0); err != nil {
		t.Fatalf("SyncServer at T: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-a", Dir: "/a", State: "idle"},
		EngineText{ID: "eng-a", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
		EngineAdd{ID: "eng-b", Dir: "/b", State: "idle"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	now := t0.Add(24 * time.Hour)
	if err := s.SyncServer(ctx, "-L a", panes, now); err != nil {
		t.Fatalf("SyncServer one day after T: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"eng-a", "eng-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
	if got := engineTextCountOf(t, s, "eng-a"); got != 1 {
		t.Fatalf("engine_text rows of kept eng-a: got %d, want 1", got)
	}
	if got, want := paneIDsOf(t, s), []string{"orrery-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pane sessions: got %v, want %v", got, want)
	}
}

func TestSyncServerEngineCutoffIncludesEventAtCutoff(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)

	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-drop", Dir: "/d", State: "idle"},
	}, t0.Add(-time.Second)); err != nil {
		t.Fatalf("ApplyEngine eng-drop: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-keep", Dir: "/k", State: "idle"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine eng-keep: %v", err)
	}

	// The cutoff is now minus EventRetention, which is t0: an event at t0 is at the cutoff and so kept.
	now := t0.Add(EventRetention)
	if err := s.SyncServer(ctx, "-L a", nil, now); err != nil {
		t.Fatalf("SyncServer at the cutoff: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"eng-keep"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
}

// seedStaleEngine writes engine sessions eng-a and eng-b, each with one engine_text row, all at t0. Their only
// events are at t0, so both are stale for any prune at t0 plus EventRetention plus one hour.
func seedStaleEngine(t *testing.T, s *Store, t0 time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, t0); err != nil {
		t.Fatalf("SyncServer at T: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "eng-a", Dir: "/a", State: "idle"},
		EngineText{ID: "eng-a", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
		EngineAdd{ID: "eng-b", Dir: "/b", State: "idle"},
		EngineText{ID: "eng-b", MessageID: "m1", Index: 0, Kind: "text", Text: "partial"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
}

func TestSyncServerKeepsLiveEngineSessionPastRetention(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	seedStaleEngine(t, s, t0)
	s.SetEngineLive(func() []string { return []string{"eng-a"} })

	now := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, now); err != nil {
		t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"eng-a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
	if got := engineTextCountOf(t, s, "eng-a"); got != 1 {
		t.Fatalf("engine_text rows of live eng-a: got %d, want 1", got)
	}
	if got := engineTextCountOf(t, s, "eng-b"); got != 0 {
		t.Fatalf("engine_text rows of removed eng-b: got %d, want 0", got)
	}
}

func TestSyncServerEmptyLiveListStillRemovesStaleEngineSession(t *testing.T) {
	lives := map[string]func() []string{
		"empty slice": func() []string { return []string{} },
		"nil slice":   func() []string { return nil },
	}
	for name, live := range lives {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := openSyncStore(t)
			t0 := time.Unix(1_700_000_000, 0)
			seedStaleEngine(t, s, t0)
			s.SetEngineLive(live)

			now := t0.Add(EventRetention + time.Hour)
			if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, now); err != nil {
				t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
			}

			if got := engineIDsOf(t, s); len(got) != 0 {
				t.Fatalf("engine sessions: got %v, want none", got)
			}
			if got := engineTextCountOf(t, s, "eng-a"); got != 0 {
				t.Fatalf("engine_text rows of removed eng-a: got %d, want 0", got)
			}
		})
	}
}

// promptFailureOf reports whether the failure marker of promptID in session id exists after a prune.
func promptFailureOf(t *testing.T, s *Store, id, promptID string) (string, bool) {
	t.Helper()
	reason, ok, err := s.EnginePromptFailure(context.Background(), id, promptID)
	if err != nil {
		t.Fatalf("EnginePromptFailure %s %s: %v", id, promptID, err)
	}
	return reason, ok
}

func TestSyncServerRemovesPromptFailureWithStaleEngineSession(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	seedStaleEngine(t, s, t0)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePromptFailed{ID: "eng-a", PromptID: "p1", Reason: "send refused"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine failure marker: %v", err)
	}

	now := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, now); err != nil {
		t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
	}

	if got := engineIDsOf(t, s); len(got) != 0 {
		t.Fatalf("engine sessions: got %v, want none", got)
	}
	if reason, ok := promptFailureOf(t, s, "eng-a", "p1"); ok {
		t.Fatalf("failure marker of removed eng-a: got reason %q, want none", reason)
	}
}

func TestSyncServerKeepsPromptFailureOfRetainedEngineSessions(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := time.Unix(1_700_000_000, 0)
	later := t0.Add(13 * 24 * time.Hour)
	seedStaleEngine(t, s, t0)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePromptFailed{ID: "eng-a", PromptID: "p1", Reason: "send refused"},
		EnginePromptFailed{ID: "eng-b", PromptID: "p1", Reason: "send refused"},
		EngineAdd{ID: "eng-c", Dir: "/c", State: "idle"},
	}, t0); err != nil {
		t.Fatalf("ApplyEngine failure markers: %v", err)
	}
	// eng-a has an event in retention, and eng-b stays live past retention.
	if err := s.ApplyEngine(ctx, []EngineOp{
		EnginePrompt{ID: "eng-a", Text: "later", PromptID: "p2"},
	}, later); err != nil {
		t.Fatalf("ApplyEngine eng-a later event: %v", err)
	}
	s.SetEngineLive(func() []string { return []string{"eng-b"} })

	now := t0.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", []Pane{{ID: "orrery-1", Pane: "%1"}}, now); err != nil {
		t.Fatalf("SyncServer at T plus 14 days and one hour: %v", err)
	}

	if got, want := engineIDsOf(t, s), []string{"eng-a", "eng-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("engine sessions: got %v, want %v", got, want)
	}
	if reason, ok := promptFailureOf(t, s, "eng-a", "p1"); !ok || reason != "send refused" {
		t.Fatalf("failure marker of retained eng-a: got (%q, %t), want (\"send refused\", true)", reason, ok)
	}
	if reason, ok := promptFailureOf(t, s, "eng-b", "p1"); !ok || reason != "send refused" {
		t.Fatalf("failure marker of live eng-b: got (%q, %t), want (\"send refused\", true)", reason, ok)
	}
}
