package server_test

import (
	"context"
	"errors"
	"net"
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

// fakeNoticesEngine is a recording orreryd Notices. Watch sends msgs in order, then returns end. Seen records its
// request and returns seenErr. When block is set, each call waits after its sends until its context ends or the test
// releases the fake, so the call stays open.
type fakeNoticesEngine struct {
	enginev1connect.UnimplementedNoticesHandler
	mu      sync.Mutex
	seen    []*enginev1.NoticesSeenRequest
	msgs    []*enginev1.NoticesWatchResponse
	end     error
	seenErr error
	block   bool
	release chan struct{}
}

func (f *fakeNoticesEngine) Watch(
	ctx context.Context,
	_ *connect.Request[enginev1.NoticesWatchRequest],
	stream *connect.ServerStream[enginev1.NoticesWatchResponse],
) error {
	for _, msg := range f.msgs {
		if err := stream.Send(msg); err != nil {
			return err
		}
	}
	f.hold(ctx)
	return f.end
}

func (f *fakeNoticesEngine) Seen(
	ctx context.Context,
	req *connect.Request[enginev1.NoticesSeenRequest],
) (*connect.Response[enginev1.NoticesSeenResponse], error) {
	f.mu.Lock()
	f.seen = append(f.seen, req.Msg)
	f.mu.Unlock()
	f.hold(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.seenErr != nil {
		return nil, f.seenErr
	}
	return connect.NewResponse(&enginev1.NoticesSeenResponse{}), nil
}

// recorded returns the Seen requests the fake has received, in order.
func (f *fakeNoticesEngine) recorded() []*enginev1.NoticesSeenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// hold returns once the call's context ends or the test releases the fake, when block is set.
func (f *fakeNoticesEngine) hold(ctx context.Context) {
	if f.block {
		select {
		case <-ctx.Done():
		case <-f.release:
		}
	}
}

// newFakeNoticesOrreryd serves fake as orreryd's Notices on a loopback test server and returns a client for it.
// Cleanup releases the fake's held calls before the server closes.
func newFakeNoticesOrreryd(t *testing.T, fake *fakeNoticesEngine) enginev1connect.NoticesClient {
	t.Helper()
	fake.release = make(chan struct{})
	_, handler := enginev1connect.NewNoticesHandler(fake)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(fake.release) })
	return enginev1connect.NewNoticesClient(http.DefaultClient, srv.URL)
}

// guardedNoticesHandler returns the guarded handler that admits allowedLogin and forwards Notices to notices, with
// shutdown as its shutdown signal. A nil notices answers unavailable, and a zero timeout is the default.
func guardedNoticesHandler(
	t *testing.T,
	notices enginev1connect.NoticesClient,
	timeout time.Duration,
	shutdown <-chan struct{},
) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		Notices:        notices,
		ControlTimeout: timeout,
		Shutdown:       shutdown,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// noticesClient serves handler on an httptest server and returns a generated Notices client whose requests carry
// allowedLogin and the client header.
func noticesClient(t *testing.T, handler http.Handler) orreryv1connect.NoticesClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return orreryv1connect.NewNoticesClient(guardedHTTPClient(), srv.URL)
}

// noticeKeyPair is one NoticeKey with a single field set, as the engine names it and as the page does.
type noticeKeyPair struct {
	engine *enginev1.NoticeKey
	page   *orreryv1.NoticeKey
}

// noticeKeyPairs returns one pair per NoticeKey field, so a relay or a forward that drops a field fails.
func noticeKeyPairs() []noticeKeyPair {
	return []noticeKeyPair{
		{&enginev1.NoticeKey{Pane: "%7"}, &orreryv1.NoticeKey{Pane: "%7"}},
		{&enginev1.NoticeKey{Session: "synthetic-session"}, &orreryv1.NoticeKey{Session: "synthetic-session"}},
		{&enginev1.NoticeKey{Epic: "synthetic-epic"}, &orreryv1.NoticeKey{Epic: "synthetic-epic"}},
		{
			&enginev1.NoticeKey{Limit: "synthetic-limit@1700000000"},
			&orreryv1.NoticeKey{Limit: "synthetic-limit@1700000000"},
		},
		{&enginev1.NoticeKey{Alert: "synthetic-alert"}, &orreryv1.NoticeKey{Alert: "synthetic-alert"}},
	}
}

// syntheticShown returns the shown message the engine sends for key, with every InAppNotice field set.
func syntheticShown(key *enginev1.NoticeKey) *enginev1.NoticesWatchResponse {
	return &enginev1.NoticesWatchResponse{Change: &enginev1.NoticesWatchResponse_Shown{Shown: &enginev1.InAppNotice{
		Key: key, State: "waiting", Title: "synthetic title", Body: "synthetic body",
	}}}
}

