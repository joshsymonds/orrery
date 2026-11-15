package store_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/joshsymonds/orrery/internal/store"
)

// openLimitStore opens a migrated store file and returns it with a reopen function. reopen closes the store and opens
// the same file again, returning the new store. The store open when the test ends is closed.
func openLimitStore(ctx context.Context, t *testing.T) (*store.Store, func() *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orrery.db")
	store.SeedMigrated(t, path)
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	current := s
	t.Cleanup(func() {
		if err := current.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	reopen := func() *store.Store {
		t.Helper()
		if err := current.Close(); err != nil {
			t.Fatalf("Close before reopen: %v", err)
		}
		reopened, openErr := store.Open(ctx, path)
		if openErr != nil {
			t.Fatalf("Open after close: %v", openErr)
		}
		current = reopened
		return reopened
	}
	return s, reopen
}

func TestPendingNoticeLimitSurvivesPutReopenAndNotices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, reopen := openLimitStore(ctx, t)

	limited := store.NoticeRow{
		ID: 4, Pane: "%3", Session: "claude-1", State: "waiting", Time: 1_700_000_000, Phase: "delayed",
		Due: 1_700_000_190_000, SourceKind: "limit", Source: []byte(`{"limit":"weekly"}`), Limit: "weekly",
	}
	plain := store.NoticeRow{
		ID: 5, Pane: "%4", Session: "claude-2", State: "waiting", Time: 1_700_000_010, Phase: "delayed",
		Due: 1_700_000_200_000, SourceKind: "session", Source: []byte(`{"event":"stop"}`),
	}
	wantNotices := func(stage string, want []store.NoticeRow) {
		t.Helper()
		got, err := s.Notices(ctx)
		if err != nil {
			t.Fatalf("Notices %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Notices %s: got %+v, want %+v", stage, got, want)
		}
	}

	for _, row := range []store.NoticeRow{limited, plain} {
		if err := s.PutNotice(ctx, row); err != nil {
			t.Fatalf("PutNotice %d: %v", row.ID, err)
		}
	}
	wantNotices("after puts", []store.NoticeRow{limited, plain})

	s = reopen()
	wantNotices("after reopen", []store.NoticeRow{limited, plain})

	// Putting the row again under its id replaces its limit as well as its other columns.
	limited.Limit = "five-hour"
	if err := s.PutNotice(ctx, limited); err != nil {
		t.Fatalf("PutNotice %d again: %v", limited.ID, err)
	}
	wantNotices("after limit replaced", []store.NoticeRow{limited, plain})

	s = reopen()
	wantNotices("after limit replaced and reopen", []store.NoticeRow{limited, plain})
}

func TestToldLimitsSurviveReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, reopen := openLimitStore(ctx, t)

	wantTold := func(stage string, want map[string][]string) {
		t.Helper()
		got, err := s.ToldLimits(ctx)
		if err != nil {
			t.Fatalf("ToldLimits %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ToldLimits %s: got %v, want %v", stage, got, want)
		}
	}

	wantTold("before any tell", map[string][]string{})

	// Steps are given out of order; each limit's told steps come back sorted. The same step under two limits is
	// told once for each.
	if err := s.TellLimit(ctx, "weekly", []string{"step-b", "step-a"}); err != nil {
		t.Fatalf("TellLimit weekly: %v", err)
	}
	if err := s.TellLimit(ctx, "five-hour", []string{"step-a"}); err != nil {
		t.Fatalf("TellLimit five-hour: %v", err)
	}
	want := map[string][]string{"weekly": {"step-a", "step-b"}, "five-hour": {"step-a"}}
	wantTold("after tells", want)

	s = reopen()
	wantTold("after reopen", want)
}

func TestTellLimitReplacesTheToldSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, reopen := openLimitStore(ctx, t)

	wantTold := func(stage string, want map[string][]string) {
		t.Helper()
		got, err := s.ToldLimits(ctx)
		if err != nil {
			t.Fatalf("ToldLimits %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ToldLimits %s: got %v, want %v", stage, got, want)
		}
	}

	for _, tell := range []struct {
		limit string
		steps []string
	}{
		{"weekly", []string{"step-a", "step-b", "step-c"}},
		{"five-hour", []string{"step-a"}},
	} {
		if err := s.TellLimit(ctx, tell.limit, tell.steps); err != nil {
			t.Fatalf("TellLimit %s: %v", tell.limit, err)
		}
	}

	// A smaller set replaces the larger one: step-a and step-c are no longer told for weekly, and five-hour is untouched.
	if err := s.TellLimit(ctx, "weekly", []string{"step-b"}); err != nil {
		t.Fatalf("TellLimit weekly smaller set: %v", err)
	}
	wantTold("after smaller set", map[string][]string{"weekly": {"step-b"}, "five-hour": {"step-a"}})

	s = reopen()
	wantTold("after smaller set and reopen", map[string][]string{"weekly": {"step-b"}, "five-hour": {"step-a"}})

	// A repeated step is told once.
	if err := s.TellLimit(ctx, "weekly", []string{"step-d", "step-d"}); err != nil {
		t.Fatalf("TellLimit weekly repeated step: %v", err)
	}
	wantTold("after repeated step", map[string][]string{"weekly": {"step-d"}, "five-hour": {"step-a"}})

	// An empty set leaves the limit untold, so it is absent from ToldLimits.
	if err := s.TellLimit(ctx, "weekly", nil); err != nil {
		t.Fatalf("TellLimit weekly empty set: %v", err)
	}
	wantTold("after empty set", map[string][]string{"five-hour": {"step-a"}})
}

func TestForgetLimitUntellsOnlyThatLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, reopen := openLimitStore(ctx, t)

	wantTold := func(stage string, want map[string][]string) {
		t.Helper()
		got, err := s.ToldLimits(ctx)
		if err != nil {
			t.Fatalf("ToldLimits %s: %v", stage, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ToldLimits %s: got %v, want %v", stage, got, want)
		}
	}

	if err := s.TellLimit(ctx, "weekly", []string{"step-a", "step-b"}); err != nil {
		t.Fatalf("TellLimit weekly: %v", err)
	}
	if err := s.TellLimit(ctx, "five-hour", []string{"step-a"}); err != nil {
		t.Fatalf("TellLimit five-hour: %v", err)
	}

	if err := s.ForgetLimit(ctx, "weekly"); err != nil {
		t.Fatalf("ForgetLimit weekly: %v", err)
	}
	wantTold("after forget", map[string][]string{"five-hour": {"step-a"}})

	s = reopen()
	wantTold("after forget and reopen", map[string][]string{"five-hour": {"step-a"}})

	// A limit that is not told is not an error.
	if err := s.ForgetLimit(ctx, "monthly"); err != nil {
		t.Fatalf("ForgetLimit of untold limit: got %v, want nil", err)
	}
	wantTold("after forgetting untold limit", map[string][]string{"five-hour": {"step-a"}})
}
