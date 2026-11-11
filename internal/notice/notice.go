// Package notice records the Web Push notices orreryd sent and sends a clear when a pane or session is seen.
package notice

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/push"
)

// Key names the pane or session a notice belongs to. Exactly one of Pane and Session is set.
type Key struct{ Pane, Session string }

// Sender posts notices and clears. *push.Publisher satisfies it.
type Sender interface {
	Publish(ctx context.Context, note push.Notification) error
	PublishClear(ctx context.Context, c push.Clear) error
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

// Notify records the notice time for key and returns. It then sends build's notification in the background. The
// send ignores ctx's cancellation.
func (n *Notices) Notify(ctx context.Context, key Key, build func(context.Context) (push.Notification, error)) {
	at := n.now().Unix()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.noticed[key] = at
	n.enqueueLocked(ctx, key, func(ctx context.Context) {
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
// The clear is sent in the background after any send already queued for key.
func (n *Notices) Seen(ctx context.Context, key Key, seen int64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	at, ok := n.noticed[key]
	if !ok || at > seen {
		return
	}
	delete(n.noticed, key)
	n.enqueueLocked(ctx, key, func(ctx context.Context) {
		c := push.Clear{Host: n.host, Pane: key.Pane, Session: key.Session, Seen: seen}
		if err := n.sender.PublishClear(ctx, c); err != nil {
			n.logger.ErrorContext(ctx, "publish push clear", "error", push.RedactEndpoints(err))
		}
	})
}

// Wait returns once every send started by Notify or Seen has finished.
func (n *Notices) Wait() {
	n.sends.Wait()
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
