package enginehost

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/store"
)

// slotOutsideRPCRepo is the repository the release slot RPC tests hold.
const slotOutsideRPCRepo = "/work/slot-rpc-repo"

// slotOutsideRPCNow returns the fixed time the release slot RPC tests read as now: 2026-11-19 10:00:00 UTC.
func slotOutsideRPCNow() time.Time {
	return time.Date(2026, time.November, 19, 10, 0, 0, 0, time.UTC)
}

// slotOutsideRPCNeverTimer is a CronTimer whose channel is never sent on, so it never fires.
type slotOutsideRPCNeverTimer struct {
	c chan time.Time
}

// C returns the channel that never receives.
func (t slotOutsideRPCNeverTimer) C() <-chan time.Time { return t.c }

// Stop reports that the timer was stopped. There is nothing to stop, so it reports true.
func (slotOutsideRPCNeverTimer) Stop() bool { return true }

// slotOutsideRPCSlots returns the ReleaseSlots the RPC tests serve: a real store, the fixed clock, and timers
// that never fire. Run is not started.
func slotOutsideRPCSlots(t *testing.T) *ReleaseSlots {
	t.Helper()
	st, _ := openStore(t)
	return NewReleaseSlots(ReleaseSlotsConfig{
		Store: st,
		Now:   slotOutsideRPCNow,
		NewTimer: func(time.Duration) CronTimer {
			return slotOutsideRPCNeverTimer{c: make(chan time.Time)}
		},
	})
}

// slotOutsideRPCClient serves slots over ServeWith on a sessions socket in a temporary directory, and returns a client
// that calls it. The server stops at cleanup.
func slotOutsideRPCClient(t *testing.T, slots ReleaseSlotCommands) enginev1connect.ReleaseSlotsClient {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeWith(ctx, ln, ServeConfig{ReleaseSlots: slots}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("ServeWith after cleanup: %v", err)
		}
	})
	return enginev1connect.NewReleaseSlotsClient(unixHTTPClient(t, path), "http://release-slots.invalid")
}

// slotOutsideRPCHold calls Hold for holder with limitSeconds on the test repository, and returns the response.
func slotOutsideRPCHold(
	t *testing.T, client enginev1connect.ReleaseSlotsClient, holder string, limitSeconds int64,
) *v1.ReleaseSlotsHoldResponse {
	t.Helper()
	resp, err := client.Hold(t.Context(), connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
		Repository: slotOutsideRPCRepo, Holder: holder, LimitSeconds: limitSeconds,
	}))
	if err != nil {
		t.Fatalf("Hold %s: %v", holder, err)
	}
	return resp.Msg
}

// slotOutsideRPCGive calls Give for holder on the test repository, and returns the response.
func slotOutsideRPCGive(
	t *testing.T, client enginev1connect.ReleaseSlotsClient, holder string,
) *v1.ReleaseSlotsGiveResponse {
	t.Helper()
	resp, err := client.Give(t.Context(), connect.NewRequest(&v1.ReleaseSlotsGiveRequest{
		Repository: slotOutsideRPCRepo, Holder: holder,
	}))
	if err != nil {
		t.Fatalf("Give %s: %v", holder, err)
	}
	return resp.Msg
}

// slotOutsideRPCShow calls Show on the test repository, and returns the response.
func slotOutsideRPCShow(t *testing.T, client enginev1connect.ReleaseSlotsClient) *v1.ReleaseSlotsShowResponse {
	t.Helper()
	resp, err := client.Show(t.Context(), connect.NewRequest(&v1.ReleaseSlotsShowRequest{
		Repository: slotOutsideRPCRepo,
	}))
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	return resp.Msg
}

// slotOutsideRPCEpicSlots is a ReleaseSlotCommands whose slot an epic holds. Hold and Show answer that epic. Only those
// two are called on it, so the embedded commands are never used.
type slotOutsideRPCEpicSlots struct {
	ReleaseSlotCommands
}

// slotOutsideRPCEpicHolder is the epic that holds the slot in the epic tests.
func slotOutsideRPCEpicHolder() store.ReleaseSlotHolder {
	return store.ReleaseSlotHolder{Kind: "epic", Epic: "epic-slot-7"}
}

// Hold answers that the epic holds the slot, so the requested label does not.
func (slotOutsideRPCEpicSlots) Hold(
	context.Context, string, string, time.Duration,
) (store.ReleaseSlotHolder, bool, error) {
	return slotOutsideRPCEpicHolder(), false, nil
}

// Show answers that the epic holds the slot.
func (slotOutsideRPCEpicSlots) Show(context.Context, string) (store.ReleaseSlotHolder, bool, error) {
	return slotOutsideRPCEpicHolder(), true, nil
}