// openNotices starts Watch on notices and returns its stream, failing the test when the call errors. It has no
// deadline of its own.
func openNotices(
	t *testing.T,
	notices orreryv1connect.NoticesClient,
) *connect.ServerStreamForClient[orreryv1.NoticesWatchResponse] {
	t.Helper()
	stream, err := notices.Watch(t.Context(), connect.NewRequest(&orreryv1.NoticesWatchRequest{}))
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	return stream
}

// receiveNotices calls Watch on notices and receives until the stream ends. It returns the messages received and the
// error the call or the stream ended with. It has no deadline of its own.
func receiveNotices(t *testing.T, notices orreryv1connect.NoticesClient) ([]*orreryv1.NoticesWatchResponse, error) {
	t.Helper()
	ctx := t.Context()
	stream, err := notices.Watch(ctx, connect.NewRequest(&orreryv1.NoticesWatchRequest{}))
	if err != nil {
		return nil, err
	}
	var msgs []*orreryv1.NoticesWatchResponse
	for stream.Receive() {
		msgs = append(msgs, stream.Msg())
	}
	return msgs, stream.Err()
}

func TestNoticesWatchRelaysEveryField(t *testing.T) {
	t.Parallel()
	pairs := noticeKeyPairs()
	engineMsgs := make([]*enginev1.NoticesWatchResponse, 0, 2*len(pairs))
	want := make([]*orreryv1.NoticesWatchResponse, 0, 2*len(pairs))
	for _, pair := range pairs {
		engineMsgs = append(engineMsgs, syntheticShown(pair.engine))
		want = append(want, &orreryv1.NoticesWatchResponse{Change: &orreryv1.NoticesWatchResponse_Shown{
			Shown: &orreryv1.InAppNotice{
				Key: pair.page, State: "waiting", Title: "synthetic title", Body: "synthetic body",
			},
		}})
	}
	for _, pair := range pairs {
		engineMsgs = append(engineMsgs, &enginev1.NoticesWatchResponse{
			Change: &enginev1.NoticesWatchResponse_Ended{Ended: pair.engine},
		})
		want = append(want, &orreryv1.NoticesWatchResponse{
			Change: &orreryv1.NoticesWatchResponse_Ended{Ended: pair.page},
		})
	}
	fake := &fakeNoticesEngine{msgs: engineMsgs, block: true}
	notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, nil))

	stream := openNotices(t, notices)
	for i, wantMsg := range want {
		if !stream.Receive() {
			t.Fatalf("message %d: stream ended before it: %v", i, stream.Err())
		}
		if !proto.Equal(stream.Msg(), wantMsg) {
			t.Fatalf("message %d = %v, want %v", i, stream.Msg(), wantMsg)
		}
	}
}

func TestNoticesEngineEndEndsPageStreamUnavailable(t *testing.T) {
	t.Parallel()
	shown := syntheticShown(&enginev1.NoticeKey{Pane: "%7"})
	cases := []struct {
		name string
		msgs []*enginev1.NoticesWatchResponse
		end  error
	}{
		{name: "engine ends", msgs: []*enginev1.NoticesWatchResponse{shown}},
		{
			name: "engine fails",
			msgs: []*enginev1.NoticesWatchResponse{shown},
			end:  connect.NewError(connect.CodeInternal, errors.New("synthetic engine detail")),
		},
		{
			name: "engine fails first",
			end:  connect.NewError(connect.CodeInternal, errors.New("synthetic engine detail")),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &fakeNoticesEngine{msgs: tc.msgs, end: tc.end}
			notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, nil))

			msgs, err := receiveNotices(t, notices)
			if len(msgs) != len(tc.msgs) {
				t.Fatalf("received %d messages, want %d", len(msgs), len(tc.msgs))
			}
			assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
		})
	}
}

func TestNoticesWatchEndsAtShutdown(t *testing.T) {
	t.Parallel()
	shutdown := make(chan struct{})
	fake := &fakeNoticesEngine{
		msgs:  []*enginev1.NoticesWatchResponse{syntheticShown(&enginev1.NoticeKey{Pane: "%7"})},
		block: true,
	}
	notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, shutdown))

	stream := openNotices(t, notices)
	if !stream.Receive() {
		t.Fatalf("stream ended before the shown message: %v", stream.Err())
	}
	close(shutdown)
	if stream.Receive() {
		t.Fatal("stream sent a message after shutdown, want it to end")
	}
	wantShutdownError(t, stream.Err())
}

