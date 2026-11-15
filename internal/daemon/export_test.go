package daemon

import "time"

// SetMirrorLockWait makes m's syncs and tidies wait on the channel wait returns in place of mirrorLockWait of real
// time, so a test outside the package decides when a wait expires.
func SetMirrorLockWait(m *Mirror, wait func() <-chan time.Time) {
	m.lockWait = wait
}
