package route

import (
	"context"
	"errors"
	"fmt"

	"github.com/joshsymonds/orrery/internal/deskstream"
	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/store"
)

// ErrNoHistory is what ReadAll and History return while no store is attached to the Router.
var ErrNoHistory = errors.New("no notice history store attached")

// attachHistoryLocked keeps rows as the notice history store and tracks as unread each key that has an unread history
// row, in place of the keys tracked before. A failed read is logged and tracks none. The caller holds r.mu.
func (r *Router) attachHistoryLocked(ctx context.Context, rows Rows) {
	r.history = rows
	r.unread = make(map[notice.Key]bool)
	readCtx, done := r.armed(ctx)
	keys, err := rows.UnreadNoticeHistoryKeys(readCtx)
	done()
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "read unread notice history keys", "error", err)
		return
	}
	for _, key := range keys {
		// store.NoticeHistoryKey and notice.Key have the same fields, so the conversion is exact.
		r.unread[notice.Key(key)] = true
	}
}

// recordHistoryLocked writes the history row of dn, the desk notice ent delivered, timed at the Router's clock. It does
// nothing without a store. The key joins the unread set whether the write succeeds or not, since a late write reads as
// failed though it may have landed. A failed or late write is logged and not retried. The caller holds r.mu.
func (r *Router) recordHistoryLocked(ctx context.Context, ent *entry, dn deskstream.Notice) {
	if r.history == nil {
		return
	}
	row := store.NoticeHistoryRow{
		Key:      store.NoticeHistoryKey(ent.n.Key),
		State:    dn.State,
		Title:    dn.Title,
		Body:     dn.Body,
		Block:    dn.Block,
		BlockRef: dn.BlockRef,
		Time:     r.cfg.Now().Unix(),
	}
	err := r.bounded(context.WithoutCancel(ctx), func(ctx context.Context) error {
		return r.history.RecordNoticeHistory(ctx, row)
	})
	r.unread[ent.n.Key] = true
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "write notice history", "key", ent.n.Key, "error", err)
	}
}

// markHistoryLocked marks read the rows of key's history at or before seen, when key is in the unread set. The store
// reports whether key still has an unread row, and key leaves the set once it has none. A failed or late mark is logged
// and key stays in the set. The caller holds r.mu.
func (r *Router) markHistoryLocked(ctx context.Context, key notice.Key, seen int64) {
	if !r.unread[key] {
		return
	}
	var unread bool
	err := r.bounded(context.WithoutCancel(ctx), func(ctx context.Context) error {
		var err error
		unread, err = r.history.MarkNoticeHistoryRead(ctx, store.NoticeHistoryKey(key), seen)
		if err != nil {
			return fmt.Errorf("seen at %d: %w", seen, err)
		}
		return nil
	})
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "mark notice history read", "key", key, "error", err)
		return
	}
	if !unread {
		delete(r.unread, key)
	}
}

// ReadAll marks every row of the notice history read, and then tracks no key as unread. It fails with ErrNoHistory
// while no store is attached, and with the store's error when the write fails, leaving the unread keys as they were.
func (r *Router) ReadAll(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.history == nil {
		return ErrNoHistory
	}
	err := r.bounded(ctx, func(ctx context.Context) error { return r.history.ReadAllNoticeHistory(ctx) })
	if err != nil {
		return err
	}
	clear(r.unread)
	return nil
}

// History returns the rows of the notice history whose time is at or after since, newest first. The read runs under
// the store-call bound and without the Router's lock. It fails with ErrNoHistory while no store is attached.
func (r *Router) History(ctx context.Context, since int64) ([]store.NoticeHistoryRow, error) {
	r.mu.Lock()
	history := r.history
	r.mu.Unlock()
	if history == nil {
		return nil, ErrNoHistory
	}
	readCtx, done := r.armed(ctx)
	rows, err := history.NoticeHistory(readCtx, since)
	done()
	if err != nil {
		return nil, fmt.Errorf("read notice history: %w", err)
	}
	return rows, nil
}
