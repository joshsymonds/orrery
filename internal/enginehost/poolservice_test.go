package enginehost

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	"github.com/joshsymonds/orrery/internal/grants"
	"github.com/joshsymonds/orrery/internal/pools"
)

// The Broker must satisfy PoolCommands, or orreryd cannot serve it.
var _ PoolCommands = (*grants.Broker)(nil)

// acquireWait bounds how long a test waits for one event from the pools service. It is a hang guard, never a timing
// assumption.
const acquireWait = 10 * time.Second

// fakePools is a PoolCommands that sends events in order, then returns err. With hold set it blocks after its events
// until its context ends, then reports that context's error on ended. Each Acquire puts its request on got, which
// holds one request. Status returns status. Hold and Free put their member on held and freed, which each hold one, and
// return memberErr.
type fakePools struct {
	events    []grants.Event
	err       error
	hold      bool
	status    grants.Status
	memberErr error
	got       chan grants.Request
	ended     chan error
	held      chan string
	freed     chan string
}

// newFakePools returns a fake that sends events and then returns err.
func newFakePools(err error, events ...grants.Event) *fakePools {
	return &fakePools{
		events: events, err: err,
		got: make(chan grants.Request, 1), ended: make(chan error, 1),
		held: make(chan string, 1), freed: make(chan string, 1),
	}
}

func (f *fakePools) Acquire(ctx context.Context, req grants.Request, send func(grants.Event) error) error {
	f.got <- req
	for _, event := range f.events {
		if err := send(event); err != nil {
			return err
		}
	}
	if f.hold {
		<-ctx.Done()
		f.ended <- ctx.Err()
		return ctx.Err()
	}
	return f.err
}

func (f *fakePools) Status() grants.Status {
	return f.status
}

func (f *fakePools) Hold(_ context.Context, member string) error {
	f.held <- member
	return f.memberErr
}

func (f *fakePools) Free(_ context.Context, member string) error {
	f.freed <- member
	return f.memberErr
}

// startPoolsServer serves commands on a sessions socket in a temporary directory and returns its path. The server stops
// at cleanup.
func startPoolsServer(t *testing.T, commands PoolCommands) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, nil, nil, nil, commands) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path
}

// newPoolsClient returns a Pools client that dials the socket at path.
func newPoolsClient(t *testing.T, path string) enginev1connect.PoolsClient {
	t.Helper()
	return enginev1connect.NewPoolsClient(unixHTTPClient(t, path), "http://pools.invalid")
}

// newAcquire is a new request for one member of the gpu pool, from project /work, client pid 4242, and the command
// make test.
func newAcquire() *v1.AcquireRequest {
	return &v1.AcquireRequest{
		Project:   "/work",
		ClientPid: 4242,
		Pools: []*v1.PoolDefinition{{
			Name: "gpu", Host: "box", Cap: 2, Members: []string{"a", "b"},
			Wake: []string{"wake-gpu", "now"}, WakeLimitSeconds: 30,
			Status: []string{"status-gpu"}, StatusLimitSeconds: 10,
			Admit: []string{"admit-gpu"}, AdmitLimitSeconds: 20,
		}},
		Needs:   []*v1.PoolNeed{{Pool: "gpu", Count: 1}},
		Command: []string{"make", "test"},
	}
}

// drain reads every response of an acquire stream until it ends, and returns them with the stream's error.
func drain(stream *connect.ServerStreamForClient[v1.AcquireResponse]) ([]*v1.AcquireResponse, error) {
	var msgs []*v1.AcquireResponse
	for stream.Receive() {
		msgs = append(msgs, stream.Msg())
	}
	return msgs, stream.Err()
}

// receiveWithin returns the next value on ch. It fails the test when none arrives within acquireWait, and what names
// the value in that failure.
func receiveWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(acquireWait):
		t.Fatalf("no %s within %v", what, acquireWait)
		var zero T
		return zero
	}
}

// wantRequest is the request a new acquire of newAcquire carries into the broker.
func wantRequest() grants.Request {
	return grants.Request{
		Project: "/work",
		Pools: []pools.Pool{{
			Name: "gpu", Host: "box", Cap: 2, Members: []string{"a", "b"},
			Wake: []string{"wake-gpu", "now"}, WakeLimit: 30 * time.Second,
			Status: []string{"status-gpu"}, StatusLimit: 10 * time.Second,
			Admit: []string{"admit-gpu"}, AdmitLimit: 20 * time.Second,
		}},
		Needs:     []grants.Need{{Pool: "gpu", Count: 1}},
		ClientPID: 4242,
		Command:   []string{"make", "test"},
	}
}

