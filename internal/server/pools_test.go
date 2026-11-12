package server_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	enginev1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
	orreryv1 "github.com/joshsymonds/orrery/internal/gen/orrery/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/v1/orreryv1connect"
	"github.com/joshsymonds/orrery/internal/server"
)

// syntheticMember is the member name the Hold and Free tests name, and the owner hold the status fixture carries.
const syntheticMember = "synthetic-member-c"

// fakePoolsEngine is a recording orreryd Pools. Each call records its request, then blocks until block closes or the
// call's context ends, when block is set, and returns err when err is set. Status returns status.
type fakePoolsEngine struct {
	enginev1connect.UnimplementedPoolsHandler
	mu     sync.Mutex
	calls  []proto.Message
	status *enginev1.PoolsStatusResponse
	err    error
	block  chan struct{}
}

// recorded returns the requests the fake has received, in order.
func (f *fakePoolsEngine) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// answer records msg and returns the error the call answers with.
func (f *fakePoolsEngine) answer(ctx context.Context, msg proto.Message) error {
	f.mu.Lock()
	f.calls = append(f.calls, msg)
	f.mu.Unlock()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.err
}

func (f *fakePoolsEngine) Status(
	ctx context.Context,
	req *connect.Request[enginev1.PoolsStatusRequest],
) (*connect.Response[enginev1.PoolsStatusResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(f.status), nil
}

func (f *fakePoolsEngine) Hold(
	ctx context.Context,
	req *connect.Request[enginev1.PoolsHoldRequest],
) (*connect.Response[enginev1.PoolsHoldResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.PoolsHoldResponse{}), nil
}

func (f *fakePoolsEngine) Free(
	ctx context.Context,
	req *connect.Request[enginev1.PoolsFreeRequest],
) (*connect.Response[enginev1.PoolsFreeResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.PoolsFreeResponse{}), nil
}

// newFakePoolsOrreryd serves fake as orreryd's Pools on a loopback test server and returns a client for it.
func newFakePoolsOrreryd(t *testing.T, fake *fakePoolsEngine) enginev1connect.PoolsClient {
	t.Helper()
	_, handler := enginev1connect.NewPoolsHandler(fake)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return enginev1connect.NewPoolsClient(http.DefaultClient, srv.URL)
}

// guardedPoolsHandler returns the guarded handler that admits allowedLogin and forwards Pools to pools. A nil pools
// answers unavailable, and a zero timeout is the default.
func guardedPoolsHandler(t *testing.T, pools enginev1connect.PoolsClient, timeout time.Duration) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		Pools:          pools,
		ControlTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// poolsClient serves handler on an httptest server and returns a generated Pools client whose requests carry
// allowedLogin and the client header.
func poolsClient(t *testing.T, handler http.Handler) orreryv1connect.PoolsClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewPoolsClient(httpClient, srv.URL)
}

// syntheticPoolsStatus is the status orreryd answers Status with: a pool with a held member, a waiting request and an
// owner hold. Every field is set, so the mapping must carry each one across.
func syntheticPoolsStatus() *enginev1.PoolsStatusResponse {
	return &enginev1.PoolsStatusResponse{
		Pools: []*enginev1.PoolStatus{{
			Name: "synthetic-pool",
			Cap:  2,
			Members: []*enginev1.PoolMemberStatus{
				{
					Name:      "synthetic-member-a",
					State:     "admitted",
					HeldSince: 1700000001,
					Holder: &enginev1.PoolHolder{
						GrantId:   "synthetic-grant",
						Project:   "/synthetic/project",
						ClientPid: 4242,
						Command:   []string{"synthetic", "command"},
						Since:     1700000000,
						Stopping:  true,
					},
				},
				{Name: "synthetic-member-b", State: "asleep"},
			},
			Waiters: []*enginev1.PoolWaiter{{
				Position: 1,
				Project:  "/synthetic/waiting",
				Command:  []string{"synthetic", "waiting"},
				Count:    1,
				Since:    1700000002,
			}},
		}},
		Holds: []*enginev1.PoolHold{{Member: syntheticMember, Since: 1700000003}},
	}
}

// wantPoolsStatus is the phone answer to syntheticPoolsStatus: the same fields, with the holder's grant_id and
// client_pid left out.
func wantPoolsStatus() *orreryv1.PoolsStatusResponse {
	return &orreryv1.PoolsStatusResponse{
		Pools: []*orreryv1.PoolStatus{{
			Name: "synthetic-pool",
			Cap:  2,
			Members: []*orreryv1.PoolMemberStatus{
				{
					Name:      "synthetic-member-a",
					State:     "admitted",
					HeldSince: 1700000001,
					Holder: &orreryv1.PoolHolder{
						Project:  "/synthetic/project",
						Command:  []string{"synthetic", "command"},
						Since:    1700000000,
						Stopping: true,
					},
				},
				{Name: "synthetic-member-b", State: "asleep"},
			},
			Waiters: []*orreryv1.PoolWaiter{{
				Position: 1,
				Project:  "/synthetic/waiting",
				Command:  []string{"synthetic", "waiting"},
				Count:    1,
				Since:    1700000002,
			}},
		}},
		Holds: []*orreryv1.PoolHold{{Member: syntheticMember, Since: 1700000003}},
	}
}

