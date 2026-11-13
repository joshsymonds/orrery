// Package rpcretry decides which failed RPCs may be retried, and retries them.
package rpcretry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"connectrpc.com/connect"
)

// Window is how long Do keeps retrying a call that fails with an error its predicate accepts.
const Window = 60 * time.Second

// initialBackoff is the wait before Do's first retry. Each later wait is backoffGrowth times the one before, up to
// maxBackoff.
const (
	initialBackoff = 100 * time.Millisecond
	backoffGrowth  = 2
	maxBackoff     = 5 * time.Second
)

// Clock supplies the time and the timers that Do measures and waits on, so a test can drive time without sleeping.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that receives once d has passed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the Clock of a running process: the wall clock.
type SystemClock struct{}

// Now returns time.Now().
func (SystemClock) Now() time.Time {
	return time.Now()
}

// After returns time.After(d).
func (SystemClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// NeverConnected reports whether err is a call whose connection was never made: the socket file is missing or the
// connection was refused. A connection cut after it was made is not NeverConnected.
func NeverConnected(err error) bool {
	var dialErr *net.OpError
	if !errors.As(err, &dialErr) || dialErr.Op != "dial" {
		return false
	}
	return errors.Is(dialErr, syscall.ENOENT) || errors.Is(dialErr, syscall.ECONNREFUSED)
}

// Unavailable reports whether err is a Connect error with code Unavailable, which includes a connection cut mid-call.
func Unavailable(err error) bool {
	return connect.CodeOf(err) == connect.CodeUnavailable
}

// Do runs call and, while it fails with an error retryable accepts and Window has not passed on clock, waits and runs
// it again. It returns nil when a call succeeds, ctx's error when ctx ends, and otherwise the last call's error.
func Do(ctx context.Context, clock Clock, retryable func(error) bool, call func(context.Context) error) error {
	start := clock.Now()
	backoff := initialBackoff
	for {
		err := call(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("rpcretry: %w", ctx.Err())
		}
		elapsed := clock.Now().Sub(start)
		if !retryable(err) || elapsed >= Window {
			return err
		}
		// Cap the wait at the end of the window so no attempt starts after Window.
		wait := min(backoff, Window-elapsed)
		select {
		case <-ctx.Done():
			return fmt.Errorf("rpcretry: %w", ctx.Err())
		case <-clock.After(wait):
		}
		backoff = min(backoffGrowth*backoff, maxBackoff)
	}
}