func TestAcquireMapsEveryFieldOfTheRequest(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil)
	client := newPoolsClient(t, startPoolsServer(t, fake))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err = drain(stream); err != nil {
		t.Fatalf("Acquire stream: %v", err)
	}
	got := receiveWithin(t, fake.got, "request reached the broker")
	if want := wantRequest(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request = %+v, want %+v", got, want)
	}
}

func TestAcquireReclaimPassesTheGrantID(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil)
	client := newPoolsClient(t, startPoolsServer(t, fake))
	msg := newAcquire()
	msg.GrantId = "g-7"

	stream, err := client.Acquire(t.Context(), connect.NewRequest(msg))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err = drain(stream); err != nil {
		t.Fatalf("Acquire stream: %v", err)
	}
	if got := receiveWithin(t, fake.got, "request reached the broker").GrantID; got != "g-7" {
		t.Fatalf("GrantID = %q, want g-7", got)
	}
}

func TestAcquireSendsQueuedThenGranted(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil,
		grants.Event{Position: 2},
		grants.Event{Grant: &grants.Grant{ID: "g1", Members: []grants.Member{
			{Pool: "gpu", Name: "a"}, {Pool: "gpu", Name: "b"}, {Pool: "cpu", Name: "c"},
		}}},
	)
	client := newPoolsClient(t, startPoolsServer(t, fake))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	msgs, err := drain(stream)
	if err != nil {
		t.Fatalf("Acquire stream: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d responses, want 2", len(msgs))
	}
	wantQueued := &v1.AcquireResponse{Event: &v1.AcquireResponse_Queued{Queued: &v1.AcquireQueued{Position: 2}}}
	if !proto.Equal(msgs[0], wantQueued) {
		t.Fatalf("first response = %v, want %v", msgs[0], wantQueued)
	}
	wantGranted := &v1.AcquireResponse{Event: &v1.AcquireResponse_Granted{Granted: &v1.AcquireGranted{
		GrantId: "g1",
		Pools: []*v1.PoolMembers{
			{Pool: "gpu", Members: []string{"a", "b"}},
			{Pool: "cpu", Members: []string{"c"}},
		},
	}}}
	if !proto.Equal(msgs[1], wantGranted) {
		t.Fatalf("second response = %v, want %v", msgs[1], wantGranted)
	}
}

func TestAcquireSendsAStopEventAsStop(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil,
		grants.Event{Grant: &grants.Grant{ID: "g1", Members: []grants.Member{{Pool: "gpu", Name: "a"}}}},
		grants.Event{Stop: []grants.Member{{Pool: "gpu", Name: "a"}}},
	)
	fake.hold = true
	client := newPoolsClient(t, startPoolsServer(t, fake))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("no granted response: %v", stream.Err())
	}
	if !stream.Receive() {
		t.Fatalf("no stop response: %v", stream.Err())
	}
	want := &v1.AcquireResponse{Event: &v1.AcquireResponse_Stop{Stop: &v1.AcquireStop{Members: []string{"a"}}}}
	if !proto.Equal(stream.Msg(), want) {
		t.Fatalf("stop response = %v, want %v", stream.Msg(), want)
	}
	if err = stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
}

func TestAcquireRefusesAQueuePositionOutsideInt32AsInternal(t *testing.T) {
	for _, position := range []int{math.MaxInt32 + 1, math.MinInt32 - 1} {
		t.Run(fmt.Sprint(position), func(t *testing.T) {
			fake := newFakePools(nil, grants.Event{Position: position})
			client := newPoolsClient(t, startPoolsServer(t, fake))

			stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			msgs, err := drain(stream)
			if connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("Acquire code = %v (err %v), want internal", connect.CodeOf(err), err)
			}
			if len(msgs) != 0 {
				t.Fatalf("got %d responses, want none: a position outside int32 must not be sent", len(msgs))
			}
		})
	}
}

