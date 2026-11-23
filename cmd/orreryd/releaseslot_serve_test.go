package main

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/enginehost"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/protocol"
	"github.com/joshsymonds/orrery/internal/store"
)

// slotOutsideServeArms sizes the channel the armed release slot timers are sent on. The test never fires them, and it arms
// at most one.
const slotOutsideServeArms = 16

// slotOutsideServeStart starts a session host over st, with the release slots reading clock as now and sending each timer
// they arm on arms. It returns the host and the session socket path the host serves on.
func slotOutsideServeStart(
	t *testing.T, st *store.Store, getenv func(string) string, clock *cronWiringClock, arms chan<- *cronWiringTimer,
) (*sessionHost, string) {
	t.Helper()
	opts := options{
		socket:         filepath.Join(t.TempDir(), "orreryd.sock"),
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
	return host, protocol.SessionSocketPath(opts.socket)
}

// slotOutsideServeHold calls Hold on the test's repository for holder, with a limit of seconds, and returns the reply.
func slotOutsideServeHold(
	t *testing.T, client enginev1connect.ReleaseSlotsClient, holder string, seconds int64,
) *v1.ReleaseSlotsHoldResponse {
	t.Helper()
	req := connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
		Repository:   slotOutsideWiringRepo,
		Holder:       holder,
		LimitSeconds: seconds,
	})
	resp, err := client.Hold(t.Context(), req)
	if err != nil {
		t.Fatalf("Hold %s over the session socket: %v", holder, err)
	}
	return resp.Msg
}

// slotOutsideServeShow calls Show on the test's repository and returns the reply.
func slotOutsideServeShow(t *testing.T, client enginev1connect.ReleaseSlotsClient) *v1.ReleaseSlotsShowResponse {
	t.Helper()
	req := connect.NewRequest(&v1.ReleaseSlotsShowRequest{Repository: slotOutsideWiringRepo})
	resp, err := client.Show(t.Context(), req)
	if err != nil {
		t.Fatalf("Show over the session socket: %v", err)
	}
	return resp.Msg
}

// slotOutsideServeGive calls Give on the test's repository for holder and returns whether it freed the slot.
func slotOutsideServeGive(t *testing.T, client enginev1connect.ReleaseSlotsClient, holder string) bool {
	t.Helper()
	req := connect.NewRequest(&v1.ReleaseSlotsGiveRequest{Repository: slotOutsideWiringRepo, Holder: holder})
	resp, err := client.Give(t.Context(), req)
	if err != nil {
		t.Fatalf("Give %s over the session socket: %v", holder, err)
	}
	return resp.Msg.GetFreed()
}

// TestSlotOutsideCLIOrrerydServesReleaseSlots checks that a running orreryd answers the ReleaseSlots RPCs on its session
// socket. gambit:e1 holds the slot for 1800 seconds from T0, so the hold runs until T0+30m, and the store then reports it.
// gambit:e2 is refused the slot for 600 seconds, and the refusal names gambit:e1. Show reports gambit:e1 as the holder.
// gambit:e1 gives the slot back, which frees it, and Show then reports it free. The host then stops without error.
func TestSlotOutsideCLIOrrerydServesReleaseSlots(t *testing.T) {
	st, getenv := slotOutsideWiringStore(t)
	clock := &cronWiringClock{at: slotOutsideWiringT0()}
	arms := make(chan *cronWiringTimer, slotOutsideServeArms)
	host, sessionSocket := slotOutsideServeStart(t, st, getenv, clock, arms)
	client := enginev1connect.NewReleaseSlotsClient(cronWiringHTTP(t, sessionSocket), "http://release-slots.invalid")

	deadline := slotOutsideWiringT0().Add(30 * time.Minute)
	hold := slotOutsideServeHold(t, client, slotOutsideWiringHolder, 1800)
	if !hold.GetHeld() {
		t.Fatalf("Hold %s held = false, want true", slotOutsideWiringHolder)
	}
	got := hold.GetHolder()
	if got.GetKind() != store.ReleaseSlotOutside || got.GetLabel() != slotOutsideWiringHolder {
		t.Fatalf("Hold %s holder = %+v, want outside %s", slotOutsideWiringHolder, got, slotOutsideWiringHolder)
	}
	if got.GetDeadline() != deadline.Unix() {
		t.Fatalf("Hold %s deadline = %d, want %d", slotOutsideWiringHolder, got.GetDeadline(), deadline.Unix())
	}
	stored, held, err := st.ReleaseSlot(t.Context(), slotOutsideWiringRepo)
	if err != nil {
		t.Fatalf("store release slot: %v", err)
	}
	if !held || stored.Label != slotOutsideWiringHolder || !stored.Deadline.Equal(deadline) {
		t.Fatalf("store release slot = %+v held %v, want %s until %v", stored, held, slotOutsideWiringHolder, deadline)
	}

	wait := slotOutsideServeHold(t, client, slotOutsideWiringWaiter, 600)
	if wait.GetHeld() {
		t.Fatalf("Hold %s held = true, want false while %s holds it", slotOutsideWiringWaiter, slotOutsideWiringHolder)
	}
	if got := wait.GetHolder().GetLabel(); got != slotOutsideWiringHolder {
		t.Fatalf("Hold %s holder label = %q, want %s", slotOutsideWiringWaiter, got, slotOutsideWiringHolder)
	}

	shown := slotOutsideServeShow(t, client)
	if !shown.GetHeld() || shown.GetHolder().GetLabel() != slotOutsideWiringHolder {
		t.Fatalf(
			"Show = held %v holder %+v, want held by %s",
			shown.GetHeld(),
			shown.GetHolder(),
			slotOutsideWiringHolder,
		)
	}

	if !slotOutsideServeGive(t, client, slotOutsideWiringHolder) {
		t.Fatalf("Give %s freed = false, want true", slotOutsideWiringHolder)
	}
	if after := slotOutsideServeShow(t, client); after.GetHeld() {
		t.Fatalf("Show after the give held = true, want false")
	}

	if err := host.stop(); err != nil {
		t.Fatalf("stop sessions: %v", err)
	}
}
