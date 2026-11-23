package main

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joshsymonds/orrery/internal/store"
)

// dueWakeWiringRecorder is the due wake a test gives the due relay. It counts each wake.
type dueWakeWiringRecorder struct {
	wakes atomic.Int64
}

// wake is the due wake: it counts one.
func (r *dueWakeWiringRecorder) wake() {
	r.wakes.Add(1)
}

// requireDueWakeWiringCount fails the test unless the recorder has counted want wakes.
func requireDueWakeWiringCount(t *testing.T, rec *dueWakeWiringRecorder, want int64) {
	t.Helper()
	if got := rec.wakes.Load(); got != want {
		t.Fatalf("due wakes = %d, want %d", got, want)
	}
}

// TestOrrerydDueDeadlineWakes checks that each wake that can move a due row wakes the due pass once the relay is set:
// the steps' Stored and a store write that moves an epic. Before the relay
// is set, each does nothing and does not block.
func TestOrrerydDueDeadlineWakes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)
	st, _ := openLimitStore(t)
	createAttentionEpic(t, st, "")
	host := newSessionHost(logger)
	host.newCallerEvents(st, options{}, logger)
	host.subscribeFlowWake(st, func([]string) {})

	host.stepStored()
	if err := st.ParkEpic(
		ctx, attentionWiringEpic, store.ParkClassBranch, "synthetic reason", time.Unix(attentionWiringStamp, 0),
	); err != nil {
		t.Fatalf("park epic %s: %v", attentionWiringEpic, err)
	}

	var rec dueWakeWiringRecorder
	host.dueWake.set(rec.wake)
	host.stepStored()
	requireDueWakeWiringCount(t, &rec, 1)
	if err := st.SetEpicBranch(ctx, attentionWiringEpic, flowWakeWiringBranch); err != nil {
		t.Fatalf("set branch of epic %s: %v", attentionWiringEpic, err)
	}
	requireDueWakeWiringCount(t, &rec, 2)
}