func TestNoticesStalledPageEndsAtSendTimeout(t *testing.T) {
	t.Parallel()
	msgs := make([]*enginev1.NoticesWatchResponse, 0, deadlineBigEvents)
	for i := range deadlineBigEvents {
		msgs = append(msgs, &enginev1.NoticesWatchResponse{Change: &enginev1.NoticesWatchResponse_Shown{
			Shown: &enginev1.InAppNotice{
				Key: &enginev1.NoticeKey{
					Pane: "%7",
				},
				State: "waiting",
				Title: "synthetic title",
				Body:  deadlineSummary(i),
			},
		}})
	}
	handler, err := server.New(server.Config{
		Logins:           []string{allowedLogin},
		Tmux:             newTmuxClient(t),
		Push:             newPushStore(t),
		Notices:          newFakeNoticesOrreryd(t, &fakeNoticesEngine{msgs: msgs}),
		EventSendTimeout: deadlineSendTimeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closed := make(chan string, deadlineConnCapacity)
	srv := &httptest.Server{
		Listener: smallBufferListener{Listener: listener},
		Config: &http.Server{Handler: handler, ConnState: func(conn net.Conn, state http.ConnState) {
			if state == http.StateClosed {
				closed <- conn.RemoteAddr().String()
			}
		}},
	}
	srv.Start()
	t.Cleanup(srv.Close)

	dialed := make(chan net.Conn, 1)
	stalledHTTP := &http.Client{Transport: headerTransport{base: stalledTransport(dialed), login: allowedLogin}}
	ctx := t.Context()
	stalled := orreryv1connect.NewNoticesClient(stalledHTTP, srv.URL)
	if _, err := stalled.Watch(ctx, connect.NewRequest(&orreryv1.NoticesWatchRequest{})); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	waitClosed(t, closed, (<-dialed).LocalAddr().String())
}

func TestNoticesSeenForwardsTheKey(t *testing.T) {
	t.Parallel()
	fake := &fakeNoticesEngine{}
	notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, nil))

	pairs := noticeKeyPairs()
	for _, pair := range pairs {
		if _, err := notices.Seen(
			t.Context(),
			connect.NewRequest(&orreryv1.NoticesSeenRequest{Key: pair.page}),
		); err != nil {
			t.Fatalf("Seen %v: %v", pair.page, err)
		}
	}
	calls := fake.recorded()
	if len(calls) != len(pairs) {
		t.Fatalf("Seen forwarded %d requests, want %d", len(calls), len(pairs))
	}
	for i, pair := range pairs {
		if !proto.Equal(calls[i].GetKey(), pair.engine) {
			t.Fatalf("Seen %d forwarded key %v, want %v", i, calls[i].GetKey(), pair.engine)
		}
	}
}

func TestNoticesSeenAnswersEngineErrorsAsControlDoes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "not_found",
			err:     connect.NewError(connect.CodeNotFound, errors.New("synthetic key not found")),
			code:    connect.CodeNotFound,
			message: "synthetic key not found",
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
			t.Parallel()
			fake := &fakeNoticesEngine{seenErr: tc.err}
			notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, nil))

			_, err := notices.Seen(t.Context(), connect.NewRequest(&orreryv1.NoticesSeenRequest{
				Key: &orreryv1.NoticeKey{Pane: "%7"},
			}))
			assertConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestNoticesSeenEndsAtControlTimeout(t *testing.T) {
	t.Parallel()
	fake := &fakeNoticesEngine{block: true}
	notices := noticesClient(t, guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), shortControlTimeout, nil))

	_, err := notices.Seen(t.Context(), connect.NewRequest(&orreryv1.NoticesSeenRequest{
		Key: &orreryv1.NoticeKey{Pane: "%7"},
	}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
}

func TestNoticesUnavailableWithoutEngine(t *testing.T) {
	t.Parallel()
	notices := noticesClient(t, guardedNoticesHandler(t, nil, 0, nil))

	msgs, err := receiveNotices(t, notices)
	if len(msgs) != 0 {
		t.Fatalf("Watch without an engine sent %d messages, want none", len(msgs))
	}
	assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	_, err = notices.Seen(t.Context(), connect.NewRequest(&orreryv1.NoticesSeenRequest{
		Key: &orreryv1.NoticeKey{Pane: "%7"},
	}))
	assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
}

func TestNoticesRequireLoginAndClientHeaderOnEveryPath(t *testing.T) {
	t.Parallel()
	fake := &fakeNoticesEngine{}
	handler := guardedNoticesHandler(t, newFakeNoticesOrreryd(t, fake), 0, nil)
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
	paths := []string{orreryv1connect.NoticesWatchProcedure, orreryv1connect.NoticesSeenProcedure}
	for _, path := range paths {
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
		t.Fatalf("refused and GET probes forwarded %d Seen requests, want none", len(calls))
	}
}
