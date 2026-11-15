package route_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/route"
	"github.com/joshsymonds/orrery/internal/store"
)

// deadlineRows is a store that records how long each row write was given before its context's deadline.
type deadlineRows struct {
	*flakyRows
	mu    sync.Mutex
	given []time.Duration
}

func (s *deadlineRows) PutNotice(ctx context.Context, row store.NoticeRow) error {
	s.record(ctx)
	return s.flakyRows.PutNotice(ctx, row)
}

func (s *deadlineRows) record(ctx context.Context) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.given = append(s.given, time.Until(deadline))
}

func (s *deadlineRows) longest() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var longest time.Duration
	for _, given := range s.given {
		longest = max(longest, given)
	}
	return longest
}

// putUnder attaches a Router built with deadline to a recording store, routes one sourced notice, and returns the
// longest time a row write was given.
func putUnder(t *testing.T, deadline time.Duration) time.Duration {
	t.Helper()
	ctx := t.Context()
	s := &deadlineRows{flakyRows: newFlakyRows(t)}
	f := newFake()
	f.setDesk(true, clockStart)
	r := route.New(route.Config{
		Host: testHost, Now: f.Now, After: f.After, Desk: f, Phone: f, Subscribed: f.Subscribed, Log: f,
		StoreDeadline: deadline,
	})
	t.Cleanup(r.Close)
	r.Attach(ctx, s)
	r.Notify(ctx, sourced(f.notice(testPane, stateWaiting, false)))
	r.Wait()
	return s.longest()
}

func TestRouterRowWritesTakeTheStoreDeadlineItsConfigNames(t *testing.T) {
	t.Parallel()
	if given := putUnder(t, time.Hour); given < 30*time.Minute {
		t.Fatalf("row write given %v with a one-hour StoreDeadline, want close to an hour", given)
	}
}

func TestRouterRowWritesKeepTheDefaultDeadlineWithoutOne(t *testing.T) {
	t.Parallel()
	if given := putUnder(t, 0); given > time.Second {
		t.Fatalf("row write given %v with no StoreDeadline, want the 200 ms default", given)
	}
}
