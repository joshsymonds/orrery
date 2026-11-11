// Package enginehost runs headless engine sessions. Its Writer is the one goroutine that stores every session's
// engine ops, so sessions never hold the store's or the pane mirror's locks.
package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

const (
	defaultTick  = 100 * time.Millisecond
	defaultLimit = 256
)

// ErrWriterStopped reports a Put or Commit that cannot be stored because Run has returned.
var ErrWriterStopped = errors.New("engine writer stopped")

// Applier stores engine ops in one transaction. *store.Store satisfies it.
type Applier interface {
	ApplyEngine(ctx context.Context, ops []store.EngineOp, now time.Time) error
}

// WriterConfig configures a Writer. A zero Tick is 100 ms and a zero Limit is 256. A nil Now is time.Now and a nil
// Logger discards.
type WriterConfig struct {
	Applier Applier
	Tick    time.Duration
	Limit   int
	Now     func() time.Time
	Logger  *slog.Logger
}

// outcome is how one queued op ends. done is closed once the op is stored or dropped. err is set before done is
// closed, and only for a dropped op.
type outcome struct {
	done chan struct{}
	err  error
}

// entry is one queued op. result is the op's outcome.
type entry struct {
	id     string
	op     store.EngineOp
	result *outcome
}

// textKey names the unfinished-text row an EngineText or EngineTextDone op addresses.
type textKey struct {
	messageID       string
	index           int
	parentToolUseID string
}

// Writer stores queued engine ops. Each tick it sends every queued op, from every session, to Applier in one call.
// Ops the Applier refuses stay queued in order and are sent again on the next tick, except an op for a session whose
// row is gone (store.ErrNoEngineSession): that op is dropped, never sent again, and its Commit fails.
type Writer struct {
	applier Applier
	tick    time.Duration
	limit   int
	now     func() time.Time
	logger  *slog.Logger

	// failing is read and written only by flush, which only Run calls.
	failing bool

	mu sync.Mutex
	// queue holds the ops not yet in an ApplyEngine call that succeeded, oldest first. A flush takes the whole
	// queue out while it applies, so an op in flight can never be coalesced into.
	queue []entry
	// unstored counts each session's queued or in-flight non-text ops.
	unstored map[string]int
	// changed is closed and replaced whenever a flush stores or drops ops, waking Puts blocked at the limit.
	changed chan struct{}
	// closing is set when Run begins its last flush. From then on nothing would store a newly queued op, so put
	// refuses it.
	closing bool
	// stopped is closed when Run returns.
	stopped chan struct{}
}

// NewWriter returns a Writer that is not running. Call Run to start it.
func NewWriter(cfg WriterConfig) *Writer {
	writer := &Writer{
		applier:  cfg.Applier,
		tick:     cfg.Tick,
		limit:    cfg.Limit,
		now:      cfg.Now,
		logger:   cfg.Logger,
		unstored: map[string]int{},
		changed:  make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	if writer.tick <= 0 {
		writer.tick = defaultTick
	}
	if writer.limit <= 0 {
		writer.limit = defaultLimit
	}
	if writer.now == nil {
		writer.now = time.Now
	}
	if writer.logger == nil {
		writer.logger = slog.New(slog.DiscardHandler)
	}
	return writer
}

// Run stores queued ops once per tick until ctx ends, then makes one last flush attempt on a context that ignores
// ctx's cancellation, and returns. A Put or Commit that would queue once that last flush has begun returns
// ErrWriterStopped. Run runs once.
func (w *Writer) Run(ctx context.Context) {
	defer close(w.stopped)
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()
	// No flush runs on a context ctx's cancellation reaches: a store write canceled during its commit can report an
	// error for a batch SQLite committed, which would queue that batch and store it twice. The store's busy timeout
	// bounds each write instead.
	flushCtx := context.WithoutCancel(ctx)
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			w.closing = true
			w.mu.Unlock()
			w.flush(flushCtx)
			return
		case <-ticker.C:
			w.flush(flushCtx)
		}
	}
}

// Put queues ops for session id, in order. A Put with a non-text op waits while the session has as many unstored
// non-text ops as the limit, so a session that cannot store pauses instead of growing without bound. The ops must
// carry id as their session ID.
func (w *Writer) Put(ctx context.Context, id string, ops ...store.EngineOp) error {
	_, err := w.put(ctx, id, ops)
	return err
}

// Commit queues ops for session id like Put, then returns once they are stored. It returns an error wrapping
// store.ErrNoEngineSession when one of the ops was dropped because its session row is gone.
func (w *Writer) Commit(ctx context.Context, id string, ops ...store.EngineOp) error {
	results, err := w.put(ctx, id, ops)
	if err != nil {
		return err
	}
	for _, result := range results {
		if awaitErr := w.await(ctx, result); awaitErr != nil {
			return awaitErr
		}
	}
	return nil
}

// put queues ops and returns, for each op, its outcome. A text op coalesced into a queued one returns that op's
// outcome.
func (w *Writer) put(ctx context.Context, id string, ops []store.EngineOp) ([]*outcome, error) {
	needsRoom := false
	for _, op := range ops {
		if !isText(op) {
			needsRoom = true
			break
		}
	}

	w.mu.Lock()
	for needsRoom && w.unstored[id] >= w.limit {
		changed := w.changed
		w.mu.Unlock()
		select {
		case <-changed:
		case <-w.stopped:
			return nil, ErrWriterStopped
		case <-ctx.Done():
			return nil, fmt.Errorf("queue ops for session %s: %w", id, ctx.Err())
		}
		w.mu.Lock()
	}
	defer w.mu.Unlock()
	if w.closing {
		return nil, ErrWriterStopped
	}

	results := make([]*outcome, 0, len(ops))
	for _, op := range ops {
		results = append(results, w.enqueueLocked(id, op))
	}
	return results, nil
}

