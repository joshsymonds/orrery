package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// headlessBaseListing is ListHeadlessSessions as it read the reply, newest reply time, first prompt and request id
// from the events, before the sessions held those four columns (queries.sql:169-204 at the base commit). It is the
// event-derived reference the listing must match on a store whose events are all kept. Its cutoff is bound as ? where
// the base query wrote sqlc.arg(cutoff); nothing else differs.
const headlessBaseListing = `SELECT
    sessions.id, sessions.dir, sessions.state, sessions.changed, sessions.seen, sessions.question, sessions.reason,
    sessions.title, sessions.headline,
    CAST(COALESCE((
        SELECT json_extract(events.payload, '$.text') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_block'
          AND json_extract(events.payload, '$.kind') = 'text'
          AND json_extract(events.payload, '$.role') = 'assistant'
          AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = ''
        ORDER BY events.at DESC, events.seq DESC LIMIT 1
    ), '') AS TEXT) AS reply,
    CAST(COALESCE((
        SELECT MAX(events.at) FROM events
        WHERE events.session_id = sessions.id AND (
            (events.kind = 'engine_block' AND json_extract(events.payload, '$.kind') = 'text'
             AND json_extract(events.payload, '$.role') = 'assistant'
             AND IFNULL(json_extract(events.payload, '$.parentToolUseId'), '') = '')
            OR (events.kind = 'engine_request' AND json_extract(events.payload, '$.refused') IS NOT 1)
        )
    ), 0) AS INTEGER) AS newest_reply,
    CAST(COALESCE((
        SELECT json_extract(events.payload, '$.text') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_prompt'
        ORDER BY events.seq LIMIT 1
    ), '') AS TEXT) AS first_prompt,
    CAST(COALESCE((
        SELECT json_extract(events.payload, '$.requestId') FROM events
        WHERE events.session_id = sessions.id AND events.kind = 'engine_request'
          AND json_extract(events.payload, '$.refused') IS NOT 1
        ORDER BY events.seq DESC LIMIT 1
    ), '') AS TEXT) AS request_id
FROM sessions
WHERE sessions.kind = 'engine'
  AND EXISTS (SELECT 1 FROM events WHERE events.session_id = sessions.id AND events.at >= ?)
ORDER BY sessions.id;`

// referenceHeadless runs headlessBaseListing on the writer's connection at cutoff and returns its rows.
func referenceHeadless(t *testing.T, s *Store, cutoff int64) []HeadlessRow {
	t.Helper()
	rows, err := s.db.DB.QueryContext(context.Background(), headlessBaseListing, cutoff)
	if err != nil {
		t.Fatalf("event-derived reference listing: %v", err)
	}
	defer rows.Close()
	var listed []HeadlessRow
	for rows.Next() {
		var row HeadlessRow
		if err := rows.Scan(
			&row.ID, &row.Dir, &row.State, &row.Changed, &row.Seen, &row.Question, &row.Reason,
			&row.Title, &row.Headline, &row.Reply, &row.NewestReply, &row.FirstPrompt, &row.RequestID,
		); err != nil {
			t.Fatalf("scan event-derived reference listing: %v", err)
		}
		listed = append(listed, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read event-derived reference listing: %v", err)
	}
	return listed
}

// seedListing writes an unpruned store through ApplyEngine. It holds an engine session e-a with three prompts, a
// subagent text block, a user text block, a thinking block and a refused request, all after the cutoff; an engine
// session e-b whose only event after the cutoff is a refused request; an engine session e-c whose subagent block
// follows its reply; an engine session e-old whose events all predate the cutoff; and a step session step-a. Every
// time is relative to engineNowAt, so the test reads no clock.
func seedListing(t *testing.T, s *Store) {
	t.Helper()
	t0 := engineNowAt().Unix()
	at := func(offset int64) time.Time { return time.Unix(t0+offset, 0) }
	applyHeadless(t, s, at(-7200),
		EngineAdd{ID: "e-old", Dir: "/o", State: "idle"},
		EnginePrompt{ID: "e-old", Text: "old prompt"},
		EngineBlock{ID: "e-old", Kind: "text", Role: "assistant", MessageID: "m0", Text: "old reply"},
	)
	applyHeadless(t, s, at(-3000),
		EngineAdd{ID: "e-a", Dir: "/a", State: "working"},
		EngineAdd{ID: "e-b", Dir: "/b", State: "idle"},
		EngineAdd{ID: "step-a", Dir: "/s", State: "idle", Kind: KindStep},
		EnginePrompt{ID: "e-a", Text: "first prompt"},
		EngineBlock{ID: "e-a", Kind: "text", Role: "assistant", MessageID: "m1", Text: "reply one"},
		EngineRequest{ID: "e-a", Kind: "permission", RequestID: "r1", ToolName: "Bash"},
		EngineRequest{ID: "e-b", Kind: "permission", RequestID: "r-b", ToolName: "Bash", Refused: true},
		EngineBlock{ID: "step-a", Kind: "text", Role: "assistant", MessageID: "m2", Text: "step text"},
	)
	applyHeadless(t, s, at(-2000),
		EnginePrompt{ID: "e-a", Text: "second prompt"},
		EngineBlock{ID: "e-a", Kind: "text", Role: "assistant", MessageID: "m3", ParentToolUseID: "toolu_sub",
			Text: "subagent reply"},
		EngineBlock{ID: "e-a", Kind: "text", Role: "user", MessageID: "m4", Text: "user text"},
		EngineRequest{ID: "e-a", Kind: "permission", RequestID: "r-refused", ToolName: "Bash", Refused: true},
	)
	applyHeadless(t, s, at(-1000),
		EngineBlock{ID: "e-a", Kind: "text", Role: "assistant", MessageID: "m5", Text: "reply two"},
		EngineBlock{ID: "e-a", Kind: "thinking", Role: "assistant", MessageID: "m6", Text: "thought"},
		EngineRequest{ID: "e-a", Kind: "permission", RequestID: "r2", ToolName: "Bash"},
		EngineSet{ID: "e-a", Changes: EngineChanges{State: strOf("waiting"), Question: strOf("ask?")}},
		EngineAdd{ID: "e-c", Dir: "/c", State: "working"},
		EnginePrompt{ID: "e-c", Text: "c prompt"},
		EngineRequest{ID: "e-c", Kind: "permission", RequestID: "r9", ToolName: "Bash"},
	)
	applyHeadless(t, s, at(-500),
		EngineBlock{ID: "e-c", Kind: "text", Role: "assistant", MessageID: "m7", Text: "c reply"},
		EngineBlock{ID: "e-c", Kind: "text", Role: "assistant", MessageID: "m8", ParentToolUseID: "toolu_c",
			Text: "c subagent"},
	)
	applyHeadless(t, s, at(-100),
		EnginePrompt{ID: "e-a", Text: "third prompt"},
		EngineBlock{ID: "e-a", Kind: "text", Role: "assistant", MessageID: "m9", Text: "reply three"},
	)
}

func TestHeadlessColumnsListingMatchesEventReference(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	seedListing(t, s)
	cutoff := headlessCutoff()

	want := referenceHeadless(t, s, cutoff)
	ids := make([]string, 0, len(want))
	for _, row := range want {
		ids = append(ids, row.ID)
	}
	if wantIDs := []string{"e-a", "e-b", "e-c"}; !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("event-derived reference lists %v, want %v, so the comparison would not exercise the listing",
			ids, wantIDs)
	}

	got, err := NewReader(path).HeadlessSessions(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("HeadlessSessions differs from the event-derived reference:\n got %+v\nwant %+v", got, want)
	}
}

