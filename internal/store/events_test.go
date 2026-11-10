package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

func strPtr(value string) *string {
	return &value
}

// eventPageStore writes a store whose events, in seq order, are: 1 session_added for orrery-1, 2 session_changed
// for orrery-1 (state, label), 3 session_added for orrery-2, 4 session_changed for orrery-2 (state), 5
// session_changed for orrery-1 (state, and label cleared to ""), 6 session_removed for orrery-1. It returns the
// writer, which stays open for the test, and the store path.
func eventPageStore(t *testing.T) (*Store, string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := time.Unix(1_700_000_000, 0)
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working", Label: "one"}},
		{ID: "orrery-2", Pane: "%2", Fields: Fields{State: "idle"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.SyncPane(
		ctx,
		"-L a",
		Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
		now,
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-2", Pane: "%2", Fields: Fields{State: "idle"}},
	}, now); err != nil {
		t.Fatalf("SyncServer removing orrery-1: %v", err)
	}
	return s, path
}

func pageSeqs(events []Event) []int64 {
	seqs := make([]int64, 0, len(events))
	for _, event := range events {
		seqs = append(seqs, event.Seq)
	}
	return seqs
}

func TestEventsPageReturnsEventsAfterCursorInSeqOrder(t *testing.T) {
	ctx := context.Background()
	_, path := eventPageStore(t)
	r := NewReader(path)

	cases := []struct {
		name  string
		after int64
		limit int
		want  []int64
	}{
		{name: "first page", after: 0, limit: 4, want: []int64{1, 2, 3, 4}},
		{name: "rest after cursor", after: 4, limit: 4, want: []int64{5, 6}},
		{name: "past the newest", after: 6, limit: 4, want: []int64{}},
		{name: "cap below the page", after: 2, limit: 1, want: []int64{3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page, err := r.Events(ctx, tc.after, tc.limit)
			if err != nil {
				t.Fatalf("Events(%d, %d): %v", tc.after, tc.limit, err)
			}
			if got := pageSeqs(page.Events); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("seqs: got %v, want %v", got, tc.want)
			}
			if page.Earliest != 1 || page.Newest != 6 {
				t.Fatalf("bounds: got oldest %d newest %d, want 1 and 6", page.Earliest, page.Newest)
			}
		})
	}
}

func TestEventsPagingByLastSeqReachesEveryEventOnce(t *testing.T) {
	ctx := context.Background()
	s, path := eventPageStore(t)
	r := NewReader(path)

	var got []int64
	var cursor int64
	for {
		page, err := r.Events(ctx, cursor, 2)
		if err != nil {
			t.Fatalf("Events after %d: %v", cursor, err)
		}
		if len(page.Events) == 0 {
			break
		}
		last := page.Events[len(page.Events)-1].Seq
		if last <= cursor {
			t.Fatalf("page after %d ended at seq %d, which does not advance the cursor", cursor, last)
		}
		got = append(got, pageSeqs(page.Events)...)
		cursor = last
	}

	logged := eventsOf(t, s)
	want := make([]int64, 0, len(logged))
	for _, event := range logged {
		want = append(want, event.Seq)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged seqs: got %v, want %v", got, want)
	}
}

func TestEventsDecodesEachKind(t *testing.T) {
	ctx := context.Background()
	_, path := eventPageStore(t)
	now := time.Unix(1_700_000_000, 0).Unix()

	page, err := NewReader(path).Events(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	want := []Event{
		{Seq: 1, SessionID: "orrery-1", Kind: EventSessionAdded, At: now, Server: "-L a", Pane: "%1"},
		{Seq: 2, SessionID: "orrery-1", Kind: EventSessionChanged, At: now,
			Changes: FieldChanges{State: strPtr("working"), Label: strPtr("one")}},
		{Seq: 3, SessionID: "orrery-2", Kind: EventSessionAdded, At: now, Server: "-L a", Pane: "%2"},
		{Seq: 4, SessionID: "orrery-2", Kind: EventSessionChanged, At: now,
			Changes: FieldChanges{State: strPtr("idle")}},
		{Seq: 5, SessionID: "orrery-1", Kind: EventSessionChanged, At: now,
			Changes: FieldChanges{State: strPtr("idle"), Label: strPtr("")}},
		{Seq: 6, SessionID: "orrery-1", Kind: EventSessionRemoved, At: now},
	}
	if !reflect.DeepEqual(page.Events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", page.Events, want)
	}
}

func TestEventsReturnsEngineKindsWithCommonFieldsOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	now := engineNowAt()
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "working"}},
	}, now); err != nil {
		t.Fatalf("SyncServer: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineAdd{ID: "e1", Dir: "/a", State: "working"},
		EnginePrompt{ID: "e1", Text: "hi"},
	}, now); err != nil {
		t.Fatalf("ApplyEngine add and prompt: %v", err)
	}
	if err := s.SyncPane(
		ctx,
		"-L a",
		Pane{ID: "orrery-1", Pane: "%1", Fields: Fields{State: "idle"}},
		now,
	); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	if err := s.ApplyEngine(ctx, []EngineOp{EngineTurnEnded{ID: "e1", Subtype: "success"}}, now); err != nil {
		t.Fatalf("ApplyEngine turn ended: %v", err)
	}

	page, err := NewReader(path).Events(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Events over engine kinds: %v", err)
	}
	at := now.Unix()
	want := []Event{
		{Seq: 1, SessionID: "orrery-1", Kind: EventSessionAdded, At: at, Server: "-L a", Pane: "%1"},
		{Seq: 2, SessionID: "orrery-1", Kind: EventSessionChanged, At: at,
			Changes: FieldChanges{State: strPtr("working")}},
		{Seq: 3, SessionID: "e1", Kind: EventEngineAdded, At: at},
		{Seq: 4, SessionID: "e1", Kind: EventEnginePrompt, At: at},
		{Seq: 5, SessionID: "orrery-1", Kind: EventSessionChanged, At: at,
			Changes: FieldChanges{State: strPtr("idle")}},
		{Seq: 6, SessionID: "e1", Kind: EventEngineTurnEnded, At: at},
	}
	if !reflect.DeepEqual(page.Events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", page.Events, want)
	}
}

