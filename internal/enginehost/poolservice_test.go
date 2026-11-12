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
// holds one request.
type fakePools struct {
	events []grants.Event
	err    error
	hold   bool
	got    chan grants.Request
	ended  chan error
}

// newFakePools returns a fake that sends events and then returns err.
func newFakePools(err error, events ...grants.Event) *fakePools {
	return &fakePools{events: events, err: err, got: make(chan grants.Request, 1), ended: make(chan error, 1)}
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
	go func() { done <- Serve(ctx, ln, nil, nil, commands) }()
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

// newAcquire is a new request for one member of the gpu pool, from project /work and client pid 4242.
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
		Needs: []*v1.PoolNeed{{Pool: "gpu", Count: 1}},
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

// receiveRequest returns the request the fake's Acquire received. It fails the test when none arrives within
// acquireWait.
func receiveRequest(t *testing.T, fake *fakePools) grants.Request {
	t.Helper()
	select {
	case req := <-fake.got:
		return req
	case <-time.After(acquireWait):
		t.Fatalf("no request reached the broker within %v", acquireWait)
		return grants.Request{}
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
	}
}

func TestAcquireMapsEveryFieldOfTheRequest(t *testing.T) {
	fake := newFakePools(nil)
	client := newPoolsClient(t, startPoolsServer(t, fake))

	stream, err := client.Acquire(t.Context(), connect.NewRequest(newAcquire()))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err = drain(stream); err != nil {
		t.Fatalf("Acquire stream: %v", err)
	}
	got := receiveRequest(t, fake)
	if want := wantRequest(); !reflect.DeepEqual(got, want) {
		t.Fatalf("request = %+v, want %+v", got, want)
	}
}

func TestAcquireReclaimPassesTheGrantID(t *testing.T) {
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
	if got := receiveRequest(t, fake).GrantID; got != "g-7" {
		t.Fatalf("GrantID = %q, want g-7", got)
	}
}

func TestAcquireSendsQueuedThenGranted(t *testing.T) {
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
