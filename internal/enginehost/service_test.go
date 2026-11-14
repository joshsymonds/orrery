package enginehost

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"github.com/joshsymonds/orrery/internal/engine/session"
	v1 "github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1"
	"github.com/joshsymonds/orrery/internal/gen/orrery/engine/v1/enginev1connect"
)

// The Manager must satisfy Commands, or orreryd cannot serve it.
var _ Commands = (*Manager)(nil)

// call is one Commands method invocation with the arguments it received.
type call struct {
	method    string
	dir       string
	prompt    string
	model     string
	id        string
	requestID string
	text      string
	promptID  string
	answer    string
}

// fakeCommands records every call and returns startID from Start and err from every method.
type fakeCommands struct {
	mu      sync.Mutex
	calls   []call
	startID string
	err     error
}

func (f *fakeCommands) record(c call) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return f.err
}

func (f *fakeCommands) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeCommands) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeCommands) Start(_ context.Context, req StartRequest) (string, error) {
	if err := f.record(call{
		method: "Start", dir: req.Dir, prompt: req.Prompt, model: req.Model, id: req.ID,
	}); err != nil {
		return "", err
	}
	return f.startID, nil
}

func (f *fakeCommands) Say(_ context.Context, req SayRequest) error {
	return f.record(call{method: "Say", id: req.ID, text: req.Text, promptID: req.PromptID})
}

func (f *fakeCommands) Answer(_ context.Context, id, requestID, answer string) error {
	return f.record(call{method: "Answer", id: id, requestID: requestID, answer: answer})
}

func (f *fakeCommands) Interrupt(_ context.Context, id string) error {
	return f.record(call{method: "Interrupt", id: id})
}

func (f *fakeCommands) Stop(_ context.Context, id string) error {
	return f.record(call{method: "Stop", id: id})
}

func (f *fakeCommands) Seen(_ context.Context, id string) error {
	return f.record(call{method: "Seen", id: id})
}

// startServer listens on a socket in a temp directory and serves commands on it, with no steps. The returned stop
// cancels the server's context and returns Serve's error. A test that does not call stop gets it through t.Cleanup.
func startServer(t *testing.T, commands Commands) (string, func() error) {
	t.Helper()
	return startServerWithSteps(t, commands, nil)
}

// startServerWithSteps is startServer with steps served beside commands.
func startServerWithSteps(t *testing.T, commands Commands, steps StepCommands) (string, func() error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "engine.sock")
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, commands, steps, nil, nil, nil) }()
	var once sync.Once
	var serveErr error
	stop := func() error {
		once.Do(func() {
			cancel()
			serveErr = <-done
		})
		return serveErr
	}
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Errorf("Serve after cleanup: %v", err)
		}
	})
	return path, stop
}

