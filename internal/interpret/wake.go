package interpret

import (
	"maps"
	"slices"
)

// Wake records that the epics named by epics changed, so the next pass Run makes passes them. Any goroutine may call it,
// it never blocks, and it takes only the runner's own mutex.
func (r *Runner) Wake(epics []string) {
	r.mu.Lock()
	r.wakeLocked(epics)
	r.mu.Unlock()
	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// wakeLocked adds each of epics to the woken set. The caller holds r.mu.
func (r *Runner) wakeLocked(epics []string) {
	for _, epic := range epics {
		r.woken[epic] = struct{}{}
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