// enqueueLocked appends op to the queue, or coalesces an EngineText into the queued one it replaces, and returns the
// outcome of op.
func (w *Writer) enqueueLocked(id string, op store.EngineOp) *outcome {
	if text, ok := op.(store.EngineText); ok {
		if coalesced, done := w.coalesceLocked(id, text); done {
			return coalesced
		}
	}
	result := &outcome{done: make(chan struct{})}
	w.queue = append(w.queue, entry{id: id, op: op, result: result})
	if !isText(op) {
		w.unstored[id]++
	}
	return result
}

// coalesceLocked replaces the queued EngineText for the same block with text, when no EngineTextDone or
// EngineTextClear for that session sits between them in the queue.
func (w *Writer) coalesceLocked(id string, text store.EngineText) (*outcome, bool) {
	block := blockOf(text.MessageID, text.Index, text.ParentToolUseID)
	for i := len(w.queue) - 1; i >= 0; i-- {
		queued := &w.queue[i]
		if queued.id != id {
			continue
		}
		switch prev := queued.op.(type) {
		case store.EngineTextClear:
			return nil, false
		case store.EngineTextDone:
			if blockOf(prev.MessageID, prev.Index, prev.ParentToolUseID) == block {
				return nil, false
			}
		case store.EngineText:
			if blockOf(prev.MessageID, prev.Index, prev.ParentToolUseID) == block {
				queued.op = text
				return queued.result, true
			}
		}
	}
	return nil, false
}

// blockOf names the unfinished-text row of one block.
func blockOf(messageID string, index int, parentToolUseID string) textKey {
	return textKey{messageID: messageID, index: index, parentToolUseID: parentToolUseID}
}

// flush sends every queued op in one ApplyEngine call. An op for a session whose row is gone is dropped, and the rest
// are sent again in the same flush, so it never holds up the others. On any other failure flush puts the ops back at
// the front of the queue for the next flush. Each flush that stores or drops ops wakes the Puts blocked at the limit.
func (w *Writer) flush(ctx context.Context) {
	w.mu.Lock()
	batch := w.queue
	w.queue = nil
	w.mu.Unlock()
	if len(batch) == 0 {
		return
	}

	var dropped []entry
	var err error
	for len(batch) > 0 {
		err = w.applier.ApplyEngine(ctx, opsOf(batch), w.now())
		var opErr *store.EngineOpError
		if err == nil || !errors.Is(err, store.ErrNoEngineSession) || !errors.As(err, &opErr) {
			break
		}
		gone := batch[opErr.Index]
		batch = slices.Delete(batch, opErr.Index, opErr.Index+1)
		w.logger.WarnContext(ctx, "engine op dropped: its session row is gone, so it is not retried",
			"session", gone.id, "err", err)
		gone.result.err = fmt.Errorf("op for session %s dropped: %w", gone.id, err)
		dropped = append(dropped, gone)
		err = nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range dropped {
		w.settleLocked(e)
	}
	if err != nil {
		w.queue = append(batch, w.queue...)
		if !w.failing {
			w.logger.WarnContext(ctx, "engine store write failed; ops kept for retry", "ops", len(batch), "err", err)
		}
		w.failing = true
	} else {
		if w.failing {
			w.logger.InfoContext(ctx, "engine store write recovered", "ops", len(batch))
		}
		w.failing = false
		for _, e := range batch {
			w.settleLocked(e)
		}
	}
	if err == nil || len(dropped) > 0 {
		close(w.changed)
		w.changed = make(chan struct{})
	}
}

// settleLocked closes the outcome of an op that is stored or dropped, and frees its session's slot when it counted
// against the limit. w.mu must be held.
func (w *Writer) settleLocked(e entry) {
	close(e.result.done)
	if !isText(e.op) {
		w.unstored[e.id]--
		if w.unstored[e.id] == 0 {
			delete(w.unstored, e.id)
		}
	}
}

// await waits for result to settle, or for ctx to end or Run to return without settling it. It returns the op's
// error when the op was dropped.
func (w *Writer) await(ctx context.Context, result *outcome) error {
	select {
	case <-result.done:
		return result.err
	case <-ctx.Done():
		return fmt.Errorf("wait for stored ops: %w", ctx.Err())
	case <-w.stopped:
		if isClosed(result.done) {
			return result.err
		}
		return ErrWriterStopped
	}
}

// opsOf returns the ops of batch, in order.
func opsOf(batch []entry) []store.EngineOp {
	ops := make([]store.EngineOp, len(batch))
	for i, e := range batch {
		ops[i] = e.op
	}
	return ops
}

// isText reports whether op is one of the unfinished-text ops, which do not count against a session's limit.
func isText(op store.EngineOp) bool {
	switch op.(type) {
	case store.EngineText, store.EngineTextDone, store.EngineTextClear:
		return true
	default:
		return false
	}
}

// isClosed reports whether ch is closed, without blocking.
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