func TestEventsSkipsEngineKindsWithoutDecodingPayload(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	s := openWriterAt(t, path)
	if err := sqlcgen.New(s.db).InsertEvent(ctx, sqlcgen.InsertEventParams{
		SessionID: "e1", Kind: EventEngineBlock, At: 1, Payload: `{"not json`,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	page, err := NewReader(path).Events(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Events of engine row with a malformed payload: %v", err)
	}
	want := []Event{{Seq: 1, SessionID: "e1", Kind: EventEngineBlock, At: 1}}
	if !reflect.DeepEqual(page.Events, want) {
		t.Fatalf("events:\n got %+v\nwant %+v", page.Events, want)
	}
}

func TestEventsBoundsWithNoEventsAreZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orrery.db")
	openWriterAt(t, path)

	page, err := NewReader(path).Events(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(page.Events) != 0 || page.Earliest != 0 || page.Newest != 0 {
		t.Fatalf("empty store: got %+v, want no events and bounds 0", page)
	}
}

func TestEventsOldestRisesAfterPrune(t *testing.T) {
	ctx := context.Background()
	s, path := eventPageStore(t)
	now := time.Unix(1_700_000_000, 0)

	// Every event of eventPageStore is older than the retention cutoff of this sync, so the sync prunes them all and
	// keeps only the event it appends for orrery-3.
	later := now.Add(EventRetention + time.Hour)
	if err := s.SyncServer(ctx, "-L a", []Pane{
		{ID: "orrery-2", Pane: "%2", Fields: Fields{State: "idle"}},
		{ID: "orrery-3", Pane: "%3"},
	}, later); err != nil {
		t.Fatalf("SyncServer after retention: %v", err)
	}

	page, err := NewReader(path).Events(ctx, 0, 10)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Seq != 7 {
		t.Fatalf("events after prune: got %+v, want only seq 7", page.Events)
	}
	if page.Earliest != 7 || page.Newest != 7 {
		t.Fatalf("bounds after prune: got oldest %d newest %d, want 7 and 7", page.Earliest, page.Newest)
	}
}

func TestEventsReturnsUnavailableWhenStoreIsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	path := filepath.Join(dir, "orrery.db")

	_, err := NewReader(path).Events(context.Background(), 0, 10)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Events of missing store: got %v, want ErrUnavailable", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store's directory was created or unreadable: %v", statErr)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing store file was created or unreadable: %v", statErr)
	}
}

func TestEventsRejectsRowsThatDoNotDecode(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		payload string
	}{
		{name: "unknown kind", kind: "session_renamed", payload: `{}`},
		{name: "added with unknown field", kind: EventSessionAdded,
			payload: `{"server":"-L a","pane":"%1","extra":"x"}`},
		{name: "added with wrong type", kind: EventSessionAdded, payload: `{"server":1,"pane":"%1"}`},
		{name: "changed with unknown field", kind: EventSessionChanged, payload: `{"fields":{"bogus":"red"}}`},
		{name: "changed malformed", kind: EventSessionChanged, payload: `{"fields":`},
		{name: "removed with a field", kind: EventSessionRemoved, payload: `{"pane":"%1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "orrery.db")
			s := openWriterAt(t, path)
			if err := sqlcgen.New(s.db).InsertEvent(ctx, sqlcgen.InsertEventParams{
				SessionID: "orrery-1", Kind: tc.kind, At: 1, Payload: tc.payload,
			}); err != nil {
				t.Fatalf("InsertEvent: %v", err)
			}

			_, err := NewReader(path).Events(ctx, 0, 10)
			if err == nil {
				t.Fatalf("Events of %s row: got no error", tc.name)
			}
			if errors.Is(err, ErrUnavailable) {
				t.Fatalf("Events of %s row: got %v, which matches ErrUnavailable", tc.name, err)
			}
		})
	}
}

func TestEventsRejectsNonPositiveLimit(t *testing.T) {
	_, path := eventPageStore(t)
	r := NewReader(path)

	for _, limit := range []int{0, -1} {
		if _, err := r.Events(context.Background(), 0, limit); err == nil {
			t.Fatalf("Events with limit %d: got no error", limit)
		}
	}
}
