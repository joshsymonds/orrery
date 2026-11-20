package enginehost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// MaxReleaseSlotLimit is the longest an outside hold may run. A hold's limit is more than zero and at most this.
const MaxReleaseSlotLimit = 2 * time.Hour

// releaseSlotRetry is how long after a pass that failed on a store error the next pass runs, when no kick comes sooner.
const releaseSlotRetry = time.Minute

// ErrReleaseSlotLimit is matched by errors.Is when a hold's limit is not more than zero and at most MaxReleaseSlotLimit.
// The store is not written for such a limit.
var ErrReleaseSlotLimit = errors.New("release slot limit out of range")

// ReleaseSlotStore is the set of release slot operations ReleaseSlots runs over. *store.Store satisfies it.
type ReleaseSlotStore interface {
	ReleaseSlot(ctx context.Context, repository string) (store.ReleaseSlotHolder, bool, error)
	HoldReleaseSlot(
		ctx context.Context, repository, label string, deadline time.Time,
	) (store.ReleaseSlotHolder, bool, error)
	GiveReleaseSlot(ctx context.Context, repository, label string) (bool, error)
	FreeExpiredReleaseSlots(ctx context.Context, now time.Time) ([]string, error)
	NextReleaseSlotDeadline(ctx context.Context) (time.Time, bool, error)
}

// ReleaseSlotsConfig configures NewReleaseSlots. Store holds the slots. Now is the clock, and NewTimer arms a timer for a
// duration. Freed runs once for each give or pass that frees a slot. Logger logs each store error a pass meets. A nil
// Now is time.Now, a nil NewTimer wraps time.NewTimer, a nil Freed does nothing, and a nil Logger discards.
type ReleaseSlotsConfig struct {
	Store    ReleaseSlotStore
	Now      func() time.Time
	NewTimer func(time.Duration) CronTimer
	Freed    func()
	Logger   *slog.Logger
}

// ReleaseSlots is the orreryd side of an outside release-slot hold. An outside releaser holds a repository's release
// slot under a label, for a limit, until it gives the slot back or the limit passes. Holds are kept in the store, so
// they survive a restart. Run frees the expired ones from one timer, for the earliest deadline still ahead.
//
// A HoldWait waits on a change to the slots: a give that freed one, a pass that freed one, or Changed. Nothing polls.
type ReleaseSlots struct {
	store    ReleaseSlotStore
	now      func() time.Time
	newTimer func(time.Duration) CronTimer
	freed    func()
	logger   *slog.Logger
	// kick holds one pending kick, so a kick that comes while one is pending is absorbed.
	kick chan struct{}
	// mu guards changed.
	mu sync.Mutex
	// changed is closed, and replaced, by each change. A HoldWait takes it before a try, so a change after the try
	// closes it and the wait sees the change.
	changed chan struct{}
	// timer is the armed timer, or nil when none is armed. Only the passes touch it, and they run on one goroutine.
	timer CronTimer
}

