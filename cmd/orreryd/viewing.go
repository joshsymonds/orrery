package main

import (
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/notice"
	"github.com/joshsymonds/orrery/internal/protocol"
)

// viewWindow is how long after its input a viewing report holds the notice for the key it names.
const viewWindow = 30 * time.Second

// viewTracker keeps the newest viewing report: the key the web view reported it views and the unix seconds of the
// input that report carries. Each input report replaces it, so a report that names no key ends the hold of the last.
type viewTracker struct {
	now func() time.Time

	mu  sync.Mutex
	key notice.Key
	at  int64
}

// newViewTracker returns a tracker with no report, which dates its checks by now.
func newViewTracker(now func() time.Time) *viewTracker {
	return &viewTracker{now: now}
}

// note records a viewing report: view is the key the web view views, and at is the unix seconds of the input it
// carries. A zero view names no key.
func (t *viewTracker) note(at int64, view protocol.ViewKey) {
	key := notice.Key{Pane: view.Pane, Session: view.Session, Epic: view.Epic}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.key = key
	t.at = at
}

// viewed reports whether the newest report names key and its input is under viewWindow old by now.
func (t *viewTracker) viewed(key notice.Key) bool {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.key == (notice.Key{}) || t.key != key {
		return false
	}
	return now.Sub(time.Unix(t.at, 0)) < viewWindow
}
