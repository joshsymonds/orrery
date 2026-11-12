package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// engineHistoryStore writes a store with a pane session orrery-1 and two engine sessions whose events interleave.
// The engine events, in seq order, are: e1 engine_added, e2 engine_added, e1 engine_prompt, e2 engine_changed (state
// waiting), e1 engine_block, e1 engine_changed (state idle). It returns the store path; the writer stays open for the
// test.
func engineHistoryStore(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := engineNowAt()
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EnginePrompt{ID: "e1", Text: "hi"},
		EngineSet{ID: "e2", Changes: EngineChanges{State: strOf("waiting")}},
		EngineBlock{ID: "e1", Kind: "text", Role: "assistant", MessageID: "m1", Text: "done"},
		EngineSet{ID: "e1", Changes: EngineChanges{State: strOf("idle")}},
	}, now); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	return path
}

func TestEngineHistoryReadsEachSessionInSeqOrder(t *testing.T) {
	r := NewReader(engineHistoryStore(t))

	type wantEvent struct {
		kind    string
		payload string
	}
	cases := []struct {
		id    string
		state string
		want  []wantEvent
	}{
		{
			id:    "e1",
			state: "idle",
			want: []wantEvent{
				{kind: EventEngineAdded, payload: `{"dir":"/a","state":"working"}`},
				{kind: EventEnginePrompt, payload: `{"text":"hi"}`},
				{kind: EventEngineBlock, payload: `{"kind":"text","role":"assistant","messageId":"m1",` +
					`"parentToolUseId":"","text":"done","toolUseId":"","name":"","input":"","content":"","isError":false}`},
				{kind: EventEngineChanged, payload: `{"fields":{"changed":"1700000000","state":"idle"}}`},
			},
		},
		{
			id:    "e2",
			state: "waiting",
			want: []wantEvent{
				{kind: EventEngineAdded, payload: `{"dir":"/b","state":"idle"}`},
				{kind: EventEngineChanged, payload: `{"fields":{"changed":"1700000000","state":"waiting"}}`},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			history, err := r.EngineHistory(context.Background(), tc.id, 0, 10)
			if err != nil {
				t.Fatalf("EngineHistory(%s): %v", tc.id, err)
			}
			if history.State != tc.state {
				t.Fatalf("state: got %q, want %q", history.State, tc.state)
			}
			if len(history.Events) != len(tc.want) {
				t.Fatalf("events: got %d, want %d: %+v", len(history.Events), len(tc.want), history.Events)
			}
			for i, event := range history.Events {
				if event.Kind != tc.want[i].kind {
					t.Fatalf("event %d kind: got %q, want %q", i, event.Kind, tc.want[i].kind)
				}
				if event.Payload != tc.want[i].payload {
					t.Fatalf("event %d payload: got %s, want %s", i, event.Payload, tc.want[i].payload)
				}
				if event.At != engineNowAt().Unix() {
					t.Fatalf("event %d at: got %d, want %d", i, event.At, engineNowAt().Unix())
				}
				if i > 0 && event.Seq <= history.Events[i-1].Seq {
					t.Fatalf("event %d seq %d does not follow seq %d", i, event.Seq, history.Events[i-1].Seq)
				}
			}
		})
	}
}

func TestEngineHistoryPagingByLastSeqReachesEveryEventOnce(t *testing.T) {
	ctx := context.Background()
	r := NewReader(engineHistoryStore(t))

	full, err := r.EngineHistory(ctx, "e1", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory full read: %v", err)
	}
	var got []EngineEvent
	var after int64
	for {
		page, pageErr := r.EngineHistory(ctx, "e1", after, 1)
		if pageErr != nil {
			t.Fatalf("EngineHistory after %d: %v", after, pageErr)
		}
		if len(page.Events) == 0 {
			break
		}
		if len(got)+len(page.Events) > len(full.Events) {
			t.Fatalf("paging returned more events than the session holds: %+v", got)
		}
		got = append(got, page.Events...)
		after = page.Events[len(page.Events)-1].Seq
	}
	if !reflect.DeepEqual(got, full.Events) {
		t.Fatalf("paged events: got %+v, want %+v", got, full.Events)
	}
}

