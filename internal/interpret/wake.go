package interpret

import (
	"maps"
	"slices"
	"strings"
	"time"

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

// noteRead records the wakes a read of epic at now, which read holds, leaves: when the repository's release slot is held
// by another epic, epic waits on that holder, and the moment its origin read goes stale is its deadline, replacing any
// earlier one, or none when that moment has come already.
func (r *Runner) noteRead(epic string, read store.EpicFlow, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if holder := read.Slot; holder != "" && holder != epic {
		if r.waiters[holder] == nil {
			r.waiters[holder] = make(map[string]struct{})
		}
		r.waiters[holder][epic] = struct{}{}
	}
	if moment := read.Epic.OriginRead.At.Add(syncInterval); moment.After(now) {
		r.deadlines[epic] = moment
	} else {
		delete(r.deadlines, epic)
	}
}

// nextDeadline returns the earliest registered deadline, and whether there is one.
func (r *Runner) nextDeadline() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var next time.Time
	found := false
	for _, moment := range r.deadlines {
		if !found || moment.Before(next) {
			next, found = moment, true
		}
	}
	return next, found
}

// wakeDue wakes each epic whose deadline now has reached, with the epics waiting on it, and forgets the deadline.
func (r *Runner) wakeDue(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var due []string
	for epic, moment := range r.deadlines {
		if !now.Before(moment) {
			due = append(due, epic)
			delete(r.deadlines, epic)
		}
	}
	r.wakeLocked(due)
}

// deadlineTimer is the one timer Run keeps. It is armed through the runner's NewTimer for the earliest deadline
// registered, and fire is nil while it is not armed, which a select never picks.
type deadlineTimer struct {
	fire <-chan time.Time
	stop func() bool
	at   time.Time
}

// fired records that the armed timer delivered, so it is no longer armed.
func (t *deadlineTimer) fired() {
	t.fire, t.stop = nil, nil
}

// disarm stops the armed timer, if there is one.
func (t *deadlineTimer) disarm() {
	if t.stop != nil {
		t.stop()
	}
	t.fired()
}

// armDeadline arms timer for the earliest registered deadline, keeping the timer already armed for it, and disarms it
// when no deadline is registered.
func (r *Runner) armDeadline(timer *deadlineTimer) {
	next, ok := r.nextDeadline()
	if !ok {
		timer.disarm()
		return
	}
	if timer.fire != nil && next.Equal(timer.at) {
		return
	}
	timer.disarm()
	timer.fire, timer.stop = r.newTimer(next.Sub(r.now()))
	timer.at = next
}

// stdTimer is time.NewTimer as a Config.NewTimer: the channel the timer fires on, and its Stop.
func stdTimer(d time.Duration) (<-chan time.Time, func() bool) {
	timer := time.NewTimer(d)
	return timer.C, timer.Stop
}
