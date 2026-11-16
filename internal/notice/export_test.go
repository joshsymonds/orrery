package notice

import "time"

// SetStoreDeadline replaces the bound on each of n's store calls, storeDeadline by default, before n is used. A test
// whose store calls must never read as late on a loaded machine sets one longer than the test can run; one that forces
// a late call holds the call until its context ends.
func (n *Notices) SetStoreDeadline(d time.Duration) {
	n.deadline = d
}
