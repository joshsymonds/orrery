// Package notice records the Web Push notices orreryd sent and sends a clear when a pane or session is seen.
package notice

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeDeadline bounds each store call a Notices makes. A call that outlives it reads as failed.
const storeDeadline = 200 * time.Millisecond

// Key names the pane, session, epic, usage limit or alert a notice belongs to. Exactly one of Pane, Session, Epic,
// Limit and Alert is set.
type Key struct{ Pane, Session, Epic, Limit, Alert string }

// Sender posts notices and clears. *push.Publisher satisfies it.
type Sender interface {
	Publish(ctx context.Context, note push.Notification) error
	PublishClear(ctx context.Context, c push.Clear) error
}

// Rows is the store a Notices keeps each key's last push in. *store.Store satisfies it.
type Rows interface {
	PutPushed(context.Context, store.PushedRow) error
	DeletePushed(ctx context.Context, pane, session string) error
	DeletePushedEpic(ctx context.Context, epic string) error
	DeletePushedLimit(ctx context.Context, limit string) error
	DeletePushedAlert(ctx context.Context, alert string) error
	Pushed(context.Context) ([]store.PushedRow, error)
}

// Notices is the record of notices sent on one host. Sends for one key run one at a time in call order, so a
// clear never precedes the notice it clears. Sends for different keys run independently.
type Notices struct {
	host   string
	sender Sender
	now    func() time.Time
	logger *slog.Logger

	mu      sync.Mutex
	noticed map[Key]int64
	// attached is set once Attach has merged its rows, or been called with nil rows. Until then the window before
	// Attach is open: Notify records pushedBeforeAttach, and Seen for a key with no notice in memory keeps keptSeens.
	attached           bool
	pushedBeforeAttach map[Key]bool
	keptSeens          map[Key]int64
	rows               Rows
	tails              map[Key]chan struct{}
	sends              sync.WaitGroup
	// unstored holds, for each key whose last row write the store refused or answered late, the write to store again:
	// a later write of the same key replaces it, and a write the store takes removes it. retrying marks the keys whose
	// retry is queued.
	unstored map[Key]rowWrite
	retrying map[Key]bool
	// deadline bounds each store call, storeDeadline unless a test sets another.
	deadline time.Duration
}

// rowWrite is one row write of a key: put writes its row with time at, and a delete removes it.
type rowWrite struct {
	put bool
	at  int64
}

// New returns a Notices for host that posts through sender, reads the time from now, and logs with logger.
func New(host string, sender Sender, now func() time.Time, logger *slog.Logger) *Notices {
	return &Notices{
		host:               host,
		sender:             sender,
		now:                now,
		logger:             logger,
		noticed:            make(map[Key]int64),
		pushedBeforeAttach: make(map[Key]bool),
		keptSeens:          make(map[Key]int64),
		tails:              make(map[Key]chan struct{}),
		unstored:           make(map[Key]rowWrite),
		retrying:           make(map[Key]bool),
		deadline:           storeDeadline,
	}
}

// Notify records the notice time for key and returns. It then sends build's notification in the background. With a
// store attached, the send first writes key's row with the notice time. The send ignores ctx's cancellation. A
// non-nil sent runs once when the send finishes: after Publish returns, with or without an error, or after build
// fails. It runs on the send's goroutine with n.mu not held, before Wait counts the send finished and before the next
// send queued for key starts.
func (n *Notices) Notify(
	ctx context.Context,
	key Key,
	build func(context.Context) (push.Notification, error),
	sent func(),
) {
	at := n.now().Unix()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.noticed[key] = at
	if !n.attached {
		n.pushedBeforeAttach[key] = true
	}
	rows := n.rows
	n.enqueueLocked(ctx, key, func(ctx context.Context) {
		if sent != nil {
			defer sent()
		}
		if rows != nil {
			n.putRow(ctx, rows, key, at)
		}
		note, err := build(ctx)
		if err != nil {
			n.logger.ErrorContext(ctx, "build push notice", "error", err)
			return
		}
		note.Time = at
		note.Host = n.host
		note.Pane = key.Pane
		note.Session = key.Session
		note.Epic = key.Epic
		note.Limit = key.Limit
		note.Alert = key.Alert
		if sendErr := n.sender.Publish(ctx, note); sendErr != nil {
			n.logger.ErrorContext(ctx, "publish push notice", "error", push.RedactEndpoints(sendErr))
		}
	})
}

