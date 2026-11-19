package route

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeDeadline is the bound each store call the Router makes is armed with. A write whose bound fires before it
// returns is late, and is retried.
const storeDeadline = 200 * time.Millisecond

// RowRetry is how long after a row write fails the Router retries the failed writes, and again after each retry that
// still fails, until they land or Close runs. A row delete the publish report makes is one: left failed until a later
// row write, a sent notice's row would outlive a restart in between and be sent to the phone again.
const RowRetry = 5 * time.Second

// The phases a row records: the wait its notice is in, or sending once its push was handed to the phone.
const (
	phaseDeciding = "deciding"
	phaseHeld     = "held"
	phaseDelayed  = "delayed"
	phaseDesk     = "desk"
	phaseSending  = "sending"
)

// reasonResent is the reason the line of a restored sending row's push, sent again at Attach, gives.
const reasonResent = "resent after restart"

// Rows is the store the Router keeps one row per sourced pending notice in, and the notice history it keeps one row per
// delivered notice in. *store.Store satisfies it.
type Rows interface {
	PutNotice(context.Context, store.NoticeRow) error
	DeleteNotice(context.Context, int64) error
	Notices(context.Context) ([]store.NoticeRow, error)
	NoticeIDFloor(context.Context) (int64, error)
	RecordNoticeHistory(context.Context, store.NoticeHistoryRow) error
	MarkNoticeHistoryRead(context.Context, store.NoticeHistoryKey, int64) (bool, error)
	ReadAllNoticeHistory(context.Context) error
	NoticeHistory(context.Context, int64) ([]store.NoticeHistoryRow, error)
	UnreadNoticeHistoryKeys(context.Context) ([]store.NoticeHistoryKey, error)
}

// Attach keeps rows in rows from now on, as the store of the notice history too. Each notice with a source that is
// already pending gets its row at once, with an id above the store's id floor. Then each stored row is restored in
// ascending id: a row whose key saw a Notify before Attach, or that a later row for its key replaces, ends `changed`; a
// row that cannot be restored ends `not restored`; a sending row has its push sent to the phone again; and the others
// resume from their phase. The keys with an unread history row are tracked as unread. A failed floor read is logged and
// leaves the Router without a store, so no history is kept and none is marked read. A failed read of the rows is logged
// and restores nothing. A failed read of the unread keys is logged and tracks none as unread.
func (r *Router) Attach(ctx context.Context, rows Rows) {
	floorCtx, done := r.armed(context.WithoutCancel(ctx))
	floor, err := rows.NoticeIDFloor(floorCtx)
	done()
	// Every row write and restore runs on ctx without its cancel, so a stopping caller never cuts one short.
	base := context.WithoutCancel(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	beforeAttach := r.beforeAttach
	r.beforeAttach = nil
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "read notice id floor", "error", err)
		return
	}
	r.putRow = func(row store.NoticeRow) error {
		return r.bounded(base, func(ctx context.Context) error { return rows.PutNotice(ctx, row) })
	}
	r.deleteRow = func(id int64) error {
		return r.bounded(base, func(ctx context.Context) error { return rows.DeleteNotice(ctx, id) })
	}
	r.attachHistoryLocked(base, rows)
	r.rowID = floor
	for _, ent := range r.sortedPendingLocked() {
		r.rowLocked(ent)
	}
	r.restoreLocked(base, rows, floor, beforeAttach)
}

// restoreLocked restores the stored rows, in ascending id. A row above floor is one this Attach wrote for a notice
// already pending, so it is not restored. Of the rows of one key, only the highest id resumes: the others end
// `changed`, as does every row of a key that saw a Notify before Attach. The caller holds r.mu.
func (r *Router) restoreLocked(ctx context.Context, rows Rows, floor int64, beforeAttach map[notice.Key]bool) {
	readCtx, done := r.armed(ctx)
	stored, err := rows.Notices(readCtx)
	done()
	if err != nil {
		r.cfg.Logger.ErrorContext(ctx, "read notices", "error", err)
		return
	}
	stored = slices.DeleteFunc(stored, func(row store.NoticeRow) bool { return row.ID > floor })
	newest := make(map[notice.Key]int64, len(stored))
	for _, row := range stored {
		newest[keyOf(row)] = row.ID
	}
	for _, row := range stored {
		key := keyOf(row)
		if beforeAttach[key] || newest[key] != row.ID {
			r.endLocked(&entry{n: Notice{Key: key, State: row.State}, row: row.ID}, None, reasonChanged)
			continue
		}
		r.restoreRowLocked(ctx, row)
	}
}