// NewReleaseSlots returns the ReleaseSlots that cfg describes.
func NewReleaseSlots(cfg ReleaseSlotsConfig) *ReleaseSlots {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newTimer := cfg.NewTimer
	if newTimer == nil {
		newTimer = func(d time.Duration) CronTimer { return stdCronTimer{time.NewTimer(d)} }
	}
	freed := cfg.Freed
	if freed == nil {
		freed = func() {}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &ReleaseSlots{
		store: cfg.Store, now: now, newTimer: newTimer, freed: freed, logger: logger,
		kick: make(chan struct{}, 1), changed: make(chan struct{}),
	}
}

// Hold holds the release slot of repository for label until limit from now, and returns the holder and whether label
// holds the slot. A slot an epic or another label holds is left as it is, and Hold returns that holder with false and
// no error. When label holds the slot, Hold kicks Run, so its timer re-arms for the new deadline. Hold returns an error
// matching ErrReleaseSlotLimit, and does not write the store, when limit is not more than zero and at most
// MaxReleaseSlotLimit.
func (r *ReleaseSlots) Hold(
	ctx context.Context, repository, label string, limit time.Duration,
) (store.ReleaseSlotHolder, bool, error) {
	if limit <= 0 || limit > MaxReleaseSlotLimit {
		return store.ReleaseSlotHolder{}, false, fmt.Errorf(
			"%w: %v, want more than 0 and at most %v", ErrReleaseSlotLimit, limit, MaxReleaseSlotLimit,
		)
	}
	holder, held, err := r.store.HoldReleaseSlot(ctx, repository, label, r.now().Add(limit))
	if err != nil {
		return store.ReleaseSlotHolder{}, false, fmt.Errorf("hold release slot: %w", err)
	}
	if held {
		r.kickRun()
	}
	return holder, held, nil
}

// HoldWait holds the release slot of repository for label, as Hold does, and waits while another holder keeps it. Each
// time the slot is held by someone else, HoldWait calls waiting with that holder, and then waits for a change: a give
// that freed a slot, a pass that freed one, or Changed. It tries again after each change, with the deadline taken from
// the clock at that try. It returns the holder once label holds the slot. It returns ctx's error when ctx ends first,
// and waiting's error when waiting fails. waiting must not be nil.
func (r *ReleaseSlots) HoldWait(
	ctx context.Context, repository, label string, limit time.Duration,
	waiting func(store.ReleaseSlotHolder) error,
) (store.ReleaseSlotHolder, error) {
	for {
		// The change channel is taken before the try, so a change made after a failed try closes it and is not lost.
		changed := r.changes()
		holder, held, holdErr := r.Hold(ctx, repository, label, limit)
		if holdErr != nil {
			return store.ReleaseSlotHolder{}, holdErr
		}
		if held {
			return holder, nil
		}
		if err := waiting(holder); err != nil {
			return store.ReleaseSlotHolder{}, err
		}
		select {
		case <-ctx.Done():
			return store.ReleaseSlotHolder{}, fmt.Errorf("wait for release slot: %w", ctx.Err())
		case <-changed:
		}
	}
}

// Give gives back the outside hold label has on the release slot of repository, and reports whether that freed the slot.
// When it did, Give calls Freed once, wakes the waiting HoldWaits, and kicks Run. A give that frees nothing calls
// nothing.
func (r *ReleaseSlots) Give(ctx context.Context, repository, label string) (bool, error) {
	given, err := r.store.GiveReleaseSlot(ctx, repository, label)
	if err != nil {
		return false, fmt.Errorf("give release slot: %w", err)
	}
	if !given {
		return false, nil
	}
	r.freedSlot()
	r.kickRun()
	return true, nil
}

// Show returns the holder of the release slot of repository, and whether the slot is held.
func (r *ReleaseSlots) Show(ctx context.Context, repository string) (store.ReleaseSlotHolder, bool, error) {
	holder, held, err := r.store.ReleaseSlot(ctx, repository)
	if err != nil {
		return store.ReleaseSlotHolder{}, false, fmt.Errorf("show release slot: %w", err)
	}
	return holder, held, nil
}

// Changed wakes every waiting HoldWait, so each tries again. It never blocks. A later task wires it to the store's
// epics-changed hook, so a slot an epic frees reaches the waiters.
func (r *ReleaseSlots) Changed() { r.wake() }

// Run runs one pass at once, then one pass on each kick and each fire of its timer, until ctx ends. It stops its timer
// when it returns. A pass under way when ctx ends finishes first.
func (r *ReleaseSlots) Run(ctx context.Context) {
	defer r.stopTimer()
	r.pass(ctx)
	for {
		var fire <-chan time.Time
		if r.timer != nil {
			fire = r.timer.C()
		}
		select {
		case <-ctx.Done():
			return
		case <-r.kick:
		case <-fire:
		}
		r.pass(ctx)
	}
}

// pass frees each outside hold whose deadline is at or before the clock's now. When it freed any slot, the pass calls
// Freed and wakes the waiting HoldWaits. Then it stops the armed timer, which may be the one that just fired, and arms
// one for the earliest deadline still ahead, or none when no outside hold is left. A pass that fails on a store error
// is logged, and it arms a retry after releaseSlotRetry, so a fired timer is never left as the one armed: the retry
// fires, or a kick runs the pass sooner.
func (r *ReleaseSlots) pass(ctx context.Context) {
	now := r.now()
	next, held, err := r.expire(ctx, now)
	r.stopTimer()
	if err != nil {
		r.logger.WarnContext(ctx, "release slot pass", "err", err)
		r.timer = r.newTimer(releaseSlotRetry)
		return
	}
	if held {
		r.timer = r.newTimer(max(next.Sub(now), 0))
	}
}

// expire frees the outside holds whose deadline is at or before now, calls Freed and wakes the waiters when it freed
// any, and returns the earliest deadline still ahead and whether there is one.
func (r *ReleaseSlots) expire(ctx context.Context, now time.Time) (time.Time, bool, error) {
	freed, err := r.store.FreeExpiredReleaseSlots(ctx, now)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("free expired release slots: %w", err)
	}
	if len(freed) > 0 {
		r.freedSlot()
	}
	next, held, err := r.store.NextReleaseSlotDeadline(ctx)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read next release slot deadline: %w", err)
	}
	return next, held, nil
}

// freedSlot reports that a slot was freed: it calls Freed, then wakes the waiting HoldWaits.
func (r *ReleaseSlots) freedSlot() {
	r.freed()
	r.wake()
}

// kickRun puts one kick in the kick channel unless one is pending already.
func (r *ReleaseSlots) kickRun() {
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// wake closes the change channel, so every HoldWait waiting on it tries again, and replaces it for the next change.
func (r *ReleaseSlots) wake() {
	r.mu.Lock()
	defer r.mu.Unlock()
	close(r.changed)
	r.changed = make(chan struct{})
}

// changes returns the change channel that the next wake closes.
func (r *ReleaseSlots) changes() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.changed
}

// stopTimer stops the armed timer, if one is armed, and forgets it.
func (r *ReleaseSlots) stopTimer() {
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}
