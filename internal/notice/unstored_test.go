package notice_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// errRefused is the error a refusingRows gives for a write it refuses, as a busy store answers past its bound.
var errRefused = errors.New("synthetic store refusal")

// refusingRows stores through inner, but refuses the first PutPushed and the first DeletePushed of the pane refuse
// names.
type refusingRows struct {
	*store.Store
	refuse string

	mu            sync.Mutex
	refusedPut    bool
	refusedDelete bool
}

func (r *refusingRows) PutPushed(ctx context.Context, row store.PushedRow) error {
	r.mu.Lock()
	refuse := row.Pane == r.refuse && !r.refusedPut
	r.refusedPut = r.refusedPut || refuse
	r.mu.Unlock()
	if refuse {
		return errRefused
	}
	return r.Store.PutPushed(ctx, row)
}

func (r *refusingRows) DeletePushed(ctx context.Context, host, pane, session string) error {
	r.mu.Lock()
	refuse := pane == r.refuse && !r.refusedDelete
	r.refusedDelete = r.refusedDelete || refuse
	r.mu.Unlock()
	if refuse {
		return errRefused
	}
	return r.Store.DeletePushed(ctx, host, pane, session)
}

// TestRefusedPushedRowIsStoredAtTheNextWrite has the store refuse the row write of one key's notice. The next row
// write the store takes, for another key, stores the refused row too, so a restart still knows the push was sent.
func TestRefusedPushedRowIsStoredAtTheNextWrite(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s))

	n.Notify(ctx, notice.Key{Pane: "%4"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s),
		store.PushedRow{Pane: "%3", Time: at}, store.PushedRow{Pane: "%4", Time: at})
}

// TestRefusedPushedDeleteIsStoredAtTheNextWrite has the store refuse the delete of one key's row when the key is seen.
// The next row write the store takes deletes it, so a restart does not clear the push again.
func TestRefusedPushedDeleteIsStoredAtTheNextWrite(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	rows.refusedPut = true // only the delete is refused
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Seen(ctx, notice.Key{Pane: "%3"}, at)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})

	n.Notify(ctx, notice.Key{Pane: "%4"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%4", Time: at})
}

// TestRefusedPushedRowIsStoredAtTheNextRetry has the store refuse one key's row write, with no later row write to
// retry it. The next Retry, which orreryd makes on each tidy pass, stores it once the store takes writes again.
func TestRefusedPushedRowIsStoredAtTheNextRetry(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s))

	n.Retry(ctx)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})
}

// TestRefusedPushedDeleteIsStoredAtTheNextRetry has the store refuse the delete of one key's row when the key is seen.
// The next Retry deletes it once the store takes writes again.
func TestRefusedPushedDeleteIsStoredAtTheNextRetry(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	rows.refusedPut = true // only the delete is refused
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Seen(ctx, notice.Key{Pane: "%3"}, at)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})

	n.Retry(ctx)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s))
}

// TestRefusedPushedRowIsStoredAtClose has the store refuse one key's row write, with no later row write or Retry. Close
// stores it before it returns, so a store closed after Close keeps the push.
func TestRefusedPushedRowIsStoredAtClose(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s))

	n.Close(ctx)
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})
}

// TestRefusedPushedDeleteIsStoredAtClose has the store refuse the delete of one key's row when the key is seen. Close
// deletes it before it returns, so the next orreryd does not clear the push again.
func TestRefusedPushedDeleteIsStoredAtClose(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	rows.refusedPut = true // only the delete is refused
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Seen(ctx, notice.Key{Pane: "%3"}, at)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})

	n.Close(ctx)
	wantRows(t, storedPushes(ctx, t, s))
}

// testStoreDeadline bounds the store calls of a test's Notices: longer than any test runs, so a store call that is only
// slow, as one on a loaded machine is, never reads as late. The test that forces a late call holds it until its own
// shorter deadline ends.
const testStoreDeadline = time.Hour

// newTestNotices returns notice.New's Notices for host-a with testStoreDeadline as its store bound.
func newTestNotices(sender notice.Sender, now func() time.Time, logger *slog.Logger) *notice.Notices {
	return notice.New("host-a", sender, now, logger, notice.WithStoreDeadline(testStoreDeadline))
}

// lateRows stores through inner, but answers the first lates PutPushed calls for the pane late names as a call that
// outlived its deadline does, storing nothing.
type lateRows struct {
	*store.Store
	late string

	mu    sync.Mutex
	lates int
}

func (r *lateRows) PutPushed(ctx context.Context, row store.PushedRow) error {
	r.mu.Lock()
	late := row.Pane == r.late && r.lates > 0
	if late {
		r.lates--
	}
	r.mu.Unlock()
	if late {
		return context.DeadlineExceeded
	}
	return r.Store.PutPushed(ctx, row)
}

// TestLateRetryOfARefusedRowIsStoredAtTheWriteAfter forces the order the gate's loaded run hit: %3's row write is
// refused, and the retry %4's write starts for it is late too, as a store call on a loaded machine can be. The store then
// holds %4 alone, as the failed run read it. The late retry must leave %3 unstored, not dropped, so %5's write, the next
// the store takes, stores it.
func TestLateRetryOfARefusedRowIsStoredAtTheWriteAfter(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &lateRows{Store: s, late: "%3", lates: 2}
	n := newTestNotices(&fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s))

	n.Notify(ctx, notice.Key{Pane: "%4"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%4", Time: at})

	n.Notify(ctx, notice.Key{Pane: "%5"}, build(push.Notification{Summary: "done"}), nil)
	n.Wait()
	wantRows(
		t,
		storedPushes(ctx, t, s),
		store.PushedRow{
			Pane: "%3",
			Time: at,
		},
		store.PushedRow{Pane: "%4", Time: at},
		store.PushedRow{Pane: "%5", Time: at},
	)
}