// restoreRowLocked resumes the notice that row holds, or ends the row when the notice cannot be rebuilt: `changed`
// or `seen` when the Rebuild says so, `not restored` otherwise. The caller holds r.mu.
func (r *Router) restoreRowLocked(ctx context.Context, row store.NoticeRow) {
	ent := storedEntry(row)
	if err := r.rebuildLocked(ctx, ent); err != nil {
		r.endLocked(ent, None, unrestoredReason(err))
		return
	}
	r.seq++
	ent.seq = r.seq
	r.pending[ent.n.Key] = ent
	r.resumeLocked(ctx, ent)
}

// unrestoredReason is the reason a row ends with when its notice fails to rebuild with err.
func unrestoredReason(err error) string {
	switch {
	case errors.Is(err, ErrChanged):
		return reasonChanged
	case errors.Is(err, ErrSeen):
		return reasonSeen
	default:
		return reasonNotRestored + ": " + err.Error()
	}
}

// storedEntry is the entry that row describes. Its notice has no Watching, Judge, Desk or Push until rebuildLocked
// gives them.
func storedEntry(row store.NoticeRow) *entry {
	return &entry{
		n: Notice{
			Key:    keyOf(row),
			State:  row.State,
			Source: Source{Kind: row.SourceKind, Data: row.Source},
		},
		at:          row.Time,
		phase:       row.Phase,
		due:         row.Due,
		row:         row.ID,
		silent:      row.Silent,
		watchLogged: row.WatchLogged,
		delayed:     row.DelayServed,
	}
}

// rebuildLocked gives ent the Watching, Judge, Desk and Push of the notice its source kind rebuilds from ent's source
// data. It fails when the kind has no Rebuild, when the Rebuild fails, or when ent's phase is not one a row records.
// The caller holds r.mu.
func (r *Router) rebuildLocked(ctx context.Context, ent *entry) error {
	rebuild, ok := r.cfg.Rebuild[ent.n.Source.Kind]
	if !ok {
		return fmt.Errorf("unknown source kind %q", ent.n.Source.Kind)
	}
	built, err := rebuild(ctx, ent.n.Source.Data, ent.at)
	if err != nil {
		return err
	}
	switch ent.phase {
	case phaseDeciding, phaseHeld, phaseDelayed, phaseDesk, phaseSending:
	default:
		return fmt.Errorf("unknown phase %q", ent.phase)
	}
	ent.n.Watching, ent.n.Judge, ent.n.Desk, ent.n.Push = built.Watching, built.Judge, built.Desk, built.Push
	return nil
}

// resumeLocked resumes the restored ent from its phase. A deciding or held notice decides at once. A delayed notice
// decides, and a desk notice escalates, when its due passes. A desk notice is shown on Pages again at once. A sending
// notice is sent to the phone again at once. The caller holds r.mu.
func (r *Router) resumeLocked(ctx context.Context, ent *entry) {
	switch ent.phase {
	case phaseDeciding, phaseHeld:
		r.startLocked(func() { r.decide(ctx, ent) })
	case phaseDelayed:
		r.dueLocked(ent, func() { r.decide(ctx, ent) })
	case phaseDesk:
		r.dueLocked(ent, func() { r.escalate(ctx, ent) })
		r.startLocked(func() { r.showRestored(ctx, ent) })
	case phaseSending:
		r.resendLocked(ctx, ent)
	}
}

