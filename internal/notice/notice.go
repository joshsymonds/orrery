// Package notice records the Web Push notices orreryd sent and sends a clear when a pane or session is seen.
package notice

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/push"
	"github.com/joshsymonds/orrery/internal/store"
)

// storeDeadline bounds each store call a Notices makes. A call that outlives it reads as failed.
const storeDeadline = 200 * time.Millisecond

// Key names the pane or session a notice belongs to. Exactly one of Pane and Session is set.
type Key struct{ Pane, Session string }

// Sender posts notices and clears. *push.Publisher satisfies it.
type Sender interface {
	Publish(ctx context.Context, note push.Notification) error
	PublishClear(ctx context.Context, c push.Clear) error
}

// Rows is the store a Notices keeps each key's last push in. *store.Store satisfies it.
type Rows interface {
	PutPushed(context.Context, store.PushedRow) error
	DeletePushed(ctx context.Context, pane, session string) error
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
		if sendErr := n.sender.Publish(ctx, note); sendErr != nil {
			n.logger.ErrorContext(ctx, "publish push notice", "error", push.RedactEndpoints(sendErr))
		}
	})
}

// Seen clears key's notice when it was recorded at or before seen, and forgets it. Otherwise it does nothing. Before
// Attach, a seen for a key with no notice in memory is kept, and Attach decides what it clears. The clear is sent in
// the background after any send already queued for key. With a store attached, the clear then deletes key's row.
func (n *Notices) Seen(ctx context.Context, key Key, seen int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	at, ok := n.noticed[key]
	if !ok {
		if !n.attached {
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
	err := bounded(context.WithoutCancel(ctx), func(ctx context.Context) error {
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
		key := Key{Pane: row.Pane, Session: row.Session}
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

// bounded runs one store call on parent under storeDeadline and returns its error. A call that outlives the deadline
// reads as failed, even when it succeeded, because it was late.
func bounded(parent context.Context, call func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, storeDeadline)
	defer cancel()
	if err := call(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store call outlived its %v deadline: %w", storeDeadline, err)
	}
	return nil
}

// clearSend returns the send that clears key at seen, then deletes key's row when rows is set.
func (n *Notices) clearSend(key Key, seen int64, rows Rows) func(context.Context) {
	return func(ctx context.Context) {
		c := push.Clear{Host: n.host, Pane: key.Pane, Session: key.Session, Seen: seen}
		if err := n.sender.PublishClear(ctx, c); err != nil {
			n.logger.ErrorContext(ctx, "publish push clear", "error", push.RedactEndpoints(err))
		}
		if rows != nil {
			n.deleteRow(ctx, rows, key)
		}
	}
}

// putRow writes key's row with time at, bounded and logged on failure.
func (n *Notices) putRow(ctx context.Context, rows Rows, key Key, at int64) {
	row := store.PushedRow{Pane: key.Pane, Session: key.Session, Time: at}
	if err := bounded(ctx, func(ctx context.Context) error { return rows.PutPushed(ctx, row) }); err != nil {
		n.logger.ErrorContext(ctx, "put pushed notice", "key", key, "error", err)
	}
}

// deleteRow deletes key's row, bounded and logged on failure.
func (n *Notices) deleteRow(ctx context.Context, rows Rows, key Key) {
	if err := bounded(ctx, func(ctx context.Context) error {
		return rows.DeletePushed(ctx, key.Pane, key.Session)
	}); err != nil {
		n.logger.ErrorContext(ctx, "delete pushed notice", "key", key, "error", err)
	}
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
