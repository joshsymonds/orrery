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
	rows    Rows
	tails   map[Key]chan struct{}
	sends   sync.WaitGroup
}

// New returns a Notices for host that posts through sender, reads the time from now, and logs with logger.
func New(host string, sender Sender, now func() time.Time, logger *slog.Logger) *Notices {
	return &Notices{
		host:    host,
		sender:  sender,
		now:     now,
		logger:  logger,
		noticed: make(map[Key]int64),
		tails:   make(map[Key]chan struct{}),
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
	rows := n.rows
	n.enqueueLocked(ctx, key, func(ctx context.Context) {
		if sent != nil {
			defer sent()
		}
		if rows != nil {
			row := store.PushedRow{Pane: key.Pane, Session: key.Session, Time: at}
			if err := bounded(ctx, func(ctx context.Context) error { return rows.PutPushed(ctx, row) }); err != nil {
				n.logger.ErrorContext(ctx, "put pushed notice", "key", key, "error", err)
			}
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

// Seen clears key's notice when it was recorded at or before seen, and forgets it. Otherwise it does nothing.
// The clear is sent in the background after any send already queued for key. With a store attached, the clear then
// deletes key's row.
func (n *Notices) Seen(ctx context.Context, key Key, seen int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	at, ok := n.noticed[key]
	if !ok || at > seen {
		return
	}
	delete(n.noticed, key)
	rows := n.rows
	n.enqueueLocked(ctx, key, func(ctx context.Context) {
		c := push.Clear{Host: n.host, Pane: key.Pane, Session: key.Session, Seen: seen}
		if err := n.sender.PublishClear(ctx, c); err != nil {
			n.logger.ErrorContext(ctx, "publish push clear", "error", push.RedactEndpoints(err))
		}
		if rows != nil {
			if err := bounded(ctx, func(ctx context.Context) error {
				return rows.DeletePushed(ctx, key.Pane, key.Session)
			}); err != nil {
				n.logger.ErrorContext(ctx, "delete pushed notice", "key", key, "error", err)
			}
		}
	})
}

// Attach keeps each key's last push in rows from now on. It loads the pushes rows holds: a key with a push in memory
// keeps the memory time, and any other key takes its stored time. A nil rows changes nothing. The store is read
// without n.mu held, so a push or clear that runs during the read still writes its row.
func (n *Notices) Attach(ctx context.Context, rows Rows) {
	if rows == nil {
		return
	}
	n.mu.Lock()
	n.rows = rows
	n.mu.Unlock()

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
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, row := range stored {
		key := Key{Pane: row.Pane, Session: row.Session}
		if _, ok := n.noticed[key]; !ok {
			n.noticed[key] = row.Time
		}
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