// Seen clears key's notice when it was recorded at or before seen, and forgets it. Otherwise it does nothing. Before
// Attach, the highest seen for a key with no notice in memory is kept, and Attach decides what it clears. The clear
// is sent in the background after any send already queued for key. With a store attached, the clear then deletes
// key's row.
func (n *Notices) Seen(ctx context.Context, key Key, seen int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	at, ok := n.noticed[key]
	if !ok {
		if kept, held := n.keptSeens[key]; !n.attached && (!held || seen > kept) {
			n.keptSeens[key] = seen
		}
		return
	}
	if at > seen {
		return
	}
	delete(n.noticed, key)
	n.enqueueLocked(ctx, key, n.clearSend(key, seen, n.rows))
}

// Attach keeps each key's last push in rows from now on. It reads the pushes rows holds without n.mu held. A failed
// read is logged and leaves no stored pushes to reconcile. Then it merges under n.mu, which closes the window before
// Attach: a key pushed in that window keeps its memory time in rows, and a kept seen clears the stored push it covers.
// A nil rows keeps nothing from then on.
func (n *Notices) Attach(ctx context.Context, rows Rows) {
	var stored []store.PushedRow
	if rows != nil {
		stored = n.readRows(ctx, rows)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.mergeLocked(ctx, rows, stored)
}

// readRows returns the pushes rows holds. A read that fails or outlives its deadline is logged and returns none.
func (n *Notices) readRows(ctx context.Context, rows Rows) []store.PushedRow {
	var stored []store.PushedRow
	err := n.bounded(context.WithoutCancel(ctx), func(ctx context.Context) error {
		var readErr error
		stored, readErr = rows.Pushed(ctx)
		if readErr != nil {
			return fmt.Errorf("read store: %w", readErr)
		}
		return nil
	})
	if err != nil {
		n.logger.ErrorContext(ctx, "read pushed notices", "error", err)
		return nil
	}
	return stored
}

// mergeLocked closes the window before Attach and reconciles stored with it. A key pushed in the window keeps its
// memory time in rows when it still has a notice in memory, and loses its stored row otherwise; its kept seen is
// dropped, since the push came after it. Any other stored key with a kept seen at or after its stored time is cleared
// once. Each other stored key takes its stored time as its notice in memory. The caller holds n.mu.
func (n *Notices) mergeLocked(ctx context.Context, rows Rows, stored []store.PushedRow) {
	pushed, seens := n.pushedBeforeAttach, n.keptSeens
	n.attached = true
	n.pushedBeforeAttach, n.keptSeens = nil, nil
	n.rows = rows
	if rows == nil {
		return
	}
	for key := range pushed {
		if at, ok := n.noticed[key]; ok {
			n.enqueueLocked(ctx, key, func(ctx context.Context) { n.putRow(ctx, rows, key, at) })
		} else {
			n.enqueueLocked(ctx, key, func(ctx context.Context) { n.deleteRow(ctx, rows, key) })
		}
	}
	for _, row := range stored {
		key := Key{Pane: row.Pane, Session: row.Session, Epic: row.Epic, Limit: row.Limit, Alert: row.Alert}
		if pushed[key] {
			continue
		}
		if seen, ok := seens[key]; ok && seen >= row.Time {
			n.enqueueLocked(ctx, key, n.clearSend(key, seen, rows))
			continue
		}
		n.noticed[key] = row.Time
	}
}

// Wait returns once every send started by Notify or Seen has finished.
func (n *Notices) Wait() {
	n.sends.Wait()
}

// Retry queues a store of every key's unstored row write again, each in its key's own order of sends, so it never
// overtakes a later write of the key. A key whose retry is already queued is skipped. orreryd calls it on each tidy
// pass, so a write the store refused is stored once the store takes writes again, even when no later row write comes
// to retry it. A retry the store refuses again stays unstored for the next. Retry returns without waiting for the
// retries.
func (n *Notices) Retry(ctx context.Context) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.rows != nil {
		n.retryLocked(ctx, n.rows)
	}
}

// Close retries every unstored row write once more, then returns once every send, the retries among them, has
// finished. Call it after the last Notify and Seen and before the store closes, so a write the store refused earlier
// gets its last try while the store is open.
func (n *Notices) Close(ctx context.Context) {
	n.Retry(ctx)
	n.Wait()
}

// bounded runs one store call on parent under the Notices' deadline and returns its error. A call that outlives the
// deadline reads as failed, even when it succeeded, because it was late.
func (n *Notices) bounded(parent context.Context, call func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, n.deadline)
	defer cancel()
	if err := call(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store call outlived its %v deadline: %w", n.deadline, err)
	}
	return nil
}

