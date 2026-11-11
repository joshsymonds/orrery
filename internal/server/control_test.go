package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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

// fakeStartedID is the session id the fake orreryd returns from Start.
const fakeStartedID = "synthetic-started-id"

// synthSessionID is a session id of the 32-digit hex form, named by Start, Say and the other Control calls.
const synthSessionID = "0123456789abcdef0123456789abcdef"

// shortControlTimeout bounds the forwards of the deadline test, short enough that the test ends quickly.
const shortControlTimeout = 50 * time.Millisecond

// controlSayPath is the Control path of Say, used by the body-limit test.
const controlSayPath = "/" + orreryv1connect.ControlName + "/Say"

// fakeEngine is a recording orreryd Engine. Each call records its request. It then blocks until block closes or the
// call's context ends, when block is set, and returns err when err is set.
type fakeEngine struct {
	mu    sync.Mutex
	calls []proto.Message
	err   error
	block chan struct{}
}

// recorded returns the requests the fake has received, in order.
func (f *fakeEngine) recorded() []proto.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// answer records msg and returns the error the call answers with.
func (f *fakeEngine) answer(ctx context.Context, msg proto.Message) error {
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

func (f *fakeEngine) Start(
	ctx context.Context,
	req *connect.Request[enginev1.StartRequest],
) (*connect.Response[enginev1.StartResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.StartResponse{Id: fakeStartedID}), nil
}

func (f *fakeEngine) Say(
	ctx context.Context,
	req *connect.Request[enginev1.SayRequest],
) (*connect.Response[enginev1.SayResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.SayResponse{}), nil
}

func (f *fakeEngine) Answer(
	ctx context.Context,
	req *connect.Request[enginev1.AnswerRequest],
) (*connect.Response[enginev1.AnswerResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.AnswerResponse{}), nil
}

func (f *fakeEngine) Interrupt(
	ctx context.Context,
	req *connect.Request[enginev1.InterruptRequest],
) (*connect.Response[enginev1.InterruptResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.InterruptResponse{}), nil
}

func (f *fakeEngine) Stop(
	ctx context.Context,
	req *connect.Request[enginev1.StopRequest],
) (*connect.Response[enginev1.StopResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.StopResponse{}), nil
}

func (f *fakeEngine) Seen(
	ctx context.Context,
	req *connect.Request[enginev1.SeenRequest],
) (*connect.Response[enginev1.SeenResponse], error) {
	if err := f.answer(ctx, req.Msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(&enginev1.SeenResponse{}), nil
}

// newFakeOrreryd serves fake as orreryd's Engine on a loopback test server and returns a client for it.
func newFakeOrreryd(t *testing.T, fake *fakeEngine) enginev1connect.EngineClient {
	t.Helper()
	_, handler := enginev1connect.NewEngineHandler(fake)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return enginev1connect.NewEngineClient(http.DefaultClient, srv.URL)
}

// newControlHandler returns the guarded handler that admits allowedLogin and forwards Control to engine. A nil
// engine answers unavailable, and a zero timeout is the default.
func newControlHandler(t *testing.T, engine enginev1connect.EngineClient, timeout time.Duration) http.Handler {
	t.Helper()
	handler, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		Engine:         engine,
		ControlTimeout: timeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return handler
}

// controlClient serves handler on an httptest server and returns a generated Control client whose requests carry
// allowedLogin and the client header.
func controlClient(t *testing.T, handler http.Handler) orreryv1connect.ControlClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := &http.Client{Transport: headerTransport{base: http.DefaultTransport, login: allowedLogin}}
	return orreryv1connect.NewControlClient(httpClient, srv.URL)
}

// controlCall is one Control call. call issues it, engine is the request the fake must receive, and reply is the
// response the call returns.
type controlCall struct {
	name   string
	call   func(context.Context, orreryv1connect.ControlClient) (proto.Message, error)
	engine proto.Message
	reply  proto.Message
}

// replyOf returns the message of a Control response, or the error of a failed call.
func replyOf[T any, M interface {
	*T
	proto.Message
}](resp *connect.Response[T], err error) (proto.Message, error) {
	if err != nil {
		return nil, err
	}
	return M(resp.Msg), nil
}

// controlCalls returns the six Control calls, each with every request field set.
func controlCalls() []controlCall {
	return []controlCall{
		{
			name: "Start",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Start(ctx, connect.NewRequest(&orreryv1.StartRequest{
					Dir: "/synthetic/dir", Prompt: "synthetic prompt", Model: "haiku", Id: synthSessionID,
				})))
			},
			engine: &enginev1.StartRequest{
				Dir: "/synthetic/dir", Prompt: "synthetic prompt", Model: "haiku", Id: synthSessionID,
			},
			reply: &orreryv1.StartResponse{Id: fakeStartedID},
		},
		{
			name: "Say",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Say(ctx, connect.NewRequest(&orreryv1.SayRequest{
					Id: synthSessionID, Text: "synthetic text", PromptId: "synthetic-prompt",
				})))
			},
			engine: &enginev1.SayRequest{Id: synthSessionID, Text: "synthetic text", PromptId: "synthetic-prompt"},
			reply:  &orreryv1.SayResponse{},
		},
		{
			name: "Answer",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Answer(ctx, connect.NewRequest(&orreryv1.AnswerRequest{
					Id: synthSessionID, RequestId: "synthetic-request", Answer: "allow",
				})))
			},
			engine: &enginev1.AnswerRequest{Id: synthSessionID, RequestId: "synthetic-request", Answer: "allow"},
			reply:  &orreryv1.AnswerResponse{},
		},
		{
			name: "Interrupt",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Interrupt(ctx, connect.NewRequest(&orreryv1.InterruptRequest{Id: synthSessionID})))
			},
			engine: &enginev1.InterruptRequest{Id: synthSessionID},
			reply:  &orreryv1.InterruptResponse{},
		},
		{
			name: "Stop",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Stop(ctx, connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID})))
			},
			engine: &enginev1.StopRequest{Id: synthSessionID},
			reply:  &orreryv1.StopResponse{},
		},
		{
			name: "Seen",
			call: func(ctx context.Context, c orreryv1connect.ControlClient) (proto.Message, error) {
				return replyOf(c.Seen(ctx, connect.NewRequest(&orreryv1.SeenRequest{Id: synthSessionID})))
			},
			engine: &enginev1.SeenRequest{Id: synthSessionID},
			reply:  &orreryv1.SeenResponse{},
		},
	}
}

