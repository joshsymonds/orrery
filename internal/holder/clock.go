package holder

import "time"

// Clock supplies the timers that holder behavior waits on, so a test can drive time without sleeping.
type Clock interface {
	// After returns a channel that receives once d has passed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the Clock of a running holder: the wall clock.
type SystemClock struct{}

// After returns time.After(d).
func (SystemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}