func TestAcquireRefusesWithTheCodeOfItsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code connect.Code
	}{
		{"refused", fmt.Errorf("pool gpu: %w", grants.ErrRefused), connect.CodeFailedPrecondition},
		{"bad need", fmt.Errorf("need of gpu: %w", grants.ErrBadNeed), connect.CodeInvalidArgument},
		{"no grant", fmt.Errorf("grant g-1: %w", grants.ErrNoGrant), connect.CodeNotFound},
		{"closed", grants.ErrClosed, connect.CodeUnavailable},
		{"other", errors.New("store write failed"), connect.CodeInternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakePools(tc.err)
			client := newPoolsClient(t, startPoolsServer(t, fake))

			stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if _, err = drain(stream); connect.CodeOf(err) != tc.code {
				t.Fatalf("Acquire code = %v (err %v), want %v", connect.CodeOf(err), err, tc.code)
			}
		})
	}
}

func TestAcquireWithoutPoolsIsUnavailable(t *testing.T) {
	t.Parallel()
	client := newPoolsClient(t, startPoolsServer(t, nil))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err = drain(stream); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Acquire code = %v (err %v), want unavailable", connect.CodeOf(err), err)
	}
}

func TestAcquireRefusesAClientPIDBelowOne(t *testing.T) {
	for _, pid := range []int32{0, -1} {
		t.Run(fmt.Sprint(pid), func(t *testing.T) {
			fake := newFakePools(nil)
			client := newPoolsClient(t, startPoolsServer(t, fake))
			msg := newAcquire()
			msg.ClientPid = pid

			stream, err := client.Acquire(t.Context(), connect.NewRequest(msg))
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if _, err = drain(stream); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("Acquire code = %v (err %v), want invalid argument", connect.CodeOf(err), err)
			}
			if len(fake.got) != 0 {
				t.Fatal("Acquire reached the broker for a client pid below 1")
			}
		})
	}
}

func TestAcquireRefusesALimitOutsideOneSecondToTheDurationRange(t *testing.T) {
	for _, seconds := range []int64{0, -5, maxLimitSeconds + 1} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			fake := newFakePools(nil)
			client := newPoolsClient(t, startPoolsServer(t, fake))
			msg := newAcquire()
			msg.Pools[0].AdmitLimitSeconds = seconds

			stream, err := client.Acquire(t.Context(), connect.NewRequest(msg))
			if err != nil {
				t.Fatalf("Acquire: %v", err)
			}
			if _, err = drain(stream); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("Acquire code = %v (err %v), want invalid argument", connect.CodeOf(err), err)
			}
			if len(fake.got) != 0 {
				t.Fatal("Acquire reached the broker for a limit outside its range")
			}
		})
	}
}

func TestAcquireEndsTheGrantWhenTheClientClosesItsStream(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil, grants.Event{Grant: &grants.Grant{ID: "g1", Members: []grants.Member{
		{Pool: "gpu", Name: "a"},
	}}})
	fake.hold = true
	client := newPoolsClient(t, startPoolsServer(t, fake))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !stream.Receive() {
		t.Fatalf("no granted response: %v", stream.Err())
	}
	if err = stream.Close(); err != nil {
		t.Fatalf("close stream: %v", err)
	}
	select {
	case ctxErr := <-fake.ended:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Fatalf("Acquire ctx ended with %v, want context.Canceled", ctxErr)
		}
	case <-time.After(acquireWait):
		t.Fatalf("Acquire ctx did not end within %v of the client closing its stream", acquireWait)
	}
}

