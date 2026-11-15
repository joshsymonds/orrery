package enginehost

import "sync/atomic"

// callsInFlight counts the calls of an engine part that are under way, each from its start to its return. Every call
// that hands work to another goroutine registers that work before it returns, as a loop, a pump, an attempt, a drive or
// a queued op, so a part with no call under way and no work registered has nothing coming. Only tests read the count,
// to tell a part whose work has finished from one between two pieces of work.
type callsInFlight struct {
	n atomic.Int64
}

// enter counts one call under way and returns the func that ends it.
func (c *callsInFlight) enter() func() {
	c.n.Add(1)
	return c.leave
}

// leave ends one call that enter counted.
func (c *callsInFlight) leave() {
	c.n.Add(-1)
}
