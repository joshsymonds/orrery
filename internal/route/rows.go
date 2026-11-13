package route

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeDeadline bounds each store call the Router makes. A call that outlives it is late, and is retried.
const storeDeadline = 200 * time.Millisecond

// The phases a row records: the wait its notice is in.
const (
	phaseDeciding = "deciding"
	phaseHeld     = "held"
	phaseDelayed  = "delayed"
	phaseDesk     = "desk"
)

// Rows is the store the Router keeps one row per sourced pending notice in. *store.Store satisfies it.
type Rows interface {
	PutNotice(context.Context, store.NoticeRow) error
	DeleteNotice(context.Context, int64) error
	Notices(context.Context) ([]store.NoticeRow, error)
	NoticeIDFloor(context.Context) (int64, error)
}

// Attach keeps rows in rows from now on. Each notice with a source that is already pending gets its row at once,
// with an id above the store's id floor. A failed floor read is logged and leaves the Router without a store.
func (r *Router) Attach(ctx context.Context, rows Rows) {
	floorCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeDeadline)
	floor, err := rows.NoticeIDFloor(floorCtx)
	cancel()
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "read notice id floor", "error", err)
		return
	}
	// Every row write runs on ctx without its cancel, so a stopping caller never cuts one short.
	base := context.WithoutCancel(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putRow = func(row store.NoticeRow) error {
		return bounded(base, func(ctx context.Context) error { return rows.PutNotice(ctx, row) })
	}
	r.deleteRow = func(id int64) error {
		return bounded(base, func(ctx context.Context) error { return rows.DeleteNotice(ctx, id) })
	}
	r.rowID = floor
	for _, ent := range r.sortedPendingLocked() {
		r.rowLocked(ent)
	}
}

// rowLocked writes ent's row. A notice without a source, and a Router without a store, have no row. The first write
// of a row gives it the next id. The failed writes are retried first, and ent's write stores its whole current row,
// which supersedes any failed write of ent. The caller holds r.mu.
func (r *Router) rowLocked(ent *entry) {
	if r.putRow == nil || ent.n.Source.Kind == "" {
		return
	}
	if ent.row == 0 {
		r.rowID++
		ent.row = r.rowID
	}
	delete(r.rowPuts, ent.row)
	r.retryLocked()
	r.putLocked(ent)
}

// rowStoredLocked reports whether ent's current row is in the store: it has a row and its last write did not fail.
// The caller holds r.mu.
func (r *Router) rowStoredLocked(ent *entry) bool {
	_, failed := r.rowPuts[ent.row]
	return ent.row != 0 && !failed
}

// deleteRowLocked deletes ent's row. A failed write of that row is dropped, so no retry stores it again. The caller
// holds r.mu.
func (r *Router) deleteRowLocked(ent *entry) {
	delete(r.rowPuts, ent.row)
	r.retryLocked()
	r.deleteLocked(ent.row, ent.n.Key)
}

// retryLocked retries the failed row writes, deletes first. A retried put stores its entry's current row. The caller
// holds r.mu.
func (r *Router) retryLocked() {
	for _, id := range slices.Sorted(maps.Keys(r.rowDeletes)) {
		r.deleteLocked(id, r.rowDeletes[id])
	}
	for _, id := range slices.Sorted(maps.Keys(r.rowPuts)) {
		r.putLocked(r.rowPuts[id])
	}
}

// putLocked writes ent's whole row. A failed or late write is logged and kept for a retry. The caller holds r.mu.
func (r *Router) putLocked(ent *entry) {
	if err := r.putRow(rowOf(ent)); err != nil {
		r.rowPuts[ent.row] = ent
		r.cfg.Logger.Error("write notice row", "key", ent.n.Key, "error", err)
		return
	}
	delete(r.rowPuts, ent.row)
}

// deleteLocked deletes the row with id, which belonged to the notice at key. A failed or late delete is logged and
// kept for a retry. The caller holds r.mu.
func (r *Router) deleteLocked(id int64, key notice.Key) {
	if err := r.deleteRow(id); err != nil {
		r.rowDeletes[id] = key
		r.cfg.Logger.Error("delete notice row", "key", key, "error", err)
		return
	}
	delete(r.rowDeletes, id)
}

// bounded runs one store write on parent under storeDeadline and returns its error. A write that outlives the
// deadline reads as failed, even when it succeeded, because it was late.
func bounded(parent context.Context, write func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, storeDeadline)
	defer cancel()
	if err := write(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write outlived its %v deadline: %w", storeDeadline, err)
	}
	return nil
}

// dueIn is the unix millisecond d from now.
func (r *Router) dueIn(d time.Duration) int64 {
	return r.cfg.Now().Add(d).UnixMilli()
}

// rowOf is the whole row ent keeps.
func rowOf(ent *entry) store.NoticeRow {
	return store.NoticeRow{
		ID:          ent.row,
		Pane:        ent.n.Key.Pane,
		Session:     ent.n.Key.Session,
		State:       ent.n.State,
		Time:        ent.at,
		Phase:       ent.phase,
		Due:         ent.due,
		DelayServed: ent.delayed,
		WatchLogged: ent.watchLogged,
		SourceKind:  ent.n.Source.Kind,
		Source:      ent.n.Source.Data,
	}
}