func TestEngineHistoryRejectsUnknownAndPaneIDs(t *testing.T) {
	ctx := context.Background()
	r := NewReader(engineHistoryStore(t))

	for _, id := range []string{"nope", "orrery-1"} {
		_, err := r.EngineHistory(ctx, id, 0, 10)
		if !errors.Is(err, ErrNoEngineSession) {
			t.Fatalf("EngineHistory(%s): got %v, want ErrNoEngineSession", id, err)
		}
	}
}

func TestEngineHistoryReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).EngineHistory(context.Background(), "e1", 0, 10)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("EngineHistory of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}

func TestEngineHistoryRejectsNonPositiveLimit(t *testing.T) {
	r := NewReader(engineHistoryStore(t))

	for _, limit := range []int{0, -1} {
		if _, err := r.EngineHistory(context.Background(), "e1", 0, limit); err == nil {
			t.Fatalf("EngineHistory with limit %d: got no error", limit)
		}
	}
}

// unfinishedHistoryStore writes engine session e1 with a finished-looking row set and two unfinished blocks, one of
// them nested under a parent tool use, and engine session e2 with its own unfinished block. It returns the store
// path and the writer, which stays open so the test can apply more ops.
func unfinishedHistoryStore(t *testing.T) (string, *Store) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EngineSet{ID: "e1", Changes: EngineChanges{
			Seen: strOf("1700000100"), Question: strOf("which file?"), Reason: strOf("stopped"),
		}},
		EngineAdd{ID: "e2", Dir: "/b", State: "idle"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, ParentToolUseID: "t1", Kind: "tool_result", Text: "nested"},
		EngineText{ID: "e1", MessageID: "m1", Index: 0, Kind: "text", Text: "first"},
		EngineText{ID: "e2", MessageID: "m9", Index: 0, Kind: "text", Text: "other"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}
	return path, s
}

func TestEngineHistoryCarriesRowFieldsAndUnfinishedText(t *testing.T) {
	path, _ := unfinishedHistoryStore(t)

	history, err := NewReader(path).EngineHistory(context.Background(), "e1", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory(e1): %v", err)
	}
	if history.Kind != KindEngine || history.Dir != "/a" || history.State != "working" {
		t.Fatalf("row kind/dir/state: got %q %q %q", history.Kind, history.Dir, history.State)
	}
	if history.Changed != "1700000000" || history.Seen != "1700000100" ||
		history.Question != "which file?" || history.Reason != "stopped" {
		t.Fatalf("row changed/seen/question/reason: got %q %q %q %q",
			history.Changed, history.Seen, history.Question, history.Reason)
	}
	want := []UnfinishedText{
		{SessionID: "e1", MessageID: "m1", BlockIndex: 0, ParentToolUseID: "", Kind: "text", Text: "first"},
		{SessionID: "e1", MessageID: "m1", BlockIndex: 0, ParentToolUseID: "t1", Kind: "tool_result", Text: "nested"},
	}
	if !reflect.DeepEqual(history.Unfinished, want) {
		t.Fatalf("unfinished: got %+v, want %+v", history.Unfinished, want)
	}
}

func TestEngineHistoryDropsTextAfterEngineTextDone(t *testing.T) {
	path, s := unfinishedHistoryStore(t)
	ctx := context.Background()
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineTextDone{ID: "e1", MessageID: "m1", Index: 0},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine EngineTextDone: %v", err)
	}

	history, err := NewReader(path).EngineHistory(ctx, "e1", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory(e1): %v", err)
	}
	want := []UnfinishedText{
		{SessionID: "e1", MessageID: "m1", BlockIndex: 0, ParentToolUseID: "t1", Kind: "tool_result", Text: "nested"},
	}
	if !reflect.DeepEqual(history.Unfinished, want) {
		t.Fatalf("unfinished after done: got %+v, want %+v", history.Unfinished, want)
	}
}

func TestEngineHistoryReadsStepKindWithNoUnfinishedText(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "s1", Dir: "/c", State: "idle", Kind: KindStep},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine: %v", err)
	}

	history, err := NewReader(path).EngineHistory(ctx, "s1", 0, 10)
	if err != nil {
		t.Fatalf("EngineHistory(s1): %v", err)
	}
	if history.Kind != KindStep {
		t.Fatalf("kind: got %q, want %q", history.Kind, KindStep)
	}
	if len(history.Unfinished) != 0 {
		t.Fatalf("unfinished: got %+v, want none", history.Unfinished)
	}
}