// controlPaths returns the Connect path of each Control call.
func controlPaths() []string {
	return []string{
		orreryv1connect.ControlStartProcedure,
		orreryv1connect.ControlSayProcedure,
		orreryv1connect.ControlAnswerProcedure,
		orreryv1connect.ControlInterruptProcedure,
		orreryv1connect.ControlStopProcedure,
		orreryv1connect.ControlSeenProcedure,
	}
}

// assertConnectError fails the test unless err is a Connect error with code and message.
func assertConnectError(t *testing.T, err error, code connect.Code, message string) {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error = %v, want a Connect error with code %v", err, code)
	}
	if connectErr.Code() != code || connectErr.Message() != message {
		t.Fatalf("error = code %v message %q, want code %v message %q",
			connectErr.Code(), connectErr.Message(), code, message)
	}
}

func TestControlForwardsEveryCall(t *testing.T) {
	for _, tc := range controlCalls() {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeEngine{}
			control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

			got, err := tc.call(t.Context(), control)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !proto.Equal(got, tc.reply) {
				t.Fatalf("%s response = %v, want %v", tc.name, got, tc.reply)
			}
			calls := fake.recorded()
			if len(calls) != 1 || !proto.Equal(calls[0], tc.engine) {
				t.Fatalf("%s forwarded %v, want one request %v", tc.name, calls, tc.engine)
			}
		})
	}
}

func TestControlAnswersOrreryErrors(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{
			name:    "not_found",
			err:     connect.NewError(connect.CodeNotFound, errors.New("synthetic missing session")),
			code:    connect.CodeNotFound,
			message: "synthetic missing session",
		},
		{
			name:    "invalid_argument",
			err:     connect.NewError(connect.CodeInvalidArgument, errors.New("synthetic bad directory")),
			code:    connect.CodeInvalidArgument,
			message: "synthetic bad directory",
		},
		{
			name:    "resource_exhausted",
			err:     connect.NewError(connect.CodeResourceExhausted, errors.New("synthetic session cap")),
			code:    connect.CodeResourceExhausted,
			message: "synthetic session cap",
		},
		{
			name:    "unavailable",
			err:     connect.NewError(connect.CodeUnavailable, errors.New("synthetic orreryd detail")),
			code:    connect.CodeUnavailable,
			message: "session service unavailable",
		},
		{
			name:    "internal",
			err:     connect.NewError(connect.CodeInternal, errors.New("synthetic internal detail")),
			code:    connect.CodeInternal,
			message: "session command failed",
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
			fake := &fakeEngine{err: tc.err}
			control := controlClient(t, newControlHandler(t, newFakeOrreryd(t, fake), 0))

			_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
			assertConnectError(t, err, tc.code, tc.message)
		})
	}
}

