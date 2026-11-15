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

func (r *refusingRows) DeletePushed(ctx context.Context, pane, session string) error {
	r.mu.Lock()
	refuse := pane == r.refuse && !r.refusedDelete
	r.refusedDelete = r.refusedDelete || refuse
	r.mu.Unlock()
	if refuse {
		return errRefused
	}
	return r.Store.DeletePushed(ctx, pane, session)
}

// TestRefusedPushedRowIsStoredAtTheNextWrite has the store refuse the row write of one key's notice. The next row
// write the store takes, for another key, stores the refused row too, so a restart still knows the push was sent.
func TestRefusedPushedRowIsStoredAtTheNextWrite(t *testing.T) {
	t.Parallel()
	const at = 1700000000
	ctx := context.Background()
	s := openStore(ctx, t, filepath.Join(t.TempDir(), "orrery.db"))
	rows := &refusingRows{Store: s, refuse: "%3"}
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
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
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
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
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
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
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
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
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
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
	n := notice.New("host-a", &fakeSender{}, (&clock{at: time.Unix(at, 0)}).now, slog.New(slog.DiscardHandler))
	n.Attach(ctx, rows)

	n.Notify(ctx, notice.Key{Pane: "%3"}, build(push.Notification{Summary: "done"}), nil)
	n.Seen(ctx, notice.Key{Pane: "%3"}, at)
	n.Wait()
	wantRows(t, storedPushes(ctx, t, s), store.PushedRow{Pane: "%3", Time: at})

	n.Close(ctx)
	wantRows(t, storedPushes(ctx, t, s))
}