// unixHTTPClient returns an HTTP client that dials the socket at path.
func unixHTTPClient(t *testing.T, path string) *http.Client {
	t.Helper()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

// newClient returns an Engine client that dials the socket at path.
func newClient(t *testing.T, path string) enginev1connect.EngineClient {
	t.Helper()
	return enginev1connect.NewEngineClient(unixHTTPClient(t, path), "http://engine.invalid")
}

// callAll calls each of the six RPCs once with fixed arguments and returns each error in order: Start, Say, Answer,
// Interrupt, Stop, Seen.
func callAll(t *testing.T, client enginev1connect.EngineClient) []error {
	t.Helper()
	ctx := context.Background()
	_, startErr := client.Start(ctx, connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello"}))
	_, sayErr := client.Say(ctx, connect.NewRequest(&v1.SayRequest{Id: "s1", Text: "more"}))
	_, answerErr := client.Answer(
		ctx,
		connect.NewRequest(&v1.AnswerRequest{Id: "s1", RequestId: "r1", Answer: "allow"}),
	)
	_, interruptErr := client.Interrupt(ctx, connect.NewRequest(&v1.InterruptRequest{Id: "s1"}))
	_, stopErr := client.Stop(ctx, connect.NewRequest(&v1.StopRequest{Id: "s1"}))
	_, seenErr := client.Seen(ctx, connect.NewRequest(&v1.SeenRequest{Id: "s1"}))
	return []error{startErr, sayErr, answerErr, interruptErr, stopErr, seenErr}
}

func TestListenCreatesSocketModeZeroSixHundred(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "engine.sock")

	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Type() != fs.ModeSocket {
		t.Fatalf("socket file type = %v, want socket", info.Mode().Type())
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600", perm)
	}
}

func TestServeRoutesEachRPCToCommands(t *testing.T) {
	t.Parallel()
	fake := &fakeCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServer(t, fake)
	client := newClient(t, path)
	ctx := context.Background()

	started, err := client.Start(ctx, connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello"}))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := started.Msg.GetId(); got != fake.startID {
		t.Fatalf("Start id = %q, want %q", got, fake.startID)
	}
	if _, err := client.Say(ctx, connect.NewRequest(&v1.SayRequest{Id: "s1", Text: "more"})); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if _, err := client.Answer(
		ctx,
		connect.NewRequest(&v1.AnswerRequest{Id: "s1", RequestId: "r1", Answer: "allow"}),
	); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if _, err := client.Interrupt(ctx, connect.NewRequest(&v1.InterruptRequest{Id: "s1"})); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if _, err := client.Stop(ctx, connect.NewRequest(&v1.StopRequest{Id: "s1"})); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := client.Seen(ctx, connect.NewRequest(&v1.SeenRequest{Id: "s1"})); err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	want := []call{
		{method: "Start", dir: "/work", prompt: "hello"},
		{method: "Say", id: "s1", text: "more"},
		{method: "Answer", id: "s1", requestID: "r1", answer: "allow"},
		{method: "Interrupt", id: "s1"},
		{method: "Stop", id: "s1"},
		{method: "Seen", id: "s1"},
	}
	if got := fake.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestServeStartForwardsModel(t *testing.T) {
	t.Parallel()
	fake := &fakeCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServer(t, fake)
	client := newClient(t, path)

	if _, err := client.Start(
		context.Background(),
		connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello", Model: "haiku"}),
	); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	want := []call{{method: "Start", dir: "/work", prompt: "hello", model: "haiku"}}
	if got := fake.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestServeStartForwardsID(t *testing.T) {
	t.Parallel()
	fake := &fakeCommands{startID: "0123456789abcdef0123456789abcdef"}
	path, stop := startServer(t, fake)
	client := newClient(t, path)

	if _, err := client.Start(
		context.Background(),
		connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello", Id: "0123456789abcdef0123456789abcdef"}),
	); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	want := []call{{method: "Start", dir: "/work", prompt: "hello", id: "0123456789abcdef0123456789abcdef"}}
	if got := fake.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestServeSayForwardsPromptID(t *testing.T) {
	t.Parallel()
	fake := &fakeCommands{}
	path, stop := startServer(t, fake)
	client := newClient(t, path)

	if _, err := client.Say(
		context.Background(),
		connect.NewRequest(&v1.SayRequest{Id: "s1", Text: "more", PromptId: "p1"}),
	); err != nil {
		t.Fatalf("Say: %v", err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	want := []call{{method: "Say", id: "s1", text: "more", promptID: "p1"}}
	if got := fake.recorded(); !slices.Equal(got, want) {
		t.Fatalf("commands received %+v, want %+v", got, want)
	}
}

func TestServeMapsErrorsToConnectCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code connect.Code
		text string
	}{
		{
			name: "cap refusal carries its reason",
			err:  ErrCapReached,
			code: connect.CodeResourceExhausted,
			text: ErrCapReached.Error(),
		},
		{
			name: "wrapped cap refusal keeps the wrapping text",
			err:  wrapf(ErrCapReached, "start session"),
			code: connect.CodeResourceExhausted,
			text: "start session: " + ErrCapReached.Error(),
		},
		{
			name: "no such session",
			err:  wrapf(ErrNoSession, "stop session x"),
			code: connect.CodeNotFound,
			text: "stop session x: " + ErrNoSession.Error(),
		},
		{
			name: "no open request",
			err:  wrapf(session.ErrNoPendingRequest, "answer"),
			code: connect.CodeNotFound,
			text: "answer: " + session.ErrNoPendingRequest.Error(),
		},
		{
			name: "bad directory",
			err:  ErrBadDir,
			code: connect.CodeInvalidArgument,
			text: ErrBadDir.Error(),
		},
		{
			name: "empty prompt",
			err:  ErrEmptyPrompt,
			code: connect.CodeInvalidArgument,
			text: ErrEmptyPrompt.Error(),
		},
		{
			name: "bad model",
			err:  ErrBadModel,
			code: connect.CodeInvalidArgument,
			text: ErrBadModel.Error(),
		},
		{
			name: "bad id",
			err:  ErrBadID,
			code: connect.CodeInvalidArgument,
			text: ErrBadID.Error(),
		},
		{
			name: "bad answer",
			err:  ErrBadAnswer,
			code: connect.CodeInvalidArgument,
			text: ErrBadAnswer.Error(),
		},
		{
			name: "unverified answer",
			err:  session.ErrUnverifiedAnswer,
			code: connect.CodeInvalidArgument,
			text: session.ErrUnverifiedAnswer.Error(),
		},
		{
			name: "any other error is internal",
			err:  errors.New("disk on fire"),
			code: connect.CodeInternal,
			text: "disk on fire",
		},
	}
	fake := &fakeCommands{}
	path, _ := startServer(t, fake)
	client := newClient(t, path)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake.setErr(tc.err)
			for i, err := range callAll(t, client) {
				if err == nil {
					t.Fatalf("RPC %d returned no error, want %v", i, tc.code)
				}
				if got := connect.CodeOf(err); got != tc.code {
					t.Fatalf("RPC %d code = %v, want %v (error %v)", i, got, tc.code, err)
				}
				var connectErr *connect.Error
				if !errors.As(err, &connectErr) {
					t.Fatalf("RPC %d error %T is not a connect error", i, err)
				}
				if got := connectErr.Message(); got != tc.text {
					t.Fatalf("RPC %d message = %q, want %q", i, got, tc.text)
				}
			}
		})
	}
}

func TestServeRefusesStartPastCapWithReason(t *testing.T) {
	t.Parallel()
	path, _ := startServer(t, &fakeCommands{err: wrapf(ErrCapReached, "start session")})
	client := newClient(t, path)

	_, err := client.Start(context.Background(), connect.NewRequest(&v1.StartRequest{Dir: "/work", Prompt: "hello"}))
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("Start past cap code = %v, want %v (error %v)", got, connect.CodeResourceExhausted, err)
	}
	if err == nil || !strings.Contains(err.Error(), ErrCapReached.Error()) {
		t.Fatalf("Start past cap error = %v, want the cap reason %q", err, ErrCapReached.Error())
	}
}

func TestNilCommandsRefusesEveryRPCUnavailable(t *testing.T) {
	t.Parallel()
	path, _ := startServer(t, nil)
	client := newClient(t, path)

	for i, err := range callAll(t, client) {
		if got := connect.CodeOf(err); got != connect.CodeUnavailable {
			t.Fatalf("RPC %d code = %v, want %v (error %v)", i, got, connect.CodeUnavailable, err)
		}
		var connectErr *connect.Error
		if !errors.As(err, &connectErr) {
			t.Fatalf("RPC %d error %T is not a connect error", i, err)
		}
		if got := connectErr.Message(); got != "session store unavailable" {
			t.Fatalf("RPC %d message = %q, want %q", i, got, "session store unavailable")
		}
	}
}

func TestServeReturnsNilWhenContextEnds(t *testing.T) {
	t.Parallel()
	_, stop := startServer(t, &fakeCommands{})
	if err := stop(); err != nil {
		t.Fatalf("Serve after context ended = %v, want nil", err)
	}
}

// wrapf wraps err with a message prefix, as the Manager wraps its sentinels.
func wrapf(err error, prefix string) error {
	return fmt.Errorf("%s: %w", prefix, err)
}
