package interpret

import (
	"maps"
	"slices"
	"strings"

	"github.com/joshsymonds/orrery/internal/store"
)

// Wake records that the epics named by epics changed, so the next pass Run makes passes them. Any goroutine may call it,
// it never blocks, and it takes only the runner's own mutex. Each epic a Wake names also wakes the epics waiting on its
// release slot (wakeLocked).
func (r *Runner) Wake(epics []string) {
	r.mu.Lock()
	r.wakeLocked(epics)
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// WakeOutsideWaiters wakes the epics that waited on an outside hold of a release slot, which an outside releaser's hold
// being given back or expiring calls. Each such holder is forgotten, and the epics waiting on it are woken, so a waiter
// that still waits records itself again on its next pass. The holder names no epic, so it is not woken. Any goroutine
// may call it, it never blocks, and it takes only the runner's own mutex.
func (r *Runner) WakeOutsideWaiters() {
	r.mu.Lock()
	for holder, waiters := range r.waiters {
		if !strings.HasPrefix(holder, store.ReleaseSlotOutsidePrefix) {
			continue
		}
		for waiter := range waiters {
			r.woken[waiter] = struct{}{}
		}
		delete(r.waiters, holder)
	}
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// wakeLocked adds each of epics to the woken set, with the epics that waited on its release slot, which are forgotten
// here: a waiter that still waits records itself again on its next pass. The caller holds r.mu.
func (r *Runner) wakeLocked(epics []string) {
	for _, epic := range epics {
		r.woken[epic] = struct{}{}
		for waiter := range r.waiters[epic] {
			r.woken[waiter] = struct{}{}
		}
		delete(r.waiters, epic)
	}
}

// takeWoken returns the woken epics, sorted, and empties the woken set.
func (r *Runner) takeWoken() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	epics := slices.Sorted(maps.Keys(r.woken))
	clear(r.woken)
	return epics
}

// noteRead records the wakes a read of epic leaves: when the repository's release slot is held by another epic, epic
// waits on that holder.
func (r *Runner) noteRead(epic string, read store.EpicFlow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if holder := read.Slot; holder != "" && holder != epic {
		if r.waiters[holder] == nil {
			r.waiters[holder] = make(map[string]struct{})
		}
		r.waiters[holder][epic] = struct{}{}
	}
}
