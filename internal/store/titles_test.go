package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

// titleStore writes a store with the sessions the title tests read: an engine session e-a with two prompts, the first
// "first prompt"; an engine session e-b with no prompt; an engine session e-c with one prompt and the stored title "Gamma
// work"; a step session step-a with a prompt; and a pane session orrery-1. Every event is stamped at engineNowAt. It
// returns the store, which stays open for the test, and the path of its file.
func titleStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	at := engineNowAt()
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
	}, at); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e-a", Dir: "/work/alpha", State: "working"},
		EnginePrompt{ID: "e-a", Text: "first prompt"},
		EnginePrompt{ID: "e-a", Text: "second prompt"},
		EngineAdd{ID: "e-b", Dir: "/work/beta", State: "idle"},
		EngineAdd{ID: "e-c", Dir: "/work/gamma", State: "idle"},
		EnginePrompt{ID: "e-c", Text: "gamma prompt"},
		EngineAdd{ID: "step-a", Dir: "/work/step", State: "idle", Kind: KindStep},
		EnginePrompt{ID: "step-a", Text: "step prompt"},
	}, at); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	if err := s.SetSessionTitle(ctx, "e-c", "Gamma work"); err != nil {
		t.Fatalf("SetSessionTitle e-c: %v", err)
	}
	return s, path
}

func TestSessionTitleUntitledSessionsListsUntitledEnginesWithAPromptOldestFirst(t *testing.T) {
	t.Parallel()
	s, _ := titleStore(t)

	got, err := s.UntitledSessions(context.Background())
	if err != nil {
		t.Fatalf("UntitledSessions: %v", err)
	}
	want := []UntitledSession{{ID: "e-a", Dir: "/work/alpha", FirstPrompt: "first prompt"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("UntitledSessions:\n got %+v\nwant %+v", got, want)
	}
}

func TestSessionTitleSetWritesOnceAndLeavesAStoredTitleAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := titleStore(t)

	if err := s.SetSessionTitle(ctx, "e-a", "Fix login"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	if err := s.SetSessionTitle(ctx, "e-a", "Something else"); err != nil {
		t.Fatalf("SetSessionTitle again: %v", err)
	}
	row, err := s.fixtureQueries().GetSession(ctx, "e-a")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.Title != "Fix login" {
		t.Fatalf("title after two writes = %q, want the first", row.Title)
	}
	untitled, err := s.UntitledSessions(ctx)
	if err != nil {
		t.Fatalf("UntitledSessions: %v", err)
	}
	if len(untitled) != 0 {
		t.Fatalf("UntitledSessions after a title = %+v, want none", untitled)
	}
}

func TestSessionTitleSetIgnoresStepPaneAndUnknownSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := titleStore(t)

	for _, id := range []string{"step-a", "orrery-1", "no-such-session"} {
		if err := s.SetSessionTitle(ctx, id, "Not a title"); err != nil {
			t.Fatalf("SetSessionTitle %s: %v", id, err)
		}
	}
	for _, id := range []string{"step-a", "orrery-1"} {
		row, err := s.fixtureQueries().GetSession(ctx, id)
		if err != nil {
			t.Fatalf("GetSession %s: %v", id, err)
		}
		if row.Title != "" {
			t.Fatalf("title of %s = %q, want none", id, row.Title)
		}
	}
}

func TestSessionTitleReadersReturnTitleAndOldestPrompt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, path := titleStore(t)
	const epic = "orrery-1/epic-titles"
	for _, id := range []string{"e-a", "e-c"} {
		recorded, err := s.RecordEpicCall(ctx, epic, EpicCaller{Session: id}, "epic_get", engineNowAt())
		if err != nil {
			t.Fatalf("RecordEpicCall %s: %v", id, err)
		}
		if !recorded {
			t.Fatalf("RecordEpicCall %s recorded nothing, want a row", id)
		}
	}
	r := NewReader(path)

	headless, err := r.HeadlessSessions(ctx, engineNowAt().Unix()-1)
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	gotHeadless := map[string][2]string{}
	for _, row := range headless {
		gotHeadless[row.ID] = [2]string{row.Title, row.FirstPrompt}
	}
	wantHeadless := map[string][2]string{
		"e-a": {"", "first prompt"},
		"e-b": {"", ""},
		"e-c": {"Gamma work", "gamma prompt"},
	}
	if !reflect.DeepEqual(gotHeadless, wantHeadless) {
		t.Fatalf("HeadlessSessions title and first prompt:\n got %v\nwant %v", gotHeadless, wantHeadless)
	}

	history, err := r.EngineHistory(ctx, "e-c", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory e-c: %v", err)
	}
	if history.Title != "Gamma work" || history.FirstPrompt != "gamma prompt" {
		t.Fatalf("EngineHistory e-c title %q, first prompt %q; want Gamma work and gamma prompt",
			history.Title, history.FirstPrompt)
	}
	history, err = r.EngineHistory(ctx, "e-b", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory e-b: %v", err)
	}
	if history.Title != "" || history.FirstPrompt != "" {
		t.Fatalf("EngineHistory e-b title %q, first prompt %q; want none", history.Title, history.FirstPrompt)
	}

	epicRows, err := r.EpicSessions(ctx, epic)
	if err != nil {
		t.Fatalf("EpicSessions: %v", err)
	}
	gotEpic := map[string][2]string{}
	for _, row := range epicRows {
		gotEpic[row.Session] = [2]string{row.Title, row.FirstPrompt}
	}
	wantEpic := map[string][2]string{
		"e-a": {"", "first prompt"},
		"e-c": {"Gamma work", "gamma prompt"},
	}
	if !reflect.DeepEqual(gotEpic, wantEpic) {
		t.Fatalf("EpicSessions title and first prompt:\n got %v\nwant %v", gotEpic, wantEpic)
	}
}