// showRestored builds the desk notice of the restored desk ent outside the lock, as route does, and shows it on Pages
// until ent ends. It is not posted to the desk link again, and a failed build shows nothing.
func (r *Router) showRestored(ctx context.Context, ent *entry) {
	dn, built := r.buildDesk(ctx, ent)
	if !built {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent.ended || r.cfg.Pages == nil {
		return
	}
	r.cfg.Pages.Show(ent.n.Key, dn)
	ent.shown = true
}

// resendLocked hands the push of the restored sending ent to the phone again, silent when its row is silent. The row
// stays until the publish reports. Nothing is posted to the desk and no timer is armed, and no subscription is checked,
// because the push was handed off before the stop. The caller holds r.mu.
func (r *Router) resendLocked(ctx context.Context, ent *entry) {
	build := ent.n.Push
	if ent.silent {
		build = silent(build)
	}
	r.handOffLocked(ctx, ent, build, ent.silent, Phone, reasonResent)
}

// dueLocked runs f on ent when ent's due passes, or at once when it has already passed. The caller holds r.mu.
func (r *Router) dueLocked(ent *entry, run func()) {
	wait := time.UnixMilli(ent.due).Sub(r.cfg.Now())
	if wait <= 0 {
		r.startLocked(run)
		return
	}
	r.laterLocked(ent, wait, run)
}

// keyOf is the key of the notice row describes.
func keyOf(row store.NoticeRow) notice.Key {
	return notice.Key{Pane: row.Pane, Session: row.Session, Epic: row.Epic, Limit: row.Limit}
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

// retryLocked retries the failed row writes, deletes first, in ascending id. A retried put stores its entry's current
// row. The pass stops at the first retry that fails: the store is still failing, so the rest wait for a later write or
// Close. The caller holds r.mu.
func (r *Router) retryLocked() {
	for _, id := range slices.Sorted(maps.Keys(r.rowDeletes)) {
		r.deleteLocked(id, r.rowDeletes[id])
		if _, failed := r.rowDeletes[id]; failed {
			return
		}
	}
	for _, id := range slices.Sorted(maps.Keys(r.rowPuts)) {
		r.putLocked(r.rowPuts[id])
		if _, failed := r.rowPuts[id]; failed {
			return
		}
	}
}

// putLocked writes ent's whole row. A failed or late write is logged and kept for a retry. The caller holds r.mu.
func (r *Router) putLocked(ent *entry) {
	if err := r.putRow(rowOf(ent)); err != nil {
		r.rowPuts[ent.row] = ent
		r.armRetryLocked()
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
		r.armRetryLocked()
		r.cfg.Logger.Error("delete notice row", "key", key, "error", err)
		return
	}
	delete(r.rowDeletes, id)
}

// armRetryLocked arms the retry of the failed row writes RowRetry from now, unless one is armed or Close has begun. The
// retry runs retryLocked, which arms the next retry when a write still fails. The caller holds r.mu.
func (r *Router) armRetryLocked() {
	if r.rowRetry != nil || r.closed {
		return
	}
	r.rowRetry = r.cfg.After(RowRetry, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.rowRetry = nil
		if !r.closed {
			r.retryLocked()
		}
	})
}

// bounded runs one store write on parent under the store-call bound and returns its error. A write whose bound fires
// before it returns reads as failed, even when it succeeded, because it was late.
func (r *Router) bounded(parent context.Context, write func(context.Context) error) error {
	ctx, done := r.armed(parent)
	err := write(ctx)
	if done() {
		return fmt.Errorf("write outlived its %v deadline: %w", storeDeadline, context.DeadlineExceeded)
	}
	return err
}

// armed returns parent under the store-call bound: the bound cancels the returned context when it fires. The returned
// func ends the bound, cancels the context, and reports whether the bound fired first. A read ignores that report: a
// read the bound cancels fails as the store reports it.
func (r *Router) armed(parent context.Context) (context.Context, func() bool) {
	ctx, cancel := context.WithCancel(parent)
	stop := r.cfg.StoreAfter(storeDeadline, cancel)
	return ctx, func() bool {
		fired := !stop()
		cancel()
		return fired
	}
}

// dueIn is the unix millisecond d from now.
func (r *Router) dueIn(d time.Duration) int64 {
	return r.cfg.Now().Add(d).UnixMilli()
}

// rowOf is the whole row ent keeps. It carries the told mark of ent's notice, so the write that stores the row marks
// it told too.
func rowOf(ent *entry) store.NoticeRow {
	return store.NoticeRow{
		ID:          ent.row,
		Pane:        ent.n.Key.Pane,
		Session:     ent.n.Key.Session,
		Epic:        ent.n.Key.Epic,
		Limit:       ent.n.Key.Limit,
		State:       ent.n.State,
		Time:        ent.at,
		Phase:       ent.phase,
		Due:         ent.due,
		DelayServed: ent.delayed,
		WatchLogged: ent.watchLogged,
		SourceKind:  ent.n.Source.Kind,
		Source:      ent.n.Source.Data,
		Silent:      ent.silent,
		Told:        ent.n.Told,
	}
}
