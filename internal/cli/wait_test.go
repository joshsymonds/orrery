package cli_test

import "time"

// receiveUntil receives from ch until ended reports that what feeds ch has ended. It has no deadline of its own: it
// looks at ended every pollInterval, and once ended reports true it looks at ch once more, so a value sent just before
// the end is still received. It reports whether it received a value or ch's close, and whether ch was open.
func receiveUntil[T any](ch <-chan T, ended func() bool) (v T, open, received bool) {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case v, open = <-ch:
			return v, open, true
		case <-tick.C:
		}
		if !ended() {
			continue
		}
		select {
		case v, open = <-ch:
			return v, open, true
		default:
			return v, false, false
		}
	}
}

// resultQueued returns the ended func of a call whose result arrives on result: the call has ended once its result is
// queued. It reads the channel's length, so the result stays on the channel for the test to read.
func resultQueued[T any](result <-chan T) func() bool {
	return func() bool { return len(result) > 0 }
}

// doneClosed returns the ended func of a call that closes done when it returns.
func doneClosed(done <-chan struct{}) func() bool {
	return func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}
}
