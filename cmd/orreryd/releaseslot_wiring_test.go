package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/enginehost"
	"github.com/joshsymonds/orrery/internal/store"
)

// Synthetic values only. The repository is invented, and the labels name outside releasers that exist nowhere.
const (
	slotOutsideWiringRepo   = "/work/slot-wiring-repo"
	slotOutsideWiringHost   = "slot-wiring-host"
	slotOutsideWiringEpic   = "orrery-0668c87/slot-wiring"
	slotOutsideWiringWaiter = "gambit:e2"
	slotOutsideWiringHolder = "gambit:e1"
)

// slotOutsideWiringT0 is the instant the tests start at: 2026-11-19 10:00:00 UTC.
func slotOutsideWiringT0() time.Time {
	return time.Date(2026, time.November, 19, 10, 0, 0, 0, time.UTC)
}

// slotOutsideWiringWait is what a HoldWait returns: the holder it returned, and its error.
type slotOutsideWiringWait struct {
	holder store.ReleaseSlotHolder
	err    error
}

// slotOutsideWiringStore opens the store a host runs over, with its state under directories the test owns, and returns
// it with the getenv the host reads those directories through.
func slotOutsideWiringStore(t *testing.T) (*store.Store, func(string) string) {
	t.Helper()
	env := map[string]string{
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CONFIG_HOME": t.TempDir(),
	}
	getenv := func(key string) string { return env[key] }
	path, err := store.Path(getenv)
	if err != nil {
		t.Fatalf("store path: %v", err)
	}
	return openPaneStore(t, path), getenv
}

// slotOutsideWiringHold holds the release slot of the repository for slotOutsideWiringHolder until T0+30m, as an outside
// releaser that holds it before the host starts does.
func slotOutsideWiringHold(t *testing.T, st *store.Store) {
	t.Helper()
	deadline := slotOutsideWiringT0().Add(30 * time.Minute)
	_, held, err := st.HoldReleaseSlot(t.Context(), slotOutsideWiringRepo, slotOutsideWiringHolder, deadline)
	if err != nil {
		t.Fatalf("hold release slot: %v", err)
	}
	if !held {
		t.Fatalf(
			"release slot of %s not held for %s, want it held until %v",
			slotOutsideWiringRepo,
			slotOutsideWiringHolder,
			deadline,
		)
	}
}

// slotOutsideWiringStart starts a session host over st, with the release slots reading clock as now. Each timer the
// release slots arm is sent on arms, and the test fires the ones it wants.
func slotOutsideWiringStart(
	t *testing.T, st *store.Store, getenv func(string) string, clock *cronWiringClock, arms chan<- *cronWiringTimer,
) *sessionHost {
	t.Helper()
	opts := options{
		socket:         filepath.Join(t.TempDir(), "orreryd.sock"),
		sessionCap:     defaultSessionCap,
		stepCap:        defaultStepCap,
		releaseSlotNow: clock.now,
		releaseSlotTimers: func(d time.Duration) enginehost.CronTimer {
			timer := &cronWiringTimer{c: make(chan time.Time, 1), d: d}
			arms <- timer
			return timer
		},
	}
	host, err := startSessions(
		t.Context(), opts, slotOutsideWiringHost, st, nil, nil, slog.New(slog.DiscardHandler), getenv,
	)
	if err != nil {
		t.Fatalf("startSessions: %v", err)
	}
	return host
}

// slotOutsideWiringStartWait runs HoldWait for slotOutsideWiringWaiter on the repository, with a limit of ten minutes, in
// a goroutine. Each waiting call sends its holder on waits, and the return is sent on done.
func slotOutsideWiringStartWait(
	ctx context.Context,
	slots *enginehost.ReleaseSlots,
	waits chan<- store.ReleaseSlotHolder,
	done chan<- slotOutsideWiringWait,
) {
	go func() {
		holder, err := slots.HoldWait(ctx, slotOutsideWiringRepo, slotOutsideWiringWaiter, 10*time.Minute,
			func(waiting store.ReleaseSlotHolder) error {
				waits <- waiting
				return nil
			})
		done <- slotOutsideWiringWait{holder: holder, err: err}
	}()
}

