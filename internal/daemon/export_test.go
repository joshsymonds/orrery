package daemon

import "time"

// SetMirrorLockWait makes m's syncs and tidies wait on the channel wait returns in place of mirrorLockWait of real
// time, so a test outside the package decides when a wait expires.
func SetMirrorLockWait(m *Mirror, wait func() <-chan time.Time) {
	m.lockWait = wait
}

// HoldMirrorLock takes m's lock as a running sync or tidy holds it, and returns the func that frees it.
func HoldMirrorLock(m *Mirror) func() {
	m.lock <- struct{}{}
	return m.release
}