// TestSlotOutsideRPCHoldShowGive checks the outside hold over the RPC: a label takes the slot, another label is
// refused behind it, and the holder gives it back so the slot is free.
func TestSlotOutsideRPCHoldShowGive(t *testing.T) {
	t.Parallel()
	client := slotOutsideRPCClient(t, slotOutsideRPCSlots(t))
	want := &v1.ReleaseSlotHolder{Kind: "outside", Label: "gambit:e1", Deadline: slotOutsideRPCNow().Unix() + 1800}

	held := slotOutsideRPCHold(t, client, "gambit:e1", 1800)
	if !held.GetHeld() || !proto.Equal(held.GetHolder(), want) {
		t.Errorf("Hold gambit:e1 = held %t, holder %v; want held true, holder %v",
			held.GetHeld(), held.GetHolder(), want)
	}

	shown := slotOutsideRPCShow(t, client)
	if !shown.GetHeld() || !proto.Equal(shown.GetHolder(), want) {
		t.Errorf("Show = held %t, holder %v; want held true, holder %v", shown.GetHeld(), shown.GetHolder(), want)
	}

	taken := slotOutsideRPCHold(t, client, "gambit:e2", 600)
	if taken.GetHeld() || taken.GetHolder().GetLabel() != "gambit:e1" {
		t.Errorf("Hold gambit:e2 = held %t, holder %v; want held false, holder label gambit:e1",
			taken.GetHeld(), taken.GetHolder())
	}

	if given := slotOutsideRPCGive(t, client, "gambit:e2"); given.GetFreed() {
		t.Errorf("Give gambit:e2 freed the slot; want freed false, since gambit:e2 holds nothing")
	}
	if given := slotOutsideRPCGive(t, client, "gambit:e1"); !given.GetFreed() {
		t.Errorf("Give gambit:e1 freed nothing; want freed true")
	}

	after := slotOutsideRPCShow(t, client)
	if after.GetHeld() || after.GetHolder() != nil {
		t.Errorf("Show after the give = held %t, holder %v; want held false and no holder",
			after.GetHeld(), after.GetHolder())
	}
}

// TestSlotOutsideRPCHoldNamesEpicHolder checks that a slot an epic holds is named over the RPC with the epic's kind and
// id, and no deadline.
func TestSlotOutsideRPCHoldNamesEpicHolder(t *testing.T) {
	t.Parallel()
	client := slotOutsideRPCClient(t, slotOutsideRPCEpicSlots{})
	want := &v1.ReleaseSlotHolder{Kind: "epic", Epic: "epic-slot-7", Deadline: 0}

	held := slotOutsideRPCHold(t, client, "gambit:e1", 1800)
	if held.GetHeld() || !proto.Equal(held.GetHolder(), want) {
		t.Errorf("Hold = held %t, holder %v; want held false, holder %v", held.GetHeld(), held.GetHolder(), want)
	}

	shown := slotOutsideRPCShow(t, client)
	if !shown.GetHeld() || !proto.Equal(shown.GetHolder(), want) {
		t.Errorf("Show = held %t, holder %v; want held true, holder %v", shown.GetHeld(), shown.GetHolder(), want)
	}
}