func TestControlUnavailableWithoutEngine(t *testing.T) {
	control := controlClient(t, newControlHandler(t, nil, 0))
	for _, tc := range controlCalls() {
		_, err := tc.call(t.Context(), control)
		assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	}
}

func TestControlUnavailableWhenOrreryClosed(t *testing.T) {
	_, handler := enginev1connect.NewEngineHandler(&fakeEngine{})
	srv := httptest.NewServer(handler)
	engine := enginev1connect.NewEngineClient(http.DefaultClient, srv.URL)
	srv.Close()
	control := controlClient(t, newControlHandler(t, engine, 0))

	for _, tc := range controlCalls() {
		_, err := tc.call(t.Context(), control)
		assertConnectError(t, err, connect.CodeUnavailable, "session service unavailable")
	}
}

func TestControlDeadlineExceeded(t *testing.T) {
	fake := &fakeEngine{block: make(chan struct{})}
	engine := newFakeOrreryd(t, fake)
	t.Cleanup(func() { close(fake.block) })
	control := controlClient(t, newControlHandler(t, engine, shortControlTimeout))

	_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
}

func TestControlZeroTimeoutMeansTenSeconds(t *testing.T) {
	fake := &fakeEngine{block: make(chan struct{})}
	engine := newFakeOrreryd(t, fake)
	t.Cleanup(func() { close(fake.block) })
	control := controlClient(t, newControlHandler(t, engine, 0))

	start := time.Now()
	_, err := control.Stop(t.Context(), connect.NewRequest(&orreryv1.StopRequest{Id: synthSessionID}))
	assertConnectError(t, err, connect.CodeDeadlineExceeded, "session command timed out")
	if elapsed := time.Since(start); elapsed < 9*time.Second {
		t.Fatalf("deadline after %v, want about 10s", elapsed)
	}
}

func TestControlRefusesBodiesOverOneMiB(t *testing.T) {
	fake := &fakeEngine{}
	handler := newControlHandler(t, newFakeOrreryd(t, fake), 0)
	body, err := proto.Marshal(&orreryv1.SayRequest{Id: synthSessionID, Text: strings.Repeat("x", 2<<20)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := serve(t, handler, http.MethodPost, controlSayPath,
		connectHeader(allowedLogin, "application/proto", true), string(body))
	var fault struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &fault); err != nil {
		t.Fatalf("error body %q: %v", rec.Body.String(), err)
	}
	if fault.Code != "resource_exhausted" {
		t.Fatalf("oversize body: status %d code %q, want code resource_exhausted", rec.Code, fault.Code)
	}
	if calls := fake.recorded(); len(calls) != 0 {
		t.Fatalf("oversize body forwarded %d requests, want none", len(calls))
	}
}

func TestControlRequiresLoginAndClientHeaderOnEveryPath(t *testing.T) {
	handler := newControlHandler(t, newFakeOrreryd(t, &fakeEngine{}), 0)
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
	for _, path := range controlPaths() {
		for _, probe := range probes {
			rec := serve(t, handler, probe.method, path, probe.header, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s with %s: status = %d, want 403", probe.method, path, probe.name, rec.Code)
			}
		}
		rec := serve(t, handler, http.MethodGet, path, connectHeader(allowedLogin, "", true), "")
		if rec.Code == http.StatusOK {
			t.Fatalf("GET %s: status = 200, want it not served", path)
		}
	}
}

func TestControlRefusesNegativeTimeout(t *testing.T) {
	_, err := server.New(server.Config{
		Logins:         []string{allowedLogin},
		Tmux:           newTmuxClient(t),
		Push:           newPushStore(t),
		ControlTimeout: -time.Second,
	})
	if err == nil {
		t.Fatal("New with negative ControlTimeout: no error")
	}
}
