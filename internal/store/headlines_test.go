package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// headlineStore writes a store with the sessions the headline tests read, every event timed relative to engineNowAt.
// An engine session e-due has a prompt and two turn ends, 300 and 100 seconds before engineNowAt, and no headline.
// An engine session e-headlined has a turn end 300 seconds before and a headline written 200 seconds before, after
// that turn end. An engine session e-resumed has the same headline and a turn end 100 seconds before, after that
// headline. An engine session e-idle has a prompt and no turn end. A step session step-a has a turn end, and a pane
// session orrery-1 exists. It returns the store, which stays open for the test, and the path of its file.
func headlineStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	t0 := engineNowAt().Unix()
	at := func(offset int64) time.Time { return time.Unix(t0+offset, 0) }

	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
	}, at(0)); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	for _, step := range []struct {
		now time.Time
		ops []EngineOp
	}{
		{at(-600), []EngineOp{
			EngineAdd{ID: "e-due", Dir: "/work/due", State: "idle"},
			EnginePrompt{ID: "e-due", Text: "first prompt"},
			EngineAdd{ID: "e-headlined", Dir: "/work/headlined", State: "idle"},
			EnginePrompt{ID: "e-headlined", Text: "headlined prompt"},
			EngineAdd{ID: "e-resumed", Dir: "/work/resumed", State: "idle"},
			EnginePrompt{ID: "e-resumed", Text: "first prompt"},
			EngineAdd{ID: "e-idle", Dir: "/work/idle", State: "idle"},
			EnginePrompt{ID: "e-idle", Text: "idle prompt"},
			EngineAdd{ID: "step-a", Dir: "/work/step", State: "idle", Kind: KindStep},
		}},
		{at(-300), []EngineOp{
			EngineTurnEnded{ID: "e-due", Subtype: "success"},
			EngineTurnEnded{ID: "e-headlined", Subtype: "success"},
			EngineTurnEnded{ID: "e-resumed", Subtype: "success"},
			EngineTurnEnded{ID: "step-a", Subtype: "success"},
		}},
		{at(-100), []EngineOp{
			EngineTurnEnded{ID: "e-due", Subtype: "success"},
			EngineTurnEnded{ID: "e-resumed", Subtype: "success"},
			EngineTurnEnded{ID: "step-a", Subtype: "success"},
		}},
	} {
		if err := s.ApplyEngine(ctx, step.ops, step.now); err != nil {
			t.Fatalf("ApplyEngine at %v: %v", step.now, err)
		}
	}
	if err := s.SetSessionHeadline(ctx, "e-headlined", "Headlined already", at(-200)); err != nil {
		t.Fatalf("SetSessionHeadline e-headlined: %v", err)
	}
	if err := s.SetSessionHeadline(ctx, "e-resumed", "Resumed", at(-200)); err != nil {
		t.Fatalf("SetSessionHeadline e-resumed: %v", err)
	}
	return s, path
}

func TestSessionHeadlineDueListsOnlySessionsWithATurnEndNewerThanTheHeadline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := headlineStore(t)
	t0 := engineNowAt().Unix()

	got, err := s.HeadlineDueSessions(ctx)
	if err != nil {
		t.Fatalf("HeadlineDueSessions: %v", err)
	}
	// e-due is due with its newest turn end, and e-resumed is due again after a turn end that follows its headline.
	// e-headlined was headlined after its last turn end, and e-idle has no turn end, so neither is listed.
	want := []HeadlineDueSession{
		{ID: "e-due", Dir: "/work/due", FirstPrompt: "first prompt", TurnAt: t0 - 100, HeadlineAt: 0},
		{ID: "e-resumed", Dir: "/work/resumed", FirstPrompt: "first prompt", TurnAt: t0 - 100, HeadlineAt: t0 - 200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadlineDueSessions:\n got %+v\nwant %+v", got, want)
	}
}

func TestSessionHeadlineDueSessionReadsOneDueSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := headlineStore(t)
	t0 := engineNowAt().Unix()

	got, ok, err := s.HeadlineDueSession(ctx, "e-resumed")
	if err != nil {
		t.Fatalf("HeadlineDueSession e-resumed: %v", err)
	}
	want := HeadlineDueSession{
		ID: "e-resumed", Dir: "/work/resumed", FirstPrompt: "first prompt", TurnAt: t0 - 100, HeadlineAt: t0 - 200,
	}
	if !ok || got != want {
		t.Fatalf("HeadlineDueSession e-resumed = %+v, %t; want %+v, true", got, ok, want)
	}
	// e-headlined was headlined after its last turn end, e-idle has no turn end, step-a is a step session, orrery-1 is a
	// pane, and no-such-session is unknown: none of them is due.
	for _, id := range []string{"e-headlined", "e-idle", "step-a", "orrery-1", "no-such-session"} {
		if _, ok, err := s.HeadlineDueSession(ctx, id); err != nil || ok {
			t.Fatalf("HeadlineDueSession %s = %t, %v; want false and no error", id, ok, err)
		}
	}
}