func TestStatusReportsThePoolsAndTheHolds(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil)
	fake.status = grants.Status{
		Pools: []grants.PoolStatus{{
			Name: "gpu", Cap: 2,
			Members: []grants.MemberStatus{
				{Name: "a", State: grants.Awake, Holder: &grants.Holder{
					GrantID: "g1", Project: "/work", ClientPID: 4242, Command: []string{"make", "test"},
					Since: time.Unix(1700000100, 0), Stopping: true,
				}, HeldSince: time.Unix(1700000200, 0)},
				{Name: "b", State: grants.Asleep},
			},
			Waiters: []grants.WaiterStatus{{
				Position: 1, Project: "/other", Command: []string{"sleep", "9"}, Count: 1,
				Since: time.Unix(1700000300, 0),
			}},
		}},
		Holds: []grants.HoldStatus{{Member: "x", Since: time.Unix(1700000400, 0)}},
	}
	client := newPoolsClient(t, startPoolsServer(t, fake))

	resp, err := client.Status(t.Context(), connect.NewRequest(&v1.PoolsStatusRequest{}))
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	want := &v1.PoolsStatusResponse{
		Pools: []*v1.PoolStatus{{
			Name: "gpu", Cap: 2,
			Members: []*v1.PoolMemberStatus{
				{Name: "a", State: "awake", Holder: &v1.PoolHolder{
					GrantId: "g1", Project: "/work", ClientPid: 4242, Command: []string{"make", "test"},
					Since: 1700000100, Stopping: true,
				}, HeldSince: 1700000200},
				{Name: "b", State: "asleep"},
			},
			Waiters: []*v1.PoolWaiter{{
				Position: 1, Project: "/other", Command: []string{"sleep", "9"}, Count: 1, Since: 1700000300,
			}},
		}},
		Holds: []*v1.PoolHold{{Member: "x", Since: 1700000400}},
	}
	if !proto.Equal(resp.Msg, want) {
		t.Fatalf("Status = %v, want %v", resp.Msg, want)
	}
}

func TestHoldReachesThePoolsWithTheMember(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil)
	client := newPoolsClient(t, startPoolsServer(t, fake))

	if _, err := client.Hold(t.Context(), connect.NewRequest(&v1.PoolsHoldRequest{Member: "a"})); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if got := receiveWithin(t, fake.held, "hold reached the pools"); got != "a" {
		t.Fatalf("held member = %q, want a", got)
	}
}

func TestFreeReachesThePoolsWithTheMember(t *testing.T) {
	t.Parallel()
	fake := newFakePools(nil)
	client := newPoolsClient(t, startPoolsServer(t, fake))

	if _, err := client.Free(t.Context(), connect.NewRequest(&v1.PoolsFreeRequest{Member: "a"})); err != nil {
		t.Fatalf("Free: %v", err)
	}
	if got := receiveWithin(t, fake.freed, "free reached the pools"); got != "a" {
		t.Fatalf("freed member = %q, want a", got)
	}
}

func TestHoldAndFreeMapTheirErrorsToCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code connect.Code
	}{
		{"bad member", fmt.Errorf("hold member: %w", grants.ErrBadMember), connect.CodeInvalidArgument},
		{"not held", fmt.Errorf("free member: %w", grants.ErrNotHeld), connect.CodeNotFound},
		{"closed", grants.ErrClosed, connect.CodeUnavailable},
		{"other", errors.New("store write failed"), connect.CodeInternal},
	}
	rpcs := []struct {
		name string
		call func(context.Context, enginev1connect.PoolsClient) error
	}{
		{"Hold", func(ctx context.Context, client enginev1connect.PoolsClient) error {
			_, err := client.Hold(ctx, connect.NewRequest(&v1.PoolsHoldRequest{Member: "a"}))
			return err
		}},
		{"Free", func(ctx context.Context, client enginev1connect.PoolsClient) error {
			_, err := client.Free(ctx, connect.NewRequest(&v1.PoolsFreeRequest{Member: "a"}))
			return err
		}},
	}
	for _, rpc := range rpcs {
		for _, tc := range tests {
			t.Run(rpc.name+" "+tc.name, func(t *testing.T) {
				fake := newFakePools(nil)
				fake.memberErr = tc.err
				client := newPoolsClient(t, startPoolsServer(t, fake))

				if err := rpc.call(t.Context(), client); connect.CodeOf(err) != tc.code {
					t.Fatalf("%s code = %v (err %v), want %v", rpc.name, connect.CodeOf(err), err, tc.code)
				}
			})
		}
	}
}

func TestStatusHoldAndFreeWithoutPoolsAreUnavailable(t *testing.T) {
	t.Parallel()
	client := newPoolsClient(t, startPoolsServer(t, nil))

	_, err := client.Status(t.Context(), connect.NewRequest(&v1.PoolsStatusRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Status code = %v (err %v), want unavailable", connect.CodeOf(err), err)
	}
	_, err = client.Hold(t.Context(), connect.NewRequest(&v1.PoolsHoldRequest{Member: "a"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Hold code = %v (err %v), want unavailable", connect.CodeOf(err), err)
	}
	_, err = client.Free(t.Context(), connect.NewRequest(&v1.PoolsFreeRequest{Member: "a"}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("Free code = %v (err %v), want unavailable", connect.CodeOf(err), err)
	}
}