// TestSlotOutsideWiringTimerFreesHold checks that an outside hold expires on the release slots' one deadline timer. The
// hold runs until T0+30m. A HoldWait for another label waits on it, and the timer, fired a second early, frees nothing
// and is armed for that second. Fired at the deadline, the timer frees the hold, wakes the HoldWait, and the HoldWait
// takes the slot for its own ten minutes from the deadline. The host then stops without error.
func TestSlotOutsideWiringTimerFreesHold(t *testing.T) {
	st, getenv := slotOutsideWiringStore(t)
	slotOutsideWiringHold(t, st)
	clock := &cronWiringClock{at: slotOutsideWiringT0()}
	arms := make(chan *cronWiringTimer, cronWiringBuffer)
	host := slotOutsideWiringStart(t, st, getenv, clock, arms)

	// The first pass of Run finds the hold ahead, and arms the timer for its deadline.
	first := <-arms
	if first.d != 30*time.Minute {
		t.Fatalf("timer armed for %v at start, want %v, the time to the hold's deadline", first.d, 30*time.Minute)
	}

	waits := make(chan store.ReleaseSlotHolder, cronWiringBuffer)
	done := make(chan slotOutsideWiringWait, 1)
	slotOutsideWiringStartWait(t.Context(), host.releaseSlots, waits, done)
	if waiting := <-waits; waiting.Label != slotOutsideWiringHolder {
		t.Fatalf("HoldWait waits on %q, want %s", waiting.Label, slotOutsideWiringHolder)
	}

	// Fired a second before the deadline, the timer frees nothing, and is armed for that second.
	clock.set(slotOutsideWiringT0().Add(30*time.Minute - time.Second))
	first.c <- time.Time{}
	second := <-arms
	if second.d != time.Second {
		t.Fatalf("timer armed for %v a second early, want %v, the time to the deadline", second.d, time.Second)
	}
	if n := len(done); n != 0 {
		t.Fatalf("HoldWait returned %d times before the hold's deadline, want none", n)
	}

	// Fired at the deadline, the timer frees the hold, and the HoldWait takes the slot for its own limit.
	clock.set(slotOutsideWiringT0().Add(30 * time.Minute))
	second.c <- time.Time{}
	got := <-done
	if got.err != nil {
		t.Fatalf("HoldWait: %v", got.err)
	}
	want := slotOutsideWiringT0().Add(40 * time.Minute)
	if got.holder.Label != slotOutsideWiringWaiter || !got.holder.Deadline.Equal(want) {
		t.Fatalf("HoldWait returned %+v, want label %s with deadline %v", got.holder, slotOutsideWiringWaiter, want)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSlotOutsideWiringEpicWriteWakesHoldWait checks that an epic write wakes a HoldWait that waits on an outside hold.
// The timers are never fired, so only the write can wake the HoldWait. The write moves an epic's revision and fires the
// store's epics-changed hook, and the HoldWait calls its waiting function again, with the same holder still in place. A
// cancel then ends the HoldWait with context.Canceled. The host then stops without error.
func TestSlotOutsideWiringEpicWriteWakesHoldWait(t *testing.T) {
	st, getenv := slotOutsideWiringStore(t)
	slotOutsideWiringHold(t, st)
	clock := &cronWiringClock{at: slotOutsideWiringT0()}
	// The arms are buffered, and the test never fires them.
	arms := make(chan *cronWiringTimer, cronWiringBuffer)
	host := slotOutsideWiringStart(t, st, getenv, clock, arms)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waits := make(chan store.ReleaseSlotHolder, cronWiringBuffer)
	done := make(chan slotOutsideWiringWait, 1)
	slotOutsideWiringStartWait(ctx, host.releaseSlots, waits, done)
	if waiting := <-waits; waiting.Label != slotOutsideWiringHolder {
		t.Fatalf("HoldWait waits on %q, want %s", waiting.Label, slotOutsideWiringHolder)
	}

	// The epic write wakes the HoldWait, which tries again while the hold still runs.
	createFlowWiringEpic(t, st, slotOutsideWiringEpic, slotOutsideWiringRepo, "", slotOutsideWiringT0())
	if waiting := <-waits; waiting.Label != slotOutsideWiringHolder {
		t.Fatalf("HoldWait waits on %q after the epic write, want %s", waiting.Label, slotOutsideWiringHolder)
	}

	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("HoldWait returned %v, want an error matching context.Canceled", got.err)
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}

// TestSlotOutsideWiringGiveWakesOutsideWaiters checks that a give that frees a slot calls the outside wake once, and a
// give that frees nothing calls it not at all. The host is not started, so the give is the only thing that calls it.
func TestSlotOutsideWiringGiveWakesOutsideWaiters(t *testing.T) {
	st, _ := slotOutsideWiringStore(t)
	host := newSessionHost(slog.New(slog.DiscardHandler))
	calls := make(chan struct{}, cronWiringBuffer)
	host.outsideWake.set(func() { calls <- struct{}{} })
	host.newReleaseSlots(options{}, st, slog.New(slog.DiscardHandler))
	ctx := t.Context()

	_, held, err := host.releaseSlots.Hold(ctx, slotOutsideWiringRepo, slotOutsideWiringHolder, 30*time.Minute)
	if err != nil {
		t.Fatalf("hold release slot: %v", err)
	}
	if !held {
		t.Fatalf("hold release slot: %s does not hold it", slotOutsideWiringHolder)
	}

	given, err := host.releaseSlots.Give(ctx, slotOutsideWiringRepo, slotOutsideWiringHolder)
	if err != nil {
		t.Fatalf("give release slot: %v", err)
	}
	if !given {
		t.Fatalf("give release slot = false, want %s's hold given", slotOutsideWiringHolder)
	}
	if n := len(calls); n != 1 {
		t.Fatalf("outside wake called %d times by the give that freed the slot, want once", n)
	}

	given, err = host.releaseSlots.Give(ctx, slotOutsideWiringRepo, slotOutsideWiringHolder)
	if err != nil {
		t.Fatalf("second give release slot: %v", err)
	}
	if given {
		t.Fatalf("second give release slot = true, want false: nothing is held")
	}
	if n := len(calls); n != 1 {
		t.Fatalf("outside wake called %d times after a give that freed nothing, want still once", n)
	}
}