func TestHeadlessColumnsListingKeepsColumnsAfterEventPrune(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	t0 := engineNowAt()
	t1 := t0.Add(time.Hour)
	const id = "e-p"

	applyHeadless(t, s, t0,
		EngineAdd{ID: id, Dir: "/p", State: "working"},
		EnginePrompt{ID: id, Text: "first prompt"},
		EngineBlock{ID: id, Kind: "text", Role: "assistant", MessageID: "m1", Text: "reply"},
		EngineRequest{ID: id, Kind: "permission", RequestID: "r1", ToolName: "Bash"},
	)
	// The one event kept after the prune is at t1, which is at or after the prune's cutoff, so the session stays listed.
	applyHeadless(t, s, t1, EngineSet{ID: id, Changes: EngineChanges{Seen: strOf("later")}})

	// The prune's cutoff is now minus EventRetention. It sits 30 minutes after t0, so the events at t0 go and t1 stays.
	pruneCutoff := t0.Add(30 * time.Minute)
	now := pruneCutoff.Add(EventRetention)
	if n := eventsAt(t, s, id, t0.Unix()); n == 0 {
		t.Fatalf("session %s has no event at t0 before the prune, so the prune removes nothing", id)
	}
	if err := s.SyncServer(context.Background(), "", "-L a", nil, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if n := eventsAt(t, s, id, t0.Unix()); n != 0 {
		t.Fatalf("session %s has %d events at t0 after the prune, want 0", id, n)
	}
	if n := eventsAt(t, s, id, t1.Unix()); n != 1 {
		t.Fatalf("session %s has %d events at t1 after the prune, want 1", id, n)
	}

	got, err := NewReader(path).HeadlessSessions(context.Background(), pruneCutoff.Unix())
	if err != nil {
		t.Fatalf("HeadlessSessions: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("HeadlessSessions after the prune lists %+v, want only %s", got, id)
	}
	row := got[0]
	if row.Reply != "reply" || row.NewestReply != t0.Unix() ||
		row.FirstPrompt != "first prompt" || row.RequestID != "r1" {
		t.Fatalf("HeadlessSessions after the prune: reply %q at %d, first prompt %q, request %q; want %q at %d, %q, %q",
			row.Reply, row.NewestReply, row.FirstPrompt, row.RequestID,
			"reply", t0.Unix(), "first prompt", "r1")
	}
}
