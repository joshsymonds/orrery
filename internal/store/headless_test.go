package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// headlessStore writes a store that holds an engine session e-recent with a stale reply, a newer reply, a thinking
// block, a tool block, a sub-agent text block, an unrefused request and a refused request; an engine session e-old
// whose events all predate the cutoff; an engine session e-neither with only a refused request; a step session
// step-a; and a pane session orrery-1. Every event that matters is timed relative to engineNowAt, and the cutoff
// the tests pass is headlessCutoff. It returns the store path; the writer stays open for the test.
func headlessStore(t *testing.T) string {
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
	steps := []struct {
		now time.Time
		ops []EngineOp
	}{
		{at(-headlessCutoffAge - 1000), []EngineOp{
			EngineAdd{ID: "e-recent", Dir: "/a", State: "working"},
			EngineBlock{ID: "e-recent", Kind: "text", Role: "assistant", MessageID: "m0", Text: "stale reply"},
			EngineAdd{ID: "e-old", Dir: "/o", State: "idle"},
			EngineBlock{ID: "e-old", Kind: "text", Role: "assistant", MessageID: "m1", Text: "old reply"},
			EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
		}},
		{at(-200), []EngineOp{
			EngineBlock{ID: "e-recent", Kind: "text", Role: "assistant", MessageID: "m2", Text: "newest reply"},
		}},
		{at(-50), []EngineOp{
			EngineRequest{ID: "e-recent", Kind: "permission", RequestID: "r1", ToolName: "Bash", Refused: false},
		}},
		{at(0), []EngineOp{
			EngineRequest{ID: "e-recent", Kind: "permission", RequestID: "r2", ToolName: "Bash", Refused: true},
			EngineBlock{ID: "e-recent", Kind: "thinking", Role: "assistant", MessageID: "m3", Text: "hidden thought"},
			EngineBlock{ID: "e-recent", Kind: "tool_use", Role: "assistant", MessageID: "m4", Name: "Read"},
			EngineBlock{ID: "e-recent", Kind: "text", Role: "assistant", MessageID: "m5", ParentToolUseID: "tool-1",
				Text: "sub-agent text"},
			EngineSet{ID: "e-recent", Changes: EngineChanges{State: strOf("waiting"), Seen: strOf("1699999000"),
				Question: strOf("ask?")}},
			EngineAdd{ID: "e-neither", Dir: "/n", State: "idle"},
			EngineRequest{ID: "e-neither", Kind: "permission", RequestID: "r3", ToolName: "Bash", Refused: true},
			EngineRequest{ID: "step-a", Kind: "permission", RequestID: "r4", ToolName: "Bash", Refused: false},
			EngineBlock{ID: "step-a", Kind: "text", Role: "assistant", MessageID: "m6", Text: "step text"},
		}},
	}
	for _, step := range steps {
		if err := s.ApplyEngine(ctx, step.ops, step.now); err != nil {
			t.Fatalf("ApplyEngine at %v: %v", step.now, err)
		}
	}
	return path
}

// headlessCutoffAge is how far before engineNowAt the cutoff sits, in seconds.
const headlessCutoffAge = 3600

func headlessCutoff() int64 {
	return engineNowAt().Unix() - headlessCutoffAge
}

func TestHeadlessSessionsListsRecentEngineRowsWithNewestReply(t *testing.T) {
	r := NewReader(headlessStore(t))

	got, err := r.HeadlessSessions(context.Background(), headlessCutoff())
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	t0 := engineNowAt().Unix()
	want := []HeadlessRow{
		{
			ID: "e-neither", Dir: "/n", State: "idle", Changed: strconv.FormatInt(t0, 10),
			Reply: "", NewestReply: 0,
		},
		{
			ID: "e-recent", Dir: "/a", State: "waiting", Changed: strconv.FormatInt(t0, 10),
			Seen: "1699999000", Question: "ask?",
			Reply: "newest reply", NewestReply: t0 - 50,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadlessSessions:\n got %+v\nwant %+v", got, want)
	}
}

func TestHeadlessSessionsOmitsRowsWithoutRecentEvents(t *testing.T) {
	r := NewReader(headlessStore(t))

	got, err := r.HeadlessSessions(context.Background(), headlessCutoff())
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	for _, row := range got {
		if row.ID == "e-old" {
			t.Fatalf("HeadlessSessions lists e-old, whose events all predate the cutoff: %+v", got)
		}
		if row.ID == "step-a" || row.ID == "orrery-1" {
			t.Fatalf("HeadlessSessions lists %s, which is not a headless engine session: %+v", row.ID, got)
		}
	}
}

func TestHeadlessSessionsReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	r := NewReader(filepath.Join(t.TempDir(), "missing.db"))

	_, err := r.HeadlessSessions(context.Background(), headlessCutoff())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("HeadlessSessions of missing store: got %v, want ErrUnavailable", err)
	}
}