// TestSlotOutsideRPCHoldWaitStreams checks that a HoldWait behind another label streams that label, then streams
// its own hold once the slot is given back, and that the stream then ends without error.
func TestSlotOutsideRPCHoldWaitStreams(t *testing.T) {
	t.Parallel()
	client := slotOutsideRPCClient(t, slotOutsideRPCSlots(t))
	slotOutsideRPCHold(t, client, "gambit:e1", 1800)

	stream, err := client.HoldWait(t.Context(), connect.NewRequest(&v1.ReleaseSlotsHoldWaitRequest{
		Repository: slotOutsideRPCRepo, Holder: "gambit:e2", LimitSeconds: 600,
	}))
	if err != nil {
		t.Fatalf("HoldWait gambit:e2: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("HoldWait sent no first event: %v", stream.Err())
	}
	if waiting := stream.Msg().GetWaiting(); waiting.GetLabel() != "gambit:e1" {
		t.Fatalf("first event = %v; want a waiting event naming gambit:e1", stream.Msg())
	}

	if given := slotOutsideRPCGive(t, client, "gambit:e1"); !given.GetFreed() {
		t.Fatalf("Give gambit:e1 freed nothing; want freed true")
	}

	if !stream.Receive() {
		t.Fatalf("HoldWait sent no held event: %v", stream.Err())
	}
	want := &v1.ReleaseSlotHolder{Kind: "outside", Label: "gambit:e2", Deadline: slotOutsideRPCNow().Unix() + 600}
	if held := stream.Msg().GetHeld(); !proto.Equal(held, want) {
		t.Errorf("held event = %v; want %v", held, want)
	}

	if stream.Receive() {
		t.Errorf("HoldWait sent an event after the held event: %v", stream.Msg())
	}
	if err := stream.Err(); err != nil {
		t.Errorf("HoldWait stream ended with %v; want no error", err)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("HoldWait Close: %v", err)
	}
}

// slotOutsideRPCCounting wraps the commands it embeds and counts the Hold and Give calls that reach them.
type slotOutsideRPCCounting struct {
	ReleaseSlotCommands
	calls atomic.Int64
}

// Hold counts the call and passes it on.
func (c *slotOutsideRPCCounting) Hold(
	ctx context.Context, repository, label string, limit time.Duration,
) (store.ReleaseSlotHolder, bool, error) {
	c.calls.Add(1)
	return c.ReleaseSlotCommands.Hold(ctx, repository, label, limit)
}

// Give counts the call and passes it on.
func (c *slotOutsideRPCCounting) Give(ctx context.Context, repository, label string) (bool, error) {
	c.calls.Add(1)
	return c.ReleaseSlotCommands.Give(ctx, repository, label)
}

// TestSlotOutsideRPCRefusals checks that each request the service checks before the commands is refused as
// invalid_argument without the commands being called, and that a server with no release slots refuses Show as
// unavailable.
func TestSlotOutsideRPCRefusals(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	commands := &slotOutsideRPCCounting{ReleaseSlotCommands: slotOutsideRPCSlots(t)}
	client := slotOutsideRPCClient(t, commands)
	tests := []struct {
		name string
		call func() error
	}{
		{"hold limit zero", func() error {
			_, err := client.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
				Repository: slotOutsideRPCRepo, Holder: "gambit:e1", LimitSeconds: 0,
			}))
			return err
		}},
		{"hold limit above the maximum", func() error {
			_, err := client.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
				Repository: slotOutsideRPCRepo, Holder: "gambit:e1", LimitSeconds: 7201,
			}))
			return err
		}},
		{"hold relative repository", func() error {
			_, err := client.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
				Repository: "relative/repo", Holder: "gambit:e1", LimitSeconds: 600,
			}))
			return err
		}},
		{"hold empty holder", func() error {
			_, err := client.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
				Repository: slotOutsideRPCRepo, Holder: "", LimitSeconds: 600,
			}))
			return err
		}},
		{"give empty holder", func() error {
			_, err := client.Give(ctx, connect.NewRequest(&v1.ReleaseSlotsGiveRequest{
				Repository: slotOutsideRPCRepo, Holder: "",
			}))
			return err
		}},
	}
	for _, tc := range tests {
		if code := connect.CodeOf(tc.call()); code != connect.CodeInvalidArgument {
			t.Errorf("%s: code = %v; want %v", tc.name, code, connect.CodeInvalidArgument)
		}
	}
	if n := commands.calls.Load(); n != 0 {
		t.Errorf("the refused calls reached the commands %d times; want 0", n)
	}

	unavailable := slotOutsideRPCClient(t, nil)
	refused := []struct {
		name string
		call func() error
	}{
		{"Hold", func() error {
			_, err := unavailable.Hold(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldRequest{
				Repository: slotOutsideRPCRepo, Holder: "gambit:e1", LimitSeconds: 600,
			}))
			return err
		}},
		{"HoldWait", func() error {
			stream, err := unavailable.HoldWait(ctx, connect.NewRequest(&v1.ReleaseSlotsHoldWaitRequest{
				Repository: slotOutsideRPCRepo, Holder: "gambit:e1", LimitSeconds: 600,
			}))
			if err != nil {
				return err
			}
			for stream.Receive() {
				t.Errorf("HoldWait with no release slots sent an event: %v", stream.Msg())
			}
			return stream.Err()
		}},
		{"Give", func() error {
			_, err := unavailable.Give(ctx, connect.NewRequest(&v1.ReleaseSlotsGiveRequest{
				Repository: slotOutsideRPCRepo, Holder: "gambit:e1",
			}))
			return err
		}},
		{"Show", func() error {
			_, err := unavailable.Show(ctx, connect.NewRequest(&v1.ReleaseSlotsShowRequest{
				Repository: slotOutsideRPCRepo,
			}))
			return err
		}},
	}
	for _, tc := range refused {
		slotOutsideRPCUnavailable(t, tc.name, tc.call())
	}
}

// slotOutsideRPCUnavailable checks that err is the refusal a ReleaseSlots RPC gets with no release slots to serve: a
// connect error with CodeUnavailable and unavailableMessage. The message is checked too, since a service that panicked
// would close the connection, and the client would report that as unavailable with another message.
func slotOutsideRPCUnavailable(t *testing.T, rpc string, err error) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeUnavailable ||
		connectErr.Message() != unavailableMessage {
		t.Errorf("%s with no release slots: err = %v; want code %v with message %q",
			rpc, err, connect.CodeUnavailable, unavailableMessage)
	}
}
