package panehost_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/panehost"
	"github.com/joshsymonds/orrery/internal/panewatch"
	"github.com/joshsymonds/orrery/internal/tmuxctl"
)

// newRunHost returns a Host whose registry watch is watch, and whose Run takes its ticks from ticks. No tmux server
// starts: New only builds the client of DefaultServer.
func newRunHost(
	t *testing.T,
	ticks <-chan time.Time,
	watch func(context.Context, panewatch.Config) error,
) *panehost.Host {
	t.Helper()
	host, err := panehost.New(panehost.Config{
		Name:          "unused",
		DefaultServer: tmuxctl.Server{Name: "orrery-panehost-unused"},
		Ticks:         ticks,
		Now:           fixedNow,
		Watch:         watch,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return host
}

// runCancelledDuringTick runs a Host whose watch cancels Run's context and then returns the watch context's error. The
// watch waits until Run has taken a tick, so the cancel lands while Run is busy with that tick, not while it is parked
// in select. When Run next selects, the watch's end and ctx's end are both ready. It returns what Run returned.
func runCancelledDuringTick(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time)
	release := make(chan struct{})
	host := newRunHost(t, ticks, func(watchCtx context.Context, _ panewatch.Config) error {
		<-release
		cancel()
		return watchCtx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx) }()
	ticks <- time.Time{}
	close(release)
	return <-done
}

// TestPaneHostRunEndsNilWhenCancelled runs cancelled Runs, and each one must return nil, whichever branch of Run's
// select it takes when the watch's end and ctx's end are both ready.
func TestPaneHostRunEndsNilWhenCancelled(t *testing.T) {
	for i := range 200 {
		if err := runCancelledDuringTick(t); err != nil {
			t.Fatalf("iteration %d: Run = %v, want nil", i, err)
		}
	}
}

// TestPaneHostRunEndsWithWatchFailure fails the watch while Run's context is live, and expects Run to return that
// failure.
func TestPaneHostRunEndsWithWatchFailure(t *testing.T) {
	sentinel := errors.New("registry watch failed")
	host := newRunHost(t, make(chan time.Time), func(context.Context, panewatch.Config) error { return sentinel })
	if err := host.Run(t.Context()); !errors.Is(err, sentinel) {
		t.Fatalf("Run = %v, want %v", err, sentinel)
	}
}
