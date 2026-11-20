package enginehost

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

// housekeepRetry is how long a housekeeping writer waits before it writes its kept writes again, or reads again the
// epics whose read failed.
const housekeepRetry = 5 * time.Second

// housekeepWake holds the epic ids a store hook named since the writer last took them, and the ids whose read failed,
// which a retry reads again. Wake is called from the store's write path, so it records the id and signals Run, and
// touches no store.
type housekeepWake struct {
	mu      sync.Mutex
	pending map[string]bool
	kept    map[string]bool
	// signal holds one pending wake for Run, so a wake that comes while one is pending is absorbed by it.
	signal   chan struct{}
	newTimer func(time.Duration) (<-chan time.Time, func() bool)
}

// newHousekeepWake returns a housekeepWake whose retry timer comes from newTimer. A nil newTimer wraps time.NewTimer.
func newHousekeepWake(newTimer func(time.Duration) (<-chan time.Time, func() bool)) *housekeepWake {
	if newTimer == nil {
		newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
			timer := time.NewTimer(d)
			return timer.C, timer.Stop
		}
	}
	return &housekeepWake{
		pending: make(map[string]bool), kept: make(map[string]bool), signal: make(chan struct{}, 1), newTimer: newTimer,
	}
}

// Wake adds id to the pending set and signals Run, without blocking.
func (w *housekeepWake) Wake(id string) {
	w.mu.Lock()
	w.pending[id] = true
	w.mu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// keep holds id for the next retry, since its read failed.
func (w *housekeepWake) keep(id string) {
	w.mu.Lock()
	w.kept[id] = true
	w.mu.Unlock()
}

// owed reports whether an id is kept for a retry.
func (w *housekeepWake) owed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.kept) > 0
}

// take returns the ids to read, in order, and clears them. A wake reads only the pending ids. A retry reads the kept
// ids as well.
func (w *housekeepWake) take(retry bool) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if retry {
		maps.Copy(w.pending, w.kept)
		clear(w.kept)
	}
	ids := slices.Sorted(maps.Keys(w.pending))
	clear(w.pending)
	return ids
}

// run calls round at start, on each wake and when a retry fires, until ctx ends. round reports whether a retry is still
// owed for a reason of its own. A retry is owed while round says so or an id is kept, and run arms one when none is
// armed. A clean round arms none. run stops an armed retry when it returns.
func (w *housekeepWake) run(ctx context.Context, round func(ctx context.Context, retry bool) bool) {
	var fire <-chan time.Time
	var stopRetry func() bool
	defer func() {
		if stopRetry != nil {
			stopRetry()
		}
	}()
	owed := round(ctx, false)
	for {
		if (owed || w.owed()) && fire == nil {
			fire, stopRetry = w.newTimer(housekeepRetry)
		}
		retry := false
		select {
		case <-ctx.Done():
			return
		case <-w.signal:
		case <-fire:
			fire = nil
			retry = true
		}
		owed = round(ctx, retry)
	}
}
