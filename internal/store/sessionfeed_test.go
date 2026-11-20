package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store/sqlcgen"
)

// sessionFeedCall is one call of the hook OnSessionEvents sets.
type sessionFeedCall struct {
	seq      int64
	sessions []string
}

// sessionFeedRecorder records each call of the hook OnSessionEvents sets. The hook runs on the goroutine of the write,
// so a test reads the calls once the write returns.
type sessionFeedRecorder struct {
	calls []sessionFeedCall
}

func (r *sessionFeedRecorder) record(seq int64, sessions []string) {
	r.calls = append(r.calls, sessionFeedCall{seq: seq, sessions: slices.Clone(sessions)})
}

// take returns the calls recorded since the last take.
func (r *sessionFeedRecorder) take() []sessionFeedCall {
	calls := r.calls
	r.calls = nil
	return calls
}

// sessionFeedNewestSeq reads the largest seq of the events table, freshly.
func sessionFeedNewestSeq(ctx context.Context, t *testing.T, s *Store) int64 {
	t.Helper()
	var seq int64
	if err := s.db.QueryRowContext(ctx, "SELECT MAX(seq) FROM events").Scan(&seq); err != nil {
		t.Fatalf("read the newest event seq: %v", err)
	}
	return seq
}

func TestSessionEventsStreamFeedNamesCommitSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	feed := &sessionFeedRecorder{}
	s.OnSessionEvents(feed.record)

	// One batch appends events for s2 and s1 and writes unfinished text for s3: one call, with the batch's newest event
	// seq and the three sessions sorted.
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineTask{ID: "s2", TaskID: "t1"},
		EngineTask{ID: "s1", TaskID: "t2"},
		EngineText{ID: "s3", MessageID: "m1", Kind: "text", Text: "one"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine with events and text: %v", err)
	}
	newest := sessionFeedNewestSeq(ctx, t, s)
	if calls := feed.take(); len(calls) != 1 || calls[0].seq != newest ||
		!slices.Equal(calls[0].sessions, []string{"s1", "s2", "s3"}) {
		t.Errorf("hook calls after the batch = %v, want one call with seq %d and sessions [s1 s2 s3]", calls, newest)
	}

	// A write that only changes unfinished text calls the hook once with seq 0.
	if err := s.ApplyEngine(ctx, []EngineOp{
		EngineText{ID: "s3", MessageID: "m1", Kind: "text", Text: "two"},
	}, engineNowAt()); err != nil {
		t.Fatalf("ApplyEngine with text only: %v", err)
	}
	if calls := feed.take(); len(calls) != 1 || calls[0].seq != 0 || !slices.Equal(calls[0].sessions, []string{"s3"}) {
		t.Errorf("hook calls after a text-only write = %v, want one call with seq 0 and sessions [s3]", calls)
	}
}

func TestSessionEventsStreamFeedSilentWithoutAppend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openSyncStore(t)
	feed := &sessionFeedRecorder{}
	s.OnSessionEvents(feed.record)

	// An epic's create touches neither table, so the hook calls nothing.
	createEpic(t, s, fullEpic(claimEpic))
	if calls := feed.take(); len(calls) != 0 {
		t.Errorf("hook calls after a write that touches neither table = %v, want none", calls)
	}

	// A write that appends an event and then fails rolls back, so the hook calls nothing.
	now := time.Unix(1_700_000_000, 0)
	refused := errors.New("synthetic refusal")
	if err := s.write(ctx, func(queries *sqlcgen.Queries) error {
		if err := appendEvent(ctx, queries, "s1", EventSessionChanged, "{}", now); err != nil {
			return err
		}
		return refused
	}); !errors.Is(err, refused) {
		t.Fatalf("rolled back write = %v, want the refusal", err)
	}
	if calls := feed.take(); len(calls) != 0 {
		t.Errorf("hook calls after a rolled back append = %v, want none", calls)
	}

	// The hook is live: a write that appends events and commits calls it once, with the newest seq and its session.
	if err := s.SyncPane(ctx, "-L a", Pane{ID: "s1", Pane: "%1", Fields: Fields{State: "working"}}, now); err != nil {
		t.Fatalf("SyncPane: %v", err)
	}
	newest := sessionFeedNewestSeq(ctx, t, s)
	if calls := feed.take(); len(calls) != 1 || calls[0].seq != newest ||
		!slices.Equal(calls[0].sessions, []string{"s1"}) {
		t.Errorf("hook calls after a committed append = %v, want one call with seq %d and sessions [s1]", calls, newest)
	}
}