func TestPoolsStatusForwardsAndMapsEveryField(t *testing.T) {
	fake := &fakePoolsEngine{status: syntheticPoolsStatus()}
	pools := poolsClient(t, guardedPoolsHandler(t, newFakePoolsOrreryd(t, fake), 0))

	resp, err := pools.Status(t.Context(), connect.NewRequest(&orreryv1.PoolsStatusRequest{}))
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if want := wantPoolsStatus(); !proto.Equal(resp.Msg, want) {
		t.Fatalf("Status response = %v, want %v", resp.Msg, want)
	}
	calls := fake.recorded()
	if len(calls) != 1 || !proto.Equal(calls[0], &enginev1.PoolsStatusRequest{}) {
		t.Fatalf("Status forwarded %v, want one empty request", calls)
	}
}

func TestPoolsHoldAndFreeForwardTheMember(t *testing.T) {
	fake := &fakePoolsEngine{}
	pools := poolsClient(t, guardedPoolsHandler(t, newFakePoolsOrreryd(t, fake), 0))

	hold := connect.NewRequest(&orreryv1.PoolsHoldRequest{Member: syntheticMember})
	if _, err := pools.Hold(t.Context(), hold); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	free := connect.NewRequest(&orreryv1.PoolsFreeRequest{Member: syntheticMember})
	if _, err := pools.Free(t.Context(), free); err != nil {
		t.Fatalf("Free: %v", err)
	}
	calls := fake.recorded()
	if len(calls) != 2 ||
		!proto.Equal(calls[0], &enginev1.PoolsHoldRequest{Member: syntheticMember}) ||
		!proto.Equal(calls[1], &enginev1.PoolsFreeRequest{Member: syntheticMember}) {
		t.Fatalf("Hold and Free forwarded %v, want a hold then a free of %s", calls, syntheticMember)
	}
}

func TestPoolsAnswersEngineErrorsAsControlDoes(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "not_found",
			err:     connect.NewError(connect.CodeNotFound, errors.New("synthetic member not held")),
			code:    connect.CodeNotFound,
			message: "synthetic member not held",
		},
		{
			name:    "unavailable",
			err:     connect.NewError(connect.CodeUnavailable, errors.New("synthetic orreryd detail")),
			code:    connect.CodeUnavailable,
			message: "session service unavailable",
		},
		{
			name:    "unknown",
			err:     errors.New("synthetic store detail"),
			code:    connect.CodeInternal,
			message: "session command failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakePoolsEngine{err: tc.err}
			pools := poolsClient(t, guardedPoolsHandler(t, newFakePoolsOrreryd(t, fake), 0))

			free := connect.NewRequest(&orreryv1.PoolsFreeRequest{Member: syntheticMember})
			_, err := pools.Free(t.Context(), free)
			assertConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestPoolsUnavailableWithoutEngine(t *testing.T) {
	pools := poolsClient(t, guardedPoolsHandler(t, nil, 0))

	_, err := pools.Status(t.Context(), connect.NewRequest(&orreryv1.PoolsStatusRequest{}))
	assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	_, err = pools.Hold(t.Context(), connect.NewRequest(&orreryv1.PoolsHoldRequest{Member: syntheticMember}))
	assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	_, err = pools.Free(t.Context(), connect.NewRequest(&orreryv1.PoolsFreeRequest{Member: syntheticMember}))
	assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
}

func TestPoolsDeadlineExceeded(t *testing.T) {
	fake := &fakePoolsEngine{block: make(chan struct{})}
	engine := newFakePoolsOrreryd(t, fake)
	t.Cleanup(func() { close(fake.block) })
	pools := poolsClient(t, guardedPoolsHandler(t, engine, shortControlTimeout))

	_, err := pools.Status(t.Context(), connect.NewRequest(&orreryv1.PoolsStatusRequest{}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
}

// poolsPaths returns the Connect path of each Pools call the guard covers.
func poolsPaths() []string {
	return []string{
		orreryv1connect.PoolsStatusProcedure,
		orreryv1connect.PoolsHoldProcedure,
		orreryv1connect.PoolsFreeProcedure,
	}
}

func TestPoolsRequireLoginAndClientHeaderOnEveryPath(t *testing.T) {
	fake := &fakePoolsEngine{}
	handler := guardedPoolsHandler(t, newFakePoolsOrreryd(t, fake), 0)
	probes := []struct {
		name   string
		method string
		header http.Header
	}{
		{"POST no login", http.MethodPost, connectHeader("", "application/proto", true)},
		{"POST stranger login", http.MethodPost, connectHeader(otherLogin, "application/proto", true)},
		{"POST non-Connect content type", http.MethodPost, connectHeader(allowedLogin, "text/plain", true)},
		{"POST no client header", http.MethodPost, connectHeader(allowedLogin, "application/proto", false)},
		{"GET no client header", http.MethodGet, connectHeader(allowedLogin, "", false)},
		{"PUT no client header", http.MethodPut, connectHeader(allowedLogin, "application/proto", false)},
		{"DELETE no client header", http.MethodDelete, connectHeader(allowedLogin, "application/proto", false)},
	}
	for _, path := range poolsPaths() {
		for _, probe := range probes {
			rec := serve(t, handler, probe.method, path, probe.header, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: status = %d, want 403", probe.method, path, probe.name, rec.Code)
			}
		}
		getPath := path + "?connect=v1&encoding=json&message=" + url.QueryEscape("{}")
		rec := serve(t, handler, http.MethodGet, getPath, connectHeader(allowedLogin, "", true), "")
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s with login and client header: status = %d, want 405", path, rec.Code)
		}
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("refused and GET probes forwarded %d requests, want none", len(calls))
	}
}