// clearSend returns the send that clears key at seen, then deletes key's row when rows is set.
func (n *Notices) clearSend(key Key, seen int64, rows Rows) func(context.Context) {
	return func(ctx context.Context) {
		c := push.Clear{
			Host: n.host, Pane: key.Pane, Session: key.Session, Epic: key.Epic, Limit: key.Limit, Alert: key.Alert,
			Seen: seen,
		}
		if err := n.sender.PublishClear(ctx, c); err != nil {
			n.logger.ErrorContext(ctx, "publish push clear", "error", push.RedactEndpoints(err))
		}
		if rows != nil {
			n.deleteRow(ctx, rows, key)
		}
	}
}

// putRow writes key's row with time at, bounded. A failure is logged, and the write is kept to be stored again (see
// stored, Retry and Close).
func (n *Notices) putRow(ctx context.Context, rows Rows, key Key, at int64) {
	row := store.PushedRow{
		Pane: key.Pane, Session: key.Session, Epic: key.Epic, Limit: key.Limit, Alert: key.Alert, Time: at,
	}
	if err := n.bounded(ctx, func(ctx context.Context) error { return rows.PutPushed(ctx, row) }); err != nil {
		n.logger.ErrorContext(ctx, "put pushed notice", "key", key, "error", err)
		n.unstoredWrite(key, rowWrite{put: true, at: at})
		return
	}
	n.stored(ctx, rows, key)
}

// unstoredWrite keeps write as key's row write to store again, in place of any earlier one.
func (n *Notices) unstoredWrite(key Key, write rowWrite) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.unstored[key] = write
}

// stored records that the store took key's row write, and queues a retry of every other key's unstored write: the
// store took a write, so it may take theirs now. Each retry runs in its key's own order of sends, after the sends
// already queued for that key, so it never overtakes a later write of the key. A retry the store refuses again stays
// unstored until the next write the store takes, the next Retry, or Close.
func (n *Notices) stored(ctx context.Context, rows Rows, key Key) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.unstored, key)
	n.retryLocked(ctx, rows)
}

// retryLocked queues a retry through rows of every unstored key's write whose retry is not already queued. The caller
// holds n.mu.
func (n *Notices) retryLocked(ctx context.Context, rows Rows) {
	for _, key := range slices.SortedFunc(maps.Keys(n.unstored), compareKeys) {
		if n.retrying[key] {
			continue
		}
		n.retrying[key] = true
		n.enqueueLocked(ctx, key, func(ctx context.Context) { n.retryRow(ctx, rows, key) })
	}
}

// retryRow stores key's unstored row write again, when it still has one.
func (n *Notices) retryRow(ctx context.Context, rows Rows, key Key) {
	n.mu.Lock()
	delete(n.retrying, key)
	write, ok := n.unstored[key]
	n.mu.Unlock()
	switch {
	case !ok:
	case write.put:
		n.putRow(ctx, rows, key, write.at)
	default:
		n.deleteRow(ctx, rows, key)
	}
}

// compareKeys orders keys by their fields, so retries are queued in one order.
func compareKeys(a, b Key) int {
	return strings.Compare(
		strings.Join([]string{a.Pane, a.Session, a.Epic, a.Limit, a.Alert}, "\x00"),
		strings.Join([]string{b.Pane, b.Session, b.Epic, b.Limit, b.Alert}, "\x00"),
	)
}

// deleteRow deletes key's row, bounded. An epic key's row is deleted by its epic, a limit key's by its limit, and an
// alert key's by its alert. A failure is logged, and the delete is kept to be stored again (see stored, Retry and
// Close).
func (n *Notices) deleteRow(ctx context.Context, rows Rows, key Key) {
	if err := n.bounded(ctx, func(ctx context.Context) error {
		if key.Epic != "" {
			return rows.DeletePushedEpic(ctx, key.Epic)
		}
		if key.Limit != "" {
			return rows.DeletePushedLimit(ctx, key.Limit)
		}
		if key.Alert != "" {
			return rows.DeletePushedAlert(ctx, key.Alert)
		}
		return rows.DeletePushed(ctx, key.Pane, key.Session)
	}); err != nil {
		n.logger.ErrorContext(ctx, "delete pushed notice", "key", key, "error", err)
		n.unstoredWrite(key, rowWrite{})
		return
	}
	n.stored(ctx, rows, key)
}

// enqueueLocked starts send in the background after the last send queued for key has finished. The caller holds
// n.mu. send runs with ctx's cancellation removed.
func (n *Notices) enqueueLocked(ctx context.Context, key Key, send func(context.Context)) {
	ctx = context.WithoutCancel(ctx)
	prev := n.tails[key]
	done := make(chan struct{})
	n.tails[key] = done
	n.sends.Add(1)
	go func() {
		defer n.sends.Done()
		if prev != nil {
			<-prev
		}
		send(ctx)
		close(done)
		n.mu.Lock()
		if n.tails[key] == done {
			delete(n.tails, key)
		}
		n.mu.Unlock()
	}()
}