func TestSessionHeadlineRecentTurnsReturnPromptsAndMainTextOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e-talk", Dir: "/work/talk", State: "idle"},
		EnginePrompt{ID: "e-talk", Text: "first"},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "assistant", MessageID: "m1", Text: "reply one"},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "assistant", MessageID: "m2", ParentToolUseID: "tool-1",
			Text: "sub-agent reply"},
		EngineBlock{ID: "e-talk", Kind: "thinking", Role: "assistant", MessageID: "m3", Text: "hidden thought"},
		EngineBlock{ID: "e-talk", Kind: "tool_use", Role: "assistant", MessageID: "m4", Name: "Read"},
		EnginePrompt{ID: "e-talk", Text: "second"},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "user", MessageID: "m5", Text: "user block"},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "assistant", MessageID: "m6", Text: "reply two"},
		EnginePrompt{ID: "e-talk", Text: "third"},
		EngineBlock{ID: "e-talk", Kind: "text", Role: "assistant", MessageID: "m7", Text: "reply three"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	got, err := s.RecentTurns(ctx, "e-talk", 4)
	if err != nil {
		t.Fatalf("RecentTurns of the newest four: %v", err)
	}
	want := []RecentTurn{
		{Role: "user", Text: "second"},
		{Role: "assistant", Text: "reply two"},
		{Role: "user", Text: "third"},
		{Role: "assistant", Text: "reply three"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RecentTurns of the newest four:\n got %+v\nwant %+v", got, want)
	}

	all, err := s.RecentTurns(ctx, "e-talk", 10)
	if err != nil {
		t.Fatalf("RecentTurns of every turn: %v", err)
	}
	wantAll := []RecentTurn{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "reply one"},
		{Role: "user", Text: "second"},
		{Role: "assistant", Text: "reply two"},
		{Role: "user", Text: "third"},
		{Role: "assistant", Text: "reply three"},
	}
	if !reflect.DeepEqual(all, wantAll) {
		t.Fatalf("RecentTurns of every turn:\n got %+v\nwant %+v", all, wantAll)
	}
}

func TestSessionHeadlineSetStoresHeadlineAndTimeOfEngineSessionsOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, _ := headlineStore(t)
	at := time.Unix(engineNowAt().Unix()-50, 0)

	if err := s.SetSessionHeadline(ctx, "e-idle", "Idle work", at); err != nil {
		t.Fatalf("SetSessionHeadline: %v", err)
	}
	if err := s.SetSessionHeadline(ctx, "e-idle", "Idle work, rewritten", at.Add(10*time.Second)); err != nil {
		t.Fatalf("SetSessionHeadline again: %v", err)
	}
	row, err := s.fixtureQueries().GetSession(ctx, "e-idle")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.Headline != "Idle work, rewritten" || row.HeadlineAt != at.Unix()+10 {
		t.Fatalf("headline of e-idle = %q at %d, want the second write at %d",
			row.Headline, row.HeadlineAt, at.Unix()+10)
	}

	for _, id := range []string{"step-a", "orrery-1", "no-such-session"} {
		if err := s.SetSessionHeadline(ctx, id, "Not a headline", at); err != nil {
			t.Fatalf("SetSessionHeadline %s: %v", id, err)
		}
	}
	for _, id := range []string{"step-a", "orrery-1"} {
		row, err := s.fixtureQueries().GetSession(ctx, id)
		if err != nil {
			t.Fatalf("GetSession %s: %v", id, err)
		}
		if row.Headline != "" || row.HeadlineAt != 0 {
			t.Fatalf("headline of %s = %q at %d, want none", id, row.Headline, row.HeadlineAt)
		}
	}
}

func TestSessionHeadlineReaderRowCarriesTheStoredHeadline(t *testing.T) {
	t.Parallel()
	_, path := headlineStore(t)

	rows, err := NewReader(path).HeadlessSessions(context.Background(), engineNowAt().Unix()-3600)
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	headlines := map[string]string{}
	for _, row := range rows {
		headlines[row.ID] = row.Headline
	}
	want := map[string]string{"e-due": "", "e-headlined": "Headlined already", "e-resumed": "Resumed", "e-idle": ""}
	if !reflect.DeepEqual(headlines, want) {
		t.Fatalf("HeadlessSessions headlines:\n got %v\nwant %v", headlines, want)
	}
}
